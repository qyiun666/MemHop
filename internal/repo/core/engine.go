// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// StorageEngine core: the record index model and the lock-protected
// accessors/iteration surface. Lifecycle (create/open/checkpoint/close)
// lives in engine_lifecycle.go, appending in engine_write.go, reads in
// engine_read.go, tombstone deletes in engine_delete.go and crash
// recovery in engine_recovery.go.

package core

import (
	"iter"
	"os"
	"sync"
)

type RecordEntry struct {
	AgentID    uint64
	RecordType uint8
	IDHash     uint64
	Data       []byte
}

// StorageEngine is a V2 append-only storage engine with A/B dual headers.
// Records live in per-agent domains: the record index and the type
// secondary index are keyed by agentID first, so two agents may hold the
// same idHash without conflict and no scan crosses domain boundaries.
type StorageEngine struct {
	file         *os.File
	mmap         []byte
	headerA      *FileHeader
	headerB      *FileHeader
	activeHeader uint8                                    // 0 = A, 1 = B
	index        map[uint64]map[uint64]uint64             // agentID → idHash → offset
	byAgentType  map[uint64]map[uint8]map[uint64]struct{} // agentID → recordType → idHashes
	recordCount  uint32
	nextOffset   uint64
	snapshotData *IndexSnapshotData
	dirty        bool // records written/deleted since the last checkpoint
	closed       bool // Close called; all operations return ErrClosed
	mu           sync.RWMutex
}

// Contains reports whether the (agent, idHash) record is live in the index.
func (e *StorageEngine) Contains(agentID, idHash uint64) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return false
	}
	_, ok := e.index[agentID][idHash]
	return ok
}

// Index iterates over all (idHash, offset) pairs of one agent domain. The
// index is copied first and the yield runs lock-free so engine methods may
// be called; iteration sees a snapshot. Returning false from yield stops
// iteration.
func (e *StorageEngine) Index(agentID uint64) iter.Seq2[uint64, uint64] {
	return func(yield func(uint64, uint64) bool) {
		e.mu.RLock()
		if e.closed {
			e.mu.RUnlock()
			return
		}
		pairs := make([]uint64, 0, len(e.index[agentID])*2)
		for id, off := range e.index[agentID] {
			pairs = append(pairs, id, off)
		}
		e.mu.RUnlock()
		for i := 0; i < len(pairs); i += 2 {
			if !yield(pairs[i], pairs[i+1]) {
				return
			}
		}
	}
}

// IndexByType iterates all idHashes of a record type inside one agent
// domain over a snapshot; the yield runs lock-free. A closed engine yields
// nothing.
func (e *StorageEngine) IndexByType(agentID uint64, rt uint8) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		e.mu.RLock()
		if e.closed {
			e.mu.RUnlock()
			return
		}
		ids := make([]uint64, 0, len(e.byAgentType[agentID][rt]))
		for id := range e.byAgentType[agentID][rt] {
			ids = append(ids, id)
		}
		e.mu.RUnlock()
		for _, id := range ids {
			if !yield(id) {
				return
			}
		}
	}
}

// IterAgents iterates every agentID that currently holds at least one
// live record, over a snapshot copy.
func (e *StorageEngine) IterAgents() iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		e.mu.RLock()
		if e.closed {
			e.mu.RUnlock()
			return
		}
		ids := make([]uint64, 0, len(e.index))
		for agentID := range e.index {
			ids = append(ids, agentID)
		}
		e.mu.RUnlock()
		for _, id := range ids {
			if !yield(id) {
				return
			}
		}
	}
}

func (e *StorageEngine) RecordCount() uint32 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.recordCount
}

func (e *StorageEngine) FileSize() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return uint64(len(e.mmap))
}

func (e *StorageEngine) SnapshotData() *IndexSnapshotData {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotData
}

func (e *StorageEngine) activeHeaderRef() *FileHeader {
	if e.activeHeader == 0 {
		return e.headerA
	}
	return e.headerB
}

// totalRecordsLocked sums live records across all agent domains. Caller
// must hold e.mu.
func (e *StorageEngine) totalRecordsLocked() int {
	total := 0
	for _, m := range e.index {
		total += len(m)
	}
	return total
}
