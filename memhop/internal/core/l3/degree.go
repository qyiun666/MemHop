// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// DegreeTracker tracks node degrees and detects isolated nodes.

package l3

import (
	"encoding/json"
	"sync"

	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
)

// DegreeTracker tracks per-node degree counts per graph.
// Degree = number of hyperedges referencing a node.
type DegreeTracker struct {
	degrees map[uint64]map[uint64]int // graphID -> nodeHash -> degree
	dirty   map[uint64]bool           // graphID -> needs rebuild
	mu      sync.RWMutex
}

// NewDegreeTracker creates an empty tracker.
func NewDegreeTracker() *DegreeTracker {
	return &DegreeTracker{
		degrees: make(map[uint64]map[uint64]int),
		dirty:   make(map[uint64]bool),
	}
}

// Rebuild performs a full-scan rebuild of degrees for one graph.
func (dt *DegreeTracker) Rebuild(engine *storage.StorageEngine, graphID uint64) error {
	degrees := fullScanDegrees(engine, graphID)
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.degrees[graphID] = degrees
	delete(dt.dirty, graphID)
	return nil
}

// OnNodeAdded registers a new node with degree 0.
func (dt *DegreeTracker) OnNodeAdded(graphID, nodeHash uint64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	g := dt.ensureGraph(graphID)
	if _, exists := g[nodeHash]; !exists {
		g[nodeHash] = 0
	}
}

// OnNodeDeleted removes a node from tracking.
func (dt *DegreeTracker) OnNodeDeleted(graphID, nodeHash uint64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if g, ok := dt.degrees[graphID]; ok {
		delete(g, nodeHash)
	}
}

// IncrementNode increments degree when an edge is added referencing it.
func (dt *DegreeTracker) IncrementNode(graphID, nodeHash uint64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	g := dt.ensureGraph(graphID)
	g[nodeHash]++
}

// DecrementNode decrements degree when an edge is removed (saturates at 0).
func (dt *DegreeTracker) DecrementNode(graphID, nodeHash uint64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if g, ok := dt.degrees[graphID]; ok {
		if d, exists := g[nodeHash]; exists && d > 0 {
			g[nodeHash] = d - 1
		}
	}
}

// OnEdgeAdded increments degree for every node in a new edge.
func (dt *DegreeTracker) OnEdgeAdded(graphID uint64, nodeIDs []uint64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	g := dt.ensureGraph(graphID)
	for _, nid := range nodeIDs {
		g[nid]++
	}
}

// OnEdgeDeleted decrements degree for every node in a removed edge.
func (dt *DegreeTracker) OnEdgeDeleted(graphID uint64, nodeIDs []uint64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if g, ok := dt.degrees[graphID]; ok {
		for _, nid := range nodeIDs {
			if d, exists := g[nid]; exists && d > 0 {
				g[nid] = d - 1
			}
		}
	}
}

// GetDegree returns the degree of a node (0 if never tracked).
func (dt *DegreeTracker) GetDegree(graphID, nodeHash uint64) int {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	if g, ok := dt.degrees[graphID]; ok {
		return g[nodeHash]
	}
	return 0
}

// FindIsolatedNodes returns node hashes with degree == 0.
func (dt *DegreeTracker) FindIsolatedNodes(graphID uint64) []uint64 {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	var isolated []uint64
	if g, ok := dt.degrees[graphID]; ok {
		for nodeHash, deg := range g {
			if deg == 0 {
				isolated = append(isolated, nodeHash)
			}
		}
	}
	return isolated
}

// FindLowDegreeNodes returns node hashes with degree <= threshold.
func (dt *DegreeTracker) FindLowDegreeNodes(graphID uint64, threshold int) []uint64 {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	var result []uint64
	if g, ok := dt.degrees[graphID]; ok {
		for nodeHash, deg := range g {
			if deg <= threshold {
				result = append(result, nodeHash)
			}
		}
	}
	return result
}

// IsDirty returns whether a graph needs rebuild.
func (dt *DegreeTracker) IsDirty(graphID uint64) bool {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	return dt.dirty[graphID]
}

// MarkDirty marks a graph as needing rebuild.
func (dt *DegreeTracker) MarkDirty(graphID uint64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.dirty[graphID] = true
}

// MarkClean clears the dirty flag for a graph.
func (dt *DegreeTracker) MarkClean(graphID uint64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	delete(dt.dirty, graphID)
}

// ClearGraph removes all tracking data for a graph.
func (dt *DegreeTracker) ClearGraph(graphID uint64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	delete(dt.degrees, graphID)
	delete(dt.dirty, graphID)
}

// InvalidateAll clears all degree data and marks known graphs as dirty.
func (dt *DegreeTracker) InvalidateAll() {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	for gid := range dt.degrees {
		dt.dirty[gid] = true
	}
	dt.degrees = make(map[uint64]map[uint64]int)
}

// --- internal helpers ---

// ensureGraph returns the per-graph degree map, creating it if needed.
// Caller must hold dt.mu.
func (dt *DegreeTracker) ensureGraph(graphID uint64) map[uint64]int {
	g, ok := dt.degrees[graphID]
	if !ok {
		g = make(map[uint64]int)
		dt.degrees[graphID] = g
	}
	return g
}

// fullScanDegrees rebuilds degrees by scanning all edges and nodes in a graph.
func fullScanDegrees(engine *storage.StorageEngine, graphID uint64) map[uint64]int {
	degrees := make(map[uint64]int)
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return true
		}
		switch rt {
		case storage.RecL3GraphEdge:
			var edge model.HypergraphEdge
			if json.Unmarshal(data, &edge) == nil && edge.GraphID == graphID {
				for _, nid := range edge.NodeIDs {
					degrees[nid]++
				}
			}
		case storage.RecL3GraphNode:
			var node model.HypergraphNode
			if json.Unmarshal(data, &node) == nil && node.GraphID == graphID {
				if _, exists := degrees[node.IDHash]; !exists {
					degrees[node.IDHash] = 0
				}
			}
		}
		return true
	})
	return degrees
}
