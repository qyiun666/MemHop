// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Subgraph extraction: BFS traversal, DTO conversion, edge kind parsing.

package graph

import (
	"strings"

	"memhop/internal/core/model"
	"memhop/internal/core/storage"
)

// AdjacencyCache defines the interface for graph adjacency caching.
// This avoids a circular dependency: internal/core/index imports graph,
// so graph cannot import index.AdjacencyCache directly.
type AdjacencyCache interface {
	Get(graphID uint64) (map[uint64][]model.AdjacencyEntry, bool)
	Put(graphID uint64, adjacency map[uint64][]model.AdjacencyEntry)
	Invalidate(graphID uint64)
}

// QuerySubgraph extracts a subgraph reachable from startNode within maxDepth hops.
// It uses the adjacency cache when available, falling back to engine BFS.
func QuerySubgraph(
	engine *storage.StorageEngine,
	cache AdjacencyCache,
	graphHash, startHash uint64,
	maxDepth int,
	kinds []model.GraphEdgeKind,
) (*Subgraph, error) {
	visited := collectBFSVisited(engine, cache, graphHash, startHash, maxDepth, kinds)
	if len(visited) == 0 {
		return &Subgraph{}, nil
	}
	return ExtractSubgraph(engine, visited)
}

// collectBFSVisited runs BFS and returns all visited node hashes (including start).
func collectBFSVisited(
	engine *storage.StorageEngine,
	cache AdjacencyCache,
	graphHash, startHash uint64,
	maxDepth int,
	kinds []model.GraphEdgeKind,
) map[uint64]bool {
	var layers [][]uint64
	if adj, ok := cache.Get(graphHash); ok {
		layers = BFSWithAdjacency(adj, startHash, maxDepth, kinds)
	} else {
		layers = BFSFromEngine(engine, graphHash, startHash, maxDepth, kinds)
		cacheAdj := BuildAdjacencyIndex(engine, graphHash)
		cache.Put(graphHash, cacheAdj)
	}
	visited := map[uint64]bool{startHash: true}
	for _, layer := range layers {
		for _, h := range layer {
			visited[h] = true
		}
	}
	return visited
}


// ParseGraphEdgeKinds converts string edge kinds to model types.
func ParseGraphEdgeKinds(kinds []string) []model.GraphEdgeKind {
	if len(kinds) == 0 {
		return nil
	}
	var parsed []model.GraphEdgeKind
	for _, s := range kinds {
		k := parseOneEdgeKind(s)
		if k != nil {
			parsed = append(parsed, *k)
		}
	}
	if len(parsed) == 0 {
		return nil
	}
	return parsed
}

func parseOneEdgeKind(s string) *model.GraphEdgeKind {
	switch strings.ToLower(s) {
	case "related":
		k := model.EdgeRelated
		return &k
	case "causal":
		k := model.EdgeCausal
		return &k
	case "part_of", "partof":
		k := model.EdgePartOf
		return &k
	case "sequence":
		k := model.EdgeSequence
		return &k
	case "dependency":
		k := model.EdgeDependency
		return &k
	case "custom":
		k := model.EdgeCustom
		return &k
	default:
		return nil
	}
}

// IsDSLQuery returns true if the input looks like a DSL query.
func IsDSLQuery(input string) bool {
	trimmed := strings.TrimSpace(strings.ToUpper(input))
	return strings.HasPrefix(trimmed, "MATCH") ||
		strings.HasPrefix(trimmed, "PATH") ||
		strings.HasPrefix(trimmed, "SUBGRAPH")
}
