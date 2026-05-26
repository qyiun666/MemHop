//! Search filter types — minimal placeholder for v0.7.3.
//!
//! The old filter logic using BlobRecord/MetaRecord has been removed.
//! This module provides a minimal FilterCriteria struct for compilation.

#![allow(dead_code)]

use std::collections::HashMap;

use crate::error::MemHopError;

// ── Search filter types ────────────────────────────────────

/// Minimal filter criteria for search operations.
pub(crate) struct FilterCriteria {
    pub(crate) is_archived: Option<bool>,
}

pub(crate) fn parse_filters(
    filters: &HashMap<String, serde_json::Value>,
) -> Result<FilterCriteria, MemHopError> {
    let mut c = FilterCriteria { is_archived: None };

    for (key, val) in filters {
        match key.as_str() {
            "is_archived" => c.is_archived = val.as_bool(),
            _ => {
                return Err(MemHopError::InvalidArgument(format!(
                    "Unknown filter key: '{}'",
                    key
                )));
            }
        }
    }
    Ok(c)
}
