// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L4 Archive search internal implementation.

use crate::layers::archive::ArchiveSlot;
use crate::query::types::L4SearchQuery;
use crate::storage::record::REC_L4_ARCHIVE;
use crate::storage::StorageEngine;
use crate::MemHopError;

/// Search L4 archives with recent/time-range/keyword filters.
pub fn search_l4(
    engine: &StorageEngine,
    query: L4SearchQuery,
) -> Result<Vec<ArchiveSlot>, MemHopError> {
    let mut results: Vec<ArchiveSlot> = Vec::new();

    for (id_hash, _) in engine.iter_index() {
        let Some((rt, data)) = engine.read_record(*id_hash)? else {
            continue;
        };
        if rt != REC_L4_ARCHIVE {
            continue;
        }
        let archive = match bincode::deserialize::<ArchiveSlot>(data) {
            Ok(a) => a,
            Err(_) => continue,
        };

        if let Some((start, end)) = query.time_range {
            if archive.created_at < start || archive.created_at > end {
                continue;
            }
        }

        if let Some(ref keywords) = query.keywords {
            let combined = format!("{} {:?}", archive.content, archive.metadata);
            if !keywords
                .iter()
                .any(|kw| crate::shared::common::matches_keyword(&combined, kw))
            {
                continue;
            }
        }

        results.push(archive);
    }

    results.sort_by_key(|a| std::cmp::Reverse(a.created_at));

    if let Some(recent) = query.recent {
        results.truncate(recent);
    }

    Ok(results)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::layers::archive::{ArchiveSlot, ContentType};
    use tempfile::NamedTempFile;

    fn insert_archive(engine: &mut StorageEngine, id: u64, content: &str, created_at: i64) {
        let archive = ArchiveSlot {
            id_hash: id,
            content_type: ContentType::Text,
            role: 0,
            context_id: 1,
            created_at,
            content: content.into(),
            metadata: None,
        };
        let data = bincode::serialize(&archive).unwrap();
        engine.write_record(REC_L4_ARCHIVE, id, &data).unwrap();
    }

    #[test]
    fn test_search_l4_filters() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();

        insert_archive(&mut engine, 100, "hello world", 1000);
        insert_archive(&mut engine, 101, "rust code", 2000);
        insert_archive(&mut engine, 102, "world news", 3000);

        let recent = search_l4(
            &engine,
            L4SearchQuery {
                recent: Some(2),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(recent.len(), 2);
        assert_eq!(recent[0].created_at, 3000);

        let range = search_l4(
            &engine,
            L4SearchQuery {
                time_range: Some((1500, 2500)),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(range.len(), 1);
        assert_eq!(range[0].id_hash, 101);

        let keyword = search_l4(
            &engine,
            L4SearchQuery {
                keywords: Some(vec!["world".into()]),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(keyword.len(), 2);

        // temp file kept alive for engine lifetime
        let _ = temp;
    }
}
