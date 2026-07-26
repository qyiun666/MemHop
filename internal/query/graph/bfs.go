// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// BFS hypergraph traversal, adjacency index building, and subgraph extraction.

package graph

import (
	"encoding/json"
	"slices"

	"github.com/qyiun666/MemHop/internal/core/model"
	"github.com/qyiun666/MemHop/internal/core/storage"
)

// Subgraph holds extracted nodes and edges from BFS.
type Subgraph struct {
	Nodes []*model.HypergraphNode
	Edges []*model.HypergraphEdge
}

// BuildAdjacencyIndex scans the engine and builds an adjacency list for a graph.
// Each node maps to a list of entries describing its connections.
func BuildAdjacencyIndex(
	engine *storage.StorageEngine,
	graphID uint64,
) map[uint64][]model.AdjacencyEntry {
	adj := make(map[uint64][]model.AdjacencyEntry)
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphEdge {
			return true
		}
		var edge model.HypergraphEdge
		if json.Unmarshal(data, &edge) != nil || edge.GraphID != graphID {
			return true
		}
		for _, nodeID := range edge.NodeIDs {
			connected := otherNodeIDs(edge.NodeIDs, nodeID)
			adj[nodeID] = append(adj[nodeID], model.AdjacencyEntry{
				NodeHash:     nodeID,
				EdgeHash:     edge.IDHash,
				Kind:         edge.Kind,
				ConnectedIDs: connected,
			})
		}
		return true
	})
	return adj
}

// otherNodeIDs returns all node IDs except the given one.
func otherNodeIDs(all []uint64, self uint64) []uint64 {
	other := make([]uint64, 0, len(all)-1)
	for _, id := range all {
		if id != self {
			other = append(other, id)
		}
	}
	return other
}

// edgeInfo bundles an edge with its node IDs for BFS expansion.
type edgeInfo struct {
	edgeHash uint64
	kind     model.GraphEdgeKind
	allIDs   []uint64
}

// buildAdjacencyWithKinds builds adjacency with optional edge kind filter.
func buildAdjacencyWithKinds(
	engine *storage.StorageEngine,
	graphID uint64,
	edgeKinds []model.GraphEdgeKind,
) map[uint64][]edgeInfo {
	adj := make(map[uint64][]edgeInfo)
	filter := len(edgeKinds) > 0
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphEdge {
			return true
		}
		var edge model.HypergraphEdge
		if json.Unmarshal(data, &edge) != nil || edge.GraphID != graphID {
			return true
		}
		if filter && !slices.Contains(edgeKinds, edge.Kind) {
			return true
		}
		info := edgeInfo{edgeHash: edge.IDHash, kind: edge.Kind, allIDs: edge.NodeIDs}
		for _, nodeID := range edge.NodeIDs {
			adj[nodeID] = append(adj[nodeID], info)
		}
		return true
	})
	return adj
}

// BFSWithAdjacency performs BFS from startNode using a pre-built adjacency map.
// Returns nodes grouped by depth layer (index 0 = depth 1).
func BFSWithAdjacency(
	adjacency map[uint64][]model.AdjacencyEntry,
	startNode uint64,
	maxDepth int,
	edgeKindFilter []model.GraphEdgeKind,
) [][]uint64 {
	if maxDepth <= 0 {
		return nil
	}
	filter := len(edgeKindFilter) > 0
	nodeDepth := map[uint64]int{startNode: 0}
	visitedEdges := make(map[uint64]struct{})
	type qi struct {
		node  uint64
		depth int
	}
	queue := []qi{{startNode, 0}}
	layers := make([][]uint64, maxDepth)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		for _, entry := range adjacency[cur.node] {
			if _, seen := visitedEdges[entry.EdgeHash]; seen {
				continue
			}
			if filter && !slices.Contains(edgeKindFilter, entry.Kind) {
				continue
			}
			visitedEdges[entry.EdgeHash] = struct{}{}
			hopDepth := cur.depth + 1
			for _, toNode := range entry.ConnectedIDs {
				if d, ok := nodeDepth[toNode]; ok && d < hopDepth {
					continue
				}
				if _, exists := nodeDepth[toNode]; !exists {
					nodeDepth[toNode] = hopDepth
					queue = append(queue, qi{toNode, hopDepth})
					layers[hopDepth-1] = append(layers[hopDepth-1], toNode)
				}
			}
		}
	}
	return layers
}

// BFSFromEngine performs BFS traversal using the engine directly.
// Returns nodes grouped by depth layer (index 0 = depth 1).
func BFSFromEngine(
	engine *storage.StorageEngine,
	graphID uint64,
	startNode uint64,
	maxDepth int,
	edgeKinds []model.GraphEdgeKind,
) [][]uint64 {
	if maxDepth <= 0 {
		return nil
	}
	adj := buildAdjacencyWithKinds(engine, graphID, edgeKinds)
	return bfsLayers(adj, startNode, maxDepth)
}

// bfsLayers performs BFS and returns visited nodes grouped by depth.
func bfsLayers(
	adj map[uint64][]edgeInfo,
	startNode uint64,
	maxDepth int,
) [][]uint64 {
	nodeDepth := map[uint64]int{startNode: 0}
	visitedEdges := make(map[uint64]struct{})
	type queueItem struct {
		node  uint64
		depth int
	}
	queue := []queueItem{{startNode, 0}}
	layers := make([][]uint64, maxDepth)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		for _, ep := range adj[cur.node] {
			if _, seen := visitedEdges[ep.edgeHash]; seen {
				continue
			}
			visitedEdges[ep.edgeHash] = struct{}{}
			hopDepth := cur.depth + 1
			for _, toNode := range ep.allIDs {
				if toNode == cur.node {
					continue
				}
				if d, ok := nodeDepth[toNode]; ok && d < hopDepth {
					continue
				}
				if _, exists := nodeDepth[toNode]; !exists {
					nodeDepth[toNode] = hopDepth
					queue = append(queue, queueItem{toNode, hopDepth})
					layers[hopDepth-1] = append(layers[hopDepth-1], toNode)
				}
			}
		}
	}
	return layers
}

// ExtractSubgraph extracts a subgraph from BFS visited nodes.
// It loads all visited nodes and all edges connecting them.
func ExtractSubgraph(
	engine *storage.StorageEngine,
	visitedNodes map[uint64]bool,
) (*Subgraph, error) {
	nodes := loadVisitedNodes(engine, visitedNodes)
	edges := loadConnectingEdges(engine, visitedNodes)
	return &Subgraph{Nodes: nodes, Edges: edges}, nil
}

// loadVisitedNodes loads model nodes for all visited hashes.
func loadVisitedNodes(
	engine *storage.StorageEngine,
	visited map[uint64]bool,
) []*model.HypergraphNode {
	var nodes []*model.HypergraphNode
	for h := range visited {
		rt, data, err := engine.ReadRecord(h)
		if err != nil || rt != storage.RecL3GraphNode {
			continue
		}
		var node model.HypergraphNode
		if json.Unmarshal(data, &node) == nil {
			nodes = append(nodes, &node)
		}
	}
	return nodes
}

// loadConnectingEdges loads edges with at least 2 endpoints in the visited set.
func loadConnectingEdges(
	engine *storage.StorageEngine,
	visited map[uint64]bool,
) []*model.HypergraphEdge {
	var edges []*model.HypergraphEdge
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphEdge {
			return true
		}
		var edge model.HypergraphEdge
		if json.Unmarshal(data, &edge) != nil {
			return true
		}
		if allNodesVisited(edge.NodeIDs, visited) {
			edges = append(edges, &edge)
		}
		return true
	})
	return edges
}

// allNodesVisited checks if at least 2 nodes of an edge are visited.
func allNodesVisited(nodeIDs []uint64, visited map[uint64]bool) bool {
	count := 0
	for _, nid := range nodeIDs {
		if visited[nid] {
			count++
		}
	}
	return count >= 2
}
