//! Brain — 顶层 API，整合 Cortex / Hippocampus / UnifiedGraph / Hopfield 等组件。
//!
//! 三层架构：
//!   L0 Cortex       工作记忆 ring buffer（~7 条，当前会话）
//!   L1 Hippocampus  暂存区（~500 条，高保真）
//!   L2 Neocortex    UnifiedGraph + Hopfield（持久化，长期记忆）
//!
//! 调用方（MeowAgent）负责编码:
//!   - perceive() 接收预计算 vector
//!   - recall()   接收预计算 query_vector (None 时降级用 NgramEncoder)

use std::cell::RefCell;
use std::collections::{HashMap, HashSet, VecDeque};
use std::sync::Arc;

use half::f16;

use crate::cortex::Cortex;
use crate::context::{ActiveContextSet, DormantContextPool, Phase};
use crate::encoder::{Encoder, NgramEncoder};
#[cfg(feature = "onnx")]
use crate::encoder::reranker::Reranker;
use crate::engram::{
    CompressResult, EmotionalContext, Engram, EngramKind,
};
use crate::error::{MemHopError, Result};
use crate::hippocampus::Hippocampus;
use crate::hnsw::HnswIndex;
use crate::hopfield::ModernHopfield;
use crate::index::SparseIndex;
use crate::llm_provider::LlmProvider;
use crate::personality::{GrowthState, Personality};
use crate::plan_gate::{PlanGate, PlanIndex};
use crate::storage::LmdbStorage;
use crate::tree::Tree;
use crate::entanglement::EntanglementEvent;
use crate::types::{
    BrainConfig, DreamReport, ForgetFilter,
    PerceptionInput, PerceptionOutput, RecallRequest, RecallResponse,
    ReflectionInput, StoreResult, TreeContext,
};
use crate::unified_graph::UnifiedGraph;
use crate::worldview::WorldviewPattern;

pub(crate) mod init;

// ── 常量 ─────────────────────────────────────────────────────

pub(crate) const HOPFIELD_BETA: f32 = 8.0;

// v0.12.0: 知识自动附带常量
pub(crate) const KNOWLEDGE_ATTACH_LIMIT: usize = 5;
pub(crate) const KNOWLEDGE_ATTACH_MAX: usize = 10;
pub(crate) const KNOWLEDGE_THRESHOLD: f32 = 0.6;

// ── v0.9.0: Engram cache ───────────────────────────────────

/// Bounded FIFO cache for hot engrams, reducing LMDB read latency.
pub(crate) struct EngramCache {
    cache: HashMap<String, Engram>,
    order: VecDeque<String>,
    max_size: usize,
}

impl EngramCache {
    pub(crate) fn new(max_size: usize) -> Self {
        EngramCache {
            cache: HashMap::new(),
            order: VecDeque::new(),
            max_size,
        }
    }

    pub(crate) fn get(&self, id: &str) -> Option<&Engram> {
        self.cache.get(id)
    }

    pub(crate) fn insert(&mut self, id: String, engram: Engram) {
        if self.cache.contains_key(&id) {
            return;
        }
        if self.order.len() >= self.max_size
            && let Some(old) = self.order.pop_front()
        {
            self.cache.remove(&old);
        }
        self.order.push_back(id.clone());
        self.cache.insert(id, engram);
    }

    pub(crate) fn remove(&mut self, id: &str) {
        self.cache.remove(id);
        self.order.retain(|x| x != id);
    }

    /// Iterate over all cached entries.
    pub(crate) fn entries(&self) -> impl Iterator<Item = (&String, &Engram)> {
        self.cache.iter()
    }
}

/// v0.13.0: A pending tree-to-context edge strengthen request from recall.
#[derive(Debug, Clone)]
pub struct PendingTreeEdge {
    pub context_id: String,
    pub tree_id: String,
    pub delta: f32,
}

// ── Brain ────────────────────────────────────────────────────

/// MemHop Brain — 三层记忆架构的顶层 API。
pub struct Brain {
    pub(crate) cortex: Cortex,
    pub(crate) hippocampus: Hippocampus,
    pub(crate) graph: UnifiedGraph,
    pub(crate) hopfield: ModernHopfield,
    /// v0.9.0: HNSW index for fast approximate nearest neighbor search.
    pub(crate) hnsw: HnswIndex,
    /// v0.9.0: Sparse inverted index for ngram-based retrieval (RRF fusion).
    pub(crate) sparse_index: SparseIndex,
    /// v0.9.0: HNSW NodeId → engram string ID reverse mapping.
    pub(crate) hnsw_id_map: HashMap<u64, String>,
    /// v0.9.0: Monotonic counter for HNSW node IDs (replaces hash-based IDs).
    pub(crate) next_node_id: u64,
    pub(crate) storage: Arc<LmdbStorage>,

    pub(crate) emotional_ctx: EmotionalContext,
    pub(crate) growth: GrowthState,
    pub(crate) personality: Personality,
    pub(crate) config: BrainConfig,

    #[allow(dead_code)]
    pub(crate) llm: Option<Box<dyn LlmProvider>>,
    pub(crate) ngram_encoder: NgramEncoder,
    /// v0.12.0: Optional Candle semantic encoder (pure Rust, no C deps).
    #[cfg(feature = "candle")]
    candle_encoder: Option<crate::encoder::CandleEncoder>,
    /// v0.10.0: Persistent Cross-Encoder reranker (loaded once at open).
    #[cfg(feature = "onnx")]
    pub(crate) reranker: Option<Reranker>,
    pub(crate) plan_gate: PlanGate,
    /// Timestamp (Unix ms) of last perceive call — for PlanGate time-gap.
    pub(crate) last_perceive_at: i64,
    /// v0.8.0: In-memory auxiliary index for fast plan lookups.
    pub(crate) plan_index: RefCell<PlanIndex>,

    /// Recall buffer: IDs recalled since last Dream, for reconsolidation.
    pub(crate) recalled_buffer: RefCell<Vec<String>>,
    /// v0.9.0: Hot engram cache for recall speed optimization.
    pub(crate) engram_cache: RefCell<EngramCache>,
    /// v0.11.0: Track last chunk per knowledge tree for CoShelf edge creation.
    pub(crate) last_chunk_per_tree: HashMap<String, String>,
    /// v0.12.0: Active context tracking set.
    pub(crate) active_contexts: ActiveContextSet,
    /// v0.13.0: Dormant context pool for three-stage context lifecycle.
    pub(crate) dormant_contexts: DormantContextPool,
    /// v0.13.0: Pending tree edge strengthens from recall (consumed by perceive).
    pub pending_tree_edges: RefCell<Vec<PendingTreeEdge>>,
    /// v0.12.0: Current memory processing phase.
    pub(crate) phase: Phase,
}

impl Brain {
    // ── open ──────────────────────────────────────────────

    /// 打开或创建 Brain。
    pub fn open(
        path: &str,
        config: BrainConfig,
        llm: Option<Box<dyn LlmProvider>>,
    ) -> Result<Self> {
        init::open(path, config, llm)
    }

    // ── perceive ──────────────────────────────────────────

    /// 存入新感知到 Hippocampus。同步，<1ms。
    pub fn perceive(&mut self, input: PerceptionInput) -> Result<PerceptionOutput> {
        crate::perceive::perceive(self, input)
    }

    // ── PGT recall (v0.8.0) — 委托到 recall/ 模块 ───

    /// Four-layer Plan-Gated Temporal recall.
    ///
    /// Returns (results sorted by score descending, layer name).
    /// Layers are tried in order L0→L3, accumulating until `need` is met.
    #[allow(dead_code)]
    fn pgt_recall(
        &self,
        query_text: &str,
        query_emb: &[f32],
        req: &RecallRequest,
    ) -> (Vec<(String, f32)>, Option<String>) {
        crate::recall::pgt::pgt_recall(self, query_text, query_emb, req)
    }

    /// L0: Plan-scoped n-gram — trigram Jaccard overlap within the plan's engrams.
    #[expect(dead_code)]
    fn recall_layer0(
        &self,
        query_text: &str,
        candidates: &[String],
        need: usize,
    ) -> Result<Vec<(String, f32)>> {
        crate::recall::pgt::recall_layer0(self, query_text, candidates, need)
    }

    /// L1: Graph BFS — expand from seed IDs using graph edges.
    #[expect(dead_code)]
    fn recall_layer1(
        &self,
        _query_emb: &[f32],
        seeds: &[(String, f32)],
        need: usize,
        exclude: &HashSet<String>,
    ) -> Vec<(String, f32)> {
        crate::recall::pgt::recall_layer1(self, _query_emb, seeds, need, exclude)
    }

    /// L2: Temporal recency — most recent engrams in the active plan.
    #[expect(dead_code)]
    fn recall_layer2(
        &self,
        active_plan_id: &str,
        need: usize,
        exclude: &HashSet<String>,
    ) -> Result<Vec<(String, f32)>> {
        crate::recall::pgt::recall_layer2(self, active_plan_id, need, exclude)
    }

    /// L3: Global n-gram fallback — scan all engrams (not just active plan).
    #[expect(dead_code)]
    fn recall_layer3(
        &self,
        query_text: &str,
        need: usize,
        exclude: &HashSet<String>,
    ) -> Result<Vec<(String, f32)>> {
        crate::recall::pgt::recall_layer3(self, query_text, need, exclude)
    }

    /// Hopfield fallback: recall among candidates within the active plan.
    #[allow(dead_code)]
    fn hopfield_candidates_in_plan(
        &self,
        query_emb: &[f32],
        plan_id: &str,
        top_k: usize,
        exclude: &HashSet<String>,
    ) -> Vec<(String, f32)> {
        crate::recall::pgt::hopfield_candidates_in_plan(self, query_emb, plan_id, top_k, exclude)
    }

    // ── recall ────────────────────────────────────────────

    /// v0.12.0: Encode text to f16 vector. Candle is the primary encoder;
    /// NgramEncoder fallback is used when allow_fallback_encoder=true (tests).
    pub fn encode_text(&self, text: &str) -> Vec<half::f16> {
        #[cfg(feature = "candle")]
        if let Some(ref candle) = self.candle_encoder {
            return candle.encode(text).dense;
        }
        // Fallback: NgramEncoder (used in tests with allow_fallback_encoder=true).
        // In production, Brain::open() requires Candle, so this path is only hit in tests.
        self.ngram_encoder.encode(text).dense
    }

    /// 召回。p99 < 2ms @ 100K。
    pub fn recall(&self, req: &RecallRequest) -> Result<RecallResponse> {
        crate::recall::associative::recall_associative(self, req)
    }

    /// v0.9.0: Retrieval mode — HNSW + RRF fusion.
    ///
    /// Returns items sorted by Reciprocal Rank Fusion score (k=60)
    /// combining HNSW cosine rank + SparseIndex ngram rank.
    #[allow(dead_code)]
    fn recall_retrieval(
        &self,
        req: &RecallRequest,
        query_vector: &[f16],
        start: std::time::Instant,
    ) -> Result<RecallResponse> {
        crate::recall::retrieval::recall_retrieval(self, req, query_vector, start)
    }

    /// v0.12.0: 从书架知识树中检索附带知识。
    ///
    /// 使用 HNSW 搜索 Knowledge engrams，应用余弦阈值过滤，
    /// 返回最多 KNOWLEDGE_ATTACH_MAX 条结果。
    #[expect(dead_code)]
    fn recall_knowledge_attached(&self, query: &[f16]) -> Vec<Engram> {
        crate::recall::knowledge::recall_knowledge_attached(self, query)
    }

    // ── v0.12.1: Tree API ──────────────────────────────────

    /// v0.12.1: 创建知识树
    pub fn create_tree(&mut self, name: &str, domain: &str, auto_created: bool) -> Result<Tree> {
        crate::tree::create_tree(self, name, domain, auto_created)
    }

    /// v0.13.0: Compress a context into a knowledge tree.
    /// Auto-creates a tree if the context doesn't have one yet.
    pub fn compress_context(&mut self, ctx_id: &str) -> Result<Option<String>> {
        let ctx = match self.active_contexts.get(ctx_id) {
            Some(c) => c.clone(),
            None => return Ok(None),
        };

        // Find or create tree
        let tree_id = if let Some(ref tid) = ctx.auto_tree_id {
            tid.clone()
        } else {
            let centroid_f16: Vec<half::f16> = ctx.centroid.clone();
            // Try to find a similar existing tree
            let existing = crate::tree::find_similar_tree(self, &centroid_f16, 0.85);
            match existing {
                Some(tid) => tid,
                None => {
                    let summary = if ctx.summary.is_empty() {
                        "未命名话题".to_string()
                    } else {
                        ctx.summary.chars().take(20).collect()
                    };
                    let tree = crate::tree::create_tree(
                        self,
                        &format!("关于{}的对话知识", summary),
                        "conversation",
                        true,
                    )?;
                    tree.id
                }
            }
        };

        // Update context with tree_id
        if let Some(ctx) = self.active_contexts.get_mut(ctx_id) {
            ctx.auto_tree_id = Some(tree_id.clone());
            ctx.last_compressed_at = crate::brain::now_millis();
            ctx.add_tree_relation(&tree_id, 0.5);
        }

        Ok(Some(tree_id))
    }

    /// v0.12.1: 列出所有知识树
    pub fn list_trees(&self) -> Result<Vec<Tree>> {
        crate::tree::list_trees(self)
    }

    /// v0.12.1: 获取单个知识树
    pub fn get_tree(&self, tree_id: &str) -> Result<Option<Tree>> {
        crate::tree::get_tree(self, tree_id)
    }

    /// v0.12.1: 删除知识树（不解绑 engram）
    pub fn delete_tree(&mut self, tree_id: &str) -> Result<()> {
        crate::tree::delete_tree(self, tree_id)
    }

    /// v0.12.1: 将 engram 移动到指定树
    pub fn move_to_tree(&mut self, engram_id: &str, tree_id: &str) -> Result<()> {
        crate::tree::move_to_tree(self, engram_id, tree_id)
    }

    // ── reflect ───────────────────────────────────────────

    /// 创建 Reflection 类型 Engram。
    pub fn reflect(&mut self, input: ReflectionInput) -> Result<String> {
        crate::organize::reflect(self, input)
    }

    // ── v0.9.1: Forget / Update ─────────────────────────

    /// Forget all engrams and the DialogueTurn for a given turn_id.
    #[deprecated(note = "use forget_batch with ForgetFilter::ByTurnId")]
    pub fn forget(&mut self, turn_id: &str) -> Result<()> {
        #[allow(deprecated)]
        crate::store::forget(self, turn_id)
    }

    /// Batch delete engrams matching a filter.
    ///
    /// Removes from all indexes: Hopfield, HNSW (soft-delete tombstone),
    /// SparseIndex, UnifiedGraph, LMDB, EngramCache, and last_chunk_per_tree.
    /// HNSW tombstones are persisted to LMDB config after all deletions.
    pub fn forget_batch(&mut self, filter: &ForgetFilter) -> Result<usize> {
        crate::store::forget_batch(self, filter)
    }

    /// Update a turn with new content (forget + perceive).
    pub fn update(&mut self, turn_id: &str, input: PerceptionInput) -> Result<PerceptionOutput> {
        crate::query::update(self, turn_id, input)
    }

    /// List all schema engrams with their metadata.
    pub fn list_schemas(&self) -> Result<Vec<(Engram, crate::engram::SchemaExtra)>> {
        crate::query::list_schemas(self)
    }

    // ── dream ─────────────────────────────────────────────

    /// 执行 Dream 整合（6 阶段）。
    /// 由 Agent 层通过 memhop_dream MCP 工具触发。
    /// 策略: 增量处理，每次处理一批新记忆，多轮逐步覆盖
    pub fn dream(&mut self) -> Result<DreamReport> {
        crate::dream::dream(self)
    }

    // ── v0.12.1: 纠缠事件查询（委托到 entanglement 模块） ─

    /// v0.12.1: 获取所有纠缠事件
    pub fn get_all_entanglements(&self) -> Result<Vec<EntanglementEvent>> {
        crate::entanglement::get_all_entanglements(self)
    }

    /// v0.12.1: 获取单个纠缠事件
    pub fn get_entanglement(&self, event_id: &str) -> Result<Option<EntanglementEvent>> {
        crate::entanglement::get_entanglement(self, event_id)
    }

    /// v0.12.1: 获取所有三观模式
    pub fn get_all_worldviews(&self) -> Result<Vec<WorldviewPattern>> {
        crate::worldview::get_all_worldviews(self)
    }

    /// v0.12.1: 获取单个三观模式
    pub fn get_worldview(&self, wv_id: &str) -> Result<Option<WorldviewPattern>> {
        crate::worldview::get_worldview(self, wv_id)
    }

    // ── v0.11.0: 核心写入管线 ─────────────────────────────

    /// Core engram writing pipeline. "LMDB is source of truth, indexes are best-effort."
    ///
    /// Written by both perceive() and store(). store_engram itself does NOT deduplicate;
    /// the caller (store) checks for duplicates first.
    pub(crate) fn store_engram(&mut self, engram: Engram) -> Result<String> {
        crate::store::store_engram(self, engram)
    }

    // ── v0.11.0: 公共 store API ───────────────────────────

    /// Public ADD-only store API.
    /// Returns StoreResult indicating whether stored or duplicate.
    pub fn store(
        &mut self,
        text: &str,
        vector: &[f16],
        kind: EngramKind,
        tree_path: Option<String>,
        source_path: Option<String>,
        source_textunit: Option<String>,
    ) -> Result<StoreResult> {
        crate::store::store(self, text, vector, kind, tree_path, source_path, source_textunit)
    }

    // ── v0.8.0: Plan 管理方法 ─────────────────────────────

    /// 1. Set the name of a plan.
    pub fn set_plan_name(&self, plan_id: &str, name: &str) -> Result<()> {
        crate::organize::set_plan_name(self, plan_id, name)
    }

    /// 2. Get the plan tree. If plan_id is None, returns all root plans.
    ///    If plan_id is Some, returns that plan and all its descendants (flat list).
    pub fn get_plan_tree(
        &self,
        plan_id: Option<&str>,
    ) -> Result<Vec<crate::engram::PlanNode>> {
        crate::organize::get_plan_tree(self, plan_id)
    }

    /// 3. Set the LLM provider for optional Dream-layer enhancement.
    pub fn set_llm(&mut self, llm: Box<dyn LlmProvider>) {
        self.llm = Some(llm);
    }

    /// 4. Complete a plan: change state to Completed, set completed_at,
    ///    optionally generate compressed summary via LLM.
    ///    All-or-nothing transaction semantics.
    pub fn complete_plan(&mut self, plan_id: &str) -> Result<()> {
        crate::organize::complete_plan(self, plan_id)
    }

    /// 5. Compress a plan's dialogue turns into a Knowledge engram and archive the originals.
    ///    v0.12.0: Full compression (delegated to organize::compress).
    pub fn compress_plan(&mut self, plan_id: &str) -> Result<CompressResult> {
        crate::organize::compress_plan(self, plan_id)
    }

    /// 6a. Compress a Version-level plan: recursively merge child summaries.
    pub fn compress_version(&mut self, version_id: &str) -> Result<String> {
        self.compress_level(version_id)
    }

    /// 6b. Compress a MajorVersion-level plan: recursively merge child summaries.
    pub fn compress_major_version(&mut self, major_version_id: &str) -> Result<String> {
        self.compress_level(major_version_id)
    }

    /// Helper: compress a parent plan by collecting child summaries.
    fn compress_level(&mut self, parent_id: &str) -> Result<String> {
        let rtxn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let all = self.storage.get_all_plans(&rtxn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(rtxn);

        let children: Vec<crate::engram::PlanNode> = all.into_iter()
            .filter(|p| p.parent_id.as_deref() == Some(parent_id))
            .collect();

        let merged = if let Some(ref llm) = self.llm {
            let summaries: Vec<&str> = children.iter()
                .filter_map(|c| c.compressed_summary.as_deref())
                .collect();
            if summaries.is_empty() {
                "(no child summaries)".to_string()
            } else {
                let prompt = format!(
                    "Merge the following summaries into one concise summary:\n\n{}\n\nMerged:",
                    summaries.join("\n---\n")
                );
                llm.generate(&prompt, 256).unwrap_or_else(|_| summaries.join(" | "))
            }
        } else {
            children.iter()
                .filter_map(|c| c.compressed_summary.as_deref())
                .collect::<Vec<_>>()
                .join(" | ")
        };

        let mut wtxn = self.storage.begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut plan = self.storage.get_plan(&wtxn, parent_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            .ok_or_else(|| MemHopError::Storage(format!("plan {} not found", parent_id)))?;
        plan.compressed_summary = Some(merged.clone());
        self.storage.put_plan(&mut wtxn, &plan)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;

        Ok(merged)
    }

    /// 7. Get all domain-level plan names (deduplicated).
    pub fn get_all_domains(&self) -> Result<Vec<String>> {
        crate::query::get_all_domains(self)
    }

    /// 8. Get archived dialogue turns for a plan, sorted by timestamp, with pagination.
    pub fn archived_dialogue(
        &self,
        plan_id: &str,
        offset: usize,
        limit: usize,
    ) -> Result<Vec<crate::engram::DialogueTurn>> {
        crate::query::archived_dialogue(self, plan_id, offset, limit)
    }

    /// 9. Randomly sample up to max_turns dialogue turns from a plan.
    pub fn extract_dialogue_sample(
        &self,
        plan_id: &str,
        max_turns: usize,
    ) -> Result<Vec<crate::engram::DialogueTurn>> {
        crate::query::extract_dialogue_sample(self, plan_id, max_turns)
    }

    /// 10. Aggregate tone statistics over a time range.
    pub fn get_tone_aggregates(
        &self,
        start_time: i64,
        end_time: i64,
    ) -> Result<crate::engram::ToneAggregate> {
        crate::query::get_tone_aggregates(self, start_time, end_time)
    }

    /// 11. Get topic distribution across all domain-level plans.
    pub fn get_topic_distribution(
        &self,
    ) -> Result<crate::engram::TopicDistribution> {
        crate::query::get_topic_distribution(self)
    }

    /// 12. Search chat history by n-gram overlap, with optional plan filter and pagination.
    pub fn search_chat_history(
        &self,
        query: &str,
        plan_id: Option<&str>,
        offset: usize,
        limit: usize,
    ) -> Result<Vec<crate::engram::DialogueTurn>> {
        crate::query::search_chat_history(self, query, plan_id, offset, limit)
    }

    // ── 访问器 ───────────────────────────────────────────

    // ── v0.9.0: Save / close ────────────────────────────

    /// Persist HNSW index before the Brain is discarded.
    pub fn close(&self) -> Result<()> {
        crate::query::close(self)
    }

    pub fn cortex_len(&self) -> usize {
        crate::query::cortex_len(self)
    }
    pub fn hippocampus_len(&self) -> usize {
        crate::query::hippocampus_len(self)
    }
    pub fn memory_count(&self) -> usize {
        crate::query::memory_count(self)
    }
    pub fn hopfield_is_empty(&self) -> bool {
        crate::query::hopfield_is_empty(self)
    }
    pub fn hnsw_is_empty(&self) -> bool {
        crate::query::hnsw_is_empty(self)
    }
    pub fn growth_state(&self) -> &GrowthState {
        crate::query::growth_state(self)
    }
    pub fn emotional_context(&self) -> &EmotionalContext {
        crate::query::emotional_context(self)
    }

    /// v0.9.1: Build per-turn hit list and per-session aggregation from associated engrams.
    pub(crate) fn build_turn_hits(
        &self,
        associations: &[Engram],
        score_map: &HashMap<String, f32>,
    ) -> Result<(Vec<crate::types::TurnHit>, Vec<crate::types::SessionScore>)> {
        crate::query::build_turn_hits(self, associations, score_map)
    }
}

// ── v0.9.1: Helper functions ──────────────────────────────────

/// Compute character-level trigram overlap between query and text.
pub(crate) fn ngram_overlap(query: &str, text: &str) -> f32 {
    if query.is_empty() || text.len() < 3 {
        return 0.0;
    }
    let q_trigrams: HashSet<&[u8]> = query.as_bytes().windows(3).collect();
    let t_trigrams: HashSet<&[u8]> = text.as_bytes().windows(3).collect();
    if q_trigrams.is_empty() {
        return 0.0;
    }
    let overlap = q_trigrams.intersection(&t_trigrams).count();
    overlap as f32 / q_trigrams.len() as f32
}

/// Compute cosine similarity between two f32 vectors.
pub(crate) fn cosine_similarity(a: &[f32], b: &[f32]) -> f32 {
    let dot: f32 = a.iter().zip(b.iter()).map(|(x, y)| x * y).sum();
    let norm_a: f32 = a.iter().map(|x| x * x).sum::<f32>().sqrt();
    let norm_b: f32 = b.iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm_a == 0.0 || norm_b == 0.0 {
        return 0.0;
    }
    (dot / (norm_a * norm_b)).clamp(-1.0, 1.0)
}

/// v0.12.0: Compute cosine similarity between two f16 vectors.
#[allow(dead_code)]
fn cosine_similarity_f16(a: &[f16], b: &[f16]) -> f32 {
    let len = a.len().min(b.len());
    if len == 0 {
        return 0.0;
    }
    let mut dot = 0.0f32;
    let mut norm_a = 0.0f32;
    let mut norm_b = 0.0f32;
    for i in 0..len {
        let av = a[i].to_f32();
        let bv = b[i].to_f32();
        dot += av * bv;
        norm_a += av * av;
        norm_b += bv * bv;
    }
    let denom = norm_a.sqrt() * norm_b.sqrt();
    if denom < 1e-10 {
        0.0
    } else {
        dot / denom
    }
}

/// v0.12.0: Build tree context information from knowledge memories.
#[allow(dead_code)]
fn build_tree_contexts(memories: &[Engram]) -> Vec<TreeContext> {
    let mut tree_map: HashMap<String, (String, usize)> = HashMap::new();
    for eng in memories {
        if let Some(ref tp) = eng.tree_path {
            let entry = tree_map.entry(tp.clone()).or_insert_with(|| ("generic".to_string(), 0));
            entry.1 += 1;
        }
    }
    tree_map
        .into_iter()
        .map(|(path, (domain, count))| TreeContext {
            tree_path: path,
            domain,
            source_count: count,
        })
        .collect()
}

// ── ID 生成 ──────────────────────────────────────────────────

pub(crate) fn generate_id() -> String {
    use rand::Rng;
    let mut rng = rand::thread_rng();
    let bytes: [u8; 8] = rng.r#gen();
    let now = now_millis();
    format!(
        "mem_{:016x}_{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}",
        now as u64, bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5], bytes[6], bytes[7],
    )
}

pub(crate) fn now_millis() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .expect("Time went backwards")
        .as_millis() as i64
}
