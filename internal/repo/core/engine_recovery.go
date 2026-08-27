// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Engine crash recovery: record-area scanning at Open, torn-tail
// truncation and secondary-index rebuild.

package core

import (
	"errors"
	"io"

	"github.com/qyiun666/MemHop/internal/common"
)

// scanRecords scans from offset, merging records into the per-agent index
// (a later same-(agent,idHash) overrides; a tombstone deletes). Stops at
// the first truncated or CRC-failed frame (crash residue) and reports
// whether it must be truncated.
func (e *StorageEngine) scanRecords(start uint64) (end uint64, truncate bool, err error) {
	offset := start
	for {
		_, flags, data, agentID, idHash, err := RecordData(e.mmap, offset)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if c := common.CodeOf(err); c == common.ErrCRCMismatch || c == common.ErrCorruption {
				return offset, true, nil
			}
			return 0, false, err
		}
		if flags&FlagDeleted != 0 {
			e.dropFromIndexLocked(agentID, idHash)
		} else {
			if e.index[agentID] == nil {
				e.index[agentID] = make(map[uint64]uint64)
			}
			e.index[agentID][idHash] = offset
		}
		offset += uint64(RecordHeaderSize) + uint64(len(data))
	}
	return offset, false, nil
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
// snapshot blobs until the end of the file. This matters when several
// snapshots are chained at the tail: trimming at the latest snapshot offset
// would leave older snapshots behind and recreate the append/crash data-loss
// window.
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
