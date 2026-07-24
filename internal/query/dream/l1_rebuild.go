// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/core/index"
	"github.com/qyiun666/MemHop/internal/core/model"
	"github.com/qyiun666/MemHop/internal/core/record"
	"github.com/qyiun666/MemHop/internal/core/storage"
)

// RebuildL1FromL2 removes stale L1 SceneNodes whose L2 topics are gone or too deep.
func RebuildL1FromL2(
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	cfg *DecayParams,
) ([]string, error) {
	staleNodes := findStaleL1Nodes(engine, l2Meta)
	return removeStaleNodes(engine, sparseIdx, staleNodes, cfg)
}

func findStaleL1Nodes(
	engine *storage.StorageEngine,
	l2Meta *index.L2MetaIndex,
) []uint64 {
	var staleNodes []uint64
	var entries []uint64
	engine.IterIndex(func(idHash, _ uint64) bool {
		entries = append(entries, idHash)
		return true
	})

	for _, idHash := range entries {
		node := readSceneNode(engine, idHash)
		if node == nil {
			continue
		}
		if isNodeStale(node, engine, l2Meta) {
			staleNodes = append(staleNodes, idHash)
		}
	}
	return staleNodes
}

func readSceneNode(engine *storage.StorageEngine, idHash uint64) *model.SceneNode {
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil || rt != storage.RecL1SceneNode {
		return nil
	}
	var node model.SceneNode
	if json.Unmarshal(data, &node) != nil {
		return nil
	}
	return &node
}

func isNodeStale(
	node *model.SceneNode,
	engine *storage.StorageEngine,
	l2Meta *index.L2MetaIndex,
) bool {
	if len(node.TopicIDs) == 0 {
		return true
	}
	firstTopicID := node.TopicIDs[0]
	if firstTopicID == 0 || !engine.Contains(firstTopicID) {
		return true
	}
	meta := l2Meta.Get(firstTopicID)
	if meta == nil {
		return false
	}
	if meta.Depth <= 2 {
		return false
	}
	return !shouldKeepDeepNode(node, firstTopicID, meta, engine, l2Meta)
}

func shouldKeepDeepNode(
	node *model.SceneNode,
	topicID uint64,
	meta *index.L2Meta,
	engine *storage.StorageEngine,
	l2Meta *index.L2MetaIndex,
) bool {
	if meta.Depth != 3 {
		return false
	}
	topic, err := readTopic(engine, topicID)
	if err != nil || topic == nil || topic.ParentID == nil {
		return false
	}
	parentMeta := l2Meta.Get(*topic.ParentID)
	if parentMeta == nil {
		return false
	}
	return parentMeta.Depth <= 2
}

func removeStaleNodes(
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	staleNodes []uint64,
	cfg *DecayParams,
) ([]string, error) {
	var updated []string
	for _, idHash := range staleNodes {
		node := readSceneNode(engine, idHash)
		if node == nil {
			continue
		}
		for _, edgeID := range node.EdgeIDs {
			if _, err := removeNodeFromEdge(engine, edgeID, idHash, cfg); err != nil {
				return updated, err
			}
		}
		_, err := engine.DeleteRecord(idHash)
		if err != nil {
			return updated, err
		}
		sparseIdx.RemoveDocument(idHash)
		updated = append(updated, hash.FormatHash(idHash))
	}
	return updated, nil
}

func removeEdgeFromNode(engine *storage.StorageEngine, nodeID, edgeID uint64) error {
	node := readSceneNode(engine, nodeID)
	if node == nil {
		return nil
	}
	found := false
	filtered := node.EdgeIDs[:0]
	for _, e := range node.EdgeIDs {
		if e == edgeID {
			found = true
		} else {
			filtered = append(filtered, e)
		}
	}
	if !found {
		return nil
	}
	node.EdgeIDs = filtered
	return record.WriteSceneNode(engine, nodeID, node)
}

// readSceneEdge reads and deserializes a SceneEdge from the engine.
func readSceneEdge(engine *storage.StorageEngine, idHash uint64) *model.SceneEdge {
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil || rt != storage.RecL1Hyperedge {
		return nil
	}
	var edge model.SceneEdge
	if json.Unmarshal(data, &edge) != nil {
		return nil
	}
	return &edge
}

// writeSceneEdge serializes and writes a SceneEdge to the engine.
func writeSceneEdge(engine *storage.StorageEngine, id uint64, edge *model.SceneEdge) error {
	data, err := json.Marshal(edge)
	if err != nil {
		return mherrors.NewError(mherrors.ErrSerialization, "marshal scene edge", err)
	}
	_, err = engine.WriteRecord(storage.RecL1Hyperedge, id, data)
	return err
}
