// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 mutation orchestration: combines engine writes with index updates.

package graph

import (
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
)

// NodeIndex defines L3Index operations needed by mutation functions.
// Defined as an interface to avoid circular dependency (index imports graph).
type NodeIndex interface {
	AddNode(node *model.HypergraphNode)
	RemoveNode(nodeHash uint64)
}

// DegreeManager defines DegreeTracker operations needed by mutation functions.
type DegreeManager interface {
	OnNodeAdded(graphID, nodeHash uint64)
	OnNodeDeleted(graphID, nodeHash uint64)
	OnEdgeAdded(graphID uint64, nodeIDs []uint64)
	OnEdgeDeleted(graphID uint64, nodeIDs []uint64)
	ClearGraph(graphID uint64)
}

// AddNodeWithIndexes adds an L3 node and updates all associated indexes.
func AddNodeWithIndexes(engine *storage.StorageEngine, node *model.HypergraphNode, nodeIdx NodeIndex, degree DegreeManager, cache AdjacencyCache) error {
	if err := AddNode(engine, node); err != nil {
		return err
	}
	nodeIdx.AddNode(node)
	degree.OnNodeAdded(node.GraphID, node.IDHash)
	cache.Invalidate(node.GraphID)
	return nil
}

// AddEdgeWithIndexes adds an L3 edge and updates all associated indexes.
func AddEdgeWithIndexes(engine *storage.StorageEngine, edge *model.HypergraphEdge, degree DegreeManager, cache AdjacencyCache) error {
	if err := AddEdge(engine, edge); err != nil {
		return err
	}
	degree.OnEdgeAdded(edge.GraphID, edge.NodeIDs)
	cache.Invalidate(edge.GraphID)
	return nil
}

// DeleteNodeWithIndexes deletes an L3 node and updates all associated indexes.
func DeleteNodeWithIndexes(engine *storage.StorageEngine, nodeHash uint64, nodeIdx NodeIndex, degree DegreeManager, cache AdjacencyCache) error {
	node, err := GetNode(engine, nodeHash)
	if err != nil {
		return nil // node not found, nothing to do
	}
	graphID := node.GraphID
	if err := DeleteNode(engine, nodeHash); err != nil {
		return err
	}
	nodeIdx.RemoveNode(nodeHash)
	degree.OnNodeDeleted(graphID, nodeHash)
	cache.Invalidate(graphID)
	return nil
}

// DeleteEdgeWithIndexes deletes an L3 edge and updates all associated indexes.
func DeleteEdgeWithIndexes(engine *storage.StorageEngine, edgeHash uint64, degree DegreeManager, cache AdjacencyCache) error {
	edge, err := GetEdge(engine, edgeHash)
	if err != nil {
		return nil // edge not found, nothing to do
	}
	graphID := edge.GraphID
	if err := DeleteEdge(engine, edgeHash); err != nil {
		return err
	}
	degree.OnEdgeDeleted(graphID, edge.NodeIDs)
	cache.Invalidate(graphID)
	return nil
}

// DeleteGraphWithIndexes deletes an L3 graph and cleans up all indexes.
func DeleteGraphWithIndexes(engine *storage.StorageEngine, graphID uint64, degree DegreeManager, cache AdjacencyCache) error {
	if err := DeleteGraph(engine, graphID); err != nil {
		return err
	}
	cache.Invalidate(graphID)
	degree.ClearGraph(graphID)
	return nil
}
