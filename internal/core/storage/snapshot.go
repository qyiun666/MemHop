// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package storage

import (
	"encoding/binary"
	"hash/crc32"

	"memhop/internal/common/mherrors"
)

// SnapshotMagic identifies a valid snapshot block ("SNAP").
const SnapshotMagic uint32 = 0x534E4150

// IndexSnapshotData holds serialized index blobs persisted at checkpoint.
type IndexSnapshotData struct {
	SparseData    []byte
	L1ReverseData []byte
	L3IndexData   []byte
}

// indexEntry is a single (idHash, offset) pair in the snapshot.
type indexEntry struct {
	IDHash uint64
	Offset uint64
}

// BuildSnapshot serializes the index and snapshot data into a single blob.
//
// Format: SNAP_MAGIC(4) + COUNT(4) + entries(16 each) + 4×blob(len+data) + CRC32(4)
func BuildSnapshot(index map[uint64]uint64, snap *IndexSnapshotData) ([]byte, error) {
	entries := make([]indexEntry, 0, len(index))
	for id, off := range index {
		entries = append(entries, indexEntry{IDHash: id, Offset: off})
	}
	// Estimate capacity.
	cap := 8 + len(entries)*16 + 12 +
		len(snap.SparseData) +
		len(snap.L1ReverseData) + len(snap.L3IndexData) + 4
	buf := make([]byte, 0, cap)
	// Magic + count.
	buf = appendU32LE(buf, SnapshotMagic)
	buf = appendU32LE(buf, uint32(len(entries)))
	// Entries.
	for _, e := range entries {
		buf = appendU64LE(buf, e.IDHash)
		buf = appendU64LE(buf, e.Offset)
	}
	// Three data blobs.
	buf = appendBlob(buf, snap.SparseData)
	buf = appendBlob(buf, snap.L1ReverseData)
	buf = appendBlob(buf, snap.L3IndexData)
	// CRC32 over everything before the CRC field.
	crc := crc32.ChecksumIEEE(buf)
	buf = appendU32LE(buf, crc)
	return buf, nil
}

// ParseSnapshot deserializes a snapshot blob, returning the index and snapshot data.
func ParseSnapshot(raw []byte) (map[uint64]uint64, *IndexSnapshotData, error) {
	if len(raw) < 12 { // magic(4) + count(4) + crc(4) minimum
		return nil, nil, mherrors.NewError(mherrors.ErrCorruption, "snapshot too short")
	}
	// Verify CRC.
	storedCRC := binary.LittleEndian.Uint32(raw[len(raw)-4:])
	if crc32.ChecksumIEEE(raw[:len(raw)-4]) != storedCRC {
		return nil, nil, mherrors.ErrCRCMismatch
	}
	magic := binary.LittleEndian.Uint32(raw[0:4])
	if magic != SnapshotMagic {
		return nil, nil, mherrors.NewError(mherrors.ErrCorruption, "invalid snapshot magic")
	}
	count := int(binary.LittleEndian.Uint32(raw[4:8]))
	pos := 8
	// Parse entries.
	needed := pos + count*16
	if needed > len(raw)-4 {
		return nil, nil, mherrors.NewError(mherrors.ErrCorruption, "snapshot entries truncated")
	}
	idx := make(map[uint64]uint64, count)
	for i := 0; i < count; i++ {
		idHash := binary.LittleEndian.Uint64(raw[pos : pos+8])
		offset := binary.LittleEndian.Uint64(raw[pos+8 : pos+16])
		idx[idHash] = offset
		pos += 16
	}
	// Parse three blobs.
	var err error
	sparse, pos, err := readBlob(raw, pos)
	if err != nil {
		return nil, nil, err
	}
	l1rev, pos, err := readBlob(raw, pos)
	if err != nil {
		return nil, nil, err
	}
	l3idx, _, err := readBlob(raw, pos)
	if err != nil {
		return nil, nil, err
	}
	snap := &IndexSnapshotData{
		SparseData:    sparse,
		L1ReverseData: l1rev,
		L3IndexData:   l3idx,
	}
	return idx, snap, nil
}

// --- helpers ---

func appendU32LE(buf []byte, v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(buf, b[:]...)
}

func appendU64LE(buf []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(buf, b[:]...)
}

func appendBlob(buf []byte, data []byte) []byte {
	buf = appendU32LE(buf, uint32(len(data)))
	return append(buf, data...)
}

func readBlob(raw []byte, pos int) ([]byte, int, error) {
	if pos+4 > len(raw)-4 {
		return nil, 0, mherrors.NewError(mherrors.ErrCorruption, "snapshot blob length truncated")
	}
	blen := int(binary.LittleEndian.Uint32(raw[pos : pos+4]))
	pos += 4
	if pos+blen > len(raw)-4 {
		return nil, 0, mherrors.NewError(mherrors.ErrCorruption, "snapshot blob data truncated")
	}
	data := make([]byte, blen)
	copy(data, raw[pos:pos+blen])
	return data, pos + blen, nil
}
