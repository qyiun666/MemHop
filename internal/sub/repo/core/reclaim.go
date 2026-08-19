// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/binary"
	"io"

	"github.com/qyiun666/MemHop/internal/sub/common"
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
	h := e.activeHeaderRef()
	if h.SnapshotOffset > 0 &&
		uint64(h.SnapshotOffset)+uint64(h.SnapshotLength) != uint64(len(e.mmap)) {
		return nil, common.NewError(common.ErrInvalidQuery,
			"snapshot not at file tail; run Compact instead")
	}
	n, err := e.countTailSnapshots()
	if err != nil {
		return nil, err
	}
	if n < ReclaimMinSnapshots {
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
	snapOffset, err := e.file.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, common.NewError(common.ErrIO, "seek snap", err)
	}
	if _, err := e.file.Write(blob); err != nil {
		return nil, common.NewError(common.ErrIO, "write snapshot", err)
	}
	if err := e.file.Sync(); err != nil {
		return nil, common.NewError(common.ErrIO, "sync", err)
	}
	mm, err := RemapFile(e.file, e.mmap)
	if err != nil {
		return nil, err
	}
	e.mmap = mm
	newHdr := e.buildCheckpointHeader(snapOffset, uint32(len(blob)))
	if err := e.writeInactiveHeader(newHdr); err != nil {
		return nil, err
	}
	mm, err = RemapFile(e.file, e.mmap)
	if err != nil {
		return nil, err
	}
	e.mmap = mm
	e.switchHeader(newHdr)
	e.snapshotData = snap
	return snap, nil
}

// countTailSnapshots counts consecutive snapshot blobs after nextOffset.
// Caller must hold e.mu.
func (e *StorageEngine) countTailSnapshots() (int, error) {
	count := 0
	pos := e.nextOffset
	for pos < uint64(len(e.mmap)) {
		n, err := snapshotBlobLength(e.mmap[pos:])
		if err != nil {
			return count, nil
		}
		pos += uint64(n)
		count++
	}
	return count, nil
}

// isSnapshotBlobAt reports whether raw starts with a snapshot magic and is
// long enough for snapshotBlobLength to inspect it.
func isSnapshotBlobAt(raw []byte) bool {
	return len(raw) >= 13 && binary.LittleEndian.Uint32(raw[0:4]) == SnapshotMagic
}

// snapshotBlobLength parses a snapshot blob's total length.
func snapshotBlobLength(raw []byte) (int, error) {
	if len(raw) < 13 {
		return 0, common.NewError(common.ErrCorruption, "snapshot too short")
	}
	if binary.LittleEndian.Uint32(raw[0:4]) != SnapshotMagic || raw[4] != SnapshotVersion {
		return 0, common.NewError(common.ErrCorruption, "not a snapshot blob")
	}
	count := int(binary.LittleEndian.Uint32(raw[5:9]))
	pos := 9 + count*16
	for i := 0; i < 3; i++ {
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

// Compact creates a new file at newPath containing only live records. snap
// must carry the caller's serialized indices or the sparse/L1/L3 data is
// silently dropped.
func (e *StorageEngine) Compact(newPath string, snap *IndexSnapshotData) error {
	if snap == nil {
		return common.NewError(common.ErrInvalidQuery, "compact requires an index snapshot")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	newEng, err := Create(newPath, e.activeHeaderRef().VectorDim)
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
	for idHash, offset := range e.index {
		rt, _, data, _, readErr := RecordData(e.mmap, offset)
		if readErr != nil {
			return common.NewError(common.ErrCorruption, "compact: read live record", readErr)
		}
		if _, writeErr := newEng.WriteRecord(rt, idHash, data); writeErr != nil {
			return writeErr
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
