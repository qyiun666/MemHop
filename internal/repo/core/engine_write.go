// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Engine append path: record batch writing and the per-agent index
// maintenance that follows a successful flush+remap.

package core

import (
	"io"

	"github.com/qyiun666/MemHop/internal/common"
)

func (e *StorageEngine) WriteRecord(agentID uint64, recordType uint8, idHash uint64, data []byte) (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, common.NewError(common.ErrClosed, "engine is closed")
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
		return nil, common.NewError(common.ErrClosed, "engine is closed")
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
	if err := e.file.Sync(); err != nil {
		return nil, common.NewError(common.ErrIO, "sync", err)
	}
	mm, err := RemapFile(e.file, e.mmap)
	if err != nil {
		return nil, err
	}
	e.mmap = mm
	e.updateIndexAfterWrite(records, offsets)
	last := records[len(records)-1]
	e.nextOffset = offsets[len(offsets)-1] + uint64(RecordHeaderSize+len(last.Data))
	e.dirty = true
	return offsets, nil
}

// appendFrames encodes each record and appends it at end of file,
// returning the frame offsets; index state is untouched until success.
func (e *StorageEngine) appendFrames(records []RecordEntry) ([]uint64, error) {
	offsets := make([]uint64, 0, len(records))
	for _, rec := range records {
		encoded := EncodeRecord(rec.AgentID, rec.RecordType, 0, rec.IDHash, rec.Data)
		offset, err := e.file.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, common.NewError(common.ErrIO, "seek end", err)
		}
		if _, err := e.file.Write(encoded); err != nil {
			return nil, common.NewError(common.ErrIO, "write record", err)
		}
		offsets = append(offsets, uint64(offset))
	}
	return offsets, nil
}

// updateIndexAfterWrite merges freshly appended frames into the per-agent
// primary/type indexes; a re-written idHash moves the type membership to
// the new record type. Caller must hold e.mu with the mmap remapped.
func (e *StorageEngine) updateIndexAfterWrite(records []RecordEntry, offsets []uint64) {
	for i, rec := range records {
		if e.index[rec.AgentID] == nil {
			e.index[rec.AgentID] = make(map[uint64]uint64)
		}
		if _, exists := e.index[rec.AgentID][rec.IDHash]; !exists {
			e.recordCount++
		}
		if oldOff, exists := e.index[rec.AgentID][rec.IDHash]; exists {
			if oldRT, ok := e.recordTypeAt(oldOff); ok && oldRT != rec.RecordType {
				delete(e.byAgentType[rec.AgentID][oldRT], rec.IDHash)
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
