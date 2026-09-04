// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/binary"

	"github.com/qyiun666/MemHop/internal/common"
)

// ReclaimMinSnapshots: below this snapshot count, CheckpointReclaim returns without reclaiming.
const ReclaimMinSnapshots = 10

// CheckpointReclaim deletes all old snapshots and rewrites only the latest
// (truncate to the data end), keeping the file as [records][single snapshot].
// Requires a tail snapshot; legacy layouts return ErrInvalidQuery (use Compact).
func (e *StorageEngine) CheckpointReclaim(snap *IndexSnapshotData) (*IndexSnapshotData, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, common.NewError(common.ErrClosed, "engine is closed")
	}
	if err := e.requireTailSnapshot(); err != nil {
		return nil, err
	}
	if e.countTailSnapshots() < ReclaimMinSnapshots {
		return snap, nil
	}
	blob, err := BuildSnapshot(e.index, snap)
	if err != nil {
		return nil, err
	}
	// 1. Truncate to the data end, dropping all old snapshots while keeping record frames.
	if err := e.truncateTail(int64(e.nextOffset)); err != nil {
		return nil, err
	}
	// 2. Clear snapshot pointers so a crash in the truncation window triggers a full scan, not an out-of-bounds read.
	if err := e.writeNullSnapshotHeader(); err != nil {
		return nil, err
	}
	// 3. Re-append the single snapshot behind the record area and flip the header.
	if err := e.appendSnapshot(blob); err != nil {
		return nil, err
	}
	e.snapshotData = snap
	return snap, nil
}

// requireTailSnapshot rejects the legacy layout where the snapshot is not
// the file tail (only Compact may rewrite such files). Caller must hold e.mu.
func (e *StorageEngine) requireTailSnapshot() error {
	h := e.activeHeaderRef()
	if h.SnapshotOffset > 0 &&
		uint64(h.SnapshotOffset)+uint64(h.SnapshotLength) != uint64(len(e.mmap)) {
		return common.NewError(common.ErrInvalidQuery,
			"snapshot not at file tail; run Compact instead")
	}
	return nil
}

// countTailSnapshots counts consecutive snapshot blobs after nextOffset.
// A parse failure on the first non-blob is "end of consecutive snapshots",
// not an error; hence no error return. Caller must hold e.mu.
func (e *StorageEngine) countTailSnapshots() int {
	count := 0
	pos := e.nextOffset
	for pos < uint64(len(e.mmap)) {
		n, err := snapshotBlobLength(e.mmap[pos:])
		if err != nil {
			return count
		}
		pos += uint64(n)
		count++
	}
	return count
}

// isSnapshotBlobAt reports whether raw starts with a snapshot magic and is
// long enough for snapshotBlobLength to inspect it.
func isSnapshotBlobAt(raw []byte) bool {
	return len(raw) >= 13 && binary.LittleEndian.Uint32(raw[0:4]) == SnapshotMagic
}

// snapshotBlobLength parses a snapshot blob's total length (0x02 per-agent
// layout).
func snapshotBlobLength(raw []byte) (int, error) {
	if len(raw) < 13 {
		return 0, common.NewError(common.ErrCorruption, "snapshot too short")
	}
	if binary.LittleEndian.Uint32(raw[0:4]) != SnapshotMagic || raw[4] != SnapshotVersion {
		return 0, common.NewError(common.ErrCorruption, "not a snapshot blob")
	}
	agentCount := int(binary.LittleEndian.Uint32(raw[5:9]))
	pos := 9
	for range agentCount {
		if pos+12 > len(raw) {
			return 0, common.NewError(common.ErrCorruption, "snapshot agent header truncated")
		}
		count := int(binary.LittleEndian.Uint32(raw[pos+8 : pos+12]))
		pos += 12 + count*16
		if pos+4 > len(raw) {
			return 0, common.NewError(common.ErrCorruption, "snapshot blob truncated")
		}
		blen := int(binary.LittleEndian.Uint32(raw[pos : pos+4]))
		pos += 4 + blen
	}
	if pos+4 > len(raw) {
		return 0, common.NewError(common.ErrCorruption, "snapshot crc truncated")
	}
	return pos + 4, nil
}

// trimTailSnapshot truncates a trailing snapshot and clears its pointer,
// keeping the record-frames-before-snapshot invariant. Caller must hold e.mu.
func (e *StorageEngine) trimTailSnapshot() error {
	h := e.activeHeaderRef()
	if h.SnapshotOffset == 0 || h.SnapshotLength == 0 {
		return nil
	}
	if uint64(h.SnapshotOffset)+uint64(h.SnapshotLength) != uint64(len(e.mmap)) {
		return nil // snapshot not at tail (legacy layout); leave to Reclaim/Compact
	}
	// nextOffset points at the end of the record area (not at the snapshot
	// tail), so one truncate drops all snapshots before the next append.
	if err := e.truncateTail(int64(e.nextOffset)); err != nil {
		return err
	}
	if err := e.writeNullSnapshotHeader(); err != nil {
		return err
	}
	e.snapshotData = nil
	return nil
}

// writeNullSnapshotHeader writes a no-snapshot header (CommitID++, pointers
// cleared) to the inactive slot and switches. Caller must hold e.mu.
func (e *StorageEngine) writeNullSnapshotHeader() error {
	nullHdr := copyHeader(e.activeHeaderRef())
	nullHdr.CommitID++
	nullHdr.SnapshotOffset = 0
	nullHdr.SnapshotLength = 0
	nullHdr.CRC32 = nullHdr.calculateCRC()
	if err := e.writeInactiveHeader(nullHdr); err != nil {
		return err
	}
	e.switchHeader(nullHdr)
	return nil
}

// Compact creates a new file at newPath containing only live records,
// preserving each record's agent domain; the new engine serializes its own
// record index. snap carries only the per-agent opaque snapshot sections and
// must not be nil.
func (e *StorageEngine) Compact(newPath string, snap *IndexSnapshotData) error {
	if snap == nil {
		return common.NewError(common.ErrInvalidQuery, "compact requires an index snapshot")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	newEng, err := Create(newPath)
	if err != nil {
		return err
	}
	needsCleanup := true
	defer func() {
		if needsCleanup {
			UnmapFile(newEng.mmap)
			unlockFile(newEng.file)
			newEng.file.Close()
		}
	}()
	for agentID, m := range e.index {
		for idHash, offset := range m {
			rt, _, data, _, _, readErr := RecordData(e.mmap, offset)
			if readErr != nil {
				return common.NewError(common.ErrCorruption, "compact: read live record", readErr)
			}
			if _, writeErr := newEng.WriteRecord(agentID, rt, idHash, data); writeErr != nil {
				return writeErr
			}
		}
	}
	if err := newEng.Checkpoint(snap); err != nil {
		return err
	}
	// Checkpoint synced data; release fd, lock and mmap without another snapshot.
	needsCleanup = false
	UnmapFile(newEng.mmap)
	if err := unlockFile(newEng.file); err != nil {
		return err
	}
	return newEng.file.Close()
}
