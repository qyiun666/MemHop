// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dsl

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/core/model"
	"github.com/qyiun666/MemHop/internal/core/storage"
)

// Executor executes parsed DSL queries against the storage engine.
type Executor struct {
	engine *storage.StorageEngine
}

// NewExecutor creates a new DSL query executor.
func NewExecutor(engine *storage.StorageEngine) *Executor {
	return &Executor{engine: engine}
}

// Execute runs a parsed query and returns results.
func (e *Executor) Execute(query *Query) (*QueryResult, error) {
	switch {
	case query.Match != nil:
		return e.executeMatch(query.Match)
	case query.Hyperedge != nil:
		return e.executeHyperedge(query.Hyperedge)
	case query.Path != nil:
		return e.executePath(query.Path)
	case query.Subgraph != nil:
		return e.executeSubgraph(query.Subgraph)
	default:
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "empty query")
	}
}

func (e *Executor) executeMatch(m *NodeMatch) (*QueryResult, error) {
	nodes, err := e.loadAllNodes()
	if err != nil {
		return nil, err
	}
	if m.NodeType != "" {
		nodes = filterByNodeType(nodes, m.NodeType)
	}
	if m.WhereClause != nil {
		nodes = filterByWhereNode(nodes, m.WhereClause)
	}
	if m.Limit > 0 && len(nodes) > m.Limit {
		nodes = nodes[:m.Limit]
	}
	items := toNodeResults(nodes)
	return &QueryResult{Nodes: &NodeResultList{Items: items, Total: len(items)}}, nil
}

func (e *Executor) executeHyperedge(h *HyperedgeMatch) (*QueryResult, error) {
	edges, err := e.loadAllEdges()
	if err != nil {
		return nil, err
	}
	if h.WhereClause != nil {
		edges = filterByWhereEdge(edges, h.WhereClause)
	}
	if h.Limit > 0 && len(edges) > h.Limit {
		edges = edges[:h.Limit]
	}
	items := toEdgeResults(edges)
	return &QueryResult{Edges: &EdgeResultList{Items: items, Total: len(items)}}, nil
}

func (e *Executor) executePath(pq *PathQuery) (*QueryResult, error) {
	startHash, err := hash.ParseID(pq.StartNode)
	if err != nil {
		return nil, fmt.Errorf("invalid start node ID: %w", err)
	}
	nodes, err := e.loadAllNodes()
	if err != nil {
		return nil, err
	}
	edges, err := e.loadAllEdges()
	if err != nil {
		return nil, err
	}
	allowedKinds := parseEdgeKinds(pq.EdgeKinds)
	hops := bfsTraversal(nodes, edges, startHash, pq.MaxDepth, allowedKinds)
	return &QueryResult{Hops: &HopResultList{Items: hops, Total: len(hops)}}, nil
}

func (e *Executor) executeSubgraph(sq *SubgraphQuery) (*QueryResult, error) {
	startHash, err := hash.ParseID(sq.StartNode)
	if err != nil {
		return nil, fmt.Errorf("invalid start node ID: %w", err)
	}
	nodes, err := e.loadAllNodes()
	if err != nil {
		return nil, err
	}
	edges, err := e.loadAllEdges()
	if err != nil {
		return nil, err
	}
	hops := bfsTraversal(nodes, edges, startHash, sq.MaxDepth, nil)
	subNodes, subEdges := collectSubgraph(nodes, edges, hops, startHash)
	return &QueryResult{Subgraph: &SubgraphResult{Nodes: subNodes, Edges: subEdges}}, nil
}

// loadAllNodes deserializes all L3 graph node records.
func (e *Executor) loadAllNodes() ([]model.HypergraphNode, error) {
	var nodes []model.HypergraphNode
	e.engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := e.engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphNode {
			return true
		}
		var node model.HypergraphNode
		if err := json.Unmarshal(data, &node); err == nil {
			nodes = append(nodes, node)
		}
		return true
	})
	return nodes, nil
}

// loadAllEdges deserializes all L3 graph edge records.
func (e *Executor) loadAllEdges() ([]model.HypergraphEdge, error) {
	var edges []model.HypergraphEdge
	e.engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := e.engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphEdge {
			return true
		}
		var edge model.HypergraphEdge
		if err := json.Unmarshal(data, &edge); err == nil {
			edges = append(edges, edge)
		}
		return true
	})
	return edges, nil
}

func filterByNodeType(nodes []model.HypergraphNode, nt string) []model.HypergraphNode {
	var result []model.HypergraphNode
	for _, n := range nodes {
		if n.NodeType == nt {
			result = append(result, n)
		}
	}
	return result
}

func filterByWhereNode(nodes []model.HypergraphNode, cond *WhereCondition) []model.HypergraphNode {
	var result []model.HypergraphNode
	for _, n := range nodes {
		if evalNodeCondition(cond, &n) {
			result = append(result, n)
		}
	}
	return result
}

func filterByWhereEdge(edges []model.HypergraphEdge, cond *WhereCondition) []model.HypergraphEdge {
	var result []model.HypergraphEdge
	for _, e := range edges {
		if evalEdgeCondition(cond, &e) {
			result = append(result, e)
		}
	}
	return result
}

func evalNodeCondition(cond *WhereCondition, node *model.HypergraphNode) bool {
	switch {
	case cond.And != nil:
		return evalNodeCondition(cond.And.Left, node) && evalNodeCondition(cond.And.Right, node)
	case cond.Or != nil:
		return evalNodeCondition(cond.Or.Left, node) || evalNodeCondition(cond.Or.Right, node)
	case cond.TypeEquals != nil:
		return node.NodeType == *cond.TypeEquals
	case cond.KeywordContains != nil:
		return containsKeyword(node.Keywords, *cond.KeywordContains)
	case cond.PropertyCompare != nil:
		return evalNodePropertyCompare(cond.PropertyCompare, node)
	default:
		return true
	}
}

func evalEdgeCondition(cond *WhereCondition, edge *model.HypergraphEdge) bool {
	switch {
	case cond.And != nil:
		return evalEdgeCondition(cond.And.Left, edge) && evalEdgeCondition(cond.And.Right, edge)
	case cond.Or != nil:
		return evalEdgeCondition(cond.Or.Left, edge) || evalEdgeCondition(cond.Or.Right, edge)
	case cond.TypeEquals != nil:
		return strings.EqualFold(edge.Kind.String(), *cond.TypeEquals)
	case cond.PropertyCompare != nil:
		return evalEdgePropertyCompare(cond.PropertyCompare, edge)
	default:
		return true
	}
}

func evalNodePropertyCompare(pc *PropertyCompareCondition, node *model.HypergraphNode) bool {
	var val float32
	switch pc.Property {
	case "importance":
		val = node.Importance
	default:
		return false
	}
	return applyCompareOp(pc.Operator, val, pc.Value)
}

func evalEdgePropertyCompare(pc *PropertyCompareCondition, edge *model.HypergraphEdge) bool {
	var val float32
	switch pc.Property {
	case "weight":
		val = edge.Weight
	default:
		return false
	}
	return applyCompareOp(pc.Operator, val, pc.Value)
}

func applyCompareOp(op CompareOp, left, right float32) bool {
	switch op {
	case OpGt:
		return left > right
	case OpGe:
		return left >= right
	case OpLt:
		return left < right
	case OpLe:
		return left <= right
	case OpEq:
		return math.Abs(float64(left-right)) < float64(math.SmallestNonzeroFloat32)
	case OpNe:
		return math.Abs(float64(left-right)) >= float64(math.SmallestNonzeroFloat32)
	default:
		return false
	}
}

func containsKeyword(keywords []string, target string) bool {
	for _, k := range keywords {
		if strings.Contains(k, target) {
			return true
		}
	}
	return false
}

// bfsTraversal performs breadth-first traversal from startHash.
func bfsTraversal(
	nodes []model.HypergraphNode,
	edges []model.HypergraphEdge,
	startHash uint64,
	maxDepth int,
	allowedKinds map[model.GraphEdgeKind]bool,
) []HopResult {
	// Build adjacency: node_hash → list of (edge, neighbor_hash).
	adj := buildAdjacency(nodes, edges, allowedKinds)
	visited := map[uint64]bool{startHash: true}
	var hops []HopResult
	type queueItem struct {
		nodeHash uint64
		depth    int
	}
	queue := []queueItem{{startHash, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		for _, neighbor := range adj[cur.nodeHash] {
			if visited[neighbor.toHash] {
				continue
			}
			visited[neighbor.toHash] = true
			hops = append(hops, HopResult{
				FromNode: hash.FormatHash(cur.nodeHash),
				ToNode:   hash.FormatHash(neighbor.toHash),
				Edge:     neighbor.edgeResult,
				Depth:    cur.depth + 1,
			})
			queue = append(queue, queueItem{neighbor.toHash, cur.depth + 1})
		}
	}
	return hops
}

type adjEntry struct {
	toHash     uint64
	edgeResult EdgeResult
}

func buildAdjacency(
	nodes []model.HypergraphNode,
	edges []model.HypergraphEdge,
	allowedKinds map[model.GraphEdgeKind]bool,
) map[uint64][]adjEntry {
	nodeSet := make(map[uint64]bool, len(nodes))
	for _, n := range nodes {
		nodeSet[n.IDHash] = true
	}
	adj := make(map[uint64][]adjEntry)
	for _, edge := range edges {
		if allowedKinds != nil && !allowedKinds[edge.Kind] {
			continue
		}
		er := toEdgeResult(edge)
		// Hyperedges connect all pairs: each node → all other nodes in edge.
		for i, fromID := range edge.NodeIDs {
			if !nodeSet[fromID] {
				continue
			}
			for j, toID := range edge.NodeIDs {
				if i == j || !nodeSet[toID] {
					continue
				}
				adj[fromID] = append(adj[fromID], adjEntry{toHash: toID, edgeResult: er})
			}
		}
	}
	return adj
}

func collectSubgraph(
	nodes []model.HypergraphNode,
	edges []model.HypergraphEdge,
	hops []HopResult,
	startHash uint64,
) ([]NodeResult, []EdgeResult) {
	nodeHashes := map[uint64]bool{startHash: true}
	edgeIDs := map[uint64]bool{}
	for _, hop := range hops {
		from, _ := hash.ParseID(hop.FromNode)
		to, _ := hash.ParseID(hop.ToNode)
		nodeHashes[from] = true
		nodeHashes[to] = true
	}
	var subNodes []NodeResult
	for _, n := range nodes {
		if nodeHashes[n.IDHash] {
			subNodes = append(subNodes, toNodeResult(n))
		}
	}
	var subEdges []EdgeResult
	for _, edge := range edges {
		if edgeIDs[edge.IDHash] {
			continue
		}
		for _, nid := range edge.NodeIDs {
			if nodeHashes[nid] {
				subEdges = append(subEdges, toEdgeResult(edge))
				edgeIDs[edge.IDHash] = true
				break
			}
		}
	}
	return subNodes, subEdges
}

func parseEdgeKinds(kinds []string) map[model.GraphEdgeKind]bool {
	if len(kinds) == 0 {
		return nil
	}
	m := make(map[model.GraphEdgeKind]bool, len(kinds))
	for _, k := range kinds {
		switch strings.ToLower(k) {
		case "related":
			m[model.EdgeRelated] = true
		case "causal":
			m[model.EdgeCausal] = true
		case "part_of", "partof":
			m[model.EdgePartOf] = true
		case "sequence":
			m[model.EdgeSequence] = true
		case "dependency":
			m[model.EdgeDependency] = true
		case "custom":
			m[model.EdgeCustom] = true
		}
	}
	return m
}

func toNodeResults(nodes []model.HypergraphNode) []NodeResult {
	items := make([]NodeResult, len(nodes))
	for i, n := range nodes {
		items[i] = toNodeResult(n)
	}
	return items
}

func toNodeResult(n model.HypergraphNode) NodeResult {
	return NodeResult{
		IDHash: hash.FormatHash(n.IDHash), GraphID: hash.FormatHash(n.GraphID),
		Title: n.Title, NodeType: n.NodeType, Content: n.Content,
		Keywords: n.Keywords, Importance: n.Importance,
	}
}

func toEdgeResults(edges []model.HypergraphEdge) []EdgeResult {
	items := make([]EdgeResult, len(edges))
	for i, e := range edges {
		items[i] = toEdgeResult(e)
	}
	return items
}

func toEdgeResult(e model.HypergraphEdge) EdgeResult {
	hexIDs := make([]string, len(e.NodeIDs))
	for i, id := range e.NodeIDs {
		hexIDs[i] = hash.FormatHash(id)
	}
	return EdgeResult{
		IDHash: hash.FormatHash(e.IDHash), GraphID: hash.FormatHash(e.GraphID),
		Kind: e.Kind.String(), NodeIDs: hexIDs, Weight: e.Weight,
	}
}
