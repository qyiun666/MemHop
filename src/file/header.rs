// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

use crate::util::{MAGIC, PAGE_SIZE, SENTINEL_PAGE_ID, TAIL_MAGIC, VERSION};
use crate::{MemHopError, Result};
use memmap2::Mmap;
use std::fs::File;
use std::io::{Seek, SeekFrom, Write};

/// Index into `FileHeader.layer_roots` for the B-tree root page.
pub const LAYER_ROOT_BTREE: usize = 0;
/// Index into `FileHeader.layer_roots` for the sparse index directory page.
pub const LAYER_ROOT_SPARSE: usize = 1;
/// Index into `FileHeader.layer_roots` for the L6 pathway weight chain.
pub const LAYER_ROOT_L6: usize = 2;
/// Index into `FileHeader.layer_roots` for the L1 inverted/reverse index chain.
pub const LAYER_ROOT_L1_INVERTED: usize = 12;
/// Index into `FileHeader.layer_roots` for the L3 hypergraph chain.
// Referenced by tests and reserved for future L3 persistence hooks.
#[allow(dead_code)]
pub const LAYER_ROOT_L3: usize = 13;

#[derive(Debug, Clone)]
#[repr(C)]
pub struct FileHeader {
    pub magic: [u8; 4],
    pub version: u16,
    pub vector_dim: u16,
    pub commit_id: u64,
    pub page_count: u32,
    pub free_list_head: u32,
    pub layer_roots: [u32; 14], // 7 layers × 2 roots. [0]=B-tree, [1]=Sparse Index
    pub journal_start: u64,
    pub journal_len: u64,
    pub flags: u32,
    pub reserved: [u8; 3988],
    pub crc32: u32,
    pub tail_magic: [u8; 4],
}

impl FileHeader {
    pub fn new(vector_dim: u16) -> Self {
        Self {
            magic: MAGIC,
            version: VERSION,
            vector_dim,
            commit_id: 0,
            page_count: 2, // Page 0 and Page 1 are headers
            free_list_head: SENTINEL_PAGE_ID,
            layer_roots: [0; 14],
            journal_start: 0,
            journal_len: 0,
            flags: 0,
            reserved: [0; 3988],
            crc32: 0,
            tail_magic: TAIL_MAGIC,
        }
    }

    pub fn calculate_crc32(&self) -> u32 {
        let mut bytes = [0u8; PAGE_SIZE];
        self.serialize_without_crc(&mut bytes);
        crc32fast::hash(&bytes[..4088])
    }

    pub fn to_bytes(&self) -> [u8; PAGE_SIZE] {
        let mut bytes = [0u8; PAGE_SIZE];
        self.serialize_without_crc(&mut bytes);

        let crc = crc32fast::hash(&bytes[..4088]);
        bytes[4088..4092].copy_from_slice(&crc.to_le_bytes());

        bytes[4092..4096].copy_from_slice(&TAIL_MAGIC);

        bytes
    }

    pub fn from_bytes(bytes: &[u8; PAGE_SIZE]) -> Result<Self> {
        if bytes[..4] != MAGIC {
            return Err(MemHopError::InvalidMagic);
        }

        if bytes[4092..4096] != TAIL_MAGIC {
            return Err(MemHopError::InvalidMagic);
        }

        let version = u16::from_le_bytes([bytes[4], bytes[5]]);
        let vector_dim = u16::from_le_bytes([bytes[6], bytes[7]]);
        let commit_id = u64::from_le_bytes([
            bytes[8], bytes[9], bytes[10], bytes[11], bytes[12], bytes[13], bytes[14], bytes[15],
        ]);
        let page_count = u32::from_le_bytes([bytes[16], bytes[17], bytes[18], bytes[19]]);
        let free_list_head = u32::from_le_bytes([bytes[20], bytes[21], bytes[22], bytes[23]]);

        let mut layer_roots = [0u32; 14];
        for (i, item) in layer_roots.iter_mut().enumerate() {
            let offset = 24 + i * 4;
            *item = u32::from_le_bytes([
                bytes[offset],
                bytes[offset + 1],
                bytes[offset + 2],
                bytes[offset + 3],
            ]);
        }

        let journal_start = u64::from_le_bytes([
            bytes[80], bytes[81], bytes[82], bytes[83], bytes[84], bytes[85], bytes[86], bytes[87],
        ]);
        let journal_len = u64::from_le_bytes([
            bytes[88], bytes[89], bytes[90], bytes[91], bytes[92], bytes[93], bytes[94], bytes[95],
        ]);
        let flags = u32::from_le_bytes([bytes[96], bytes[97], bytes[98], bytes[99]]);

        let mut reserved = [0u8; 3988];
        reserved.copy_from_slice(&bytes[100..4088]);

        let crc32 = u32::from_le_bytes([bytes[4088], bytes[4089], bytes[4090], bytes[4091]]);

        let calculated_crc = crc32fast::hash(&bytes[..4088]);
        if crc32 != calculated_crc {
            return Err(MemHopError::CrcMismatch);
        }

        Ok(Self {
            magic: MAGIC,
            version,
            vector_dim,
            commit_id,
            page_count,
            free_list_head,
            layer_roots,
            journal_start,
            journal_len,
            flags,
            reserved,
            crc32,
            tail_magic: TAIL_MAGIC,
        })
    }

    fn serialize_without_crc(&self, bytes: &mut [u8; PAGE_SIZE]) {
        bytes[..4].copy_from_slice(&MAGIC);
        bytes[4..6].copy_from_slice(&self.version.to_le_bytes());
        bytes[6..8].copy_from_slice(&self.vector_dim.to_le_bytes());
        bytes[8..16].copy_from_slice(&self.commit_id.to_le_bytes());
        bytes[16..20].copy_from_slice(&self.page_count.to_le_bytes());
        bytes[20..24].copy_from_slice(&self.free_list_head.to_le_bytes());

        for i in 0..14 {
            let offset = 24 + i * 4;
            bytes[offset..offset + 4].copy_from_slice(&self.layer_roots[i].to_le_bytes());
        }

        bytes[80..88].copy_from_slice(&self.journal_start.to_le_bytes());
        bytes[88..96].copy_from_slice(&self.journal_len.to_le_bytes());
        bytes[96..100].copy_from_slice(&self.flags.to_le_bytes());
        bytes[100..4088].copy_from_slice(&self.reserved);

        // CRC32 placeholder, filled by caller
        bytes[4088..4092].copy_from_slice(&0u32.to_le_bytes());
        bytes[4092..4096].copy_from_slice(&TAIL_MAGIC);
    }
}

pub fn read_headers(mmap: &Mmap) -> Result<(FileHeader, FileHeader)> {
    if mmap.len() < PAGE_SIZE * 2 {
        return Err(MemHopError::Io(std::io::Error::new(
            std::io::ErrorKind::UnexpectedEof,
            "File too small for dual headers",
        )));
    }

    let header_a_bytes: [u8; PAGE_SIZE] = mmap[..PAGE_SIZE]
        .try_into()
        .map_err(|_| MemHopError::Serialization("Failed to read header A".to_string()))?;

    let header_b_bytes: [u8; PAGE_SIZE] = mmap[PAGE_SIZE..PAGE_SIZE * 2]
        .try_into()
        .map_err(|_| MemHopError::Serialization("Failed to read header B".to_string()))?;

    let header_a = FileHeader::from_bytes(&header_a_bytes)?;
    let header_b = FileHeader::from_bytes(&header_b_bytes)?;

    Ok((header_a, header_b))
}

pub fn select_valid_header(a: &FileHeader, b: &FileHeader) -> Result<FileHeader> {
    let a_valid = a.magic == MAGIC && a.tail_magic == TAIL_MAGIC && a.crc32 == a.calculate_crc32();
    let b_valid = b.magic == MAGIC && b.tail_magic == TAIL_MAGIC && b.crc32 == b.calculate_crc32();

    match (a_valid, b_valid) {
        (true, true) => {
            // Both valid → higher commit_id wins
            if a.commit_id >= b.commit_id {
                Ok(a.clone())
            } else {
                Ok(b.clone())
            }
        }
        (true, false) => Ok(a.clone()),
        (false, true) => Ok(b.clone()),
        (false, false) => Err(MemHopError::CrcMismatch),
    }
}

// Used by header tests; reserved for future dual-header persistence path.
#[allow(dead_code)]
pub fn write_active_header(file: &mut File, header: &FileHeader, is_a: bool) -> Result<()> {
    let offset = if is_a { 0 } else { PAGE_SIZE as u64 };
    file.seek(SeekFrom::Start(offset))?;

    let bytes = header.to_bytes();
    file.write_all(&bytes)?;
    file.flush()?;

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::NamedTempFile;

    #[test]
    fn test_header_serialization_roundtrip() {
        let mut header = FileHeader::new(768);
        header.commit_id = 42;
        header.page_count = 100;
        header.layer_roots[LAYER_ROOT_BTREE] = 5;
        header.layer_roots[LAYER_ROOT_L3] = 99;

        let bytes = header.to_bytes();
        let restored = FileHeader::from_bytes(&bytes).unwrap();

        assert_eq!(restored.magic, MAGIC);
        assert_eq!(restored.version, VERSION);
        assert_eq!(restored.vector_dim, 768);
        assert_eq!(restored.commit_id, 42);
        assert_eq!(restored.page_count, 100);
        assert_eq!(restored.layer_roots[LAYER_ROOT_BTREE], 5);
        assert_eq!(restored.layer_roots[LAYER_ROOT_L3], 99);
        assert_eq!(restored.tail_magic, TAIL_MAGIC);
    }

    #[test]
    fn test_header_crc_validation() {
        let header = FileHeader::new(512);
        let mut bytes = header.to_bytes();

        bytes[100] ^= 0xFF;

        assert!(FileHeader::from_bytes(&bytes).is_err());
    }

    #[test]
    fn test_header_magic_validation() {
        let header = FileHeader::new(768);
        let mut bytes = header.to_bytes();

        bytes[0] = 0x00;

        assert!(FileHeader::from_bytes(&bytes).is_err());
    }

    #[test]
    fn test_select_valid_header() {
        let mut header_a = FileHeader::new(768);
        header_a.commit_id = 10;
        header_a.crc32 = header_a.calculate_crc32();

        let mut header_b = FileHeader::new(768);
        header_b.commit_id = 20;
        header_b.crc32 = header_b.calculate_crc32();

        // Both valid, B has higher commit_id
        let selected = select_valid_header(&header_a, &header_b).unwrap();
        assert_eq!(selected.commit_id, 20);

        let mut corrupt_a = header_a.clone();
        corrupt_a.crc32 = 0;

        let selected = select_valid_header(&corrupt_a, &header_b).unwrap();
        assert_eq!(selected.commit_id, 20);
    }

    #[test]
    fn test_write_read_header() {
        let temp_file = NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = File::options()
            .read(true)
            .write(true)
            .create(true)
            .truncate(true)
            .open(path)
            .unwrap();

        file.set_len((PAGE_SIZE * 2) as u64).unwrap();

        let mut header_a = FileHeader::new(768);
        header_a.commit_id = 100;

        let mut header_b = FileHeader::new(768);
        header_b.commit_id = 50;

        write_active_header(&mut file, &header_a, true).unwrap();
        write_active_header(&mut file, &header_b, false).unwrap();

        file.seek(SeekFrom::Start(0)).unwrap();
        let mmap = unsafe { Mmap::map(&file).unwrap() };
        let (read_a, read_b) = read_headers(&mmap).unwrap();

        assert_eq!(read_a.commit_id, 100);
        assert_eq!(read_a.vector_dim, 768);
        assert_eq!(read_b.commit_id, 50);

        let selected = select_valid_header(&read_a, &read_b).unwrap();
        assert_eq!(selected.commit_id, 100); // A has higher commit_id
    }
}
