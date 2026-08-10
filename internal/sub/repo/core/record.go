// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package record provides typed Read/Write helpers for all storage record
// types. All direct ReadRecord/WriteRecord calls against typed slots should
// go through this package.
package core

import (
	"encoding/json"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

// --- generic plumbing ---

// readJSON reads a record and deserializes it into T.
func readJSON[T any](engine *StorageEngine, id uint64, label string) (*T, error) {
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	var slot T
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, common.NewError(common.ErrDeserialization, "unmarshal "+label, err)
	}
	return &slot, nil
}

// writeJSON serializes v and writes it as a record of the given type.
func writeJSON[T any](engine *StorageEngine, rt uint8, id uint64, v *T, label string) error {
	data, err := json.Marshal(v)
	if err != nil {
		return common.NewError(common.ErrSerialization, "marshal "+label, err)
	}
	_, err = engine.WriteRecord(rt, id, data)
	return err
}

// collectAll iterates all records of the given type and loads them.
// Corrupt or unparsable records are skipped without aborting the scan.
func collectAll[T any](engine *StorageEngine, rt uint8) []T {
	var all []T
	_ = engine.IterIndexByType(rt, func(idHash uint64) error {
		slot, err := readJSON[T](engine, idHash, "")
		if err != nil {
			return nil // 单条损坏/解析失败不影响整体遍历
		}
		all = append(all, *slot)
		return nil
	})
	return all
}

// --- L0 Profile ---

// ReadProfileSlot reads and deserializes a ProfileSlot from the storage engine.
func ReadProfileSlot(engine *StorageEngine, id uint64) (*ProfileSlot, error) {
	return readJSON[ProfileSlot](engine, id, "ProfileSlot")
}

// WriteProfileSlot serializes and writes a ProfileSlot to the storage engine.
func WriteProfileSlot(engine *StorageEngine, id uint64, slot *ProfileSlot) error {
	return writeJSON(engine, RecL0Profile, id, slot, "ProfileSlot")
}

// --- L1 SceneNode ---

// ReadSceneNode reads and deserializes a SceneNode from the storage engine.
func ReadSceneNode(engine *StorageEngine, id uint64) (*SceneNode, error) {
	return readJSON[SceneNode](engine, id, "SceneNode")
}

// WriteSceneNode serializes and writes a SceneNode to the storage engine.
func WriteSceneNode(engine *StorageEngine, id uint64, slot *SceneNode) error {
	return writeJSON(engine, RecL1SceneNode, id, slot, "SceneNode")
}

// CollectAllSceneNodes iterates the engine index and loads every L1 SceneNode.
func CollectAllSceneNodes(engine *StorageEngine) []SceneNode {
	return collectAll[SceneNode](engine, RecL1SceneNode)
}

// --- L2 SceneSlot / TopicSlot ---

// ReadSceneSlot reads and deserializes a SceneSlot from the storage engine.
func ReadSceneSlot(engine *StorageEngine, id uint64) (*SceneSlot, error) {
	rt, data, err := engine.ReadRecord(id)
	if err != nil {
		return nil, err
	}
	if rt != RecL2Scene {
		return nil, common.NewError(common.ErrNotFound, "record not found")
	}
	var slot SceneSlot
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, common.NewError(common.ErrDeserialization, "unmarshal SceneSlot", err)
	}
	return &slot, nil
}

// WriteSceneSlot serializes and writes a SceneSlot to the storage engine.
func WriteSceneSlot(engine *StorageEngine, id uint64, slot *SceneSlot) error {
	return writeJSON(engine, RecL2Scene, id, slot, "SceneSlot")
}

// ReadTopicSlot reads and deserializes a TopicSlot from the storage engine,
// returning it as a single-element slice.
func ReadTopicSlot(engine *StorageEngine, id uint64) ([]TopicSlot, error) {
	slot, err := readJSON[TopicSlot](engine, id, "TopicSlot")
	if err != nil {
		return nil, err
	}
	return []TopicSlot{*slot}, nil
}

// WriteTopicSlot serializes and writes a TopicSlot to the storage engine.
func WriteTopicSlot(engine *StorageEngine, id uint64, slot *TopicSlot) error {
	return writeJSON(engine, RecL2Topic, id, slot, "TopicSlot")
}

// CollectAllTopics iterates the engine index and loads every L2 TopicSlot.
func CollectAllTopics(engine *StorageEngine) []TopicSlot {
	return collectAll[TopicSlot](engine, RecL2Topic)
}

// ReadTopicLenient reads and deserializes a TopicSlot from the engine.
// Unlike ReadTopicSlot it is lenient: a non-RecL2Topic record yields
// (nil, nil) instead of unmarshalling garbage.
func ReadTopicLenient(engine *StorageEngine, idHash uint64) (*TopicSlot, error) {
	rt, data, err := engine.ReadRecord(idHash)
	if err != nil {
		return nil, err
	}
	if rt != RecL2Topic {
		return nil, nil
	}
	var topic TopicSlot
	if err := json.Unmarshal(data, &topic); err != nil {
		return nil, err
	}
	return &topic, nil
}

// --- L3 Hypergraph ---

// ReadHypergraphNode reads and deserializes a HypergraphNode from the storage engine.
func ReadHypergraphNode(engine *StorageEngine, id uint64) (*HypergraphNode, error) {
	return readJSON[HypergraphNode](engine, id, "HypergraphNode")
}

// WriteHypergraphNode serializes and writes a HypergraphNode to the storage engine.
func WriteHypergraphNode(engine *StorageEngine, id uint64, slot *HypergraphNode) error {
	return writeJSON(engine, RecL3GraphNode, id, slot, "HypergraphNode")
}

// ReadHypergraphEdge reads and deserializes a HypergraphEdge from the storage engine.
func ReadHypergraphEdge(engine *StorageEngine, id uint64) (*HypergraphEdge, error) {
	return readJSON[HypergraphEdge](engine, id, "HypergraphEdge")
}

// WriteHypergraphEdge serializes and writes a HypergraphEdge to the storage engine.
func WriteHypergraphEdge(engine *StorageEngine, id uint64, slot *HypergraphEdge) error {
	return writeJSON(engine, RecL3GraphEdge, id, slot, "HypergraphEdge")
}

// ReadGraphSlot reads and deserializes a HypergraphSlot from the storage engine.
func ReadGraphSlot(engine *StorageEngine, id uint64) (*HypergraphSlot, error) {
	return readJSON[HypergraphSlot](engine, id, "HypergraphSlot")
}

// WriteGraphSlot serializes and writes a HypergraphSlot to the storage engine.
func WriteGraphSlot(engine *StorageEngine, id uint64, slot *HypergraphSlot) error {
	return writeJSON(engine, RecL3GraphSlot, id, slot, "HypergraphSlot")
}

// CollectAllGraphSlots iterates the engine index and loads every L3 HypergraphSlot.
func CollectAllGraphSlots(engine *StorageEngine) []HypergraphSlot {
	return collectAll[HypergraphSlot](engine, RecL3GraphSlot)
}

// CollectAllHypergraphNodes iterates the engine index and loads every L3 HypergraphNode.
func CollectAllHypergraphNodes(engine *StorageEngine) []HypergraphNode {
	return collectAll[HypergraphNode](engine, RecL3GraphNode)
}

// CollectAllHypergraphEdges iterates the engine index and loads every L3 HypergraphEdge.
func CollectAllHypergraphEdges(engine *StorageEngine) []HypergraphEdge {
	return collectAll[HypergraphEdge](engine, RecL3GraphEdge)
}

// --- L4 Archive ---

// ReadArchiveSlot reads and deserializes an ArchiveSlot from the storage engine.
func ReadArchiveSlot(engine *StorageEngine, id uint64) (*ArchiveSlot, error) {
	return readJSON[ArchiveSlot](engine, id, "ArchiveSlot")
}

// WriteArchiveSlot serializes and writes an ArchiveSlot to the storage engine.
func WriteArchiveSlot(engine *StorageEngine, id uint64, slot *ArchiveSlot) error {
	return writeJSON(engine, RecL4Archive, id, slot, "ArchiveSlot")
}

// CollectAllArchives iterates the engine index and loads every L4 ArchiveSlot.
func CollectAllArchives(engine *StorageEngine) []ArchiveSlot {
	return collectAll[ArchiveSlot](engine, RecL4Archive)
}

// --- L5 ActionChain / ActionStep ---

// ReadActionChainSlot reads and deserializes an ActionChainSlot from the storage engine.
func ReadActionChainSlot(engine *StorageEngine, id uint64) (*ActionChainSlot, error) {
	return readJSON[ActionChainSlot](engine, id, "ActionChainSlot")
}

// WriteActionChainSlot serializes and writes an ActionChainSlot to the storage engine.
func WriteActionChainSlot(engine *StorageEngine, id uint64, slot *ActionChainSlot) error {
	return writeJSON(engine, RecL5ActionChain, id, slot, "ActionChainSlot")
}

// ReadActionStep reads and deserializes an ActionStep from the storage engine.
func ReadActionStep(engine *StorageEngine, id uint64) (*ActionStep, error) {
	return readJSON[ActionStep](engine, id, "ActionStep")
}

// WriteActionStep serializes and writes an ActionStep to the storage engine.
func WriteActionStep(engine *StorageEngine, id uint64, step *ActionStep) error {
	return writeJSON(engine, RecL5ActionStep, id, step, "ActionStep")
}

// CollectAllActionChains iterates the engine index and loads every L5 ActionChainSlot.
func CollectAllActionChains(engine *StorageEngine) []ActionChainSlot {
	return collectAll[ActionChainSlot](engine, RecL5ActionChain)
}

// CollectAllActionSteps iterates the engine index and loads every L5 ActionStep.
func CollectAllActionSteps(engine *StorageEngine) []ActionStep {
	return collectAll[ActionStep](engine, RecL5ActionStep)
}
