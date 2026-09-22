// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Engine crash recovery: record-area scanning at Open, torn-tail
// truncation and secondary-index rebuild.

package core

import (
	"errors"
	"io"
	"log/slog"

	"github.com/qyiun666/MemHop/internal/common"
)

// scanRecords scans from offset, merging records into the per-agent index
// (a later same-(agent,idHash) overrides; a tombstone deletes). Two failures mean
// two different things: a frame that does not fit the file is the torn tail a
// crash left mid-write, and the caller truncates from there; a frame whose
// checksum disagrees is one damaged record — the scan steps past it and keeps
// every record after it.
//
// Advancing past a damaged frame cannot use that frame's length: the length bytes
// are inside what its checksum just disbelieved, so they may point anywhere. The
// scan instead searches forward for the next offset whose frame reads clean —
// nothing in the residue is indexable until some offset proves itself.
func (e *StorageEngine) scanRecords(start uint64) (end uint64, truncate bool, err error) {
	offset := start
	skipped, firstSkipped := 0, uint64(0)
	// Reported on every exit, including the ones that stop early: a recovery that
	// quietly dropped records is the one an operator never hears about.
	defer func() {
		if skipped > 0 {
			slog.Warn("engine: frames failed to decode at open and were stepped past",
				"count", skipped, "first_offset", firstSkipped, "scan_end", end, "truncated", truncate)
		}
	}()
	for {
		_, flags, data, agentID, idHash, err := RecordData(e.mmap, offset)
		if err == nil {
			if flags&FlagDeleted != 0 {
				e.dropFromIndexLocked(agentID, idHash)
			} else {
				if e.index[agentID] == nil {
					e.index[agentID] = make(map[uint64]uint64)
				}
				e.index[agentID][idHash] = offset
			}
			offset += uint64(RecordHeaderSize) + uint64(len(data))
			continue
		}
		if errors.Is(err, io.EOF) {
			// End of the log, or the zero-filled space an append will overwrite.
			return offset, false, nil
		}
		if c := common.CodeOf(err); c != common.ErrCRCMismatch && c != common.ErrCorruption {
			return 0, false, err
		}
		if skipped == 0 {
			firstSkipped = offset
		}
		skipped++
		if e.cursorInSnapshotArea(offset) {
			// The bytes here are a snapshot blob the active header already named,
			// not a record whose header rotted: the record area ends here.
			return offset, true, nil
		}
		next, ok := resyncScan(e.mmap, offset+1)
		if !ok {
			// Nothing after the cursor reads as a record either, so the rest of the
			// file holds nothing the index could ever name; cutting it off leaves the
			// append point where the log last proved itself.
			return offset, true, nil
		}
		offset = next
	}
}

// cursorInSnapshotArea reports whether offset lands in the snapshot area the
// active header names (0 when the file carries none). A committed snapshot tail
// is residue to cut, not rot to walk past; the header answers this without
// byte-scanning a blob the size of the log.
func (e *StorageEngine) cursorInSnapshotArea(offset uint64) bool {
	snapshotOffset := e.activeHeaderRef().SnapshotOffset
	return snapshotOffset != 0 && offset >= snapshotOffset
}

// resyncScan finds the first offset at or after from where a frame reads clean,
// or false when the rest of the file holds none. A candidate must satisfy its own
// checksum, so a byte-aligned guess cannot resurrect garbage as a record.
func resyncScan(mmap []byte, from uint64) (uint64, bool) {
	for off := from; off < uint64(len(mmap)); off++ {
		if _, _, _, _, _, err := RecordData(mmap, off); err == nil {
			return off, true
		}
	}
	return 0, false
}

// dropFromIndexLocked removes one idHash from the per-agent primary index
// and prunes the empty domain map. Caller must hold e.mu.
func (e *StorageEngine) dropFromIndexLocked(agentID, idHash uint64) {
	m := e.index[agentID]
	if m == nil {
		return
	}
	delete(m, idHash)
	if len(m) == 0 {
		delete(e.index, agentID)
	}
}

// recoverRecordAreaEnd returns the end of the record area for files with a
// valid tail snapshot. New headers carry RecordEnd directly; legacy files
// (RecordEnd == 0) are reconstructed by walking record frames and skipping
// snapshot blobs. With several snapshots chained at the tail, trimming at the
// latest snapshot offset would leave older snapshots behind.
func (e *StorageEngine) recoverRecordAreaEnd() uint64 {
	active := e.activeHeaderRef()
	if active.RecordEnd >= DataStart &&
		active.RecordEnd <= active.SnapshotOffset &&
		active.RecordEnd <= uint64(len(e.mmap)) {
		return active.RecordEnd
	}
	_, recordEnd, ok := e.walkRecordArea()
	if ok {
		return recordEnd
	}
	return active.SnapshotOffset
}

// walkRecordArea steps through frames and snapshot blobs from DataStart;
// ok is false when the walk hit zero padding or an unrecognized tail (the
// caller must not guess a truncation point that could drop records).
func (e *StorageEngine) walkRecordArea() (offset, recordEnd uint64, ok bool) {
	offset = uint64(DataStart)
	for offset < uint64(len(e.mmap)) {
		if isSnapshotBlobAt(e.mmap[offset:]) {
			n, err := snapshotBlobLength(e.mmap[offset:])
			if err != nil {
				return offset, 0, false
			}
			offset += uint64(n)
			continue
		}
		_, _, data, _, _, err := RecordData(e.mmap, offset)
		if err != nil {
			return offset, 0, false
		}
		offset += uint64(RecordHeaderSize) + uint64(len(data))
		recordEnd = offset
	}
	return offset, recordEnd, offset == uint64(len(e.mmap))
}

// truncateTail shrinks the file and remaps, discarding crash residue.
// Caller must hold e.mu.
func (e *StorageEngine) truncateTail(size int64) error {
	if err := UnmapFile(e.mmap); err != nil {
		return err
	}
	e.mmap = nil
	if err := e.file.Truncate(size); err != nil {
		return common.NewError(common.ErrIO, "truncate crash residue", err)
	}
	if err := e.file.Sync(); err != nil {
		return common.NewError(common.ErrIO, "sync after truncate", err)
	}
	mm, err := MapFile(e.file, int(size))
	if err != nil {
		return err
	}
	e.mmap = mm
	return nil
}

// rebuildByAgentType rebuilds the (agent, type) secondary index. Caller
// must hold e.mu.
func (e *StorageEngine) rebuildByAgentType() {
	e.byAgentType = make(map[uint64]map[uint8]map[uint64]struct{})
	for agentID, m := range e.index {
		for id, off := range m {
			rt, ok := e.recordTypeAt(off)
			if !ok {
				continue
			}
			if e.byAgentType[agentID] == nil {
				e.byAgentType[agentID] = make(map[uint8]map[uint64]struct{})
			}
			if e.byAgentType[agentID][rt] == nil {
				e.byAgentType[agentID][rt] = make(map[uint64]struct{})
			}
			e.byAgentType[agentID][rt][id] = struct{}{}
		}
	}
}
