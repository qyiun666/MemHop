// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 API of the public facade: thin delegation to the internal layer
// DB methods, reusing the DB instance returned by Open.

package api

import "github.com/qyiun666/MemHop/internal/repo/core"

// Thin wrapper; see internal/l3.go ((db *DB) GetL3).
func (db *DB) GetL3(id string) (*L3Graph, error) {
	return db.DB.GetL3(core.DefaultAgentID, id)
}

// Thin wrapper; see internal/l3.go ((db *DB) ListL3).
func (db *DB) ListL3() ([]HypergraphSlot, error) {
	return db.DB.ListL3(core.DefaultAgentID)
}

// Thin wrapper; see internal/l3.go ((db *DB) ImportL3).
func (db *DB) ImportL3(items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	return db.DB.ImportL3(core.DefaultAgentID, items, mode)
}

// Thin wrapper; see internal/l3.go ((db *DB) UpdateL3).
func (db *DB) UpdateL3(id string, name *string) (*L3Graph, error) {
	return db.DB.UpdateL3(core.DefaultAgentID, id, name)
}

// Thin wrapper; see internal/l3.go ((db *DB) DeleteL3).
func (db *DB) DeleteL3(id string) error {
	return db.DB.DeleteL3(core.DefaultAgentID, id)
}

// Thin wrapper; see internal/l3query.go ((db *DB) QueryL3Nodes).
func (db *DB) QueryL3Nodes(q L3NodeQuery) ([]HypergraphNode, error) {
	return db.DB.QueryL3Nodes(core.DefaultAgentID, q)
}

// Thin wrapper; see internal/l3query.go ((db *DB) QueryL3Subgraph).
func (db *DB) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []GraphEdgeKind) (*L3Subgraph, error) {
	return db.DB.QueryL3Subgraph(core.DefaultAgentID, graphID, startNodeID, maxDepth, edgeKinds)
}
