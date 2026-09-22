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
	"maps"
	"os"
	"slices"
	"sync"

	"github.com/qyiun666/MemHop/internal/common"
)

type RecordEntry struct {
	AgentID    uint64
	RecordType uint8
	IDHash     uint64
	Data       []byte
}

// errEngineClosed is the single answer every engine operation gives once
// Close has run; the code is what callers branch on.
var errEngineClosed = common.NewError(common.ErrClosed, "engine is closed")

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
	nextOffset   uint64
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

// Stats reports the whole-file view: the mapped size in bytes — the file's size,
// the mapping covers it end to end — and the number of live records across every
// domain. Read-only diagnostics for the layers above; a closed engine reports
// zeros.
func (e *StorageEngine) Stats() (sizeBytes int64, records int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return 0, 0
	}
	return int64(len(e.mmap)), e.totalRecordsLocked()
}

// IndexByType iterates all idHashes of a record type inside one agent
// domain over a snapshot; the yield runs lock-free. A closed engine yields
// nothing.
func (e *StorageEngine) IndexByType(agentID uint64, rt uint8) iter.Seq[uint64] {
	return e.iterSnapshot(func() []uint64 {
		return slices.Collect(maps.Keys(e.byAgentType[agentID][rt]))
	})
}

// IterAgents iterates every agentID that currently holds at least one
// live record, over a snapshot copy.
func (e *StorageEngine) IterAgents() iter.Seq[uint64] {
	return e.iterSnapshot(func() []uint64 {
		return slices.Collect(maps.Keys(e.index))
	})
}

// iterSnapshot builds a key snapshot under the read lock, releases it, then
// yields the ids; a closed engine yields nothing. It takes e.mu itself, unlike
// the *Locked helpers, which is why iteration cannot re-enter the lock.
func (e *StorageEngine) iterSnapshot(snapshot func() []uint64) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		e.mu.RLock()
		if e.closed {
			e.mu.RUnlock()
			return
		}
		ids := snapshot()
		e.mu.RUnlock()
		for _, id := range ids {
			if !yield(id) {
				return
			}
		}
	}
}

// mappedSize is the length of the mapped region, which is the file's length.
func (e *StorageEngine) mappedSize() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return uint64(len(e.mmap))
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
