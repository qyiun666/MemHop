// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Engine delete path: tombstone appending and the index bookkeeping that
// drops deleted records from the per-agent primary/type indexes.

package core

import (
	"io"

	"github.com/qyiun666/MemHop/internal/common"
)

// DeleteRecord appends a FlagDeleted tombstone (same idHash, original type,
// empty data) and drops the record from the index.
func (e *StorageEngine) DeleteRecord(agentID, idHash uint64) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return false, common.NewError(common.ErrClosed, "engine is closed")
	}
	deleted, err := e.deleteRecordBatchLocked(agentID, []uint64{idHash})
	return deleted > 0, err
}

// DeleteRecordBatch deletes multiple records of one agent domain in one
// flush+remap cycle; already-missing ids are skipped. Returns the number
// deleted.
func (e *StorageEngine) DeleteRecordBatch(agentID uint64, idHashes []uint64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, common.NewError(common.ErrClosed, "engine is closed")
	}
	return e.deleteRecordBatchLocked(agentID, idHashes)
}

// deleteRecordBatchLocked appends tombstones, syncs once, remaps once, then
// bulk-updates the index. Caller must hold e.mu.
func (e *StorageEngine) deleteRecordBatchLocked(agentID uint64, idHashes []uint64) (int, error) {
	tombstones, liveOffsets, deleted := e.buildTombstones(agentID, idHashes)
	if deleted == 0 {
		return 0, nil
	}
	// Trim any trailing snapshot so record frames always precede snapshots.
	if err := e.trimTailSnapshot(); err != nil {
		return 0, err
	}
	end, err := e.file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, common.NewError(common.ErrIO, "seek end", err)
	}
	if _, err := e.file.Write(tombstones); err != nil {
		return 0, common.NewError(common.ErrIO, "write tombstones", err)
	}
	// The tombstones are in the file from here on, so the record-area end
	// moves before the flush is attempted: a Sync or remap that fails must not
	// leave nextOffset behind the log, because the checkpoint header writes
	// RecordEnd from it and a compact truncates at it.
	e.nextOffset = uint64(end) + uint64(len(tombstones))
	if err := e.file.Sync(); err != nil {
		return 0, common.NewError(common.ErrIO, "sync tombstones", err)
	}
	mm, err := RemapFile(e.file, e.mmap)
	if err != nil {
		return 0, err
	}
	e.mmap = mm
	e.applyDeletions(agentID, liveOffsets)
	return deleted, nil
}

// buildTombstones encodes one tombstone frame per live (agent, idHash),
// keeping the original type for forensics; liveOffsets maps the deleted ids
// to their old frame offsets (missing ids are skipped).
func (e *StorageEngine) buildTombstones(agentID uint64, idHashes []uint64) (frames []byte, liveOffsets map[uint64]uint64, deleted int) {
	liveOffsets = make(map[uint64]uint64, len(idHashes))
	for _, idHash := range idHashes {
		offset, ok := e.index[agentID][idHash]
		if !ok {
			continue
		}
		var rt uint8
		if int(offset) < len(e.mmap) {
			rt = e.mmap[int(offset)]
		}
		frames = append(frames, EncodeRecord(agentID, rt, FlagDeleted, idHash, nil)...)
		liveOffsets[idHash] = offset
		deleted++
	}
	return frames, liveOffsets, deleted
}

// applyDeletions drops the tombstoned records from the primary and type
// indexes after the frames are durable. Caller must hold e.mu.
func (e *StorageEngine) applyDeletions(agentID uint64, deletedOffsets map[uint64]uint64) {
	for idHash, offset := range deletedOffsets {
		delete(e.index[agentID], idHash)
		if oldRT, ok := e.recordTypeAt(offset); ok {
			e.removeTypeLocked(agentID, oldRT, idHash)
		}
	}
	if len(e.index[agentID]) == 0 {
		delete(e.index, agentID)
	}
}

// removeTypeLocked drops one idHash from the (agent, type) secondary index
// and prunes the empty containers. Caller must hold e.mu.
func (e *StorageEngine) removeTypeLocked(agentID uint64, rt uint8, idHash uint64) {
	ids, ok := e.byAgentType[agentID][rt]
	if !ok {
		return
	}
	delete(ids, idHash)
	if len(ids) > 0 {
		return
	}
	delete(e.byAgentType[agentID], rt)
	if len(e.byAgentType[agentID]) == 0 {
		delete(e.byAgentType, agentID)
	}
}
