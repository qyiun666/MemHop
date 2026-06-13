// Journal transaction log module
use crate::file::header::FileHeader;
use crate::util::PAGE_SIZE;
use crate::{MemHopError, Result};
use memmap2::Mmap;
use std::fs::File;
use std::io::{Seek, SeekFrom, Write};

/// Journal entry representing a transaction
#[derive(Debug, Clone)]
pub struct JournalEntry {
    pub commit_id: u64,
    pub pages: Vec<(u32, Vec<u8>)>, // (page_id, full_page_data_4096)
}

impl JournalEntry {
    /// Create a new journal entry
    pub fn new(commit_id: u64) -> Self {
        Self {
            commit_id,
            pages: Vec::new(),
        }
    }

    /// Add a page to the journal entry
    pub fn add_page(&mut self, page_id: u32, data: Vec<u8>) {
        if data.len() != PAGE_SIZE {
            panic!("Page data must be exactly {} bytes", PAGE_SIZE);
        }
        self.pages.push((page_id, data));
    }
}

/// Serialize a journal entry to bytes
/// Format: entry_size(u32) + commit_id(u64) + page_count(u8) + [page_id(u32) + data(4096)] × N + crc32(u32)
pub fn serialize_entry(entry: &JournalEntry) -> Vec<u8> {
    let page_count = entry.pages.len();

    // Calculate total size: 4 (entry_size) + 8 (commit_id) + 1 (page_count) + N * (4 + 4096) + 4 (crc32)
    let data_size = 4 + 8 + 1 + page_count * (4 + PAGE_SIZE) + 4;

    let mut buffer = Vec::with_capacity(data_size);

    // Placeholder for entry_size (will be filled at the end)
    buffer.extend_from_slice(&0u32.to_le_bytes());

    // Commit ID (8 bytes)
    buffer.extend_from_slice(&entry.commit_id.to_le_bytes());

    // Page count (1 byte)
    buffer.push(page_count as u8);

    // Pages
    for (page_id, data) in &entry.pages {
        buffer.extend_from_slice(&page_id.to_le_bytes());
        buffer.extend_from_slice(data);
    }

    // Calculate CRC32 over everything except the entry_size field and crc32 itself
    let crc = crc32fast::hash(&buffer[4..]);
    buffer.extend_from_slice(&crc.to_le_bytes());

    // Now fill in the actual entry_size (excluding the 4-byte size field itself)
    let actual_size = (buffer.len() - 4) as u32;
    buffer[0..4].copy_from_slice(&actual_size.to_le_bytes());

    buffer
}

/// Deserialize a journal entry from bytes
pub fn deserialize_entry(data: &[u8]) -> Result<JournalEntry> {
    if data.len() < 17 {
        // Minimum: 4 (size) + 8 (commit_id) + 1 (page_count) + 4 (crc)
        return Err(MemHopError::Serialization(
            "Journal entry too small".to_string(),
        ));
    }

    // Read entry_size
    let entry_size = u32::from_le_bytes([data[0], data[1], data[2], data[3]]) as usize;

    // Validate we have enough data
    if data.len() < 4 + entry_size {
        return Err(MemHopError::Serialization(
            "Incomplete journal entry".to_string(),
        ));
    }

    // Verify CRC32 (over data excluding entry_size and crc32)
    let crc_offset = 4 + entry_size - 4;
    let stored_crc = u32::from_le_bytes([
        data[crc_offset],
        data[crc_offset + 1],
        data[crc_offset + 2],
        data[crc_offset + 3],
    ]);

    let calculated_crc = crc32fast::hash(&data[4..crc_offset]);
    if stored_crc != calculated_crc {
        return Err(MemHopError::CrcMismatch);
    }

    // Read commit_id
    let commit_id = u64::from_le_bytes([
        data[4], data[5], data[6], data[7], data[8], data[9], data[10], data[11],
    ]);

    // Read page_count
    let page_count = data[12] as usize;

    // Read pages
    let mut pages = Vec::with_capacity(page_count);
    let mut offset = 13;

    for _ in 0..page_count {
        if offset + 4 + PAGE_SIZE > 4 + entry_size - 4 {
            return Err(MemHopError::Serialization(
                "Journal entry corrupted: incomplete page data".to_string(),
            ));
        }

        let page_id = u32::from_le_bytes([
            data[offset],
            data[offset + 1],
            data[offset + 2],
            data[offset + 3],
        ]);
        offset += 4;

        let mut page_data = vec![0u8; PAGE_SIZE];
        page_data.copy_from_slice(&data[offset..offset + PAGE_SIZE]);
        offset += PAGE_SIZE;

        pages.push((page_id, page_data));
    }

    Ok(JournalEntry { commit_id, pages })
}

/// Append a journal entry to the file
pub fn append_journal(file: &mut File, entry: &JournalEntry) -> Result<()> {
    let serialized = serialize_entry(entry);

    // Seek to end of file
    file.seek(SeekFrom::End(0))?;

    // Write the serialized entry
    file.write_all(&serialized)?;
    file.flush()?;

    Ok(())
}

/// Replay journal entries from file starting at journal_start position
pub fn replay_journal(mmap: &Mmap, header: &FileHeader) -> Result<Vec<JournalEntry>> {
    let mut entries = Vec::new();

    if header.journal_len == 0 {
        return Ok(entries);
    }

    let start_pos = header.journal_start as usize;
    let end_pos = start_pos + header.journal_len as usize;

    if end_pos > mmap.len() {
        return Err(MemHopError::Serialization(
            "Journal extends beyond file bounds".to_string(),
        ));
    }

    let mut offset = start_pos;

    while offset < end_pos {
        // Need at least 4 bytes to read entry_size
        if offset + 4 > mmap.len() {
            break;
        }

        // Read entry_size
        let entry_size = u32::from_le_bytes([
            mmap[offset],
            mmap[offset + 1],
            mmap[offset + 2],
            mmap[offset + 3],
        ]) as usize;

        // Check if we have the complete entry
        if offset + 4 + entry_size > end_pos {
            break;
        }

        // Try to deserialize the entry
        let entry_data = &mmap[offset..offset + 4 + entry_size];
        match deserialize_entry(entry_data) {
            Ok(entry) => {
                entries.push(entry);
                offset += 4 + entry_size;
            }
            Err(_) => {
                // Stop replaying on corruption
                break;
            }
        }
    }

    Ok(entries)
}

/// Truncate file to specified length
pub fn truncate_journal(file: &mut File, new_len: u64) -> Result<()> {
    file.set_len(new_len)?;
    file.sync_all()?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::NamedTempFile;

    #[test]
    fn test_journal_entry_serialization_roundtrip() {
        let mut entry = JournalEntry::new(12345);

        // Add some pages
        let page1_data = vec![1u8; PAGE_SIZE];
        let page2_data = vec![2u8; PAGE_SIZE];

        entry.add_page(1, page1_data.clone());
        entry.add_page(2, page2_data.clone());

        // Serialize
        let serialized = serialize_entry(&entry);

        // Deserialize
        let restored = deserialize_entry(&serialized).unwrap();

        assert_eq!(restored.commit_id, 12345);
        assert_eq!(restored.pages.len(), 2);
        assert_eq!(restored.pages[0].0, 1);
        assert_eq!(restored.pages[0].1, page1_data);
        assert_eq!(restored.pages[1].0, 2);
        assert_eq!(restored.pages[1].1, page2_data);
    }

    #[test]
    fn test_journal_entry_empty_pages() {
        let entry = JournalEntry::new(999);

        let serialized = serialize_entry(&entry);
        let restored = deserialize_entry(&serialized).unwrap();

        assert_eq!(restored.commit_id, 999);
        assert_eq!(restored.pages.len(), 0);
    }

    #[test]
    fn test_journal_entry_crc_validation() {
        let mut entry = JournalEntry::new(42);
        let page_data = vec![0xAB; PAGE_SIZE];
        entry.add_page(5, page_data);

        let mut serialized = serialize_entry(&entry);

        // Corrupt some data
        serialized[10] ^= 0xFF;

        assert!(deserialize_entry(&serialized).is_err());
    }

    #[test]
    fn test_append_and_replay_journal() {
        let temp_file = NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = File::options()
            .read(true)
            .write(true)
            .create(true)
            .truncate(true)
            .open(path)
            .unwrap();

        // Create and append journal entries
        let mut entry1 = JournalEntry::new(1);
        entry1.add_page(10, vec![10u8; PAGE_SIZE]);

        let mut entry2 = JournalEntry::new(2);
        entry2.add_page(11, vec![11u8; PAGE_SIZE]);
        entry2.add_page(12, vec![12u8; PAGE_SIZE]);

        append_journal(&mut file, &entry1).unwrap();
        append_journal(&mut file, &entry2).unwrap();

        // Create a mock header with journal info
        let header = FileHeader {
            magic: [0x4D, 0x45, 0x48, 0x21],
            version: 0x001E,
            vector_dim: 768,
            commit_id: 2,
            page_count: 20,
            free_list_head: 0xFFFFFFFF,
            layer_roots: [0; 14],
            journal_start: 0, // Journal starts at beginning for this test
            journal_len: file.metadata().unwrap().len(),
            flags: 0,
            reserved: [0; 3988],
            crc32: 0,
            tail_magic: [0xDE, 0xAD, 0xBE, 0xEF],
        };

        // Map and replay
        unsafe {
            let mmap = Mmap::map(&file).unwrap();
            let entries = replay_journal(&mmap, &header).unwrap();

            assert_eq!(entries.len(), 2);
            assert_eq!(entries[0].commit_id, 1);
            assert_eq!(entries[0].pages.len(), 1);
            assert_eq!(entries[1].commit_id, 2);
            assert_eq!(entries[1].pages.len(), 2);
        }
    }

    #[test]
    fn test_truncate_journal() {
        let temp_file = NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = File::options()
            .read(true)
            .write(true)
            .create(true)
            .truncate(true)
            .open(path)
            .unwrap();

        // Write some data
        file.write_all(&vec![0u8; 1000]).unwrap();
        file.flush().unwrap();

        // Truncate to 500 bytes
        truncate_journal(&mut file, 500).unwrap();

        assert_eq!(file.metadata().unwrap().len(), 500);
    }
}
