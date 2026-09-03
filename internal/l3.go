// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 hypergraph operations of the internal layer: view / import / update / delete.

package internal

import (
	"fmt"
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/knowledge"
	"github.com/qyiun666/MemHop/internal/common"
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
		ok, err := db.importOneL3Node(agentID, &items[i], mode, graphCache, nodeTitles, result)
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
			db.importL3Relations(agentID, &items[i], graphCache, nodeTitles, result)
		}
	}
	return result, nil
}

// importOneL3Node applies one item's node: graph slot create/reuse, then
// node create/merge/overwrite per mode with its SourceRef. Reports whether
// the node landed (false = skipped existing node in Skip mode).
func (db *DB) importOneL3Node(agentID uint64, item *L3ImportItem, mode L3ImportMode, graphCache map[string]uint64, nodeTitles map[uint64]map[string]struct{}, result *L3ImportResult) (bool, error) {
	graphID, ok := graphCache[item.Domain]
	if !ok {
		gid, err := repo.CreateGraphL3(db.engine, agentID, item.Domain, core.HypergraphSource{Kind: core.SourceManual})
		if err != nil {
			return false, err
		}
		graphID, graphCache[item.Domain] = gid, gid
	}
	if _, seen := nodeTitles[graphID]; !seen {
		titles := make(map[string]struct{})
		for _, n := range repo.ListNodeL3(db.engine, agentID, graphID) {
			titles[n.Title] = struct{}{}
		}
		nodeTitles[graphID] = titles
	}
	nodeID := repo.NodeIDL3(graphID, item.Title)
	if _, exists := nodeTitles[graphID][item.Title]; exists {
		switch mode {
		case L3ImportSkip:
			result.SkippedCount++
			return false, nil
		case L3ImportMerge:
			if err := mutateImportedNode(db, agentID, graphID, *item, knowledge.MergeFields); err != nil {
				return false, err
			}
		case L3ImportOverwrite:
			if err := mutateImportedNode(db, agentID, graphID, *item, knowledge.OverwriteFields); err != nil {
				return false, err
			}
		}
		result.UpdatedIDs = append(result.UpdatedIDs, common.FormatHash(nodeID))
		return true, nil
	}
	id, err := repo.CreateNodeL3(db.engine, agentID, graphID, item.Title, item.NodeType, item.Content, item.Keywords, item.SourceRef)
	if err != nil {
		return false, err
	}
	nodeTitles[graphID][item.Title] = struct{}{}
	result.CreatedIDs = append(result.CreatedIDs, common.FormatHash(id))
	return true, nil
}

// importL3Relations resolves one item's Related entries against the graph's
// node set and creates the hyperedges; every unresolvable entry is recorded
// in result.Errors and the rest continue. Edge ids hash the sorted node
// pair, so re-importing the same batch is idempotent.
func (db *DB) importL3Relations(agentID uint64, item *L3ImportItem, graphCache map[string]uint64, nodeTitles map[uint64]map[string]struct{}, result *L3ImportResult) {
	if len(item.Related) == 0 {
		return
	}
	graphID, ok := graphCache[item.Domain]
	if !ok {
		return
	}
	fromID := repo.NodeIDL3(graphID, item.Title)
	for _, rel := range item.Related {
		switch {
		case rel.Title == "" || rel.Title == item.Title:
			result.Errors = append(result.Errors, fmt.Sprintf("%s: relation %q: empty or self target", item.Title, rel.Title))
			continue
		case rel.Kind > core.EdgeCustom:
			result.Errors = append(result.Errors, fmt.Sprintf("%s: relation %q: invalid edge kind %d", item.Title, rel.Title, rel.Kind))
			continue
		}
		if _, exists := nodeTitles[graphID][rel.Title]; !exists {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: relation %q: target node not found", item.Title, rel.Title))
			continue
		}
		ids := []uint64{fromID, repo.NodeIDL3(graphID, rel.Title)}
		slices.Sort(ids)
		if _, err := repo.CreateEdgeL3(db.engine, agentID, graphID, rel.Kind, ids, 1.0); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: relation %q: %v", item.Title, rel.Title, err))
			continue
		}
		result.EdgesCreated++
	}
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

// mutateImportedNode applies one knowledge field-merge policy to the stored
// node of an import item (record access and membership stay in the repo).
func mutateImportedNode(db *DB, agentID uint64, graphID uint64, item L3ImportItem,
	merge func(*core.HypergraphNode, string, string, []string, string, int64)) error {
	now := time.Now().UnixMilli()
	_, err := repo.MutateNodeL3(db.engine, agentID, graphID, item.Title, func(n *core.HypergraphNode) {
		merge(n, item.NodeType, item.Content, item.Keywords, item.SourceRef, now)
	})
	return err
}
