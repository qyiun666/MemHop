//! dream — memory consolidation pipeline (8-stage).
//! Stage 1 (NREM): Hyperedge weight time decay + pruning.
//! Stage 2 (REM-Topic Merge): Similar topic merging.
//! Stage 3 (REM-Reflect): Update each topic summary from L1 nodes.
//! Stage 4 (REM-Plan): Consolidate plan summaries.
//! Stage 5 (Co-occurrence): L2→L1 cross-topic hyperedges.
//! Stage 6 (L0 Formation): Extract worldview/values from L2 topics.
//! Stage 7: BM25 + VectorIndex rebuild.
//! Stage 8: Procedural Crystallization.

pub mod l0;
pub mod nrem;
pub mod rem;

use crate::brain::Brain;
use crate::error::Result;
use crate::types::{ConsolidateReport, CrystallizeReport, DreamConfig};

/// Run the 8-stage consolidation pipeline.
pub fn run(brain: &mut Brain, _config: &DreamConfig) -> Result<ConsolidateReport> {
    let start = std::time::Instant::now();
    let mut report = ConsolidateReport::default();

    // Stage 1: NREM — hyperedge weight time decay + pruning
    if let Err(e) = nrem::nrem_decay(brain, &mut report) {
        eprintln!("[dream] NREM decay error: {}", e);
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

    // Stage 6: L0 Formation — extract worldview from L2 topics
    match l0::l0_formation(brain) {
        Ok(updated) => report.l0_updated = updated,
        Err(e) => eprintln!("[dream] L0 formation error: {}", e),
    }

    // Stage 7: Rebuild BM25 + VectorIndex after weight changes
    {
        brain.ensure_l1()?;
        let l1 = brain.l1.as_mut().unwrap();
        let l1_env = brain.l1_env.as_ref().unwrap();
        {
            let mut wtxn = l1_env
                .env
                .write_txn()
                .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
            if let Err(e) = l1.rebuild_bm25(l1_env, &mut wtxn) {
                eprintln!("[dream] BM25 rebuild error: {}", e);
            } else {
                wtxn.commit()
                    .map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
            }
        }
        if let Err(e) = l1.rebuild_vector_index(l1_env) {
            eprintln!("[dream] VectorIndex rebuild error: {}", e);
        }
    }
    // Rebuild L2 topic vector index
    {
        brain.ensure_l2()?;
        let l2 = brain.l2.as_mut().unwrap();
        let l2_env = brain.l2_env.as_ref().unwrap();
        if let Err(e) = l2.rebuild_topic_vectors(l2_env) {
            eprintln!("[dream] L2 topic vectors rebuild error: {}", e);
        }
    }

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
