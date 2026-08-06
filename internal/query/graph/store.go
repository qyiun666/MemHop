// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package l3 implements the L3 hypergraph subsystem: node/edge CRUD,
// BFS traversal, adjacency caching, and degree tracking.
package graph

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/common/timeutil"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// maxNodeContentLen is the maximum content length for a knowledge node.
const maxNodeContentLen = 200

// CreateGraph creates a new L3 hypergraph slot and persists it.
func CreateGraph(
	engine *storage.StorageEngine,
	name string,
	source model.HypergraphSource,
) (*model.HypergraphSlot, error) {
	now := timeutil.NowMs()
	idHash := hash.HashID(name)
	slot := &model.HypergraphSlot{
		IDHash:    idHash,
		Name:      name,
		Source:    source,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := writeSlot(engine, storage.RecL3GraphSlot, idHash, slot); err != nil {
		return nil, err
	}
	return slot, nil
}

// DeleteGraph deletes an entire L3 graph: all nodes, edges, and the slot.
func DeleteGraph(engine *storage.StorageEngine, graphID uint64) error {
	if !engine.Contains(graphID) {
		return nil
	}
	nodeHashes, edgeHashes := collectMemberHashes(engine, graphID)
	for _, h := range edgeHashes {
		if _, err := engine.DeleteRecord(h); err != nil {
			return fmt.Errorf("delete graph edge %016x: %w", h, err)
		}
	}
	for _, h := range nodeHashes {
		if _, err := engine.DeleteRecord(h); err != nil {
			return fmt.Errorf("delete graph node %016x: %w", h, err)
		}
	}
	if _, err := engine.DeleteRecord(graphID); err != nil {
		return fmt.Errorf("delete graph slot %016x: %w", graphID, err)
	}
	return nil
}

// AddNode persists a hypergraph node, truncating content to 200 chars.
func AddNode(engine *storage.StorageEngine, node *model.HypergraphNode) error {
	runes := []rune(node.Content)
	if len(runes) > maxNodeContentLen {
		node.Content = string(runes[:maxNodeContentLen])
	}
	return writeSlot(engine, storage.RecL3GraphNode, node.IDHash, node)
}

// AddEdge persists a hypergraph edge.
func AddEdge(engine *storage.StorageEngine, edge *model.HypergraphEdge) error {
	return writeSlot(engine, storage.RecL3GraphEdge, edge.IDHash, edge)
}

// GetNode reads a hypergraph node by hash.
func GetNode(engine *storage.StorageEngine, nodeHash uint64) (*model.HypergraphNode, error) {
	rt, data, err := engine.ReadRecord(nodeHash)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL3GraphNode {
		return nil, mherrors.ErrNotFound
	}
	var node model.HypergraphNode
	if err := json.Unmarshal(data, &node); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "unmarshal node", err)
	}
	return &node, nil
}

// GetEdge reads a hypergraph edge by hash.
func GetEdge(engine *storage.StorageEngine, edgeHash uint64) (*model.HypergraphEdge, error) {
	rt, data, err := engine.ReadRecord(edgeHash)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL3GraphEdge {
		return nil, mherrors.ErrNotFound
	}
	var edge model.HypergraphEdge
	if err := json.Unmarshal(data, &edge); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "unmarshal edge", err)
	}
	return &edge, nil
}

// DeleteNode deletes a node and cascade-deletes all edges referencing it.
// A missing node is a no-op; any other error is propagated.
func DeleteNode(engine *storage.StorageEngine, nodeHash uint64) error {
	node, err := GetNode(engine, nodeHash)
	if err != nil {
		if errors.Is(err, mherrors.ErrNotFound) {
			return nil // node not found, nothing to do
		}
		return fmt.Errorf("delete node %016x: %w", nodeHash, err)
	}
	graphID := node.GraphID
	edgeHashes := findEdgesContainingNode(engine, graphID, nodeHash)
	for _, eh := range edgeHashes {
		if _, err := engine.DeleteRecord(eh); err != nil {
			return fmt.Errorf("delete referencing edge %016x: %w", eh, err)
		}
	}
	if _, err := engine.DeleteRecord(nodeHash); err != nil {
		return fmt.Errorf("delete node %016x: %w", nodeHash, err)
	}
	return nil
}

// DeleteEdge deletes a single edge.
func DeleteEdge(engine *storage.StorageEngine, edgeHash uint64) error {
	if _, err := engine.DeleteRecord(edgeHash); err != nil {
		return fmt.Errorf("delete edge %016x: %w", edgeHash, err)
	}
	return nil
}

// ListNodes returns all nodes belonging to a graph.
func ListNodes(engine *storage.StorageEngine, graphID uint64) ([]*model.HypergraphNode, error) {
	var nodes []*model.HypergraphNode
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphNode {
			return true
		}
		var node model.HypergraphNode
		if json.Unmarshal(data, &node) == nil && node.GraphID == graphID {
			nodes = append(nodes, &node)
		}
		return true
	})
	return nodes, nil
}

// ListEdges returns all edges belonging to a graph.
func ListEdges(engine *storage.StorageEngine, graphID uint64) ([]*model.HypergraphEdge, error) {
	var edges []*model.HypergraphEdge
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphEdge {
			return true
		}
		var edge model.HypergraphEdge
		if json.Unmarshal(data, &edge) == nil && edge.GraphID == graphID {
			edges = append(edges, &edge)
		}
		return true
	})
	return edges, nil
}

// GetGraphSlot reads a hypergraph slot by ID hash.
func GetGraphSlot(engine *storage.StorageEngine, graphID uint64) (*model.HypergraphSlot, error) {
	rt, data, err := engine.ReadRecord(graphID)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL3GraphSlot {
		return nil, mherrors.ErrNotFound
	}
	var slot model.HypergraphSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "unmarshal slot", err)
	}
	return &slot, nil
}

// --- internal helpers ---

// writeSlot serializes and writes a record to the engine.
func writeSlot(engine *storage.StorageEngine, recType uint8, idHash uint64, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return mherrors.NewError(mherrors.ErrSerialization, "marshal record", err)
	}
	_, err = engine.WriteRecord(recType, idHash, data)
	return err
}

// collectMemberHashes gathers all node and edge hashes belonging to a graph.
func collectMemberHashes(engine *storage.StorageEngine, graphID uint64) ([]uint64, []uint64) {
	var nodeHashes, edgeHashes []uint64
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return true
		}
		switch rt {
		case storage.RecL3GraphNode:
			var node model.HypergraphNode
			if json.Unmarshal(data, &node) == nil && node.GraphID == graphID {
				nodeHashes = append(nodeHashes, idHash)
			}
		case storage.RecL3GraphEdge:
			var edge model.HypergraphEdge
			if json.Unmarshal(data, &edge) == nil && edge.GraphID == graphID {
				edgeHashes = append(edgeHashes, idHash)
			}
		}
		return true
	})
	return nodeHashes, edgeHashes
}

// findEdgesContainingNode finds edge hashes that reference a given node.
func findEdgesContainingNode(
	engine *storage.StorageEngine,
	graphID uint64,
	nodeHash uint64,
) []uint64 {
	var result []uint64
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL3GraphEdge {
			return true
		}
		var edge model.HypergraphEdge
		if json.Unmarshal(data, &edge) == nil && edge.GraphID == graphID {
			for _, nid := range edge.NodeIDs {
				if nid == nodeHash {
					result = append(result, idHash)
					break
				}
			}
		}
		return true
	})
	return result
}
