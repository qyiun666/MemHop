// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 query operations of the sub layer: node lookup and BFS subgraph.

package sub

import (
	"strings"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// L3NodeQuery 节点条件查询。GraphID 必填；IDs / Keyword / NodeType 三者选一（IDs 优先）。
type L3NodeQuery struct {
	GraphID  string   `json:"graph_id"`
	IDs      []string `json:"ids,omitempty"`
	Keyword  string   `json:"keyword,omitempty"`
	NodeType string   `json:"node_type,omitempty"`
	Limit    int      `json:"limit,omitempty"` // <=0 不限制
}

// QueryL3Nodes 按条件查询 L3 节点；结果空时返回空切片。
func (db *DB) QueryL3Nodes(q L3NodeQuery) ([]core.HypergraphNode, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	if q.GraphID == "" {
		return nil, common.NewError(common.ErrInvalidQuery, "graph_id is required")
	}
	var out []core.HypergraphNode
	switch {
	case len(q.IDs) > 0:
		for _, id := range q.IDs {
			idHash, err := common.ParseID(id)
			if err != nil {
				continue
			}
			node, err := core.ReadHypergraphNode(db.engine, idHash)
			if err != nil {
				continue
			}
			out = append(out, *node)
		}
	case q.Keyword != "":
		kw := strings.ToLower(q.Keyword)
		for _, n := range repo.ListNodeL3(db.engine, q.GraphID) {
			if nodeMatchesKeyword(n, kw) {
				out = append(out, n)
			}
		}
	case q.NodeType != "":
		for _, n := range repo.ListNodeL3(db.engine, q.GraphID) {
			if n.NodeType == q.NodeType {
				out = append(out, n)
			}
		}
	default:
		return []core.HypergraphNode{}, nil
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	if out == nil {
		return []core.HypergraphNode{}, nil
	}
	return out, nil
}

// nodeMatchesKeyword 大小写不敏感子串匹配 Title / Keywords / Content。
func nodeMatchesKeyword(n core.HypergraphNode, kw string) bool {
	if strings.Contains(strings.ToLower(n.Title), kw) {
		return true
	}
	if strings.Contains(strings.ToLower(n.Content), kw) {
		return true
	}
	for _, k := range n.Keywords {
		if strings.Contains(strings.ToLower(k), kw) {
			return true
		}
	}
	return false
}

// L3Subgraph BFS 子图查询结果。
type L3Subgraph struct {
	Nodes []core.HypergraphNode
	Edges []core.HypergraphEdge
}

// QueryL3Subgraph 从 startNodeID 出发 BFS，返回 maxDepth 层内可达子图；
// edgeKinds 非空时仅经指定边类型可达的节点纳入；maxDepth<=0 视为 1。
func (db *DB) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []core.GraphEdgeKind) (*L3Subgraph, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	startHash, err := common.ParseID(startNodeID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse start node id", err)
	}
	if _, err := core.ReadHypergraphNode(db.engine, startHash); err != nil {
		return nil, common.NewError(common.ErrNotFound, "start node not found", err)
	}
	if maxDepth <= 0 {
		maxDepth = 1
	}

	// 邻接表：图内全部边（按 edgeKinds 过滤），超边 nodeIDs 两两互连。
	adj := make(map[uint64]map[uint64]struct{})
	var edges []core.HypergraphEdge
	for _, e := range repo.ListEdgeL3(db.engine, graphID) {
		if len(edgeKinds) > 0 && !containsEdgeKind(edgeKinds, e.Kind) {
			continue
		}
		edges = append(edges, e)
		connectNodes(adj, e.NodeIDs)
	}

	// BFS 分层遍历：maxDepth 为跳数，每轮扩展一跳。
	visited := map[uint64]struct{}{startHash: {}}
	queue := []uint64{startHash}
	for depth := 0; depth < maxDepth && len(queue) > 0; depth++ {
		var next []uint64
		for _, cur := range queue {
			for nb := range adj[cur] {
				if _, seen := visited[nb]; seen {
					continue
				}
				visited[nb] = struct{}{}
				next = append(next, nb)
			}
		}
		queue = next
	}

	// 子图提取：visited 节点 + 两端均在 visited 中的边。
	nodes := make([]core.HypergraphNode, 0, len(visited))
	for h := range visited {
		if n, err := core.ReadHypergraphNode(db.engine, h); err == nil {
			nodes = append(nodes, *n)
		}
	}
	subEdges := make([]core.HypergraphEdge, 0, len(edges))
	for _, e := range edges {
		if allNodesVisited(e.NodeIDs, visited) {
			subEdges = append(subEdges, e)
		}
	}
	return &L3Subgraph{Nodes: nodes, Edges: subEdges}, nil
}

// connectNodes 将超边 nodeIDs 两两互连（跳过自环）。
func connectNodes(adj map[uint64]map[uint64]struct{}, nodeIDs []uint64) {
	for i, a := range nodeIDs {
		for _, b := range nodeIDs[i+1:] {
			if a == b {
				continue
			}
			if adj[a] == nil {
				adj[a] = make(map[uint64]struct{})
			}
			if adj[b] == nil {
				adj[b] = make(map[uint64]struct{})
			}
			adj[a][b] = struct{}{}
			adj[b][a] = struct{}{}
		}
	}
}

func containsEdgeKind(kinds []core.GraphEdgeKind, k core.GraphEdgeKind) bool {
	for _, kk := range kinds {
		if kk == k {
			return true
		}
	}
	return false
}

func allNodesVisited(nodeIDs []uint64, visited map[uint64]struct{}) bool {
	for _, id := range nodeIDs {
		if _, ok := visited[id]; !ok {
			return false
		}
	}
	return true
}
