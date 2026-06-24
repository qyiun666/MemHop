// Page management module
use crate::util::{PageType, PAGE_SIZE};
use crate::{MemHopError, Result};
#[cfg(test)]
use memmap2::Mmap;
use memmap2::MmapMut;

/// Page header structure (32 bytes, #[repr(C)])
#[derive(Debug, Clone)]
#[repr(C)]
pub struct PageHeader {
    pub page_id: u32,
    pub page_type: u16,
    pub slot_count: u16,
    pub free_bytes: u16,
    pub layer_id: u16,
    pub next_page: u32,
    pub prev_page: u32,
    pub reserved: [u8; 12],
}

impl PageHeader {
    /// Create a new PageHeader
    pub fn new(page_id: u32, page_type: PageType, layer_id: u16, next_page_id: u32) -> Self {
        Self {
            page_id,
            page_type: page_type.to_u16(),
            slot_count: 0,
            free_bytes: (PAGE_SIZE - 32) as u16, // Total data space
            layer_id,
            next_page: next_page_id,
            prev_page: 0xFFFFFFFF, // No previous page
            reserved: [0; 12],
        }
    }

    /// Serialize to bytes (Little-Endian)
    pub fn to_bytes(&self) -> [u8; 32] {
        let mut bytes = [0u8; 32];

        bytes[0..4].copy_from_slice(&self.page_id.to_le_bytes());
        bytes[4..6].copy_from_slice(&self.page_type.to_le_bytes());
        bytes[6..8].copy_from_slice(&self.slot_count.to_le_bytes());
        bytes[8..10].copy_from_slice(&self.free_bytes.to_le_bytes());
        bytes[10..12].copy_from_slice(&self.layer_id.to_le_bytes());
        bytes[12..16].copy_from_slice(&self.next_page.to_le_bytes());
        bytes[16..20].copy_from_slice(&self.prev_page.to_le_bytes());
        bytes[20..32].copy_from_slice(&self.reserved);

        bytes
    }

    /// Deserialize from bytes (Little-Endian)
    pub fn from_bytes(bytes: &[u8; 32]) -> Result<Self> {
        let page_id = u32::from_le_bytes([bytes[0], bytes[1], bytes[2], bytes[3]]);
        let page_type = u16::from_le_bytes([bytes[4], bytes[5]]);
        let slot_count = u16::from_le_bytes([bytes[6], bytes[7]]);
        let free_bytes = u16::from_le_bytes([bytes[8], bytes[9]]);
        let layer_id = u16::from_le_bytes([bytes[10], bytes[11]]);
        let next_page = u32::from_le_bytes([bytes[12], bytes[13], bytes[14], bytes[15]]);
        let prev_page = u32::from_le_bytes([bytes[16], bytes[17], bytes[18], bytes[19]]);
        let mut reserved = [0u8; 12];
        reserved.copy_from_slice(&bytes[20..32]);

        Ok(Self {
            page_id,
            page_type,
            slot_count,
            free_bytes,
            layer_id,
            next_page,
            prev_page,
            reserved,
        })
    }
}

/// Allocate a new page from the free list, write page header, return page_id
///
/// This is the primary page allocation API for dream stages and other modules
/// that need to allocate pages with a specific PageType and layer_id.
pub fn allocate_page(
    mmap: &mut MmapMut,
    header: &mut crate::file::header::FileHeader,
    page_type: PageType,
    layer_id: u16,
    next_page_id: u32,
) -> Result<u32> {
    // Use free list allocation (reuses freed pages first)
    let new_page_id = crate::file::free_list::allocate_from_free_list(mmap, header)?;

    let page_offset = (new_page_id as usize) * PAGE_SIZE;

    // Safety check: ensure page is within mmap bounds
    if page_offset + PAGE_SIZE > mmap.len() {
        return Err(MemHopError::Io(std::io::Error::other(format!(
            "Allocated page {} out of mmap bounds (size: {})",
            new_page_id,
            mmap.len()
        ))));
    }

    // Zero the page to clear stale data
    mmap[page_offset..page_offset + PAGE_SIZE].fill(0);

    // Write page header
    let page_header = PageHeader::new(new_page_id, page_type, layer_id, next_page_id);
    let header_bytes = page_header.to_bytes();
    mmap[page_offset..page_offset + 32].copy_from_slice(&header_bytes);

    Ok(new_page_id)
}

/// Read page header from mmap
///
/// Accepts any type that dereferences to a byte slice so both `&Mmap` and
/// `&MmapMut` callers can use it without extra coercion.
pub fn read_page_header(mmap: &[u8], page_id: u32) -> Result<PageHeader> {
    let offset = (page_id as usize) * PAGE_SIZE;

    if offset + 32 > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }

    let mut header_bytes = [0u8; 32];
    header_bytes.copy_from_slice(&mmap[offset..offset + 32]);

    PageHeader::from_bytes(&header_bytes)
}

/// Write page header to mmap
pub fn write_page_header(mmap: &mut MmapMut, page_id: u32, header: &PageHeader) -> Result<()> {
    let offset = (page_id as usize) * PAGE_SIZE;

    if offset + 32 > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }

    let header_bytes = header.to_bytes();
    mmap[offset..offset + 32].copy_from_slice(&header_bytes);

    Ok(())
}

/// Read page data (skip 32-byte header)
///
/// Accepts any type that dereferences to a byte slice so both `&Mmap` and
/// `&MmapMut` callers can use it without extra coercion.
pub fn read_page_data(mmap: &[u8], page_id: u32) -> Result<&[u8]> {
    let offset = (page_id as usize) * PAGE_SIZE;

    if offset + PAGE_SIZE > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }

    // Skip 32-byte header
    Ok(&mmap[offset + 32..offset + PAGE_SIZE])
}

/// Write page data (skip 32-byte header)
pub fn write_page_data(mmap: &mut MmapMut, page_id: u32, data: &[u8]) -> Result<()> {
    let offset = (page_id as usize) * PAGE_SIZE;

    if offset + PAGE_SIZE > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }

    if data.len() > PAGE_SIZE - 32 {
        return Err(MemHopError::Serialization(format!(
            "Data too large for page: {} > {}",
            data.len(),
            PAGE_SIZE - 32
        )));
    }

    // Skip 32-byte header
    let start = offset + 32;
    // Zero remaining bytes to prevent stale data residue from previous writes
    let end = offset + PAGE_SIZE;
    if start + data.len() < end {
        mmap[start + data.len()..end].fill(0);
    }
    mmap[start..start + data.len()].copy_from_slice(data);

    Ok(())
}

/// Encode page reference (page_id + slot_index) into u64
pub fn encode_page_ref(page_id: u32, slot_index: u16) -> u64 {
    ((page_id as u64) << 16) | (slot_index as u64)
}

/// Decode page reference from u64
pub fn decode_page_ref(page_ref: u64) -> (u32, u16) {
    let page_id = (page_ref >> 16) as u32;
    let slot_index = (page_ref & 0xFFFF) as u16;
    (page_id, slot_index)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_page_header_serialization_roundtrip() {
        let header = PageHeader::new(42, PageType::ContextNode, 1, 100);

        let bytes = header.to_bytes();
        let restored = PageHeader::from_bytes(&bytes).unwrap();

        assert_eq!(restored.page_id, 42);
        assert_eq!(restored.page_type, PageType::ContextNode.to_u16());
        assert_eq!(restored.slot_count, 0);
        assert_eq!(restored.layer_id, 1);
        assert_eq!(restored.next_page, 100);
        assert_eq!(restored.prev_page, 0xFFFFFFFF);
    }

    #[test]
    fn test_page_type_conversion() {
        assert_eq!(PageType::from_u16(0x01), Some(PageType::ContextNode));
        assert_eq!(PageType::from_u16(0x02), Some(PageType::Hyperedge));
        assert_eq!(PageType::from_u16(0xFF), Some(PageType::Overflow));
        assert_eq!(PageType::from_u16(0x99), None);
    }

    #[test]
    fn test_encode_decode_page_ref() {
        let page_id = 12345;
        let slot_index = 42;

        let encoded = encode_page_ref(page_id, slot_index);
        let (decoded_page_id, decoded_slot_index) = decode_page_ref(encoded);

        assert_eq!(decoded_page_id, page_id);
        assert_eq!(decoded_slot_index, slot_index);
    }

    #[test]
    fn test_page_ref_edge_cases() {
        // Max values
        let encoded = encode_page_ref(0xFFFFFFFF, 0xFFFF);
        let (page_id, slot_index) = decode_page_ref(encoded);
        assert_eq!(page_id, 0xFFFFFFFF);
        assert_eq!(slot_index, 0xFFFF);

        // Zero values
        let encoded = encode_page_ref(0, 0);
        let (page_id, slot_index) = decode_page_ref(encoded);
        assert_eq!(page_id, 0);
        assert_eq!(slot_index, 0);
    }

    #[test]
    fn test_read_write_page_data() {
        use std::fs::File;
        use std::io::{Seek, SeekFrom, Write};
        use tempfile::NamedTempFile;

        let temp_file = NamedTempFile::new().unwrap();
        let path = temp_file.path();

        // Create file with 2 pages
        let mut file = File::options()
            .read(true)
            .write(true)
            .create(true)
            .truncate(true)
            .open(path)
            .unwrap();

        file.set_len((PAGE_SIZE * 2) as u64).unwrap();

        // Initialize first page with header
        let header = PageHeader::new(0, PageType::ContextNode, 1, 0xFFFFFFFF);
        let header_bytes = header.to_bytes();
        file.write_all(&header_bytes).unwrap();

        // Write some data to page 0
        let data = b"Hello, MemHop!";
        file.seek(SeekFrom::Start(32)).unwrap();
        file.write_all(data).unwrap();
        file.flush().unwrap();

        // Map and read back
        unsafe {
            let mmap = Mmap::map(&file).unwrap();
            let read_data = read_page_data(&mmap, 0).unwrap();
            assert_eq!(&read_data[..data.len()], data);
        }
    }
}
