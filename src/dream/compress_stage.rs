// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Stage: L2 Merge-Compress — adjacent-topic detection + merge + subtree sink.

use crate::dream::llm::LlmProvider;
use crate::encoder::Encoder;
use crate::file::free_list::free_page;
use crate::file::header::FileHeader;
use crate::file::page::{allocate_page, encode_page_ref, write_page_data};
use crate::index::btree::BTreeIndex;
use crate::index::l2_meta::L2MetaIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::{ActivationState, ContextSlot, LlmParams};
use crate::query::types::MergeCompressResult;
use crate::util::{get_current_timestamp, PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;
use std::fs::File;

/// Dream-stage internal group of adjacent same-topic depth-1 nodes (not persisted).
struct TurnGroup {
    scene_id: u64,
    nodes: Vec<ContextSlot>,
}

/// Compute recommended LLM parameters based on context content features.
///
/// Heuristics:
/// - Code keyword density > 0.05 → temperature 0.1-0.3 (technical precision)
/// - Emotion word density > 0.03 → temperature 0.7-0.9, top_p relaxed (creative/emotional)
/// - Knowledge density (turn_count + archive_refs + l3_refs) → presence_penalty boost
fn compute_llm_params(ctx: &ContextSlot, compressed_summary: &str) -> LlmParams {
    let text = format!("{} {}", ctx.title, compressed_summary);
    let text_lower = text.to_lowercase();
    let word_count = text.split_whitespace().count().max(1);

    let code_keywords = [
        "fn",
        "let",
        "struct",
        "impl",
        "async",
        "pub",
        "use",
        "match",
        "return",
        "if",
        "for",
        "while",
        "loop",
        "mod",
        "enum",
        "trait",
        "type",
        "const",
        "mut",
        "ref",
        "self",
        "super",
        "crate",
        "extern",
        "unsafe",
        "move",
        "where",
        "dyn",
        "static",
        "yield",
        "await",
        "function",
        "class",
        "var",
        "def",
        "import",
        "from",
        "else",
        "try",
        "catch",
        "new",
        "this",
        "null",
        "undefined",
        "true",
        "false",
        "=>",
        "{}",
        "();",
        "::",
        "->",
        "==",
        "!=",
        "===",
    ];
    let code_count = code_keywords
        .iter()
        .filter(|kw| text_lower.contains(&kw.to_lowercase()))
        .count();
    let code_density = code_count as f32 / word_count as f32;

    let emotion_words = [
        "开心",
        "难过",
        "生气",
        "害怕",
        "惊喜",
        "失望",
        "兴奋",
        "焦虑",
        "愤怒",
        "恐惧",
        "love",
        "hate",
        "happy",
        "sad",
        "angry",
        "afraid",
        "excited",
        "worried",
        "frustrated",
        "joy",
        "sorrow",
        "fear",
        "hope",
        "despair",
        "delighted",
        "annoyed",
        "anxious",
        "！",
        "？",
        "!!",
        "???",
        "哈哈",
        "呵呵",
        "呜呜",
        "嘿嘿",
        "哼",
        "啊",
        "哦",
        "哇",
    ];
    let emotion_count = emotion_words
        .iter()
        .filter(|ew| text_lower.contains(&ew.to_lowercase()))
        .count();
    let emotion_density = emotion_count as f32 / word_count as f32;

    let knowledge_density = (ctx.turn_count as f32 * 0.1
        + ctx.archive_refs.len() as f32 * 0.2
        + ctx.l3_refs.len() as f32 * 0.3)
        .min(1.0);

    let temperature = if code_density > 0.05 {
        0.1 + code_density.min(0.2) // technical: 0.1-0.3
    } else if emotion_density > 0.03 {
        0.7 + emotion_density.min(0.2) // emotional: 0.7-0.9
    } else {
        0.5 // default
    };

    let top_p = (0.85 + emotion_density * 0.5).min(1.0);
    let presence_penalty = (knowledge_density * 0.6).min(0.6);
    let frequency_penalty = (ctx.turn_count as f32 * 0.02).min(0.5);

    LlmParams {
        temperature,
        top_p,
        presence_penalty,
        frequency_penalty,
    }
}

/// Merge adjacent depth-1 contexts within the same scene via LLM-driven topic
/// detection.  For each detected group of same-topic adjacent nodes, create a new
/// depth-1 parent, sink the old nodes (depth + 1), and delete any subtree whose
/// depth reaches 4.
///
/// # Arguments
/// * `mmap` - Mutable memory-mapped file.
/// * `header` - File header for page allocation / free list.
/// * `btree` - B-tree index.
/// * `sparse_index` - Sparse (BM25) index.
/// * `l2_meta` - In-memory L2 metadata index (mutable).
/// * `llm` - LLM provider for topic detection and merge summarization.
/// * `active_scene_ids` - Scene IDs whose depth-1 nodes should be considered.
/// * `file` - Backing file for mmap extension.
/// * `encoder` - Optional encoder for centroid vectors.
#[allow(clippy::too_many_arguments)]
pub fn l2_merge_compress(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    llm: &dyn LlmProvider,
    active_scene_ids: &HashSet<u64>,
    file: &mut File,
    encoder: Option<&(dyn Encoder + Send + Sync)>,
) -> Result<MergeCompressResult, MemHopError> {
    let mut result = MergeCompressResult {
        groups_detected: 0,
        nodes_merged: 0,
        parent_nodes_created: 0,
        nodes_sunk: 0,
        nodes_removed: 0,
    };

    for &scene_id in active_scene_ids {
        let groups = detect_adjacent_same_topic(scene_id, mmap, btree, l2_meta, llm)?;
        for (group_idx, group) in groups.iter().enumerate() {
            result.groups_detected += 1;
            result.nodes_merged += group.nodes.len() as u32;

            let parent_node = merge_and_compress(
                group,
                group_idx,
                mmap,
                header,
                btree,
                sparse_index,
                llm,
                file,
                encoder,
            )?;

            // Save parent node to mmap
            let parent_page_id = allocate_page(mmap, header, PageType::Context, 2, 0, file)?;
            let parent_data = parent_node
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            write_page_data(mmap, parent_page_id, &parent_data)?;
            let parent_ref = encode_page_ref(parent_page_id, 0);
            btree.insert(parent_node.id_hash, parent_ref);

            // Index parent for BM25
            let mut index_text = parent_node.title.clone();
            if let Some(ref s) = parent_node.summary {
                index_text.push(' ');
                index_text.push_str(s);
            }
            let index_terms = crate::index::sparse::tokenize(&index_text);
            let doc_len = index_terms.len() as u32;
            sparse_index.add_document(parent_node.id_hash, index_terms, doc_len);

            result.parent_nodes_created += 1;

            // Sink each original depth-1 node under the new parent
            for child in &group.nodes {
                sink_subtree(
                    child.id_hash,
                    parent_node.id_hash,
                    mmap,
                    header,
                    btree,
                    sparse_index,
                    l2_meta,
                    file,
                    &mut result,
                )?;
            }

            // Register parent in the L2 meta index
            l2_meta.update_from_context(&parent_node);
        }
    }

    Ok(result)
}

/// Detect groups of adjacent same-topic depth-1 nodes within a single scene.
///
/// Uses the LLM to check whether each consecutive pair (sorted by `created_at`)
/// belongs to the same conversation topic.  Only returns groups with ≥ 2 nodes.
fn detect_adjacent_same_topic(
    scene_id: u64,
    mmap: &[u8],
    btree: &BTreeIndex,
    l2_meta: &L2MetaIndex,
    llm: &dyn LlmProvider,
) -> Result<Vec<TurnGroup>, MemHopError> {
    let depth1_ids = match l2_meta.get_by_scene_depth(scene_id, 1) {
        Some(ids) => ids.clone(),
        None => return Ok(vec![]),
    };

    if depth1_ids.len() < 2 {
        return Ok(vec![]);
    }

    // Load all depth-1 ContextSlots
    let mut nodes: Vec<ContextSlot> = Vec::with_capacity(depth1_ids.len());
    for &id_hash in &depth1_ids {
        let page_ref = match btree.search(id_hash) {
            Some(pr) => pr,
            None => continue,
        };
        let slot_data = match crate::shared::slot_io::get_slot_data(mmap, page_ref) {
            Some(d) => d,
            None => continue,
        };
        if let Ok(ctx) = ContextSlot::deserialize_slot(slot_data) {
            nodes.push(ctx);
        }
    }

    if nodes.len() < 2 {
        return Ok(vec![]);
    }

    // Sort by created_at (earliest first)
    nodes.sort_by_key(|n| n.created_at);

    // Run topic check on each adjacent pair
    let mut adjacency = Vec::with_capacity(nodes.len() - 1);
    for i in 0..nodes.len() - 1 {
        let prev_summary = nodes[i].summary.as_deref().unwrap_or("");
        let curr_summary = nodes[i + 1].summary.as_deref().unwrap_or("");
        let same = if prev_summary.is_empty() || curr_summary.is_empty() {
            false
        } else {
            llm.check_same_topic(prev_summary, curr_summary).unwrap_or(false)
        };
        adjacency.push(same);
    }

    // Group consecutive same-topic nodes
    let mut groups: Vec<TurnGroup> = Vec::new();
    let mut i = 0;
    while i < nodes.len() {
        if i + 1 < nodes.len() && adjacency[i] {
            let mut group_nodes = vec![nodes[i].clone()];
            group_nodes.push(nodes[i + 1].clone());
            i += 1;
            while i + 1 < nodes.len() && adjacency[i] {
                i += 1;
                group_nodes.push(nodes[i].clone());
            }
            if group_nodes.len() >= 2 {
                groups.push(TurnGroup {
                    scene_id,
                    nodes: group_nodes,
                });
            }
        } else {
            i += 1;
        }
    }

    Ok(groups)
}

/// Merge a group of same-topic nodes into a new parent `ContextSlot`.
///
/// The new node is depth-1, parent_id=None, and its `children_ids` references
/// all original nodes.  A centroid vector is computed from the merged summary
/// if an encoder is available.
#[allow(clippy::too_many_arguments)]
fn merge_and_compress(
    group: &TurnGroup,
    group_idx: usize,
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &BTreeIndex,
    sparse_index: &SparseIndex,
    llm: &dyn LlmProvider,
    file: &mut File,
    encoder: Option<&(dyn Encoder + Send + Sync)>,
) -> Result<ContextSlot, MemHopError> {
    let _ = (btree, sparse_index); // used implicitly via encoding/BM25 indexing

    // Collect texts for LLM merge summarization
    let texts: Vec<String> = group
        .nodes
        .iter()
        .map(|n| {
            format!(
                "Title: {}\nSummary: {}",
                n.title,
                n.summary.as_deref().unwrap_or("(none)")
            )
        })
        .collect();

    let (new_title, new_summary) = llm.merge_summarize(&texts)?;

    // Compute centroid from the merged summary text
    let centroid_text = group
        .nodes
        .iter()
        .filter_map(|n| n.summary.as_deref())
        .collect::<Vec<&str>>()
        .join(" ");

    let centroid_page_ref = if let Some(enc) = encoder {
        match enc.encode(&centroid_text) {
            Ok(output) => {
                let v_page_id = allocate_page(mmap, header, PageType::VectorMatrix, 2, 0, file)?;
                let v_offset = crate::shared::slot_io::slot_offset(v_page_id);
                let v_bytes: Vec<u8> =
                    output.dense.iter().flat_map(|v| v.to_ne_bytes()).collect();
                if v_offset + v_bytes.len() > mmap.len() {
                    tracing::warn!("Centroid page allocation failed, centroid omitted");
                    let _ = free_page(mmap, header, v_page_id);
                    0
                } else {
                    mmap[v_offset..v_offset + v_bytes.len()].copy_from_slice(&v_bytes);
                    encode_page_ref(v_page_id, 0)
                }
            }
            Err(e) => {
                tracing::warn!("Failed to encode merged centroid: {}", e);
                0
            }
        }
    } else {
        0
    };

    // Merge archive_refs (deduplicated)
    let mut archive_refs: Vec<u64> = Vec::new();
    for node in &group.nodes {
        for &rid in &node.archive_refs {
            if !archive_refs.contains(&rid) {
                archive_refs.push(rid);
            }
        }
    }

    // Merge l3_refs (deduplicated)
    let mut l3_refs: Vec<u64> = Vec::new();
    for node in &group.nodes {
        for &rid in &node.l3_refs {
            if !l3_refs.contains(&rid) {
                l3_refs.push(rid);
            }
        }
    }

    let now_ms = get_current_timestamp();
    let children_ids: Vec<u64> = group.nodes.iter().map(|n| n.id_hash).collect();
    let total_turn_count: u32 = group.nodes.iter().map(|n| n.turn_count).sum();
    let first_created = group
        .nodes
        .iter()
        .map(|n| n.created_at)
        .min()
        .unwrap_or(now_ms);

    // Generate a deterministic-but-unique id for the parent (group_idx ensures uniqueness across groups)
    let parent_id_hash = crate::util::hash_id(&format!(
        "merged_parent_{}_{}_{}",
        group.scene_id, now_ms, group_idx
    ));

    let mut parent_node = ContextSlot {
        id_hash: parent_id_hash,
        scene_id: group.scene_id,
        parent_id: None,
        children_ids,
        depth: 1,
        title: new_title,
        summary: Some(new_summary),
        archive_refs,
        l3_refs,
        turn_count: total_turn_count,
        created_at: first_created,
        updated_at: now_ms,
        version: 3,
        importance: 0.5,
        activation_score: 0.0,
        is_active: false,
        activation_state: ActivationState::Dormant,
        centroid_page_ref,
        dialogue_range: (first_created, now_ms),
        llm_params: LlmParams::default(),
    };

    // Compute LLM parameters based on merged context content features
    let merged_summary = parent_node
        .summary
        .as_deref()
        .unwrap_or("");
    parent_node.llm_params = compute_llm_params(&parent_node, merged_summary);

    Ok(parent_node)
}

/// Sink a node's depth by 1 and update its parent.
///
/// If the new depth ≥ 4 the node and its entire subtree are deleted.
/// Otherwise the node is saved and its own children are sunk recursively.
#[allow(clippy::too_many_arguments)]
fn sink_subtree(
    id_hash: u64,
    new_parent_id: u64,
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    file: &mut File,
    result: &mut MergeCompressResult,
) -> Result<(), MemHopError> {
    let page_ref = match btree.search(id_hash) {
        Some(pr) => pr,
        None => return Ok(()),
    };
    let page_id = crate::shared::slot_io::decode_page_id(page_ref);
    let slot_data = match crate::shared::slot_io::get_slot_data(&mmap[..], page_ref) {
        Some(d) => d,
        None => return Ok(()),
    };
    let mut ctx = match ContextSlot::deserialize_slot(slot_data) {
        Ok(c) => c,
        Err(_) => return Ok(()),
    };

    ctx.depth += 1;
    let new_depth = ctx.depth;
    ctx.parent_id = Some(new_parent_id);
    ctx.updated_at = get_current_timestamp();

    if new_depth >= 4 {
        // Delete this node and all descendants
        return free_node_and_descendants(
            id_hash,
            mmap,
            header,
            btree,
            sparse_index,
            l2_meta,
            file,
            result,
        );
    }

    // Save updated node
    let data = ctx
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    write_page_data(mmap, page_id, &data)?;

    // Update in-memory L2 metadata index
    l2_meta.update_from_context(&ctx);

    result.nodes_sunk += 1;

    // Recursively sink children
    let child_ids = ctx.children_ids.clone();
    for &child_id in &child_ids {
        sink_subtree(
            child_id,
            id_hash,
            mmap,
            header,
            btree,
            sparse_index,
            l2_meta,
            file,
            result,
        )?;
    }

    // After sinking children, some may have been deleted (depth >= 4).
    // Clean up children_ids to remove references to deleted children.
    let original_len = ctx.children_ids.len();
    ctx.children_ids.retain(|&cid| btree.search(cid).is_some());
    if ctx.children_ids.len() < original_len {
        // Re-serialize and write updated node with cleaned children_ids
        let data = ctx
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        write_page_data(mmap, page_id, &data)?;
        l2_meta.update_from_context(&ctx);
    }

    Ok(())
}

/// Recursively delete a node and all its descendants.
///
/// Removes the node from the B-tree, sparse index, L2 meta index, frees the
/// page (and any associated centroid vector page), and updates the result
/// counter.
#[allow(clippy::too_many_arguments)]
fn free_node_and_descendants(
    id_hash: u64,
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    l2_meta: &mut L2MetaIndex,
    file: &mut File,
    result: &mut MergeCompressResult,
) -> Result<(), MemHopError> {
    let page_ref = match btree.search(id_hash) {
        Some(pr) => pr,
        None => return Ok(()),
    };
    let page_id = crate::shared::slot_io::decode_page_id(page_ref);

    // Load node to traverse children and free the centroid vector page
    let ctx = {
        let slot_data = match crate::shared::slot_io::get_slot_data(&mmap[..], page_ref) {
            Some(d) => d,
            None => return Ok(()),
        };
        match ContextSlot::deserialize_slot(slot_data) {
            Ok(c) => c,
            Err(_) => return Ok(()),
        }
    };

    // Recursively free children first (post-order traversal)
    let child_ids = ctx.children_ids.clone();
    for &child_id in &child_ids {
        free_node_and_descendants(
            child_id,
            mmap,
            header,
            btree,
            sparse_index,
            l2_meta,
            file,
            result,
        )?;
    }

    // Free centroid vector page if present
    if ctx.centroid_page_ref != 0 {
        let v_page_id = crate::shared::slot_io::decode_page_id(ctx.centroid_page_ref);
        if v_page_id > 0 {
            let v_offset = (v_page_id as usize) * PAGE_SIZE;
            if v_offset + PAGE_SIZE <= mmap.len() {
                mmap[v_offset..v_offset + PAGE_SIZE].fill(0);
                let _ = free_page(mmap, header, v_page_id);
            }
        }
    }

    // Remove from indices
    btree.remove(id_hash);
    sparse_index.remove_document(id_hash);
    l2_meta.remove(id_hash);

    // Zero and free the page
    let page_offset = crate::shared::slot_io::page_offset(page_id);
    let page_end = page_offset + PAGE_SIZE;
    if page_end <= mmap.len() {
        mmap[page_offset..page_end].fill(0);
    }
    free_page(mmap, header, page_id)?;

    result.nodes_removed += 1;

    let _ = file; // keep signature symmetric with other helpers
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::LlmConfig;
    use crate::dream::llm::{
        CompressedSummary, CrystalDef, HabitAnalysis, LlmDistillResult, MemorySummary, Pattern,
    };
    use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
    use crate::test_helpers::create_test_mmap_with_tempfile;
    use std::io::Write;

    // ========================================================================
    // MockLLM — deterministic 
    // ========================================================================

    struct MockLLM {
        /// When true, `check_same_topic` returns true when BOTH summaries
        /// contain the keyword below.
        use_topic_keyword: bool,
        topic_keyword: String,
    }

    impl MockLLM {
        fn new() -> Self {
            Self {
                use_topic_keyword: true,
                topic_keyword: "sametopic".to_string(),
            }
        }
    }

    impl LlmProvider for MockLLM {
        fn summarize(&self, texts: &[String]) -> Result<CompressedSummary, MemHopError> {
            Ok(CompressedSummary {
                theme: "mock".into(),
                title: "Mock Title".into(),
                key_points: vec![],
                summary: texts.join(" "),
            })
        }

        fn extract_patterns(&self, _: &[MemorySummary]) -> Result<Vec<Pattern>, MemHopError> {
            Ok(vec![])
        }

        fn generate_crystal(&self, _: &Pattern) -> Result<CrystalDef, MemHopError> {
            Ok(CrystalDef {
                condition: "mock".into(),
                action: "mock".into(),
                steps: vec![],
                confidence: 1.0,
            })
        }

        fn fallback_summarize(&self, texts: &[String]) -> CompressedSummary {
            CompressedSummary {
                theme: "mock".into(),
                title: "Mock Title".into(),
                key_points: vec![],
                summary: texts.join(" "),
            }
        }

        fn fallback_extract_patterns(&self, _: &[MemorySummary]) -> Vec<Pattern> {
            vec![]
        }

        fn fallback_generate_crystal(&self, _: &Pattern) -> CrystalDef {
            CrystalDef {
                condition: "mock".into(),
                action: "mock".into(),
                steps: vec![],
                confidence: 1.0,
            }
        }

        fn analyze_user_habits(
            &self,
            _: &[String],
        ) -> Result<HabitAnalysis, MemHopError> {
            Ok(HabitAnalysis::default())
        }

        fn fallback_analyze_user_habits(&self, _: &[String]) -> HabitAnalysis {
            HabitAnalysis::default()
        }

        fn distill_concepts(&self, _: &str) -> Result<LlmDistillResult, MemHopError> {
            Ok(LlmDistillResult {
                concepts: vec![],
                relations: vec![],
            })
        }

        fn fallback_distill_concepts(&self, _: &str) -> LlmDistillResult {
            LlmDistillResult {
                concepts: vec![],
                relations: vec![],
            }
        }

        fn check_same_topic(
            &self,
            summary_a: &str,
            summary_b: &str,
        ) -> Result<bool, MemHopError> {
            if self.use_topic_keyword {
                Ok(summary_a.contains(&self.topic_keyword)
                    && summary_b.contains(&self.topic_keyword))
            } else {
                Ok(false)
            }
        }

        fn merge_summarize(
            &self,
            _texts: &[String],
        ) -> Result<(String, String), MemHopError> {
            Ok((
                "Merged Conversation Topic".to_string(),
                "This is a merged summary of multiple related conversations.".to_string(),
            ))
        }

        fn compress_for_retrieval(
            &self,
            text: &str,
            _role: &str,
        ) -> Result<String, MemHopError> {
            Ok(text.to_string())
        }
    }

    // ========================================================================
    // Helper: create a temporary file-based env for direct sink/free tests
    // ========================================================================

    /// Create mmap + header + btree + file from a NamedTempFile.
    fn create_sink_test_env(
        pages: usize,
    ) -> (
        tempfile::NamedTempFile,
        memmap2::MmapMut,
        FileHeader,
        BTreeIndex,
        std::fs::File,
    ) {
        let (tf, mut mmap, mut header, file) = create_test_mmap_with_tempfile(pages);
        let path = tf.path();
        let btree = BTreeIndex::new();

        // Also open a second file handle usable by allocate_page
        let f2 = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        // Properly init free list (replace test_helpers manual setup)
        crate::file::free_list::init_free_list(&mut header).unwrap();
        for page_id in (2..pages as u32).rev() {
            crate::file::free_list::free_page(&mut mmap, &mut header, page_id).unwrap();
        }

        (tf, mmap, header, btree, f2)
    }

    /// Pack a ContextSlot into mmap + btree, return page_id.
    fn pack_context(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        ctx: &ContextSlot,
        file: &mut std::fs::File,
    ) -> u32 {
        let page_id = allocate_page(mmap, header, PageType::Context, 2, 0, file).unwrap();
        let data = ctx.serialize().unwrap();
        write_page_data(mmap, page_id, &data).unwrap();
        let page_ref = encode_page_ref(page_id, 0);
        btree.insert(ctx.id_hash, page_ref);
        page_id
    }

    fn default_ctx(id_hash: u64, title: &str, depth: u8) -> ContextSlot {
        ContextSlot {
            id_hash,
            scene_id: 1,
            parent_id: None,
            children_ids: vec![],
            depth,
            title: title.to_string(),
            summary: Some(format!("summary of {}", title)),
            archive_refs: vec![],
            l3_refs: vec![],
            turn_count: 1,
            created_at: id_hash as i64, // use id_hash as timestamp for deterministic ordering
            updated_at: id_hash as i64,
            version: 3,
            importance: 0.5,
            activation_score: 0.0,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_page_ref: 0,
            dialogue_range: (id_hash as i64, id_hash as i64),
            llm_params: LlmParams::default(),
        }
    }

    // ========================================================================
    // AC-2: 相邻对话 LLM 话题检测 + 合并压缩
    // ========================================================================

    #[test]
    fn test_ac2_topic_detection_and_merge() {
        // 5 depth-1 nodes (t1~t5), t2/t3/t4 are same topic (contain "sametopic")
        let (_tf, mut mmap, mut header, mut btree, mut file) = create_sink_test_env(64);
        let mut sparse_index = SparseIndex::new();
        let mut l2_meta = L2MetaIndex::new();

        // t1: no sametopic keyword
        let mut t1 = default_ctx(101, "introduction", 1);
        t1.summary = Some("General introduction".to_string());
        pack_context(&mut mmap, &mut header, &mut btree, &t1, &mut file);

        // t2: contains sametopic
        let mut t2 = default_ctx(102, "core topic part A", 1);
        t2.summary = Some("This is sametopic discussion part A".to_string());
        t2.created_at = 102;
        t2.updated_at = 102;
        pack_context(&mut mmap, &mut header, &mut btree, &t2, &mut file);

        // t3: contains sametopic
        let mut t3 = default_ctx(103, "core topic part B", 1);
        t3.summary = Some("More sametopic content part B".to_string());
        t3.created_at = 103;
        t3.updated_at = 103;
        pack_context(&mut mmap, &mut header, &mut btree, &t3, &mut file);

        // t4: contains sametopic
        let mut t4 = default_ctx(104, "core topic part C", 1);
        t4.summary = Some("Additional sametopic details part C".to_string());
        t4.created_at = 104;
        t4.updated_at = 104;
        pack_context(&mut mmap, &mut header, &mut btree, &t4, &mut file);

        // t5: no sametopic keyword
        let mut t5 = default_ctx(105, "conclusion", 1);
        t5.summary = Some("Final conclusion remarks".to_string());
        t5.created_at = 105;
        t5.updated_at = 105;
        pack_context(&mut mmap, &mut header, &mut btree, &t5, &mut file);

        // Build l2_meta
        l2_meta = L2MetaIndex::build(&mmap, &btree);
        let scene_id: u64 = 1;
        let mut active_scene_ids = HashSet::new();
        active_scene_ids.insert(scene_id);

        let llm = MockLLM::new();

        let result = l2_merge_compress(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            &mut l2_meta,
            &llm,
            &active_scene_ids,
            &mut file,
            None,
        )
        .unwrap();

        // Should detect 1 group, merge 3 nodes, create 1 parent
        assert_eq!(result.groups_detected, 1, "should detect 1 group");
        assert_eq!(result.nodes_merged, 3, "should merge 3 nodes");
        assert_eq!(result.parent_nodes_created, 1, "should create 1 parent");
        assert_eq!(result.nodes_sunk, 3, "should sink 3 nodes");
        assert_eq!(result.nodes_removed, 0, "no nodes should be removed");

        // Rebuild l2_meta to get fresh view
        l2_meta = L2MetaIndex::build(&mmap, &btree);

        // Find the parent node (depth=1, children_ids = [102, 103, 104])
        let parent = l2_meta
            .get_by_scene_depth(scene_id, 1)
            .and_then(|ids| {
                ids.iter().find_map(|&id| {
                    let m = l2_meta.get(id)?;
                    if m.children_ids.len() == 3 {
                        Some(m)
                    } else {
                        None
                    }
                })
            })
            .expect("parent node should exist with 3 children");

        // Parent's children should be 102, 103, 104
        assert_eq!(parent.children_ids.len(), 3);
        assert!(parent.children_ids.contains(&102));
        assert!(parent.children_ids.contains(&103));
        assert!(parent.children_ids.contains(&104));

        // t2, t3, t4 should now be depth=2 with parent_id pointing to parent
        for &cid in &[102u64, 103, 104] {
            let meta = l2_meta.get(cid).expect("child should exist in l2_meta");
            assert_eq!(
                meta.depth, 2,
                "child {} should be depth=2 after sinking",
                cid
            );
        }

        // t1 and t5 should still be depth=1 without parent
        for &cid in &[101u64, 105] {
            let meta = l2_meta.get(cid).expect("node should still exist");
            assert_eq!(meta.depth, 1, "{} should stay depth=1", cid);
        }
    }

    // ========================================================================
    // AC-3: 子树被动下沉
    // ========================================================================

    #[test]
    fn test_ac3_subtree_passive_sink() {
        // Setup: turn9(depth=1) has children turn4/5/6(depth=2)
        let (_tf, mut mmap, mut header, mut btree, mut file) = create_sink_test_env(64);
        let mut sparse_index = SparseIndex::new();
        let mut l2_meta = L2MetaIndex::new();

        // Create depth-2 children first (so they exist before parent references them)
        let mut t4 = default_ctx(104, "child turn 4", 2);
        t4.parent_id = Some(109);
        let mut t5 = default_ctx(105, "child turn 5", 2);
        t5.parent_id = Some(109);
        let mut t6 = default_ctx(106, "child turn 6", 2);
        t6.parent_id = Some(109);

        // Create depth-1 parent with children
        let mut turn9 = default_ctx(109, "parent turn 9", 1);
        turn9.children_ids = vec![104, 105, 106];

        pack_context(&mut mmap, &mut header, &mut btree, &t4, &mut file);
        pack_context(&mut mmap, &mut header, &mut btree, &t5, &mut file);
        pack_context(&mut mmap, &mut header, &mut btree, &t6, &mut file);
        pack_context(&mut mmap, &mut header, &mut btree, &turn9, &mut file);

        // Build l2_meta
        l2_meta = L2MetaIndex::build(&mmap, &btree);

        let new_parent_id = 999;
        let mut result = MergeCompressResult {
            groups_detected: 0,
            nodes_merged: 0,
            parent_nodes_created: 0,
            nodes_sunk: 0,
            nodes_removed: 0,
        };

        // Sink turn9 under new_parent
        sink_subtree(
            109,
            new_parent_id,
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            &mut l2_meta,
            &mut file,
            &mut result,
        )
        .unwrap();

        // turn9 should be depth=2, parent_id=999
        {
            let slot_data =
                crate::shared::slot_io::get_slot_data(&mmap[..], btree.search(109).unwrap()).unwrap();
            let ctx = ContextSlot::deserialize_slot(slot_data).unwrap();
            assert_eq!(
                ctx.depth, 2,
                "turn9 should be depth=2 after sinking"
            );
            assert_eq!(
                ctx.parent_id,
                Some(999),
                "turn9 parent_id should be 999"
            );
        }

        // turn4/5/6 should be depth=3, parent_id=109
        for &cid in &[104u64, 105, 106] {
            let slot_data =
                crate::shared::slot_io::get_slot_data(&mmap[..], btree.search(cid).unwrap())
                    .unwrap();
            let ctx = ContextSlot::deserialize_slot(slot_data).unwrap();
            assert_eq!(
                ctx.depth, 3,
                "child {} should be depth=3 after passive sink",
                cid
            );
            assert_eq!(
                ctx.parent_id,
                Some(109),
                "child {} parent_id should still be 109",
                cid
            );
        }

        assert_eq!(result.nodes_sunk, 4, "4 nodes should be sunk");
        assert_eq!(result.nodes_removed, 0, "no nodes should be removed");
    }

    // ========================================================================
    // AC-4: depth>=4 自动删除
    // ========================================================================

    #[test]
    fn test_ac4_depth4_auto_remove() {
        // Setup: turn9(depth=2) has children turn4/5/6(depth=3)
        let (_tf, mut mmap, mut header, mut btree, mut file) = create_sink_test_env(64);
        let mut sparse_index = SparseIndex::new();
        let mut l2_meta = L2MetaIndex::new();

        let mut t4 = default_ctx(104, "deep child 4", 3);
        t4.parent_id = Some(109);
        let mut t5 = default_ctx(105, "deep child 5", 3);
        t5.parent_id = Some(109);
        let mut t6 = default_ctx(106, "deep child 6", 3);
        t6.parent_id = Some(109);

        let mut turn9 = default_ctx(109, "parent turn 9 at depth2", 2);
        turn9.children_ids = vec![104, 105, 106];

        pack_context(&mut mmap, &mut header, &mut btree, &t4, &mut file);
        pack_context(&mut mmap, &mut header, &mut btree, &t5, &mut file);
        pack_context(&mut mmap, &mut header, &mut btree, &t6, &mut file);
        pack_context(&mut mmap, &mut header, &mut btree, &turn9, &mut file);

        l2_meta = L2MetaIndex::build(&mmap, &btree);

        let new_parent_id = 999;
        let mut result = MergeCompressResult {
            groups_detected: 0,
            nodes_merged: 0,
            parent_nodes_created: 0,
            nodes_sunk: 0,
            nodes_removed: 0,
        };

        // Sink turn9 (depth=2 → 3), children (depth=3 → 4) should be deleted
        sink_subtree(
            109,
            new_parent_id,
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            &mut l2_meta,
            &mut file,
            &mut result,
        )
        .unwrap();

        // turn9 still exists at depth=3
        {
            assert!(
                btree.search(109).is_some(),
                "turn9 should still exist in btree"
            );
            let slot_data =
                crate::shared::slot_io::get_slot_data(&mmap[..], btree.search(109).unwrap()).unwrap();
            let ctx = ContextSlot::deserialize_slot(slot_data).unwrap();
            assert_eq!(
                ctx.depth, 3,
                "turn9 should be depth=3 after sinking"
            );
        }

        // turn4/5/6 should be removed (depth=3 → 4 triggers deletion)
        for &cid in &[104u64, 105, 106] {
            assert!(
                btree.search(cid).is_none(),
                "child {} should be removed from btree (depth>=4)",
                cid
            );
            let meta = l2_meta.get(cid);
            assert!(
                meta.is_none(),
                "child {} should be removed from l2_meta",
                cid
            );
        }

        // Should sink 1 (turn9 itself), remove 3 (children)
        assert_eq!(result.nodes_sunk, 1, "only turn9 should be sunk");
        assert_eq!(result.nodes_removed, 3, "3 children should be removed");
    }

    #[test]
    fn test_l2_merge_compress_empty_scenes() {
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; 4096 * 50]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);
        let mut btree = BTreeIndex::new();
        let mut sparse_index = SparseIndex::new();
        let mut l2_meta = L2MetaIndex::new();

        let llm = OpenAICompatibleLlmProvider::new(LlmConfig {
            api_url: "https://api.example.com/v1/chat/completions".to_string(),
            api_key: "test-key".to_string(),
            model: "test-model".to_string(),
            ..Default::default()
        });
        let empty_scenes = HashSet::new();

        let mut file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();
        let result = l2_merge_compress(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            &mut l2_meta,
            &llm,
            &empty_scenes,
            &mut file,
            None,
        );

        assert!(result.is_ok());
        let report = result.unwrap();
        assert_eq!(report.groups_detected, 0);
        assert_eq!(report.nodes_merged, 0);
        assert_eq!(report.parent_nodes_created, 0);
        assert_eq!(report.nodes_sunk, 0);
        assert_eq!(report.nodes_removed, 0);
    }

    #[test]
    fn test_compute_llm_params_technical() {
        let ctx = ContextSlot {
            id_hash: 1,
            parent_id: None,
            children_ids: vec![],
            scene_id: 0,
            depth: 1,
            title: "Rust function implementation".to_string(),
            summary: Some("fn main() { let x = 42; }".to_string()),
            archive_refs: vec![],
            l3_refs: vec![],
            turn_count: 5,
            created_at: 0,
            updated_at: 0,
            version: 2,
            importance: 0.5,
            activation_score: 0.0,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
            llm_params: LlmParams::default(),
        };

        let params = compute_llm_params(&ctx, "fn main() { let x = 42; }");
        assert!(
            params.temperature >= 0.1 && params.temperature <= 0.3,
            "technical context should have low temperature, got {}",
            params.temperature
        );
    }

    #[test]
    fn test_compute_llm_params_emotional() {
        let ctx = ContextSlot {
            id_hash: 2,
            parent_id: None,
            children_ids: vec![],
            scene_id: 0,
            depth: 1,
            title: "I am so happy today!".to_string(),
            summary: Some("Feeling joyful and excited about the project!".to_string()),
            archive_refs: vec![],
            l3_refs: vec![],
            turn_count: 3,
            created_at: 0,
            updated_at: 0,
            version: 2,
            importance: 0.5,
            activation_score: 0.0,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
            llm_params: LlmParams::default(),
        };

        let params = compute_llm_params(&ctx, "Feeling joyful and excited about the project!");
        assert!(
            params.temperature >= 0.7 && params.temperature <= 0.9,
            "emotional context should have high temperature, got {}",
            params.temperature
        );
        assert!(
            params.top_p > 0.9,
            "emotional context should have relaxed top_p, got {}",
            params.top_p
        );
    }

    #[test]
    fn test_compute_llm_params_knowledge_dense() {
        let ctx = ContextSlot {
            id_hash: 3,
            parent_id: None,
            children_ids: vec![],
            scene_id: 0,
            depth: 1,
            title: "Knowledge base".to_string(),
            summary: Some("Comprehensive overview of multiple topics".to_string()),
            archive_refs: vec![1, 2, 3, 4, 5],
            l3_refs: vec![10, 20, 30],
            turn_count: 20,
            created_at: 0,
            updated_at: 0,
            version: 2,
            importance: 0.5,
            activation_score: 0.0,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
            llm_params: LlmParams::default(),
        };

        let params = compute_llm_params(&ctx, "Comprehensive overview of multiple topics");
        assert!(
            params.presence_penalty > 0.3,
            "knowledge-dense context should have elevated presence_penalty, got {}",
            params.presence_penalty
        );
    }
}
