// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// DB is the global in-memory database instance returned by Open; business
// methods (search/dream/update) hang directly on it.
type DB struct {
	engine       *core.StorageEngine
	config       *MemHopConfig
	sparseIndex  *index.SparseIndex
	l2Meta       *index.L2MetaIndex
	llm          *Provider
	l1Reverse    atomic.Pointer[index.L1ReverseIndex]
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

// activateScene appends a scene idempotently; repeats keep first-order
// positions. The active set is unbounded here: Update triggers a Dream on
// the oldest scene when it reaches Defaults.Capacity, and RunDream removes
// compressed scenes to bring it back down.
func (db *DB) activateScene(sceneID uint64) {
	if slices.Contains(db.activeScenes, sceneID) {
		return
	}
	db.activeScenes = append(db.activeScenes, sceneID)
}

func (db *DB) TouchLastDreamAt() { db.lastDreamAt.Store(time.Now().UnixMilli()) }

func (db *DB) getL1Reverse() *index.L1ReverseIndex { return db.l1Reverse.Load() }

// syncL2Meta refreshes one topic entry of the L2MetaIndex from the record
// just written; call it right after engine writes, before the sparse index
// update (storage → l2meta → sparse lock order). On read failure the entry
// is removed so stale metadata is never served.
func (db *DB) syncL2Meta(idHash uint64) {
	if db.l2Meta == nil {
		return
	}
	topic, err := core.ReadTopicLenient(db.engine, idHash)
	if err != nil || topic == nil {
		db.l2Meta.Remove(idHash)
		return
	}
	db.l2Meta.Update(index.L2MetaFromTopic(topic))
}

// retargetL2Meta moves every topic of the merged-away scenes to the primary
// scene in the L2MetaIndex, mirroring repo.MergeScenesL2 after a merge.
func (db *DB) retargetL2Meta(primaryHash uint64, removed map[uint64]struct{}) {
	if db.l2Meta == nil {
		return
	}
	for sid := range removed {
		for _, id := range db.l2Meta.GetByScene(sid) {
			meta := db.l2Meta.Remove(id)
			if meta == nil {
				continue
			}
			meta.SceneID = primaryHash
			db.l2Meta.Update(meta)
		}
	}
}

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
	engErr := db.engine.Close(snap)
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
	return db.engine.Checkpoint(snap)
}

// buildSnapshot serializes the in-memory indices for checkpoint persistence.
// L3IndexData stays nil: L3 data is persisted as individual records.
func (db *DB) buildSnapshot() (*core.IndexSnapshotData, error) {
	sparseData, err := db.sparseIndex.Serialize()
	if err != nil {
		return nil, common.NewError(common.ErrSerialization, "sparse index", err)
	}
	l1RevData, err := db.getL1Reverse().Serialize()
	if err != nil {
		return nil, common.NewError(common.ErrSerialization, "l1 reverse index", err)
	}
	return &core.IndexSnapshotData{
		SparseData:    sparseData,
		L1ReverseData: l1RevData,
		L3IndexData:   nil,
	}, nil
}
