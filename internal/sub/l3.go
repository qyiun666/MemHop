// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 hypergraph operations of the sub layer: view / import / update / delete.

package sub

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

type L3Graph struct {
	Slot  core.HypergraphSlot
	Nodes []core.HypergraphNode
	Edges []core.HypergraphEdge
}

type L3ImportItem struct {
	Title    string   `json:"title"`
	Domain   string   `json:"domain"`
	NodeType string   `json:"node_type"`
	Content  string   `json:"content"`
	Keywords []string `json:"keywords"`
}

type L3ImportMode string

const (
	L3ImportSkip      L3ImportMode = "Skip"
	L3ImportMerge     L3ImportMode = "Merge"
	L3ImportOverwrite L3ImportMode = "Overwrite"
)

type L3ImportResult struct {
	CreatedIDs   []string `json:"created_ids"`
	UpdatedIDs   []string `json:"updated_ids"`
	SkippedCount int      `json:"skipped_count"`
}

func (db *DB) GetL3(id string) (*L3Graph, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	return db.getL3Graph(id)
}

// getL3Graph is the lock-free impl shared by GetL3 and UpdateL3 (write lock).
func (db *DB) getL3Graph(id string) (*L3Graph, error) {
	graphHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
	}
	var slot *core.HypergraphSlot
	graphs := repo.ListGraphsL3(db.engine)
	for i := range graphs {
		if graphs[i].IDHash == graphHash {
			slot = &graphs[i]
			break
		}
	}
	if slot == nil {
		return nil, common.NewError(common.ErrNotFound, "graph not found")
	}
	nodes := repo.ListNodeL3(db.engine, id)
	edges := repo.ListEdgeL3(db.engine, id)
	if nodes == nil {
		nodes = []core.HypergraphNode{}
	}
	if edges == nil {
		edges = []core.HypergraphEdge{}
	}
	return &L3Graph{Slot: *slot, Nodes: nodes, Edges: edges}, nil
}

func (db *DB) ListL3() ([]core.HypergraphSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	all := repo.ListGraphsL3(db.engine)
	if all == nil {
		return []core.HypergraphSlot{}, nil
	}
	return all, nil
}

// ImportL3 batch-imports knowledge nodes: per-Domain graph slot create/reuse,
// existing nodes handled by mode.
func (db *DB) ImportL3(items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	if len(items) == 0 {
		return nil, common.NewError(common.ErrInvalidQuery, "import: no items")
	}
	if mode == "" {
		mode = L3ImportOverwrite
	}
	graphCache := make(map[string]uint64, len(items)) // Domain → graphID
	for _, g := range repo.ListGraphsL3(db.engine) {
		graphCache[g.Name] = g.IDHash
	}
	nodeTitles := make(map[uint64]map[string]struct{}) // graphID → existing node titles
	result := &L3ImportResult{CreatedIDs: []string{}, UpdatedIDs: []string{}}
	for _, item := range items {
		if item.Title == "" {
			continue
		}
		graphID, ok := graphCache[item.Domain]
		if !ok {
			gid, err := repo.CreateGraphL3(db.engine, item.Domain, core.HypergraphSource{Kind: core.SourceManual})
			if err != nil {
				continue
			}
			graphID, graphCache[item.Domain] = gid, gid
		}
		if _, seen := nodeTitles[graphID]; !seen {
			titles := make(map[string]struct{})
			for _, n := range repo.ListNodeL3(db.engine, common.FormatHash(graphID)) {
				titles[n.Title] = struct{}{}
			}
			nodeTitles[graphID] = titles
		}
		if _, exists := nodeTitles[graphID][item.Title]; exists {
			switch mode {
			case L3ImportSkip:
				result.SkippedCount++
			case L3ImportMerge:
				result.UpdatedIDs = append(result.UpdatedIDs,
					common.FormatHash(common.HashID(fmt.Sprintf("%s:%s", common.FormatHash(graphID), item.Title))))
			}
			continue
		}
		id, err := repo.CreateNodeL3(db.engine, common.FormatHash(graphID), item.Title, item.NodeType, item.Content, item.Keywords)
		if err != nil {
			continue
		}
		nodeTitles[graphID][item.Title] = struct{}{}
		result.CreatedIDs = append(result.CreatedIDs, common.FormatHash(id))
	}
	return result, nil
}

// UpdateL3 partially updates a graph slot (currently Name only).
func (db *DB) UpdateL3(id string, name *string) (*L3Graph, error) {
	if _, err := repo.UpdateGraphL3(db.engine, id, name); err != nil {
		return nil, err
	}
	return db.getL3Graph(id)
}

// DeleteL3 cascades: deletes the graph with all its nodes and edges.
func (db *DB) DeleteL3(id string) error {
	if _, err := common.ParseID(id); err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
	}
	if !repo.DeleteGraphL3(db.engine, id) {
		return common.NewError(common.ErrIO, "delete graph", nil)
	}
	return nil
}
