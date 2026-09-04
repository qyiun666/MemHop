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
	"github.com/qyiun666/MemHop/internal/scene"
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
// second pass (a relation may target an item later in the batch). The batch is
// validated up front — every item needs a Title and a Domain — so a malformed
// request writes nothing at all; per-item storage failures are what
// result.Errors reports. nil is only returned on success. The result carries
// the graph ids the batch wrote into as well as the node ids, because a host
// needs the former to hang the graph on a scene.
func (db *DB) ImportL3(agentID uint64, items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	if len(items) == 0 {
		return nil, common.NewError(common.ErrInvalidQuery, "import: no items")
	}
	for i := range items {
		if items[i].Title == "" {
			return nil, common.NewError(common.ErrInvalidQuery,
				fmt.Sprintf("import: item %d has no title", i))
		}
		if items[i].Domain == "" {
			return nil, common.NewError(common.ErrInvalidQuery,
				fmt.Sprintf("import: item %q has no domain", items[i].Title))
		}
	}
	switch mode {
	case L3ImportSkip, L3ImportMerge, L3ImportOverwrite:
	default:
		return nil, common.NewError(common.ErrInvalidQuery,
			"import mode must be Skip, Merge or Overwrite")
	}
	batch := graph.NewImportBatch(db.engine, agentID, mode)
	for i := range items {
		if err := batch.ImportNode(&items[i]); err != nil {
			batch.Result().Errors = append(batch.Result().Errors, fmt.Sprintf("%s: %v", items[i].Title, err))
			continue
		}
	}
	// Relations for every item, including one whose node was skipped: edges
	// are deduped by their sorted members plus kind, so re-declaring one is a
	// no-op — while withholding it would silently drop the edges of a node an
	// earlier DeleteL3Nodes had removed and this batch just brought back.
	for i := range items {
		batch.ImportRelations(&items[i])
	}
	result := batch.Result()
	result.GraphIDs = batch.GraphIDs()
	return result, nil
}

// UpdateL3 partially updates a graph slot (currently Name only). The new name
// has to be free: a domain label addresses a graph for the import path, so two
// slots under one label would make that label resolve ambiguously.
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
	if name != nil {
		if err := graph.CheckName(db.engine, agentID, graphHash, *name); err != nil {
			return nil, err
		}
	}
	if _, err := repo.UpdateGraphL3(db.engine, agentID, graphHash, name); err != nil {
		return nil, err
	}
	return db.getL3Graph(agentID, id)
}

// DeleteL3 cascades: deletes the graph with all its nodes and edges, and drops
// the L2 anchors that named it.
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
	if _, err := core.ReadGraphSlot(db.engine, agentID, graphHash); err != nil {
		return err
	}
	if !repo.DeleteGraphL3(db.engine, agentID, graphHash) {
		return common.NewError(common.ErrIO, "delete graph", nil)
	}
	return scene.DetachGraph(db.engine, agentID, graphHash)
}

// DeleteL3Nodes removes nodes from one graph and cascades the hyperedges that
// touch them, so correcting a knowledge node does not mean rebuilding the graph
// (and losing the edges bound to it). Every id must name a node of this graph;
// an unknown or foreign id is refused and nothing is deleted.
func (db *DB) DeleteL3Nodes(agentID uint64, graphID string, nodeIDs []string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	if len(nodeIDs) == 0 {
		return common.NewError(common.ErrInvalidQuery, "delete nodes: no node ids")
	}
	graphHash, err := common.ParseID(graphID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
	}
	if _, err := core.ReadGraphSlot(db.engine, agentID, graphHash); err != nil {
		return err
	}
	targets := make([]uint64, 0, len(nodeIDs))
	for _, raw := range nodeIDs {
		id, err := common.ParseID(raw)
		if err != nil {
			return common.NewError(common.ErrInvalidQuery, "parse node id", err)
		}
		targets = append(targets, id)
	}
	return repo.DeleteNodesL3(db.engine, agentID, graphHash, targets)
}
