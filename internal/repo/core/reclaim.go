// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/binary"
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
)

// isSnapshotBlobAt reports whether raw starts with a snapshot magic and is
// long enough for snapshotBlobLength to inspect it.
func isSnapshotBlobAt(raw []byte) bool {
	return len(raw) >= 13 && binary.LittleEndian.Uint32(raw[0:4]) == SnapshotMagic
}

// snapshotBlobLength parses a snapshot blob's total length (0x03 per-agent
// layout: an id header then that many 16-byte offset entries).
func snapshotBlobLength(raw []byte) (int, error) {
	if len(raw) < 13 {
		return 0, common.NewError(common.ErrCorruption, "snapshot too short")
	}
	if binary.LittleEndian.Uint32(raw[0:4]) != SnapshotMagic || raw[4] != SnapshotVersion {
		return 0, common.NewError(common.ErrCorruption, "not a snapshot blob")
	}
	agentCount := int(binary.LittleEndian.Uint32(raw[5:9]))
	pos := 9
	// The agent sections are walked with the decoder that reads them, so the two
	// cannot drift apart on where one section ends when the layout moves.
	for range agentCount {
		_, _, next, err := parseSnapshotAgent(raw, pos)
		if err != nil {
			return 0, err
		}
		pos = next
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
		return nil // snapshot not at tail (legacy layout); leave to Compact
	}
	// nextOffset points at the end of the record area (not at the snapshot
	// tail), so one truncate drops all snapshots before the next append.
	if err := e.truncateTail(int64(e.nextOffset)); err != nil {
		return err
	}
	return e.writeNullSnapshotHeader()
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
// record index.
func (e *StorageEngine) Compact(newPath string) error {
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
	// Compact's cost is the flush and the remap each write does, so the copy runs
	// in batches: writing 2000 records one at a time pays 2000 flushes. The chunk
	// bounds how many payloads are held at once, since the whole point is to rewrite
	// a large file.
	const chunk = 256
	for agentID, m := range e.index {
		batch := make([]RecordEntry, 0, chunk)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			_, err := newEng.WriteRecordBatch(batch)
			batch = batch[:0]
			return err
		}
		for idHash, offset := range m {
			rt, _, data, _, _, readErr := RecordData(e.mmap, offset)
			if readErr != nil {
				// Which half of the file is damaged decides what to do about it, so
				// the read keeps its own code — and the record is named, because a
				// compaction that refused has to say what it refused to move.
				code := common.CodeOf(readErr)
				if code != common.ErrCRCMismatch && code != common.ErrCorruption {
					code = common.ErrCorruption
				}
				return common.NewError(code,
					fmt.Sprintf("compact: record %s at offset %d will not read", common.FormatHash(idHash), offset),
					readErr)
			}
			batch = append(batch, RecordEntry{AgentID: agentID, RecordType: rt, IDHash: idHash, Data: data})
			if len(batch) == chunk {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := flush(); err != nil {
			return err
		}
	}
	if err := newEng.Checkpoint(); err != nil {
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
