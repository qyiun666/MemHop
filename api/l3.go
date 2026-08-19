// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 API of the public facade: thin delegation to the internal layer
// DB methods, reusing the DB instance returned by Open.

package api

import (
	"github.com/qyiun666/MemHop/internal/common"
)

// Thin wrapper; see internal/l3.go ((db *DB) GetL3).
func (db *DB) GetL3(id string) (*L3Graph, error) {
	return db.DB.GetL3(id)
}

// Thin wrapper; see internal/l3.go ((db *DB) ListL3).
func (db *DB) ListL3() ([]HypergraphSlot, error) {
	return db.DB.ListL3()
}

// Thin wrapper; write op, delegates under the write lock.
func (db *DB) ImportL3(items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.ImportL3(items, mode)
}

// Thin wrapper; write op, delegates under the write lock.
func (db *DB) UpdateL3(id string, name *string) (*L3Graph, error) {
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

// Thin wrapper; see internal/l3query.go ((db *DB) QueryL3Nodes).
func (db *DB) QueryL3Nodes(q L3NodeQuery) ([]HypergraphNode, error) {
	return db.DB.QueryL3Nodes(q)
}

// Thin wrapper; see internal/l3query.go ((db *DB) QueryL3Subgraph).
func (db *DB) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []GraphEdgeKind) (*L3Subgraph, error) {
	return db.DB.QueryL3Subgraph(graphID, startNodeID, maxDepth, edgeKinds)
}
