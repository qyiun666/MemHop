// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 Hypergraph CRUD operations.

package query

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
	"github.com/qyiun666/memhop/memhop/internal/hash"
	"github.com/qyiun666/memhop/memhop/internal/timeutil"
)

// GetL3 loads an L3 hypergraph by ID with all nodes and edges.
func GetL3(engine *storage.StorageEngine, id string) (*L3Detail, error) {
	graphHash, err := hash.ParseID(id)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse l3 id", err)
	}
	slot, err := loadGraphSlot(engine, graphHash)
	if err != nil {
		return nil, err
	}
	nodes, edges := collectGraphMembers(engine, graphHash)
	return &L3Detail{
		Slot:  toGraphSlot(slot),
		Nodes: nodes,
		Edges: edges,
	}, nil
}

// UpdateL3 partially updates an L3 hypergraph container.
func UpdateL3(
	engine *storage.StorageEngine,
	id string,
	fields UpdateL3Fields,
) (*L3Detail, error) {
	graphHash, err := hash.ParseID(id)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse l3 id", err)
	}
	slot, err := loadGraphSlot(engine, graphHash)
	if err != nil {
		return nil, err
	}
	if fields.Name != nil {
		slot.Name = *fields.Name
	}
	slot.UpdatedAt = timeutil.NowMs()
	slot.Version++
	if err := writeGraphSlot(engine, graphHash, slot); err != nil {
		return nil, err
	}
	return GetL3(engine, id)
}

// L3NodeRemover abstracts node removal from the L3 in-memory index.
// Implemented by *l3.L3Index; avoids a circular import.
type L3NodeRemover interface {
	RemoveNode(nodeHash uint64)
}

// DeleteL3 deletes an L3 hypergraph and cleans up L2 references.
func DeleteL3(engine *storage.StorageEngine, l3Idx L3NodeRemover, l3ID string) error {
	graphHash, err := hash.ParseID(l3ID)
	if err != nil {
		return core.NewError(core.ErrInvalidQuery, "parse l3 id", err)
	}
	l2Refs := findL2RefsToGraph(engine, graphHash)
	nodeHashes := deleteGraphMembers(engine, graphHash)
	engine.DeleteRecord(graphHash)
	removeL2GraphRefs(engine, l2Refs, graphHash)
	// Drop the graph's nodes from the in-memory index so searches
	// never return dangling IDs.
	if l3Idx != nil {
		for _, nh := range nodeHashes {
			l3Idx.RemoveNode(nh)
		}
	}
	return nil
}

// ListKnowledge lists L3 hypergraphs with pagination.
func ListKnowledge(
	engine *storage.StorageEngine,
	q KnowledgeListQuery,
) (*KnowledgeListResult, error) {
	all := collectAllGraphSlots(engine)
	filterGraphsByKeyword(&all, q.Keyword)
	sortGraphsByUpdated(all)
	skip, take := paginationParams(q.Page, q.PageSize)
	total := len(all)
	items := make([]KnowledgeSummary, 0, take)
	for i := skip; i < skip+take && i < total; i++ {
		items = append(items, graphToSummary(&all[i]))
	}
	return &KnowledgeListResult{
		Items:    items,
		Total:    total,
		Page:     q.Page,
		PageSize: q.PageSize,
		HasMore:  skip+take < total,
	}, nil
}

// GetKnowledgeNodes returns L3 nodes matching a query (by IDs, keyword, or type).
func GetKnowledgeNodes(
	engine *storage.StorageEngine,
	q KnowledgeNodeQuery,
) (*KnowledgeNodesResult, error) {
	switch {
	case q.ByIds != nil:
		return getNodesByIds(engine, q.ByIds)
	case q.ByKeyword != nil:
		return getNodesByKeyword(engine, q.ByKeyword)
	case q.ByType != nil:
		return getNodesByType(engine, q.ByType)
	default:
		return &KnowledgeNodesResult{Nodes: []KnowledgeNodeDetail{}}, nil
	}
}

// --- internal helpers ---

func loadGraphSlot(engine *storage.StorageEngine, idHash uint64) (*model.HypergraphSlot, error) {
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL3GraphSlot {
		return nil, core.ErrNotFound
	}
	var slot model.HypergraphSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "unmarshal graph slot", err)
	}
	return &slot, nil
}

func writeGraphSlot(engine *storage.StorageEngine, idHash uint64, slot *model.HypergraphSlot) error {
	data, err := json.Marshal(slot)
	if err != nil {
		return core.NewError(core.ErrSerialization, "marshal graph slot", err)
	}
	_, err = engine.WriteRecord(storage.RecL3GraphSlot, idHash, data)
	return err
}

func collectGraphMembers(
	engine *storage.StorageEngine,
	graphHash uint64,
) ([]GraphNode, []GraphEdge) {
	var nodes []GraphNode
	var edges []GraphEdge
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return true
		}
		switch rt {
		case storage.RecL3GraphNode:
			var node model.HypergraphNode
			if json.Unmarshal(data, &node) == nil && node.GraphID == graphHash {
				nodes = append(nodes, toGraphNode(&node))
			}
		case storage.RecL3GraphEdge:
			var edge model.HypergraphEdge
			if json.Unmarshal(data, &edge) == nil && edge.GraphID == graphHash {
				edges = append(edges, toGraphEdge(&edge))
			}
		}
		return true
	})
	return nodes, edges
}

func toGraphNode(n *model.HypergraphNode) GraphNode {
	return GraphNode{
		ID:         hash.FormatHash(n.IDHash),
		GraphID:    hash.FormatHash(n.GraphID),
		Title:      n.Title,
		NodeType:   n.NodeType,
		Content:    n.Content,
		Keywords:   n.Keywords,
		SourceRef:  n.SourceRef,
		Importance: n.Importance,
		Summary:    n.Summary,
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
	}
}

func toGraphEdge(e *model.HypergraphEdge) GraphEdge {
	nodeIDs := make([]string, len(e.NodeIDs))
	for i, id := range e.NodeIDs {
		nodeIDs[i] = hash.FormatHash(id)
	}
	return GraphEdge{
		ID:          hash.FormatHash(e.IDHash),
		GraphID:     hash.FormatHash(e.GraphID),
		Kind:        e.Kind,
		NodeIDs:     nodeIDs,
		Weight:      e.Weight,
		Label:       e.Label,
		Description: e.Description,
		Confidence:  e.Confidence,
		CreatedAt:   e.CreatedAt,
	}
}

func toGraphSlot(s *model.HypergraphSlot) GraphSlot {
	return GraphSlot{
		ID:        hash.FormatHash(s.IDHash),
		Name:      s.Name,
		NodeCount: s.NodeCount,
		EdgeCount: s.EdgeCount,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

func findL2RefsToGraph(engine *storage.StorageEngine, graphHash uint64) []uint64 {
	var refs []uint64
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL2Topic {
			return true
		}
		var ctx model.TopicSlot
		if json.Unmarshal(data, &ctx) == nil {
			if containsUint64(ctx.UserL3Refs, graphHash) || containsUint64(ctx.AgentL3Refs, graphHash) {
				refs = append(refs, idHash)
			}
		}
		return true
	})
	return refs
}

// deleteGraphMembers removes all node and edge records of a graph and
// returns the deleted node hashes (for in-memory index cleanup).
func deleteGraphMembers(engine *storage.StorageEngine, graphHash uint64) []uint64 {
	var toDelete []uint64
	var nodeHashes []uint64
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return true
		}
		switch rt {
		case storage.RecL3GraphNode:
			var node model.HypergraphNode
			if json.Unmarshal(data, &node) == nil && node.GraphID == graphHash {
				toDelete = append(toDelete, idHash)
				nodeHashes = append(nodeHashes, idHash)
			}
		case storage.RecL3GraphEdge:
			var edge model.HypergraphEdge
			if json.Unmarshal(data, &edge) == nil && edge.GraphID == graphHash {
				toDelete = append(toDelete, idHash)
			}
		}
		return true
	})
	for _, h := range toDelete {
		engine.DeleteRecord(h)
	}
	return nodeHashes
}

func removeL2GraphRefs(engine *storage.StorageEngine, l2IDs []uint64, graphHash uint64) {
	for _, idHash := range l2IDs {
		_, data, err := engine.ReadRecord(idHash)
		if err != nil {
			continue
		}
		var ctx model.TopicSlot
		if json.Unmarshal(data, &ctx) != nil {
			continue
		}
		ctx.UserL3Refs = removeUint64Val(ctx.UserL3Refs, graphHash)
		ctx.AgentL3Refs = removeUint64Val(ctx.AgentL3Refs, graphHash)
		ctx.UpdatedAt = timeutil.NowMs()
		writeTopic(engine, idHash, &ctx)
	}
}

func collectAllGraphSlots(engine *storage.StorageEngine) []model.HypergraphSlot {
	var all []model.HypergraphSlot
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphSlot {
			return true
		}
		var slot model.HypergraphSlot
		if json.Unmarshal(data, &slot) == nil {
			all = append(all, slot)
		}
		return true
	})
	return all
}

func filterGraphsByKeyword(all *[]model.HypergraphSlot, keyword *string) {
	if keyword == nil {
		return
	}
	kw := strings.ToLower(*keyword)
	filtered := make([]model.HypergraphSlot, 0, len(*all))
	for _, s := range *all {
		if strings.Contains(strings.ToLower(s.Name), kw) {
			filtered = append(filtered, s)
		}
	}
	*all = filtered
}

func sortGraphsByUpdated(all []model.HypergraphSlot) {
	sort.Slice(all, func(i, j int) bool {
		return all[i].UpdatedAt > all[j].UpdatedAt
	})
}

func graphToSummary(s *model.HypergraphSlot) KnowledgeSummary {
	return KnowledgeSummary{
		ID:        hash.FormatHash(s.IDHash),
		Title:     s.Name,
		Domain:    s.Name,
		UpdatedAt: s.UpdatedAt,
	}
}

func getNodesByIds(
	engine *storage.StorageEngine,
	q *ByIdsQuery,
) (*KnowledgeNodesResult, error) {
	var nodes []KnowledgeNodeDetail
	for _, idStr := range q.IDs {
		h, err := hash.ParseID(idStr)
		if err != nil {
			continue
		}
		rt, data, err := engine.ReadRecord(h)
		if err != nil || rt != storage.RecL3GraphNode {
			continue
		}
		var node model.HypergraphNode
		if json.Unmarshal(data, &node) == nil {
			nodes = append(nodes, toKnowledgeNodeDetail(&node, q.IncludeText))
		}
	}
	return &KnowledgeNodesResult{
		Nodes:     nodes,
		Total:     len(nodes),
		Requested: len(q.IDs),
	}, nil
}

func getNodesByKeyword(
	engine *storage.StorageEngine,
	q *ByKeywordQuery,
) (*KnowledgeNodesResult, error) {
	graphHash, err := hash.ParseID(q.GraphID)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse graph id", err)
	}
	kw := strings.ToLower(q.Keyword)
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	var nodes []KnowledgeNodeDetail
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphNode {
			return true
		}
		var node model.HypergraphNode
		if json.Unmarshal(data, &node) == nil && node.GraphID == graphHash {
			title := strings.ToLower(node.Title)
			if strings.Contains(title, kw) {
				nodes = append(nodes, toKnowledgeNodeDetail(&node, true))
			}
		}
		return len(nodes) < limit
	})
	return &KnowledgeNodesResult{
		Nodes:     nodes,
		Total:     len(nodes),
		Requested: limit,
	}, nil
}

func getNodesByType(
	engine *storage.StorageEngine,
	q *ByTypeQuery,
) (*KnowledgeNodesResult, error) {
	graphHash, err := hash.ParseID(q.GraphID)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse graph id", err)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	var nodes []KnowledgeNodeDetail
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphNode {
			return true
		}
		var node model.HypergraphNode
		if json.Unmarshal(data, &node) == nil && node.GraphID == graphHash && node.NodeType == q.NodeType {
			nodes = append(nodes, toKnowledgeNodeDetail(&node, true))
		}
		return len(nodes) < limit
	})
	return &KnowledgeNodesResult{
		Nodes:     nodes,
		Total:     len(nodes),
		Requested: limit,
	}, nil
}

func toKnowledgeNodeDetail(n *model.HypergraphNode, includeText bool) KnowledgeNodeDetail {
	d := KnowledgeNodeDetail{
		ID:            hash.FormatHash(n.IDHash),
		Title:         n.Title,
		Keywords:      n.Keywords,
		Domain:        hash.FormatHash(n.GraphID),
		KnowledgeType: n.NodeType,
		CreatedAt:     n.CreatedAt,
		Importance:    n.Importance,
	}
	if includeText {
		d.Text = &n.Content
	}
	return d
}

func containsUint64(ids []uint64, v uint64) bool {
	for _, id := range ids {
		if id == v {
			return true
		}
	}
	return false
}

func removeUint64Val(ids []uint64, v uint64) []uint64 {
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if id != v {
			out = append(out, id)
		}
	}
	return out
}

// ============================================================================
// BFS Graph Traversal
// ============================================================================

// BFSTraversal performs breadth-first traversal of an L3 hypergraph.
// Returns traversal hops from startNode up to maxDepth hops.
func BFSTraversal(
	engine *storage.StorageEngine,
	graphID uint64,
	startNode uint64,
	maxDepth int,
	edgeKinds []model.GraphEdgeKind,
) []TraversalHop {
	if maxDepth <= 0 {
		return nil
	}
	adj := buildAdjacency(engine, graphID, edgeKinds)
	return bfsWithAdjacency(adj, startNode, maxDepth)
}

// GraphQuery extracts a subgraph reachable from startNode within maxDepth.
func GraphQuery(
	engine *storage.StorageEngine,
	graphID string,
	startNode string,
	maxDepth int,
	edgeKinds []model.GraphEdgeKind,
) (*Subgraph, error) {
	graphHash, err := hash.ParseID(graphID)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse graph id", err)
	}
	startHash, err := hash.ParseID(startNode)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse start node", err)
	}
	hops := BFSTraversal(engine, graphHash, startHash, maxDepth, edgeKinds)
	return buildSubgraph(engine, graphHash, startHash, hops), nil
}

func buildAdjacency(
	engine *storage.StorageEngine,
	graphID uint64,
	edgeKinds []model.GraphEdgeKind,
) map[uint64][]edgeEndpoints {
	adj := make(map[uint64][]edgeEndpoints)
	kindFilter := len(edgeKinds) > 0
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphEdge {
			return true
		}
		var edge model.HypergraphEdge
		if json.Unmarshal(data, &edge) != nil || edge.GraphID != graphID {
			return true
		}
		if kindFilter && !containsEdgeKind(edgeKinds, edge.Kind) {
			return true
		}
		for _, nodeID := range edge.NodeIDs {
			adj[nodeID] = append(adj[nodeID], edgeEndpoints{edge: &edge, allIDs: edge.NodeIDs})
		}
		return true
	})
	return adj
}

type edgeEndpoints struct {
	edge   *model.HypergraphEdge
	allIDs []uint64
}

func bfsWithAdjacency(
	adj map[uint64][]edgeEndpoints,
	startNode uint64,
	maxDepth int,
) []TraversalHop {
	var hops []TraversalHop
	nodeDepth := map[uint64]int{startNode: 0}
	visitedEdges := make(map[uint64]struct{})
	queue := []struct {
		node  uint64
		depth int
	}{{startNode, 0}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		for _, ep := range adj[cur.node] {
			if _, seen := visitedEdges[ep.edge.IDHash]; seen {
				continue
			}
			visitedEdges[ep.edge.IDHash] = struct{}{}
			hopDepth := cur.depth + 1
			for _, toNode := range ep.allIDs {
				if toNode == cur.node {
					continue
				}
				if d, ok := nodeDepth[toNode]; ok && d < hopDepth {
					continue
				}
				hops = append(hops, TraversalHop{
					Depth:    hopDepth,
					FromNode: cur.node,
					Edge:     toGraphEdge(ep.edge),
					ToNode:   toNode,
				})
				if _, exists := nodeDepth[toNode]; !exists {
					nodeDepth[toNode] = hopDepth
					queue = append(queue, struct {
						node  uint64
						depth int
					}{toNode, hopDepth})
				}
			}
		}
	}
	return hops
}

func buildSubgraph(
	engine *storage.StorageEngine,
	graphHash uint64,
	startHash uint64,
	hops []TraversalHop,
) *Subgraph {
	nodeSet := map[uint64]struct{}{startHash: {}}
	edgeSet := make(map[string]struct{})
	var edges []GraphEdge
	for _, h := range hops {
		nodeSet[h.FromNode] = struct{}{}
		nodeSet[h.ToNode] = struct{}{}
		if _, ok := edgeSet[h.Edge.ID]; !ok {
			edgeSet[h.Edge.ID] = struct{}{}
			edges = append(edges, h.Edge)
		}
	}
	var nodes []GraphNode
	for nHash := range nodeSet {
		rt, data, err := engine.ReadRecord(nHash)
		if err != nil || rt != storage.RecL3GraphNode {
			continue
		}
		var node model.HypergraphNode
		if json.Unmarshal(data, &node) == nil && node.GraphID == graphHash {
			nodes = append(nodes, toGraphNode(&node))
		}
	}
	if nodes == nil {
		nodes = []GraphNode{}
	}
	if edges == nil {
		edges = []GraphEdge{}
	}
	return &Subgraph{Nodes: nodes, Edges: edges}
}

// CollectGraphMembersPublic is the exported wrapper for collectGraphMembers.
func CollectGraphMembersPublic(
	engine *storage.StorageEngine,
	graphHash uint64,
) ([]GraphNode, []GraphEdge) {
	return collectGraphMembers(engine, graphHash)
}

func containsEdgeKind(kinds []model.GraphEdgeKind, k model.GraphEdgeKind) bool {
	for _, kind := range kinds {
		if kind == k {
			return true
		}
	}
	return false
}

// ============================================================================
// L3 Knowledge Search
// ============================================================================

// L3NodeSearcher abstracts the L3 in-memory index for keyword/type search.
// Implemented by *l3.L3Index; avoids a circular import.
type L3NodeSearcher interface {
	SearchByKeyword(keyword string, limit int) []uint64
	SearchByType(nodeType string, graphID uint64, limit int) []uint64
}

// SearchL3Nodes is the unified L3 knowledge search entry point.
// Routes to keyword, type, or score-based search depending on query fields.
func SearchL3Nodes(
	idx L3NodeSearcher,
	engine *storage.StorageEngine,
	q L3SearchQuery,
) (*L3SearchResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	var nodes []uint64
	switch {
	case q.NodeType != "":
		nodes = searchByNodeType(idx, q, limit)
	case q.MinScore > 0:
		nodes = searchByMinScore(idx, engine, q, limit)
	default:
		nodes = idx.SearchByKeyword(q.Keyword, limit)
	}
	return &L3SearchResult{Nodes: nodes}, nil
}

func searchByNodeType(idx L3NodeSearcher, q L3SearchQuery, limit int) []uint64 {
	var gid uint64
	if q.GraphID != "" {
		parsed, err := hash.ParseID(q.GraphID)
		if err == nil {
			gid = parsed
		}
	}
	return idx.SearchByType(q.NodeType, gid, limit)
}

func searchByMinScore(
	idx L3NodeSearcher,
	engine *storage.StorageEngine,
	q L3SearchQuery,
	limit int,
) []uint64 {
	candidates := idx.SearchByKeyword(q.Keyword, limit*4)
	var filtered []uint64
	for _, id := range candidates {
		rt, data, err := engine.ReadRecord(id)
		if err != nil || rt != storage.RecL3GraphNode {
			continue
		}
		var node model.HypergraphNode
		if json.Unmarshal(data, &node) != nil {
			continue
		}
		if float64(node.Importance) >= q.MinScore {
			filtered = append(filtered, id)
			if len(filtered) >= limit {
				break
			}
		}
	}
	return filtered
}
