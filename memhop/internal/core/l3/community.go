// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Community detection on hypergraphs via clique expansion + simplified Louvain.

package l3

import (
	"sort"

	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// CommunityConfig holds parameters for community detection.
type CommunityConfig struct {
	Resolution       float64 `json:"resolution"`         // modularity resolution (default 1.0)
	MaxHyperedgeSize int     `json:"max_hyperedge_size"` // skip hyperedges above this size
}

// DefaultCommunityConfig returns sensible defaults.
func DefaultCommunityConfig() CommunityConfig {
	return CommunityConfig{Resolution: 1.0, MaxHyperedgeSize: 10}
}

// Community is a single detected cluster.
type Community struct {
	ID      int      `json:"id"`
	NodeIDs []string `json:"node_ids"` // hex-formatted hashes
	Size    int      `json:"size"`
}

// CommunityResult holds the output of DetectCommunities.
type CommunityResult struct {
	GraphID          string      `json:"graph_id"`
	Communities      []Community `json:"communities"`
	Modularity       float64     `json:"modularity"`
	TotalNodes       int         `json:"total_nodes"`
	TotalCommunities int         `json:"total_communities"`
}

// BinaryEdge is a weighted undirected edge produced by clique expansion.
type BinaryEdge struct {
	A, B   uint64
	Weight float64
}

// neighbor is an adjacency entry used internally by Louvain.
type neighbor struct {
	idx int
	w   float64
}

// ReduceHyperedges converts hyperedges to weighted binary edges via clique expansion.
// Weight = edge.Weight / (k-1) for each pair, duplicate pairs have weights summed.
func ReduceHyperedges(edges []*model.HypergraphEdge, maxSize int) []BinaryEdge {
	type pair struct{ a, b uint64 }
	weights := make(map[pair]float64)

	for _, edge := range edges {
		k := len(edge.NodeIDs)
		if k < 2 || k > maxSize {
			continue
		}
		pw := float64(edge.Weight) / float64(k-1)
		for i := 0; i < k; i++ {
			for j := i + 1; j < k; j++ {
				a, b := edge.NodeIDs[i], edge.NodeIDs[j]
				if a > b {
					a, b = b, a
				}
				weights[pair{a, b}] += pw
			}
		}
	}
	result := make([]BinaryEdge, 0, len(weights))
	for p, w := range weights {
		result = append(result, BinaryEdge{A: p.a, B: p.b, Weight: w})
	}
	return result
}

// DetectCommunities runs Louvain community detection on an L3 graph.
func DetectCommunities(
	engine *storage.StorageEngine,
	graphID uint64,
	config CommunityConfig,
) (*CommunityResult, error) {
	edges := loadGraphEdges(engine, graphID)
	binaryEdges := ReduceHyperedges(edges, config.MaxHyperedgeSize)
	nodeHashes := collectNodeHashes(engine, graphID, binaryEdges)

	if len(nodeHashes) == 0 {
		return emptyResult(graphID), nil
	}

	partition, mod := runLouvain(nodeHashes, binaryEdges, config.Resolution)
	communities := buildCommunities(nodeHashes, partition)

	return &CommunityResult{
		GraphID:          hash.FormatHash(graphID),
		Communities:      communities,
		Modularity:       mod,
		TotalNodes:       len(nodeHashes),
		TotalCommunities: len(communities),
	}, nil
}

// --- internal helpers ---

func loadGraphEdges(engine *storage.StorageEngine, graphID uint64) []*model.HypergraphEdge {
	edges, _ := ListEdges(engine, graphID)
	return edges
}

func collectNodeHashes(
	engine *storage.StorageEngine,
	graphID uint64,
	binaryEdges []BinaryEdge,
) []uint64 {
	seen := make(map[uint64]bool)
	for _, e := range binaryEdges {
		seen[e.A] = true
		seen[e.B] = true
	}
	nodes, _ := ListNodes(engine, graphID)
	for _, n := range nodes {
		seen[n.IDHash] = true
	}
	hashes := make([]uint64, 0, len(seen))
	for h := range seen {
		hashes = append(hashes, h)
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i] < hashes[j] })
	return hashes
}

func emptyResult(graphID uint64) *CommunityResult {
	return &CommunityResult{GraphID: hash.FormatHash(graphID)}
}

// runLouvain executes simplified Louvain (local-moving phase only, up to 10 passes).
func runLouvain(
	nodes []uint64,
	edges []BinaryEdge,
	resolution float64,
) (map[uint64]int, float64) {
	n := len(nodes)
	idxMap := make(map[uint64]int, n)
	for i, h := range nodes {
		idxMap[h] = i
	}

	adj := make([][]neighbor, n)
	for _, e := range edges {
		ia, ib := idxMap[e.A], idxMap[e.B]
		adj[ia] = append(adj[ia], neighbor{ib, e.Weight})
		adj[ib] = append(adj[ib], neighbor{ia, e.Weight})
	}

	comm := make([]int, n)
	for i := range comm {
		comm[i] = i
	}

	degree := make([]float64, n)
	for i, nbrs := range adj {
		for _, nb := range nbrs {
			degree[i] += nb.w
		}
	}

	var m2 float64
	for _, d := range degree {
		m2 += d
	}
	if m2 == 0 {
		return toPartitionMap(nodes, comm), 0
	}

	for pass := 0; pass < 10; pass++ {
		if !louvainPass(n, comm, adj, degree, m2, resolution) {
			break
		}
	}

	mod := modularity(n, comm, adj, degree, m2, resolution)
	return toPartitionMap(nodes, comm), mod
}

// louvainPass runs one pass of local moving. Returns true if any node moved.
func louvainPass(
	n int,
	comm []int,
	adj [][]neighbor,
	degree []float64,
	m2 float64,
	resolution float64,
) bool {
	moved := false
	for i := 0; i < n; i++ {
		if moveNodeLouvain(i, comm, adj, degree, m2, resolution) {
			moved = true
		}
	}
	return moved
}

// moveNodeLouvain attempts to move node i to the best neighboring community.
func moveNodeLouvain(
	i int,
	comm []int,
	adj [][]neighbor,
	degree []float64,
	m2 float64,
	resolution float64,
) bool {
	oldComm := comm[i]
	commWeights := neighborCommWeights(i, comm, adj)
	ki := degree[i]

	// Gain from removing node i from old community.
	sigmaOld := communityTotalExcluding(comm, degree, oldComm, i)
	gainRemove := -2*commWeights[oldComm] + resolution*2*ki*sigmaOld/m2

	bestComm := oldComm
	bestGain := 0.0
	for c, wToC := range commWeights {
		if c == oldComm {
			continue
		}
		sigmaC := communityTotalExcluding(comm, degree, c, i)
		gainJoin := 2*wToC - resolution*2*ki*sigmaC/m2
		if total := gainRemove + gainJoin; total > bestGain {
			bestGain = total
			bestComm = c
		}
	}

	if bestComm == oldComm {
		return false
	}
	comm[i] = bestComm
	return true
}

// neighborCommWeights sums edge weights from node i to each neighboring community.
func neighborCommWeights(i int, comm []int, adj [][]neighbor) map[int]float64 {
	cw := make(map[int]float64)
	for _, nb := range adj[i] {
		cw[comm[nb.idx]] += nb.w
	}
	return cw
}

// communityTotalExcluding returns sum of degrees in community c, excluding node skip.
func communityTotalExcluding(comm []int, degree []float64, c int, skip int) float64 {
	var total float64
	for j := range comm {
		if comm[j] == c && j != skip {
			total += degree[j]
		}
	}
	return total
}

// modularity computes Q = (1/2m) Σ [A_ij - γ·k_i·k_j/(2m)] δ(c_i,c_j).
func modularity(
	n int,
	comm []int,
	adj [][]neighbor,
	degree []float64,
	m2 float64,
	resolution float64,
) float64 {
	var q float64
	for i := 0; i < n; i++ {
		for _, nb := range adj[i] {
			if comm[i] == comm[nb.idx] {
				q += nb.w - resolution*degree[i]*degree[nb.idx]/m2
			}
		}
	}
	return q / m2
}

func toPartitionMap(nodes []uint64, comm []int) map[uint64]int {
	m := make(map[uint64]int, len(nodes))
	for i, h := range nodes {
		m[h] = comm[i]
	}
	return m
}

func buildCommunities(nodes []uint64, partition map[uint64]int) []Community {
	groups := make(map[int][]uint64)
	for _, h := range nodes {
		groups[partition[h]] = append(groups[partition[h]], h)
	}
	comms := make([]Community, 0, len(groups))
	for id, members := range groups {
		hexIDs := make([]string, len(members))
		for i, h := range members {
			hexIDs[i] = hash.FormatHash(h)
		}
		comms = append(comms, Community{ID: id, NodeIDs: hexIDs, Size: len(members)})
	}
	sort.Slice(comms, func(i, j int) bool { return comms[i].Size > comms[j].Size })
	return comms
}
