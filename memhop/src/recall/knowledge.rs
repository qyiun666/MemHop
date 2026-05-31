use half::f16;

use crate::Brain;
use crate::engram::{Engram, EngramKind};

/// v0.12.0: Retrieve attached knowledge from knowledge trees.
///
/// Uses HNSW search on Knowledge engrams, applies cosine threshold filtering,
/// returns at most KNOWLEDGE_ATTACH_MAX results.
pub(crate) fn recall_knowledge_attached(brain: &Brain, query: &[f16]) -> Vec<Engram> {
    const HNSW_K: usize = crate::brain::KNOWLEDGE_ATTACH_LIMIT * 10;

    let hnsw_results = brain.hnsw.search(query, HNSW_K);
    let hnsw_strings: Vec<(String, f32)> = hnsw_results
        .iter()
        .filter_map(|(node_id, sim)| {
            brain
                .hnsw_id_map
                .get(node_id)
                .map(|sid| (sid.clone(), *sim))
        })
        .collect();

    // Filter candidates by cosine threshold and kind
    let mut candidates: Vec<(String, f32)> = Vec::new();
    if let Ok(rtxn) = brain.storage.begin_read() {
        for (id, cos_sim) in &hnsw_strings {
            if *cos_sim <= crate::brain::KNOWLEDGE_THRESHOLD {
                continue;
            }
            let engram = brain.engram_cache.borrow().get(id).cloned();
            let engram = match engram {
                Some(e) => e,
                None => {
                    if let Ok(Some(e)) = brain.storage.get_hippocampus(&rtxn, id) {
                        brain
                            .engram_cache
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
    candidates.truncate(crate::brain::KNOWLEDGE_ATTACH_MAX);

    // Load full engrams
    let mut results = Vec::with_capacity(candidates.len());
    if let Ok(rtxn) = brain.storage.begin_read() {
        for (id, _) in &candidates {
            if let Ok(Some(engram)) = brain.storage.get_hippocampus(&rtxn, id) {
                results.push(engram);
            }
        }
    }
    results
}
