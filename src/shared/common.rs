// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Shared utilities for query module.

#[inline]
pub fn now_ms() -> i64 {
    crate::util::get_current_timestamp()
}

/// Parse ID string to u64 hash
/// Supports both hex-encoded hashes (16 chars) and raw strings
#[inline]
pub fn parse_id_to_hash(id: &str) -> u64 {
    if id.len() == 16 {
        // Likely a hex-encoded hash (e.g., "1a2b3c4d5e6f7890")
        u64::from_str_radix(id, 16).unwrap_or_else(|_| crate::util::hash_id(id))
    } else {
        crate::util::hash_id(id)
    }
}

#[inline]
pub fn format_hash(hash: u64) -> String {
    format!("{:016x}", hash)
}

/// Extract L2 topic sparse index terms and document length from title and summary.
/// Returns (terms, doc_len) where terms are tokenized (CJK-aware) lowercase tokens.
pub fn build_l2_sparse_terms(title: &str, summary: &Option<String>) -> (Vec<String>, u32) {
    let mut terms: Vec<String> = crate::index::sparse::tokenize(title);
    if let Some(ref s) = summary {
        terms.extend(crate::index::sparse::tokenize(s));
    }
    let doc_len = (title.len() + summary.as_ref().map_or(0, |s| s.len())) as u32;
    (terms, doc_len)
}

#[inline]
pub fn pagination_params(page: usize, page_size: usize) -> (usize, usize) {
    let skip = page.saturating_sub(1) * page_size;
    let take = page_size;
    (skip, take)
}

#[inline]
pub fn has_more(skip: usize, take: usize, total: usize) -> bool {
    skip + take < total
}

#[inline]
pub fn matches_keyword(text: &str, keyword: &str) -> bool {
    let keyword_lower = keyword.to_lowercase();
    text.to_lowercase().contains(&keyword_lower)
}
