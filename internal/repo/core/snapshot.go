// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"

	"github.com/qyiun666/MemHop/internal/common"
)

const SnapshotMagic uint32 = 0x534E4150

// SnapshotVersion 0x02 serializes the record index plus one opaque blob per
// agent domain. Since v1.5.0 the business layer writes that section empty —
// the topic index it used to carry went with the retrieval subsystem — but
// the layout is unchanged, so 0x02 files keep opening without a format bump.
// 0x01 (single flat index + sparse/L3 blobs) is rejected together with
// pre-0x0008 files.
const SnapshotVersion uint8 = 0x02

// IndexSnapshotData carries the per-agent opaque snapshot sections. The
// engine round-trips them at Checkpoint/Close and Open; since v1.5.0 the
// business layer has nothing to put in them and passes an empty map.
type IndexSnapshotData struct {
	BlobByAgent map[uint64][]byte // agentID → opaque section bytes
}

type indexEntry struct {
	IDHash uint64
	Offset uint64
}

// BuildSnapshot serializes the per-agent index and snapshot data into a
// single blob. Format: MAGIC(4) VERSION(1) AGENT_COUNT(4) then per agent
// AGENT_ID(8) COUNT(4) entries(16 each) blob(len+data), CRC32(4).
func BuildSnapshot(index map[uint64]map[uint64]uint64, snap *IndexSnapshotData) ([]byte, error) {
	if snap == nil {
		snap = &IndexSnapshotData{}
	}
	agents := make([]uint64, 0, len(index))
	total := 0
	for agentID, m := range index {
		if len(m) == 0 {
			continue // empty domain: nothing to persist
		}
		agents = append(agents, agentID)
		total += len(m)
	}
	// Estimate capacity: header + per-agent framing + entries + blobs.
	capacity := 9 + len(agents)*(8+4) + total*16 + len(agents)*4 + 4
	for _, blob := range snap.BlobByAgent {
		capacity += len(blob)
	}
	buf := make([]byte, 0, capacity)
	buf = appendU32LE(buf, SnapshotMagic)
	buf = append(buf, SnapshotVersion)
	buf = appendU32LE(buf, uint32(len(agents)))
	for _, agentID := range agents {
		m := index[agentID]
		buf = appendU64LE(buf, agentID)
		buf = appendU32LE(buf, uint32(len(m)))
		for id, off := range m {
			buf = appendU64LE(buf, id)
			buf = appendU64LE(buf, off)
		}
		buf = appendBlob(buf, snap.BlobByAgent[agentID])
	}
	crc := crc32.ChecksumIEEE(buf)
	buf = appendU32LE(buf, crc)
	return buf, nil
}

// ParseSnapshot restores the per-agent record index and snapshot blobs.
func ParseSnapshot(raw []byte) (map[uint64]map[uint64]uint64, *IndexSnapshotData, error) {
	if err := checkSnapshotEnvelope(raw); err != nil {
		return nil, nil, err
	}
	agentCount := int(binary.LittleEndian.Uint32(raw[5:9]))
	pos := 9
	idx := make(map[uint64]map[uint64]uint64, agentCount)
	snap := &IndexSnapshotData{BlobByAgent: make(map[uint64][]byte, agentCount)}
	for range agentCount {
		agentID, m, blob, next, err := parseSnapshotAgent(raw, pos)
		if err != nil {
			return nil, nil, err
		}
		idx[agentID] = m
		if len(blob) > 0 {
			snap.BlobByAgent[agentID] = blob
		}
		pos = next
	}
	return idx, snap, nil
}

// checkSnapshotEnvelope validates length, CRC, magic and version of a
// snapshot blob (CRC covers everything but its own trailing 4 bytes).
func checkSnapshotEnvelope(raw []byte) error {
	if len(raw) < 13 { // magic(4) + version(1) + agent_count(4) + crc(4) minimum
		return common.NewError(common.ErrCorruption, "snapshot too short")
	}
	storedCRC := binary.LittleEndian.Uint32(raw[len(raw)-4:])
	if crc32.ChecksumIEEE(raw[:len(raw)-4]) != storedCRC {
		return common.NewError(common.ErrCRCMismatch, "crc32 mismatch")
	}
	magic := binary.LittleEndian.Uint32(raw[0:4])
	if magic != SnapshotMagic {
		return common.NewError(common.ErrCorruption, "invalid snapshot magic")
	}
	if raw[4] != SnapshotVersion {
		return common.NewError(common.ErrCorruption,
			fmt.Sprintf("unsupported snapshot version 0x%02x (expected 0x%02x)", raw[4], SnapshotVersion))
	}
	return nil
}

// parseSnapshotAgent reads one agent section (id header, offset entries,
// opaque blob) starting at pos; next is the offset after the section.
func parseSnapshotAgent(raw []byte, pos int) (agentID uint64, m map[uint64]uint64, blob []byte, next int, err error) {
	if pos+12 > len(raw)-4 {
		return 0, nil, nil, 0, common.NewError(common.ErrCorruption, "snapshot agent header truncated")
	}
	agentID = binary.LittleEndian.Uint64(raw[pos : pos+8])
	count := int(binary.LittleEndian.Uint32(raw[pos+8 : pos+12]))
	pos += 12
	if pos+count*16 > len(raw)-4 {
		return 0, nil, nil, 0, common.NewError(common.ErrCorruption, "snapshot entries truncated")
	}
	m = make(map[uint64]uint64, count)
	for range count {
		idHash := binary.LittleEndian.Uint64(raw[pos : pos+8])
		offset := binary.LittleEndian.Uint64(raw[pos+8 : pos+16])
		m[idHash] = offset
		pos += 16
	}
	blob, pos, err = readBlob(raw, pos)
	if err != nil {
		return 0, nil, nil, 0, err
	}
	return agentID, m, blob, pos, nil
}

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
		return nil, 0, common.NewError(common.ErrCorruption, "snapshot blob length truncated")
	}
	blen := int(binary.LittleEndian.Uint32(raw[pos : pos+4]))
	pos += 4
	if pos+blen > len(raw)-4 {
		return nil, 0, common.NewError(common.ErrCorruption, "snapshot blob data truncated")
	}
	data := make([]byte, blen)
	copy(data, raw[pos:pos+blen])
	return data, pos + blen, nil
}
