#!/usr/bin/env python3
"""Remove L6 from storage/engine.rs."""

path = "/Volumes/zt_hd/projects/meow/memhop/src/storage/engine.rs"
with open(path, "r") as f:
    content = f.read()

# 1. Remove l6_pathway_data from IndexSnapshotData
content = content.replace(
    """pub struct IndexSnapshotData {
    pub sparse_data: Vec<u8>,
    pub ivf_data: Vec<u8>,
    pub l1_reverse_data: Vec<u8>,
    pub l3_index_data: Vec<u8>,
    pub l6_pathway_data: Vec<u8>,
}""",
    """pub struct IndexSnapshotData {
    pub sparse_data: Vec<u8>,
    pub ivf_data: Vec<u8>,
    pub l1_reverse_data: Vec<u8>,
    pub l3_index_data: Vec<u8>,
}"""
)

# 2. Remove l6_pathway_data from build_snapshot
content = content.replace(
    """        buf.extend_from_slice(&(index_data.l3_index_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.l3_index_data);
        buf.extend_from_slice(&(index_data.l6_pathway_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.l6_pathway_data);
        let crc = crc32fast::hash(&buf);""",
    """        buf.extend_from_slice(&(index_data.l3_index_data.len() as u32).to_le_bytes());
        buf.extend_from_slice(&index_data.l3_index_data);
        let crc = crc32fast::hash(&buf);"""
)

# 3. Replace l6_pathway_data parse in load_snapshot with backward compat skip
content = content.replace(
    """        let sparse_data = parse_field(snap, &mut pos, "sparse_data")?;
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
        });""",
    """        let sparse_data = parse_field(snap, &mut pos, "sparse_data")?;
        let ivf_data = parse_field(snap, &mut pos, "ivf_data")?;
        let l1_reverse_data = parse_field(snap, &mut pos, "l1_reverse_data")?;
        let l3_index_data = parse_field(snap, &mut pos, "l3_index_data")?;

        // Backward compat: old snapshots may have l6_pathway_data field; skip if present
        if pos + 4 <= snap.len() {
            let field_len = u32::from_le_bytes([snap[pos], snap[pos + 1], snap[pos + 2], snap[pos + 3]]) as usize;
            if pos + 4 + field_len <= snap.len() {
                pos += 4 + field_len;
            }
        }

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
        });"""
)

# 4. Fix test snapshot construction
content = content.replace(
    """            let snapshot = IndexSnapshotData {
                sparse_data: b"sparse".to_vec(),
                ivf_data: b"ivf".to_vec(),
                l1_reverse_data: b"l1".to_vec(),
                l3_index_data: b"l3".to_vec(),
                l6_pathway_data: b"l6".to_vec(),
            };""",
    """            let snapshot = IndexSnapshotData {
                sparse_data: b"sparse".to_vec(),
                ivf_data: b"ivf".to_vec(),
                l1_reverse_data: b"l1".to_vec(),
                l3_index_data: b"l3".to_vec(),
            };"""
)

with open(path, "w") as f:
    f.write(content)

# Verify
with open(path, "r") as f:
    c = f.read()
remaining = [l.strip() for l in c.split("\n") if "l6_pathway" in l.lower()]
if remaining:
    print("STILL HAS L6:")
    for r in remaining:
        print("  ", r)
else:
    print("OK: engine.rs L6 fully removed")
