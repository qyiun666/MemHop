use std::path::Path;

use crate::domain_graph::L3DomainGraph;
#[cfg(feature = "candle")]
use crate::encoder::CandleEncoder;
use crate::encoder::{Encoder, NgramEncoder};
use crate::engram::{RawDocument, Topic};
use crate::error::{MemHopError, Result};
use crate::hypergraph::L1Hypergraph;
use crate::index::HnswConfig;
use crate::lmdb::{L0Env, L1Env, L2Env, L3Env, L4Env};
use crate::profile::L0ProfileStore;
use crate::raw_archive::L4RawArchive;
use crate::session::SessionManager;
use crate::topic_graph::L2TopicGraph;
use crate::types::DreamConfig;
use crate::types::{
    ActivatedTopicInfo, BatchReport, BrainConfig, ConsolidateReport, L0Profile, L3PathInfo,
    RecallRequest, RecallResponse, StoreBatch,
};

/// MemHop v0.18.1 Brain — 5 层仿人脑记忆架构顶层 API。
pub struct Brain {
    pub config: BrainConfig,
    pub l0_env: L0Env,
    pub l1_env: L1Env,
    pub l2_env: L2Env,
    pub l3_env: L3Env,
    pub l4_env: L4Env,
    pub l0: L0ProfileStore,
    pub l1: L1Hypergraph,
    pub l2: L2TopicGraph,
    pub l3: L3DomainGraph,
    pub l4: L4RawArchive,
    pub session_mgr: SessionManager,
    pub encoder: Box<dyn Encoder>,
}

impl Brain {
    pub fn open(config: BrainConfig) -> Result<Self> {
        let path = Path::new(&config.brains_dir);
        std::fs::create_dir_all(path)
            .map_err(|e| MemHopError::Storage(format!("create brains dir: {}", e)))?;

        let l0_env = L0Env::open(&path.join("l0_profile.db"))?;
        let l1_env = L1Env::open(&path.join("l1_hypergraph.db"))?;
        let l2_env = L2Env::open(&path.join("l2_topics.db"))?;
        let l3_env = L3Env::open(&path.join("l3_domains.db"))?;
        let l4_env = L4Env::open(&path.join("l4_raw.db"))?;

        let l0 = L0ProfileStore::new();

        // 先初始化编码器以获取维度
        let encoder: Box<dyn Encoder> = if let Some(ref _mp) = config.model_path {
            // v0.18.1: 尝试使用 CandleEncoder
            #[cfg(feature = "candle")]
            {
                match CandleEncoder::from_path(mp) {
                    Ok(enc) => {
                        eprintln!("memhop-brain: using CandleEncoder from '{}'", mp);
                        Box::new(enc) as Box<dyn Encoder>
                    }
                    Err(e) => {
                        eprintln!(
                            "memhop-brain: failed to load CandleEncoder from '{}': {}, falling back to NgramEncoder",
                            mp, e
                        );
                        Box::new(NgramEncoder::new(1024)) as Box<dyn Encoder>
                    }
                }
            }
            #[cfg(not(feature = "candle"))]
            {
                eprintln!("memhop-brain: candle feature disabled, using NgramEncoder");
                Box::new(NgramEncoder::new(1024)) as Box<dyn Encoder>
            }
        } else {
            eprintln!("memhop-brain: no model_path specified, using NgramEncoder");
            Box::new(NgramEncoder::new(1024)) as Box<dyn Encoder>
        };

        // v0.16.0: 使用编码器维度初始化各层向量索引
        let encoder_dim = encoder.dim();

        // v0.18.0: 根据各层数据规模动态调整 HNSW 配置
        let l1_config = HnswConfig::default(); // L1 通常较小
        let l2_config = HnswConfig::default(); // L2 通常较小
        let l3_config = HnswConfig::default(); // L3 通常中等
        let l4_config = HnswConfig::default(); // L4 通常较大

        let mut l1 = L1Hypergraph::with_dim_and_config(encoder_dim, l1_config)?;
        l1.rebuild_bm25(&l1_env)?;
        if let Err(e) = l1.rebuild_vector_index(&l1_env) {
            eprintln!("[brain] rebuild L1 vector index error: {}", e);
        }
        let mut l2 = L2TopicGraph::with_dim_and_config(encoder_dim, l2_config)?;
        if let Err(e) = l2.rebuild_topic_vectors(&l2_env) {
            eprintln!("[brain] rebuild L2 topic vectors error: {}", e);
        }
        let mut l3 = L3DomainGraph::with_dim_and_config(encoder_dim, l3_config)?;
        if let Err(e) = l3.rebuild_vector_index(&l3_env) {
            eprintln!("[brain] rebuild L3 vector index error: {}", e);
        }
        if let Err(e) = l3.rebuild_bm25(&l3_env) {
            eprintln!("[brain] rebuild L3 BM25 index error: {}", e);
        }
        let mut l4 = L4RawArchive::with_dim_and_config(encoder_dim, l4_config)?;
        if let Err(e) = l4.rebuild_vector_index(&l4_env) {
            eprintln!("[brain] rebuild L4 vector index error: {}", e);
        }
        let session_mgr = SessionManager::new();

        Ok(Brain {
            config,
            l0_env,
            l1_env,
            l2_env,
            l3_env,
            l4_env,
            l0,
            l1,
            l2,
            l3,
            l4,
            session_mgr,
            encoder,
        })
    }

    pub fn batch_store(&mut self, batch: StoreBatch) -> Result<BatchReport> {
        crate::batch_store::execute(self, batch)
    }

    pub fn recall(&mut self, req: &RecallRequest) -> Result<RecallResponse> {
        // 清理过期激活
        self.session_mgr.purge_expired();
        let mut resp = crate::recall::enhanced_recall(self, req)?;
        // 附带 L0 Profile
        let txn = self
            .l0_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        resp.l0_profile = self.l0.get_profile(&txn, &self.l0_env)?;
        // 附带已激活 Topic 列表
        resp.activated_topics = self.session_mgr.get_active_list();
        Ok(resp)
    }

    pub fn consolidate(&mut self) -> Result<ConsolidateReport> {
        let config = DreamConfig::default();
        crate::dream::run(self, &config)
    }

    pub fn organize_node(&mut self, node_id: &str) -> Result<()> {
        crate::organize::organize_node(self, node_id)
    }

    pub fn config(&self) -> &BrainConfig {
        &self.config
    }

    /// 获取 L0 角色画像
    pub fn get_l0_profile(&self) -> Result<Option<L0Profile>> {
        let txn = self
            .l0_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.l0.get_profile(&txn, &self.l0_env)
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
        let env = self.l0_env.env.clone();
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        // 读取现有 profile 或创建新的
        let mut profile = self
            .l0
            .get_profile(&wtxn, &self.l0_env)?
            .unwrap_or_default();
        // catid 只在首次设置时保存，之后不可修改
        if profile.catid.is_none() {
            if let Some(id) = catid {
                profile.catid = Some(id);
            }
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
        self.l0.update_profile(&mut wtxn, &self.l0_env, &profile)?;
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
        let env = self.l0_env.env.clone();
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut profile = self
            .l0
            .get_profile(&wtxn, &self.l0_env)?
            .unwrap_or_default();
        // catid 只在首次设置时保存，之后不可修改
        if profile.catid.is_none() {
            if let Some(id) = catid {
                profile.catid = Some(id);
            }
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
        self.l0.update_profile(&mut wtxn, &self.l0_env, &profile)?;
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
        let env = self.l2_env.env.clone();
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let key = format!("topic:{}:meta", topic_id);
        match self
            .l2_env
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
                    self.l2_env
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
    pub fn get_l4_raw(&self, doc_id: &str) -> Result<Option<RawDocument>> {
        let txn = self
            .l4_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        match self
            .l4_env
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
    pub fn list_l3_paths(&self) -> Result<Vec<L3PathInfo>> {
        let txn = self
            .l3_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut paths = Vec::new();
        if let Ok(iter) = self.l3_env.domain_meta.iter(&txn) {
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
    pub fn list_topics(&self) -> Result<Vec<Topic>> {
        let txn = self
            .l2_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut topics = Vec::new();
        if let Ok(iter) = self.l2_env.topics.iter(&txn) {
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
        let txn = self
            .l0_env
            .env
            .read_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        resp.l0_profile = self.l0.get_profile(&txn, &self.l0_env)?;
        resp.activated_topics = self.session_mgr.get_active_list();
        Ok(resp)
    }
}
