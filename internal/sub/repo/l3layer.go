// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"fmt"
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
