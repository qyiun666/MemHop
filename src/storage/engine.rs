// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! V2 append-only storage engine with A/B dual headers and index snapshots.

use crate::storage::record::{encode_record, record_data, RECORD_HEADER_SIZE};
use crate::{MemHopError, Result};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fs::File;
use std::io::{Seek, SeekFrom, Write};
use std::path::Path;

// A/B dual header constants
const HEADER_SIZE: usize = 4096;
const HEADER_A_OFFSET: u64 = 0;
const HEADER_B_OFFSET: u64 = 4096;
const DATA_START: u64 = 8192;
const MAGIC: &[u8; 4] = b"MEH2";
const TAIL_MAGIC: &[u8; 4] = b"2HEM";
const SNAPSHOT_MAGIC: u32 = 0x534E_4150; // "SNAP"

/// On-disk file header for v2 storage engine.
#[derive(Debug, Clone)]
pub struct FileHeader {
    pub magic: [u8; 4],
    pub version: u16,
    pub vector_dim: u16,
    pub commit_id: u64,
    pub snapshot_offset: u64,
    pub snapshot_length: u32,
    pub record_count: u32,
    pub flags: u32,
    pub crc32: u32,
    pub tail_magic: [u8; 4],
}

impl FileHeader {
    pub fn new(vector_dim: u16) -> Self {
        Self {
            magic: *MAGIC,
            version: 0x0002,
            vector_dim,
            commit_id: 0,
            snapshot_offset: 0,
            snapshot_length: 0,
            record_count: 0,
            flags: 0,
            crc32: 0,
            tail_magic: *TAIL_MAGIC,
        }
    }

    fn calculate_crc32(&self) -> u32 {
        let mut buf = [0u8; HEADER_SIZE];
        buf[0..4].copy_from_slice(&self.magic);
        buf[4..6].copy_from_slice(&self.version.to_le_bytes());
        buf[6..8].copy_from_slice(&self.vector_dim.to_le_bytes());
        buf[8..16].copy_from_slice(&self.commit_id.to_le_bytes());
        buf[16..24].copy_from_slice(&self.snapshot_offset.to_le_bytes());
        buf[24..28].copy_from_slice(&self.snapshot_length.to_le_bytes());
        buf[28..32].copy_from_slice(&self.record_count.to_le_bytes());
        buf[32..36].copy_from_slice(&self.flags.to_le_bytes());
        // bytes 36..4088 reserved (zeroed)
        crc32fast::hash(&buf[..4088])
    }

    fn to_bytes(&self) -> [u8; HEADER_SIZE] {
        let mut buf = [0u8; HEADER_SIZE];
        buf[0..4].copy_from_slice(&self.magic);
        buf[4..6].copy_from_slice(&self.version.to_le_bytes());
        buf[6..8].copy_from_slice(&self.vector_dim.to_le_bytes());
        buf[8..16].copy_from_slice(&self.commit_id.to_le_bytes());
        buf[16..24].copy_from_slice(&self.snapshot_offset.to_le_bytes());
        buf[24..28].copy_from_slice(&self.snapshot_length.to_le_bytes());
        buf[28..32].copy_from_slice(&self.record_count.to_le_bytes());
        buf[32..36].copy_from_slice(&self.flags.to_le_bytes());
        // bytes 36..4088 reserved (zeroed)
        let crc = crc32fast::hash(&buf[..4088]);
        buf[4088..4092].copy_from_slice(&crc.to_le_bytes());
        buf[4092..4096].copy_from_slice(&self.tail_magic);
        buf
    }

    fn from_bytes(bytes: &[u8; HEADER_SIZE]) -> Result<Self> {
        if bytes[0..4] != *MAGIC {
            return Err(MemHopError::InvalidMagic);
        }
        if bytes[4092..4096] != *TAIL_MAGIC {
            return Err(MemHopError::InvalidMagic);
        }
        let version = u16::from_le_bytes([bytes[4], bytes[5]]);
        let vector_dim = u16::from_le_bytes([bytes[6], bytes[7]]);
        let commit_id = u64::from_le_bytes([
            bytes[8], bytes[9], bytes[10], bytes[11], bytes[12], bytes[13], bytes[14], bytes[15],
        ]);
        let snapshot_offset = u64::from_le_bytes([
            bytes[16], bytes[17], bytes[18], bytes[19], bytes[20], bytes[21], bytes[22], bytes[23],
        ]);
        let snapshot_length = u32::from_le_bytes([bytes[24], bytes[25], bytes[26], bytes[27]]);
        let record_count = u32::from_le_bytes([bytes[28], bytes[29], bytes[30], bytes[31]]);
        let flags = u32::from_le_bytes([bytes[32], bytes[33], bytes[34], bytes[35]]);
        let crc32 = u32::from_le_bytes([bytes[4088], bytes[4089], bytes[4090], bytes[4091]]);
        let calculated = crc32fast::hash(&bytes[..4088]);
        if crc32 != calculated {
            return Err(MemHopError::CrcMismatch);
        }
        Ok(Self {
            magic: *MAGIC,
            version,
            vector_dim,
            commit_id,
            snapshot_offset,
            snapshot_length,
            record_count,
            flags,
            crc32,
            tail_magic: *TAIL_MAGIC,
        })
    }
}

/// Index entry for snapshot serialization.
#[derive(Serialize, Deserialize, Debug, Clone, Copy)]
struct IndexEntry {
    id_hash: u64,
    offset: u64,
}

/// Snapshot data passed to checkpoint/close.
#[derive(Debug, Clone, Default)]
pub struct IndexSnapshotData {
    pub sparse_data: Vec<u8>,
    pub ivf_data: Vec<u8>,
    pub l1_reverse_data: Vec<u8>,
    pub l3_index_data: Vec<u8>,
    pub l6_pathway_data: Vec<u8>,
}

/// V2 append-only storage engine.
pub struct StorageEngine {
    file: File,
    mmap: memmap2::MmapMut,
    header_a: FileHeader,
    header_b: FileHeader,
    active_header: u8,        // 0 = A, 1 = B
    index: HashMap<u64, u64>, // id_hash -> file_offset
    record_count: u32,
    next_offset: u64,
    snapshot_data: Option<IndexSnapshotData>,
}

impl StorageEngine {
    /// Create a new empty `.meh` file.
    pub fn create(path: &Path, vector_dim: u16) -> Result<Self> {
        let mut file = File::options()
            .read(true)
            .write(true)
            .create(true)
            .truncate(true)
            .open(path)?;
        file.set_len(DATA_START)?;
        file.flush()?;

        let header = FileHeader::new(vector_dim);
        let bytes = header.to_bytes();
        file.seek(SeekFrom::Start(0))?;
        file.write_all(&bytes)?;
        file.seek(SeekFrom::Start(HEADER_B_OFFSET))?;
        file.write_all(&bytes)?;
        file.flush()?;

        let mmap = unsafe { memmap2::MmapMut::map_mut(&file)? };

        Ok(Self {
            file,
            mmap,
            header_a: header.clone(),
            header_b: header.clone(),
            active_header: 0,
            index: HashMap::new(),
            record_count: 0,
            next_offset: DATA_START,
            snapshot_data: None,
        })
    }

    /// Open an existing `.meh` file.
    pub fn open(path: &Path) -> Result<Self> {
        let file = File::options().read(true).write(true).open(path)?;

        let mmap = unsafe { memmap2::MmapMut::map_mut(&file)? };
        if mmap.len() < HEADER_SIZE * 2 {
            return Err(MemHopError::Io(std::io::Error::new(
                std::io::ErrorKind::UnexpectedEof,
                "file too small for dual headers",
            )));
        }

        let header_a = FileHeader::from_bytes(
            mmap[..HEADER_SIZE]
                .try_into()
                .map_err(|_| MemHopError::Serialization("header A".to_string()))?,
        )?;
        let header_b = FileHeader::from_bytes(
            mmap[HEADER_SIZE..HEADER_SIZE * 2]
                .try_into()
                .map_err(|_| MemHopError::Serialization("header B".to_string()))?,
        )?;

        let active = select_valid_header(&header_a, &header_b)?;
        let active_header = if active.commit_id == header_a.commit_id {
            0
        } else {
            1
        };

        let mut engine = Self {
            file,
            mmap,
            header_a,
            header_b,
            active_header,
            index: HashMap::new(),
            record_count: active.record_count,
            next_offset: DATA_START,
            snapshot_data: None,
        };

        // If a snapshot exists, load it; otherwise scan records.
        if active.snapshot_offset > 0 && active.snapshot_length > 0 {
            engine.load_snapshot()?;
        } else {
            engine.scan_records()?;
        }

        Ok(engine)
    }

    /// Write a single record (thin wrapper around write_record_batch).
    pub fn write_record(&mut self, record_type: u8, id_hash: u64, data: &[u8]) -> Result<u64> {
        let offsets = self.write_record_batch(&[(record_type, id_hash, data)])?;
        Ok(offsets[0])
    }

    /// Write multiple records in batch (flush + remap only once).
    pub fn write_record_batch(&mut self, records: &[(u8, u64, &[u8])]) -> Result<Vec<u64>> {
        if records.is_empty() {
            return Ok(Vec::new());
        }
        let mut offsets = Vec::with_capacity(records.len());
        for &(record_type, id_hash, data) in records {
            let record = encode_record(record_type, 0, id_hash, data);
            let offset = self.file.seek(SeekFrom::End(0))?;
            self.file.write_all(&record)?;
            self.index.insert(id_hash, offset);
            self.next_offset = offset + record.len() as u64;
            self.record_count += 1;
            offsets.push(offset);
        }
        self.flush_and_remap()?;
        Ok(offsets)
    }

    /// Flush file and remap mmap.
    pub fn flush_and_remap(&mut self) -> Result<()> {
        self.file.flush()?;
        self.remap()?;
        Ok(())
    }

    /// Read a record by id_hash (zero-copy via mmap).
    pub fn read_record(&self, id_hash: u64) -> Result<Option<(u8, &[u8])>> {
        let Some(&offset) = self.index.get(&id_hash) else {
            return Ok(None);
        };
        let (rt, _flags, data, _id_hash) = match record_data(&self.mmap, offset)? {
            Some(v) => v,
            None => return Ok(None),
        };
        Ok(Some((rt, data)))
    }

    /// Delete a record from the index (mark as garbage in file).
    pub fn delete_record(&mut self, id_hash: u64) -> Result<bool> {
        if self.index.remove(&id_hash).is_some() {
            self.record_count = self.record_count.saturating_sub(1);
            Ok(true)
        } else {
            Ok(false)
        }
    }

    /// Check if an id_hash exists.
    pub fn contains(&self, id_hash: u64) -> bool {
        self.index.contains_key(&id_hash)
    }

    /// Iterate over all (id_hash, offset) pairs (unordered).
    pub fn iter_index(&self) -> impl Iterator<Item = (&u64, &u64)> {
        self.index.iter()
    }

    /// Iterate over all (id_hash, offset) pairs sorted by id_hash.
    pub fn iter_sorted(&self) -> Vec<(u64, u64)> {
        let mut entries: Vec<(u64, u64)> = self.index.iter().map(|(&k, &v)| (k, v)).collect();
        entries.sort_by_key(|(k, _)| *k);
        entries
    }

    /// Returns the last loaded snapshot data, if any.
    pub fn snapshot_data(&self) -> Option<&IndexSnapshotData> {
        self.snapshot_data.as_ref()
    }

    /// Checkpoint: write index snapshot + update A/B dual header.
    pub fn checkpoint(&mut self, index_data: &IndexSnapshotData) -> Result<()> {
        let snapshot = self.build_snapshot(index_data)?;
        let snap_offset = self.file.seek(SeekFrom::End(0))?;
        self.file.write_all(&snapshot)?;
        self.file.flush()?;
        self.remap()?;

        let new_header = FileHeader {
            commit_id: self.active_commit_id() + 1,
            snapshot_offset: snap_offset,
            snapshot_length: snapshot.len() as u32,
            record_count: self.record_count,
            ..self.active_header_ref().clone()
        };

        // Write to the inactive header slot
        let is_a = self.active_header == 1;
        let offset = if is_a {
            HEADER_A_OFFSET
        } else {
            HEADER_B_OFFSET
        };
        self.file.seek(SeekFrom::Start(offset))?;
        self.file.write_all(&new_header.to_bytes())?;
        self.file.flush()?;
        self.remap()?;

        if is_a {
            self.header_a = new_header;
            self.active_header = 0;
        } else {
            self.header_b = new_header;
            self.active_header = 1;
        }
        Ok(())
    }

    /// Compact: create temp file, copy live records, rename.
    pub fn compact(&mut self, path: &Path) -> Result<()> {
        let mut new_engine = StorageEngine::create(path, self.active_header_ref().vector_dim)?;
        for (&id_hash, &offset) in &self.index {
            let (rt, data, _id_hash) = match record_data(&self.mmap, offset)? {
                Some(v) => (v.0, v.2, v.3),
                None => continue,
            };
            new_engine.write_record(rt, id_hash, data)?;
        }
        new_engine.checkpoint(&IndexSnapshotData::default())?;
        Ok(())
    }

    /// Close the engine (checkpoint + sync).
    pub fn close(mut self, index_data: &IndexSnapshotData) -> Result<()> {
        self.checkpoint(index_data)?;
        self.mmap.flush()?;
        self.file.sync_all()?;
        Ok(())
    }

    /// Number of live records.
    pub fn record_count(&self) -> u32 {
        self.record_count
    }

    /// Vector dimension configured in the engine header.
    pub fn vector_dim(&self) -> u16 {
        self.active_header_ref().vector_dim
    }

    /// Total file size in bytes.
    pub fn file_size(&self) -> u64 {
        self.mmap.len() as u64
    }

    // ------------------------------------------------------------------
    // Internal helpers
    // ------------------------------------------------------------------

    fn active_header_ref(&self) -> &FileHeader {
        if self.active_header == 0 {
            &self.header_a
        } else {
            &self.header_b
        }
    }

    fn active_commit_id(&self) -> u64 {
        self.active_header_ref().commit_id
    }

    fn remap(&mut self) -> Result<()> {
        self.mmap = unsafe { memmap2::MmapMut::map_mut(&self.file)? };
        Ok(())
    }

    fn scan_records(&mut self) -> Result<()> {
        let mut offset = DATA_START;
        while let Some((rt, flags, data, id_hash)) = record_data(&self.mmap, offset)? {
            if flags & crate::storage::record::FLAG_DELETED == 0 {
                self.index.insert(id_hash, offset);
            }
            offset += RECORD_HEADER_SIZE as u64 + data.len() as u64;
        }
        self.next_offset = offset;
        Ok(())
    }

    fn build_snapshot(&self, index_data: &IndexSnapshotData) -> Result<Vec<u8>> {
        let entries: Vec<IndexEntry> = self
            .index
            .iter()
            .map(|(&id_hash, &offset)| IndexEntry { id_hash, offset })
            .collect();

        let mut buf = Vec::new();
        buf.extend_from_slice(&SNAPSHOT_MAGIC.to_le_bytes());
        buf.extend_from_slice(&(entries.len() as u32).to_le_bytes());
        let encoded =
            bincode::serialize(&entries).map_err(|e| MemHopError::Serialization(e.to_string()))?;
        buf.extend_from_slice(&encoded);
        buf.extend_from_slice(&(index_data.sparse_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.sparse_data);
        buf.extend_from_slice(&(index_data.ivf_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.ivf_data);
        buf.extend_from_slice(&(index_data.l1_reverse_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.l1_reverse_data);
        buf.extend_from_slice(&(index_data.l3_index_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.l3_index_data);
        buf.extend_from_slice(&(index_data.l6_pathway_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.l6_pathway_data);
        let crc = crc32fast::hash(&buf);
        buf.extend_from_slice(&crc.to_le_bytes());
        Ok(buf)
    }

    fn load_snapshot(&mut self) -> Result<()> {
        let hdr = self.active_header_ref();
        let off = hdr.snapshot_offset as usize;
        let len = hdr.snapshot_length as usize;
        if off + len > self.mmap.len() {
            return Err(MemHopError::Corruption(
                "snapshot out of bounds".to_string(),
            ));
        }
        let snap = &self.mmap[off..off + len];
        if snap.len() < 8 {
            return Err(MemHopError::Corruption("snapshot too short".to_string()));
        }
        let magic = u32::from_le_bytes([snap[0], snap[1], snap[2], snap[3]]);
        if magic != SNAPSHOT_MAGIC {
            return Err(MemHopError::Corruption(
                "invalid snapshot magic".to_string(),
            ));
        }
        let count = u32::from_le_bytes([snap[4], snap[5], snap[6], snap[7]]) as usize;
        let entries: Vec<IndexEntry> = bincode::deserialize(&snap[8..])
            .map_err(|e| MemHopError::Deserialization(e.to_string()))?;
        if entries.len() != count {
            return Err(MemHopError::Corruption(
                "snapshot entry count mismatch".to_string(),
            ));
        }
        self.index.clear();
        for entry in &entries {
            self.index.insert(entry.id_hash, entry.offset);
        }
        self.record_count = count as u32;

        // Parse extended snapshot fields after entries.
        let entries_end = 8 + bincode::serialized_size(&entries).unwrap_or(0) as usize;
        let mut pos = entries_end;

        let parse_field = |snap: &[u8], pos: &mut usize, label: &str| -> Result<Vec<u8>> {
            if *pos + 4 > snap.len() {
                return Err(MemHopError::Corruption(format!(
                    "snapshot truncated before {}",
                    label
                )));
            }
            let field_len =
                u32::from_le_bytes([snap[*pos], snap[*pos + 1], snap[*pos + 2], snap[*pos + 3]])
                    as usize;
            *pos += 4;
            if *pos + field_len > snap.len() {
                return Err(MemHopError::Corruption(format!(
                    "snapshot data truncated for {}",
                    label
                )));
            }
            let data = snap[*pos..*pos + field_len].to_vec();
            *pos += field_len;
            Ok(data)
        };

        let sparse_data = parse_field(snap, &mut pos, "sparse_data")?;
        let ivf_data = parse_field(snap, &mut pos, "ivf_data")?;
        let l1_reverse_data = parse_field(snap, &mut pos, "l1_reverse_data")?;
        let l3_index_data = parse_field(snap, &mut pos, "l3_index_data")?;
        let l6_pathway_data = parse_field(snap, &mut pos, "l6_pathway_data")?;

        // Verify CRC at the end
        if pos + 4 != len {
            return Err(MemHopError::Corruption(
                "snapshot length mismatch".to_string(),
            ));
        }
        let stored_crc =
            u32::from_le_bytes([snap[len - 4], snap[len - 3], snap[len - 2], snap[len - 1]]);
        let calculated = crc32fast::hash(&snap[..len - 4]);
        if stored_crc != calculated {
            return Err(MemHopError::CrcMismatch);
        }

        self.snapshot_data = Some(IndexSnapshotData {
            sparse_data,
            ivf_data,
            l1_reverse_data,
            l3_index_data,
            l6_pathway_data,
        });
        Ok(())
    }
}

fn select_valid_header(a: &FileHeader, b: &FileHeader) -> Result<FileHeader> {
    let a_valid =
        a.magic == *MAGIC && a.tail_magic == *TAIL_MAGIC && a.crc32 == a.calculate_crc32();
    let b_valid =
        b.magic == *MAGIC && b.tail_magic == *TAIL_MAGIC && b.crc32 == b.calculate_crc32();
    match (a_valid, b_valid) {
        (true, true) => {
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

/// Buffered write/delete batch for dream operations.
pub struct DreamBuffer {
    pub pending_writes: Vec<(u8, u64, Vec<u8>)>, // (record_type, id_hash, data)
    pub pending_deletes: Vec<u64>,               // id_hash
}

impl DreamBuffer {
    pub fn new() -> Self {
        Self {
            pending_writes: Vec::new(),
            pending_deletes: Vec::new(),
        }
    }

    pub fn write(&mut self, record_type: u8, id_hash: u64, data: Vec<u8>) {
        self.pending_writes.push((record_type, id_hash, data));
    }

    pub fn delete(&mut self, id_hash: u64) {
        self.pending_deletes.push(id_hash);
    }

    pub fn commit(self, engine: &mut StorageEngine) -> Result<()> {
        for (record_type, id_hash, data) in self.pending_writes {
            engine.write_record(record_type, id_hash, &data)?;
        }
        for id_hash in self.pending_deletes {
            engine.delete_record(id_hash)?;
        }
        Ok(())
    }

    pub fn discard(self) {
        // Drops self, discarding all pending operations.
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::NamedTempFile;

    #[test]
    fn test_header_serialization_roundtrip() {
        let mut h = FileHeader::new(768);
        h.commit_id = 42;
        h.record_count = 100;
        h.snapshot_offset = 12345;
        h.snapshot_length = 678;
        let bytes = h.to_bytes();
        let restored = FileHeader::from_bytes(&bytes).unwrap();
        assert_eq!(restored.magic, *MAGIC);
        assert_eq!(restored.version, 2);
        assert_eq!(restored.vector_dim, 768);
        assert_eq!(restored.commit_id, 42);
        assert_eq!(restored.record_count, 100);
        assert_eq!(restored.snapshot_offset, 12345);
        assert_eq!(restored.snapshot_length, 678);
        assert_eq!(restored.tail_magic, *TAIL_MAGIC);
    }

    #[test]
    fn test_header_crc_validation() {
        let h = FileHeader::new(512);
        let mut bytes = h.to_bytes();
        bytes[100] ^= 0xFF;
        assert!(FileHeader::from_bytes(&bytes).is_err());
    }

    #[test]
    fn test_header_magic_validation() {
        let h = FileHeader::new(768);
        let mut bytes = h.to_bytes();
        bytes[0] = 0x00;
        assert!(FileHeader::from_bytes(&bytes).is_err());
    }

    #[test]
    fn test_select_valid_header_logic() {
        let mut a = FileHeader::new(768);
        a.commit_id = 10;
        a.crc32 = a.calculate_crc32();
        let mut b = FileHeader::new(768);
        b.commit_id = 20;
        b.crc32 = b.calculate_crc32();
        let sel = select_valid_header(&a, &b).unwrap();
        assert_eq!(sel.commit_id, 20);

        let mut corrupt_a = a.clone();
        corrupt_a.crc32 = 0;
        let sel = select_valid_header(&corrupt_a, &b).unwrap();
        assert_eq!(sel.commit_id, 20);
    }

    #[test]
    fn test_create_write_read_roundtrip() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let data = b"hello storage engine";
        let offset = engine.write_record(0x01, 12345, data).unwrap();
        assert_eq!(offset, DATA_START);

        let (rt, read) = engine.read_record(12345).unwrap().unwrap();
        assert_eq!(rt, 0x01);
        assert_eq!(read, data.as_slice());
    }

    #[test]
    fn test_write_multiple_delete_one() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        engine.write_record(0x01, 1, b"first").unwrap();
        engine.write_record(0x02, 2, b"second").unwrap();
        engine.write_record(0x03, 3, b"third").unwrap();

        assert!(engine.delete_record(2).unwrap());
        assert!(engine.read_record(2).unwrap().is_none());
        assert!(engine.read_record(1).unwrap().is_some());
        assert!(engine.read_record(3).unwrap().is_some());
        assert_eq!(engine.record_count(), 2);
    }

    #[test]
    fn test_checkpoint_and_reopen() {
        let temp = NamedTempFile::new().unwrap();
        {
            let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
            engine.write_record(0x01, 100, b"checkpoint data").unwrap();
            let snapshot = IndexSnapshotData {
                sparse_data: b"sparse".to_vec(),
                ivf_data: b"ivf".to_vec(),
                l1_reverse_data: b"l1".to_vec(),
                l3_index_data: b"l3".to_vec(),
                l6_pathway_data: b"l6".to_vec(),
            };
            engine.checkpoint(&snapshot).unwrap();
        }

        let engine = StorageEngine::open(temp.path()).unwrap();
        assert_eq!(engine.record_count(), 1);
        let (rt, data) = engine.read_record(100).unwrap().unwrap();
        assert_eq!(rt, 0x01);
        assert_eq!(data, b"checkpoint data");
    }

    #[test]
    fn test_compact_integrity() {
        let temp = NamedTempFile::new().unwrap();
        let compact_temp = NamedTempFile::new().unwrap();
        {
            let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
            engine.write_record(0x01, 1, b"keep").unwrap();
            engine.write_record(0x02, 2, b"delete").unwrap();
            engine.delete_record(2).unwrap();
            engine.compact(compact_temp.path()).unwrap();
        }

        let engine = StorageEngine::open(compact_temp.path()).unwrap();
        assert_eq!(engine.record_count(), 1);
        assert!(engine.read_record(1).unwrap().is_some());
        assert!(engine.read_record(2).unwrap().is_none());
    }

    #[test]
    fn test_file_size() {
        let temp = NamedTempFile::new().unwrap();
        let engine = StorageEngine::create(temp.path(), 768).unwrap();
        assert_eq!(engine.file_size(), DATA_START);
    }

    #[test]
    fn test_dream_buffer_commit() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let mut buf = DreamBuffer::new();
        buf.write(0x01, 100, b"hello".to_vec());
        buf.write(0x02, 200, b"world".to_vec());
        buf.delete(300);
        buf.commit(&mut engine).unwrap();

        assert!(engine.read_record(100).unwrap().is_some());
        assert!(engine.read_record(200).unwrap().is_some());
        assert!(engine.read_record(300).unwrap().is_none());
    }

    #[test]
    fn test_dream_buffer_discard() {
        let temp = NamedTempFile::new().unwrap();
        let mut engine = StorageEngine::create(temp.path(), 768).unwrap();
        let mut buf = DreamBuffer::new();
        buf.write(0x01, 100, b"hello".to_vec());
        buf.discard();

        assert!(engine.read_record(100).unwrap().is_none());
    }
}
