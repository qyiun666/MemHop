use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;

use crate::domain_graph::L3DomainGraph;
use crate::encoder::Encoder;
use crate::engram::{RawDocument, Topic};
use crate::error::{MemHopError, Result};
use crate::hypergraph::L1Hypergraph;
use crate::index::HnswConfig;
use crate::lmdb::{L0Env, L1Env, L2Env, L3Env, L4Env, L5Env};
use crate::profile::L0ProfileStore;
use crate::raw_archive::L4RawArchive;
use crate::session::SessionManager;
use crate::topic_graph::L2TopicGraph;
use crate::types::DreamConfig;
use crate::types::{
    ActivatedTopicInfo, BatchReport, BrainConfig, ConsolidateReport, CrystallizeReport, L0Profile,
    L3PathInfo, ProceduralCrystal, RecallRequest, RecallResponse, StoreBatch,
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

    pub(crate) fn ensure_l1(&mut self) -> Result<()> {
        if self.l1.is_some() {
            return Ok(());
        }
        let _timer = std::time::Instant::now();
        self.ensure_l1_env()?;
        let encoder_dim = self.encoder.dim();
        let config = HnswConfig::default();
        let mut l1 = L1Hypergraph::with_dim_and_config(encoder_dim, config)?;
        let l1_env = self.l1_env.as_ref().unwrap();
        l1.rebuild_bm25(l1_env)?;
        l1.rebuild_vector_index(l1_env)
            .map_err(|e| MemHopError::Internal(format!("rebuild L1 vector index: {}", e)))?;
        let elapsed = _timer.elapsed();
        if elapsed.as_millis() > 100 {
            eprintln!("[memhop] WARNING: L1 first open took {}ms", elapsed.as_millis());
        }
        self.l1 = Some(l1);
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

    pub(crate) fn ensure_l2(&mut self) -> Result<()> {
        if self.l2.is_some() {
            return Ok(());
        }
        let _timer = std::time::Instant::now();
        self.ensure_l2_env()?;
        let encoder_dim = self.encoder.dim();
        let config = HnswConfig::default();
        let mut l2 = L2TopicGraph::with_dim_and_config(encoder_dim, config)?;
        let l2_env = self.l2_env.as_ref().unwrap();
        l2.rebuild_topic_vectors(l2_env)
            .map_err(|e| MemHopError::Internal(format!("rebuild L2 topic vectors: {}", e)))?;
        let elapsed = _timer.elapsed();
        if elapsed.as_millis() > 100 {
            eprintln!("[memhop] WARNING: L2 first open took {}ms", elapsed.as_millis());
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

    pub(crate) fn ensure_l3(&mut self) -> Result<()> {
        if self.l3.is_some() {
            return Ok(());
        }
        let _timer = std::time::Instant::now();
        self.ensure_l3_env()?;
        let encoder_dim = self.encoder.dim();
        let config = HnswConfig::default();
        let mut l3 = L3DomainGraph::with_dim_and_config(encoder_dim, config)?;
        let l3_env = self.l3_env.as_ref().unwrap();
        l3.rebuild_vector_index(l3_env)
            .map_err(|e| MemHopError::Internal(format!("rebuild L3 vector index: {}", e)))?;
        l3.rebuild_bm25(l3_env)
            .map_err(|e| MemHopError::Internal(format!("rebuild L3 BM25 index: {}", e)))?;
        let elapsed = _timer.elapsed();
        if elapsed.as_millis() > 100 {
            eprintln!("[memhop] WARNING: L3 first open took {}ms", elapsed.as_millis());
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

    pub(crate) fn ensure_l4(&mut self) -> Result<()> {
        if self.l4.is_some() {
            return Ok(());
        }
        let _timer = std::time::Instant::now();
        self.ensure_l4_env()?;
        let encoder_dim = self.encoder.dim();
        let config = HnswConfig::default();
        let mut l4 = L4RawArchive::with_dim_and_config(encoder_dim, config)?;
        let l4_env = self.l4_env.as_ref().unwrap();
        l4.rebuild_vector_index(l4_env)
            .map_err(|e| MemHopError::Internal(format!("rebuild L4 vector index: {}", e)))?;
        let elapsed = _timer.elapsed();
        if elapsed.as_millis() > 100 {
            eprintln!("[memhop] WARNING: L4 first open took {}ms", elapsed.as_millis());
        }
        self.l4 = Some(l4);
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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

    /// 获取 L0 角色画像
    pub fn get_l0_profile(&mut self) -> Result<Option<L0Profile>> {
        self.ensure_l0_env()?;
        let l0_env = self.l0_env.as_ref().unwrap();
        let txn = l0_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let key = format!("topic:{}:meta", topic_id);
        match l2_env
            .topics
            .get(&wtxn, &key)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
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
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    l2_env
                        .topics
                        .put(&mut wtxn, &key, &new_bytes)
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// 获取已激活 Topic 列表
    pub fn get_activated_topics(&mut self) -> Vec<ActivatedTopicInfo> {
        self.session_mgr.purge_expired();
        self.session_mgr.get_active_list()
    }

    /// 获取 L4 原文
    pub fn get_l4_raw(&mut self, doc_id: &str) -> Result<Option<RawDocument>> {
        self.ensure_l4_env()?;
        let l4_env = self.l4_env.as_ref().unwrap();
        let txn = l4_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        match l4_env
            .docs
            .get(&txn, doc_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
        {
            Some(bytes) => Ok(Some(
                bincode::deserialize(bytes).map_err(|e| MemHopError::Storage(e.to_string()))?,
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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

    /// 带过滤的再搜索（排除已选结果）
    pub fn re_search(&mut self, req: &RecallRequest) -> Result<RecallResponse> {
        self.session_mgr.purge_expired();
        let mut resp = crate::recall::enhanced_recall(self, req)?;
        self.ensure_l0_env()?;
        let l0_env = self.l0_env.as_ref().unwrap();
        let txn = l0_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let bytes = bincode::serialize(crystal)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        l5_env
            .crystals
            .put(&mut wtxn, &crystal.id, &bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn.commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// v0.18.3: 按 ID 获取程序性晶体。
    pub fn get_crystal(&mut self, id: &str) -> Result<Option<ProceduralCrystal>> {
        self.ensure_l5_env()?;
        let l5_env = self.l5_env.as_ref().unwrap();
        let txn = l5_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        match l5_env
            .crystals
            .get(&txn, id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
        {
            Some(bytes) => Ok(Some(
                bincode::deserialize(bytes)
                    .map_err(|e| MemHopError::Storage(e.to_string()))?,
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
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
}
