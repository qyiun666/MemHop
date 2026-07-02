// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

use crate::file::header::FileHeader;
use crate::util::PAGE_SIZE;
use crate::MemHopError;
use memmap2::MmapMut;
use std::fs::File;

/// When free list is exhausted, extends file by `grow_pages` pages and retries.
/// This prevents partial writes that would corrupt the database.
pub fn allocate_or_extend(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    file: &mut File,
    grow_pages: u32,
) -> Result<u32, MemHopError> {
    match allocate_from_free_list(mmap, header) {
        Ok(page_id) => Ok(page_id),
        Err(MemHopError::FileFull) => {
            let old_count = header.page_count;
            let new_count = old_count + grow_pages;
            let new_size = (new_count as usize) * PAGE_SIZE;
            let old_free_list_head = header.free_list_head;

            file.set_len(new_size as u64)?;

            *mmap = unsafe { MmapMut::map_mut(&*file)? };

            // Link new pages into free list (LIFO order)
            let mut next_free = old_free_list_head;
            let free_type = crate::util::PageType::Free.to_u16().to_le_bytes();
            for page_id in (old_count..new_count).rev() {
                let page_offset = (page_id as usize) * PAGE_SIZE;
                mmap[page_offset..page_offset + 4]
                    .copy_from_slice(&next_free.to_le_bytes());
                mmap[page_offset + 4..page_offset + 6]
                    .copy_from_slice(&free_type);
                next_free = page_id;
            }

            header.free_list_head = next_free;
            header.page_count = new_count;

            allocate_from_free_list(mmap, header)
        }
        Err(e) => Err(e),
    }
}

pub const EMPTY_FREE_LIST: u32 = 0xFFFFFFFF;

pub fn init_free_list(header: &mut FileHeader) -> Result<(), MemHopError> {
    header.free_list_head = EMPTY_FREE_LIST;
    Ok(())
}

pub fn allocate_from_free_list(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
) -> Result<u32, MemHopError> {
    let first_free = header.free_list_head;

    if first_free == EMPTY_FREE_LIST {
        Err(MemHopError::FileFull)
    } else {
        let next_free_offset = first_free as usize * PAGE_SIZE;
        if next_free_offset + PAGE_SIZE > mmap.len() {
            return Err(MemHopError::Io(std::io::Error::other(format!(
                "Free list page ID {} out of bounds (file size: {} bytes)",
                first_free,
                mmap.len()
            ))));
        }

        let next_free_data = &mmap[next_free_offset..next_free_offset + 4];
        let next_free = u32::from_le_bytes(next_free_data.try_into().unwrap());

        header.free_list_head = next_free;

        // Zero out entire page to prevent stale data from corrupting
        // subsequent deserialization (e.g., ContextSlot reading garbage as UTF-8)
        let page_start = first_free as usize * PAGE_SIZE;
        mmap[page_start..page_start + PAGE_SIZE].fill(0);

        Ok(first_free)
    }
}

pub fn free_page(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    page_id: u32,
) -> Result<(), MemHopError> {
    let page_offset = page_id as usize * PAGE_SIZE;
    if page_offset + 4 > mmap.len() {
        return Err(MemHopError::Io(std::io::Error::other(format!(
            "Page ID {} out of bounds (file size: {} bytes)",
            page_id,
            mmap.len()
        ))));
    }

    let current_head = header.free_list_head;

    mmap[page_offset..page_offset + 4].copy_from_slice(&current_head.to_le_bytes());

    header.free_list_head = page_id;

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs::File;
    use std::io::Write;
    use tempfile::NamedTempFile;

    #[test]
    fn test_init_free_list() {
        let mut header = FileHeader::new(768);

        init_free_list(&mut header).unwrap();

        assert_eq!(header.free_list_head, EMPTY_FREE_LIST);
    }

    #[test]
    fn test_free_and_allocate_page() {
        let temp_file = NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = File::create(path).unwrap();
        file.write_all(&vec![0u8; PAGE_SIZE * 4]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);

        init_free_list(&mut header).unwrap();

        free_page(&mut mmap, &mut header, 3).unwrap();

        let allocated = allocate_from_free_list(&mut mmap, &mut header).unwrap();
        assert_eq!(allocated, 3);

        assert!(allocate_from_free_list(&mut mmap, &mut header).is_err());
    }

    #[test]
    fn test_free_list_chain() {
        let temp_file = NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = File::create(path).unwrap();
        file.write_all(&vec![0u8; PAGE_SIZE * 5]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);

        init_free_list(&mut header).unwrap();

        free_page(&mut mmap, &mut header, 3).unwrap();
        free_page(&mut mmap, &mut header, 4).unwrap();

        // LIFO order: 4 then 3
        let alloc1 = allocate_from_free_list(&mut mmap, &mut header).unwrap();
        assert_eq!(alloc1, 4);

        let alloc2 = allocate_from_free_list(&mut mmap, &mut header).unwrap();
        assert_eq!(alloc2, 3);
    }
}
