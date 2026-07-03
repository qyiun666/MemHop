// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Checkpoint, persistence, and page-allocation helpers.

use crate::file::header::{
    LAYER_ROOT_BTREE, LAYER_ROOT_L1_INVERTED, LAYER_ROOT_L3, LAYER_ROOT_L6, LAYER_ROOT_SPARSE,
};
use crate::file::page::read_page_data;
use crate::util::PAGE_SIZE;
use crate::MemHop;
use crate::MemHopError;
use crate::Result;
use memmap2::MmapMut;

/// Magic number for the L3 index directory page.
const L3_INDEX_DIRECTORY_MAGIC: u32 = 0x4C334444; // "L3DD"

impl MemHop {
    /// Extend the database file by `grow_pages` pages and remap mmap.
    ///
    /// New pages are initialized as free-list pages (`PageType::Free`) and linked
    /// into the existing free list. After extending, A/B dual headers are rewritten
    /// via the checkpoint mechanism for crash safety.
    pub fn extend_file(&mut self, grow_pages: u32) -> Result<()> {
        let old_page_count = self.header.page_count;
        let new_page_count = old_page_count + grow_pages;
        let new_size = (new_page_count as usize) * PAGE_SIZE;
        let old_free_list_head = self.header.free_list_head;

        self.file.set_len(new_size as u64)?;

        self.mmap = unsafe { MmapMut::map_mut(&self.file)? };

        // Link new pages in reverse order (LIFO) in front of existing free list.
        // Each free page stores the next free page id in its first 4 bytes.
        let mut next_free = old_free_list_head;
        let free_type = crate::util::PageType::Free.to_u16().to_le_bytes();
        for page_id in (old_page_count..new_page_count).rev() {
            let page_offset = (page_id as usize) * PAGE_SIZE;
            self.mmap[page_offset..page_offset + 4].copy_from_slice(&next_free.to_le_bytes());
            self.mmap[page_offset + 4..page_offset + 6].copy_from_slice(&free_type);
            next_free = page_id;
        }

        // Update header; rollback on checkpoint failure
        self.header.free_list_head = next_free;
        self.header.page_count = new_page_count;

        if let Err(e) = self.checkpoint() {
            self.header.free_list_head = old_free_list_head;
            self.header.page_count = old_page_count;
            return Err(e);
        }

        Ok(())
    }

    /// Allocate a new page, automatically extending the file if the free list is exhausted.
    ///
    /// `allocate_page` now has built-in auto-extension via `allocate_or_extend`.
    pub fn allocate_page(
        &mut self,
        page_type: crate::util::PageType,
        layer_id: u16,
        next_page_id: u32,
    ) -> Result<u32> {
        crate::file::page::allocate_page(
            &mut self.mmap,
            &mut self.header,
            page_type,
            layer_id,
            next_page_id,
            &mut self.file,
        )
    }

    /// Persist an index by writing its on-disk representation and recording
    /// the new root page in `layer_roots`. The caller-supplied closure performs
    /// the index-specific serialization and page allocation.
    fn persist_index<W>(&mut self, layer_root_idx: usize, writer: W) -> Result<u32>
    where
        W: FnOnce(&mut Self) -> Result<u32>,
    {
        let root_page = writer(self)?;
        self.header.layer_roots[layer_root_idx] = root_page;
        Ok(root_page)
    }

    /// Checkpoint: save indices to disk and update header
    pub fn checkpoint(&mut self) -> Result<()> {
        // Save old page references so we can free them AFTER writing new pages.
        let old_btree_directory = u32::from_le_bytes([
            self.header.reserved[8],
            self.header.reserved[9],
            self.header.reserved[10],
            self.header.reserved[11],
        ]);
        let old_btree_bucket_count = u32::from_le_bytes([
            self.header.reserved[0],
            self.header.reserved[1],
            self.header.reserved[2],
            self.header.reserved[3],
        ]);
        let old_sparse_directory = self.header.layer_roots[LAYER_ROOT_SPARSE];
        let old_l1_root = self.header.layer_roots[LAYER_ROOT_L1_INVERTED];
        let old_btree_root = self.header.layer_roots[LAYER_ROOT_BTREE];
        let old_l3_root = self.header.layer_roots[LAYER_ROOT_L3];

        // Persist B-tree using multi-page Linear Hash layout.
        self.persist_index(LAYER_ROOT_BTREE, |db| {
            let pages = db
                .btree
                .serialize_to_pages()
                .map_err(MemHopError::Serialization)?;
            let directory_page = db.write_btree_pages(&pages)?;
            db.header.reserved[0..4].copy_from_slice(&pages.bucket_count.to_le_bytes());
            db.header.reserved[4..8].copy_from_slice(&pages.split_pointer.to_le_bytes());
            db.header.reserved[8..12].copy_from_slice(&directory_page.to_le_bytes());
            Ok(directory_page)
        })?;

        // Persist Sparse Index using multi-page bucket chains.
        self.persist_index(LAYER_ROOT_SPARSE, |db| {
            let pages = db
                .sparse_index
                .serialize_to_pages()
                .map_err(MemHopError::Serialization)?;
            db.write_sparse_pages(&pages)
        })?;

        // Persist L1 reverse index using a dedicated page chain.
        self.persist_index(LAYER_ROOT_L1_INVERTED, |db| {
            let data = db.l1_reverse_index.serialize()?;
            db.write_l1_reverse_pages(&data)
        })?;

        // Persist L6 pathway weights using a dedicated page chain.
        let old_pathway_root = self.header.layer_roots[LAYER_ROOT_L6];
        self.persist_index(LAYER_ROOT_L6, |db| {
            let data = crate::layers::pathway::PathwayWeightSlot::serialize_pathways(&db.pathways)?;
            db.write_pathway_pages(&data)
        })?;

        // Persist L3 hypergraph index map.
        self.persist_index(LAYER_ROOT_L3, |db| db.write_l3_index_pages())?;

        // Persist IVF index (non-fatal: warn on failure)
        if let Some(ref ivf) = self.ivf_index {
            if let Err(e) = crate::index::vector::write_ivf_index(
                &mut self.mmap,
                &mut self.header,
                ivf,
                &mut self.file,
            ) {
                tracing::warn!("Failed to persist IVF index: {}", e);
            }
        }

        // Update header commit_id
        self.header.commit_id += 1;

        // Write updated headers with A/B alternation for crash safety:
        // Write B first (backup), flush, then write A (primary), flush.
        // If crash occurs between flushes, select_valid_header picks the one
        // with higher commit_id (both will be valid, both have same content).
        let header_bytes = self.header.to_bytes();
        self.mmap[PAGE_SIZE..PAGE_SIZE * 2].copy_from_slice(&header_bytes); // B first
        self.mmap.flush_range(PAGE_SIZE, PAGE_SIZE)?;
        self.mmap[..PAGE_SIZE].copy_from_slice(&header_bytes); // A second
        self.mmap.flush_range(0, PAGE_SIZE)?;

        // NOW free old pages (write-then-free strategy).
        // If a crash happened before this point, the old pages are still valid
        // and the old headers (A or B) still point to them.
        self.free_old_btree_pages(old_btree_directory, old_btree_bucket_count, old_btree_root)?;
        self.free_old_sparse_pages(old_sparse_directory)?;
        self.free_old_l1_reverse_pages(old_l1_root)?;
        self.free_old_pathway_pages(old_pathway_root)?;
        self.free_l3_index_pages(old_l3_root)?;

        Ok(())
    }

    /// Free old B-tree pages after a successful checkpoint.
    fn free_old_btree_pages(
        &mut self,
        old_directory: u32,
        old_bucket_count: u32,
        old_root: u32,
    ) -> Result<()> {
        if old_directory != 0 && old_directory < self.header.page_count && old_bucket_count > 0 {
            // Free new-format bucket chains via directory page.
            let dir_offset = (old_directory as usize) * PAGE_SIZE + 32;
            let dir_data = &self.mmap[dir_offset..dir_offset + PAGE_SIZE - 32];
            let primary_pages = Self::read_directory_page(dir_data, old_bucket_count);
            for primary_page in primary_pages {
                if primary_page != 0 && primary_page < self.header.page_count {
                    Self::free_page_chain(&mut self.mmap, &mut self.header, primary_page)?;
                }
            }
            // Free directory page itself.
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, old_directory)?;
        } else if old_root != 0 && old_root < self.header.page_count {
            // Free legacy single-page btree.
            crate::file::free_list::free_page(&mut self.mmap, &mut self.header, old_root)?;
        }
        Ok(())
    }

    /// Free old Sparse Index pages after a successful checkpoint.
    fn free_old_sparse_pages(&mut self, old_directory: u32) -> Result<()> {
        if old_directory == 0 || old_directory >= self.header.page_count {
            return Ok(());
        }

        let dir_data = match read_page_data(&self.mmap, old_directory) {
            Ok(d) => d,
            Err(_) => return Ok(()),
        };

        if dir_data.len() >= 4 {
            let magic = u32::from_le_bytes([dir_data[0], dir_data[1], dir_data[2], dir_data[3]]);
            if magic == crate::index::sparse::SPARSE_MAGIC {
                if let Some(dir) = super::SparseDirectory::parse(dir_data) {
                    for &page_id in &dir.term_primary_pages {
                        if page_id != 0 {
                            Self::free_page_chain(&mut self.mmap, &mut self.header, page_id)?;
                        }
                    }
                    for &page_id in &dir.doc_primary_pages {
                        if page_id != 0 {
                            Self::free_page_chain(&mut self.mmap, &mut self.header, page_id)?;
                        }
                    }
                    if dir.entity_start != 0 {
                        Self::free_page_chain(&mut self.mmap, &mut self.header, dir.entity_start)?;
                    }
                }
            }
        }

        crate::file::free_list::free_page(&mut self.mmap, &mut self.header, old_directory)?;
        Ok(())
    }

    /// Free old L1 reverse index pages after a successful checkpoint.
    fn free_old_l1_reverse_pages(&mut self, old_root: u32) -> Result<()> {
        if old_root == 0 || old_root >= self.header.page_count {
            return Ok(());
        }
        Self::free_page_chain(&mut self.mmap, &mut self.header, old_root)?;
        Ok(())
    }

    /// Free old L6 pathway weight pages after a successful checkpoint.
    fn free_old_pathway_pages(&mut self, old_root: u32) -> Result<()> {
        if old_root == 0 || old_root >= self.header.page_count {
            return Ok(());
        }
        Self::free_page_chain(&mut self.mmap, &mut self.header, old_root)?;
        Ok(())
    }

    /// Write the L3 hypergraph index map to disk.
    ///
    /// Each graph index is serialized into a chain of `L3IndexPage` pages; a
    /// single directory page records `[graph_id, first_page_id]` entries and
    /// returns the directory page id.
    fn write_l3_index_pages(&mut self) -> Result<u32> {
        use crate::file::page::write_page_data;
        use crate::util::PageType;

        if self.l3_index_map.is_empty() {
            return Ok(0);
        }

        let indices: Vec<(u64, crate::l3::L3Index)> = self
            .l3_index_map
            .iter()
            .map(|(&graph_id, index)| (graph_id, index.clone()))
            .collect();

        let mut entries: Vec<(u64, u32)> = Vec::with_capacity(indices.len());
        for (graph_id, index) in indices {
            let needed = index.pages_needed().map_err(MemHopError::Serialization)?;
            let needed = needed.max(1);

            let mut page_ids = Vec::with_capacity(needed);
            for _ in 0..needed {
                let page_id =
                    self.allocate_page(PageType::L3IndexPage, 0, crate::index::btree::EMPTY_PAGE)?;
                page_ids.push(page_id);
            }

            index
                .write_to_pages(&mut self.mmap, &page_ids)
                .map_err(MemHopError::Serialization)?;
            entries.push((graph_id, page_ids[0]));
        }

        let mut dir_payload = Vec::with_capacity(8 + entries.len() * 12);
        dir_payload.extend_from_slice(&L3_INDEX_DIRECTORY_MAGIC.to_le_bytes());
        dir_payload.extend_from_slice(&(entries.len() as u32).to_le_bytes());
        for (graph_id, first_page) in entries {
            dir_payload.extend_from_slice(&graph_id.to_le_bytes());
            dir_payload.extend_from_slice(&first_page.to_le_bytes());
        }

        let dir_page =
            self.allocate_page(PageType::L3IndexPage, 0, crate::index::btree::EMPTY_PAGE)?;
        write_page_data(&mut self.mmap, dir_page, &dir_payload)?;

        Ok(dir_page)
    }

    /// Free old L3 hypergraph index pages after a successful checkpoint.
    fn free_l3_index_pages(&mut self, old_root: u32) -> Result<()> {
        if old_root == 0 || old_root >= self.header.page_count {
            return Ok(());
        }

        let dir_data = match crate::file::page::read_page_data(&self.mmap, old_root) {
            Ok(d) => d.to_vec(),
            Err(_) => return Ok(()),
        };

        const HEADER_SIZE: usize = 8;
        const ENTRY_SIZE: usize = 12;

        if dir_data.len() >= HEADER_SIZE {
            let magic = u32::from_le_bytes([dir_data[0], dir_data[1], dir_data[2], dir_data[3]]);
            if magic == L3_INDEX_DIRECTORY_MAGIC {
                let count = u32::from_le_bytes([dir_data[4], dir_data[5], dir_data[6], dir_data[7]])
                    as usize;
                if dir_data.len() >= HEADER_SIZE + count * ENTRY_SIZE {
                    for i in 0..count {
                        let off = HEADER_SIZE + i * ENTRY_SIZE;
                        let first_page = u32::from_le_bytes([
                            dir_data[off + 8],
                            dir_data[off + 9],
                            dir_data[off + 10],
                            dir_data[off + 11],
                        ]);
                        if first_page != 0 && first_page < self.header.page_count {
                            Self::free_page_chain(&mut self.mmap, &mut self.header, first_page)?;
                        }
                    }
                }
            }
        }

        crate::file::free_list::free_page(&mut self.mmap, &mut self.header, old_root)?;
        Ok(())
    }
}
