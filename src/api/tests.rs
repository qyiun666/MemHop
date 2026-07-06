// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Unit tests for the MemHop API surface.

use super::*;
use crate::config::MemHopConfig;
use tempfile::TempDir;

#[test]
fn test_file_auto_extension() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("extend.meh");
    let mut config = MemHopConfig::new(path, 768);
    config.encoder_grpc_addr = None; // unit test does not need real encoder
    let mut db = MemHop::open(config).unwrap();

    // Initial database has 2000 pages; pages 18..1999 are free (1982 pages).
    assert_eq!(db.header.page_count, 2000);

    // Consume all initially free pages.
    for _ in 0..1982 {
        db.allocate_page(
            crate::util::PageType::Context,
            2,
            crate::file::free_list::EMPTY_FREE_LIST,
        )
        .unwrap();
    }

    // The next allocation must trigger an automatic extension.
    let page_id = db
        .allocate_page(
            crate::util::PageType::Context,
            2,
            crate::file::free_list::EMPTY_FREE_LIST,
        )
        .unwrap();
    assert!(page_id >= 2000);
    assert_eq!(db.header.page_count, 2500);

    // Additional allocations from the extended region should succeed.
    for _ in 0..10 {
        db.allocate_page(
            crate::util::PageType::Context,
            2,
            crate::file::free_list::EMPTY_FREE_LIST,
        )
        .unwrap();
    }
}

#[test]
fn test_extend_file_preserves_old_free_list() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("extend_old_free.meh");
    let mut config = MemHopConfig::new(path, 768);
    config.encoder_grpc_addr = None; // unit test does not need real encoder
    let mut db = MemHop::open(config).unwrap();

    let old_page_count = db.header.page_count;
    let old_free_list_head = db.header.free_list_head;
    assert_ne!(old_free_list_head, crate::file::free_list::EMPTY_FREE_LIST);

    // Extend the file by a small number of pages.
    let grow_pages = 50;
    db.extend_file(grow_pages).unwrap();

    assert_eq!(db.header.page_count, old_page_count + grow_pages);

    // The last new page is the tail of the new free chain and should
    // still be marked as Free until the whole new chain is consumed.
    let tail_page = old_page_count + grow_pages - 1;
    let free_header = crate::file::page::read_page_header(&db.mmap, tail_page).unwrap();
    assert_eq!(free_header.page_type, crate::util::PageType::Free as u16);

    // All new pages plus at least one page from the old free list must be
    // reachable without triggering another auto-extension.
    for i in 0..grow_pages + 1 {
        db.allocate_page(
            crate::util::PageType::Context,
            2,
            crate::file::free_list::EMPTY_FREE_LIST,
        )
        .unwrap_or_else(|_| panic!("allocation {} should succeed (old free list lost?)", i));
    }
}
