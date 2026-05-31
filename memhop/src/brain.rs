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
use std::time::Instant;
use std::path::Path;

use half::f16;

use crate::activation;
use crate::cortex::Cortex;
use crate::context::{ActiveContextSet, Phase};
use crate::encoder::{Encoder, NgramEncoder};
#[cfg(feature = "onnx")]
use crate::encoder::reranker::Reranker;
use crate::engram::{
    AssociationKind, CompressResult, DialogueTurn, EmotionalContext, Engram, EngramKind, PlanLevel, PlanNode, PlanState,
    Protection, StyleCompact, ToneMeta,
};
use crate::error::{MemHopError, Result};
use crate::hippocampus::Hippocampus;
use crate::hnsw::HnswIndex;
use crate::hopfield::ModernHopfield;
use crate::index::SparseIndex;
use crate::llm_provider::LlmProvider;
use crate::personality::{GrowthState, Personality};
use crate::plan_gate::{PlanContext, PlanGate, PlanIndex};
use crate::scene_gating::SceneGate;
use crate::schema;
use crate::storage::LmdbStorage;
use crate::storage::CURRENT_SCHEMA;
use crate::tree::{Tree, TreeRef};
use crate::entanglement::{EntanglementEvent, EntanglementTrigger};
use crate::types::{
    BrainConfig, ConflictItem, DreamReport, ForgetFilter, GraphAssociation, InnateSchema,
    PerceptionInput, PerceptionOutput, RecallMode, RecallRequest, RecallResponse, RecallTrace,
    ReflectionInput, StoreResult, StoreStatus, TreeContext,
};
use crate::unified_graph::UnifiedGraph;
use crate::vitality;
use crate::worldview::{PatternCategory, WorldviewPattern};

// ── 常量 ─────────────────────────────────────────────────────

const HOPFIELD_BETA: f32 = 8.0;
const HOPFIELD_TOP_K: usize = 200;

// v0.12.0: 知识自动附带常量
const KNOWLEDGE_ATTACH_LIMIT: usize = 5;
const KNOWLEDGE_ATTACH_MAX: usize = 10;
const KNOWLEDGE_THRESHOLD: f32 = 0.6;

// ── v0.9.0: Engram cache ───────────────────────────────────

/// Bounded FIFO cache for hot engrams, reducing LMDB read latency.
struct EngramCache {
    cache: HashMap<String, Engram>,
    order: VecDeque<String>,
    max_size: usize,
}

impl EngramCache {
    fn new(max_size: usize) -> Self {
        EngramCache {
            cache: HashMap::new(),
            order: VecDeque::new(),
            max_size,
        }
    }

    fn get(&self, id: &str) -> Option<&Engram> {
        self.cache.get(id)
    }

    fn insert(&mut self, id: String, engram: Engram) {
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

    fn remove(&mut self, id: &str) {
        self.cache.remove(id);
        self.order.retain(|x| x != id);
    }
}

// ── Brain ────────────────────────────────────────────────────

/// MemHop Brain — 三层记忆架构的顶层 API。
pub struct Brain {
    cortex: Cortex,
    hippocampus: Hippocampus,
    graph: UnifiedGraph,
    hopfield: ModernHopfield,
    /// v0.9.0: HNSW index for fast approximate nearest neighbor search.
    pub hnsw: HnswIndex,
    /// v0.9.0: Sparse inverted index for ngram-based retrieval (RRF fusion).
    sparse_index: SparseIndex,
    /// v0.9.0: HNSW NodeId → engram string ID reverse mapping.
    hnsw_id_map: HashMap<u64, String>,
    /// v0.9.0: Monotonic counter for HNSW node IDs (replaces hash-based IDs).
    next_node_id: u64,
    pub(crate) storage: Arc<LmdbStorage>,

    emotional_ctx: EmotionalContext,
    growth: GrowthState,
    personality: Personality,
    config: BrainConfig,
    store_count: usize,

    #[allow(dead_code)]
    llm: Option<Box<dyn LlmProvider>>,
    ngram_encoder: NgramEncoder,
    /// v0.12.0: Optional Candle semantic encoder (pure Rust, no C deps).
    #[cfg(feature = "candle")]
    candle_encoder: Option<crate::encoder::CandleEncoder>,
    /// v0.10.0: Persistent Cross-Encoder reranker (loaded once at open).
    #[cfg(feature = "onnx")]
    reranker: Option<Reranker>,
    plan_gate: PlanGate,
    /// Timestamp (Unix ms) of last perceive call — for PlanGate time-gap.
    last_perceive_at: i64,
    /// v0.8.0: In-memory auxiliary index for fast plan lookups.
    plan_index: RefCell<PlanIndex>,

    /// Recall buffer: IDs recalled since last Dream, for reconsolidation.
    recalled_buffer: RefCell<Vec<String>>,
    /// v0.9.0: Hot engram cache for recall speed optimization.
    engram_cache: RefCell<EngramCache>,
    /// v0.11.0: Track last chunk per knowledge tree for CoShelf edge creation.
    last_chunk_per_tree: HashMap<String, String>,
    /// v0.12.0: Active context tracking set.
    active_contexts: ActiveContextSet,
    /// v0.12.0: Current memory processing phase.
    phase: Phase,
}

impl Brain {
    // ── open ──────────────────────────────────────────────

    /// 打开或创建 Brain。
    pub fn open(
        path: &str,
        config: BrainConfig,
        llm: Option<Box<dyn LlmProvider>>,
    ) -> Result<Self> {
        let storage = Arc::new(
            LmdbStorage::open(path)
                .map_err(|e| MemHopError::Storage(e.to_string()))?,
        );

        // v0.11.0: Schema version check — must happen before any data deserialization.
        {
            let rtxn = storage.begin_read()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            let ver: Option<String> = storage.get_config(&rtxn, "schema_version")
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            drop(rtxn);

            match ver {
                Some(ref v) if v == CURRENT_SCHEMA => {
                    // Schema matches — proceed
                }
                Some(ref v) => {
                    // Incompatible old version
                    return Err(MemHopError::IncompatibleSchema {
                        found: v.clone(),
                        expected: CURRENT_SCHEMA,
                        hint: "This version is not backward compatible. Delete the old database or continue using v0.10.x.",
                    });
                }
                None => {
                    // New/empty database — write current version
                    let mut wtxn = storage.begin_write()
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    storage.put_config(&mut wtxn, "schema_version", &CURRENT_SCHEMA.to_string())
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    wtxn.commit()
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                }
            }
        }

        let personality = config.personality;
        let hippocampus = Hippocampus::rebuild(&storage, config.hippocampus_capacity)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        let graph = UnifiedGraph::rebuild(&storage)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        let hopfield = Self::rebuild_hopfield(&storage, config.hopfield.knowledge_pattern_weight)?;

        // v0.9.0: Engram cache for recall speed — bounded FIFO
        let engram_cache = RefCell::new(EngramCache::new(1000));

        // v0.9.0: Load or rebuild HNSW index from storage
        let mut counter: u64 = 0;
        let (mut hnsw, hnsw_id_map) = match HnswIndex::load_from_storage(&storage) {
            Ok(Some(idx)) => {
                let mut id_map = HashMap::new();
                if let Ok(txn) = storage.begin_read()
                    && let Ok(entries) = storage.all_hippocampus_entries(&txn) {
                        for (id_str, _) in &entries {
                            let node_id = counter;
                            counter += 1;
                            id_map.insert(node_id, id_str.clone());
                        }
                    }
                (idx, id_map)
            }
            _ => {
                let mut idx = HnswIndex::new(crate::engram::VECTOR_DIM);
                let mut id_map = HashMap::new();
                if let Ok(txn) = storage.begin_read()
                    && let Ok(entries) = storage.all_hippocampus_entries(&txn) {
                        // v0.9.0: Pre-warm engram cache during HNSW rebuild
                        let engram_cache_ref = &engram_cache;
                        for (id_str, engram) in &entries {
                            let node_id = counter;
                            counter += 1;
                            id_map.insert(node_id, id_str.clone());
                            idx.insert(node_id, &engram.vector);
                            engram_cache_ref.borrow_mut().insert(id_str.clone(), engram.clone());
                        }
                    }
                (idx, id_map)
            }
        };

        // v0.11.0: Restore HNSW tombstones from LMDB config
        {
            if let Ok(rtxn) = storage.begin_read()
                && let Ok(Some(ids)) = storage.get_config::<Vec<u64>>(&rtxn, "hnsw_tombstones")
            {
                for id in ids {
                    hnsw.tombstones.insert(id);
                }
            }
        }

        // v0.9.0: Rebuild sparse index from stored engrams
        let sparse_index = {
            let mut si = SparseIndex::new();
            if let Ok(txn) = storage.begin_read()
                && let Ok(entries) = storage.all_hippocampus_entries(&txn) {
                    let tmp_enc = NgramEncoder::new(crate::engram::VECTOR_DIM);
                    for (id_str, engram) in &entries {
                        let sparse = tmp_enc.encode(&engram.text).sparse;
                        si.add(id_str, &sparse, engram.text.chars().count());
                    }
                }
            si
        };

        if hopfield.is_empty() && !config.innate_schemas.is_empty() {
            let now = now_millis();
            for innate in &config.innate_schemas {
                Self::bootstrap_innate_schema(&storage, &graph, innate, now)?;
            }
        }

        // v0.8.0: Rebuild PlanIndex from stored PlanNodes
        let plan_index = {
            let mut pi = PlanIndex::new();
            let ro_txn = storage
                .begin_read()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            if let Ok(plans) = storage.get_all_plans(&ro_txn) {
                pi.rebuild(&plans);
            }
            pi
        };

        let hnsw_id_map_len = hnsw_id_map.len() as u64;

        // ── v0.12.1: Load Candle encoder — no ONNX, no Ngram fallback ──
        // Candle is a pure Rust BERT/XLM-RoBERTa encoder via safetensors.
        // BGE-M3 (xlm-roberta), BGE-Small/BGE-Base (bert) all work.
        // If onnx_model_path is set but Candle fails, return a hard error.

        let encoder_loaded: bool;
        #[cfg(feature = "candle")]
        let candle_encoder: Option<crate::encoder::CandleEncoder>;
        #[cfg(not(feature = "candle"))]
        let candle_encoder: Option<crate::encoder::CandleEncoder> = None;

        if let Some(model_path) = &config.onnx_model_path {
            let safetensors_path = Path::new(model_path).join("model.safetensors");

            if !safetensors_path.exists() {
                return Err(MemHopError::Internal(format!(
                    "Candle encoder requires model.safetensors in '{}'. File not found.",
                    model_path
                )));
            }

            // Try loading with Candle (supports BERT, XLM-RoBERTa, and similar architectures)
            #[cfg(feature = "candle")]
            {
                eprintln!("memhop: loading Candle encoder from '{}'...", model_path);
                let start = std::time::Instant::now();
                match crate::encoder::CandleEncoder::from_path(model_path) {
                    Ok(enc) => {
                        let elapsed = start.elapsed();
                        eprintln!(
                            "memhop: Candle encoder ready (dim={}, {:.1}s)",
                            enc.dim(),
                            elapsed.as_secs_f64()
                        );
                        candle_encoder = Some(enc);
                        encoder_loaded = true;
                    }
                    Err(e) => {
                        #[allow(unused_assignments)]
                        { candle_encoder = None; encoder_loaded = false; }
                        return Err(MemHopError::Internal(format!(
                            "Candle encoder failed to load from '{}': {}. \
                             Check model files are valid. MemHop does NOT fall back to ONNX or Ngram.",
                            model_path, e
                        )));
                    }
                }
            }
            #[cfg(not(feature = "candle"))]
            {
                return Err(MemHopError::Internal(format!(
                    "Candle encoder feature is not enabled. Build with --features candle. \
                     MemHop does NOT support ONNX or Ngram fallback."
                )));
            }
        } else {
            candle_encoder = None;
            encoder_loaded = false;
            eprintln!("memhop: no external model configured, using built-in NgramEncoder.");
        }

        if encoder_loaded {
            eprintln!("memhop: semantic encoder active — NgramEncoder disabled.");
        }

        // v0.10.0: Initialize reranker from config
        #[cfg(feature = "onnx")]
        let reranker = {
            if let Some(model_path) = &config.reranker_model_path {
                match crate::encoder::reranker::Reranker::from_path(model_path) {
                    Ok(r) => Some(r),
                    Err(e) => {
                        eprintln!(
                            "memhop: failed to load reranker from '{}': {}, reranker disabled",
                            model_path, e
                        );
                        None
                    }
                }
            } else {
                None
            }
        };

        // v0.12.0: Initialize active context set
        let active_contexts = ActiveContextSet::new(
            config.max_active_contexts,
            config.context_match_threshold,
            config.context_half_life_hours,
        );

        let brain = Brain {
            cortex: Cortex::new(config.cortex_capacity),
            hippocampus,
            graph,
            hopfield,
            hnsw,
            sparse_index,
            hnsw_id_map,
            next_node_id: hnsw_id_map_len,
            storage,
            emotional_ctx: EmotionalContext::new(),
            growth: GrowthState::new(),
            personality,
            store_count: 0,
            llm,
            ngram_encoder: NgramEncoder::new(crate::engram::VECTOR_DIM),
            #[cfg(feature = "candle")]
            candle_encoder,
            #[cfg(feature = "onnx")]
            reranker,
            plan_gate: PlanGate::new(
                config.plan_boundary_threshold.unwrap_or(0.55),
                3,
                24,
            ),
            plan_index: RefCell::new(plan_index),
            config,
            recalled_buffer: RefCell::new(Vec::new()),
            engram_cache: RefCell::new(EngramCache::new(1000)),
            last_chunk_per_tree: HashMap::new(),
            last_perceive_at: 0,
            phase: Phase::Warmup,
            active_contexts,
        };

        // Warm up the encoder (ONNX/Candle) at startup to avoid
        // long delay on the first perceive() call.
        brain.warmup_encoder();

        Ok(brain)
    }

    /// Warm up the ONNX/Candle encoder by encoding a short text.
    /// This forces ONNX Runtime session compilation at startup
    /// rather than on the first perceive() call.
    fn warmup_encoder(&self) {
        #[cfg(any(feature = "onnx", feature = "candle"))]
        {
            let warmup_start = std::time::Instant::now();
            let _ = self.encode_text("warmup");
            let elapsed = warmup_start.elapsed();
            if elapsed.as_secs_f32() > 0.1 {
                eprintln!("memhop: encoder warmup completed in {:.1}s", elapsed.as_secs_f32());
            }
        }
    }

    // ── perceive ──────────────────────────────────────────

    /// 存入新感知到 Hippocampus。同步，<1ms。
    pub fn perceive(&mut self, input: PerceptionInput) -> Result<PerceptionOutput> {
        let now = now_millis();
        let id = generate_id();
        // v0.9.1: Auto-generate turn_id if empty
        let turn_id = if input.turn_id.is_empty() {
            format!("turn_{}_{}", now, input.turn_index)
        } else {
            input.turn_id.clone()
        };

        self.emotional_ctx
            .update(input.emotional_state.valence, input.emotional_state.arousal);

        // ── v0.8.0: Plan-gating — PlanGate boundary detection & decision ──

        // 1. Convert embedding to f32 for PlanGate
        let query_f32: Vec<f32> = input.vector.iter().map(|x| x.to_f32()).collect();

        // 2. Get plan centroid from PlanIndex (may be None for new brain)
        let plan_centroid: Option<Vec<f32>> = {
            let idx = self.plan_index.borrow();
            idx.active_plan_id
                .as_ref()
                .and_then(|pid| {
                    idx.centroids
                        .get(pid)
                        .map(|c| c.iter().map(|x: &half::f16| x.to_f32()).collect())
                })
        };

        // 3. Time gap since last perceive (minutes)
        let time_gap_minutes = if self.last_perceive_at > 0 {
            ((now - self.last_perceive_at).max(0) as f64) / 60_000.0
        } else {
            0.0
        };
        self.last_perceive_at = now;

        // 4. Extract user tone from text (rule-based, no LLM)
        let current_tone = crate::tone_extractor::extract_tone(&input.content);

        // 5. Compute boundary score
        let boundary = self.plan_gate.boundary_score(
            &query_f32,
            &current_tone,
            &input.attention_anchors,
            PlanContext {
                centroid: plan_centroid.as_deref(),
                avg_tone: None,
                anchors: &[],
            },
            time_gap_minutes,
        );

        // ── v0.12.0: 保存旧 plan_id 用于 compress ──
        let old_plan_id = self.plan_index.borrow().active_plan_id.clone().unwrap_or_default();

        // 6. Match to plan
        let matched_plan = self.plan_gate.match_to_plan(
            input.plan_id.as_deref(),
            &self.plan_index.borrow(),
            &query_f32,
            boundary,
        );

        // 7. Determine plan_id: explicit match → use it; otherwise anonymous
        let plan_id = matched_plan.unwrap_or_else(|| format!("plan_{}", now));

        // 8. Decide plan hint (accumulates boundary scores over rounds)
        let plan_hint = self.plan_gate.decide(boundary, now);

        // 9. Plan name from explicit input or default
        let plan_name = input
            .plan_id
            .as_deref()
            .unwrap_or("Unnamed Plan")
            .to_string();

        // ── v0.12.0: Full 模式下边界检测 → 自动压缩 ──
        if self.phase == Phase::Full && plan_hint == crate::engram::PlanHint::NewTopicLikely
            && !old_plan_id.is_empty() && old_plan_id != plan_id {
            let _ = self.compress_plan(&old_plan_id);
        }

        // ── v0.8.0: Populate PlanIndex ──
        {
            let mut pi = self.plan_index.borrow_mut();
            pi.add_engram(&plan_id, &id);
            pi.update_centroid(&plan_id, &query_f32);
            if pi.active_plan_id.is_none() {
                pi.active_plan_id = Some(plan_id.clone());
            }
        }

        // ── v0.12.0: Phase 判断 ──
        let phase = if self.growth.total_perceptions < self.config.warmup_rounds as u64 {
            Phase::Warmup
        } else if self.growth.total_perceptions < (self.config.warmup_rounds as u64) * 2 {
            Phase::Early
        } else {
            Phase::Full
        };
        self.phase = phase;

        // ── v0.12.0: 活跃上下文匹配（Warmup 不做） ──
        let mut matched_ctx_id: Option<String> = None;
        let mut matched_tree_id: Option<String> = None; // v0.12.1
        if self.phase != Phase::Warmup {
            if let Some(ctx) = self.active_contexts.match_context(&query_f32, now) {
                matched_ctx_id = Some(ctx.id.clone());
                matched_tree_id = ctx.tree_id.clone(); // v0.12.1: capture tree_id from context
            } else {
                // 没有匹配到上下文 → 使用 PlanGate 的结果创建新上下文
                let tree_id = None; // 将来可从 identify_tree 获取
                self.active_contexts.create(tree_id, plan_id.clone(), input.vector.clone(), now);
            }
            // 淘汰过期的上下文
            self.active_contexts.evict_stale();
        }

        // v0.12.1: 从匹配上下文的 tree_id 查找 Tree，构建 tree_ref
        let engram_tree_ref: Option<TreeRef> = if let Some(ref tid) = matched_tree_id {
            self.get_tree(tid).ok().flatten().map(|tree| TreeRef {
                tree_id: tree.id,
                tree_name: tree.name,
                tree_domain: tree.domain,
            })
        } else {
            None
        };

        // Save data for DialogueTurn before input is consumed
        let saved_content = input.content.clone();
        let saved_vector = input.vector.clone();
        let saved_agent_response = input.agent_response.clone();
        let saved_dialogue_timestamp = input.dialogue_timestamp;

        // ── v0.9.1: Long text segmentation ──
        const MAX_SEGMENT_CHARS: usize = 5000;
        let segments: Vec<String> = if saved_content.len() > MAX_SEGMENT_CHARS {
            split_text_at_boundaries(&saved_content, MAX_SEGMENT_CHARS)
        } else {
            vec![saved_content.clone()]
        };
        let segment_count = segments.len() as u32;
        let text_was_split = segments.len() > 1;

        // ── Create engrams (one per segment) ──
        let mut engram_ids: Vec<String> = Vec::new();
        for (seg_idx, segment_text) in segments.iter().enumerate() {
            let seg_id = if seg_idx == 0 {
                id.clone()
            } else {
                generate_id()
            };

            // Re-encode per segment if text was split, else use original vector
            let seg_vector = if text_was_split {
                self.encode_text(segment_text)
            } else {
                input.vector.clone()
            };

            let engram = Engram {
                id: seg_id.clone(),
                text: segment_text.clone(),
                summary: None,
                vector: seg_vector,
                keywords: Vec::new(),
                content_type: None,
                valence: input.emotional_state.valence,
                arousal: input.emotional_state.arousal,
                vitality: 1.0,
                protection: Protection::Normal,
                created_at: now,
                last_activated: now,
                activation_count: 1,
                kind: EngramKind::Episode,
                meta: HashMap::new(),
                is_archived: false,
                is_dormant: false,
                turn_id: Some(turn_id.clone()),
                tree_path: None,
                source_path: None,
                source_textunit: None,
                turn_ids: Vec::new(),
                context_id: matched_ctx_id.clone(),
                tree_ref: engram_tree_ref.clone(),
            };

            self.cortex.push(engram.clone(), &input.session_id);
            // v0.11.0: Use store_engram for unified index writes (LMDB, HNSW, Hopfield, SparseIndex, cache)
            self.store_engram(engram)?;
            engram_ids.push(seg_id);
        }

        // 建立时间边（与 Hippocampus 中最近 3 条），只连接最后一个 segment
        let last_seg_id = engram_ids.last().cloned().unwrap_or_default();
        let recent_entries = self
            .hippocampus
            .batch_entries(&self.storage, self.hippocampus.len().saturating_sub(4), 3)?;
        for (recent_id, _) in &recent_entries {
            if recent_id.as_str() != last_seg_id.as_str() {
                self.graph.add_edge(
                    &self.storage, &last_seg_id, recent_id, 0.5, AssociationKind::Temporal, now,
                )?;
                self.graph.add_edge(
                    &self.storage, recent_id, &last_seg_id, 0.5, AssociationKind::Temporal, now,
                )?;
            }
        }

        self.growth.total_perceptions += 1;

        // 记录到 Anchor 索引（所有 segment 都关联）
        if !input.attention_anchors.is_empty() {
            for seg_id in &engram_ids {
                let _ = SceneGate::add_to_anchors(&self.storage, seg_id, &input.attention_anchors);
            }
        }

        // Parse turn source
        let turn_source = input.source.as_deref()
            .map(parse_turn_source)
            .unwrap_or(crate::engram::TurnSource::User);

        // ── v0.8.0: Persist PlanNode & DialogueTurn ──
        {
            // Persist PlanNode to LMDB (upsert pattern)
            let plan = PlanNode {
                id: plan_id.clone(),
                parent_id: None,
                name: plan_name.clone(),
                level: PlanLevel::Plan,
                centroid_vector: query_f32.iter().map(|&x| f16::from_f32(x)).collect(),
                dialogue_count: 1,
                compressed_summary: None,
                state: PlanState::Active,
                created_at: now,
                completed_at: None,
                meta: HashMap::new(),
            };
            let mut txn = self.storage
                .begin_write()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            self.storage
                .put_plan(&mut txn, &plan)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;

            // v0.11.0: Always create DialogueTurn for session-level aggregation,
            // regardless of whether agent_response is present.
            let turn = DialogueTurn {
                id: turn_id.clone(),
                plan_id: plan_id.clone(),
                user_input: saved_content.clone(),
                agent_response: saved_agent_response.clone().unwrap_or_default(),
                user_tone: current_tone,
                agent_tone: ToneMeta {
                    valence: 0.0,
                    arousal: 0.0,
                    tone_tags: vec![],
                    filler_ratio: 0.0,
                    sentence_style: StyleCompact {
                        avg_sentence_len: 0.0,
                        question_ratio: 0.0,
                        exclamation_count: 0,
                    },
                },
                timestamp: saved_dialogue_timestamp.unwrap_or(now),
                vector: saved_vector.clone(),
                session_id: input.session_id.clone(),
                turn_index: input.turn_index,
                segment_count,
                source: turn_source,
                topic_label: input.topic_label.clone(),
            };
            self.storage
                .put_dialogue(&mut txn, &turn)
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;

            txn.commit()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
        }

        if self.store_count >= self.config.dream_interval {
            self.store_count = 0;
            let _ = self.dream_internal();
        }

        Ok(PerceptionOutput {
            engram_id: id,
            current_plan_id: plan_id,
            plan_hint,
            plan_name,
            context_id: matched_ctx_id,
            phase: format!("{}", self.phase),
        })
    }

    // ── PGT recall (v0.8.0) ────────────────────────────

    /// Four-layer Plan-Gated Temporal recall.
    ///
    /// Returns (results sorted by score descending, layer name).
    /// Layers are tried in order L0→L3, accumulating until `need` is met.
    fn pgt_recall(
        &self,
        query_text: &str,
        query_emb: &[f32],
        req: &RecallRequest,
    ) -> (Vec<(String, f32)>, Option<String>) {
        let plan_id = match &req.active_plan_id {
            Some(pid) => pid,
            None => return (Vec::new(), None),
        };
        let need = req.limit;
        let mut results: Vec<(String, f32)> = Vec::new();
        let mut exclude: HashSet<String> = HashSet::new();

        // L0: Plan-scoped n-gram search
        let plan_candidates = self.plan_index.borrow().candidates(Some(plan_id));
        if let Ok(l0) = self.recall_layer0(query_text, &plan_candidates, need) {
            for (id, _) in &l0 { exclude.insert(id.clone()); }
            results.extend(l0);
        }
        if results.len() >= need {
            return (results, Some("L0".to_string()));
        }

        // L1: Graph BFS from L0 seeds
        let l1 = self.recall_layer1(query_emb, &results, need - results.len(), &exclude);
        for (id, _) in &l1 { exclude.insert(id.clone()); }
        results.extend(l1);
        if results.len() >= need {
            return (results, Some("L1".to_string()));
        }

        // L2: Temporal recency
        if let Ok(l2) = self.recall_layer2(plan_id, need - results.len(), &exclude) {
            for (id, _) in &l2 { exclude.insert(id.clone()); }
            results.extend(l2);
        }
        if results.len() >= need {
            return (results, Some("L2".to_string()));
        }

        // L3: Global n-gram fallback
        if let Ok(l3) = self.recall_layer3(query_text, need - results.len(), &exclude) {
            results.extend(l3);
        }

        let layer = if results.is_empty() { "None" } else { "L3" };
        (results, Some(layer.to_string()))
    }

    /// L0: Plan-scoped n-gram — trigram Jaccard overlap within the plan's engrams.
    fn recall_layer0(
        &self,
        query_text: &str,
        candidates: &[String],
        need: usize,
    ) -> Result<Vec<(String, f32)>> {
        if candidates.is_empty() || need == 0 {
            return Ok(Vec::new());
        }
        let txn = self
            .storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        let mut scored: Vec<(String, f32)> = Vec::with_capacity(candidates.len().min(need * 4));
        for id in candidates.iter().take(candidates.len().min(need * 4)) {
            if let Ok(Some(engram)) = self.storage.get_hippocampus(&txn, id) {
                let score = ngram_overlap(query_text, &engram.text);
                if score > 0.0 {
                    scored.push((id.clone(), score));
                }
            }
        }
        drop(txn);

        scored.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        scored.truncate(need);
        Ok(scored)
    }

    /// L1: Graph BFS — expand from seed IDs using graph edges.
    fn recall_layer1(
        &self,
        _query_emb: &[f32],
        seeds: &[(String, f32)],
        need: usize,
        exclude: &HashSet<String>,
    ) -> Vec<(String, f32)> {
        if seeds.is_empty() || need == 0 {
            return Vec::new();
        }
        let mut neighbor_scores: HashMap<String, f32> = HashMap::new();

        for (seed_id, seed_score) in seeds {
            for edge in self.graph.edges_of(seed_id) {
                if exclude.contains(&edge.target_id) || edge.target_id == *seed_id {
                    continue;
                }
                let score = edge.weight * seed_score;
                let entry = neighbor_scores.entry(edge.target_id.clone()).or_insert(0.0);
                *entry = entry.max(score);
            }
        }

        let mut scored: Vec<(String, f32)> = neighbor_scores.into_iter().collect();
        scored.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        scored.truncate(need);
        scored
    }

    /// L2: Temporal recency — most recent engrams in the active plan.
    fn recall_layer2(
        &self,
        active_plan_id: &str,
        need: usize,
        exclude: &HashSet<String>,
    ) -> Result<Vec<(String, f32)>> {
        if need == 0 {
            return Ok(Vec::new());
        }
        let candidates = self.plan_index.borrow().candidates(Some(active_plan_id));
        if candidates.is_empty() {
            return Ok(Vec::new());
        }

        let txn = self
            .storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        let now = now_millis();
        let mut with_times: Vec<(String, f32)> = Vec::with_capacity(candidates.len());

        for id in &candidates {
            if exclude.contains(id) {
                continue;
            }
            if let Ok(Some(engram)) = self.storage.get_hippocampus(&txn, id) {
                let hours_ago = ((now - engram.created_at).max(0) as f64) / 3_600_000.0;
                let recency = 1.0f64 / (1.0 + hours_ago / 24.0);
                with_times.push((id.to_string(), recency as f32));
            }
        }
        drop(txn);

        with_times.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        with_times.truncate(need);
        Ok(with_times)
    }

    /// L3: Global n-gram fallback — scan all engrams (not just active plan).
    fn recall_layer3(
        &self,
        query_text: &str,
        need: usize,
        exclude: &HashSet<String>,
    ) -> Result<Vec<(String, f32)>> {
        if need == 0 {
            return Ok(Vec::new());
        }
        let candidates = self.plan_index.borrow().candidates(None);
        let filtered: Vec<String> = candidates
            .into_iter()
            .filter(|id| !exclude.contains(id))
            .collect();
        self.recall_layer0(query_text, &filtered, need)
    }

    /// Hopfield fallback: recall among candidates within the active plan.
    fn hopfield_candidates_in_plan(
        &self,
        query_emb: &[f32],
        plan_id: &str,
        top_k: usize,
        exclude: &HashSet<String>,
    ) -> Vec<(String, f32)> {
        if self.hopfield.is_empty() {
            return Vec::new();
        }
        let candidates = self.plan_index.borrow().candidates(Some(plan_id));
        let candidate_refs: Vec<&str> = candidates.iter().map(|s: &String| s.as_str()).collect();

        self.hopfield
            .recall_among_raw(query_emb, &candidate_refs)
            .into_iter()
            .filter(|(id, _)| !exclude.contains(id))
            .take(top_k)
            .collect()
    }

    // ── recall ────────────────────────────────────────────

    /// v0.12.0: Encode text to f16 vector. Prefers Candle → ONNX → NgramEncoder.
    pub fn encode_text(&self, text: &str) -> Vec<half::f16> {
        #[cfg(feature = "candle")]
        if let Some(ref candle) = self.candle_encoder {
            return candle.encode(text).dense;
        }
        self.ngram_encoder.encode(text).dense
    }

    /// 召回。p99 < 2ms @ 100K。
    pub fn recall(&self, req: &RecallRequest) -> Result<RecallResponse> {
        let start = Instant::now();

        // 1. Query vector

        // 1. Query vector
        let query_vector: Vec<f16> = match &req.query_vector {
            Some(v) => v.clone(),
            None => self.encode_text(&req.query),
        };
        let query_f32: Vec<f32> = query_vector.iter().map(|&x| x.to_f32()).collect();

        // v0.9.0: Mode dispatch — Retrieval mode uses HNSW + RRF fusion
        match req.mode {
            RecallMode::Retrieval => {
                return self.recall_retrieval(req, &query_vector, start);
            }
            RecallMode::Associative => {
                // Fall through to existing recall path
            }
        }

        // 2. L0 Cortex — 工作记忆
        let working_memory = self.cortex.recent(&req.session_id, req.recent_limit);

        // 3. PGT recall (v0.8.0: four-layer plan-gated retrieval)
        let (pgt_results, pgt_layer) = if req.active_plan_id.is_some() {
            self.pgt_recall(&req.query, &query_f32, req)
        } else {
            (Vec::new(), None)
        };

        // 4. Build seeds — PGT-first, Hopfield fallback
        let mut hopfield_count: usize = 0;
        let seeds: HashMap<String, f32> = if pgt_results.len() >= req.limit {
            // PGT produced enough — skip Hopfield entirely
            pgt_results
                .into_iter()
                .take(req.spread_top_k * 2)
                .collect()
        } else if let Some(ref plan_id) = req.active_plan_id {
            // PGT not enough — supplement with Hopfield in plan scope
            let exclude: HashSet<String> =
                pgt_results.iter().map(|(id, _): &(String, f32)| id.clone()).collect();
            let remaining = req.spread_top_k * 2 - pgt_results.len();
            let hopfield_supp = self.hopfield_candidates_in_plan(
                &query_f32, plan_id, remaining, &exclude,
            );
            hopfield_count = hopfield_supp.len();
            pgt_results
                .into_iter()
                .chain(hopfield_supp)
                .take(req.spread_top_k * 2)
                .collect()
        } else {
            // No active plan — classic Hopfield top-K path
            let hopfield_candidates: Vec<(String, f32)> = if self.hopfield.is_empty() {
                Vec::new()
            } else {
                self.hopfield.recall_topk(&query_f32, HOPFIELD_TOP_K)
            };

            // Scene gating
            let hopfield_candidates = if !req.attention_anchors.is_empty() {
                if let Ok(Some(candidates)) =
                    SceneGate::get_candidates(&self.storage, &req.attention_anchors)
                {
                    hopfield_candidates
                        .into_iter()
                        .filter(|(id, _)| candidates.contains(id))
                        .collect()
                } else {
                    hopfield_candidates
                }
            } else {
                hopfield_candidates
            };
            hopfield_count = hopfield_candidates.len();
            hopfield_candidates
                .into_iter()
                .take(req.spread_top_k * 2)
                .collect()
        };

        // 5. 竞争性扩散激活（内部包含矛盾抑制）
        let spread_result = activation::competitive_spread(
            &self.graph, &seeds, &self.personality, req.spread_top_k,
        );
        // v0.9.1: Capture scores for turn-level aggregation
        let score_map: HashMap<String, f32> = spread_result.activated.iter().cloned().collect();

        // 6. 从存储加载激活的 Engram，按类型分类
        let mut associations: Vec<Engram> = Vec::new();
        let mut schemas: Vec<Engram> = Vec::new();
        let mut emotional_echoes: Vec<Engram> = Vec::new();
        let mut knowledge_memories: Vec<Engram> = Vec::new();
        let mut conflicts: Vec<ConflictItem> = Vec::new();

        if let Ok(rtxn) = self.storage.begin_read() {
            let activated_ids: Vec<String> = spread_result.activated.iter().map(|(id, _)| id.clone()).collect();
            for id in &activated_ids {
                // v0.9.0: Try cache first, fall back to storage
                let engram = self.engram_cache.borrow().get(id).cloned();
                let engram = match engram {
                    Some(e) => e,
                    None => {
                        if let Ok(Some(e)) = self.storage.get_hippocampus(&rtxn, id) {
                            self.engram_cache.borrow_mut().insert(id.clone(), e.clone());
                            e
                        } else {
                            continue;
                        }
                    }
                };
                // v0.11.0: Apply kind_filter
                if !req.kind_filter.is_empty() && !req.kind_filter.contains(&engram.kind) {
                    continue;
                }
                // v0.11.0: Apply tree filter
                if let Some(ref tree_path) = req.tree
                    && engram.kind == EngramKind::Knowledge
                    && engram.tree_path.as_deref() != Some(tree_path.as_str())
                {
                    continue;
                }
                // v0.12.1: Apply tree_id filter (via tree_ref)
                if let Some(ref tree_id) = req.tree_id
                    && engram.tree_ref.as_ref().map(|tr| &tr.tree_id) != Some(tree_id)
                {
                    continue;
                }
                // v0.12.0: Apply time filter
                if req.time_from.is_some() || req.time_to.is_some() {
                    let after = req.time_from.is_none_or(|t| engram.created_at >= t);
                    let before = req.time_to.is_none_or(|t| engram.created_at <= t);
                    if !(after && before) {
                        continue;
                    }
                }
                match engram.kind {
                    EngramKind::Knowledge => knowledge_memories.push(engram),
                    EngramKind::Schema => schemas.push(engram),
                    _ => {
                        // 高 arousal 记忆标记为情绪回声
                        if engram.arousal > 0.7 {
                            emotional_echoes.push(engram.clone());
                        }
                        associations.push(engram);
                    }
                }
            }

            // 7b. 情绪对齐排序: 与当前情绪状态一致度高的记忆优先
            associations.sort_by(|a, b| {
                let score_a = activation::emotional_alignment(
                    req.emotional_state.valence,
                    req.emotional_state.arousal,
                    a,
                );
                let score_b = activation::emotional_alignment(
                    req.emotional_state.valence,
                    req.emotional_state.arousal,
                    b,
                );
                score_b.partial_cmp(&score_a).unwrap_or(std::cmp::Ordering::Equal)
            });

            // 8. 从激活集中检测矛盾对
            let id_set: HashSet<String> = activated_ids.into_iter().collect();
            for (a, b) in self.graph.contradiction_pairs_in(&id_set) {
                conflicts.push(ConflictItem {
                    memory_a_id: a,
                    memory_b_id: b,
                    conflict_type: "contradiction".to_string(),
                });
            }
        }

        // v0.11.0: Build tree_contexts from knowledge_memories
        let mut tree_contexts: Vec<TreeContext> = Vec::new();
        for e in &knowledge_memories {
            if let Some(ref tree_path) = e.tree_path {
                let domain = e.meta.get("domain")
                    .and_then(|v| v.as_str())
                    .unwrap_or("generic");
                if !tree_contexts.iter().any(|tc: &TreeContext| tc.tree_path == *tree_path) {
                    let source_count = knowledge_memories.iter()
                        .filter(|ke| ke.tree_path.as_deref() == Some(tree_path.as_str()))
                        .count();
                    tree_contexts.push(TreeContext {
                        tree_path: tree_path.clone(),
                        domain: domain.to_string(),
                        source_count,
                    });
                }
            }
        }

        // v0.11.0: Build graph_associations from top results
        let mut graph_associations: Vec<GraphAssociation> = Vec::new();
        let mut seen: HashSet<String> = HashSet::new();
        for (id, _score) in &spread_result.activated {
            let edges = self.graph.edges_of(id);
            for edge in edges {
                let pair_key = if *id < edge.target_id {
                    format!("{}|{}", id, edge.target_id)
                } else {
                    format!("{}|{}", edge.target_id, id)
                };
                if seen.contains(&pair_key) { continue; }
                seen.insert(pair_key);

                if edge.kind == AssociationKind::CoShelf {
                    graph_associations.push(GraphAssociation {
                        source_id: id.clone(),
                        target_id: edge.target_id.clone(),
                        kind: edge.kind.clone(),
                        weight: edge.weight,
                        description: "CoShelf: same knowledge tree".to_string(),
                    });
                }
            }
        }

        // 8. 记录被召回的记忆 ID，供 Dream reconsolidation 处理
        {
            let mut buf = self.recalled_buffer.borrow_mut();
            for (id, _) in &spread_result.activated {
                if !buf.contains(id) {
                    buf.push(id.clone());
                }
            }
        }

        // v0.12.1: 检测跨树命中 → 创建纠缠事件
        if self.phase == Phase::Full {
            let mut tree_ids_set: HashSet<String> = HashSet::new();
            let mut node_ids: Vec<String> = Vec::new();
            for eng in associations.iter().chain(knowledge_memories.iter()) {
                if let Some(ref tr) = eng.tree_ref {
                    tree_ids_set.insert(tr.tree_id.clone());
                    node_ids.push(eng.id.clone());
                }
            }
            if tree_ids_set.len() >= 2 && node_ids.len() >= 2 {
                let context = "记忆在查询中跨树关联".to_string();
                let tree_ids: Vec<String> = tree_ids_set.into_iter().collect();
                self.create_or_update_entanglement(
                    node_ids, tree_ids, context, EntanglementTrigger::RecallCrossTree,
                );
            }
        }

        // v0.12.1: 展开纠缠事件节点 — 将 strength > 0.5 的关联节点加入结果
        self.expand_entangled_results(&mut associations);

        // v0.12.1: 三观模式介入
        let (worldview_context, cognitive_conflicts) = self.extract_worldview_context(&req.query);

        // v0.9.1: Build turn-level hits from associated engrams
        let (hit_turns, aggregated_sessions) = self.build_turn_hits(&associations, &score_map)
            .unwrap_or_default();

        let latency_us = start.elapsed().as_micros() as u64;

        Ok(RecallResponse {
            working_memory,
            associations,
            schemas,
            emotional_echoes,
            conflicts,
            archive_results: None,
            hit_turns,
            aggregated_sessions,
            knowledge_memories,
            tree_contexts,
            graph_associations,
            worldview_context,
            cognitive_conflicts,
            trace: RecallTrace {
                latency_us,
                gated_anchors: req.attention_anchors.clone(),
                hopfield_candidates: hopfield_count,
                spread_steps: 3,
                post_inhibition_count: spread_result.activated.len(),
                pgt_layer,
            },
        })
    }

    /// v0.9.0: Retrieval mode — HNSW + RRF fusion.
    ///
    /// Returns items sorted by Reciprocal Rank Fusion score (k=60)
    /// combining HNSW cosine rank + SparseIndex ngram rank.
    fn recall_retrieval(
        &self,
        req: &RecallRequest,
        query_vector: &[f16],
        start: std::time::Instant,
    ) -> Result<RecallResponse> {
        const HNSW_SEARCH_K: usize = 80;

        // Step 1: HNSW search — get candidates
        let hnsw_results = self.hnsw.search(query_vector, HNSW_SEARCH_K);

        // Step 2: Map HNSW results to string IDs with rank
        let hnsw_strings: Vec<(String, f32)> = hnsw_results
            .iter()
            .filter_map(|(node_id, sim)| {
                self.hnsw_id_map
                    .get(node_id)
                    .map(|sid| (sid.clone(), *sim))
            })
            .collect();

        // Step 3: SparseIndex BM25 search (v0.10.0: replaces IDF-weighted search)
        let query_sparse = self.ngram_encoder.encode(&req.query).sparse;
        let idf = self.sparse_index.idf_map();
        let sparse_results = self.sparse_index.bm25_search(&query_sparse, &idf, HNSW_SEARCH_K);

        // Step 4: BM25 score-based fusion (v0.10.0: replaces RRF rank-based)
        let mut bm25_map: HashMap<String, f32> = sparse_results.into_iter().collect();
        let hnsw_map: HashMap<String, f32> = hnsw_strings.iter().cloned().collect();

        // Min-max normalize BM25 scores
        let bm25_min = bm25_map.values().cloned().fold(f32::MAX, f32::min);
        let bm25_max = bm25_map.values().cloned().fold(f32::MIN, f32::max);
        for score in bm25_map.values_mut() {
            if (bm25_max - bm25_min).abs() < f32::EPSILON {
                *score = 0.5;
            } else {
                *score = (*score - bm25_min) / (bm25_max - bm25_min);
            }
        }

        // Fuse scores: 0.4 * BM25 + 0.6 * HNSW cosine similarity
        let mut fused: HashMap<String, f32> = HashMap::new();
        for (id, norm_score) in &bm25_map {
            let cos_sim = hnsw_map.get(id).copied().unwrap_or(0.0);
            fused.insert(id.clone(), 0.4 * norm_score + 0.6 * cos_sim);
        }
        for (id, cos_sim) in &hnsw_map {
            if !fused.contains_key(id) {
                fused.insert(id.clone(), 0.6 * *cos_sim);
            }
        }

        let mut sorted: Vec<(String, f32)> = fused.into_iter().collect();
        sorted.sort_by(|a, b| {
            b.1.partial_cmp(&a.1)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        sorted.truncate(req.limit);

        // v0.9.0: Optional Cross-Encoder reranking (feature-gated behind `onnx`)
        if req.use_reranker {
            #[cfg(feature = "onnx")]
            {
                if let Some(reranker) = self.reranker.as_ref() {
                    if let Ok(rtxn) = self.storage.begin_read() {
                        let candidate_texts: Vec<String> = sorted
                            .iter()
                            .map(|(id, _)| {
                                self.storage
                                    .get_hippocampus(&rtxn, id)
                                    .ok()
                                    .flatten()
                                    .map(|e| e.text)
                                    .unwrap_or_default()
                            })
                            .collect();
                        let candidate_refs: Vec<&str> =
                            candidate_texts.iter().map(|s| s.as_str()).collect();

                        let reranked = reranker
                            .rerank(&req.query, &candidate_refs)
                            .unwrap_or_else(|e| {
                                eprintln!("memhop: reranker error, falling back: {e}");
                                sorted.iter().enumerate().map(|(i, _)| (i, 0.0_f32)).collect()
                            });

                        let original = std::mem::take(&mut sorted);
                        let mut reordered = Vec::with_capacity(reranked.len());
                        for (orig_idx, _) in reranked {
                            if orig_idx < original.len() {
                                reordered.push(original[orig_idx].clone());
                            }
                        }
                        sorted = reordered;
                    } else {
                        eprintln!("memhop: failed to open read txn for reranker, skipping");
                    }
                } else {
                    // Warn only once about missing reranker to avoid log spam
                    static WARNED: std::sync::OnceLock<bool> = std::sync::OnceLock::new();
                    WARNED.get_or_init(|| {
                        eprintln!("memhop: reranker not loaded (model path not configured or load failed), skipping rerank");
                        true
                    });
                }
            }
            #[cfg(not(feature = "onnx"))]
            {
                eprintln!("memhop: reranker requires `onnx` feature, skipping");
            }
        }

        // v0.10.0: Archived penalty — multiply score by 0.3 for archived engrams
        if let Ok(rtxn) = self.storage.begin_read() {
            for (id, score) in sorted.iter_mut() {
                if let Ok(Some(engram)) = self.storage.get_hippocampus(&rtxn, id)
                    && engram.is_archived
                {
                    *score *= 0.3;
                }
            }
        }
        sorted.sort_by(|a, b| {
            b.1.partial_cmp(&a.1)
                .unwrap_or(std::cmp::Ordering::Equal)
        });

        // Step 5: Load engrams from storage, classify by kind
        let mut associations: Vec<Engram> = Vec::new();
        let mut schemas: Vec<Engram> = Vec::new();
        let mut emotional_echoes: Vec<Engram> = Vec::new();
        let mut knowledge_memories: Vec<Engram> = Vec::new();
        let conflicts: Vec<ConflictItem> = Vec::new();
        // v0.9.1: Capture scores for turn-level aggregation
        let score_map: HashMap<String, f32> = sorted.iter().cloned().collect();

        if let Ok(rtxn) = self.storage.begin_read() {
            for (id, _score) in &sorted {
                // v0.9.0: Try cache first, fall back to storage
                let engram = self.engram_cache.borrow().get(id).cloned();
                let engram = match engram {
                    Some(e) => e,
                    None => {
                        if let Ok(Some(e)) = self.storage.get_hippocampus(&rtxn, id) {
                            self.engram_cache.borrow_mut().insert(id.clone(), e.clone());
                            e
                        } else {
                            continue;
                        }
                    }
                };
                // v0.11.0: Apply kind_filter
                if !req.kind_filter.is_empty() && !req.kind_filter.contains(&engram.kind) {
                    continue;
                }
                // v0.11.0: Apply tree filter
                if let Some(ref tree_path) = req.tree
                    && engram.kind == EngramKind::Knowledge
                    && engram.tree_path.as_deref() != Some(tree_path.as_str())
                {
                    continue;
                }
                // v0.12.1: Apply tree_id filter (via tree_ref)
                if let Some(ref tree_id) = req.tree_id
                    && engram.tree_ref.as_ref().map(|tr| &tr.tree_id) != Some(tree_id)
                {
                    continue;
                }
                // v0.12.0: Apply time filter
                if req.time_from.is_some() || req.time_to.is_some() {
                    let after = req.time_from.is_none_or(|t| engram.created_at >= t);
                    let before = req.time_to.is_none_or(|t| engram.created_at <= t);
                    if !(after && before) {
                        continue;
                    }
                }
                match engram.kind {
                    EngramKind::Knowledge => knowledge_memories.push(engram),
                    EngramKind::Schema => schemas.push(engram),
                    _ => {
                        if engram.arousal > 0.7 {
                            emotional_echoes.push(engram.clone());
                        }
                        associations.push(engram);
                    }
                }
            }
        }

        // v0.12.0: 知识自动附带 — 从书架检索附加知识
        if req.attach_knowledge && self.phase != Phase::Warmup {
            knowledge_memories = self.recall_knowledge_attached(query_vector);
        }

        // v0.11.0: Build tree_contexts from knowledge_memories
        let mut tree_contexts: Vec<TreeContext> = Vec::new();
        for e in &knowledge_memories {
            if let Some(ref tree_path) = e.tree_path {
                let domain = e.meta.get("domain")
                    .and_then(|v| v.as_str())
                    .unwrap_or("generic");
                if !tree_contexts.iter().any(|tc: &TreeContext| tc.tree_path == *tree_path) {
                    let source_count = knowledge_memories.iter()
                        .filter(|ke| ke.tree_path.as_deref() == Some(tree_path.as_str()))
                        .count();
                    tree_contexts.push(TreeContext {
                        tree_path: tree_path.clone(),
                        domain: domain.to_string(),
                        source_count,
                    });
                }
            }
        }

        // v0.11.0: Build graph_associations from top results
        let mut graph_associations: Vec<GraphAssociation> = Vec::new();
        let mut seen: HashSet<String> = HashSet::new();
        for (id, _score) in &sorted {
            let edges = self.graph.edges_of(id);
            for edge in edges {
                let pair_key = if *id < edge.target_id {
                    format!("{}|{}", id, edge.target_id)
                } else {
                    format!("{}|{}", edge.target_id, id)
                };
                if seen.contains(&pair_key) { continue; }
                seen.insert(pair_key);

                if edge.kind == AssociationKind::CoShelf {
                    graph_associations.push(GraphAssociation {
                        source_id: id.clone(),
                        target_id: edge.target_id.clone(),
                        kind: edge.kind.clone(),
                        weight: edge.weight,
                        description: "CoShelf: same knowledge tree".to_string(),
                    });
                }
            }
        }

        // Step 6: Record recalled IDs for Dream reconsolidation
        {
            let mut buf = self.recalled_buffer.borrow_mut();
            for (id, _) in &sorted {
                if !buf.contains(id) {
                    buf.push(id.clone());
                }
            }
        }

        // v0.12.1: 检测跨树命中 → 创建纠缠事件
        if self.phase == Phase::Full {
            let mut tree_ids_set: HashSet<String> = HashSet::new();
            let mut node_ids: Vec<String> = Vec::new();
            for eng in associations.iter().chain(knowledge_memories.iter()) {
                if let Some(ref tr) = eng.tree_ref {
                    tree_ids_set.insert(tr.tree_id.clone());
                    node_ids.push(eng.id.clone());
                }
            }
            if tree_ids_set.len() >= 2 && node_ids.len() >= 2 {
                let context = "记忆在查询中跨树关联".to_string();
                let tree_ids: Vec<String> = tree_ids_set.into_iter().collect();
                self.create_or_update_entanglement(
                    node_ids, tree_ids, context, EntanglementTrigger::RecallCrossTree,
                );
            }
        }

        // v0.12.1: 展开纠缠事件节点
        self.expand_entangled_results(&mut associations);

        // v0.12.1: 三观模式介入
        let (worldview_context, cognitive_conflicts) = self.extract_worldview_context(&req.query);

        // v0.9.1: Build turn-level hits from associated engrams
        let (hit_turns, aggregated_sessions) = self.build_turn_hits(&associations, &score_map)
            .unwrap_or_default();

        // Step 7: L0 Cortex (working memory)
        let working_memory = self.cortex.recent(&req.session_id, req.recent_limit);

        let latency_us = start.elapsed().as_micros() as u64;

        Ok(RecallResponse {
            working_memory,
            associations,
            schemas,
            emotional_echoes,
            conflicts,
            archive_results: None,
            hit_turns,
            aggregated_sessions,
            knowledge_memories,
            tree_contexts,
            graph_associations,
            worldview_context,
            cognitive_conflicts,
            trace: RecallTrace {
                latency_us,
                gated_anchors: req.attention_anchors.clone(),
                hopfield_candidates: sorted.len(),
                spread_steps: 0,
                post_inhibition_count: sorted.len(),
                pgt_layer: None,
            },
        })
    }

    /// v0.12.0: 从书架知识树中检索附带知识。
    ///
    /// 使用 HNSW 搜索 Knowledge engrams，应用余弦阈值过滤，
    /// 返回最多 KNOWLEDGE_ATTACH_MAX 条结果。
    fn recall_knowledge_attached(&self, query: &[f16]) -> Vec<Engram> {
        const HNSW_K: usize = KNOWLEDGE_ATTACH_LIMIT * 10;

        let hnsw_results = self.hnsw.search(query, HNSW_K);
        let hnsw_strings: Vec<(String, f32)> = hnsw_results
            .iter()
            .filter_map(|(node_id, sim)| {
                self.hnsw_id_map
                    .get(node_id)
                    .map(|sid| (sid.clone(), *sim))
            })
            .collect();

        // Filter candidates by cosine threshold and kind
        let mut candidates: Vec<(String, f32)> = Vec::new();
        if let Ok(rtxn) = self.storage.begin_read() {
            for (id, cos_sim) in &hnsw_strings {
                if *cos_sim <= KNOWLEDGE_THRESHOLD {
                    continue;
                }
                let engram = self.engram_cache.borrow().get(id).cloned();
                let engram = match engram {
                    Some(e) => e,
                    None => {
                        if let Ok(Some(e)) = self.storage.get_hippocampus(&rtxn, id) {
                            self.engram_cache
                                .borrow_mut()
                                .insert(id.clone(), e.clone());
                            e
                        } else {
                            continue;
                        }
                    }
                };
                if engram.kind != EngramKind::Knowledge {
                    continue;
                }
                candidates.push((id.clone(), *cos_sim));
            }
        }

        // Sort by HNSW cosine similarity descending
        candidates.sort_by(|a, b| {
            b.1.partial_cmp(&a.1)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        candidates.truncate(KNOWLEDGE_ATTACH_MAX);

        // Load full engrams
        let mut results = Vec::with_capacity(candidates.len());
        if let Ok(rtxn) = self.storage.begin_read() {
            for (id, _) in &candidates {
                if let Ok(Some(engram)) = self.storage.get_hippocampus(&rtxn, id) {
                    results.push(engram);
                }
            }
        }
        results
    }

    // ── v0.12.1: Tree API ──────────────────────────────────

    /// v0.12.1: 创建知识树
    pub fn create_tree(&mut self, name: &str, domain: &str) -> Result<Tree> {
        let now = now_millis();
        let id = format!("tree_{}", now);
        let tree = Tree {
            id: id.clone(),
            name: name.to_string(),
            domain: domain.to_string(),
            description: None,
            memory_count: 0,
            last_active_at: now,
            shelf_paths: vec![],
            created_at: now,
        };
        let mut wtxn = self.storage
            .begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.storage
            .put_tree(&mut wtxn, &tree)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn
            .commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(tree)
    }

    /// v0.12.1: 列出所有知识树
    pub fn list_trees(&self) -> Result<Vec<Tree>> {
        let rtxn = self.storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let trees = self.storage
            .get_all_trees(&rtxn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(trees)
    }

    /// v0.12.1: 获取单个知识树
    pub fn get_tree(&self, tree_id: &str) -> Result<Option<Tree>> {
        let rtxn = self.storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.storage
            .get_tree(&rtxn, tree_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))
    }

    /// v0.12.1: 删除知识树（不解绑 engram）
    pub fn delete_tree(&mut self, tree_id: &str) -> Result<()> {
        let mut wtxn = self.storage
            .begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.storage
            .delete_tree(&mut wtxn, tree_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn
            .commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// v0.12.1: 将 engram 移动到指定树
    pub fn move_to_tree(&mut self, engram_id: &str, tree_id: &str) -> Result<()> {
        // 1. Read engram from hippocampus
        let rtxn = self.storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut engram = self.storage
            .get_hippocampus(&rtxn, engram_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            .ok_or_else(|| MemHopError::NotFound(format!("engram '{}' not found", engram_id)))?;
        drop(rtxn);

        // 2. Read Tree to get name and domain
        let tree = self.get_tree(tree_id)?
            .ok_or_else(|| MemHopError::NotFound(format!("tree '{}' not found", tree_id)))?;

        // 3. Update tree_ref and deprecated tree_path
        engram.tree_ref = Some(TreeRef {
            tree_id: tree.id.clone(),
            tree_name: tree.name.clone(),
            tree_domain: tree.domain.clone(),
        });
        engram.tree_path = Some(tree.name.clone());

        // 4. Write back
        let mut wtxn = self.storage
            .begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.storage
            .put_hippocampus(&mut wtxn, engram_id, &engram)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn
            .commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        Ok(())
    }

    // ── reflect ───────────────────────────────────────────

    /// 创建 Reflection 类型 Engram。
    pub fn reflect(&mut self, input: ReflectionInput) -> Result<String> {
        let now = now_millis();
        let id = generate_id();
        let kind_name = input.kind.to_string();

        let mut meta = HashMap::new();
        meta.insert(
            "reflection_kind".to_string(),
            serde_json::Value::String(kind_name.clone()),
        );
        meta.insert(
            "anchored_to".to_string(),
            serde_json::Value::Array(
                input
                    .anchored_to
                    .iter()
                    .map(|s| serde_json::Value::String(s.clone()))
                    .collect(),
            ),
        );

        let engram = Engram {
            id: id.clone(),
            text: input.content,
            summary: None,
            vector: vec![f16::from_f32(0.0); crate::engram::VECTOR_DIM],
            keywords: vec![kind_name],
            content_type: Some("reflection".to_string()),
            valence: input.emotional_state.valence,
            arousal: input.emotional_state.arousal,
            vitality: 0.9,
            protection: Protection::Normal,
            created_at: now,
            last_activated: now,
            activation_count: 1,
            kind: EngramKind::Reflection,
            meta,
            is_archived: false,
            is_dormant: false,
            turn_id: None,
            tree_path: None,
            source_path: None,
            source_textunit: None,
            turn_ids: Vec::new(),
            context_id: None,
            tree_ref: None,
        };

        self.hippocampus.store(&self.storage, &engram)?;

        for anchor_id in &input.anchored_to {
            self.graph
                .add_edge(&self.storage, &id, anchor_id, 0.7, AssociationKind::Manual, now)?;
            self.graph
                .add_edge(&self.storage, anchor_id, &id, 0.7, AssociationKind::Manual, now)?;
        }

        self.growth.total_reflections += 1;
        self.growth.total_engrams_created += 1;

        Ok(id)
    }

    // ── v0.9.1: Turn Crystallizer ──────────────────────────

    /// NREM-2b: Semantic clustering of DialogueTurns into Schema engrams.
    fn nrem_turn_crystallizer(&mut self, report: &mut DreamReport) -> Result<()> {
        let rtxn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let turns = self.storage.all_dialogues(&rtxn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(rtxn);

        if turns.len() < 3 {
            return Ok(());
        }

        let now = now_millis();
        let schemas = crate::schema::turn_cluster_emergence(&turns, 0.85, now);

        for (schema_engram, schema_extra) in schemas {
            self.hippocampus.store(&self.storage, &schema_engram)?;
            self.hopfield.add_pattern(&schema_engram.id, &schema_engram.vector);
            let mut txn = self.storage.begin_write()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            self.storage.put_schema(&mut txn, &schema_engram.id, &schema_extra)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            txn.commit()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;

            // Hebbian-enhanced bidirectional edges: turn → Schema (weight=2.0, Hierarchical)
            for turn_id in &schema_extra.source_episodes {
                if let Err(e) = self.graph.add_bidirectional_edge(
                    &self.storage,
                    turn_id,
                    &schema_engram.id,
                    2.0,
                    AssociationKind::Hierarchical,
                    now,
                ) {
                    eprintln!(
                        "[dream] Hebbian edge failed for turn {} → schema {}: {e}",
                        turn_id, schema_engram.id
                    );
                }
            }

            report.turn_schemas_created += 1;
        }
        Ok(())
    }

    // ── v0.9.1: Forget / Update ─────────────────────────

    /// Forget all engrams and the DialogueTurn for a given turn_id.
    #[deprecated(note = "use forget_batch with ForgetFilter::ByTurnId")]
    pub fn forget(&mut self, turn_id: &str) -> Result<()> {
        let count = self.forget_batch(&ForgetFilter::ByTurnId(turn_id.to_string()))?;
        // Also delete the dialogue turn for backward compatibility.
        if let Ok(wtxn) = self.storage.begin_write() {
            let mut txn = wtxn;
            let _ = self.storage.delete_dialogue(&mut txn, turn_id);
            let _ = txn.commit();
        }
        if count == 0 {
            return Err(MemHopError::NotFound(format!(
                "turn_id not found: {}",
                turn_id
            )));
        }
        Ok(())
    }

    /// Batch delete engrams matching a filter.
    ///
    /// Removes from all indexes: Hopfield, HNSW (soft-delete tombstone),
    /// SparseIndex, UnifiedGraph, LMDB, EngramCache, and last_chunk_per_tree.
    /// HNSW tombstones are persisted to LMDB config after all deletions.
    pub fn forget_batch(&mut self, filter: &ForgetFilter) -> Result<usize> {
        // 1. Read all hippocampus entries from LMDB
        let rtxn = self
            .storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let entries = self
            .storage
            .all_hippocampus_entries(&rtxn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(rtxn);

        // 2. Filter entries by the given filter criteria
        let to_remove: Vec<(String, Engram)> = entries
            .into_iter()
            .filter(|(_, e)| match filter {
                ForgetFilter::ByTreePath(tp) => {
                    e.kind == EngramKind::Knowledge
                        && e.tree_path.as_deref() == Some(tp.as_str())
                }
                ForgetFilter::ByTurnId(tid) => e.turn_id.as_deref() == Some(tid.as_str()),
                ForgetFilter::ByEngramId(eid) => &e.id == eid,
            })
            .collect();

        let count = to_remove.len();
        if count == 0 {
            return Ok(0);
        }

        // Log the deletion
        let kind_summary = if to_remove
            .first()
            .map(|(_, e)| e.kind == EngramKind::Knowledge)
            .unwrap_or(false)
        {
            "knowledge"
        } else {
            "memory"
        };
        eprintln!(
            "[memhop] forget_batch: removing {} {} engrams",
            count, kind_summary
        );

        // 3. Remove from each index/system
        for (id, engram) in &to_remove {
            // A. Hopfield remove
            self.hopfield.remove_pattern(id);

            // B. HNSW mark_deleted
            let found: Vec<u64> = self
                .hnsw_id_map
                .iter()
                .filter(|(_, sid)| *sid == id)
                .map(|(nid, _)| *nid)
                .collect();
            for node_id in found {
                self.hnsw.mark_deleted(node_id);
            }

            // C. SparseIndex remove
            self.sparse_index.remove(id);

            // D. Graph remove node (removes all incident edges)
            let _ = self.graph.remove_node(&self.storage, id);

            // E. LMDB delete
            {
                let mut wtxn = self
                    .storage
                    .begin_write()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                self.storage
                    .delete_hippocampus(&mut wtxn, id)
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                wtxn
                    .commit()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
            }

            // F. Remove from EngramCache if present
            self.engram_cache.borrow_mut().remove(id);

            // G. Clean up last_chunk_per_tree
            if engram.kind == EngramKind::Knowledge
                && let Some(ref tp) = engram.tree_path
                && self.last_chunk_per_tree.get(tp) == Some(id)
            {
                self.last_chunk_per_tree.remove(tp);
            }
        }

        // 4. Persist HNSW tombstones to LMDB config
        {
            let mut wtxn = self
                .storage
                .begin_write()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            let tombstone_ids: Vec<u64> = self.hnsw.tombstones.iter().copied().collect();
            self.storage
                .put_config(&mut wtxn, "hnsw_tombstones", &tombstone_ids)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            wtxn
                .commit()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
        }

        Ok(count)
    }

    /// Update a turn with new content (forget + perceive).
    pub fn update(&mut self, turn_id: &str, input: PerceptionInput) -> Result<PerceptionOutput> {
        self.forget_batch(&ForgetFilter::ByTurnId(turn_id.to_string()))?;
        // Also delete the dialogue turn for backward compatibility.
        if let Ok(mut wtxn) = self.storage.begin_write() {
            let _ = self.storage.delete_dialogue(&mut wtxn, turn_id);
            let _ = wtxn.commit();
        }
        self.perceive(input)
    }

    /// List all schema engrams with their metadata.
    pub fn list_schemas(&self) -> Result<Vec<(Engram, crate::engram::SchemaExtra)>> {
        let rtxn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let ids = self.storage.all_schema_ids(&rtxn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut results = Vec::new();
        for id in &ids {
            let engram = self.storage.get_hippocampus(&rtxn, id)
                .map_err(|e| MemHopError::Storage(e.to_string()))?
                .ok_or_else(|| MemHopError::Storage(format!("schema engram not found: {}", id)))?;
            let extra = self.storage.get_schema(&rtxn, id)
                .map_err(|e| MemHopError::Storage(e.to_string()))?
                .unwrap_or_default();
            results.push((engram, extra));
        }
        Ok(results)
    }

    // ── v0.12.1: EntanglementEvent decay (NREM phase) ──────

    /// v0.12.1: 衰减纠缠事件强度。
    /// 超过 30 天未命中的事件每天衰减 10%，强度 < 0.1 时删除。
    fn nrem_entanglement_decay(&mut self, report: &mut DreamReport) -> Result<()> {
        let now = now_millis();
        let rtxn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let events = self.storage.get_all_entanglements(&rtxn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(rtxn);

        for event in &events {
            let days_since = (now - event.last_hit_at).max(0) / 86_400_000;
            if days_since > 30 {
                let decay = 0.9_f32.powi((days_since - 30) as i32);
                let new_strength = event.strength * decay;

                if new_strength < 0.1 {
                    // Delete the event
                    let mut wtxn = self.storage.begin_write()
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    for node_id in &event.nodes {
                        let _ = self.storage.remove_entanglement_node(&mut wtxn, node_id, &event.id);
                    }
                    self.storage.delete_entanglement(&mut wtxn, &event.id)
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    wtxn.commit()
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    report.entanglements_decayed += 1;
                } else {
                    // Update strength
                    let mut wtxn = self.storage.begin_write()
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    let mut updated = event.clone();
                    updated.strength = new_strength;
                    self.storage.put_entanglement(&mut wtxn, &updated)
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    wtxn.commit()
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                }
            }
        }

        Ok(())
    }

    // ── v0.12.1: EntanglementEvent creation (REM phase) ────

    /// v0.12.1: Dream REM 阶段 — 检测跨 Anchor 的跨树纠缠，创建 EntanglementEvent。
    fn rem_entanglement_creation(&mut self, report: &mut DreamReport) -> Result<()> {
        let txn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let anchor_names = self.storage.all_anchor_names(&txn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(txn);

        if anchor_names.len() < 2 {
            return Ok(());
        }

        for i in 0..anchor_names.len() {
            let txn = self.storage.begin_read()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            let ids_a = self.storage.anchor_get_ids(&txn, &anchor_names[i])
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            drop(txn);

            for other_name in anchor_names.iter().skip(i + 1) {
                let txn = self.storage.begin_read()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                let ids_b = self.storage.anchor_get_ids(&txn, other_name)
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                drop(txn);

                // Collect engrams with tree_refs from both anchors
                let mut tree_ids_set: HashSet<String> = HashSet::new();
                let mut node_ids: Vec<String> = Vec::new();

                for id in ids_a.iter().chain(ids_b.iter()) {
                    let txn = self.storage.begin_read()
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    if let Ok(Some(engram)) = self.storage.get_hippocampus(&txn, id)
                        && let Some(ref tr) = engram.tree_ref
                    {
                        tree_ids_set.insert(tr.tree_id.clone());
                        if !node_ids.contains(&engram.id) {
                            node_ids.push(engram.id.clone());
                        }
                    }
                    drop(txn);
                }

                if tree_ids_set.len() >= 2 && node_ids.len() >= 2 {
                    let context = format!(
                        "Dream REM 跨 Anchor 纠缠: {} <-> {}",
                        anchor_names[i], other_name,
                    );
                    let tree_ids: Vec<String> = tree_ids_set.into_iter().collect();
                    self.create_or_update_entanglement(
                        node_ids,
                        tree_ids,
                        context,
                        EntanglementTrigger::DreamEmergence,
                    );
                    report.entanglements_created += 1;
                }
            }
        }

        Ok(())
    }

    // ── v0.12.1: REM 三观涌现 ─────────────────────────────

    /// v0.12.1: REM 阶段 — 从纠缠事件涌现三观模式。
    ///
    /// 对 strength > 0.5 的纠缠事件按 context 关键词聚类，
    /// 每类 ≥3 事件且平均稳定度 ≥0.3 则创建或更新 WorldviewPattern。
    fn rem_worldview_emergence(&mut self, report: &mut DreamReport) -> Result<()> {
        let rtxn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        // 1. 获取所有纠缠事件（strength > 0.5）
        let events = self.storage.get_all_entanglements(&rtxn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(rtxn);

        if events.len() < 10 {
            return Ok(());
        }

        let strong_events: Vec<&EntanglementEvent> = events.iter()
            .filter(|e| e.strength > 0.5)
            .collect();

        if strong_events.len() < 5 {
            return Ok(());
        }

        // 2. 对 event.context 做简单语义聚类（按关键词重叠分组）
        let mut clusters: Vec<Vec<(&EntanglementEvent, Vec<String>)>> = Vec::new();

        for event in &strong_events {
            let keywords: Vec<String> = event.context
                .split(|c: char| !c.is_alphanumeric() && c != '_')
                .filter(|s| s.len() > 1)
                .map(|s| s.to_lowercase())
                .collect();

            // 找最匹配的已有聚类
            let mut best_cluster = None;
            for (ci, cluster) in clusters.iter().enumerate() {
                let cluster_keywords: Vec<&str> = cluster.iter()
                    .flat_map(|(_, kw)| kw.iter().map(|s| s.as_str()))
                    .collect();
                let overlap = keywords.iter()
                    .filter(|k| cluster_keywords.contains(&k.as_str()))
                    .count();
                if overlap >= 2 {
                    best_cluster = Some(ci);
                    break;
                }
            }

            if let Some(ci) = best_cluster {
                clusters[ci].push((event, keywords));
            } else {
                clusters.push(vec![(event, keywords)]);
            }
        }

        // 3. 为每个类簇创建或更新 WorldviewPattern
        let now = now_millis();
        let mut emerged = 0usize;

        for cluster in &clusters {
            if cluster.len() < 3 {
                continue;
            }

            let avg_strength: f32 = cluster.iter().map(|(e, _)| e.strength).sum::<f32>() / cluster.len() as f32;
            let occurrence = cluster.len() as u64;
            let stability = (1.0_f32.min(occurrence as f32 / 10.0)) * avg_strength;

            if stability < 0.3 {
                continue;
            }

            // 生成模式描述
            let source_ids: Vec<String> = cluster.iter().map(|(e, _)| e.id.clone()).collect();
            let contexts: Vec<&str> = cluster.iter().map(|(e, _)| e.context.as_str()).collect();
            let pattern_text = contexts.join("; ");

            // 分类（简化版）
            let category = PatternCategory::ThinkingStyle;

            // 检查是否已有类似的世界观（通过 source_events 重叠）
            let rtxn = self.storage.begin_read()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            let existing = self.storage.get_all_worldviews(&rtxn)
                .unwrap_or_default();
            drop(rtxn);

            let mut updated = false;
            for old_wv in &existing {
                let old_events: Vec<&str> = old_wv.source_events.iter().map(|s| s.as_str()).collect();
                let new_events: Vec<&str> = source_ids.iter().map(|s| s.as_str()).collect();
                let overlap = old_events.iter().filter(|e| new_events.contains(e)).count();
                if overlap >= 2 {
                    // 更新已有模式
                    let mut wtxn = self.storage.begin_write()
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    let mut updated_wv = old_wv.clone();
                    updated_wv.occurrence_count += occurrence;
                    updated_wv.stability = (updated_wv.stability + stability) / 2.0;
                    updated_wv.last_reinforced_at = now;
                    // 合并 source_events（去重）
                    for sid in &source_ids {
                        if !updated_wv.source_events.contains(sid) {
                            updated_wv.source_events.push(sid.clone());
                        }
                    }
                    self.storage.put_worldview(&mut wtxn, &updated_wv)
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    wtxn.commit()
                        .map_err(|e| MemHopError::Storage(e.to_string()))?;
                    updated = true;
                    break;
                }
            }

            if !updated {
                // 创建新 WorldviewPattern
                let id = generate_id();
                let wv = WorldviewPattern {
                    id: id.clone(),
                    source_events: source_ids,
                    pattern: pattern_text.chars().take(200).collect(),
                    category,
                    occurrence_count: occurrence,
                    stability,
                    emerged_at: now,
                    last_reinforced_at: now,
                };
                let mut wtxn = self.storage.begin_write()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                self.storage.put_worldview(&mut wtxn, &wv)
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                wtxn.commit()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                emerged += 1;
            }
        }

        report.worldviews_emerged = emerged;
        Ok(())
    }

    // ── dream ─────────────────────────────────────────────

    /// 执行 Dream 整合（6 阶段）。
    /// 触发条件: 每 `dream_interval` 次 perceive | hippocampus 满载 | 显式调用
    /// 策略: 增量处理，每次处理一批新记忆，多轮逐步覆盖
    pub fn dream(&mut self) -> Result<DreamReport> {
        let start = Instant::now();
        let mut report = DreamReport::default();

        // NREM-1: Vitality 衰减 + 归档/遗忘
        if let Err(e) = self.nrem_vitality_decay(&mut report) {
            eprintln!("[dream] NREM-1 error: {}", e);
        }

        // NREM-2: 边衰减 + 剪枝（含平均度数 ≤30 强制剪枝）
        if let Err(e) = self.graph.decay_edges(&self.storage, self.personality.decay_lambda()) {
            eprintln!("[dream] NREM-2 decay error: {}", e);
        }
        if let Ok(pruned) = self.graph.prune_edges(&self.storage, 0.03) {
            report.pruned_edges = pruned;
        }
        if self.graph.avg_degree() > 30.0
            && let Ok(extra) = self.graph.prune_to_max_degree(&self.storage, 30)
        {
            report.pruned_edges += extra;
        }

        // NREM-2b: v0.9.1 — Turn Crystallizer
        if let Err(e) = self.nrem_turn_crystallizer(&mut report) {
            eprintln!("[dream] NREM-2b error: {}", e);
        }

        // REM-1: Hippocampus → Neocortex 整合
        if let Err(e) = self.rem_consolidate(&mut report) {
            eprintln!("[dream] REM-1 error: {}", e);
        }

        // REM-2: Schema 涌现
        if let Err(e) = self.rem_schema_emergence(&mut report) {
            eprintln!("[dream] REM-2 error: {}", e);
        }

        // NREM-3: 矛盾检测
        if let Err(e) = self.nrem_contradiction_detection(&mut report) {
            eprintln!("[dream] NREM-3 error: {}", e);
        }

        // v0.12.1: NREM — EntanglementEvent 衰减
        if let Err(e) = self.nrem_entanglement_decay(&mut report) {
            eprintln!("[dream] NREM entanglement decay error: {}", e);
        }

        // REM-3: 跨 Anchor 发现
        if let Err(e) = self.rem_cross_anchor_discovery(&mut report) {
            eprintln!("[dream] REM-3 error: {}", e);
        }

        // v0.12.1: REM — EntanglementEvent 创建（跨 Anchor 跨树检测）
        if let Err(e) = self.rem_entanglement_creation(&mut report) {
            eprintln!("[dream] REM entanglement creation error: {}", e);
        }

        // v0.12.1: REM — 三观模式涌现
        if let Err(e) = self.rem_worldview_emergence(&mut report) {
            eprintln!("[dream] REM worldview emergence error: {}", e);
        }

        // REM-4: v0.8.0 Cross-plan schema emergence
        if let Err(e) = schema::cross_plan_schema_emergence(self) {
            eprintln!("[dream] REM-4 cross-plan-schema error: {}", e);
        }

        // v0.9.0: LLM-enhanced dream phases (fire-and-forget, errors are logged)
        let saved_llm = self.llm.take();
        if let Some(ref llm) = saved_llm {
            if let Err(e) = self.dream_llm_keywords(&**llm, &mut report) {
                eprintln!("[dream] LLM keywords error: {}", e);
            }
            if let Err(e) = self.dream_llm_contradictions(&**llm, &mut report) {
                eprintln!("[dream] LLM contradictions error: {}", e);
            }
        }
        self.llm = saved_llm;

        // v0.11.0: HNSW compact — rebuild index without tombstoned nodes
        {
            let ratio = self.hnsw.tombstone_ratio();
            if ratio > 0.3 {
                eprintln!("[dream] HNSW tombstone ratio {:.2} > 0.3, compacting", ratio);
                let removed = self.hnsw.compact();
                if removed > 0 {
                    let _ = self.hnsw.save_to_storage(&self.storage);
                    // Clear tombstones from LMDB config
                    let mut wtxn = match self.storage.begin_write() {
                        Ok(t) => t,
                        Err(e) => {
                            eprintln!("[dream] failed to open txn after compact: {e}");
                            return Ok(report);
                        }
                    };
                    let _ = self.storage.put_config(&mut wtxn, "hnsw_tombstones", &Vec::<u64>::new());
                    let _ = wtxn.commit();
                    report.hnsw_compacted = removed;
                }
            }
        }

        self.growth.dream_cycles += 1;
        report.duration_ms = start.elapsed().as_millis() as u64;
        Ok(report)
    }

    fn dream_internal(&mut self) -> Result<()> {
        let _ = self.dream()?;
        Ok(())
    }

    // ─ v0.9.0: LLM-enhanced dream phases ──────────────────

    /// If an LlmProvider is configured, suggest keywords for every engram in
    /// Hippocampus whose keyword list is empty. Updated engrams are written
    /// back to storage in-place.
    fn dream_llm_keywords(&self, llm: &dyn LlmProvider, report: &mut DreamReport) -> Result<()> {
        let entries = self.hippocampus.all_entries(&self.storage)?;
        let mut count = 0usize;

        for (id, mut engram) in entries {
            if !engram.keywords.is_empty() {
                continue;
            }
            match crate::llm_provider::llm_suggest_keywords(llm, &engram.text) {
                Ok(kws) if !kws.is_empty() => {
                    engram.keywords = kws;
                    let mut txn = self.storage.begin_write()?;
                    self.storage.put_hippocampus(&mut txn, &id, &engram)?;
                    txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
                    count += 1;
                }
                Ok(_) => { /* LLM returned empty list -- skip */ }
                Err(e) => eprintln!("[dream] LLM suggest_keywords for {}: {}", id, e),
            }
        }

        report.llm_keywords_added = count;
        Ok(())
    }

    /// If an LlmProvider is configured, verify high-cosine, low-keyword-overlap
    /// pairs with the LLM before marking them as contradictions. This runs in
    /// addition to (not instead of) the heuristic check in nrem_contradiction_detection.
    fn dream_llm_contradictions(&mut self, llm: &dyn LlmProvider, report: &mut DreamReport) -> Result<()> {
        let entries = self.hippocampus.all_entries(&self.storage)?;
        let episodes: Vec<(String, Engram)> = entries
            .into_iter()
            .filter(|(_, e)| e.kind == EngramKind::Episode)
            .collect();

        if episodes.len() < 2 {
            return Ok(());
        }

        let now = now_millis();
        let mut detected = 0usize;

        for i in 0..episodes.len() {
            let query_f32: Vec<f32> = episodes[i].1.vector.iter().map(|x| x.to_f32()).collect();
            let neighbors = self.hopfield.recall_topk(&query_f32, 20);

            for (neighbor_id, sim) in &neighbors {
                if *sim <= 0.8 {
                    continue;
                }
                if let Some(j) = episodes.iter().position(|(id, _)| id.as_str() == neighbor_id.as_str()) {
                    if j <= i {
                        continue;
                    }
                    let overlap = keyword_overlap(&episodes[i].1.keywords, &episodes[j].1.keywords);
                    if overlap < 0.3 {
                        match crate::llm_provider::llm_detect_contradiction(
                            llm,
                            &episodes[i].1.text,
                            &episodes[j].1.text,
                        ) {
                            Ok(true) => {
                                self.graph.add_edge(
                                    &self.storage, &episodes[i].0, neighbor_id,
                                    *sim, AssociationKind::Contradicts, now,
                                )?;
                                self.graph.add_edge(
                                    &self.storage, neighbor_id, &episodes[i].0,
                                    *sim, AssociationKind::Contradicts, now,
                                )?;
                                detected += 1;
                            }
                            Ok(false) => { /* LLM says not contradictory -- skip */ }
                            Err(e) => eprintln!("[dream] LLM contradiction check: {}", e),
                        }
                    }
                }
            }
        }

        report.llm_contradictions = detected;
        self.growth.total_contradictions += detected as u64;
        Ok(())
    }

    // ── NREM-1: Vitality decay ────────────────────────────

    /// 扫描 Hippocampus 中的记忆，计算时间衰减后的 vitality。
    /// vitality < 0.01 → 删除（遗忘）
    /// vitality < 0.1  → 标记 is_archived（归档）
    /// 其余 → 正常衰减更新 vitality
    fn nrem_vitality_decay(&mut self, report: &mut DreamReport) -> Result<()> {
        let entries = self.hippocampus.all_entries(&self.storage)?;

        // ── Reconsolidation: 处理 recall buffer ───────────────
        let recalled: Vec<String> = self.recalled_buffer.borrow_mut().drain(..).collect();
        if !recalled.is_empty() {
            for recalled_id in &recalled {
                if let Some(engram) = entries.iter().find(|(id, _)| id == recalled_id).map(|(_, e)| e.clone()) {
                    if engram.protection == Protection::Permanent {
                        continue;
                    }
                    let mut e = engram;
                    vitality::reconsolidate(&mut e.vitality, &mut e.activation_count, &mut e.last_activated);
                    let mut txn = self.storage.begin_write()?;
                    self.storage.put_hippocampus(&mut txn, recalled_id, &e)?;
                    txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
                }
            }
        }

        if entries.is_empty() {
            return Ok(());
        }

        // ── Vitality 衰减 ────────────────────────────────────
        let now = now_millis();
        let mut decayed = 0u64;
        let mut archived = 0u64;
        let mut forgotten = 0u64;
        let mut knowledge_count = 0u64;

        // Collect IDs to forget before mutating self
        let mut to_forget: Vec<String> = Vec::new();

        for (id, mut engram) in entries {
            // v0.11.0: Both Episode and Knowledge engrams participate.
            // Episode uses default decay scale (1.0), Knowledge uses slower rate.
            let kind_decay_scale = match engram.kind {
                EngramKind::Knowledge => {
                    self.config.vitality.knowledge_decay_rate / self.config.vitality.episode_decay_rate
                }
                _ => 1.0,
            };

            if engram.kind == EngramKind::Knowledge {
                knowledge_count += 1;
            }

            // 永久保护的不参与衰减
            if engram.protection == Protection::Permanent {
                continue;
            }

            // v0.10.0: Piggyback archive — turn-type engrams inactive >30 days
            if engram.turn_id.is_some() && (now - engram.last_activated) > 30 * 24 * 3600 * 1000 {
                engram.is_archived = true;
                let mut txn = self.storage.begin_write()?;
                self.storage.put_hippocampus(&mut txn, &id, &engram)?;
                txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
                report.turns_archived += 1;
                continue;
            }

            let hours_since_active = (now - engram.last_activated).max(0) as f64 / 3_600_000.0;
            if hours_since_active < 0.5 {
                continue; // 很新的记忆跳过本轮
            }

            // 计算干扰: 用 Hopfield 找近邻相似度
            let query_f32: Vec<f32> = engram.vector.iter().map(|x| x.to_f32()).collect();
            let neighbors = self.hopfield.recall_topk(&query_f32, 10);
            let recent_similar: Vec<f32> = neighbors
                .iter()
                .filter(|(nid, _)| nid.as_str() != id.as_str())
                .map(|(_, sim)| *sim)
                .collect();

            let ctx = vitality::DecayContext {
                hours_since_last_activated: hours_since_active,
                recent_similar,
                lambda: self.personality.decay_lambda(),
                interference_alpha: self.personality.interference_alpha(),
                arousal_beta: self.personality.arousal_beta(),
            };

            let new_vitality = vitality::compute_vitality(
                engram.vitality,
                engram.arousal,
                engram.activation_count,
                engram.last_activated,
                &ctx,
                kind_decay_scale,
            );

            if new_vitality < 0.01 {
                to_forget.push(id.clone());
                forgotten += 1;
            } else if new_vitality < 0.1 {
                engram.is_archived = true;
                engram.vitality = new_vitality;
                let mut txn = self.storage.begin_write()?;
                self.storage.put_hippocampus(&mut txn, &id, &engram)?;
                txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
                archived += 1;
            } else {
                engram.vitality = new_vitality;
                let mut txn = self.storage.begin_write()?;
                self.storage.put_hippocampus(&mut txn, &id, &engram)?;
                txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
                decayed += 1;
            }
        }

        // 遗忘：从 Hippocampus + Hopfield + Graph 中删除
        for id in &to_forget {
            self.hopfield.remove_pattern(id);
            let _ = self.graph.remove_node(&self.storage, id);
            let mut txn = self.storage.begin_write()?;
            let _ = self.storage.delete_hippocampus(&mut txn, id);
            txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
        }
        // 也清理 Hippocampus 内存索引
        if !to_forget.is_empty() {
            let _ = self.hippocampus.remove_batch(&self.storage, &to_forget);
        }

        report.vitality_decayed = decayed as usize;
        report.archived_count = archived as usize;
        report.forgotten_count = forgotten as usize;
        report.knowledge_processed = knowledge_count as usize;
        self.growth.total_forgotten += forgotten;
        Ok(())
    }

    // ── REM-1: Hippocampus → Neocortex ──────────────────

    /// 将 Hippocampus 中的记忆整合到 Neocortex（Hopfield + Graph）。
    /// - cosine > 0.9 → Semantic 边（关联已有节点）
    /// - 否则 → 独立插入 Hopfield
    /// - 建立 Temporal 边
    fn rem_consolidate(&mut self, report: &mut DreamReport) -> Result<()> {
        let entries = self.hippocampus.all_entries(&self.storage)?;
        if entries.is_empty() {
            return Ok(());
        }

        let now = now_millis();
        let mut consolidated = Vec::new();
        let mut edge_count = 0;

        for (id, engram) in &entries {
            let query_f32: Vec<f32> = engram.vector.iter().map(|x| x.to_f32()).collect();
            let neighbors = self.hopfield.recall_topk(&query_f32, 5);
            let mut merged = false;

            for (neighbor_id, sim) in &neighbors {
                if *sim > 0.9 {
                    // 高度相似 → 创建 Semantic 双向边
                    self.graph.add_edge(
                        &self.storage, id, neighbor_id, *sim, AssociationKind::Semantic, now,
                    )?;
                    self.graph.add_edge(
                        &self.storage, neighbor_id, id, *sim, AssociationKind::Semantic, now,
                    )?;
                    edge_count += 2;
                    merged = true;
                    break;
                }
            }

            if !merged {
                // 独立插入 Neocortex
                self.hopfield.add_pattern(id, &engram.vector);
                // 与同批其他未合并的记忆建立 Temporal 边
                for (other_id, _) in entries.iter().filter(|(oid, _)| *oid != id.as_str()) {
                    if !consolidated.contains(other_id) {
                        self.graph.add_edge(
                            &self.storage, id, other_id, 0.3, AssociationKind::Temporal, now,
                        )?;
                        edge_count += 1;
                    }
                }
            }
            consolidated.push(id.clone());
        }

        // 从 Hippocampus 删除已整合的记忆
        if !consolidated.is_empty() {
            self.hippocampus.remove_batch(&self.storage, &consolidated)?;
        }

        report.consolidated_count = consolidated.len();
        report.new_edges = edge_count;
        self.growth.total_consolidated += consolidated.len() as u64;
        Ok(())
    }

    // ── REM-2: Schema 涌现 ───────────────────────────────

    /// 对 Hippocampus 中的 Episode 和 Knowledge 进行增量聚类。
    /// cosine > 0.7 → 归入同一簇
    /// 簇大小 ≥3 → 调用 try_emerge_schema() 创建 Schema 节点
    fn rem_schema_emergence(&mut self, report: &mut DreamReport) -> Result<()> {
        let entries = self.hippocampus.all_entries(&self.storage)?;
        // v0.11.0: Include both Episode and Knowledge engrams for schema emergence
        let episodes: Vec<(String, Engram)> = entries
            .into_iter()
            .filter(|(_, e)| {
                (e.kind == EngramKind::Episode || e.kind == EngramKind::Knowledge) && !e.is_archived
            })
            .collect();

        if episodes.len() < 3 {
            return Ok(());
        }

        let now = now_millis();
        let mut new_schemas = 0;
        let mut assigned: HashSet<usize> = HashSet::new();

        for i in 0..episodes.len() {
            if assigned.contains(&i) {
                continue;
            }

            // 用 Hopfield 找当前 Episode 的近邻
            let query: Vec<f32> = episodes[i].1.vector.iter().map(|x| x.to_f32()).collect();
            let neighbors = self.hopfield.recall_topk(&query, 10);

            // 筛选出相似度 > 0.7 且未分配的 episodese
            let mut cluster: Vec<usize> = vec![i];
            for (nid, sim) in &neighbors {
                if *sim > 0.7
                    && let Some(idx) = episodes.iter().position(|(id, _)| id.as_str() == nid.as_str())
                    && !assigned.contains(&idx) && idx != i
                {
                    cluster.push(idx);
                    assigned.insert(idx);
                }
            }
            assigned.insert(i);

            if cluster.len() >= 3 {
                // v0.11.0: Detect cross-kind clusters (Episode + Knowledge)
                let has_episode = cluster.iter().any(|&idx| episodes[idx].1.kind == EngramKind::Episode);
                let has_knowledge = cluster.iter().any(|&idx| episodes[idx].1.kind == EngramKind::Knowledge);
                if has_episode && has_knowledge {
                    report.cross_kind_new_associations += 1;
                }

                let cluster_ids: Vec<String> = cluster.iter().map(|&idx| episodes[idx].0.clone()).collect();
                let cluster_engrams: Vec<&Engram> = cluster.iter().map(|&idx| &episodes[idx].1).collect();

                if let Some((schema_engram, schema_extra)) =
                    schema::try_emerge_schema(&cluster_ids, &cluster_engrams, now)
                {
                    // 存 Schema 到 Hippocampus
                    self.hippocampus.store(&self.storage, &schema_engram)?;
                    // 注册到 Hopfield
                    self.hopfield.add_pattern(&schema_engram.id, &schema_engram.vector);
                    // 持久化 SchemaExtra
                    let mut txn = self.storage.begin_write()?;
                    self.storage.put_schema(&mut txn, &schema_engram.id, &schema_extra)?;
                    txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
                    new_schemas += 1;
                }
            }
        }

        report.new_schemas = new_schemas;
        self.growth.total_schemas_emerged += new_schemas as u64;
        Ok(())
    }

    // ── NREM-3: 矛盾检测（增量） ─────────────────────────

    /// 扫描 Hippocampus 中的 Episode，用 Hopfield 找近邻（top-20），
    /// 对 cosine > 0.8 且关键词重叠低的候选对建立 Contradicts 边。
    fn nrem_contradiction_detection(&mut self, report: &mut DreamReport) -> Result<()> {
        let entries = self.hippocampus.all_entries(&self.storage)?;
        let episodes: Vec<(String, Engram)> = entries
            .into_iter()
            .filter(|(_, e)| e.kind == EngramKind::Episode)
            .collect();

        if episodes.len() < 2 {
            return Ok(());
        }

        let now = now_millis();
        let mut detected = 0u32;

        for i in 0..episodes.len() {
            let query_f32: Vec<f32> = episodes[i].1.vector.iter().map(|x| x.to_f32()).collect();
            let neighbors = self.hopfield.recall_topk(&query_f32, 20);

            for (neighbor_id, sim) in &neighbors {
                if *sim <= 0.8 {
                    continue;
                }
                // 找邻居在 episodes 中的索引
                if let Some(j) = episodes.iter().position(|(id, _)| id.as_str() == neighbor_id.as_str()) {
                    if j <= i {
                        continue; // 避免重复对
                    }
                    // 关键词重叠度: 低重叠 + 高 cosine → 矛盾嫌疑
                    let overlap = keyword_overlap(&episodes[i].1.keywords, &episodes[j].1.keywords);
                    if overlap < 0.3 {
                        self.graph.add_edge(
                            &self.storage,
                            &episodes[i].0,
                            neighbor_id,
                            *sim,
                            AssociationKind::Contradicts,
                            now,
                        )?;
                        self.graph.add_edge(
                            &self.storage,
                            neighbor_id,
                            &episodes[i].0,
                            *sim,
                            AssociationKind::Contradicts,
                            now,
                        )?;
                        detected += 1;
                    }
                }
            }
        }

        report.conflicts_detected = detected as usize;
        self.growth.total_contradictions += detected as u64;
        Ok(())
    }

    // ── REM-3: 跨 Anchor 发现（增量） ────────────────────

    /// 扫描已有 Anchor，跨 Anchor 发现 cosine > 0.8 的记忆对并建立 Semantic 边。
    fn rem_cross_anchor_discovery(&mut self, report: &mut DreamReport) -> Result<()> {
        let txn = self.storage.begin_read()?;
        let anchor_names = self.storage.all_anchor_names(&txn)?;
        drop(txn);

        if anchor_names.len() < 2 {
            return Ok(());
        }

        let now = now_millis();
        let mut new_edges = 0u32;

        for i in 0..anchor_names.len() {
            let txn = self.storage.begin_read()?;
            let ids_a = self.storage.anchor_get_ids(&txn, &anchor_names[i])?;
            drop(txn);

            for other_name in anchor_names.iter().skip(i + 1) {
                let txn = self.storage.begin_read()?;
                let ids_b = self.storage.anchor_get_ids(&txn, other_name)?;
                drop(txn);

                // 取每个 Anchor 下前 3 条记忆做跨 Anchor 比较
                for id_a in ids_a.iter().take(3) {
                    let txn = self.storage.begin_read()?;
                    let engram_a = self.storage.get_hippocampus(&txn, id_a)?;
                    drop(txn);

                    if let Some(engram_a) = engram_a {
                        let query_f32: Vec<f32> =
                            engram_a.vector.iter().map(|x| x.to_f32()).collect();
                        let neighbors = self.hopfield.recall_topk(&query_f32, 10);

                        for (neighbor_id, sim) in &neighbors {
                            if *sim > 0.8 && ids_b.contains(neighbor_id) {
                                self.graph.add_edge(
                                    &self.storage,
                                    id_a,
                                    neighbor_id,
                                    *sim,
                                    AssociationKind::Semantic,
                                    now,
                                )?;
                                self.graph.add_edge(
                                    &self.storage,
                                    neighbor_id,
                                    id_a,
                                    *sim,
                                    AssociationKind::Semantic,
                                    now,
                                )?;
                                new_edges += 2;
                            }
                        }
                    }
                }
            }
        }

        report.new_edges += new_edges as usize;
        Ok(())
    }

    // ── v0.12.1: EntanglementEvent 创建/更新 ───────────────

    /// v0.12.1: 创建或更新跨树纠缠事件。
    /// 检查是否已有相同节点集合的事件，有则更新强度，无则新建。
    fn create_or_update_entanglement(
        &self,
        nodes: Vec<String>,
        tree_ids: Vec<String>,
        context: String,
        trigger: EntanglementTrigger,
    ) {
        let now = now_millis();

        // Check if an existing event covers these same nodes
        let rtxn = match self.storage.begin_read() {
            Ok(t) => t,
            Err(e) => {
                eprintln!("[entanglement] begin_read error: {}", e);
                return;
            }
        };

        let existing_event_ids = if let Some(first_node) = nodes.first() {
            self.storage
                .get_entanglement_ids_for_node(&rtxn, first_node)
                .unwrap_or_default()
        } else {
            vec![]
        };
        let rtxn_ref = &rtxn; // borrow for the find_map closure

        let found = existing_event_ids.iter().find_map(|eid| {
            match self.storage.get_entanglement(rtxn_ref, eid) {
                Ok(Some(event)) => {
                    if event.nodes.len() == nodes.len()
                        && event.nodes.iter().all(|n| nodes.contains(n))
                    {
                        Some(event.clone())
                    } else {
                        None
                    }
                }
                _ => None,
            }
        });
        drop(rtxn);

        if let Some(mut existing) = found {
            // Update existing event
            existing.hit_count += 1;
            existing.strength = (existing.strength + 0.2).min(1.0);
            existing.last_hit_at = now;

            let mut wtxn = match self.storage.begin_write() {
                Ok(t) => t,
                Err(e) => {
                    eprintln!("[entanglement] begin_write error: {}", e);
                    return;
                }
            };
            if let Err(e) = self.storage.put_entanglement(&mut wtxn, &existing) {
                eprintln!("[entanglement] put error: {}", e);
            }
            let _ = wtxn.commit();
        } else {
            // Create new event
            let id = generate_id();
            let event = EntanglementEvent {
                id: id.clone(),
                nodes: nodes.clone(),
                tree_ids,
                context,
                trigger,
                strength: 0.3,
                plan_ids: vec![],
                created_at: now,
                last_hit_at: now,
                hit_count: 1,
            };

            let mut wtxn = match self.storage.begin_write() {
                Ok(t) => t,
                Err(e) => {
                    eprintln!("[entanglement] begin_write error: {}", e);
                    return;
                }
            };
            if let Err(e) = self.storage.put_entanglement(&mut wtxn, &event) {
                eprintln!("[entanglement] put error: {}", e);
                let _ = wtxn.commit();
                return;
            }
            // Build node reverse index
            for node_id in &nodes {
                if let Err(e) = self.storage.add_entanglement_node(&mut wtxn, node_id, &id) {
                    eprintln!("[entanglement] add_node error: {}", e);
                    break;
                }
            }
            let _ = wtxn.commit();
        }
    }

    /// v0.12.1: 展开纠缠事件中的节点 — 将 strength > 0.5 的事件中
    /// 尚未在结果中的 engram 添加到 associations。
    fn expand_entangled_results(&self, associations: &mut Vec<Engram>) {
        let mut included_ids: HashSet<String> = HashSet::new();
        for eng in associations.iter() {
            included_ids.insert(eng.id.clone());
        }

        let rtxn = match self.storage.begin_read() {
            Ok(t) => t,
            Err(_) => return,
        };

        let mut to_add: Vec<Engram> = Vec::new();
        for eng in associations.iter() {
            let event_ids = match self.storage.get_entanglement_ids_for_node(&rtxn, &eng.id) {
                Ok(ids) => ids,
                Err(_) => continue,
            };
            for eid in &event_ids {
                let event = match self.storage.get_entanglement(&rtxn, eid) {
                    Ok(Some(e)) => e,
                    _ => continue,
                };
                if event.strength > 0.5 {
                    for node_id in &event.nodes {
                        if !included_ids.contains(node_id)
                            && let Ok(Some(extra)) =
                                self.storage.get_hippocampus(&rtxn, node_id)
                        {
                            included_ids.insert(node_id.clone());
                            to_add.push(extra);
                        }
                    }
                }
            }
        }
        drop(rtxn);

        associations.extend(to_add);
    }

    /// v0.12.1: 三观模式介入 — 提取稳定度 > 0.7 的模式上下文和认知冲突。
    fn extract_worldview_context(&self, query: &str) -> (Vec<String>, Vec<String>) {
        let rtxn = match self.storage.begin_read() {
            Ok(t) => t,
            Err(_) => return (Vec::new(), Vec::new()),
        };
        let worldviews = match self.storage.get_all_worldviews(&rtxn) {
            Ok(w) => w,
            Err(_) => return (Vec::new(), Vec::new()),
        };
        drop(rtxn);

        let mut worldview_context = Vec::new();
        let mut cognitive_conflicts = Vec::new();

        for wv in &worldviews {
            if wv.stability > 0.7 {
                worldview_context.push(wv.pattern.clone());
            }
            if wv.stability > 0.5 {
                let query_lower = query.to_lowercase();
                if query_lower.contains("不应该")
                    || query_lower.contains("不对")
                    || query_lower.contains("相反")
                {
                    cognitive_conflicts.push(format!(
                        "当前输入与模式 '{}' 可能冲突",
                        wv.pattern
                    ));
                }
            }
        }

        (worldview_context, cognitive_conflicts)
    }

    /// v0.12.1: 获取所有纠缠事件
    pub fn get_all_entanglements(&self) -> Result<Vec<EntanglementEvent>> {
        let rtxn = self.storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let events = self.storage
            .get_all_entanglements(&rtxn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(events)
    }

    /// v0.12.1: 获取单个纠缠事件
    pub fn get_entanglement(&self, event_id: &str) -> Result<Option<EntanglementEvent>> {
        let rtxn = self.storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.storage
            .get_entanglement(&rtxn, event_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))
    }

    /// v0.12.1: 获取所有三观模式
    pub fn get_all_worldviews(&self) -> Result<Vec<WorldviewPattern>> {
        let rtxn = self.storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let worldviews = self.storage
            .get_all_worldviews(&rtxn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(worldviews)
    }

    /// v0.12.1: 获取单个三观模式
    pub fn get_worldview(&self, wv_id: &str) -> Result<Option<WorldviewPattern>> {
        let rtxn = self.storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.storage
            .get_worldview(&rtxn, wv_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))
    }

    // ── 私有辅助 ─────────────────────────────────────────

    fn rebuild_hopfield(storage: &LmdbStorage, knowledge_weight: f32) -> Result<ModernHopfield> {
        let txn = storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let entries = storage
            .all_hippocampus_entries(&txn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(txn);

        let mut hopfield = ModernHopfield::new(crate::engram::VECTOR_DIM, HOPFIELD_BETA);
        for (id, engram) in &entries {
            let weight = match engram.kind {
                EngramKind::Knowledge => knowledge_weight,
                _ => 1.0,
            };
            hopfield.add_pattern_weighted(id, &engram.vector, weight);
        }
        Ok(hopfield)
    }

    fn bootstrap_innate_schema(
        _storage: &LmdbStorage,
        _graph: &UnifiedGraph,
        _innate: &InnateSchema,
        _now: i64,
    ) -> Result<()> {
        Ok(())
    }

    // ── v0.11.0: 去重检查 ────────────────────────────────

    /// Check if a candidate engram is a duplicate of an existing one.
    /// Episode: cosine similarity > 0.95 → duplicate.
    /// Knowledge: cosine similarity > 0.9 AND same tree_path+source_path → duplicate.
    fn check_duplicate(
        &self,
        vector: &[f16],
        kind: &EngramKind,
        tree_path: Option<&str>,
        source_path: Option<&str>,
    ) -> Option<String> {
        let threshold = match kind {
            EngramKind::Knowledge => 0.9,
            _ => 0.95,
        };

        let cache = self.engram_cache.borrow();
        let vec_f32: Vec<f32> = vector.iter().map(|x| x.to_f32()).collect();

        for (id, existing) in cache.cache.iter() {
            if existing.kind != *kind {
                continue;
            }

            // For Knowledge, also check tree_path + source_path match
            if *kind == EngramKind::Knowledge {
                let etp = existing.tree_path.as_deref();
                let esp = existing.source_path.as_deref();
                if etp != tree_path || esp != source_path {
                    continue;
                }
            }

            let existing_f32: Vec<f32> = existing.vector.iter().map(|x| x.to_f32()).collect();
            let sim = cosine_similarity(&vec_f32, &existing_f32);
            if sim > threshold {
                return Some(id.clone());
            }
        }
        None
    }

    // ── v0.11.0: 核心写入管线 ─────────────────────────────

    /// Core engram writing pipeline. "LMDB is source of truth, indexes are best-effort."
    ///
    /// Written by both perceive() and store(). store_engram itself does NOT deduplicate;
    /// the caller (store) checks for duplicates first.
    fn store_engram(&mut self, mut engram: Engram) -> Result<String> {
        let id = if engram.id.is_empty() {
            generate_id()
        } else {
            engram.id.clone()
        };
        engram.id = id.clone();

        // Text truncation: Knowledge engrams are capped at 2000 chars (PRD R2)
        if engram.kind == EngramKind::Knowledge && engram.text.len() > 2000 {
            engram.text = engram.text.chars().take(2000).collect();
        }

        let now = now_millis();
        if engram.created_at == 0 {
            engram.created_at = now;
        }
        if engram.last_activated == 0 {
            engram.last_activated = now;
        }
        if engram.activation_count == 0 {
            engram.activation_count = 1;
        }

        // 1. LMDB write (source of truth)
        {
            let mut wtxn = self
                .storage
                .begin_write()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            self.storage
                .put_hippocampus(&mut wtxn, &id, &engram)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            wtxn
                .commit()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
        }

        // 1b. Maintain in-memory hippocampus order (for len() and Dream iteration)
        self.hippocampus.push_id(&id);

        // 2. EngramCache (hot cache)
        self.engram_cache.borrow_mut().insert(id.clone(), engram.clone());

        // 3. HNSW insert (best-effort)
        let node_id = self.next_node_id;
        self.next_node_id += 1;
        self.hnsw.insert(node_id, &engram.vector);
        self.hnsw_id_map.insert(node_id, id.clone());

        // 4. SparseIndex add (best-effort)
        let encoded = self.ngram_encoder.encode(&engram.text);
        let doc_length = engram.text.chars().count();
        self.sparse_index.add(&id, &encoded.sparse, doc_length);

        // 5. Hopfield add (best-effort, with weight)
        let weight = match engram.kind {
            EngramKind::Knowledge => self.config.hopfield.knowledge_pattern_weight,
            _ => 1.0,
        };
        self.hopfield.add_pattern_weighted(&id, &engram.vector, weight);

        // 6. CoShelf edge creation (for Knowledge engrams with adjacent chunks)
        if engram.kind == EngramKind::Knowledge
            && let Some(ref tree_path) = engram.tree_path
        {
                let key = tree_path.clone();
                if let Some(prev_id) = self.last_chunk_per_tree.get(&key) {
                    let now = now_millis();
                    // Create bidirectional CoShelf edge with weight 0.7
                    let _ = self.graph.add_edge(
                        &self.storage,
                        prev_id,
                        &id,
                        0.7,
                        AssociationKind::CoShelf,
                        now,
                    );
                    let _ = self.graph.add_edge(
                        &self.storage,
                        &id,
                        prev_id,
                        0.7,
                        AssociationKind::CoShelf,
                        now,
                    );
                }
                self.last_chunk_per_tree.insert(key, id.clone());
        }

        self.growth.total_engrams_created += 1;
        self.store_count += 1;

        Ok(id)
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
        // Dedup check
        if let Some(dup_id) = self.check_duplicate(
            vector,
            &kind,
            tree_path.as_deref(),
            source_path.as_deref(),
        ) {
            return Ok(StoreResult {
                engram_id: String::new(),
                status: StoreStatus::Duplicate,
                duplicate_of: Some(dup_id),
            });
        }

        let id = generate_id();
        let now = now_millis();

        let engram = Engram {
            id: id.clone(),
            text: text.to_string(),
            summary: None,
            vector: vector.to_vec(),
            keywords: vec![],
            content_type: None,
            valence: 0.0,
            arousal: 0.5,
            vitality: 1.0,
            protection: Protection::Normal,
            created_at: now,
            last_activated: now,
            activation_count: 1,
            kind,
            meta: HashMap::new(),
            is_archived: false,
            is_dormant: false,
            turn_id: None,
            tree_path,
            source_path,
            source_textunit,
            turn_ids: Vec::new(),
            context_id: None,
            tree_ref: None,
        };

        let stored_id = self.store_engram(engram)?;

        Ok(StoreResult {
            engram_id: stored_id,
            status: StoreStatus::Stored,
            duplicate_of: None,
        })
    }

    // ── v0.8.0: Plan 管理方法 ─────────────────────────────

    /// 1. Set the name of a plan.
    pub fn set_plan_name(&self, plan_id: &str, name: &str) -> Result<()> {
        let mut txn = self.storage.begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut plan = self.storage.get_plan(&txn, plan_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            .ok_or_else(|| MemHopError::Storage(format!("plan {} not found", plan_id)))?;
        plan.name = name.to_string();
        self.storage.put_plan(&mut txn, &plan)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// 2. Get the plan tree. If plan_id is None, returns all root plans.
    ///    If plan_id is Some, returns that plan and all its descendants (flat list).
    pub fn get_plan_tree(
        &self,
        plan_id: Option<&str>,
    ) -> Result<Vec<crate::engram::PlanNode>> {
        let txn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let all = self.storage.get_all_plans(&txn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        match plan_id {
            None => Ok(all.into_iter().filter(|p| p.parent_id.is_none()).collect()),
            Some(pid) => {
                let mut result = Vec::new();
                let mut queue: Vec<String> = vec![pid.to_string()];
                while let Some(id) = queue.pop() {
                    for plan in &all {
                        if plan.id == id {
                            result.push(plan.clone());
                            break;
                        }
                    }
                    for plan in &all {
                        if plan.parent_id.as_deref() == Some(&id) {
                            queue.push(plan.id.clone());
                        }
                    }
                }
                Ok(result)
            }
        }
    }

    /// 3. Set the LLM provider for optional Dream-layer enhancement.
    pub fn set_llm(&mut self, llm: Box<dyn LlmProvider>) {
        self.llm = Some(llm);
    }

    /// 4. Complete a plan: change state to Completed, set completed_at,
    ///    optionally generate compressed summary via LLM.
    ///    All-or-nothing transaction semantics.
    pub fn complete_plan(&mut self, plan_id: &str) -> Result<()> {
        let mut txn = self.storage.begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut plan = self.storage.get_plan(&txn, plan_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            .ok_or_else(|| MemHopError::Storage(format!("plan {} not found", plan_id)))?;

        let now = now_millis();
        plan.state = PlanState::Completed;
        plan.completed_at = Some(now);

        // Generate compressed summary if LLM is available
        if let Some(ref llm) = self.llm {
            let turns = self.storage.get_dialogues_by_plan(&txn, plan_id)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            if !turns.is_empty() {
                let content: String = turns.iter()
                    .flat_map(|t| vec![t.user_input.as_str(), t.agent_response.as_str()])
                    .collect::<Vec<_>>()
                    .join("\n");
                let prompt = crate::llm_provider::PromptTemplates::summarize(&content);
                match llm.generate(&prompt, 256) {
                    Ok(summary) => { plan.compressed_summary = Some(summary); }
                    Err(e) => eprintln!("[brain] LLM summary failed for plan {}: {}", plan_id, e),
                }
            }
        }

        self.storage.put_plan(&mut txn, &plan)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// 5. Compress a plan's dialogue turns into a Knowledge engram and archive the originals.
    ///    v0.12.0: Full compression — heuristic summary, Knowledge engram creation,
    ///    Episode engram archiving, PlanNode state update to Completed.
    pub fn compress_plan(&mut self, plan_id: &str) -> Result<CompressResult> {
        let now = now_millis();

        // 1. Get PlanNode
        let rtxn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let plan_option = self.storage.get_plan(&rtxn, plan_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(rtxn);

        let plan = match plan_option {
            Some(p) => p,
            None => return Ok(CompressResult {
                knowledge_id: String::new(),
                archived_count: 0,
                summary: String::new(),
                skipped: true,
            }),
        };

        // 2. Read all DialogueTurns
        let rtxn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let turns = self.storage.get_dialogues_by_plan(&rtxn, plan_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(rtxn);

        // 3. If < 3 turns, skip (not enough to compress meaningfully)
        if turns.len() < 3 {
            return Ok(CompressResult {
                knowledge_id: String::new(),
                archived_count: 0,
                summary: String::new(),
                skipped: true,
            });
        }

        // 4. Generate heuristic summary (no LLM)
        let summary = self.heuristic_compress(&turns, &plan.name);

        // 5. Find associated engram IDs via PlanIndex
        let engram_ids: Vec<String> = {
            let pi = self.plan_index.borrow();
            pi.candidates(Some(plan_id))
        };

        // 6. Create Knowledge Engram
        let knowledge_id = generate_id();
        let summary_vector = self.encode_text(&summary);
        let turn_ids: Vec<String> = turns.iter().map(|t| t.id.clone()).collect();
        let knowledge_engram = Engram {
            id: knowledge_id.clone(),
            text: summary.clone(),
            summary: None,
            vector: summary_vector,
            keywords: Vec::new(),
            content_type: None,
            valence: 0.0,
            arousal: 0.5,
            vitality: 1.0,
            protection: Protection::Normal,
            created_at: now,
            last_activated: now,
            activation_count: 1,
            kind: EngramKind::Knowledge,
            meta: {
                let mut m = HashMap::new();
                m.insert("compressed_from_plan".to_string(), serde_json::json!(plan_id));
                m.insert("turn_count".to_string(), serde_json::json!(turns.len()));
                m
            },
            is_archived: false,
            is_dormant: false,
            turn_id: None,
            tree_path: None,
            source_path: None,
            source_textunit: None,
            turn_ids,
            context_id: None,
            tree_ref: None,
        };

        // 7. Store Knowledge Engram (updates all indexes)
        self.store_engram(knowledge_engram)?;

        // 8. Archive original Episode engrams (mark is_archived in LMDB)
        let archived_count = engram_ids.len();
        for engram_id in &engram_ids {
            let rtxn = self.storage.begin_read()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            let mut engram = match self.storage.get_hippocampus(&rtxn, engram_id) {
                Ok(Some(e)) => e,
                _ => { drop(rtxn); continue; }
            };
            drop(rtxn);

            engram.is_archived = true;

            let mut wtxn = self.storage.begin_write()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            self.storage.put_hippocampus(&mut wtxn, engram_id, &engram)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            wtxn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
        }

        // 9. Update PlanNode: set compressed_summary, state=Completed, completed_at
        {
            let mut wtxn = self.storage.begin_write()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            let mut plan_to_update = plan.clone();
            plan_to_update.compressed_summary = Some(summary.clone());
            plan_to_update.state = PlanState::Completed;
            plan_to_update.completed_at = Some(now);
            self.storage.put_plan(&mut wtxn, &plan_to_update)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            wtxn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
        }

        // 10. Update PlanIndex (in-memory)
        {
            let mut pi = self.plan_index.borrow_mut();
            if let Some(info) = pi.plan_info.get_mut(plan_id) {
                info.state = PlanState::Completed;
            }
            if pi.active_plan_id.as_deref() == Some(plan_id) {
                pi.active_plan_id = None;
            }
        }

        // v0.12.1: 检测压缩涉及的 engram 是否来自不同树 → 创建纠缠事件
        {
            let mut tree_ids_set: HashSet<String> = HashSet::new();
            let mut node_ids: Vec<String> = Vec::new();
            let rtxn = self.storage.begin_read()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            for engram_id in &engram_ids {
                if let Ok(Some(engram)) = self.storage.get_hippocampus(&rtxn, engram_id)
                    && let Some(ref tr) = engram.tree_ref
                {
                    tree_ids_set.insert(tr.tree_id.clone());
                    if !node_ids.contains(&engram.id) {
                        node_ids.push(engram.id.clone());
                    }
                }
            }
            drop(rtxn);
            if tree_ids_set.len() >= 2 && node_ids.len() >= 2 {
                let context = format!("Plan 压缩跨树关联: {}", plan.name);
                let tree_ids: Vec<String> = tree_ids_set.into_iter().collect();
                self.create_or_update_entanglement(
                    node_ids, tree_ids, context, EntanglementTrigger::PlanCompression,
                );
            }
        }

        Ok(CompressResult {
            knowledge_id,
            archived_count,
            summary,
            skipped: false,
        })
    }

    /// v0.12.0: Heuristic compression without LLM.
    /// Takes the last agent response as base, prepends up to 3 non-empty user inputs as keywords.
    fn heuristic_compress(&self, turns: &[DialogueTurn], plan_name: &str) -> String {
        let last_response = turns.last()
            .map(|t| t.agent_response.as_str())
            .unwrap_or("");

        if last_response.is_empty() {
            return format!("{}: 对话完成", plan_name);
        }

        // Extract keywords from user inputs (first 3 different non-empty inputs)
        let keywords: Vec<&str> = turns.iter()
            .map(|t| t.user_input.trim())
            .filter(|s| !s.is_empty())
            .take(3)
            .collect();

        format!("{}: {} — {}", plan_name, keywords.join("; "), last_response)
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
        let txn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let plans = self.storage.get_all_plans(&txn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut domains: Vec<String> = plans.into_iter()
            .filter(|p| p.level == PlanLevel::Domain)
            .map(|p| p.name)
            .collect();
        domains.sort();
        domains.dedup();
        Ok(domains)
    }

    /// 8. Get archived dialogue turns for a plan, sorted by timestamp, with pagination.
    pub fn archived_dialogue(
        &self,
        plan_id: &str,
        offset: usize,
        limit: usize,
    ) -> Result<Vec<crate::engram::DialogueTurn>> {
        let txn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let turns = self.storage.get_dialogues_by_plan(&txn, plan_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let turns: Vec<_> = turns.into_iter().skip(offset).take(limit).collect();
        Ok(turns)
    }

    /// 9. Randomly sample up to max_turns dialogue turns from a plan.
    pub fn extract_dialogue_sample(
        &self,
        plan_id: &str,
        max_turns: usize,
    ) -> Result<Vec<crate::engram::DialogueTurn>> {
        let txn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let turns = self.storage.get_dialogues_by_plan(&txn, plan_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        if turns.len() <= max_turns {
            return Ok(turns);
        }
        // Simple random sampling: shuffle and take first max_turns
        use rand::seq::SliceRandom;
        let mut rng = rand::thread_rng();
        let mut sample = turns;
        sample.shuffle(&mut rng);
        sample.truncate(max_turns);
        Ok(sample)
    }

    /// 10. Aggregate tone statistics over a time range.
    pub fn get_tone_aggregates(
        &self,
        start_time: i64,
        end_time: i64,
    ) -> Result<crate::engram::ToneAggregate> {
        let txn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let all_turns = self.storage.all_dialogues(&txn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(txn);

        let turns: Vec<&crate::engram::DialogueTurn> = all_turns.iter()
            .filter(|t| t.timestamp >= start_time && t.timestamp <= end_time)
            .collect();

        if turns.is_empty() {
            return Ok(crate::engram::ToneAggregate {
                time_range_start: start_time,
                time_range_end: end_time,
                avg_valence: 0.0,
                avg_arousal: 0.0,
                valence_trend: 0.0,
                top_tone_tags: Vec::new(),
                filler_ratio_trend: 0.0,
            });
        }

        let n = turns.len() as f32;
        let sum_valence: f32 = turns.iter().map(|t| t.user_tone.valence).sum();
        let sum_arousal: f32 = turns.iter().map(|t| t.user_tone.arousal).sum();
        let avg_valence = sum_valence / n;
        let avg_arousal = sum_arousal / n;

        // Valence trend: early half vs late half
        let mid = turns.len() / 2;
        let early_val: f32 = turns[..mid].iter().map(|t| t.user_tone.valence).sum::<f32>() / mid as f32;
        let late_val: f32 = turns[mid..].iter().map(|t| t.user_tone.valence).sum::<f32>() / (turns.len() - mid) as f32;
        let valence_trend = late_val - early_val;

        // Tone tag frequency
        let mut tag_counts: HashMap<&str, u32> = HashMap::new();
        for t in &turns {
            for tag in &t.user_tone.tone_tags {
                *tag_counts.entry(tag.as_str()).or_default() += 1;
            }
        }
        let mut top_tone_tags: Vec<(String, u32)> = tag_counts.into_iter()
            .map(|(k, v)| (k.to_string(), v))
            .collect();
        top_tone_tags.sort_by_key(|b| std::cmp::Reverse(b.1));
        top_tone_tags.truncate(10);

        // Filler ratio trend
        let early_fill: f32 = turns[..mid].iter().map(|t| t.user_tone.filler_ratio).sum::<f32>() / mid as f32;
        let late_fill: f32 = turns[mid..].iter().map(|t| t.user_tone.filler_ratio).sum::<f32>() / (turns.len() - mid) as f32;
        let filler_ratio_trend = late_fill - early_fill;

        Ok(crate::engram::ToneAggregate {
            time_range_start: start_time,
            time_range_end: end_time,
            avg_valence,
            avg_arousal,
            valence_trend,
            top_tone_tags,
            filler_ratio_trend,
        })
    }

    /// 11. Get topic distribution across all domain-level plans.
    pub fn get_topic_distribution(
        &self,
    ) -> Result<crate::engram::TopicDistribution> {
        let txn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let plans = self.storage.get_all_plans(&txn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(txn);

        let mut domains: HashMap<String, crate::engram::DomainStats> = HashMap::new();
        for plan in &plans {
            if plan.level != PlanLevel::Domain {
                continue;
            }
            let entry = domains.entry(plan.name.clone()).or_insert_with(|| {
                crate::engram::DomainStats {
                    plan_count: 0,
                    dialogue_count: 0,
                    avg_valence: 0.0,
                    top_keywords: Vec::new(),
                }
            });
            entry.plan_count += 1;
            entry.dialogue_count += plan.dialogue_count;
        }

        Ok(crate::engram::TopicDistribution { domains })
    }

    /// 12. Search chat history by n-gram overlap, with optional plan filter and pagination.
    pub fn search_chat_history(
        &self,
        query: &str,
        plan_id: Option<&str>,
        offset: usize,
        limit: usize,
    ) -> Result<Vec<crate::engram::DialogueTurn>> {
        let txn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let all_turns = self.storage.all_dialogues(&txn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(txn);

        // Optional plan filter
        let turns: Vec<crate::engram::DialogueTurn> = match plan_id {
            Some(pid) => all_turns.into_iter().filter(|t| t.plan_id == pid).collect(),
            None => all_turns,
        };

        let query_lower = query.to_lowercase();
        let mut scored: Vec<(f32, crate::engram::DialogueTurn)> = turns.into_iter()
            .map(|t| {
                let user_score = ngram_overlap(&query_lower, &t.user_input.to_lowercase());
                let agent_score = ngram_overlap(&query_lower, &t.agent_response.to_lowercase());
                let score = user_score.max(agent_score);
                (score, t)
            })
            .collect();

        scored.sort_by(|a, b| b.0.partial_cmp(&a.0).unwrap_or(std::cmp::Ordering::Equal));
        scored.truncate(limit + offset);

        Ok(scored.into_iter().skip(offset).filter(|(s, _)| *s > 0.0).map(|(_, t)| t).collect())
    }

    // ── 访问器 ───────────────────────────────────────────

    // ── v0.9.0: Save / close ────────────────────────────

    /// Persist HNSW index before the Brain is discarded.
    pub fn close(&self) -> Result<()> {
        self.hnsw
            .save_to_storage(&self.storage)
            .map_err(|e| MemHopError::Storage(e.to_string()))
    }

    pub fn cortex_len(&self) -> usize {
        self.cortex.len()
    }
    pub fn hippocampus_len(&self) -> usize {
        self.hippocampus.len()
    }
    pub fn memory_count(&self) -> usize {
        self.hopfield.len()
    }
    pub fn hopfield_is_empty(&self) -> bool {
        self.hopfield.is_empty()
    }
    pub fn hnsw_is_empty(&self) -> bool {
        self.hnsw.is_empty()
    }
    pub fn growth_state(&self) -> &GrowthState {
        &self.growth
    }
    pub fn emotional_context(&self) -> &EmotionalContext {
        &self.emotional_ctx
    }

    /// v0.9.1: Build per-turn hit list and per-session aggregation from associated engrams.
    fn build_turn_hits(
        &self,
        associations: &[Engram],
        score_map: &HashMap<String, f32>,
    ) -> Result<(Vec<crate::types::TurnHit>, Vec<crate::types::SessionScore>)> {
        // Group engrams by turn_id
        let mut turn_groups: HashMap<String, Vec<(f32, &Engram)>> = HashMap::new();
        for engram in associations {
            if let Some(ref turn_id) = engram.turn_id {
                let score = score_map.get(&engram.id).copied().unwrap_or(0.0);
                turn_groups.entry(turn_id.clone()).or_default().push((score, engram));
            }
        }

        if turn_groups.is_empty() {
            return Ok((Vec::new(), Vec::new()));
        }

        let rtxn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut hit_turns = Vec::new();
        let mut session_agg: HashMap<String, (f32, Vec<String>)> = HashMap::new();

        for (turn_id, entries) in &turn_groups {
            let (best_score, best_engram) = if let Some(best) = entries.iter()
                .max_by(|a, b| a.0.partial_cmp(&b.0).unwrap_or(std::cmp::Ordering::Equal))
            {
                best
            } else {
                continue;
            };
            let snippet = best_engram.text.chars().take(200).collect::<String>();

            if let Ok(Some(turn)) = self.storage.get_dialogue(&rtxn, turn_id) {
                hit_turns.push(crate::types::TurnHit {
                    engram_id: best_engram.id.clone(),
                    turn_id: turn_id.clone(),
                    session_id: turn.session_id.clone(),
                    score: *best_score,
                    snippet,
                });
                let entry = session_agg.entry(turn.session_id).or_default();
                entry.0 += *best_score;
                entry.1.push(turn_id.clone());
            }
        }

        hit_turns.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
        let mut aggregated_sessions: Vec<crate::types::SessionScore> = session_agg
            .into_iter()
            .map(|(sid, (total, ids))| crate::types::SessionScore {
                session_id: sid,
                total_score: total,
                top_turn_ids: ids.into_iter().take(5).collect(),
            })
            .collect();
        aggregated_sessions.sort_by(|a, b| b.total_score.partial_cmp(&a.total_score).unwrap_or(std::cmp::Ordering::Equal));

        Ok((hit_turns, aggregated_sessions))
    }
}

// ── v0.9.1: Helper functions ──────────────────────────────────

/// Split text at sentence boundaries near `max_chars` chunks.
fn split_text_at_boundaries(text: &str, max_chars: usize) -> Vec<String> {
    let mut segments: Vec<String> = Vec::new();
    let mut start = 0;
    let chars: Vec<char> = text.chars().collect();
    let len = chars.len();

    while start < len {
        if start + max_chars >= len {
            segments.push(chars[start..].iter().collect());
            break;
        }
        // Find the last sentence boundary at or before start + max_chars
        let end = chars[start..start + max_chars]
            .iter()
            .rposition(|&c| c == '.' || c == '!' || c == '?' || c == '\n')
            .map(|pos| start + pos + 1)
            .unwrap_or(start + max_chars);

        // Ensure minimum segment size (500 chars), merge if too short
        if !segments.is_empty() && end - start < 500 {
            // Merge with previous segment
            let last = segments.last_mut().unwrap();
            last.extend(chars[start..end].iter());
        } else {
            segments.push(chars[start..end].iter().collect());
        }
        start = end;
    }

    segments
}

/// Parse a string into a TurnSource, defaulting to User on unrecognized values.
fn parse_turn_source(s: &str) -> crate::engram::TurnSource {
    match s.to_lowercase().as_str() {
        "agent" => crate::engram::TurnSource::Agent,
        "system" => crate::engram::TurnSource::System,
        "external" => crate::engram::TurnSource::External,
        _ => crate::engram::TurnSource::User,
    }
}

// ── ID 生成 ──────────────────────────────────────────────────

fn generate_id() -> String {
    use rand::Rng;
    let mut rng = rand::thread_rng();
    let bytes: [u8; 8] = rng.r#gen();
    let now = now_millis();
    format!(
        "mem_{:016x}_{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}",
        now as u64, bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5], bytes[6], bytes[7],
    )
}

fn now_millis() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .expect("Time went backwards")
        .as_millis() as i64
}

/// Compute character-level trigram overlap between query and text.
fn ngram_overlap(query: &str, text: &str) -> f32 {
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

/// Compute keyword overlap score between two keyword lists.
fn keyword_overlap(a: &[String], b: &[String]) -> f32 {
    if a.is_empty() || b.is_empty() {
        return 0.0;
    }
    let set_a: HashSet<&str> = a.iter().map(|s: &String| s.as_str()).collect();
    let set_b: HashSet<&str> = b.iter().map(|s: &String| s.as_str()).collect();
    let intersection = set_a.intersection(&set_b).count();
    intersection as f32 / set_a.len().min(set_b.len()) as f32
}

/// Compute cosine similarity between two f32 vectors.
fn cosine_similarity(a: &[f32], b: &[f32]) -> f32 {
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


// ── 测试 ─────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use crate::engram::EmotionalState;
    use crate::types::ReflectionKind;

    fn test_storage_path() -> String {
        let dir = tempfile::tempdir().unwrap();
        dir.keep().to_string_lossy().to_string()
    }

    fn simple_input(text: &str, session: &str) -> PerceptionInput {
        PerceptionInput {
            content: text.to_string(),
            vector: vec![f16::from_f32(0.0); crate::engram::VECTOR_DIM],
            emotional_state: EmotionalState::default(),
            attention_anchors: Vec::new(),
            perceived_importance: 0.5,
            session_id: session.to_string(),
            protection: Protection::Normal,
            manual_links: Vec::new(),
            meta: HashMap::new(),
            plan_id: None,
            agent_response: None,
            dialogue_timestamp: None,
            source: None,
            turn_id: String::new(),
            turn_index: 0,
            segment_index: 0,
            topic_label: None,
        }
    }

    #[test]
    fn test_brain_open_and_perceive() {
        let path = test_storage_path();
        let mut brain = Brain::open(&path, BrainConfig::default(), None).unwrap();
        let output = brain.perceive(simple_input("hello world", "s1")).unwrap();
        assert!(!output.engram_id.is_empty());
        assert_eq!(brain.cortex_len(), 1);
        assert_eq!(brain.hippocampus_len(), 1);
    }

    #[test]
    fn test_brain_reflect() {
        let path = test_storage_path();
        let mut brain = Brain::open(&path, BrainConfig::default(), None).unwrap();
        let id = brain
            .reflect(ReflectionInput {
                content: "I noticed a pattern".to_string(),
                kind: ReflectionKind::Pattern,
                anchored_to: vec![],
                emotional_state: EmotionalState::default(),
                session_id: "s1".to_string(),
            })
            .unwrap();
        assert!(!id.is_empty());
    }

    #[test]
    fn test_brain_recall_empty() {
        let path = test_storage_path();
        let brain = Brain::open(&path, BrainConfig::default(), None).unwrap();
        let resp = brain
            .recall(&RecallRequest {
                query: "test".to_string(),
                session_id: "s1".to_string(),
                ..Default::default()
            })
            .unwrap();
        assert!(resp.working_memory.is_empty());
    }

    #[test]
    fn test_brain_dream() {
        let path = test_storage_path();
        let mut brain = Brain::open(&path, BrainConfig::default(), None).unwrap();
        for i in 0..3 {
            let _ = brain
                .perceive(simple_input(&format!("test memory {}", i), "s1"))
                .unwrap();
        }
        let report = brain.dream().unwrap();
        assert!(report.duration_ms < 1000);
    }
}
