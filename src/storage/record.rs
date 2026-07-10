// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! V2 storage engine record format: [type:u8 | flags:u8 | len:u32 | id_hash:u64 | data]

use crate::{MemHopError, Result};

/// Record header size: type(1) + flags(1) + length(4) + id_hash(8) = 14 bytes
pub const RECORD_HEADER_SIZE: usize = 14;

// Record type constants
pub const REC_L0_PROFILE: u8 = 0x01;
pub const REC_L1_SCENE_NODE: u8 = 0x02;
pub const REC_L1_HYPEREDGE: u8 = 0x03;
pub const REC_L2_TOPIC: u8 = 0x04;
pub const REC_L2_SCENE: u8 = 0x05;
pub const REC_L3_GRAPH_NODE: u8 = 0x06;
pub const REC_L3_GRAPH_EDGE: u8 = 0x07;
pub const REC_L4_ARCHIVE: u8 = 0x08;
pub const REC_L5_ACTION_CHAIN: u8 = 0x09;
pub const REC_L6_PATHWAY: u8 = 0x0A;
pub const REC_L3_GRAPH_SLOT: u8 = 0x0B; // L3 图容器
pub const REC_L5_ACTION_STEP: u8 = 0x0C; // L5 动作步骤
                                         // 0x0D-0xEF: reserved for L7+
                                         // 0xF0-0xFF: system internal types

/// Flags bits
pub const FLAG_DELETED: u8 = 0x01;

/// Encode a record into a byte vector.
pub fn encode_record(record_type: u8, flags: u8, id_hash: u64, data: &[u8]) -> Vec<u8> {
    let mut buf = Vec::with_capacity(RECORD_HEADER_SIZE + data.len());
    buf.push(record_type);
    buf.push(flags);
    buf.extend_from_slice(&(data.len() as u32).to_le_bytes());
    buf.extend_from_slice(&id_hash.to_le_bytes());
    buf.extend_from_slice(data);
    buf
}

/// Parse a record header from a byte slice.
/// Returns `(record_type, flags, data_length, id_hash)` if the slice is large enough.
pub fn parse_record_header(buf: &[u8]) -> Option<(u8, u8, u32, u64)> {
    if buf.len() < RECORD_HEADER_SIZE {
        return None;
    }
    let record_type = buf[0];
    let flags = buf[1];
    let len = u32::from_le_bytes([buf[2], buf[3], buf[4], buf[5]]);
    let id_hash = u64::from_le_bytes([
        buf[6], buf[7], buf[8], buf[9], buf[10], buf[11], buf[12], buf[13],
    ]);
    Some((record_type, flags, len, id_hash))
}

/// Read a full record from an mmap slice at the given offset.
/// Returns `(record_type, flags, data_slice, id_hash)` with zero-copy data reference.
pub fn record_data(mmap: &[u8], offset: u64) -> Result<Option<(u8, u8, &[u8], u64)>> {
    let off = offset as usize;
    if off + RECORD_HEADER_SIZE > mmap.len() {
        return Ok(None);
    }
    let record_type = mmap[off];
    let flags = mmap[off + 1];
    let len =
        u32::from_le_bytes([mmap[off + 2], mmap[off + 3], mmap[off + 4], mmap[off + 5]]) as usize;
    let id_hash = u64::from_le_bytes([
        mmap[off + 6],
        mmap[off + 7],
        mmap[off + 8],
        mmap[off + 9],
        mmap[off + 10],
        mmap[off + 11],
        mmap[off + 12],
        mmap[off + 13],
    ]);
    let data_end = off + RECORD_HEADER_SIZE + len;
    if data_end > mmap.len() {
        return Err(MemHopError::Corruption(format!(
            "Record at offset {} claims length {} but file ends at {}",
            offset,
            len,
            mmap.len()
        )));
    }
    Ok(Some((
        record_type,
        flags,
        &mmap[off + RECORD_HEADER_SIZE..data_end],
        id_hash,
    )))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_encode_parse_roundtrip() {
        let data = b"hello world";
        let encoded = encode_record(REC_L3_GRAPH_NODE, FLAG_DELETED, 12345, data);
        assert_eq!(encoded.len(), RECORD_HEADER_SIZE + data.len());

        let (rt, flags, len, id_hash) = parse_record_header(&encoded).unwrap();
        assert_eq!(rt, REC_L3_GRAPH_NODE);
        assert_eq!(flags, FLAG_DELETED);
        assert_eq!(len, data.len() as u32);
        assert_eq!(id_hash, 12345);
    }

    #[test]
    fn test_parse_header_too_short() {
        assert!(parse_record_header(&[0x01, 0x00]).is_none());
        assert!(parse_record_header(&[]).is_none());
    }

    #[test]
    fn test_record_data_zero_copy() {
        let data = b"zero copy data";
        let encoded = encode_record(REC_L2_SCENE, 0, 999, data);
        let (rt, flags, slice, id_hash) = record_data(&encoded, 0).unwrap().unwrap();
        assert_eq!(rt, REC_L2_SCENE);
        assert_eq!(flags, 0);
        assert_eq!(slice, data.as_slice());
        assert_eq!(id_hash, 999);
    }

    #[test]
    fn test_record_data_offset() {
        let prefix = b"prefix";
        let data = b"actual data";
        let mut buf = Vec::new();
        buf.extend_from_slice(prefix);
        buf.extend_from_slice(&encode_record(REC_L4_ARCHIVE, 0, 42, data));

        let (rt, flags, slice, id_hash) = record_data(&buf, prefix.len() as u64).unwrap().unwrap();
        assert_eq!(rt, REC_L4_ARCHIVE);
        assert_eq!(flags, 0);
        assert_eq!(slice, data.as_slice());
        assert_eq!(id_hash, 42);
    }

    #[test]
    fn test_record_data_out_of_bounds() {
        assert!(record_data(b"short", 0).unwrap().is_none());
    }

    #[test]
    fn test_record_data_corruption() {
        let mut buf = encode_record(REC_L1_SCENE_NODE, 0, 0, b"");
        // Corrupt length to claim more than available
        buf[2] = 0xFF;
        buf[3] = 0xFF;
        buf[4] = 0xFF;
        buf[5] = 0x7F;
        assert!(record_data(&buf, 0).is_err());
    }
}
