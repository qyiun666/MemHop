// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Engine append path: record batch writing and the per-agent index
// maintenance that follows a successful flush+remap.

package core

import (
	"errors"
	"io"

	"github.com/qyiun666/MemHop/internal/common"
)

func (e *StorageEngine) WriteRecord(agentID uint64, recordType uint8, idHash uint64, data []byte) (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, errEngineClosed
	}
	offsets, err := e.writeRecordBatch([]RecordEntry{{AgentID: agentID, RecordType: recordType, IDHash: idHash, Data: data}})
	if err != nil {
		return 0, err
	}
	return offsets[0], nil
}

// WriteRecordBatch writes all records in one flush+remap cycle.
func (e *StorageEngine) WriteRecordBatch(records []RecordEntry) ([]uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errEngineClosed
	}
	return e.writeRecordBatch(records)
}

// writeRecordBatch appends encoded frames, syncs and remaps once, then
// updates the in-memory indexes. Caller must hold e.mu.
func (e *StorageEngine) writeRecordBatch(records []RecordEntry) ([]uint64, error) {
	if len(records) == 0 {
		return nil, nil
	}
	// Trim any trailing snapshot so record frames always precede snapshots.
	if err := e.trimTailSnapshot(); err != nil {
		return nil, err
	}
	offsets, err := e.appendFrames(records)
	if err != nil {
		return nil, err
	}
	// The frames are in the file from here on, so the record-area end moves
	// before the flush is attempted: a Sync or remap that fails must not leave
	// nextOffset behind the log, because the checkpoint header writes RecordEnd
	// from it and a compact truncates at it.
	last := records[len(records)-1]
	e.nextOffset = offsets[len(offsets)-1] + uint64(RecordHeaderSize+len(last.Data))
	if err := e.file.Sync(); err != nil {
		return nil, common.NewError(common.ErrIO, "sync", err)
	}
	mm, err := RemapFile(e.file, e.mmap)
	if err != nil {
		return nil, err
	}
	e.mmap = mm
	e.updateIndexAfterWrite(records, offsets)
	return offsets, nil
}

// appendFrames encodes each record and appends it at end of file, returning the
// frame offsets; index state is untouched until success, and a batch that fails
// half-way is undone — see undoAppend.
func (e *StorageEngine) appendFrames(records []RecordEntry) ([]uint64, error) {
	start, err := e.file.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, common.NewError(common.ErrIO, "seek end", err)
	}
	offsets := make([]uint64, 0, len(records))
	for _, rec := range records {
		encoded := EncodeRecord(rec.AgentID, rec.RecordType, 0, rec.IDHash, rec.Data)
		offset, err := e.file.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, e.undoAppend(start, common.NewError(common.ErrIO, "seek end", err))
		}
		if _, err := e.file.Write(encoded); err != nil {
			return nil, e.undoAppend(start, common.NewError(common.ErrIO, "write record", err))
		}
		offsets = append(offsets, uint64(offset))
	}
	return offsets, nil
}

// undoAppend cuts the file back to the offset a failed append batch began at:
// a partial frame left between valid records sits past the reach of Open's tail
// recovery. Both causes are joined — a failed cut means the log holds that frame.
func (e *StorageEngine) undoAppend(start int64, cause error) error {
	// ponytail: a truncateTail that itself fails leaves the mapping dropped. That is
	// the recovery primitive's known ceiling, shared with the trim step every batch
	// already runs, and no test here can provoke a failed ftruncate.
	return errors.Join(cause, e.truncateTail(start))
}

// updateIndexAfterWrite merges freshly appended frames into the per-agent
// primary/type indexes; a re-written idHash moves the type membership to
// the new record type. Caller must hold e.mu with the mmap remapped.
func (e *StorageEngine) updateIndexAfterWrite(records []RecordEntry, offsets []uint64) {
	for i, rec := range records {
		if e.index[rec.AgentID] == nil {
			e.index[rec.AgentID] = make(map[uint64]uint64)
		}
		if oldOff, exists := e.index[rec.AgentID][rec.IDHash]; exists {
			if oldRT, ok := e.recordTypeAt(oldOff); ok && oldRT != rec.RecordType {
				e.removeTypeLocked(rec.AgentID, oldRT, rec.IDHash)
			}
		}
		e.index[rec.AgentID][rec.IDHash] = offsets[i]
		if e.byAgentType[rec.AgentID] == nil {
			e.byAgentType[rec.AgentID] = make(map[uint8]map[uint64]struct{})
		}
		if e.byAgentType[rec.AgentID][rec.RecordType] == nil {
			e.byAgentType[rec.AgentID][rec.RecordType] = make(map[uint64]struct{})
		}
		e.byAgentType[rec.AgentID][rec.RecordType][rec.IDHash] = struct{}{}
	}
}

// recordTypeAt reads the type byte of the frame at offset from the live
// mmap; ok is false when the offset falls outside the mapping.
func (e *StorageEngine) recordTypeAt(offset uint64) (uint8, bool) {
	if int(offset) >= len(e.mmap) {
		return 0, false
	}
	return e.mmap[int(offset)], true
}
