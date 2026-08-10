// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3Index provides keyword, type, graph, and BM25 search for L3 hypergraph nodes.

package index

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// IndexedNode caches key fields of an L3 node for fast lookup.
type IndexedNode struct {
	IDHash   uint64
	GraphID  uint64
	NodeType string
	Keywords []string
}

// L3Index is a concurrent-safe in-memory index over L3 hypergraph nodes.
type L3Index struct {
	byKeyword map[string]map[uint64]bool // keyword → set of node hashes
	byType    map[string]map[uint64]bool // node_type → set of node hashes
	byGraph   map[uint64]map[uint64]bool // graph_id → set of node hashes
	nodes     map[uint64]*IndexedNode    // node hash → cached info
	bm25      *SparseIndex               // BM25 sparse index
	mu        sync.RWMutex
}

// NewL3Index creates an empty L3Index.
func NewL3Index() *L3Index {
	return &L3Index{
		byKeyword: make(map[string]map[uint64]bool),
		byType:    make(map[string]map[uint64]bool),
		byGraph:   make(map[uint64]map[uint64]bool),
		nodes:     make(map[uint64]*IndexedNode),
		bm25:      NewSparseIndex(),
	}
}

// BuildFromEngine scans the engine and indexes all L3 graph nodes.
func (idx *L3Index) BuildFromEngine(engine *core.StorageEngine) error {
	nodes, err := loadAllL3Nodes(engine)
	if err != nil {
		return err
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, node := range nodes {
		idx.addNodeLocked(node)
	}
	return nil
}

// AddNode adds a single node to the index.
func (idx *L3Index) AddNode(node *core.HypergraphNode) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.addNodeLocked(node)
}

// RemoveNode removes a node from the index by common.
func (idx *L3Index) RemoveNode(nodeHash uint64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	info, ok := idx.nodes[nodeHash]
	if !ok {
		return
	}
	idx.removeNodeLocked(nodeHash, info)
}

// SearchByKeyword returns node hashes matching a keyword (exact match).
func (idx *L3Index) SearchByKeyword(keyword string, limit int) []uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	set := idx.byKeyword[keyword]
	return common.SetToSlice(set, limit)
}

// SearchByType returns node hashes of a given type, optionally filtered by graph.
func (idx *L3Index) SearchByType(nodeType string, graphID uint64, limit int) []uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	typeSet := idx.byType[nodeType]
	if graphID == 0 {
		return common.SetToSlice(typeSet, limit)
	}
	return intersectWithGraph(typeSet, idx.byGraph[graphID], limit)
}

// Len returns the number of indexed nodes.
func (idx *L3Index) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.nodes)
}

// --- internal helpers (caller must hold idx.mu) ---

func (idx *L3Index) addNodeLocked(node *core.HypergraphNode) {
	h := node.IDHash
	if old, ok := idx.nodes[h]; ok {
		idx.removeNodeLocked(h, old)
	}
	info := &IndexedNode{
		IDHash:   h,
		GraphID:  node.GraphID,
		NodeType: node.NodeType,
		Keywords: node.Keywords,
	}
	idx.nodes[h] = info

	for _, kw := range node.Keywords {
		if idx.byKeyword[kw] == nil {
			idx.byKeyword[kw] = make(map[uint64]bool)
		}
		idx.byKeyword[kw][h] = true
	}

	if idx.byType[node.NodeType] == nil {
		idx.byType[node.NodeType] = make(map[uint64]bool)
	}
	idx.byType[node.NodeType][h] = true

	if idx.byGraph[node.GraphID] == nil {
		idx.byGraph[node.GraphID] = make(map[uint64]bool)
	}
	idx.byGraph[node.GraphID][h] = true

	// BM25: index title + content.
	tokens := TokenizeWords(node.Title + " " + node.Content)
	idx.bm25.AddDocument(h, tokens, uint32(len(tokens)))
}

func (idx *L3Index) removeNodeLocked(nodeHash uint64, info *IndexedNode) {
	delete(idx.nodes, nodeHash)

	for _, kw := range info.Keywords {
		if s := idx.byKeyword[kw]; s != nil {
			delete(s, nodeHash)
			if len(s) == 0 {
				delete(idx.byKeyword, kw)
			}
		}
	}
	if s := idx.byType[info.NodeType]; s != nil {
		delete(s, nodeHash)
		if len(s) == 0 {
			delete(idx.byType, info.NodeType)
		}
	}
	if s := idx.byGraph[info.GraphID]; s != nil {
		delete(s, nodeHash)
		if len(s) == 0 {
			delete(idx.byGraph, info.GraphID)
		}
	}
	idx.bm25.RemoveDocument(nodeHash)
}

// loadAllL3Nodes reads every L3 node from the engine.
func loadAllL3Nodes(engine *core.StorageEngine) ([]*core.HypergraphNode, error) {
	var nodes []*core.HypergraphNode
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != core.RecL3GraphNode {
			return true
		}
		var node core.HypergraphNode
		if err := json.Unmarshal(data, &node); err == nil {
			nodes = append(nodes, &node)
		}
		return true
	})
	return nodes, nil
}

// intersectWithGraph returns hashes present in both sets, sorted and limited.
func intersectWithGraph(typeSet, graphSet map[uint64]bool, limit int) []uint64 {
	if len(typeSet) == 0 || len(graphSet) == 0 {
		return nil
	}
	var out []uint64
	for h := range typeSet {
		if graphSet[h] {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
