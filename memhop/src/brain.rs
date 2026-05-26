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
use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::Instant;

use half::f16;

use crate::activation;
use crate::cortex::Cortex;
use crate::encoder::{Encoder, NgramEncoder};
use crate::engram::{
    AssociationKind, DialogueTurn, EmotionalContext, Engram, EngramKind, PlanLevel, PlanNode, PlanState,
    Protection, StyleCompact, ToneMeta,
};
use crate::error::{MemHopError, Result};
use crate::hippocampus::Hippocampus;
use crate::hopfield::ModernHopfield;
use crate::llm_provider::LlmProvider;
use crate::personality::{GrowthState, Personality};
use crate::plan_gate::{PlanContext, PlanGate, PlanIndex};
use crate::scene_gating::SceneGate;
use crate::schema;
use crate::storage::LmdbStorage;
use crate::types::{
    BrainConfig, ConflictItem, DreamReport, InnateSchema, PerceptionInput, PerceptionOutput,
    RecallRequest, RecallResponse, RecallTrace, ReflectionInput,
};
use crate::unified_graph::UnifiedGraph;
use crate::vitality;

// ── 常量 ─────────────────────────────────────────────────────

const HOPFIELD_BETA: f32 = 8.0;
const HOPFIELD_TOP_K: usize = 200;

// ── Brain ────────────────────────────────────────────────────

/// MemHop Brain — 三层记忆架构的顶层 API。
pub struct Brain {
    cortex: Cortex,
    hippocampus: Hippocampus,
    graph: UnifiedGraph,
    hopfield: ModernHopfield,
    storage: Arc<LmdbStorage>,

    emotional_ctx: EmotionalContext,
    growth: GrowthState,
    personality: Personality,
    config: BrainConfig,
    store_count: usize,

    #[allow(dead_code)]
    llm: Option<Box<dyn LlmProvider>>,
    ngram_encoder: NgramEncoder,
    plan_gate: PlanGate,
    /// Timestamp (Unix ms) of last perceive call — for PlanGate time-gap.
    last_perceive_at: i64,
    /// v0.8.0: In-memory auxiliary index for fast plan lookups.
    plan_index: RefCell<PlanIndex>,

    /// Recall buffer: IDs recalled since last Dream, for reconsolidation.
    recalled_buffer: RefCell<Vec<String>>,
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

        let personality = config.personality;
        let hippocampus = Hippocampus::rebuild(&storage, config.hippocampus_capacity)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        let graph = UnifiedGraph::rebuild(&storage)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        let hopfield = Self::rebuild_hopfield(&storage)?;

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

        Ok(Brain {
            cortex: Cortex::new(config.cortex_capacity),
            hippocampus,
            graph,
            hopfield,
            storage,
            emotional_ctx: EmotionalContext::new(),
            growth: GrowthState::new(),
            personality,
            store_count: 0,
            llm,
            ngram_encoder: NgramEncoder::new(crate::engram::VECTOR_DIM),
            plan_gate: PlanGate::new(
                config.plan_boundary_threshold.unwrap_or(0.55),
                3,
                24,
            ),
            plan_index: RefCell::new(plan_index),
            config,
            recalled_buffer: RefCell::new(Vec::new()),
            last_perceive_at: 0,
        })
    }

    // ── perceive ──────────────────────────────────────────

    /// 存入新感知到 Hippocampus。同步，<1ms。
    pub fn perceive(&mut self, input: PerceptionInput) -> Result<PerceptionOutput> {
        let now = now_millis();
        let id = generate_id();

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

        // ── v0.8.0: Populate PlanIndex ──
        {
            let mut pi = self.plan_index.borrow_mut();
            pi.add_engram(&plan_id, &id);
            pi.update_centroid(&plan_id, &query_f32);
            if pi.active_plan_id.is_none() {
                pi.active_plan_id = Some(plan_id.clone());
            }
        }

        // Save data for DialogueTurn before input is consumed
        let saved_content = input.content.clone();
        let saved_vector = input.vector.clone();
        let saved_agent_response = input.agent_response.clone();
        let saved_dialogue_timestamp = input.dialogue_timestamp;

        // ── Create engram ────────────────────────────────────

        let engram = Engram::new_episode(
            id.clone(),
            input.content,
            input.vector,
            Vec::new(),
            input.emotional_state.valence,
            input.emotional_state.arousal,
            now,
        );

        self.cortex.push(engram.clone(), &input.session_id);
        self.hippocampus.store(&self.storage, &engram)?;
        self.hopfield.add_pattern(&id, &engram.vector);

        // 建立时间边（与 Hippocampus 中最近 3 条）
        let recent_entries = self
            .hippocampus
            .batch_entries(&self.storage, self.hippocampus.len().saturating_sub(4), 3)?;
        for (recent_id, _) in &recent_entries {
            if recent_id.as_str() != id.as_str() {
                self.graph.add_edge(
                    &self.storage, &id, recent_id, 0.5, AssociationKind::Temporal, now,
                )?;
                self.graph.add_edge(
                    &self.storage, recent_id, &id, 0.5, AssociationKind::Temporal, now,
                )?;
            }
        }

        self.growth.total_perceptions += 1;
        self.growth.total_engrams_created += 1;
        self.store_count += 1;

        // 记录到 Anchor 索引
        if !input.attention_anchors.is_empty() {
            let _ = SceneGate::add_to_anchors(&self.storage, &id, &input.attention_anchors);
        }

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

            // v0.8.0: Create DialogueTurn if agent_response is present
            if let Some(ref agent_resp) = saved_agent_response {
                let turn = DialogueTurn {
                    id: format!("turn_{}_{}", now, self.store_count),
                    plan_id: plan_id.clone(),
                    user_input: saved_content,
                    agent_response: agent_resp.clone(),
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
                    vector: saved_vector,
                };
                self.storage
                    .put_dialogue(&mut txn, &turn)
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
            }

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

    /// 召回。p99 < 2ms @ 100K。
    pub fn recall(&self, req: &RecallRequest) -> Result<RecallResponse> {
        let start = Instant::now();

        // 1. Query vector
        let query_vector: Vec<f16> = match &req.query_vector {
            Some(v) => v.clone(),
            None => self.ngram_encoder.encode(&req.query).dense,
        };
        let query_f32: Vec<f32> = query_vector.iter().map(|&x| x.to_f32()).collect();

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

        // 6. 从存储加载激活的 Engram，按类型分类
        let mut associations: Vec<Engram> = Vec::new();
        let mut schemas: Vec<Engram> = Vec::new();
        let mut emotional_echoes: Vec<Engram> = Vec::new();
        let mut conflicts: Vec<ConflictItem> = Vec::new();

        if let Ok(rtxn) = self.storage.begin_read() {
            let activated_ids: Vec<String> = spread_result.activated.iter().map(|(id, _)| id.clone()).collect();
            for id in &activated_ids {
                if let Ok(Some(engram)) = self.storage.get_hippocampus(&rtxn, id) {
                    match engram.kind {
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

        // 8. 记录被召回的记忆 ID，供 Dream reconsolidation 处理
        {
            let mut buf = self.recalled_buffer.borrow_mut();
            for (id, _) in &spread_result.activated {
                if !buf.contains(id) {
                    buf.push(id.clone());
                }
            }
        }

        let latency_us = start.elapsed().as_micros() as u64;

        Ok(RecallResponse {
            working_memory,
            associations,
            schemas,
            emotional_echoes,
            conflicts,
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

        // REM-3: 跨 Anchor 发现
        if let Err(e) = self.rem_cross_anchor_discovery(&mut report) {
            eprintln!("[dream] REM-3 error: {}", e);
        }

        // REM-4: v0.8.0 Cross-plan schema emergence
        if let Err(e) = schema::cross_plan_schema_emergence(self) {
            eprintln!("[dream] REM-4 cross-plan-schema error: {}", e);
        }

        self.growth.dream_cycles += 1;
        report.duration_ms = start.elapsed().as_millis() as u64;
        Ok(report)
    }

    fn dream_internal(&mut self) -> Result<()> {
        let _ = self.dream()?;
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

        // Collect IDs to forget before mutating self
        let mut to_forget: Vec<String> = Vec::new();

        for (id, mut engram) in entries {
            // 只处理 Episode 类型的记忆
            if engram.kind != EngramKind::Episode {
                continue;
            }
            // 永久保护的不参与衰减
            if engram.protection == Protection::Permanent {
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

    /// 对 Hippocampus 中的 Episode 进行增量聚类。
    /// cosine > 0.7 → 归入同一簇
    /// 簇大小 ≥3 → 调用 try_emerge_schema() 创建 Schema 节点
    fn rem_schema_emergence(&mut self, report: &mut DreamReport) -> Result<()> {
        let entries = self.hippocampus.all_entries(&self.storage)?;
        let episodes: Vec<(String, Engram)> = entries
            .into_iter()
            .filter(|(_, e)| e.kind == EngramKind::Episode && !e.is_archived)
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

    // ── 私有辅助 ─────────────────────────────────────────

    fn rebuild_hopfield(storage: &LmdbStorage) -> Result<ModernHopfield> {
        let txn = storage
            .begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let entries = storage
            .all_hippocampus_entries(&txn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(txn);

        let mut hopfield = ModernHopfield::new(crate::engram::VECTOR_DIM, HOPFIELD_BETA);
        for (id, engram) in &entries {
            hopfield.add_pattern(id, &engram.vector);
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

    /// 5. Compress a plan's dialogue turns into a summary.
    pub fn compress_plan(&mut self, plan_id: &str) -> Result<String> {
        let rtxn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let turns = self.storage.get_dialogues_by_plan(&rtxn, plan_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(rtxn);

        let summary = if let Some(ref llm) = self.llm {
            let content: String = turns.iter()
                .flat_map(|t| vec![t.user_input.as_str(), t.agent_response.as_str()])
                .collect::<Vec<_>>()
                .join("\n");
            let prompt = crate::llm_provider::PromptTemplates::summarize(&content);
            llm.generate(&prompt, 256).unwrap_or_else(|e| {
                eprintln!("[brain] LLM summary failed: {}", e);
                fallback_summary(&turns)
            })
        } else {
            fallback_summary(&turns)
        };

        let mut wtxn = self.storage.begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let mut plan = self.storage.get_plan(&wtxn, plan_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
            .ok_or_else(|| MemHopError::Storage(format!("plan {} not found", plan_id)))?;
        plan.compressed_summary = Some(summary.clone());
        self.storage.put_plan(&mut wtxn, &plan)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;

        Ok(summary)
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

    /// 8. Get archived dialogue turns for a plan, sorted by timestamp.
    pub fn archived_dialogue(
        &self,
        plan_id: &str,
    ) -> Result<Vec<crate::engram::DialogueTurn>> {
        let txn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        self.storage.get_dialogues_by_plan(&txn, plan_id)
            .map_err(|e| MemHopError::Storage(e.to_string()))
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
        top_tone_tags.sort_by(|a, b| b.1.cmp(&a.1));
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

    /// 12. Search chat history by n-gram overlap.
    pub fn search_chat_history(
        &self,
        query: &str,
        limit: usize,
    ) -> Result<Vec<crate::engram::DialogueTurn>> {
        let txn = self.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let all_turns = self.storage.all_dialogues(&txn)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(txn);

        let query_lower = query.to_lowercase();
        let mut scored: Vec<(f32, crate::engram::DialogueTurn)> = all_turns.into_iter()
            .map(|t| {
                let user_score = ngram_overlap(&query_lower, &t.user_input.to_lowercase());
                let agent_score = ngram_overlap(&query_lower, &t.agent_response.to_lowercase());
                let score = user_score.max(agent_score);
                (score, t)
            })
            .collect();

        scored.sort_by(|a, b| b.0.partial_cmp(&a.0).unwrap_or(std::cmp::Ordering::Equal));
        scored.truncate(limit);

        Ok(scored.into_iter().filter(|(s, _)| *s > 0.0).map(|(_, t)| t).collect())
    }

    // ── 访问器 ───────────────────────────────────────────

    pub fn cortex_len(&self) -> usize {
        self.cortex.len()
    }
    pub fn hippocampus_len(&self) -> usize {
        self.hippocampus.len()
    }
    pub fn memory_count(&self) -> usize {
        self.hopfield.len()
    }
    pub fn growth_state(&self) -> &GrowthState {
        &self.growth
    }
    pub fn emotional_context(&self) -> &EmotionalContext {
        &self.emotional_ctx
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

/// Fallback summary: concatenate first N turns' content when no LLM is available.
fn fallback_summary(turns: &[crate::engram::DialogueTurn]) -> String {
    let n = turns.len().min(5);
    turns.iter()
        .take(n)
        .flat_map(|t| vec![t.user_input.as_str(), t.agent_response.as_str()])
        .map(|s| s.chars().take(80).collect::<String>())
        .collect::<Vec<_>>()
        .join(" | ")
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
