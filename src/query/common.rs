//! Common utility functions for query module
//!
//! Provides shared utilities to eliminate code duplication across query implementations.

use crate::MemHopError;

/// Get current timestamp in milliseconds
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

/// Format hash as hex string
#[inline]
pub fn format_hash(hash: u64) -> String {
    format!("{:016x}", hash)
}

/// Helper macro for deserializing slot types that implement their own `deserialize` method
///
/// This macro generates a wrapper function that calls the type's `deserialize` method
/// and converts the error to MemHopError::Serialization.
macro_rules! impl_deserialize_slot {
    ($type:ty, $name:expr) => {
        impl $type {
            /// Deserialize with error handling
            pub fn deserialize_slot(data: &[u8]) -> Result<Self, MemHopError> {
                <$type>::deserialize(data)
                    .map_err(|e| MemHopError::Serialization(format!("{} deserialize: {}", $name, e)))
            }
        }
    };
}

// Implement for all slot types
impl_deserialize_slot!(crate::slot::context::ContextSlot, "ContextSlot");
impl_deserialize_slot!(crate::slot::context_node::ContextNode, "ContextNode");
impl_deserialize_slot!(crate::slot::archive::ArchiveSlot, "ArchiveSlot");
impl_deserialize_slot!(crate::slot::hypergraph::HypergraphSlot, "HypergraphSlot");
impl_deserialize_slot!(crate::slot::profile::ProfileSlot, "ProfileSlot");
impl_deserialize_slot!(crate::slot::action_chain::ActionChainSlot, "ActionChainSlot");

/// Calculate pagination parameters
#[inline]
pub fn pagination_params(page: usize, page_size: usize) -> (usize, usize) {
    let skip = page.saturating_sub(1) * page_size;
    let take = page_size;
    (skip, take)
}

/// Calculate has_more flag
#[inline]
pub fn has_more(skip: usize, take: usize, total: usize) -> bool {
    skip + take < total
}

/// Apply keyword filter (case-insensitive)
#[inline]
pub fn matches_keyword(text: &str, keyword: &str) -> bool {
    let keyword_lower = keyword.to_lowercase();
    text.to_lowercase().contains(&keyword_lower)
}

/// Sort items by score (descending)
#[inline]
pub fn sort_by_score<T, F>(items: &mut [T], score_fn: F)
where
    F: Fn(&T) -> f32,
{
    items.sort_by(|a, b| {
        score_fn(b)
            .partial_cmp(&score_fn(a))
            .unwrap_or(std::cmp::Ordering::Equal)
    })
}
