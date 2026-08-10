// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
)

// DB is the global in-memory database instance returned by Open.
// 只承载数据与生命周期方法；search/dream/update 等业务方法直接挂在 DB 上。
type DB struct {
	engine       *repo.StorageEngine
	config       *MemHopConfig
	sparseIndex  *repo.SparseIndex
	llm          *Provider
	l1Reverse    atomic.Pointer[repo.L1ReverseIndex]
	encoder      Encoder
	activeScenes []uint64     // 激活场景 ID 列表
	lastDreamAt  atomic.Int64 // Unix ms of the last successful Dream (0 = never)
	closed       atomic.Bool
	mu           sync.RWMutex // public methods RLock; Close/Dream Lock
}

// Lock 写锁；Unlock 释放写锁。internal 层写操作组合使用。
func (db *DB) Lock()   { db.mu.Lock() }
func (db *DB) Unlock() { db.mu.Unlock() }

// IsClosed 报告数据库是否已关闭。
func (db *DB) IsClosed() bool { return db.closed.Load() }

// HasActiveScenes 报告是否存在激活场景。
func (db *DB) HasActiveScenes() bool { return len(db.activeScenes) > 0 }

// activateScene 追加激活场景；已存在则跳过（幂等去重）。
// Search 持 RLock 且业务约定串行调用，无需额外锁。
func (db *DB) activateScene(sceneID uint64) {
	for _, sid := range db.activeScenes {
		if sid == sceneID {
			return
		}
	}
	db.activeScenes = append(db.activeScenes, sceneID)
}

// TouchLastDreamAt 记录最近一次成功 Dream 的时间。
func (db *DB) TouchLastDreamAt() { db.lastDreamAt.Store(time.Now().UnixMilli()) }

// getL1Reverse returns the currently active L1 reverse index snapshot.
func (db *DB) getL1Reverse() *repo.L1ReverseIndex { return db.l1Reverse.Load() }

// beginRead takes the shared read lock for a public operation and rejects
// use after Close. On nil error the caller must defer db.mu.RUnlock().
func (db *DB) beginRead() error {
	db.mu.RLock()
	if db.closed.Load() {
		db.mu.RUnlock()
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return nil
}

// Close persists all data and releases resources.
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
	// Always close engine to release mmap/file even if encoder failed.
	engErr := repo.Close(db.engine, snap)
	if encErr != nil {
		return common.NewError(common.ErrEncoder, "encoder close", encErr)
	}
	return engErr
}

// Checkpoint persists current state to disk without closing.
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
// L3IndexData is intentionally left nil: L3 hypergraph data (graph slots,
// nodes, edges) is persisted directly as individual records in the storage
// engine and does not require a separate in-memory index snapshot.
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
