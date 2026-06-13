// Free list module
use crate::file::header::FileHeader;
use crate::util::PAGE_SIZE;
use crate::MemHopError;
use memmap2::MmapMut;

const EMPTY_FREE_LIST: u32 = 0xFFFFFFFF;

/// Initialize free list in FileHeader
pub fn init_free_list(header: &mut FileHeader) -> Result<(), MemHopError> {
    // Set free_list_head to EMPTY_FREE_LIST (no free pages initially)
    header.free_list_head = EMPTY_FREE_LIST;
    Ok(())
}

/// Allocate a page from free list or extend file
pub fn allocate_from_free_list(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
) -> Result<u32, MemHopError> {
    // Read free list head from FileHeader
    let first_free = header.free_list_head;

    if first_free == EMPTY_FREE_LIST {
        // Extend file: allocate new page at end
        // Calculate new page ID based on current mmap size
        let _new_page_id = (mmap.len() / PAGE_SIZE) as u32;

        // In real implementation, we'd need to extend the file and remap
        // For now, return error as file extension requires OS-level operations
        Err(MemHopError::Io(std::io::Error::other(
            "File extension not yet implemented - pre-allocate file space",
        )))
    } else {
        // Use first free page
        // Validate page_id is within bounds
        let next_free_offset = first_free as usize * PAGE_SIZE;
        if next_free_offset + 4 > mmap.len() {
            return Err(MemHopError::Io(std::io::Error::other(
                format!("Free list page ID {} out of bounds (file size: {} bytes)", first_free, mmap.len())
            )));
        }
        
        // Read next free page ID from the allocated page
        let next_free_data = &mmap[next_free_offset..next_free_offset + 4];
        let next_free = u32::from_le_bytes(next_free_data.try_into().unwrap());

        // Update free list head in FileHeader
        header.free_list_head = next_free;

        Ok(first_free)
    }
}

/// Free a page by adding it to free list
pub fn free_page(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    page_id: u32,
) -> Result<(), MemHopError> {
    // Validate page_id is within file bounds
    let page_offset = page_id as usize * PAGE_SIZE;
    if page_offset + 4 > mmap.len() {
        return Err(MemHopError::Io(std::io::Error::other(
            format!("Page ID {} out of bounds (file size: {} bytes)", page_id, mmap.len())
        )));
    }

    // Read current free list head from FileHeader
    let current_head = header.free_list_head;

    // Write current head to the freed page's first 4 bytes
    mmap[page_offset..page_offset + 4].copy_from_slice(&current_head.to_le_bytes());

    // Update free list head in FileHeader to point to this page
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

        // Initialize free list
        init_free_list(&mut header).unwrap();

        // Verify free list is initialized to EMPTY_FREE_LIST
        assert_eq!(header.free_list_head, EMPTY_FREE_LIST);
    }

    #[test]
    fn test_free_and_allocate_page() {
        let temp_file = NamedTempFile::new().unwrap();
        let path = temp_file.path();

        // Create file with 4 pages
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

        // Initialize free list
        init_free_list(&mut header).unwrap();

        // Free page 3
        free_page(&mut mmap, &mut header, 3).unwrap();

        // Allocate should return page 3
        let allocated = allocate_from_free_list(&mut mmap, &mut header).unwrap();
        assert_eq!(allocated, 3);

        // Next allocation should fail (no more free pages)
        assert!(allocate_from_free_list(&mut mmap, &mut header).is_err());
    }

    #[test]
    fn test_free_list_chain() {
        let temp_file = NamedTempFile::new().unwrap();
        let path = temp_file.path();

        // Create file with 5 pages
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

        // Initialize free list
        init_free_list(&mut header).unwrap();

        // Free pages in order: 3, then 4
        free_page(&mut mmap, &mut header, 3).unwrap();
        free_page(&mut mmap, &mut header, 4).unwrap();

        // Allocate should return pages in LIFO order (4, then 3)
        let alloc1 = allocate_from_free_list(&mut mmap, &mut header).unwrap();
        assert_eq!(alloc1, 4);

        let alloc2 = allocate_from_free_list(&mut mmap, &mut header).unwrap();
        assert_eq!(alloc2, 3);
    }
}
