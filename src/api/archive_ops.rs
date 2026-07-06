// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-8: L4 Archive search operations.

use crate::query::types::{Archive, L4SearchQuery};
use crate::{MemHop, Result};

impl MemHop {
    /// Search L4 archives with recent/time-range/keyword filters.
    pub fn search_l4(&self, query: L4SearchQuery) -> Result<Vec<Archive>> {
        let slots = crate::query::l4_ops::search_l4(&self.mmap, &self.header, &self.btree, query)?;
        Ok(slots
            .into_iter()
            .map(|arc| {
                let src = arc.request_source();
                Archive {
                    id: crate::shared::common::format_hash(arc.id_hash),
                    content: arc.content,
                    content_type: arc.content_type.as_str().to_string(),
                    source_ref: None,
                    topic_id: Some(crate::shared::common::format_hash(arc.context_id)),
                    engram_ids: vec![],
                    created_at: arc.created_at,
                    source_agent: src.source_agent,
                    source_platform: src.source_platform,
                }
            })
            .collect())
    }
}
