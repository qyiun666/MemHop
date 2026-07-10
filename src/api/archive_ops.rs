// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-8: L4 Archive search operations.

use crate::query::types::{Archive, ArchiveQuery};
use crate::{MemHop, Result};

impl MemHop {
    /// Unified L4 archive retrieval.
    ///
    /// When `topic_id` is provided, lists archives for that specific topic.
    /// Otherwise searches by keyword, time range, or lists all archives.
    pub fn query_archives(&self, query: ArchiveQuery) -> Result<Vec<Archive>> {
        if let Some(topic_id) = &query.topic_id {
            use crate::query::list::list_archives_by_topic;
            use crate::query::types::ArchivePageQuery;
            let page_query = ArchivePageQuery {
                page: query.page,
                page_size: query.page_size,
                start_time: query.time_range.map(|(s, _)| s),
                end_time: query.time_range.map(|(_, e)| e),
                content_type: None,
            };
            let result = list_archives_by_topic(&self.engine, topic_id, page_query)?;
            Ok(result.items)
        } else {
            use crate::query::l4_ops::search_l4;
            use crate::query::types::L4SearchQuery;
            let l4_query = L4SearchQuery {
                recent: None,
                time_range: query.time_range,
                keywords: query.keyword.map(|k| vec![k]),
            };
            let slots = search_l4(&self.engine, l4_query)?;
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
}
