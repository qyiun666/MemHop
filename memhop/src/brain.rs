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
    AssociationKind, EmotionalContext, Engram, EngramKind, Protection,
};
use crate::error::{MemHopError, Result};
use crate::hippocampus::Hippocampus;
use crate::hopfield::ModernHopfield;
use crate::llm_provider::LlmProvider;
use crate::personality::{GrowthState, Personality};
use crate::scene_gating::SceneGate;
use crate::schema;
use crate::storage::LmdbStorage;
use crate::types::{
    BrainConfig, ConflictItem, DreamReport, InnateSchema, PerceptionInput, RecallRequest,
    RecallResponse, RecallTrace, ReflectionInput,
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

        Ok(Brain {
            cortex: Cortex::new(config.cortex_capacity),
            hippocampus,
            graph,
            hopfield,
            storage,
            emotional_ctx: EmotionalContext::new(),
            growth: GrowthState::new(),
            personality,
            config,
            store_count: 0,
            llm,
            ngram_encoder: NgramEncoder::new(crate::engram::VECTOR_DIM),
            recalled_buffer: RefCell::new(Vec::new()),
        })
    }

    // ── perceive ──────────────────────────────────────────

    /// 存入新感知到 Hippocampus。同步，<1ms。
    pub fn perceive(&mut self, input: PerceptionInput) -> Result<String> {
        let now = now_millis();
        let id = generate_id();

        self.emotional_ctx
            .update(input.emotional_state.valence, input.emotional_state.arousal);

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

        if self.store_count >= self.config.dream_interval {
            self.store_count = 0;
            let _ = self.dream_internal();
        }

        Ok(id)
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

        // 3. Hopfield top-K — 语义近似候选
        let hopfield_candidates: Vec<(String, f32)> = if self.hopfield.is_empty() {
            Vec::new()
        } else {
            self.hopfield.recall_topk(&query_f32, HOPFIELD_TOP_K)
        };

        // 3b. Scene gating — 按 attention_anchors 过滤候选集
        let hopfield_candidates = if !req.attention_anchors.is_empty() {
            if let Ok(Some(candidates)) = SceneGate::get_candidates(&self.storage, &req.attention_anchors) {
                hopfield_candidates.into_iter()
                    .filter(|(id, _)| candidates.contains(id))
                    .collect()
            } else {
                hopfield_candidates
            }
        } else {
            hopfield_candidates
        };

        // 4. 构建种子激活图: id → Hopfield 相似度
        let seeds: HashMap<String, f32> = hopfield_candidates
            .iter()
            .take(req.spread_top_k * 2)
            .map(|(id, score)| (id.clone(), *score))
            .collect();

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
                hopfield_candidates: hopfield_candidates.len(),
                spread_steps: 3,
                post_inhibition_count: spread_result.activated.len(),
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

/// Compute keyword overlap score between two keyword lists.
fn keyword_overlap(a: &[String], b: &[String]) -> f32 {
    if a.is_empty() || b.is_empty() {
        return 0.0;
    }
    let set_a: HashSet<&str> = a.iter().map(|s| s.as_str()).collect();
    let set_b: HashSet<&str> = b.iter().map(|s| s.as_str()).collect();
    let intersection = set_a.intersection(&set_b).count();
    intersection as f32 / set_a.len().min(set_b.len()) as f32
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
        }
    }

    #[test]
    fn test_brain_open_and_perceive() {
        let path = test_storage_path();
        let mut brain = Brain::open(&path, BrainConfig::default(), None).unwrap();
        let id = brain.perceive(simple_input("hello world", "s1")).unwrap();
        assert!(!id.is_empty());
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
