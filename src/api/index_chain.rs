// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Multi-page index serialization helpers for SparseIndex, L3Index, and BTree.

use crate::file::header::{FileHeader, LAYER_ROOT_BTREE};
use crate::file::page::{read_page_data, write_page_data};
use crate::index::btree::{BTreeIndex, EMPTY_PAGE};
use crate::index::sparse::{SparseIndex, SparsePageData, SPARSE_MAGIC};
use crate::util::{PageType, PAGE_SIZE};
use crate::{MemHop, MemHopError, Result};
use memmap2::{Mmap, MmapMut};
use std::collections::HashMap;

/// Magic number for the L3 index directory page.
pub(crate) const L3_INDEX_DIRECTORY_MAGIC: u32 = 0x4C334444; // "L3DD"

/// Parsed contents of a multi-page SparseIndex directory page.
pub(crate) struct SparseDirectory {
    pub(crate) term_bucket_count: u32,
    pub(crate) doc_bucket_count: u32,
    pub(crate) term_count: u32,
    pub(crate) doc_count: u32,
    pub(crate) total_term_count: u64,
    pub(crate) avg_doc_length: f32,
    pub(crate) k1: f32,
    pub(crate) b: f32,
    pub(crate) term_primary_pages: Vec<u32>,
    pub(crate) doc_primary_pages: Vec<u32>,
}

impl SparseDirectory {
    pub(crate) fn parse(data: &[u8]) -> Option<Self> {
        if data.len() < 44 {
            return None;
        }
        let magic = u32::from_le_bytes([data[0], data[1], data[2], data[3]]);
        if magic != SPARSE_MAGIC {
            return None;
        }
        let term_bucket_count = u32::from_le_bytes([data[4], data[5], data[6], data[7]]);
        let doc_bucket_count = u32::from_le_bytes([data[8], data[9], data[10], data[11]]);
        let term_count = u32::from_le_bytes([data[12], data[13], data[14], data[15]]);
        let doc_count = u32::from_le_bytes([data[16], data[17], data[18], data[19]]);
        let total_term_count = u64::from_le_bytes([
            data[20], data[21], data[22], data[23], data[24], data[25], data[26], data[27],
        ]);
        let avg_doc_length = f32::from_le_bytes([data[28], data[29], data[30], data[31]]);
        let k1 = f32::from_le_bytes([data[32], data[33], data[34], data[35]]);
        let b = f32::from_le_bytes([data[36], data[37], data[38], data[39]]);
        // Bytes [40..44] are a reserved slot; kept for header compatibility.
        let _ = u32::from_le_bytes([data[40], data[41], data[42], data[43]]);

        let mut offset = 44usize;
        let term_pages_len = term_bucket_count as usize * 4;
        let doc_pages_len = doc_bucket_count as usize * 4;
        if data.len() < offset + term_pages_len + doc_pages_len {
            return None;
        }

        let mut term_primary_pages = Vec::with_capacity(term_bucket_count as usize);
        for _ in 0..term_bucket_count {
            term_primary_pages.push(u32::from_le_bytes([
                data[offset],
                data[offset + 1],
                data[offset + 2],
                data[offset + 3],
            ]));
            offset += 4;
        }

        let mut doc_primary_pages = Vec::with_capacity(doc_bucket_count as usize);
        for _ in 0..doc_bucket_count {
            doc_primary_pages.push(u32::from_le_bytes([
                data[offset],
                data[offset + 1],
                data[offset + 2],
                data[offset + 3],
            ]));
            offset += 4;
        }

        Some(Self {
            term_bucket_count,
            doc_bucket_count,
            term_count,
            doc_count,
            total_term_count,
            avg_doc_length,
            k1,
            b,
            term_primary_pages,
            doc_primary_pages,
        })
    }
}

impl MemHop {
    // ===================================================================
    // SparseIndex multi-page serialization helpers
    // ===================================================================

    /// Read all page payloads in a chain starting at `start_page`.
    pub(crate) fn read_sparse_chain(
        mmap: &Mmap,
        header: &FileHeader,
        start_page: u32,
    ) -> Vec<Vec<u8>> {
        let mut pages = Vec::new();
        let mut current = start_page;
        let mut chain_len = 0u32;
        while current != EMPTY_PAGE && current < header.page_count {
            match read_page_data(mmap, current) {
                Ok(data) => pages.push(data.to_vec()),
                Err(_) => break,
            }
            match crate::file::page::read_page_header(mmap, current) {
                Ok(page_header) => current = page_header.next_page,
                Err(_) => break,
            }
            chain_len += 1;
            if chain_len > header.page_count {
                break;
            }
        }
        pages
    }

    /// Allocate a chain of file pages and write payloads to them.
    /// Returns the primary page id, or 0 if `payloads` is empty.
    pub(crate) fn allocate_sparse_chain(&mut self, payloads: &[Vec<u8>]) -> Result<u32> {
        if payloads.is_empty() {
            return Ok(0);
        }

        let mut next = EMPTY_PAGE;
        let mut page_ids = vec![0u32; payloads.len()];
        for (i, _payload) in payloads.iter().enumerate().rev() {
            let page_type = if i == 0 {
                PageType::SparseIndex
            } else {
                PageType::Overflow
            };
            let page_id = self.allocate_page(page_type, 0, next)?;
            page_ids[i] = page_id;
            next = page_id;
        }

        for (page_id, payload) in page_ids.iter().zip(payloads.iter()) {
            write_page_data(&mut self.mmap, *page_id, payload)?;
        }

        Ok(page_ids[0])
    }

    /// Write SparseIndex page chains and return the directory page id.
    pub(crate) fn write_sparse_pages(&mut self, page_data: &SparsePageData) -> Result<u32> {
        let mut term_starts = Vec::with_capacity(page_data.term_bucket_count as usize);
        for bucket in &page_data.term_buckets {
            term_starts.push(self.allocate_sparse_chain(bucket)?);
        }

        let mut doc_starts = Vec::with_capacity(page_data.doc_bucket_count as usize);
        for bucket in &page_data.doc_buckets {
            doc_starts.push(self.allocate_sparse_chain(bucket)?);
        }

        let directory_payload =
            crate::index::sparse::build_sparse_directory(page_data, &term_starts, &doc_starts);
        let directory_page = self.allocate_page(PageType::SparseIndex, 0, EMPTY_PAGE)?;
        write_page_data(&mut self.mmap, directory_page, &directory_payload)?;
        Ok(directory_page)
    }

    /// Load the SparseIndex from disk, supporting both the new multi-page
    /// format and the legacy single-page bincode format.
    pub(crate) fn load_sparse_index(mmap: &Mmap, header: &FileHeader) -> SparseIndex {
        use crate::file::header::LAYER_ROOT_SPARSE;

        let directory_page = header.layer_roots[LAYER_ROOT_SPARSE];
        if directory_page == 0 || directory_page >= header.page_count {
            return SparseIndex::new();
        }

        let dir_data = match read_page_data(mmap, directory_page) {
            Ok(d) => d,
            Err(_) => return SparseIndex::new(),
        };

        if dir_data.len() >= 4 {
            let magic = u32::from_le_bytes([dir_data[0], dir_data[1], dir_data[2], dir_data[3]]);
            if magic == SPARSE_MAGIC {
                if let Some(dir) = SparseDirectory::parse(dir_data) {
                    let mut term_buckets = Vec::with_capacity(dir.term_bucket_count as usize);
                    for &page_id in &dir.term_primary_pages {
                        if page_id == 0 {
                            term_buckets.push(Vec::new());
                        } else {
                            term_buckets.push(Self::read_sparse_chain(mmap, header, page_id));
                        }
                    }

                    let mut doc_buckets = Vec::with_capacity(dir.doc_bucket_count as usize);
                    for &page_id in &dir.doc_primary_pages {
                        if page_id == 0 {
                            doc_buckets.push(Vec::new());
                        } else {
                            doc_buckets.push(Self::read_sparse_chain(mmap, header, page_id));
                        }
                    }

                    let page_data = SparsePageData {
                        term_bucket_count: dir.term_bucket_count,
                        doc_bucket_count: dir.doc_bucket_count,
                        term_count: dir.term_count,
                        doc_count: dir.doc_count,
                        total_term_count: dir.total_term_count,
                        avg_doc_length: dir.avg_doc_length,
                        k1: dir.k1,
                        b: dir.b,
                        term_buckets,
                        doc_buckets,
                    };

                    match SparseIndex::deserialize_from_pages(&page_data) {
                        Ok(index) => return index,
                        Err(e) => {
                            tracing::warn!(
                                "Failed to load multi-page Sparse Index: {}. Trying legacy format.",
                                e
                            );
                            // Fall through to legacy single-page format below.
                        }
                    }
                }
            }
        }

        // Legacy single-page bincode format.
        match SparseIndex::deserialize(dir_data) {
            Ok(index) => index,
            Err(e) => {
                tracing::warn!(
                    "Failed to load Sparse Index from disk: {}. Using empty index.",
                    e
                );
                SparseIndex::new()
            }
        }
    }

    // ===================================================================
    // L3Index multi-page persistence helpers
    // ===================================================================

    /// Read the L3 hypergraph index map from disk.
    pub(crate) fn read_l3_index_pages(
        mmap: &Mmap,
        header: &FileHeader,
        root_page: u32,
    ) -> Result<HashMap<u64, crate::l3::L3Index>> {
        const HEADER_SIZE: usize = 8;
        const ENTRY_SIZE: usize = 12;

        if root_page == 0 || root_page >= header.page_count {
            return Err(MemHopError::InvalidPageType);
        }

        let dir_data = read_page_data(mmap, root_page)?;
        if dir_data.len() < HEADER_SIZE {
            return Err(MemHopError::Serialization(
                "L3 index directory page too small".to_string(),
            ));
        }

        let magic = u32::from_le_bytes([dir_data[0], dir_data[1], dir_data[2], dir_data[3]]);
        if magic != L3_INDEX_DIRECTORY_MAGIC {
            return Err(MemHopError::Serialization(
                "L3 index directory magic mismatch".to_string(),
            ));
        }

        let count =
            u32::from_le_bytes([dir_data[4], dir_data[5], dir_data[6], dir_data[7]]) as usize;
        if dir_data.len() < HEADER_SIZE + count * ENTRY_SIZE {
            return Err(MemHopError::Serialization(
                "L3 index directory entries truncated".to_string(),
            ));
        }

        let mut map = HashMap::with_capacity(count);
        for i in 0..count {
            let off = HEADER_SIZE + i * ENTRY_SIZE;
            let graph_id = u64::from_le_bytes([
                dir_data[off],
                dir_data[off + 1],
                dir_data[off + 2],
                dir_data[off + 3],
                dir_data[off + 4],
                dir_data[off + 5],
                dir_data[off + 6],
                dir_data[off + 7],
            ]);
            let first_page = u32::from_le_bytes([
                dir_data[off + 8],
                dir_data[off + 9],
                dir_data[off + 10],
                dir_data[off + 11],
            ]);

            if first_page == 0 || first_page >= header.page_count {
                tracing::warn!(
                    "Skipping L3 index entry for graph {}: invalid first page {}",
                    graph_id,
                    first_page
                );
                continue;
            }

            match crate::l3::L3Index::read_from_pages(mmap, first_page)
                .map_err(MemHopError::Serialization)
            {
                Ok(index) => {
                    map.insert(graph_id, index);
                }
                Err(e) => {
                    tracing::warn!(
                        "Failed to read L3 index for graph {}: {}. Skipping.",
                        graph_id,
                        e
                    );
                }
            }
        }

        Ok(map)
    }

    /// Write bucket page chains for the btree and return the directory page id.
    pub(crate) fn write_btree_pages(
        &mut self,
        page_data: &crate::index::btree::BTreePageData,
    ) -> Result<u32> {
        let bucket_count = page_data.bucket_count as usize;
        if bucket_count == 0 {
            return Ok(0);
        }

        // Allocate primary bucket pages (not required to be contiguous).
        let mut primary_pages = Vec::with_capacity(bucket_count);
        for _ in 0..bucket_count {
            let page_id = self.allocate_page(PageType::BTreeLeaf, 0, EMPTY_PAGE)?;
            primary_pages.push(page_id);
        }

        // Write each bucket's chain.
        for (bucket_idx, bucket) in page_data.buckets.iter().enumerate() {
            let primary_page = primary_pages[bucket_idx];
            let mut prev_page = primary_page;

            for (page_idx, page_payload) in bucket.iter().enumerate() {
                let page_id = if page_idx == 0 {
                    primary_page
                } else {
                    self.allocate_page(PageType::Overflow, 0, EMPTY_PAGE)?
                };

                // Write page payload.
                write_page_data(&mut self.mmap, page_id, page_payload)?;

                if page_idx > 0 {
                    // Link previous page to this overflow page.
                    let mut prev_header = {
                        let offset = (prev_page as usize) * PAGE_SIZE;
                        let mut bytes = [0u8; 32];
                        bytes.copy_from_slice(&self.mmap[offset..offset + 32]);
                        crate::file::page::PageHeader::from_bytes(&bytes)?
                    };
                    prev_header.next_page = page_id;
                    crate::file::page::write_page_header(&mut self.mmap, prev_page, &prev_header)?;
                    prev_page = page_id;
                } else {
                    // Ensure primary page header has no stale next_page.
                    let mut header = {
                        let offset = (page_id as usize) * PAGE_SIZE;
                        let mut bytes = [0u8; 32];
                        bytes.copy_from_slice(&self.mmap[offset..offset + 32]);
                        crate::file::page::PageHeader::from_bytes(&bytes)?
                    };
                    header.next_page = EMPTY_PAGE;
                    crate::file::page::write_page_header(&mut self.mmap, page_id, &header)?;
                }
            }
        }

        // Allocate and write directory page.
        let directory_page = self.allocate_page(PageType::BTreeLeaf, 0, EMPTY_PAGE)?;
        let mut dir_data = Vec::with_capacity(8 + bucket_count * 4);
        dir_data.extend_from_slice(&page_data.bucket_count.to_le_bytes());
        dir_data.extend_from_slice(&page_data.split_pointer.to_le_bytes());
        for &page_id in &primary_pages {
            dir_data.extend_from_slice(&page_id.to_le_bytes());
        }
        write_page_data(&mut self.mmap, directory_page, &dir_data)?;

        Ok(directory_page)
    }

    /// Load the B-tree index from disk.
    ///
    /// Reads the Linear Hash bucket layout starting at the page recorded in
    /// `header.reserved[8..12]`. Falls back to the legacy single-page format
    /// stored at `header.layer_roots[LAYER_ROOT_BTREE]` if the new metadata is missing or
    /// invalid, and finally falls back to an empty index.
    pub(crate) fn load_btree(mmap: &Mmap, header: &FileHeader) -> BTreeIndex {
        let bucket_count = u32::from_le_bytes([
            header.reserved[0],
            header.reserved[1],
            header.reserved[2],
            header.reserved[3],
        ]);
        let split_pointer = u32::from_le_bytes([
            header.reserved[4],
            header.reserved[5],
            header.reserved[6],
            header.reserved[7],
        ]);
        let directory_page = u32::from_le_bytes([
            header.reserved[8],
            header.reserved[9],
            header.reserved[10],
            header.reserved[11],
        ]);

        // Try new multi-page Linear Hash format using a directory page.
        if directory_page != 0 && directory_page < header.page_count && bucket_count > 0 {
            let dir_offset = (directory_page as usize) * PAGE_SIZE + 32;
            let dir_data = &mmap[dir_offset..dir_offset + PAGE_SIZE - 32];
            let primary_pages = Self::read_directory_page(dir_data, bucket_count);
            if primary_pages.len() == bucket_count as usize {
                let mut buckets: Vec<Vec<Vec<u8>>> = Vec::with_capacity(bucket_count as usize);
                let mut valid = true;

                for primary_page in primary_pages {
                    if primary_page == 0 || primary_page >= header.page_count {
                        valid = false;
                        break;
                    }

                    let mut bucket_pages: Vec<Vec<u8>> = Vec::new();
                    let mut current_page = primary_page;
                    let mut chain_len = 0u32;
                    while current_page != EMPTY_PAGE && current_page < header.page_count {
                        match read_page_data(mmap, current_page) {
                            Ok(data) => bucket_pages.push(data.to_vec()),
                            Err(_) => break,
                        }
                        match crate::file::page::read_page_header(mmap, current_page) {
                            Ok(page_header) => current_page = page_header.next_page,
                            Err(_) => break,
                        }
                        chain_len += 1;
                        // Safety guard against corrupt circular chains.
                        if chain_len > header.page_count {
                            valid = false;
                            break;
                        }
                    }
                    buckets.push(bucket_pages);
                }

                if valid {
                    match BTreeIndex::deserialize_from_pages(&buckets, bucket_count, split_pointer)
                    {
                        Ok(index) => return index,
                        Err(e) => {
                            tracing::warn!(
                                "Failed to load Linear Hash B-tree from disk: {}. Trying legacy format.",
                                e
                            );
                        }
                    }
                }
            }
        }

        // Fall back to legacy single-page format.
        let btree_page = header.layer_roots[LAYER_ROOT_BTREE];
        if btree_page != 0 && btree_page < header.page_count {
            match read_page_data(mmap, btree_page) {
                Ok(data) => match BTreeIndex::deserialize(data) {
                    Ok(index) => return index,
                    Err(e) => {
                        tracing::warn!(
                            "Failed to load legacy B-tree from disk: {}. Using empty index.",
                            e
                        );
                    }
                },
                Err(e) => {
                    tracing::warn!(
                        "Failed to read legacy B-tree page: {}. Using empty index.",
                        e
                    );
                }
            }
        }

        BTreeIndex::new()
    }

    /// Parse the bucket directory page data and return the primary page id for each bucket.
    pub(crate) fn read_directory_page(dir_data: &[u8], bucket_count: u32) -> Vec<u32> {
        let mut primary_pages = Vec::new();
        let needed = 8 + bucket_count as usize * 4;
        if dir_data.len() < needed {
            return primary_pages;
        }
        for i in 0..bucket_count as usize {
            let off = 8 + i * 4;
            primary_pages.push(u32::from_le_bytes([
                dir_data[off],
                dir_data[off + 1],
                dir_data[off + 2],
                dir_data[off + 3],
            ]));
        }
        primary_pages
    }

    /// Free a chain of pages starting at `start_page`.
    pub(crate) fn free_page_chain(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        start_page: u32,
    ) -> Result<()> {
        let mut current = start_page;
        let mut chain_len = 0u32;
        while current != EMPTY_PAGE && current < header.page_count {
            let next = {
                let offset = (current as usize) * PAGE_SIZE;
                let mut bytes = [0u8; 32];
                bytes.copy_from_slice(&mmap[offset..offset + 32]);
                match crate::file::page::PageHeader::from_bytes(&bytes) {
                    Ok(h) => h.next_page,
                    Err(_) => break,
                }
            };
            crate::file::free_list::free_page(mmap, header, current)?;
            current = next;
            chain_len += 1;
            if chain_len > header.page_count {
                break;
            }
        }
        Ok(())
    }
}
