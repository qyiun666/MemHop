// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Generic magic-header page-chain helpers shared by L1 reverse index and L6 pathway weights.

use crate::file::header::FileHeader;
use crate::file::page::{read_page_data, write_page_data};
use crate::index::btree::EMPTY_PAGE;
use crate::util::{PageType, PAGE_SIZE};
use crate::{MemHopError, Result};
use memmap2::MmapMut;
use std::fs::File;

/// Magic number for the L1 reverse index page chain.
pub const L1REVERSE_MAGIC: u32 = 0x4C315256; // "L1RV"

/// Magic number for the L6 pathway weight page chain.
pub const PATHWAY_MAGIC: u32 = 0x4C365057; // "L6PW"

const HEADER_SIZE: usize = 8;
const FIRST_PAGE_DATA_CAPACITY: usize = PAGE_SIZE - 32 - HEADER_SIZE;
const OVERFLOW_DATA_CAPACITY: usize = PAGE_SIZE - 32;

/// Read all payload bytes in a magic-header page chain starting at `start_page`.
pub fn read_magic_chain(
    mmap: &[u8],
    header: &FileHeader,
    start_page: u32,
    magic: u32,
) -> Result<Vec<u8>> {
    if start_page == 0 || start_page >= header.page_count {
        return Err(MemHopError::InvalidPageType);
    }

    let first_payload = read_page_data(mmap, start_page)?;
    if first_payload.len() < HEADER_SIZE {
        return Err(MemHopError::Serialization(
            "Magic chain page too small".to_string(),
        ));
    }

    let page_magic = u32::from_le_bytes([
        first_payload[0],
        first_payload[1],
        first_payload[2],
        first_payload[3],
    ]);
    if page_magic != magic {
        return Err(MemHopError::Serialization(
            "Magic chain magic mismatch".to_string(),
        ));
    }

    let total_length = u32::from_le_bytes([
        first_payload[4],
        first_payload[5],
        first_payload[6],
        first_payload[7],
    ]) as usize;

    let mut pages = Vec::new();
    pages.push(first_payload.to_vec());
    let mut current = start_page;
    let mut chain_len = 0u32;
    loop {
        let page_header = crate::file::page::read_page_header(mmap, current)?;
        current = page_header.next_page;
        if current == EMPTY_PAGE || current >= header.page_count {
            break;
        }
        pages.push(read_page_data(mmap, current)?.to_vec());
        chain_len += 1;
        if chain_len > header.page_count {
            return Err(MemHopError::Serialization(
                "Magic chain too long".to_string(),
            ));
        }
    }

    let mut result = Vec::with_capacity(total_length);
    let first_data = &first_payload[HEADER_SIZE..];
    let first_take = first_data.len().min(total_length);
    result.extend_from_slice(&first_data[..first_take]);

    for payload in pages.iter().skip(1) {
        if result.len() >= total_length {
            break;
        }
        let remaining = total_length - result.len();
        let take = payload.len().min(remaining);
        result.extend_from_slice(&payload[..take]);
    }

    if result.len() != total_length {
        return Err(MemHopError::Serialization(
            "Magic chain length mismatch".to_string(),
        ));
    }

    Ok(result)
}

/// Allocate a chain of pages, write `data` with a magic header, and return the root page id.
pub fn write_magic_chain(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    file: &mut File,
    data: &[u8],
    page_type: PageType,
    magic: u32,
) -> Result<u32> {
    if data.is_empty() {
        return Ok(0);
    }

    let total_length = data.len();
    let overflow_needed = if total_length > FIRST_PAGE_DATA_CAPACITY {
        (total_length - FIRST_PAGE_DATA_CAPACITY).div_ceil(OVERFLOW_DATA_CAPACITY)
    } else {
        0
    };
    let page_count = 1 + overflow_needed;

    let mut page_ids = vec![0u32; page_count];
    let mut next = EMPTY_PAGE;
    for i in (0..page_count).rev() {
        let page_id = crate::file::page::allocate_page(
            mmap,
            header,
            if i == 0 {
                page_type
            } else {
                PageType::Overflow
            },
            0,
            next,
            file,
        )?;
        page_ids[i] = page_id;
        next = page_id;
    }

    let first_data_len = total_length.min(FIRST_PAGE_DATA_CAPACITY);
    let mut first_payload = Vec::with_capacity(HEADER_SIZE + first_data_len);
    first_payload.extend_from_slice(&magic.to_le_bytes());
    first_payload.extend_from_slice(&(total_length as u32).to_le_bytes());
    first_payload.extend_from_slice(&data[..first_data_len]);
    write_page_data(mmap, page_ids[0], &first_payload)?;

    let mut offset = first_data_len;
    for &page_id in page_ids.iter().skip(1) {
        let end = (offset + OVERFLOW_DATA_CAPACITY).min(total_length);
        write_page_data(mmap, page_id, &data[offset..end])?;
        offset = end;
    }

    Ok(page_ids[0])
}

/// Free all pages in a magic-header chain starting at `start_page`.
pub fn free_magic_chain(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    start_page: u32,
) -> Result<()> {
    if start_page == 0 || start_page >= header.page_count {
        return Ok(());
    }

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
