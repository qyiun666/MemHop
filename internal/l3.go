// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 API of the internal assembly layer: thin delegation to the sub layer
// DB methods, reusing the DB instance returned by Open.

package memhop

import (
	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// Thin wrapper; see internal/sub/l3.go ((db *DB) GetL3).
func (db *DB) GetL3(id string) (*sub.L3Graph, error) {
	return db.DB.GetL3(id)
}

// Thin wrapper; see internal/sub/l3.go ((db *DB) ListL3).
func (db *DB) ListL3() ([]core.HypergraphSlot, error) {
	return db.DB.ListL3()
}

// Thin wrapper; write op, delegates under the write lock.
func (db *DB) ImportL3(items []sub.L3ImportItem, mode sub.L3ImportMode) (*sub.L3ImportResult, error) {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.ImportL3(items, mode)
}

// Thin wrapper; write op, delegates under the write lock.
func (db *DB) UpdateL3(id string, name *string) (*sub.L3Graph, error) {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.UpdateL3(id, name)
}

// Thin wrapper; write op, delegates under the write lock.
func (db *DB) DeleteL3(id string) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.DeleteL3(id)
}

// Thin wrapper; see internal/sub/l3query.go ((db *DB) QueryL3Nodes).
func (db *DB) QueryL3Nodes(q sub.L3NodeQuery) ([]core.HypergraphNode, error) {
	return db.DB.QueryL3Nodes(q)
}

// Thin wrapper; see internal/sub/l3query.go ((db *DB) QueryL3Subgraph).
func (db *DB) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []core.GraphEdgeKind) (*sub.L3Subgraph, error) {
	return db.DB.QueryL3Subgraph(graphID, startNodeID, maxDepth, edgeKinds)
}
