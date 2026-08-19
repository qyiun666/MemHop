// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// DB is the global in-memory database instance returned by Open; business
// methods (search/dream/update) hang directly on it.
type DB struct {
	engine       *repo.StorageEngine
	config       *MemHopConfig
	sparseIndex  *repo.SparseIndex
	llm          *Provider
	l1Reverse    atomic.Pointer[repo.L1ReverseIndex]
	encoder      Encoder
	activeScenes []uint64
	// builtinCapabilities are read-only reference capabilities attached to
	// L5 query responses; they are never written to the .meh file. Set once
	// via SetBuiltinCapabilities before the DB is published.
	builtinCapabilities []core.Capability
	lastDreamAt         atomic.Int64 // Unix ms of the last successful Dream (0 = never)
	closed              atomic.Bool
	mu                  sync.RWMutex // public methods RLock; Close/Dream Lock
}

// Lock/Unlock provide the write lock for combined internal-layer write ops.
func (db *DB) Lock()   { db.mu.Lock() }
func (db *DB) Unlock() { db.mu.Unlock() }

func (db *DB) IsClosed() bool { return db.closed.Load() }

func (db *DB) HasActiveScenes() bool { return len(db.activeScenes) > 0 }

// activateScene appends a scene idempotently and bounds the active set to
// Defaults.Capacity. When full, the oldest activation is evicted; that scene
// remains searchable on disk but is no longer a Dream compression target.
func (db *DB) activateScene(sceneID uint64) {
	for _, sid := range db.activeScenes {
		if sid == sceneID {
			return
		}
	}
	capacity := 0
	if db.config != nil {
		capacity = db.config.Defaults.Capacity
	}
	if capacity > 0 && len(db.activeScenes) >= capacity {
		copy(db.activeScenes, db.activeScenes[1:])
		db.activeScenes[len(db.activeScenes)-1] = sceneID
		return
	}
	db.activeScenes = append(db.activeScenes, sceneID)
}

func (db *DB) TouchLastDreamAt() { db.lastDreamAt.Store(time.Now().UnixMilli()) }

func (db *DB) getL1Reverse() *repo.L1ReverseIndex { return db.l1Reverse.Load() }

// beginRead takes the shared lock for a public operation and rejects use
// after Close.
func (db *DB) beginRead() error {
	db.mu.RLock()
	if db.closed.Load() {
		db.mu.RUnlock()
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return nil
}

func (db *DB) Close() error {
	if !db.closed.CompareAndSwap(false, true) {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	snap, err := db.buildSnapshot()
	if err != nil {
		return err
	}
	var encErr error
	if c, ok := db.encoder.(interface{ Close() error }); ok {
		encErr = c.Close()
	}
	// Always close the engine to release mmap/file even if the encoder failed.
	engErr := repo.Close(db.engine, snap)
	if encErr != nil {
		return common.NewError(common.ErrEncoder, "encoder close", encErr)
	}
	return engErr
}

func (db *DB) Checkpoint() error {
	if err := db.beginRead(); err != nil {
		return err
	}
	defer db.mu.RUnlock()
	snap, err := db.buildSnapshot()
	if err != nil {
		return err
	}
	return repo.Checkpoint(db.engine, snap)
}

// buildSnapshot serializes the in-memory indices for checkpoint persistence.
// L3IndexData stays nil: L3 data is persisted as individual records.
func (db *DB) buildSnapshot() (*repo.IndexSnapshotData, error) {
	sparseData, err := repo.SerializeSparseIndex(db.sparseIndex)
	if err != nil {
		return nil, common.NewError(common.ErrSerialization, "sparse index", err)
	}
	l1RevData, err := repo.SerializeL1ReverseIndex(db.getL1Reverse())
	if err != nil {
		return nil, common.NewError(common.ErrSerialization, "l1 reverse index", err)
	}
	return &repo.IndexSnapshotData{
		SparseData:    sparseData,
		L1ReverseData: l1RevData,
		L3IndexData:   nil,
	}, nil
}
