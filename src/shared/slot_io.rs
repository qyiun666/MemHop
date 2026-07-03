// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Slot I/O helpers for page/slot offset calculation and data access.

use crate::util::PAGE_SIZE;

#[inline]
pub fn decode_page_id(page_ref: u64) -> u32 {
    (page_ref >> 16) as u32
}

/// Calculate page offset (page header start, no +32)
#[inline]
pub fn page_offset(page_id: u32) -> usize {
    (page_id as usize) * PAGE_SIZE
}

/// Calculate slot offset within a page
#[inline]
pub fn slot_offset(page_id: u32) -> usize {
    (page_id as usize) * PAGE_SIZE + 32
}

#[inline]
pub fn get_slot_data(mmap: &[u8], page_ref: u64) -> Option<&[u8]> {
    let page_id = decode_page_id(page_ref);
    let offset = slot_offset(page_id);

    if offset < mmap.len() {
        Some(&mmap[offset..])
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_decode_page_id() {
        let page_ref = (5u64 << 16) | 0x1234;
        assert_eq!(decode_page_id(page_ref), 5);
    }

    #[test]
    fn test_slot_offset() {
        assert_eq!(slot_offset(0), 32);
        assert_eq!(slot_offset(1), PAGE_SIZE + 32);
    }
}
