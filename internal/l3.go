// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 hypergraph big methods of the composition root: view / import / update
// / delete. The import steps live in internal/graph.

package internal

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/graph"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func (db *DB) GetL3(agentID uint64, id string) (*L3Graph, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	return db.getL3Graph(agentID, id)
}

// getL3Graph is the lock-free impl shared by GetL3 and UpdateL3 (domain lock).
func (db *DB) getL3Graph(agentID uint64, id string) (*L3Graph, error) {
	graphHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
	}
	var slot *core.HypergraphSlot
	graphs := core.CollectAllGraphSlots(db.engine, agentID)
	for i := range graphs {
		if graphs[i].IDHash == graphHash {
			slot = &graphs[i]
			break
		}
	}
	if slot == nil {
		return nil, common.NewError(common.ErrNotFound, "graph not found")
	}
	nodes := repo.ListNodeL3(db.engine, agentID, graphHash)
	edges := repo.ListEdgeL3(db.engine, agentID, graphHash)
	if nodes == nil {
		nodes = []core.HypergraphNode{}
	}
	if edges == nil {
		edges = []core.HypergraphEdge{}
	}
	return &L3Graph{Slot: *slot, Nodes: nodes, Edges: edges}, nil
}

func (db *DB) ListL3(agentID uint64) ([]core.HypergraphSlot, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	all := core.CollectAllGraphSlots(db.engine, agentID)
	if all == nil {
		return []core.HypergraphSlot{}, nil
	}
	return all, nil
}

// ImportL3 batch-imports knowledge nodes: per-Domain graph slot create/reuse,
// existing nodes handled by mode, then Related hyperedges resolved in a
// second pass (a relation may target an item later in the batch). Per-item
// failures are recorded in result.Errors and the batch continues; nil is
// only returned on success.
func (db *DB) ImportL3(agentID uint64, items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	if len(items) == 0 {
		return nil, common.NewError(common.ErrInvalidQuery, "import: no items")
	}
	switch mode {
	case "":
		mode = L3ImportOverwrite
	case L3ImportSkip, L3ImportMerge, L3ImportOverwrite:
	default:
		return nil, common.NewError(common.ErrInvalidQuery,
			"import mode must be Skip, Merge or Overwrite")
	}
	graphCache := make(map[string]uint64, len(items)) // Domain → graphID
	for _, g := range core.CollectAllGraphSlots(db.engine, agentID) {
		graphCache[g.Name] = g.IDHash
	}
	nodeTitles := make(map[uint64]map[string]struct{}) // graphID → existing node titles
	result := &L3ImportResult{CreatedIDs: []string{}, UpdatedIDs: []string{}}
	imported := make([]bool, len(items))
	for i := range items {
		if items[i].Title == "" {
			continue
		}
		ok, err := graph.ImportOneNode(db.engine, agentID, &items[i], mode, graphCache, nodeTitles, result)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", items[i].Title, err))
			continue
		}
		imported[i] = ok
	}
	// Relations only for items whose node landed (create/merge/overwrite):
	// a skipped node contributes no edges.
	for i := range items {
		if imported[i] {
			graph.ImportRelations(db.engine, agentID, &items[i], graphCache, nodeTitles, result)
		}
	}
	return result, nil
}

// UpdateL3 partially updates a graph slot (currently Name only).
func (db *DB) UpdateL3(agentID uint64, id string, name *string) (*L3Graph, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	graphHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
	}
	if _, err := repo.UpdateGraphL3(db.engine, agentID, graphHash, name); err != nil {
		return nil, err
	}
	return db.getL3Graph(agentID, id)
}

// DeleteL3 cascades: deletes the graph with all its nodes and edges.
func (db *DB) DeleteL3(agentID uint64, id string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	graphHash, err := common.ParseID(id)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
	}
	if !repo.DeleteGraphL3(db.engine, agentID, graphHash) {
		return common.NewError(common.ErrIO, "delete graph", nil)
	}
	return nil
}
