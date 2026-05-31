//! ADD-only store API — engram persistence, dedup, and deletion.
//!
//! v0.12.2: Extracted from brain.rs.

use std::collections::HashMap;

use half::f16;

use crate::brain::{cosine_similarity, generate_id, now_millis, Brain};
use crate::encoder::Encoder;
use crate::engram::{AssociationKind, Engram, EngramKind, Protection};
use crate::error::{MemHopError, Result};
use crate::types::{ForgetFilter, StoreResult, StoreStatus};

// ── v0.11.0: 去重检查 ───────────────────────────────────

/// Check if a candidate engram is a duplicate of an existing one.
/// Episode: cosine similarity > 0.95 → duplicate.
/// Knowledge: cosine similarity > 0.9 AND same tree_path+source_path → duplicate.
pub(crate) fn check_duplicate(
    brain: &Brain,
    vector: &[f16],
    kind: &EngramKind,
    tree_path: Option<&str>,
    source_path: Option<&str>,
) -> Option<String> {
    let threshold = match kind {
        EngramKind::Knowledge => 0.9,
        _ => 0.95,
    };

    let cache = brain.engram_cache.borrow();
    let vec_f32: Vec<f32> = vector.iter().map(|x| x.to_f32()).collect();

    for (id, existing) in cache.entries() {
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
pub(crate) fn store_engram(brain: &mut Brain, mut engram: Engram) -> Result<String> {
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
        let mut wtxn = brain
            .storage
            .begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        brain
            .storage
            .put_hippocampus(&mut wtxn, &id, &engram)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn
            .commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    // 1b. Maintain in-memory hippocampus order (for len() and Dream iteration)
    brain.hippocampus.push_id(&id);

    // 2. EngramCache (hot cache)
    brain.engram_cache.borrow_mut().insert(id.clone(), engram.clone());

    // 3. HNSW insert (best-effort)
    let node_id = brain.next_node_id;
    brain.next_node_id += 1;
    brain.hnsw.insert(node_id, &engram.vector);
    brain.hnsw_id_map.insert(node_id, id.clone());

    // 4. SparseIndex add (best-effort)
    let encoded = brain.ngram_encoder.encode(&engram.text);
    let doc_length = engram.text.chars().count();
    brain.sparse_index.add(&id, &encoded.sparse, doc_length);

    // 5. Hopfield add (best-effort, with weight)
    let weight = match engram.kind {
        EngramKind::Knowledge => brain.config.hopfield.knowledge_pattern_weight,
        _ => 1.0,
    };
    brain.hopfield.add_pattern_weighted(&id, &engram.vector, weight);

    // 6. CoShelf edge creation (for Knowledge engrams with adjacent chunks)
    if engram.kind == EngramKind::Knowledge
        && let Some(ref tree_path) = engram.tree_path
    {
            let key = tree_path.clone();
            if let Some(prev_id) = brain.last_chunk_per_tree.get(&key) {
                let now = now_millis();
                // Create bidirectional CoShelf edge with weight 0.7
                let _ = brain.graph.add_edge(
                    &brain.storage,
                    prev_id,
                    &id,
                    0.7,
                    AssociationKind::CoShelf,
                    now,
                );
                let _ = brain.graph.add_edge(
                    &brain.storage,
                    &id,
                    prev_id,
                    0.7,
                    AssociationKind::CoShelf,
                    now,
                );
            }
            brain.last_chunk_per_tree.insert(key, id.clone());
    }

    brain.growth.total_engrams_created += 1;

    Ok(id)
}

// ── v0.11.0: 公共 store API ─────────────────────────────

/// Public ADD-only store API.
/// Returns StoreResult indicating whether stored or duplicate.
pub(crate) fn store(
    brain: &mut Brain,
    text: &str,
    vector: &[f16],
    kind: EngramKind,
    tree_path: Option<String>,
    source_path: Option<String>,
    source_textunit: Option<String>,
) -> Result<StoreResult> {
    // Dedup check
    if let Some(dup_id) = check_duplicate(
        brain,
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

    let stored_id = store_engram(brain, engram)?;

    Ok(StoreResult {
        engram_id: stored_id,
        status: StoreStatus::Stored,
        duplicate_of: None,
    })
}

// ── v0.9.1: Forget ────────────────────────────────────

/// Forget all engrams and the DialogueTurn for a given turn_id.
#[deprecated(note = "use forget_batch with ForgetFilter::ByTurnId")]
pub(crate) fn forget(brain: &mut Brain, turn_id: &str) -> Result<()> {
    let count = forget_batch(brain, &ForgetFilter::ByTurnId(turn_id.to_string()))?;
    // Also delete the dialogue turn for backward compatibility.
    if let Ok(wtxn) = brain.storage.begin_write() {
        let mut txn = wtxn;
        let _ = brain.storage.delete_dialogue(&mut txn, turn_id);
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
pub(crate) fn forget_batch(brain: &mut Brain, filter: &ForgetFilter) -> Result<usize> {
    // 1. Read all hippocampus entries from LMDB
    let rtxn = brain
        .storage
        .begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let entries = brain
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
        brain.hopfield.remove_pattern(id);

        // B. HNSW mark_deleted
        let found: Vec<u64> = brain
            .hnsw_id_map
            .iter()
            .filter(|(_, sid)| *sid == id)
            .map(|(nid, _)| *nid)
            .collect();
        for node_id in found {
            brain.hnsw.mark_deleted(node_id);
        }

        // C. SparseIndex remove
        brain.sparse_index.remove(id);

        // D. Graph remove node (removes all incident edges)
        let _ = brain.graph.remove_node(&brain.storage, id);

        // E. LMDB delete
        {
            let mut wtxn = brain
                .storage
                .begin_write()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            brain.storage
                .delete_hippocampus(&mut wtxn, id)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            wtxn
                .commit()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
        }

        // F. Remove from EngramCache if present
        brain.engram_cache.borrow_mut().remove(id);

        // G. Clean up last_chunk_per_tree
        if engram.kind == EngramKind::Knowledge
            && let Some(ref tp) = engram.tree_path
            && brain.last_chunk_per_tree.get(tp) == Some(id)
        {
            brain.last_chunk_per_tree.remove(tp);
        }
    }

    // 4. Persist HNSW tombstones to LMDB config
    {
        let mut wtxn = brain
            .storage
            .begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let tombstone_ids: Vec<u64> = brain.hnsw.tombstones.iter().copied().collect();
        brain.storage
            .put_config(&mut wtxn, "hnsw_tombstones", &tombstone_ids)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        wtxn
            .commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
    }

    Ok(count)
}
