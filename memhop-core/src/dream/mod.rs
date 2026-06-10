//! dream — memory consolidation pipeline (8-stage → v1.0 reduced).
//! Stage 1 (NREM): Hyperedge weight time decay + pruning (personalized lambda).
//! Stage 1.5 (Temporal Binding): Time-window based hyperedge creation.
//! Stage 2 (REM-Topic Merge): Similar topic merging.
//! Stage 3 (REM-Reflect): Update each topic summary from L1 nodes.
//! Stage 4 (REM-Plan): Consolidate plan summaries.
//! Stage 5 (Co-occurrence): L2→L1 cross-topic hyperedges.
//! Stage 5.5 (Emotional Entanglement): High-emotion nodes auto-link.
//! Stage 5.6 (Cross-Domain Link Discovery): L3 domain bridge topics.
//! Stage 6 (L0 Formation): Extract worldview/values from L2 topics.
//! Stage 6.5 (Reconsolidation): Labile node re-encoding.
//! Stage 7 (REMOVED): BM25 + VectorIndex rebuild — now incremental.
//! Stage 8: Procedural Crystallization.

pub mod emotional;
pub mod l0;
pub mod nrem;
pub mod rem;
pub mod write_batch;

use crate::brain::Brain;
use crate::error::Result;
use crate::types::{ConsolidateReport, CrystallizeReport, DreamConfig};

/// Run the consolidation pipeline.
pub fn run(brain: &mut Brain, _config: &DreamConfig) -> Result<ConsolidateReport> {
    let start = std::time::Instant::now();
    let mut report = ConsolidateReport::default();

    // Stage 1: NREM — hyperedge weight time decay + pruning (personalized lambda)
    if let Err(e) = nrem::nrem_decay(brain, &mut report) {
        eprintln!("[dream] NREM decay error: {}", e);
    }

    // Stage 1.5: Temporal Binding — 同一时间窗口内的节点自动建边
    match nrem::temporal_binding(brain, 24.0) {
        Ok(n) => if n > 0 { eprintln!("[dream] temporal binding: {} hyperedges", n); },
        Err(e) => eprintln!("[dream] temporal binding error: {}", e),
    }

    // Stage 2: REM — topic merging
    if let Err(e) = rem::rem_merge_topics(brain, &mut report) {
        eprintln!("[dream] REM merge error: {}", e);
    }

    // Stage 3: REM — reflect: update each topic summary from L1 nodes
    if let Err(e) = rem::rem_reflect_topics(brain, &mut report) {
        eprintln!("[dream] REM reflect error: {}", e);
    }

    // Stage 4: REM — plan consolidation (v0.17.0: 空操作，改由 LLM 通过 memhop_update_topic 完成)
    if let Err(e) = rem::rem_plan_consolidate(brain, &mut report) {
        eprintln!("[dream] REM plan error: {}", e);
    }

    // Stage 5: Co-occurrence — L2→L1 cross-topic hyperedges
    match crate::organize::reflect::create_cooccurrence_hyperedges(brain) {
        Ok(n) => report.schemas_emerged += n,
        Err(e) => eprintln!("[dream] co-occurrence error: {}", e),
    }

    // Stage 5.5: Emotional Entanglement — 共享高情感强度的节点自动建边
    match emotional::emotional_entanglement(brain) {
        Ok(n) => if n > 0 { eprintln!("[dream] emotional entanglement: {} hyperedges", n); },
        Err(e) => eprintln!("[dream] emotional entanglement error: {}", e),
    }

    // Stage 5.6: Cross-Domain Link Discovery — L3 跨域桥接话题发现
    if brain.ensure_l3().is_ok()
        && let Some(ref l3) = brain.l3
        && let Some(ref store) = brain.redb_store
    {
        match l3.discover_cross_domain_links(store) {
            Ok(links) => {
                if !links.is_empty() {
                    eprintln!("[dream] cross-domain links: {}", links.len());
                }
            }
            Err(e) => eprintln!("[dream] cross-domain link discovery error: {}", e),
        }
    }

    // Stage 6: L0 Formation — extract worldview from L2 topics
    match l0::l0_formation(brain) {
        Ok(updated) => report.l0_updated = updated,
        Err(e) => eprintln!("[dream] L0 formation error: {}", e),
    }

    // Stage 6.5: Reconsolidation — 对 labile 节点做再巩固
    if let Some(ref mut rm) = brain.reconsolidation
        && let Some(ref store) = brain.redb_store
    {
        match rm.reconsolidate(store) {
            Ok(n) => if n > 0 { eprintln!("[dream] reconsolidated: {} nodes", n); },
            Err(e) => eprintln!("[dream] reconsolidation error: {}", e),
        }
    }

    // Stage 7: REMOVED in v1.0 — 索引改为增量维护（在 batch_store 写入时和 reconsolidation 重编码时更新）
    // Dream 期间只衰减超边权重和合并话题，节点内容不变，不需要重建文本/向量索引

    // Stage 8: Procedural Crystallization
    let _crystal_report = match crate::procedural::crystallize(brain) {
        Ok(cr) => {
            report.crystals_created = cr.crystals_created;
            cr
        }
        Err(e) => {
            eprintln!("[dream] crystallization error: {}", e);
            CrystallizeReport::default()
        }
    };

    report.duration_ms = start.elapsed().as_millis() as u64;
    Ok(report)
}
