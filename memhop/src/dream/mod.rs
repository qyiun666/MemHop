//! Dream — 记忆整合引擎（6 阶段：NREM-1/2/3 + REM-1/2/3）
//!
//! 从 brain.rs 抽取的独立模块，内部通过 pub(crate) 函数访问 Brain 字段。

use std::collections::HashSet;
use std::time::Instant;

use crate::brain::Brain;
use crate::error::Result;
use crate::types::DreamReport;

pub(crate) mod nrem;
pub(crate) mod rem;

// ── 编排入口 ──────────────────────────────────────────

/// 执行 Dream 整合（6 阶段）。
/// 触发条件: 每 `dream_interval` 次 perceive | hippocampus 满载 | 显式调用
/// 策略: 增量处理，每次处理一批新记忆，多轮逐步覆盖
pub fn dream(brain: &mut Brain) -> Result<DreamReport> {
    let start = Instant::now();
    let mut report = DreamReport::default();

    // NREM-1: Vitality 衰减 + 归档/遗忘
    if let Err(e) = nrem::nrem_vitality_decay(brain, &mut report) {
        eprintln!("[dream] NREM-1 error: {}", e);
    }

    // NREM-2: 边衰减 + 剪枝（含平均度数 ≤30 强制剪枝）
    if let Err(e) = brain.graph.decay_edges(&brain.storage, brain.personality.decay_lambda()) {
        eprintln!("[dream] NREM-2 decay error: {}", e);
    }
    if let Ok(pruned) = brain.graph.prune_edges(&brain.storage, 0.03) {
        report.pruned_edges = pruned;
    }
    if brain.graph.avg_degree() > 30.0
        && let Ok(extra) = brain.graph.prune_to_max_degree(&brain.storage, 30)
    {
        report.pruned_edges += extra;
    }

    // NREM-2b: v0.9.1 — Turn Crystallizer
    if let Err(e) = nrem::nrem_turn_crystallizer(brain, &mut report) {
        eprintln!("[dream] NREM-2b error: {}", e);
    }

    // REM-1: Hippocampus → Neocortex 整合
    if let Err(e) = rem::rem_consolidate(brain, &mut report) {
        eprintln!("[dream] REM-1 error: {}", e);
    }

    // REM-2: Schema 涌现
    if let Err(e) = rem::rem_schema_emergence(brain, &mut report) {
        eprintln!("[dream] REM-2 error: {}", e);
    }

    // NREM-3: 矛盾检测
    if let Err(e) = nrem::nrem_contradiction_detection(brain, &mut report) {
        eprintln!("[dream] NREM-3 error: {}", e);
    }

    // v0.12.1: NREM — EntanglementEvent 衰减
    if let Err(e) = nrem::nrem_entanglement_decay(brain, &mut report) {
        eprintln!("[dream] NREM entanglement decay error: {}", e);
    }

    // REM-3: 跨 Anchor 发现
    if let Err(e) = rem::rem_cross_anchor_discovery(brain, &mut report) {
        eprintln!("[dream] REM-3 error: {}", e);
    }

    // v0.12.1: REM — EntanglementEvent 创建（跨 Anchor 跨树检测）
    if let Err(e) = rem::rem_entanglement_creation(brain, &mut report) {
        eprintln!("[dream] REM entanglement creation error: {}", e);
    }

    // v0.12.1: REM — 三观模式涌现
    if let Err(e) = rem::rem_worldview_emergence(brain, &mut report) {
        eprintln!("[dream] REM worldview emergence error: {}", e);
    }

    // REM-4: v0.8.0 Cross-plan schema emergence
    if let Err(e) = crate::schema::cross_plan_schema_emergence(brain) {
        eprintln!("[dream] REM-4 cross-plan-schema error: {}", e);
    }

    // v0.9.0: LLM-enhanced dream phases (fire-and-forget, errors are logged)
    let saved_llm = brain.llm.take();
    if let Some(ref llm) = saved_llm {
        if let Err(e) = rem::dream_llm_keywords(brain, &**llm, &mut report) {
            eprintln!("[dream] LLM keywords error: {}", e);
        }
        if let Err(e) = rem::dream_llm_contradictions(brain, &**llm, &mut report) {
            eprintln!("[dream] LLM contradictions error: {}", e);
        }
    }
    brain.llm = saved_llm;

    // v0.11.0: HNSW compact — rebuild index without tombstoned nodes
    {
        let ratio = brain.hnsw.tombstone_ratio();
        if ratio > 0.3 {
            eprintln!("[dream] HNSW tombstone ratio {:.2} > 0.3, compacting", ratio);
            let removed = brain.hnsw.compact();
            if removed > 0 {
                let _ = brain.hnsw.save_to_storage(&brain.storage);
                // Clear tombstones from LMDB config
                let mut wtxn = match brain.storage.begin_write() {
                    Ok(t) => t,
                    Err(e) => {
                        eprintln!("[dream] failed to open txn after compact: {e}");
                        return Ok(report);
                    }
                };
                let _ = brain.storage.put_config(&mut wtxn, "hnsw_tombstones", &Vec::<u64>::new());
                let _ = wtxn.commit();
                report.hnsw_compacted = removed;
            }
        }
    }

    brain.growth.dream_cycles += 1;
    report.duration_ms = start.elapsed().as_millis() as u64;
    Ok(report)
}

/// 内部 dream（无报告输出）。
#[allow(dead_code)]
pub(crate) fn dream_internal(brain: &mut Brain) -> Result<()> {
    let _ = dream(brain)?;
    Ok(())
}

// ── 辅助函数 ──────────────────────────────────────────

/// 当前 Unix 毫秒时间戳。
pub(crate) fn now_millis() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .expect("Time went backwards")
        .as_millis() as i64
}

/// Compute keyword overlap score between two keyword lists.
pub(crate) fn keyword_overlap(a: &[String], b: &[String]) -> f32 {
    if a.is_empty() || b.is_empty() {
        return 0.0;
    }
    let set_a: HashSet<&str> = a.iter().map(|s: &String| s.as_str()).collect();
    let set_b: HashSet<&str> = b.iter().map(|s: &String| s.as_str()).collect();
    let intersection = set_a.intersection(&set_b).count();
    intersection as f32 / set_a.len().min(set_b.len()) as f32
}
