// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L4 Archive search internal implementation.

use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::layers::archive::ArchiveSlot;
use crate::query::types::L4SearchQuery;
use crate::shared::slot_io::{decode_page_id, get_slot_data};
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;

/// Search L4 archives with recent/time-range/keyword filters.
pub fn search_l4(
    mmap: &MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    query: L4SearchQuery,
) -> Result<Vec<ArchiveSlot>, MemHopError> {
    let data: &[u8] = &mmap[..];
    let mut results: Vec<ArchiveSlot> = Vec::new();

    for (_, page_ref) in btree.iter_unsorted() {
        let page_id = decode_page_id(*page_ref);
        if page_id >= header.page_count {
            continue;
        }
        if page_type(data, page_id) != Some(PageType::Archive as u16) {
            continue;
        }
        let slot_data = match get_slot_data(data, *page_ref) {
            Some(d) => d,
            None => continue,
        };
        let archive = match ArchiveSlot::deserialize_slot(slot_data) {
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

#[inline]
fn page_type(data: &[u8], page_id: u32) -> Option<u16> {
    let offset = (page_id as usize) * PAGE_SIZE + 4;
    if offset + 2 > data.len() {
        return None;
    }
    Some(u16::from_le_bytes([data[offset], data[offset + 1]]))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::file::page::{allocate_page, write_page_data};
    use crate::layers::archive::{ArchiveSlot, ContentType};
    use crate::test_helpers::create_test_mmap;

    fn insert_archive(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        id: u64,
        content: &str,
        created_at: i64,
        file: &mut std::fs::File,
    ) {
        let archive = ArchiveSlot {
            id_hash: id,
            content_type: ContentType::Text,
            role: 0,
            context_id: 1,
            created_at,
            content: content.into(),
            metadata: None,
        };
        let page_id = allocate_page(
            mmap,
            header,
            PageType::Archive,
            4,
            crate::index::btree::EMPTY_PAGE,
            file,
        )
        .unwrap();
        write_page_data(mmap, page_id, &archive.serialize().unwrap()).unwrap();
        btree.insert(id, (page_id as u64) << 16);
    }

    #[test]
    fn test_search_l4_filters() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);

        insert_archive(
            &mut mmap,
            &mut header,
            &mut btree,
            100,
            "hello world",
            1000,
            &mut file,
        );
        insert_archive(
            &mut mmap,
            &mut header,
            &mut btree,
            101,
            "rust code",
            2000,
            &mut file,
        );
        insert_archive(
            &mut mmap,
            &mut header,
            &mut btree,
            102,
            "world news",
            3000,
            &mut file,
        );

        let recent = search_l4(
            &mmap,
            &header,
            &btree,
            L4SearchQuery {
                recent: Some(2),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(recent.len(), 2);
        assert_eq!(recent[0].created_at, 3000);

        let range = search_l4(
            &mmap,
            &header,
            &btree,
            L4SearchQuery {
                time_range: Some((1500, 2500)),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(range.len(), 1);
        assert_eq!(range[0].id_hash, 101);

        let keyword = search_l4(
            &mmap,
            &header,
            &btree,
            L4SearchQuery {
                keywords: Some(vec!["world".into()]),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(keyword.len(), 2);
    }
}
