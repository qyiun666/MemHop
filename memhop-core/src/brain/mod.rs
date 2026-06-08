use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;

use crate::activation::{ActivationConfig, ActivationManager};
use crate::domain_graph::L3DomainGraph;
use crate::encoder::Encoder;
use crate::engram::{RawDocument, Topic};
use crate::error::{MemHopError, Result};
use crate::hypergraph::L1Hypergraph;
use crate::index::MemHopHnswConfig;
use crate::lmdb::{L0Env, L1Env, L2Env, L3Env, L4Env, L5Env};
use crate::profile::L0ProfileStore;
use crate::raw_archive::L4RawArchive;
use crate::session::SessionManager;
use crate::topic_graph::L2TopicGraph;
use crate::types::DreamConfig;
use crate::types::{
    ActivatedTopicInfo, BatchReport, BrainConfig, ConsolidateReport, CrystallizeL3Report,
    CrystallizeL3Request, CrystallizeReport, Emotion, EmotionalDimension, EmotionalFeedback,
    EmotionRecallRequest, L0Profile, L3PathInfo, ProceduralCrystal, RecallRequest,
    RecallResponse, ShelfDomain, ShelfMeta, StoreBatch,
};

/// MemHop v0.18.3 Brain — 6 层仿人脑记忆架构顶层 API。
/// 所有 LMDB 环境延迟打开（Lazy Open），首次访问时打开。
pub struct Brain {
    pub config: BrainConfig,
    pub l0_env: Option<L0Env>,
    pub l1_env: Option<L1Env>,
    pub l2_env: Option<L2Env>,
    pub l3_env: Option<L3Env>,
    pub l4_env: Option<L4Env>,
    pub l5_env: Option<L5Env>,
    pub l0: Option<L0ProfileStore>,
    pub l1: Option<L1Hypergraph>,
    pub l2: Option<L2TopicGraph>,
    pub l3: Option<L3DomainGraph>,
    pub l4: Option<L4RawArchive>,
    pub session_mgr: SessionManager,
    pub encoder: Arc<Box<dyn Encoder>>,
    /// v0.23.0: 记忆激活管理器 (Active/Latent/Dormant)
    pub activation: Option<ActivationManager>,
    /// v0.24.0: 情感内存索引 (Emotion → Node IDs)，启动时自动重建
    pub emotion_index: HashMap<Emotion, Vec<String>>,
}

/// 预热单层结果。
#[derive(Debug, Clone, serde::Serialize)]
pub struct PrewarmLayerResult {
    pub nodes: u64,
    pub duration_ms: u64,
}

impl Brain {
    /// 打开 Brain，仅保存 config 和 encoder，不打开任何 LMDB 环境。
    /// 所有 LMDB 环境在首次访问时延迟打开。
    pub fn open(config: BrainConfig, encoder: Arc<Box<dyn Encoder>>) -> Result<Self> {
        let path = Path::new(&config.brains_dir);
        std::fs::create_dir_all(path)
            .map_err(|e| MemHopError::Storage(format!("create brains dir: {}", e)))?;

        let session_mgr = SessionManager::new();

        Ok(Brain {
            config,
            l0_env: None,
            l1_env: None,
            l2_env: None,
            l3_env: None,
            l4_env: None,
            l5_env: None,
            l0: None,
            l1: None,
            l2: None,
            l3: None,
            l4: None,
            session_mgr,
            encoder,
            activation: None,
            emotion_index: HashMap::new(),
        })
    }

    // ── Path helper ──
    fn brain_path(&self) -> &Path {
        Path::new(&self.config.brains_dir)
    }

    // ── Lazy open: L0 (env + L0ProfileStore) ──
    pub(crate) fn ensure_l0_env(&mut self) -> Result<()> {
        if self.l0_env.is_some() {
            return Ok(());
        }
        let path = self.brain_path().join("l0_profile.db");
        self.l0_env = Some(L0Env::open(&path)?);
        self.l0 = Some(L0ProfileStore::new());
        Ok(())
    }

    // ── Lazy open: L1 ──
    pub(crate) fn ensure_l1_env(&mut self) -> Result<()> {
        if self.l1_env.is_some() {
            return Ok(());
        }
        let path = self.brain_path().join("l1_hypergraph.db");
        self.l1_env = Some(L1Env::open(&path)?);
        Ok(())
    }

    /// v0.22.0: 按节点规模自适应 HNSW 配置（for_scale 代替 default）。
    pub(crate) fn ensure_l1(&mut self) -> Result<()> {
        if self.l1.is_some() {
            return Ok(());
        }
        let _timer = std::time::Instant::now();
        self.ensure_l1_env()?;
        let encoder_dim = self.encoder.dim();
        let l1_env = self.l1_env.as_ref().unwrap();
        // 先计数，再按规模配置 HNSW
        let txn = l1_env
            .env
            .read_txn()
            ?;
        let node_count = l1_env.nodes.len(&txn).unwrap_or(0) as usize;
        drop(txn);
        let config = MemHopHnswConfig::for_scale(node_count);
        let connectivity = config.connectivity;
        let mut l1 = L1Hypergraph::with_dim_and_config(encoder_dim, config);
        let mut wtxn = l1_env
            .env
            .write_txn()
            ?;
        l1.rebuild_bm25(l1_env, &mut wtxn)?;
        wtxn.commit()
            ?;
        l1.rebuild_vector_index(l1_env)
            .map_err(|e| MemHopError::Internal(format!("rebuild L1 vector index: {}", e)))?;
        let elapsed = _timer.elapsed();
        if elapsed.as_millis() > 100 {
            eprintln!("[memhop] WARNING: L1 first open took {}ms ({} nodes, connectivity={})", elapsed.as_millis(), node_count, connectivity);
        }
        // v0.23.0: 初始化 ActivationManager
        if self.activation.is_none() {
            let config = ActivationConfig::default();
            self.activation = Some(ActivationManager::new(config));
        }
        self.l1 = Some(l1);

        // v0.24.0: 首次加载时从 LMDB 重建情感索引
        if self.emotion_index.is_empty() {
            self.rebuild_emotion_index()?;
        }

        Ok(())
    }

    /// v0.24.0: 从 LMDB 全量重建情感索引（max_scan = 10,000）。
    fn rebuild_emotion_index(&mut self) -> Result<()> {
        const MAX_SCAN: usize = 10_000;
        let l1_env = match self.l1_env.as_ref() {
            Some(env) => env,
            None => return Ok(()),
        };
        let txn = l1_env
            .env
            .read_txn()
            ?;
        let mut scanned = 0usize;
        if let Ok(iter) = l1_env.nodes.iter(&txn) {
            for item in iter {
                if scanned >= MAX_SCAN {
                    break;
                }
                if let Ok((_key, bytes)) = item
                    && let Ok(node) =
                        bincode::deserialize::<crate::engram::KnowledgeNode>(bytes)
                {
                    self.emotion_index
                        .entry(node.emotion)
                        .or_default()
                        .push(node.id);
                }
                scanned += 1;
            }
        }
        Ok(())
    }

    // ── Lazy open: L2 ──
    pub(crate) fn ensure_l2_env(&mut self) -> Result<()> {
        if self.l2_env.is_some() {
            return Ok(());
        }
        let path = self.brain_path().join("l2_topics.db");
        self.l2_env = Some(L2Env::open(&path)?);
        Ok(())
    }

    /// v0.22.0: 按规模自适应 HNSW 配置。
    pub(crate) fn ensure_l2(&mut self) -> Result<()> {
        if self.l2.is_some() {
            return Ok(());
        }
        let _timer = std::time::Instant::now();
        self.ensure_l2_env()?;
        let encoder_dim = self.encoder.dim();
        let l2_env = self.l2_env.as_ref().unwrap();
        // 先计数 topic，再按规模配置 HNSW
        let txn = l2_env
            .env
            .read_txn()
            ?;
        let topic_count = l2_env.topics.len(&txn).unwrap_or(0) as usize;
        drop(txn);
        let config = MemHopHnswConfig::for_scale(topic_count);
        let mut l2 = L2TopicGraph::with_dim_and_config(encoder_dim, config);
        l2.rebuild_topic_vectors(l2_env)
            .map_err(|e| MemHopError::Internal(format!("rebuild L2 topic vectors: {}", e)))?;
        let elapsed = _timer.elapsed();
        if elapsed.as_millis() > 100 {
            eprintln!("[memhop] WARNING: L2 first open took {}ms ({} topics)", elapsed.as_millis(), topic_count);
        }
        self.l2 = Some(l2);
        Ok(())
    }

    // ── Lazy open: L3 ──
    pub(crate) fn ensure_l3_env(&mut self) -> Result<()> {
        if self.l3_env.is_some() {
            return Ok(());
        }
        let path = self.brain_path().join("l3_domains.db");
        self.l3_env = Some(L3Env::open(&path)?);
        Ok(())
    }

    /// v0.22.0: 按规模自适应 HNSW + L3 延迟加载（仅显式请求时激活）。
    pub(crate) fn ensure_l3(&mut self) -> Result<()> {
        if self.l3.is_some() {
            return Ok(());
        }
        let _timer = std::time::Instant::now();
        self.ensure_l3_env()?;
        let encoder_dim = self.encoder.dim();
        let l3_env = self.l3_env.as_ref().unwrap();
        // 先计数，再按规模配置 HNSW
        let txn = l3_env
            .env
            .read_txn()
            ?;
        let node_count = l3_env.domain_nodes.len(&txn).unwrap_or(0) as usize;
        drop(txn);
        let config = MemHopHnswConfig::for_scale(node_count);
        let connectivity = config.connectivity;
        let mut l3 = L3DomainGraph::with_dim_and_config(encoder_dim, config);
        l3.rebuild_vector_index(l3_env)
            .map_err(|e| MemHopError::Internal(format!("rebuild L3 vector index: {}", e)))?;
        l3.rebuild_bm25(l3_env)
            .map_err(|e| MemHopError::Internal(format!("rebuild L3 BM25 index: {}", e)))?;
        let elapsed = _timer.elapsed();
        if elapsed.as_millis() > 100 {
            eprintln!("[memhop] WARNING: L3 first open took {}ms ({} nodes, connectivity={})", elapsed.as_millis(), node_count, connectivity);
        }
        self.l3 = Some(l3);
        Ok(())
    }

    // ── Lazy open: L4 ──
    pub(crate) fn ensure_l4_env(&mut self) -> Result<()> {
        if self.l4_env.is_some() {
            return Ok(());
        }
        let path = self.brain_path().join("l4_raw.db");
        self.l4_env = Some(L4Env::open(&path)?);
        Ok(())
    }

    /// v0.22.0: L4 仅打开环境，不构建向量索引（HNSW 已移除）。
    pub(crate) fn ensure_l4(&mut self) -> Result<()> {
        if self.l4.is_some() {
            return Ok(());
        }
        self.ensure_l4_env()?;
        self.l4 = Some(L4RawArchive::new());
        Ok(())
    }

    // ── Lazy open: L5 (env only, no data struct) ──
    pub(crate) fn ensure_l5_env(&mut self) -> Result<()> {
        if self.l5_env.is_some() {
            return Ok(());
        }
        let path = self.brain_path().join("l5_procedural.db");
        self.l5_env = Some(L5Env::open(&path)?);
        Ok(())
    }

    // ── Public API ────────────────────────────────────────────

    pub fn batch_store(&mut self, batch: StoreBatch) -> Result<BatchReport> {
        crate::batch_store::execute(self, batch)
    }

    pub fn recall(&mut self, req: &RecallRequest) -> Result<RecallResponse> {
        // 清理过期激活
        self.session_mgr.purge_expired();
        let mut resp = crate::recall::enhanced_recall(self, req)?;

        // v0.18.3: 根据查询词匹配程序性晶体推荐
        if !req.query.is_empty() {
            resp.recommended_crystals = self.get_crystals_by_keyword(&req.query)?;
        }

        // 附带 L0 Profile
        self.ensure_l0_env()?;
        let l0_env = self.l0_env.as_ref().unwrap();
        let txn = l0_env
            .env
            .read_txn()
            ?;
        let l0 = self.l0.as_ref().unwrap();
        resp.l0_profile = l0.get_profile(&txn, l0_env)?;
        // 附带已激活 Topic 列表
        resp.activated_topics = self.session_mgr.get_active_list();
        Ok(resp)
    }

    pub fn consolidate(&mut self) -> Result<ConsolidateReport> {
        let config = DreamConfig::default();
        crate::dream::run(self, &config)
    }

    /// v0.18.3: 运行程序性结晶管线。
    pub fn procedural_crystallize(&mut self) -> Result<CrystallizeReport> {
        crate::procedural::crystallize(self)
    }

    pub fn organize_node(&mut self, node_id: &str) -> Result<()> {
        crate::organize::organize_node(self, node_id)
    }

    pub fn config(&self) -> &BrainConfig {
        &self.config
    }

    /// v0.23.1: 按 session_id 获取 L4 原文文档
    pub fn get_l4_by_session(&mut self, session_id: &str) -> Result<Vec<RawDocument>> {
        self.ensure_l4()?;
        let l4_env = self.l4_env.as_ref().unwrap();
        let l4 = self.l4.as_ref().unwrap();
        let txn = l4_env
            .env
            .read_txn()
            ?;
        l4.get_by_session(&txn, l4_env, session_id)
    }

    /// v0.23.1: 按 topic_id 获取关联的 L4 原文文档
    /// 先从 L2 获取 topic 的 node_ids，再从 L4 获取对应的文档
    pub fn get_l4_by_topic(&mut self, topic_id: &str) -> Result<Vec<RawDocument>> {
        // 1. 从 L2 获取 topic 的 node_ids
        self.ensure_l2()?;
        let l2 = self.l2.as_ref().unwrap();
        let l2_env = self.l2_env.as_ref().unwrap();
        let txn = l2_env
            .env
            .read_txn()
            ?;
        let topic = l2.get_topic_by_id(&txn, l2_env, topic_id)?;
        drop(txn);

        let topic = match topic {
            Some(t) => t,
            None => return Ok(Vec::new()),
        };

        // 2. 如果 topic 有 doc_ids，从 L4 获取
        if topic.doc_ids.is_empty() {
            return Ok(Vec::new());
        }

        self.ensure_l4()?;
        let l4_env = self.l4_env.as_ref().unwrap();
        let l4 = self.l4.as_ref().unwrap();
        let txn = l4_env
            .env
            .read_txn()
            ?;
        l4.get_by_ids(&txn, l4_env, &topic.doc_ids)
    }

    /// 获取 L0 角色画像
    pub fn get_l0_profile(&mut self) -> Result<Option<L0Profile>> {
        self.ensure_l0_env()?;
        let l0_env = self.l0_env.as_ref().unwrap();
        let txn = l0_env
            .env
            .read_txn()
            ?;
        let l0 = self.l0.as_ref().unwrap();
        l0.get_profile(&txn, l0_env)
    }

    /// 设置 L0 角色画像（身份字段：catid, role_name, role, position, traits）
    /// catid 首次设置后不可修改
    pub fn set_l0_profile(
        &mut self,
        catid: Option<String>,
        role_name: Option<String>,
        role: Option<String>,
        position: Option<String>,
        traits: std::collections::HashMap<String, String>,
    ) -> Result<()> {
        self.ensure_l0_env()?;
        let l0_env = self.l0_env.as_ref().unwrap();
        let env = l0_env.env.clone();
        let mut wtxn = env
            .write_txn()
            ?;
        // 读取现有 profile 或创建新的
        let l0 = self.l0.as_ref().unwrap();
        let mut profile = l0
            .get_profile(&wtxn, l0_env)?
            .unwrap_or_default();
        // catid 只在首次设置时保存，之后不可修改
        if profile.catid.is_none() && let Some(id) = catid {
            profile.catid = Some(id);
        }
        if let Some(name) = role_name {
            profile.role_name = Some(name);
        }
        if let Some(r) = role {
            profile.role = Some(r);
        }
        if let Some(p) = position {
            profile.position = Some(p);
        }
        if !traits.is_empty() {
            profile.traits.extend(traits);
        }
        profile.version += 1;
        profile.updated_at = chrono::Utc::now().timestamp_millis();
        l0.update_profile(&mut wtxn, l0_env, &profile)?;
        wtxn.commit()
            ?;
        Ok(())
    }

    /// v0.17.0: LLM 直接写入完整的 L0 角色画像（替代 old set_l0_profile 的逐个字段）。
    /// catid 首次设置后不可修改
    pub fn set_l0(
        &mut self,
        catid: Option<String>,
        role_name: Option<String>,
        personality: Vec<String>,
        values: Vec<String>,
        worldview: Vec<String>,
        traits: std::collections::HashMap<String, String>,
    ) -> Result<()> {
        self.ensure_l0_env()?;
        let l0_env = self.l0_env.as_ref().unwrap();
        let env = l0_env.env.clone();
        let mut wtxn = env
            .write_txn()
            ?;
        let l0 = self.l0.as_ref().unwrap();
        let mut profile = l0
            .get_profile(&wtxn, l0_env)?
            .unwrap_or_default();
        // catid 只在首次设置时保存，之后不可修改
        if profile.catid.is_none() && let Some(id) = catid {
            profile.catid = Some(id);
        }
        if let Some(name) = role_name {
            profile.role_name = Some(name);
        }
        if !personality.is_empty() {
            profile.personality = personality;
        }
        if !values.is_empty() {
            profile.values = values;
        }
        if !worldview.is_empty() {
            profile.worldview = worldview;
        }
        if !traits.is_empty() {
            profile.traits = traits;
        }
        profile.updated_at = chrono::Utc::now().timestamp_millis();
        profile.version += 1;
        l0.update_profile(&mut wtxn, l0_env, &profile)?;
        wtxn.commit()
            ?;
        Ok(())
    }

    /// v0.17.0: LLM plan compression 后更新 topic 的摘要/关键词/扩展元数据。
    pub fn update_topic(
        &mut self,
        topic_id: &str,
        summary: Option<String>,
        keywords: Option<Vec<String>>,
        extended_meta: Option<std::collections::HashMap<String, String>>,
    ) -> Result<()> {
        self.ensure_l2_env()?;
        let l2_env = self.l2_env.as_ref().unwrap();
        let env = l2_env.env.clone();
        let mut wtxn = env
            .write_txn()
            ?;
        let key = format!("topic:{}:meta", topic_id);
        match l2_env
            .topics
            .get(&wtxn, &key)
            ?
        {
            Some(bytes) => {
                if let Ok(mut topic) = bincode::deserialize::<crate::engram::Topic>(bytes) {
                    if let Some(ref s) = summary {
                        topic.summary = Some(s.clone());
                    }
                    if let Some(ref kw) = keywords {
                        topic.keywords = kw.clone();
                    }
                    if let Some(ref meta) = extended_meta {
                        topic.extended_meta = meta.clone();
                    }
                    topic.updated_at = chrono::Utc::now().timestamp_millis();
                    let new_bytes = bincode::serialize(&topic)
                        ?;
                    l2_env
                        .topics
                        .put(&mut wtxn, &key, &new_bytes)
                        ?;
                }
            }
            None => {
                return Err(MemHopError::NotFound(format!(
                    "topic {} not found",
                    topic_id
                )));
            }
        }
        wtxn.commit()
            ?;
        Ok(())
    }

    /// 获取已激活 Topic 列表
    pub fn get_activated_topics(&mut self) -> Vec<ActivatedTopicInfo> {
        self.session_mgr.purge_expired();
        self.session_mgr.get_active_list()
    }

    /// v0.22.0: 获取 L4 文档计数（LMDB 直接统计，无 HNSW 依赖）。
    pub fn l4_doc_count(&self) -> usize {
        self.l4_env.as_ref().map(|env| {
            if let Ok(txn) = env.env.read_txn() {
                env.docs.len(&txn).unwrap_or(0) as usize
            } else {
                0
            }
        }).unwrap_or(0)
    }

    /// 获取 L4 原文
    pub fn get_l4_raw(&mut self, doc_id: &str) -> Result<Option<RawDocument>> {
        self.ensure_l4_env()?;
        let l4_env = self.l4_env.as_ref().unwrap();
        let txn = l4_env
            .env
            .read_txn()
            ?;
        match l4_env
            .docs
            .get(&txn, doc_id)
            ?
        {
            Some(bytes) => Ok(Some(
                bincode::deserialize(bytes)?,
            )),
            None => Ok(None),
        }
    }

    /// 列出 L3 领域路径
    pub fn list_l3_paths(&mut self) -> Result<Vec<L3PathInfo>> {
        self.ensure_l3_env()?;
        let l3_env = self.l3_env.as_ref().unwrap();
        let txn = l3_env
            .env
            .read_txn()
            ?;
        let mut paths = Vec::new();
        if let Ok(iter) = l3_env.domain_meta.iter(&txn) {
            for item in iter {
                if let Ok((_key, bytes)) = item
                    && let Ok(meta) = serde_json::from_slice::<serde_json::Value>(bytes)
                {
                    paths.push(L3PathInfo {
                        domain_id: meta["id"].as_str().unwrap_or("").to_string(),
                        name: meta["name"].as_str().unwrap_or("").to_string(),
                        node_count: meta["node_count"].as_u64().unwrap_or(0),
                        mounted_at: meta["created_at"].as_i64().unwrap_or(0),
                    });
                }
            }
        }
        Ok(paths)
    }

    /// 列出所有 L2 Topic
    pub fn list_topics(&mut self) -> Result<Vec<Topic>> {
        self.ensure_l2_env()?;
        let l2_env = self.l2_env.as_ref().unwrap();
        let txn = l2_env
            .env
            .read_txn()
            ?;
        let mut topics = Vec::new();
        if let Ok(iter) = l2_env.topics.iter(&txn) {
            for (key, bytes) in iter.flatten() {
                if !key.starts_with("topic:") || !key.ends_with(":meta") {
                    continue;
                }
                match bincode::deserialize::<Topic>(bytes) {
                    Ok(t) => topics.push(t),
                    Err(e) => eprintln!(
                        "[brain] list_topics: deserialize error for key '{}': {}",
                        key, e
                    ),
                }
            }
        }
        Ok(topics)
    }

    /// v0.20.0: 按 topic_id 查询单个话题。
    /// 返回 Ok(None) 表示 topic 不存在（非错误），与 get_crystal 语义一致。
    pub fn get_topic(&mut self, topic_id: &str) -> Result<Option<Topic>> {
        self.ensure_l2_env()?;
        let l2_env = self.l2_env.as_ref().unwrap();
        let txn = l2_env
            .env
            .read_txn()
            ?;
        let key = format!("topic:{}:meta", topic_id);
        match l2_env
            .topics
            .get(&txn, &key)
            ?
        {
            Some(bytes) => Ok(Some(
                bincode::deserialize(bytes)
                    ?,
            )),
            None => Ok(None),
        }
    }

    /// 带过滤的再搜索（排除已选结果）
    pub fn re_search(&mut self, req: &RecallRequest) -> Result<RecallResponse> {
        self.session_mgr.purge_expired();
        let mut resp = crate::recall::enhanced_recall(self, req)?;
        self.ensure_l0_env()?;
        let l0_env = self.l0_env.as_ref().unwrap();
        let txn = l0_env
            .env
            .read_txn()
            ?;
        let l0 = self.l0.as_ref().unwrap();
        resp.l0_profile = l0.get_profile(&txn, l0_env)?;
        resp.activated_topics = self.session_mgr.get_active_list();
        Ok(resp)
    }

    // ── L5 程序性结晶 CRUD ─────────────────────────────────

    /// v0.18.3: 存储一个程序性晶体。
    pub fn store_crystal(&mut self, crystal: &ProceduralCrystal) -> Result<()> {
        self.ensure_l5_env()?;
        let l5_env = self.l5_env.as_ref().unwrap();
        let env = l5_env.env.clone();
        let mut wtxn = env
            .write_txn()
            ?;
        let bytes = bincode::serialize(crystal)
            ?;
        l5_env
            .crystals
            .put(&mut wtxn, &crystal.id, &bytes)
            ?;
        wtxn.commit()
            ?;
        Ok(())
    }

    /// v0.18.3: 按 ID 获取程序性晶体。
    pub fn get_crystal(&mut self, id: &str) -> Result<Option<ProceduralCrystal>> {
        self.ensure_l5_env()?;
        let l5_env = self.l5_env.as_ref().unwrap();
        let txn = l5_env
            .env
            .read_txn()
            ?;
        match l5_env
            .crystals
            .get(&txn, id)
            ?
        {
            Some(bytes) => Ok(Some(
                bincode::deserialize(bytes)
                    ?,
            )),
            None => Ok(None),
        }
    }

    /// v0.18.3: 列出所有程序性晶体。
    pub fn list_crystals(&mut self) -> Result<Vec<ProceduralCrystal>> {
        self.ensure_l5_env()?;
        let l5_env = self.l5_env.as_ref().unwrap();
        let txn = l5_env
            .env
            .read_txn()
            ?;
        let mut crystals = Vec::new();
        if let Ok(iter) = l5_env.crystals.iter(&txn) {
            for (_key, bytes) in iter.flatten() {
                match bincode::deserialize::<ProceduralCrystal>(bytes) {
                    Ok(c) => crystals.push(c),
                    Err(e) => eprintln!("[brain] list_crystals: deserialize error: {}", e),
                }
            }
        }
        Ok(crystals)
    }

    /// v0.18.3: 按关键词过滤程序性晶体（子串匹配 trigger_keywords）。
    pub fn get_crystals_by_keyword(&mut self, keyword: &str) -> Result<Vec<ProceduralCrystal>> {
        self.ensure_l5_env()?;
        let l5_env = self.l5_env.as_ref().unwrap();
        let txn = l5_env
            .env
            .read_txn()
            ?;
        let mut matched = Vec::new();
        if let Ok(iter) = l5_env.crystals.iter(&txn) {
            for (_key, bytes) in iter.flatten() {
                match bincode::deserialize::<ProceduralCrystal>(bytes) {
                    Ok(c) => {
                        if c.trigger_keywords
                            .iter()
                            .any(|kw| kw.contains(keyword) || keyword.contains(kw))
                        {
                            matched.push(c);
                        }
                    }
                    Err(e) => {
                        eprintln!(
                            "[brain] get_crystals_by_keyword: deserialize error: {}",
                            e
                        )
                    }
                }
            }
        }
        Ok(matched)
    }

    /// v0.18.3: 延迟索引重建 + 预热 — 主动加载并重建指定层的索引。
    /// 返回各层节点数和耗时。
    pub fn prewarm(&mut self, layers: &[String]) -> Result<HashMap<String, PrewarmLayerResult>> {
        let mut results = HashMap::new();

        for layer in layers {
            match layer.as_str() {
                "L1" => {
                    let start = std::time::Instant::now();
                    self.ensure_l1()?;
                    let nodes = self.l1.as_ref().map(|l1| l1.node_count()).unwrap_or(0);
                    results.insert("L1".to_string(), PrewarmLayerResult {
                        nodes,
                        duration_ms: start.elapsed().as_millis() as u64,
                    });
                }
                "L2" => {
                    let start = std::time::Instant::now();
                    self.ensure_l2()?;
                    let nodes = self.count_l2_topics();
                    results.insert("L2".to_string(), PrewarmLayerResult {
                        nodes,
                        duration_ms: start.elapsed().as_millis() as u64,
                    });
                }
                "L3" => {
                    let start = std::time::Instant::now();
                    self.ensure_l3()?;
                    let nodes = self.count_l3_nodes();
                    results.insert("L3".to_string(), PrewarmLayerResult {
                        nodes,
                        duration_ms: start.elapsed().as_millis() as u64,
                    });
                }
                "L4" => {
                    let start = std::time::Instant::now();
                    self.ensure_l4()?;
                    let nodes = self.count_l4_docs();
                    results.insert("L4".to_string(), PrewarmLayerResult {
                        nodes,
                        duration_ms: start.elapsed().as_millis() as u64,
                    });
                }
                _ => {
                    eprintln!("[brain] prewarm: unknown layer '{}', skipping", layer);
                }
            }
        }

        Ok(results)
    }

    fn count_l2_topics(&self) -> u64 {
        if let Some(ref env) = self.l2_env
            && let Ok(txn) = env.env.read_txn()
            && let Ok(iter) = env.topics.iter(&txn)
        {
            let mut count = 0u64;
            for (key, _) in iter.flatten() {
                if key.starts_with("topic:") && key.ends_with(":meta") {
                    count += 1;
                }
            }
            return count;
        }
        0
    }

    fn count_l3_nodes(&self) -> u64 {
        if let Some(ref env) = self.l3_env
            && let Ok(txn) = env.env.read_txn()
            && let Ok(iter) = env.domain_nodes.iter(&txn)
        {
            return iter.flatten().count() as u64;
        }
        0
    }

    fn count_l4_docs(&self) -> u64 {
        if let Some(ref env) = self.l4_env
            && let Ok(txn) = env.env.read_txn()
            && let Ok(iter) = env.docs.iter(&txn)
        {
            return iter.flatten().count() as u64;
        }
        0
    }

    // ── Shelf 便捷方法（委托到 shelf:: 模块函数）

    /// 挂载知识库
    pub fn mount_shelf(
        &mut self,
        dir_path: &str,
        domain: ShelfDomain,
        domain_name: &str,
    ) -> Result<ShelfMeta> {
        crate::shelf::mount(self, dir_path, domain, domain_name)
    }

    /// 卸载知识库
    pub fn unmount_shelf(&mut self, domain_id: &str) -> Result<()> {
        crate::shelf::unmount(self, domain_id)
    }

    /// 列出所有已挂载知识库
    pub fn list_shelf(&mut self) -> Result<Vec<ShelfMeta>> {
        crate::shelf::list(self)
    }

    // ── 会话管理便捷方法

    /// 激活话题
    pub fn activate_topic(&mut self, session_id: &str, topic_id: &str, ttl_ms: i64) {
        self.session_mgr.activate(session_id, topic_id, ttl_ms);
    }

    /// 去激活话题
    pub fn deactivate_topic(&mut self, session_id: &str, topic_id: &str) {
        self.session_mgr.deactivate(session_id, topic_id);
    }

    /// 获取指定会话的激活话题 ID 列表
    pub fn get_activated(&self, session_id: &str) -> Vec<String> {
        self.session_mgr.get_active_topic_ids(session_id)
    }

    // ── L0 Profile 便捷方法

    /// 通过 L0Profile 结构体设置角色画像（完整写入所有字段）
    pub fn set_l0_from_profile(&mut self, profile: &L0Profile) -> Result<()> {
        self.ensure_l0_env()?;
        let l0_env = self.l0_env.as_ref().unwrap();
        let env = l0_env.env.clone();
        let mut wtxn = env
            .write_txn()
            ?;
        let l0 = self.l0.as_ref().unwrap();
        let mut existing = l0
            .get_profile(&wtxn, l0_env)?
            .unwrap_or_default();
        // catid 首次设置后不可修改
        if existing.catid.is_none() && profile.catid.is_some() {
            existing.catid = profile.catid.clone();
        }
        existing.role_name = profile.role_name.clone();
        existing.role = profile.role.clone();
        existing.position = profile.position.clone();
        existing.personality = profile.personality.clone();
        existing.values = profile.values.clone();
        existing.worldview = profile.worldview.clone();
        existing.traits = profile.traits.clone();
        existing.version += 1;
        existing.updated_at = chrono::Utc::now().timestamp_millis();
        l0.update_profile(&mut wtxn, l0_env, &existing)?;
        wtxn.commit()
            ?;
        Ok(())
    }

    /// 返回各层存储使用率统计（仅遍历已打开的 LxEnv）
    pub fn storage_stats(&self) -> Vec<crate::types::StorageLayerInfo> {
        let mut stats = Vec::new();

        if let Some(ref env) = self.l0_env
            && let Ok(u) = env.space_usage()
        {
            stats.push(crate::types::StorageLayerInfo {
                layer: "L0".into(),
                used_bytes: u.used_bytes,
                map_size: u.map_size,
                usage_pct: u.usage_pct,
            });
        }
        if let Some(ref env) = self.l1_env
            && let Ok(u) = env.space_usage()
        {
            stats.push(crate::types::StorageLayerInfo {
                layer: "L1".into(),
                used_bytes: u.used_bytes,
                map_size: u.map_size,
                usage_pct: u.usage_pct,
            });
        }
        if let Some(ref env) = self.l2_env
            && let Ok(u) = env.space_usage()
        {
            stats.push(crate::types::StorageLayerInfo {
                layer: "L2".into(),
                used_bytes: u.used_bytes,
                map_size: u.map_size,
                usage_pct: u.usage_pct,
            });
        }
        if let Some(ref env) = self.l3_env
            && let Ok(u) = env.space_usage()
        {
            stats.push(crate::types::StorageLayerInfo {
                layer: "L3".into(),
                used_bytes: u.used_bytes,
                map_size: u.map_size,
                usage_pct: u.usage_pct,
            });
        }
        if let Some(ref env) = self.l4_env
            && let Ok(u) = env.space_usage()
        {
            stats.push(crate::types::StorageLayerInfo {
                layer: "L4".into(),
                used_bytes: u.used_bytes,
                map_size: u.map_size,
                usage_pct: u.usage_pct,
            });
        }
        if let Some(ref env) = self.l5_env
            && let Ok(u) = env.space_usage()
        {
            stats.push(crate::types::StorageLayerInfo {
                layer: "L5".into(),
                used_bytes: u.used_bytes,
                map_size: u.map_size,
                usage_pct: u.usage_pct,
            });
        }

        stats
    }

    // ── v0.24.0: L3 结晶化 ──────────────────────────────────────

    /// 将 L2 话题"结晶"为 L3 高层领域知识。
    /// meowAgent 负责 LLM 总结生成 summary + keywords；
    /// MemHop 负责创建/更新 L3 domain 节点、更新 L2 的 linked_domain_ids。
    pub fn crystallize_l3(&mut self, req: &CrystallizeL3Request) -> Result<CrystallizeL3Report> {
        req.validate()?;
        // 1. 验证 topic 存在
        self.ensure_l2()?;
        let topic = {
            let l2_env = self.l2_env.as_ref().unwrap();
            let txn = l2_env
                .env
                .read_txn()
                ?;
            self.l2
                .as_ref()
                .unwrap()
                .get_topic_by_id(&txn, l2_env, &req.topic_id)?
                .ok_or_else(|| MemHopError::NotFound(format!("topic {}", req.topic_id)))?
        };

        let domain_name = req
            .domain_name
            .clone()
            .unwrap_or_else(|| topic.label.clone());
        let domain_id = format!("crystallized_{}", req.topic_id);

        // 2. 创建/获取 L3 domain
        self.ensure_l3()?;
        let l3 = self.l3.as_mut().unwrap();
        let l3_env = self.l3_env.as_ref().unwrap();
        let env = l3_env.env.clone();
        let mut wtxn = env
            .write_txn()
            ?;

        let meta_key = format!("meta:{}", domain_id);
        if l3_env
            .domain_meta
            .get(&wtxn, &meta_key)
            ?
            .is_none()
        {
            l3.mount_domain(&mut wtxn, l3_env, &domain_name)?;
        }

        // 3. 将 summary + keywords 编码后写入 L3 node
        let encoded = self.encoder.encode(&req.summary);
        let l3_node_id = crate::batch_store::unique_id("l3n");
        l3.add_node_with_id(
            &mut wtxn,
            l3_env,
            &l3_node_id,
            &domain_id,
            &req.summary,
            &encoded.sparse,
            "",
            encoded.dense,
        )?;
        let mut l3_nodes_created = 1u32;

        // 为每个 keyword 也写入 L3 node
        for kw in &req.keywords {
            let kw_encoded = self.encoder.encode(kw);
            let kw_id = crate::batch_store::unique_id("l3n");
            l3.add_node_with_id(
                &mut wtxn,
                l3_env,
                &kw_id,
                &domain_id,
                kw,
                &kw_encoded.sparse,
                "",
                kw_encoded.dense,
            )?;
            l3_nodes_created += 1;
        }

        wtxn.commit()
            ?;

        // 4. 更新 L2 topic 的 linked_domain_ids + domain_weights
        let topic_linked = {
            let _l2 = self.l2.as_mut().unwrap();
            let l2_env = self.l2_env.as_ref().unwrap();
            let env = l2_env.env.clone();
            let mut wtxn = env
                .write_txn()
                ?;
            let mut topic = topic;
            if !topic.linked_domain_ids.contains(&domain_id) {
                topic.linked_domain_ids.push(domain_id.clone());
            }
            let weight = topic
                .domain_weights
                .get(&domain_id)
                .copied()
                .unwrap_or(0.0);
            topic
                .domain_weights
                .insert(domain_id.clone(), weight + 1.0);
            topic.updated_at = chrono::Utc::now().timestamp_millis();
            let key = format!("topic:{}:meta", &req.topic_id);
            let bytes =
                bincode::serialize(&topic)?;
            l2_env
                .topics
                .put(&mut wtxn, &key, &bytes)
                ?;
            wtxn.commit()
                ?;
            true
        };

        Ok(CrystallizeL3Report {
            domain_id,
            domain_name,
            l3_nodes_created,
            topic_linked,
        })
    }

    // ── v0.24.0: 情感系统 API ─────────────────────────────────────

    /// 情感反馈 — 根据用户情感调节记忆 importance。
    /// v0.24.0: 同步维护 emotion_index。
    pub fn emotional_feedback(&mut self, feedback: &EmotionalFeedback) -> Result<()> {
        feedback.validate()?;
        self.ensure_l1()?;
        let l1 = self.l1.as_mut().unwrap();
        let l1_env = self.l1_env.as_ref().unwrap();
        let env = l1_env.env.clone();
        let mut wtxn = env
            .write_txn()
            ?;

        if let Ok(Some(bytes)) = l1_env.nodes.get(&wtxn, &feedback.memory_id)
            && let Ok(mut node) = bincode::deserialize::<crate::engram::KnowledgeNode>(bytes)
        {
            let old_emotion = node.emotion;
            // 根据情感类型计算 importance 调整
            let delta = match feedback.emotion {
                Emotion::Joy => feedback.intensity * 0.15,
                Emotion::Sadness => feedback.intensity * 0.10,
                Emotion::Anger => feedback.intensity * 0.05,
                Emotion::Fear => feedback.intensity * 0.12,
                Emotion::Surprise => feedback.intensity * 0.08,
                Emotion::Disgust => -(feedback.intensity * 0.10),
                Emotion::Neutral => 0.0,
            };
            node.importance = (node.importance + delta).clamp(0.0, 1.0);
            node.emotion = feedback.emotion;
            node.emotion_intensity = feedback.intensity;
            node.updated_at = chrono::Utc::now().timestamp_millis();
            let new_bytes =
                bincode::serialize(&node)?;
            l1_env
                .nodes
                .put(&mut wtxn, &feedback.memory_id, &new_bytes)
                ?;
            l1.vector_index.update(&feedback.memory_id, &node.vector);

            // v0.24.0: 在 commit 前更新 emotion_index，确保崩溃重启后索引与 LMDB 一致
            if old_emotion != feedback.emotion
            {
                // 从旧情感条目中移除
                if let Some(ids) = self.emotion_index.get_mut(&old_emotion) {
                    ids.retain(|id| id != &feedback.memory_id);
                }
                // 添加到新情感条目
                self.emotion_index
                    .entry(feedback.emotion)
                    .or_default()
                    .push(feedback.memory_id.clone());
            }
        } else {
            // v0.24.0: 对不存在的 memory_id 返回 Err，与 get_emotion() 语义一致
            return Err(MemHopError::NotFound(format!(
                "emotion not found: {}",
                feedback.memory_id
            )));
        }

        wtxn.commit()
            ?;

        Ok(())
    }

    /// 获取记忆的情感维度。
    ///
    /// # 错误
    /// - `MemHopError::NotFound` — memory_id 不存在
    pub fn get_emotion(&mut self, memory_id: &str) -> Result<EmotionalDimension> {
        self.ensure_l1()?;
        let l1_env = self.l1_env.as_ref().unwrap();
        let txn = l1_env
            .env
            .read_txn()
            ?;
        if let Ok(Some(bytes)) = l1_env.nodes.get(&txn, memory_id)
            && let Ok(node) = bincode::deserialize::<crate::engram::KnowledgeNode>(bytes)
        {
            return Ok(EmotionalDimension {
                emotion: node.emotion,
                intensity: node.emotion_intensity,
                valence: node.valence,
                arousal: node.arousal,
            });
        }
        // v0.24.0: 对不存在的 memory_id 返回 Err 而非静默返回默认中性值
        Err(MemHopError::NotFound(format!("emotion not found: {memory_id}")))
    }

    /// 按情感检索记忆。
    /// v0.24.0: 使用 emotion_index 加速检索（O(N) → O(K)），硬编码 max_scan = 10000 防御 DoS。
    pub fn recall_by_emotion(
        &mut self,
        req: &EmotionRecallRequest,
    ) -> Result<RecallResponse> {
        req.validate()?;
        self.ensure_l1()?;
        let l1_env = self.l1_env.as_ref().unwrap();
        let txn = l1_env
            .env
            .read_txn()
            ?;

        const MAX_SCAN: usize = 10_000;
        let mut results: Vec<crate::types::RecallResult> = Vec::new();

        if let Some(target_emotion) = req.emotion {
            // 快速路径：通过 emotion_index 直接定位候选节点
            if let Some(candidate_ids) = self.emotion_index.get(&target_emotion) {
                for memory_id in candidate_ids {
                    if let Ok(Some(bytes)) = l1_env.nodes.get(&txn, memory_id.as_str())
                        && let Ok(node) =
                            bincode::deserialize::<crate::engram::KnowledgeNode>(bytes)
                    {
                        if node.emotion_intensity < req.min_intensity {
                            continue;
                        }
                        // 时间衰减
                        let hours_since =
                            (chrono::Utc::now().timestamp_millis() - node.created_at) as f32
                                / 3_600_000.0;
                        let decay = req
                            .time_decay_lambda
                            .map(|lambda| (-lambda * hours_since).exp())
                            .unwrap_or(1.0);
                        let score = node.emotion_intensity * decay;
                        results.push(crate::types::RecallResult {
                            layer: crate::types::Layer::L1,
                            id: node.id.clone(),
                            text: node.text.clone(),
                            score,
                            topic_label: None,
                            created_at: node.created_at,
                            version: node.version,
                            emotion: Some(EmotionalDimension {
                                emotion: node.emotion,
                                intensity: node.emotion_intensity,
                                valence: node.valence,
                                arousal: node.arousal,
                            }),
                        });
                    }
                }
            }
        } else {
            // 慢速路径：全量扫描（无情感过滤时）
            // ⚠️ 用单独的 scanned 计数器限制扫描总量，避免多数节点不满足
            // min_intensity 时遍历整个数据库。
            let mut scanned = 0usize;
            if let Ok(iter) = l1_env.nodes.iter(&txn) {
                for item in iter {
                    if scanned >= MAX_SCAN {
                        break;
                    }
                    scanned += 1;
                    if let Ok((_key, bytes)) = item
                        && let Ok(node) =
                            bincode::deserialize::<crate::engram::KnowledgeNode>(bytes)
                    {
                        if node.emotion_intensity < req.min_intensity {
                            continue;
                        }
                        // 时间衰减
                        let hours_since =
                            (chrono::Utc::now().timestamp_millis() - node.created_at) as f32
                                / 3_600_000.0;
                        let decay = req
                            .time_decay_lambda
                            .map(|lambda| (-lambda * hours_since).exp())
                            .unwrap_or(1.0);
                        let score = node.emotion_intensity * decay;
                        results.push(crate::types::RecallResult {
                            layer: crate::types::Layer::L1,
                            id: node.id.clone(),
                            text: node.text.clone(),
                            score,
                            topic_label: None,
                            created_at: node.created_at,
                            version: node.version,
                            emotion: Some(EmotionalDimension {
                                emotion: node.emotion,
                                intensity: node.emotion_intensity,
                                valence: node.valence,
                                arousal: node.arousal,
                            }),
                        });
                    }
                }
            }
        }

        results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
        results.truncate(req.max_results);

        Ok(RecallResponse {
            results,
            total_count: 0,
            l0_profile: None,
            confidence: None,
            activated_topics: Vec::new(),
            recommended_crystals: Vec::new(),
        })
    }
}

// ── v0.24.0: 情感索引单元测试 ─────────────────────────────────
#[cfg(test)]
mod tests {
    use super::*;
    use crate::encoder::NgramEncoder;
    use crate::types::{EmotionRecallRequest, StoreBatch, StoreItem};

    /// 创建临时的测试用 Brain。
    fn make_test_brain() -> Brain {
        let tmp = tempfile::tempdir().unwrap();
        let cfg = BrainConfig {
            brains_dir: tmp.path().to_str().unwrap().to_string(),
            agent_id: "emotion_index_test".to_string(),
        };
        let encoder: Arc<Box<dyn Encoder>> =
            Arc::new(Box::new(NgramEncoder::new(1024)));
        Brain::open(cfg, encoder).unwrap()
    }

    /// 存储一个测试节点并返回其 ID。
    fn store_test_node(brain: &mut Brain, text: &str) -> String {
        let batch = StoreBatch {
            items: vec![StoreItem {
                text: text.to_string(),
                source: "test".to_string(),
                turn_id: None,
                session_id: None,
                topic_label: None,
                llm_keywords: None,
                llm_compressed_summary: None,
                chain_parent_id: None,
                chain_label: None,
                domain_id: None,
                importance: None,
                valence: None,
                arousal: None,
            }],
        };
        let report = brain.batch_store(batch).unwrap();
        report.engram_ids.get("0").cloned().unwrap()
    }

    #[test]
    fn test_emotion_index_rebuild_on_ensure_l1() {
        let mut brain = make_test_brain();
        // L1 尚未初始化，emotion_index 为空
        assert!(brain.emotion_index.is_empty());
        // 触发 ensure_l1 → 内部重建空索引（LMDB 无数据）
        brain.ensure_l1().unwrap();
        assert!(brain.emotion_index.is_empty());
    }

    #[test]
    fn test_emotional_feedback_updates_index() {
        let mut brain = make_test_brain();
        let node_id = store_test_node(&mut brain, "I love this world!");

        // batch_store 已同步 emotion_index，节点默认情感为 Neutral
        let neutral_ids = brain.emotion_index.get(&Emotion::Neutral).unwrap();
        assert!(neutral_ids.contains(&node_id), "batch_store 后 Neutral 索引应包含节点");

        // 情感反馈：Joy
        brain
            .emotional_feedback(&EmotionalFeedback {
                memory_id: node_id.clone(),
                emotion: Emotion::Joy,
                intensity: 0.9,
                reason: None,
            })
            .unwrap();

        // Joy 索引中应有该节点
        let joy_ids = brain.emotion_index.get(&Emotion::Joy).unwrap();
        assert!(joy_ids.contains(&node_id), "Joy 索引应包含节点");
        // Neutral 索引中应已移除
        let neutral_ids = brain.emotion_index.get(&Emotion::Neutral);
        assert!(
            neutral_ids.map_or(true, |ids| !ids.contains(&node_id)),
            "Neutral 索引应已移除节点"
        );

        // 再次反馈改为 Sadness
        brain
            .emotional_feedback(&EmotionalFeedback {
                memory_id: node_id.clone(),
                emotion: Emotion::Sadness,
                intensity: 0.7,
                reason: None,
            })
            .unwrap();

        // Joy 索引中应已移除
        let joy_ids = brain.emotion_index.get(&Emotion::Joy);
        assert!(
            joy_ids.map_or(true, |ids| !ids.contains(&node_id)),
            "Joy 索引应已移除节点"
        );
        // Sadness 索引中应有该节点
        let sad_ids = brain.emotion_index.get(&Emotion::Sadness).unwrap();
        assert!(sad_ids.contains(&node_id), "Sadness 索引应包含节点");
    }

    #[test]
    fn test_recall_by_emotion_uses_index() {
        let mut brain = make_test_brain();
        let node_id = store_test_node(&mut brain, "What a wonderful day!");

        // 设置情感为 Joy
        brain
            .emotional_feedback(&EmotionalFeedback {
                memory_id: node_id.clone(),
                emotion: Emotion::Joy,
                intensity: 0.8,
                reason: None,
            })
            .unwrap();

        // 按 Joy 检索
        let resp = brain
            .recall_by_emotion(&EmotionRecallRequest {
                emotion: Some(Emotion::Joy),
                min_intensity: 0.0,
                time_decay_lambda: None,
                max_results: 10,
            })
            .unwrap();

        assert_eq!(resp.results.len(), 1, "应找到 1 个 Joy 节点");
        assert_eq!(resp.results[0].id, node_id);

        // 按 Sadness 检索 → 应无结果
        let resp = brain
            .recall_by_emotion(&EmotionRecallRequest {
                emotion: Some(Emotion::Sadness),
                min_intensity: 0.0,
                time_decay_lambda: None,
                max_results: 10,
            })
            .unwrap();
        assert_eq!(resp.results.len(), 0, "不应找到 Sadness 节点");
    }

    #[test]
    fn test_recall_by_emotion_min_intensity() {
        let mut brain = make_test_brain();
        let node_id = store_test_node(&mut brain, "A mildly interesting fact.");

        // 低强度情感：0.3
        brain
            .emotional_feedback(&EmotionalFeedback {
                memory_id: node_id.clone(),
                emotion: Emotion::Surprise,
                intensity: 0.3,
                reason: None,
            })
            .unwrap();

        // min_intensity = 0.5 → 不应匹配
        let resp = brain
            .recall_by_emotion(&EmotionRecallRequest {
                emotion: Some(Emotion::Surprise),
                min_intensity: 0.5,
                time_decay_lambda: None,
                max_results: 10,
            })
            .unwrap();
        assert_eq!(resp.results.len(), 0, "min_intensity 过滤应生效");

        // min_intensity = 0.2 → 应匹配
        let resp = brain
            .recall_by_emotion(&EmotionRecallRequest {
                emotion: Some(Emotion::Surprise),
                min_intensity: 0.2,
                time_decay_lambda: None,
                max_results: 10,
            })
            .unwrap();
        assert_eq!(resp.results.len(), 1, "min_intensity=0.2 时应匹配");
    }

    #[test]
    fn test_emotion_index_multi_node() {
        let mut brain = make_test_brain();
        let id1 = store_test_node(&mut brain, "I love this!");
        let id2 = store_test_node(&mut brain, "So happy today!");
        let id3 = store_test_node(&mut brain, "This makes me sad.");

        // 设置情感
        brain
            .emotional_feedback(&EmotionalFeedback {
                memory_id: id1,
                emotion: Emotion::Joy,
                intensity: 0.8,
                reason: None,
            })
            .unwrap();
        brain
            .emotional_feedback(&EmotionalFeedback {
                memory_id: id2,
                emotion: Emotion::Joy,
                intensity: 0.9,
                reason: None,
            })
            .unwrap();
        brain
            .emotional_feedback(&EmotionalFeedback {
                memory_id: id3,
                emotion: Emotion::Sadness,
                intensity: 0.7,
                reason: None,
            })
            .unwrap();

        // Joy 有 2 个节点
        assert_eq!(
            brain.emotion_index.get(&Emotion::Joy).unwrap().len(),
            2,
            "Joy 应有 2 个节点"
        );
        // Sadness 有 1 个节点
        assert_eq!(
            brain.emotion_index.get(&Emotion::Sadness).unwrap().len(),
            1,
            "Sadness 应有 1 个节点"
        );
    }

    #[test]
    fn test_recall_by_emotion_no_emotion_filter() {
        let mut brain = make_test_brain();
        store_test_node(&mut brain, "Node A");
        store_test_node(&mut brain, "Node B");
        store_test_node(&mut brain, "Node C");

        // 无 emotion 过滤，走全量扫描路径
        let resp = brain
            .recall_by_emotion(&EmotionRecallRequest {
                emotion: None,
                min_intensity: 0.0,
                time_decay_lambda: None,
                max_results: 100,
            })
            .unwrap();

        // 应扫描到所有节点（Neutral 情感）
        // 注意：新节点 emotion=Neutral, intensity=0.0，所以 score=0.0
        assert!(resp.results.len() >= 3, "应返回至少 3 个节点");
    }
}
