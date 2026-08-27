// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"fmt"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// CreateEdgeL3 creates a hyperedge; ID = hash(graphID:nodeIDs).
func CreateEdgeL3(engine *core.StorageEngine, agentID uint64, graphID string, kind core.GraphEdgeKind, nodeIDs []uint64, weight float32) (uint64, error) {
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
	if err := core.WriteHypergraphEdge(engine, agentID, edgeID, edge); err != nil {
		return 0, err
	}
	return edgeID, nil
}

func ListEdgeL3(engine *core.StorageEngine, agentID uint64, graphID string) []core.HypergraphEdge {
	graphHash, err := common.ParseID(graphID)
	if err != nil {
		return nil
	}
	var out []core.HypergraphEdge
	for _, edge := range core.CollectAllHypergraphEdges(engine, agentID) {
		if edge.GraphID == graphHash {
			out = append(out, edge)
		}
	}
	return out
}

// CreateGraphL3 imports/creates a hypergraph; ID = hash(name).
func CreateGraphL3(engine *core.StorageEngine, agentID uint64, name string, source core.HypergraphSource) (uint64, error) {
	graphID := common.HashID(name)
	now := time.Now().UnixMilli()
	slot := &core.HypergraphSlot{
		IDHash:    graphID,
		Name:      name,
		Source:    source,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := core.WriteGraphSlot(engine, agentID, graphID, slot); err != nil {
		return 0, err
	}
	return graphID, nil
}

// DeleteGraphL3 cascades: collects all nodes/edges of the graph plus the
// graph record and deletes them in one batch.
func DeleteGraphL3(engine *core.StorageEngine, agentID uint64, id string) bool {
	graphHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	var targets []uint64
	for _, node := range core.CollectAllHypergraphNodes(engine, agentID) {
		if node.GraphID == graphHash {
			targets = append(targets, node.IDHash)
		}
	}
	for _, edge := range core.CollectAllHypergraphEdges(engine, agentID) {
		if edge.GraphID == graphHash {
			targets = append(targets, edge.IDHash)
		}
	}
	targets = append(targets, graphHash)
	_, err = engine.DeleteRecordBatch(agentID, targets)
	return err == nil
}

// UpdateGraphL3 partially updates a graph slot (currently Name only).
func UpdateGraphL3(engine *core.StorageEngine, agentID uint64, id string, name *string) (*core.HypergraphSlot, error) {
	graphHash, err := parseGraphID(id)
	if err != nil {
		return nil, err
	}
	slot, err := core.ReadGraphSlot(engine, agentID, graphHash)
	if err != nil {
		return nil, err
	}
	if name != nil {
		slot.Name = *name
	}
	slot.UpdatedAt = time.Now().UnixMilli()
	if err := core.WriteGraphSlot(engine, agentID, graphHash, slot); err != nil {
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
func CreateNodeL3(engine *core.StorageEngine, agentID uint64, graphID, title, nodeType, content string, keywords []string) (uint64, error) {
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
	if err := core.WriteHypergraphNode(engine, agentID, nodeID, node); err != nil {
		return 0, err
	}
	return nodeID, nil
}

// NodeIDL3 derives the stable node ID from a graph ID and node title.
func NodeIDL3(graphID, title string) uint64 {
	return common.HashID(fmt.Sprintf("%s:%s", graphID, title))
}

func ListNodeL3(engine *core.StorageEngine, agentID uint64, graphID string) []core.HypergraphNode {
	graphHash, err := common.ParseID(graphID)
	if err != nil {
		return nil
	}
	var out []core.HypergraphNode
	for _, node := range core.CollectAllHypergraphNodes(engine, agentID) {
		if node.GraphID == graphHash {
			out = append(out, node)
		}
	}
	return out
}

// MutateNodeL3 reads one node, applies mutate and writes it back; the merge
// policy itself belongs to the caller (cap/knowledge), so this module keeps
// record access and membership validation only.
func MutateNodeL3(engine *core.StorageEngine, agentID uint64, graphID, title string, mutate func(*core.HypergraphNode)) (uint64, error) {
	graphHash, err := parseGraphID(graphID)
	if err != nil {
		return 0, err
	}
	nodeID := NodeIDL3(graphID, title)
	node, err := core.ReadHypergraphNode(engine, agentID, nodeID)
	if err != nil {
		return 0, err
	}
	if node.GraphID != graphHash {
		return 0, common.NewError(common.ErrNotFound, "node graph mismatch")
	}
	mutate(node)
	if err := core.WriteHypergraphNode(engine, agentID, nodeID, node); err != nil {
		return 0, err
	}
	return nodeID, nil
}
