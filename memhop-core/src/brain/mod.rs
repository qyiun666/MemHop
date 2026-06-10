use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;

use crate::activation::{ActivationConfig, ActivationManager};
use crate::reconsolidation::ReconsolidationManager;
use crate::domain_graph::L3DomainGraph;
use crate::encoder::Encoder;
use crate::engram::KnowledgeNode;
use crate::error::{MemHopError, Result};
use crate::hypergraph::L1Hypergraph;
use crate::index::MemHopHnswConfig;

use crate::profile::L0ProfileStore;
use crate::raw_archive::L4RawArchive;
use crate::session::SessionManager;
use crate::storage::store::RedbStore;
use crate::topic_graph::L2TopicGraph;
use crate::types::{
    ActivatedTopicInfo, BatchReport, BrainConfig, Emotion, RecallRequest,
    RecallResponse, ShelfDomain, ShelfMeta, StoreBatch,
};

// ── 子模块声明 ──────────────────────────────────────────────
mod l0_profile;
mod l2_topics;
mod l3_domains;
mod l4_archive;
mod l5_crystal;
mod emotion;
mod lifecycle;

/// MemHop v0.25.0 Brain — 6 层仿人脑记忆架构顶层 API。
/// 所有层使用 redb 单文件存储引擎。
pub struct Brain {
    pub config: BrainConfig,
    pub l0: Option<L0ProfileStore>,
    pub l1: Option<L1Hypergraph>,
    pub l2: Option<L2TopicGraph>,
    pub l3: Option<L3DomainGraph>,
    pub l4: Option<L4RawArchive>,
    /// redb 存储引擎
    pub redb_store: Option<RedbStore>,
    pub session_mgr: SessionManager,
    pub encoder: Arc<Box<dyn Encoder>>,
    /// v1.0: E5 编码器（通过 IPC 连接 memhop-encoder 服务，可选）
    pub encoder_e5: Option<Arc<Box<dyn Encoder>>>,
    /// v0.23.0: 记忆激活管理器 (Active/Latent/Dormant)
    pub activation: Option<ActivationManager>,
    /// v0.24.0: 情感内存索引 (Emotion → Node IDs)，启动时自动重建
    pub emotion_index: HashMap<Emotion, Vec<String>>,
    /// v1.0: 再巩固管理器
    pub reconsolidation: Option<ReconsolidationManager>,
}

/// 预热单层结果。
#[derive(Debug, Clone, serde::Serialize)]
pub struct PrewarmLayerResult {
    pub nodes: u64,
    pub duration_ms: u64,
}

impl Brain {
    /// 打开 Brain，仅保存 config 和 encoder，所有层使用 redb 单文件存储引擎。
    pub fn open(config: BrainConfig, encoder: Arc<Box<dyn Encoder>>) -> Result<Self> {
        let path = Path::new(&config.brains_dir);
        std::fs::create_dir_all(path)
            .map_err(|e| MemHopError::Storage(format!("create brains dir: {}", e)))?;

        let session_mgr = SessionManager::new();

        // v0.25.0: 打开 redb 单文件存储引擎（必需，不再有 LMDB 回退）
        let brain_db_path = path.join("agent_brain.db");
        let redb_store = Some(
            RedbStore::open(&brain_db_path)
                .map_err(|e| MemHopError::Storage(format!("failed to open agent_brain.db: {}", e)))?,
        );
        eprintln!("[brain] redb storage opened at {}", brain_db_path.display());

        Ok(Brain {
            config,
            l0: None,
            l1: None,
            l2: None,
            l3: None,
            l4: None,
            redb_store,
            session_mgr,
            encoder,
            encoder_e5: None,
            activation: None,
            emotion_index: HashMap::new(),
            reconsolidation: None,
        })
    }

    /// v1.0: 设置 E5 编码器（用于第三检索通道）
    pub fn set_e5_encoder(&mut self, encoder: Arc<Box<dyn Encoder>>) {
        self.encoder_e5 = Some(encoder);
    }

    /// v0.22.0: 按节点规模自适应 HNSW 配置（for_scale 代替 default）。
    pub fn ensure_l1(&mut self) -> Result<()> {
        if self.l1.is_some() {
            return Ok(());
        }
        let _timer = std::time::Instant::now();
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let encoder_dim = self.encoder.dim();
        let node_count = store.l1_node_count().unwrap_or(0) as usize;
        let config = MemHopHnswConfig::for_scale(node_count);
        let connectivity = config.connectivity;
        let mut l1 = L1Hypergraph::with_dim_and_config(encoder_dim, config);
        l1.rebuild_bm25(store)?;
        l1.rebuild_vector_index(store)?;
        let elapsed = _timer.elapsed();
        if elapsed.as_millis() > 100 {
            eprintln!(
                "[memhop] WARNING: L1 first open took {}ms ({} nodes, connectivity={})",
                elapsed.as_millis(),
                node_count,
                connectivity
            );
        }
        if self.activation.is_none() {
            self.activation = Some(ActivationManager::new(ActivationConfig::default()));
        }
        self.l1 = Some(l1);
        if self.emotion_index.is_empty() {
            self.rebuild_emotion_index()?;
        }
        Ok(())
    }

    /// v0.25.0: 从 redb 重建情感索引（max_scan = 10,000）。
    fn rebuild_emotion_index(&mut self) -> Result<()> {
        const MAX_SCAN: usize = 10_000;
        let store = match self.redb_store.as_ref() {
            Some(s) => s,
            None => return Ok(()),
        };
        let rtxn = match store.begin_read() {
            Ok(t) => t,
            Err(_) => return Ok(()),
        };
        let nodes: Vec<(String, KnowledgeNode)> =
            match store.iter_bincode(&rtxn, crate::storage::L1_NODES) {
                Ok(v) => v,
                Err(_) => return Ok(()),
            };
        drop(rtxn);
        for (scanned, (_key, node)) in nodes.into_iter().enumerate() {
            if scanned >= MAX_SCAN {
                break;
            }
            self.emotion_index
                .entry(node.memory.emotion)
                .or_default()
                .push(node.id);
        }
        Ok(())
    }

    // ── Lazy open: L2 ──
    pub(crate) fn ensure_l2(&mut self) -> Result<()> {
        if self.l2.is_some() {
            return Ok(());
        }
        let _timer = std::time::Instant::now();
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let encoder_dim = self.encoder.dim();
        let topic_count = store.l2_topic_count().unwrap_or(0) as usize;
        let config = MemHopHnswConfig::for_scale(topic_count);
        let mut l2 = L2TopicGraph::with_dim_and_config(encoder_dim, config);
        l2.rebuild_topic_vectors(store)
            .map_err(|e| MemHopError::Internal(format!("rebuild L2 topic vectors: {}", e)))?;
        // v1.0: 重建 topic ngram 倒排索引
        if let Err(e) = l2.rebuild_ngram_index(store) {
            eprintln!("[memhop] WARNING: L2 ngram index rebuild failed: {}", e);
        }
        let elapsed = _timer.elapsed();
        if elapsed.as_millis() > 100 {
            eprintln!(
                "[memhop] WARNING: L2 first open took {}ms ({} topics)",
                elapsed.as_millis(),
                topic_count
            );
        }
        self.l2 = Some(l2);
        Ok(())
    }

    // ── Lazy open: L3 ──
    pub(crate) fn ensure_l3(&mut self) -> Result<()> {
        if self.l3.is_some() {
            return Ok(());
        }
        let _timer = std::time::Instant::now();
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let encoder_dim = self.encoder.dim();
        let node_count = store.l3_node_count().unwrap_or(0) as usize;
        let config = MemHopHnswConfig::for_scale(node_count);
        let connectivity = config.connectivity;
        let mut l3 = L3DomainGraph::with_dim_and_config(encoder_dim, config);
        l3.rebuild_vector_index(store)
            .map_err(|e| MemHopError::Internal(format!("rebuild L3 vector index: {}", e)))?;
        l3.rebuild_bm25(store)
            .map_err(|e| MemHopError::Internal(format!("rebuild L3 BM25 index: {}", e)))?;
        let elapsed = _timer.elapsed();
        if elapsed.as_millis() > 100 {
            eprintln!(
                "[memhop] WARNING: L3 first open took {}ms ({} nodes, connectivity={})",
                elapsed.as_millis(),
                node_count,
                connectivity
            );
        }
        self.l3 = Some(l3);
        Ok(())
    }

    // ── Lazy open: L4 ──
    pub(crate) fn ensure_l4(&mut self) -> Result<()> {
        if self.l4.is_some() {
            return Ok(());
        }
        self.l4 = Some(L4RawArchive::new());
        Ok(())
    }

    // ── redb 引用 ──
    pub fn redb_store(&self) -> Option<&RedbStore> {
        self.redb_store.as_ref()
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
        if let Some(ref store) = self.redb_store {
            resp.l0_profile = store.l0_get_profile()?;
        }
        // 附带已激活 Topic 列表
        resp.activated_topics = self.session_mgr.get_active_list();
        Ok(resp)
    }

    pub fn config(&self) -> &BrainConfig {
        &self.config
    }

    /// 获取已激活 Topic 列表
    pub fn get_activated_topics(&mut self) -> Vec<ActivatedTopicInfo> {
        self.session_mgr.purge_expired();
        self.session_mgr.get_active_list()
    }

    /// 带过滤的再搜索（排除已选结果）
    pub fn re_search(&mut self, req: &RecallRequest) -> Result<RecallResponse> {
        self.session_mgr.purge_expired();
        let mut resp = crate::recall::enhanced_recall(self, req)?;
        // 附带 L0 Profile
        if let Some(ref store) = self.redb_store {
            resp.l0_profile = store.l0_get_profile()?;
        }
        resp.activated_topics = self.session_mgr.get_active_list();
        Ok(resp)
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
}
