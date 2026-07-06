// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Stage: L2 Depth-based Compression — depth demotion on active contexts.

use crate::dream::llm::LlmProvider;
use crate::dream::prune::{CompressResult, DemotionResult};

/// Result type for compress_active_contexts function
pub type CompressStageResult = Result<
    (
        Vec<DemotionResult>,
        Vec<CompressResult>,
        Vec<String>, // removed context IDs (depth 4 → gone)
        Vec<String>, // demoted to tertiary IDs (depth 2 → 3)
    ),
    MemHopError,
>;
use crate::file::free_list::free_page;
use crate::file::header::FileHeader;
use crate::file::page::{allocate_page, read_page_header, write_page_data};
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::layers::context::{ActivationState, ContextSlot, LlmParams};
use crate::util::hash::hash_id;
use crate::util::{get_current_timestamp, PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;
use std::fs::File;

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

/// Compress activated L2 contexts through depth-based demotion
///
/// Depth 4→removed, 3→4, 2→3, 1→compressed+demoted to 2.
pub fn compress_active_contexts(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    llm: &dyn LlmProvider,
    active_topic_ids: &HashSet<u64>,
    file: &mut File,
) -> CompressStageResult {
    let now_ms = get_current_timestamp();
    let _page_count = header.page_count;

    let mut active_contexts: Vec<(u32, ContextSlot)> = Vec::new();

    for &topic_id in active_topic_ids {
        if let Some(page_ref) = btree.search(topic_id) {
            let page_id = (page_ref >> 16) as u32;
            let offset = (page_id as usize) * PAGE_SIZE;
            if offset + PAGE_SIZE > mmap.len() {
                continue;
            }
            if let Ok(page_header) = read_page_header(&mmap[..], page_id) {
                if page_header.page_type != PageType::Context as u16 {
                    continue;
                }
                if let Ok(ctx) = ContextSlot::deserialize(&mmap[offset + 32..]) {
                    active_contexts.push((page_id, ctx));
                }
            }
        }
    }

    if active_contexts.is_empty() {
        return Ok((vec![], vec![], vec![], vec![]));
    }

    let mut demoted_to_secondary: Vec<DemotionResult> = Vec::new();
    let mut new_compressed: Vec<CompressResult> = Vec::new();
    let mut removed_contexts: Vec<String> = Vec::new();
    let mut demoted_to_tertiary: Vec<String> = Vec::new();

    // Process deepest first to avoid conflicts

    // 2a: Remove depth-4 contexts (semantic summaries)
    let depth4: Vec<_> = active_contexts
        .iter()
        .filter(|(_, ctx)| ctx.depth == 4)
        .collect();

    for &(page_id, ref ctx) in &depth4 {
        let ctx_id = format!("{:016x}", ctx.id_hash);

        let page_offset = (*page_id as usize) * PAGE_SIZE;
        mmap[page_offset..page_offset + PAGE_SIZE].fill(0);
        free_page(mmap, header, *page_id)?;

        btree.remove(ctx.id_hash);
        sparse_index.remove_document(ctx.id_hash);

        removed_contexts.push(ctx_id);
    }

    // 2b: Demote depth-3 contexts to depth 4
    for (page_id, ctx) in active_contexts.iter_mut().filter(|(_, ctx)| ctx.depth == 3) {
        ctx.depth = 4;
        ctx.updated_at = now_ms;

        let serialized = ctx
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        write_page_data(mmap, *page_id, &serialized)?;

        // Depth-3 → depth-4 demotions are not tracked separately
    }

    // 2c: Demote depth-2 contexts to depth 3
    for (page_id, ctx) in active_contexts.iter_mut().filter(|(_, ctx)| ctx.depth == 2) {
        let ctx_id = format!("{:016x}", ctx.id_hash);

        ctx.depth = 3;
        ctx.updated_at = now_ms;

        let serialized = ctx
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        write_page_data(mmap, *page_id, &serialized)?;

        demoted_to_tertiary.push(ctx_id);
    }

    // 2d: Compress and demote depth-1 contexts
    let depth1: Vec<(u32, ContextSlot)> = active_contexts
        .iter()
        .filter(|(_, ctx)| ctx.depth == 1)
        .map(|(pid, ctx)| (*pid, ctx.clone()))
        .collect();

    for (page_id, ctx) in &depth1 {
        let ctx_id = format!("{:016x}", ctx.id_hash);

        let texts_to_compress: Vec<String> = vec![
            format!("Title: {}", ctx.title),
            format!("Summary: {}", ctx.summary.as_deref().unwrap_or("(none)")),
            format!(
                "Turns: {}, Archives: {}",
                ctx.turn_count,
                ctx.archive_refs.len()
            ),
        ];

        let compressed_summary = match llm.summarize(&texts_to_compress) {
            Ok(s) => s,
            Err(_) => llm.fallback_summarize(&texts_to_compress),
        };

        let llm_params = compute_llm_params(ctx, &compressed_summary);

        let new_id_hash = hash_id(&format!("compressed_{}_{}", ctx_id, now_ms));
        let new_ctx = ContextSlot {
            id_hash: new_id_hash,
            parent_id: None,
            depth: 1,
            title: format!("[Compressed] {}", ctx.title),
            summary: Some(compressed_summary.clone()),
            archive_refs: ctx.archive_refs.clone(),
            l3_refs: ctx.l3_refs.clone(),
            turn_count: ctx.turn_count,
            created_at: now_ms,
            updated_at: now_ms,
            version: 2,
            importance: ctx.importance * 0.9,
            activation_score: 0.3,
            is_active: false, // Compressed contexts start inactive
            activation_state: ActivationState::Crystallized,
            centroid_page_ref: ctx.centroid_page_ref,
            dialogue_range: ctx.dialogue_range,
            llm_params,
        };

        let new_page_id = allocate_page(mmap, header, PageType::Context, 2, 0, file)?;
        let new_serialized = new_ctx
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        write_page_data(mmap, new_page_id, &new_serialized)?;

        let new_page_ref = crate::file::page::encode_page_ref(new_page_id, 0);
        btree.insert(new_id_hash, new_page_ref);

        let title_terms = crate::index::sparse::tokenize(&new_ctx.title);
        let doc_len = title_terms.len() as u32;
        sparse_index.add_document(new_id_hash, title_terms, doc_len);

        new_compressed.push(CompressResult {
            new_context_id: format!("{:016x}", new_id_hash),
            source_context_id: ctx_id.clone(),
            new_summary: compressed_summary.clone(),
        });

        let mut demoted_ctx = ctx.clone();
        demoted_ctx.depth = 2;
        demoted_ctx.parent_id = Some(new_id_hash);
        demoted_ctx.summary = Some(compressed_summary.clone());
        demoted_ctx.updated_at = now_ms;
        demoted_ctx.llm_params = llm_params;

        let demoted_serialized = demoted_ctx
            .serialize()
            .map_err(|e| MemHopError::Serialization(e.to_string()))?;
        write_page_data(mmap, *page_id, &demoted_serialized)?;

        demoted_to_secondary.push(DemotionResult {
            context_id: ctx_id,
            original_title: ctx.title.clone(),
            compressed_summary,
            new_depth: 2,
        });
    }

    Ok((
        demoted_to_secondary,
        new_compressed,
        removed_contexts,
        demoted_to_tertiary,
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::LlmConfig;
    use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
    use std::io::Write;

    #[test]
    fn test_compress_empty_active_topics() {
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

        let llm = OpenAICompatibleLlmProvider::new(LlmConfig {
            api_url: "https://api.example.com/v1/chat/completions".to_string(),
            api_key: "test-key".to_string(),
            model: "test-model".to_string(),
            ..Default::default()
        });
        let empty_topics = HashSet::new();

        let mut file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();
        let result = compress_active_contexts(
            &mut mmap,
            &mut header,
            &mut btree,
            &mut sparse_index,
            &llm,
            &empty_topics,
            &mut file,
        );

        assert!(result.is_ok());
        let (demoted, compressed, removed, tertiary) = result.unwrap();
        assert!(demoted.is_empty());
        assert!(compressed.is_empty());
        assert!(removed.is_empty());
        assert!(tertiary.is_empty());
    }

    #[test]
    fn test_compute_llm_params_technical() {
        let ctx = ContextSlot {
            id_hash: 1,
            parent_id: None,
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
