// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// CreateEdgeL3 creates a hyperedge; ID = hash(graphID:nodeIDs).
func CreateEdgeL3(engine *core.StorageEngine, graphID string, kind core.GraphEdgeKind, nodeIDs []uint64, weight float32) (uint64, error) {
	graphHash, err := parseGraphID(graphID)
	if err != nil {
		return 0, err
	}
	edgeID := common.HashID(fmt.Sprintf("%s:%v", graphID, nodeIDs))
	edge := &core.HypergraphEdge{
		IDHash:    edgeID,
		GraphID:   graphHash,
		Kind:      kind,
		NodeIDs:   nodeIDs,
		Weight:    weight,
		CreatedAt: time.Now().UnixMilli(),
	}
	if err := core.WriteHypergraphEdge(engine, edgeID, edge); err != nil {
		return 0, err
	}
	return edgeID, nil
}

func ListEdgeL3(engine *core.StorageEngine, graphID string) []core.HypergraphEdge {
	graphHash, err := common.ParseID(graphID)
	if err != nil {
		return nil
	}
	var out []core.HypergraphEdge
	for _, edge := range core.CollectAllHypergraphEdges(engine) {
		if edge.GraphID == graphHash {
			out = append(out, edge)
		}
	}
	return out
}

// CreateGraphL3 imports/creates a hypergraph; ID = hash(name).
func CreateGraphL3(engine *core.StorageEngine, name string, source core.HypergraphSource) (uint64, error) {
	graphID := common.HashID(name)
	now := time.Now().UnixMilli()
	slot := &core.HypergraphSlot{
		IDHash:    graphID,
		Name:      name,
		Source:    source,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := core.WriteGraphSlot(engine, graphID, slot); err != nil {
		return 0, err
	}
	return graphID, nil
}

func ListGraphsL3(engine *core.StorageEngine) []core.HypergraphSlot {
	return core.CollectAllGraphSlots(engine)
}

// DeleteGraphL3 cascades: collects all nodes/edges of the graph plus the
// graph record and deletes them in one batch.
func DeleteGraphL3(engine *core.StorageEngine, id string) bool {
	graphHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	var targets []uint64
	for _, node := range core.CollectAllHypergraphNodes(engine) {
		if node.GraphID == graphHash {
			targets = append(targets, node.IDHash)
		}
	}
	for _, edge := range core.CollectAllHypergraphEdges(engine) {
		if edge.GraphID == graphHash {
			targets = append(targets, edge.IDHash)
		}
	}
	targets = append(targets, graphHash)
	_, err = engine.DeleteRecordBatch(targets)
	return err == nil
}

// UpdateGraphL3 partially updates a graph slot (currently Name only).
func UpdateGraphL3(engine *core.StorageEngine, id string, name *string) (*core.HypergraphSlot, error) {
	graphHash, err := parseGraphID(id)
	if err != nil {
		return nil, err
	}
	slot, err := core.ReadGraphSlot(engine, graphHash)
	if err != nil {
		return nil, err
	}
	if name != nil {
		slot.Name = *name
	}
	slot.UpdatedAt = time.Now().UnixMilli()
	if err := core.WriteGraphSlot(engine, graphHash, slot); err != nil {
		return nil, err
	}
	return slot, nil
}

func parseGraphID(id string) (uint64, error) {
	graphHash, err := common.ParseID(id)
	if err != nil {
		return 0, common.NewError(common.ErrInvalidQuery, "parse graph id", err)
	}
	return graphHash, nil
}

// CreateNodeL3 creates a hypergraph node; ID = hash(graphID:title).
func CreateNodeL3(engine *core.StorageEngine, graphID, title, nodeType, content string, keywords []string) (uint64, error) {
	graphHash, err := parseGraphID(graphID)
	if err != nil {
		return 0, err
	}
	nodeID := common.HashID(fmt.Sprintf("%s:%s", graphID, title))
	now := time.Now().UnixMilli()
	node := &core.HypergraphNode{
		IDHash:    nodeID,
		GraphID:   graphHash,
		Title:     title,
		NodeType:  nodeType,
		Content:   content,
		Keywords:  keywords,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := core.WriteHypergraphNode(engine, nodeID, node); err != nil {
		return 0, err
	}
	return nodeID, nil
}

// NodeIDL3 derives the stable node ID from a graph ID and node title.
func NodeIDL3(graphID, title string) uint64 {
	return common.HashID(fmt.Sprintf("%s:%s", graphID, title))
}

// MergeNodeL3 merges imported fields into an existing node. Empty imported
// values keep the existing content; non-empty NodeType replaces, Content is
// appended when it adds new information, and keywords are unioned.
func MergeNodeL3(engine *core.StorageEngine, graphID, title, nodeType, content string, keywords []string) (uint64, error) {
	graphHash, err := parseGraphID(graphID)
	if err != nil {
		return 0, err
	}
	nodeID := NodeIDL3(graphID, title)
	node, err := core.ReadHypergraphNode(engine, nodeID)
	if err != nil {
		return 0, err
	}
	if node.GraphID != graphHash {
		return 0, common.NewError(common.ErrNotFound, "node graph mismatch")
	}
	if nodeType != "" {
		node.NodeType = nodeType
	}
	node.Content = mergeNodeContent(node.Content, content)
	node.Keywords = mergeKeywords(node.Keywords, keywords)
	node.UpdatedAt = time.Now().UnixMilli()
	if err := core.WriteHypergraphNode(engine, nodeID, node); err != nil {
		return 0, err
	}
	return nodeID, nil
}

// OverwriteNodeL3 replaces an existing node's mutable fields with the
// imported values. The ID and graph membership are stable.
func OverwriteNodeL3(engine *core.StorageEngine, graphID, title, nodeType, content string, keywords []string) (uint64, error) {
	graphHash, err := parseGraphID(graphID)
	if err != nil {
		return 0, err
	}
	nodeID := NodeIDL3(graphID, title)
	node, err := core.ReadHypergraphNode(engine, nodeID)
	if err != nil {
		return 0, err
	}
	if node.GraphID != graphHash {
		return 0, common.NewError(common.ErrNotFound, "node graph mismatch")
	}
	node.NodeType = nodeType
	node.Content = content
	node.Keywords = keywords
	node.UpdatedAt = time.Now().UnixMilli()
	if err := core.WriteHypergraphNode(engine, nodeID, node); err != nil {
		return 0, err
	}
	return nodeID, nil
}

func mergeNodeContent(oldContent, newContent string) string {
	switch {
	case newContent == "":
		return oldContent
	case oldContent == "":
		return newContent
	case oldContent == newContent || strings.Contains(oldContent, newContent):
		return oldContent
	case strings.Contains(newContent, oldContent):
		return newContent
	default:
		return oldContent + "\n" + newContent
	}
}

func mergeKeywords(existing, imported []string) []string {
	out := make([]string, 0, len(existing)+len(imported))
	out = append(out, existing...)
	seen := make(map[string]struct{}, len(out))
	for _, kw := range out {
		seen[kw] = struct{}{}
	}
	for _, kw := range imported {
		if _, ok := seen[kw]; ok {
			continue
		}
		seen[kw] = struct{}{}
		out = append(out, kw)
	}
	return out
}

func ListNodeL3(engine *core.StorageEngine, graphID string) []core.HypergraphNode {
	graphHash, err := common.ParseID(graphID)
	if err != nil {
		return nil
	}
	var out []core.HypergraphNode
	for _, node := range core.CollectAllHypergraphNodes(engine) {
		if node.GraphID == graphHash {
			out = append(out, node)
		}
	}
	return out
}

// MatchL3Graphs returns graph IDs whose nodes are mentioned by the query
// keywords or text. Search attaches these graph IDs to a new topic as
// L3Refs, which is what makes DirectedL3ID scoping work.
func MatchL3Graphs(engine *core.StorageEngine, keywords []string, text string) []uint64 {
	query := strings.ToLower(strings.TrimSpace(text))
	terms := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" {
			terms = append(terms, kw)
		}
	}

	var graphIDs []uint64
	for _, node := range core.CollectAllHypergraphNodes(engine) {
		if !nodeMatchesQuery(node, terms, query) {
			continue
		}
		graphIDs = append(graphIDs, node.GraphID)
	}
	if len(graphIDs) == 0 {
		return nil
	}
	sort.Slice(graphIDs, func(i, j int) bool { return graphIDs[i] < graphIDs[j] })
	return common.DedupSorted(graphIDs)
}

func nodeMatchesQuery(node core.HypergraphNode, terms []string, query string) bool {
	title := strings.ToLower(node.Title)
	content := strings.ToLower(node.Content)
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(title, term) || strings.Contains(content, term) {
			return true
		}
		for _, kw := range node.Keywords {
			kw = strings.ToLower(kw)
			if strings.Contains(kw, term) || strings.Contains(term, kw) {
				return true
			}
		}
	}
	if query == "" {
		return false
	}
	if strings.Contains(query, title) && title != "" {
		return true
	}
	for _, kw := range node.Keywords {
		kw = strings.ToLower(kw)
		if kw != "" && strings.Contains(query, kw) {
			return true
		}
	}
	return false
}
