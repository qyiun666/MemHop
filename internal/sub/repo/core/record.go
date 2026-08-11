// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package core provides typed Read/Write helpers for storage record types;
// typed slot access should go through this package.
package core

import (
	"encoding/json"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

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

func writeJSON[T any](engine *StorageEngine, rt uint8, id uint64, v *T, label string) error {
	data, err := json.Marshal(v)
	if err != nil {
		return common.NewError(common.ErrSerialization, "marshal "+label, err)
	}
	_, err = engine.WriteRecord(rt, id, data)
	return err
}

func collectAll[T any](engine *StorageEngine, rt uint8) []T {
	var all []T
	_ = engine.IterIndexByType(rt, func(idHash uint64) error {
		slot, err := readJSON[T](engine, idHash, "")
		if err != nil {
			return nil // skip corrupt records; keep scanning
		}
		all = append(all, *slot)
		return nil
	})
	return all
}

func ReadProfileSlot(engine *StorageEngine, id uint64) (*ProfileSlot, error) {
	return readJSON[ProfileSlot](engine, id, "ProfileSlot")
}

func WriteProfileSlot(engine *StorageEngine, id uint64, slot *ProfileSlot) error {
	return writeJSON(engine, RecL0Profile, id, slot, "ProfileSlot")
}

func ReadSceneNode(engine *StorageEngine, id uint64) (*SceneNode, error) {
	return readJSON[SceneNode](engine, id, "SceneNode")
}

func WriteSceneNode(engine *StorageEngine, id uint64, slot *SceneNode) error {
	return writeJSON(engine, RecL1SceneNode, id, slot, "SceneNode")
}

func CollectAllSceneNodes(engine *StorageEngine) []SceneNode {
	return collectAll[SceneNode](engine, RecL1SceneNode)
}

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

func WriteSceneSlot(engine *StorageEngine, id uint64, slot *SceneSlot) error {
	return writeJSON(engine, RecL2Scene, id, slot, "SceneSlot")
}

// ReadTopicSlot returns the topic as a single-element slice.
func ReadTopicSlot(engine *StorageEngine, id uint64) ([]TopicSlot, error) {
	slot, err := readJSON[TopicSlot](engine, id, "TopicSlot")
	if err != nil {
		return nil, err
	}
	return []TopicSlot{*slot}, nil
}

func WriteTopicSlot(engine *StorageEngine, id uint64, slot *TopicSlot) error {
	return writeJSON(engine, RecL2Topic, id, slot, "TopicSlot")
}

func CollectAllTopics(engine *StorageEngine) []TopicSlot {
	return collectAll[TopicSlot](engine, RecL2Topic)
}

// ReadTopicLenient returns (nil, nil) for non-RecL2Topic records instead of
// unmarshalling garbage.
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

func ReadHypergraphNode(engine *StorageEngine, id uint64) (*HypergraphNode, error) {
	return readJSON[HypergraphNode](engine, id, "HypergraphNode")
}

func WriteHypergraphNode(engine *StorageEngine, id uint64, slot *HypergraphNode) error {
	return writeJSON(engine, RecL3GraphNode, id, slot, "HypergraphNode")
}

func ReadHypergraphEdge(engine *StorageEngine, id uint64) (*HypergraphEdge, error) {
	return readJSON[HypergraphEdge](engine, id, "HypergraphEdge")
}

func WriteHypergraphEdge(engine *StorageEngine, id uint64, slot *HypergraphEdge) error {
	return writeJSON(engine, RecL3GraphEdge, id, slot, "HypergraphEdge")
}

func ReadGraphSlot(engine *StorageEngine, id uint64) (*HypergraphSlot, error) {
	return readJSON[HypergraphSlot](engine, id, "HypergraphSlot")
}

func WriteGraphSlot(engine *StorageEngine, id uint64, slot *HypergraphSlot) error {
	return writeJSON(engine, RecL3GraphSlot, id, slot, "HypergraphSlot")
}

func CollectAllGraphSlots(engine *StorageEngine) []HypergraphSlot {
	return collectAll[HypergraphSlot](engine, RecL3GraphSlot)
}

func CollectAllHypergraphNodes(engine *StorageEngine) []HypergraphNode {
	return collectAll[HypergraphNode](engine, RecL3GraphNode)
}

func CollectAllHypergraphEdges(engine *StorageEngine) []HypergraphEdge {
	return collectAll[HypergraphEdge](engine, RecL3GraphEdge)
}

func ReadArchiveSlot(engine *StorageEngine, id uint64) (*ArchiveSlot, error) {
	return readJSON[ArchiveSlot](engine, id, "ArchiveSlot")
}

func WriteArchiveSlot(engine *StorageEngine, id uint64, slot *ArchiveSlot) error {
	return writeJSON(engine, RecL4Archive, id, slot, "ArchiveSlot")
}

func CollectAllArchives(engine *StorageEngine) []ArchiveSlot {
	return collectAll[ArchiveSlot](engine, RecL4Archive)
}

func ReadActionChainSlot(engine *StorageEngine, id uint64) (*ActionChainSlot, error) {
	return readJSON[ActionChainSlot](engine, id, "ActionChainSlot")
}

func WriteActionChainSlot(engine *StorageEngine, id uint64, slot *ActionChainSlot) error {
	return writeJSON(engine, RecL5ActionChain, id, slot, "ActionChainSlot")
}

func ReadActionStep(engine *StorageEngine, id uint64) (*ActionStep, error) {
	return readJSON[ActionStep](engine, id, "ActionStep")
}

func WriteActionStep(engine *StorageEngine, id uint64, step *ActionStep) error {
	return writeJSON(engine, RecL5ActionStep, id, step, "ActionStep")
}

func CollectAllActionChains(engine *StorageEngine) []ActionChainSlot {
	return collectAll[ActionChainSlot](engine, RecL5ActionChain)
}

func CollectAllActionSteps(engine *StorageEngine) []ActionStep {
	return collectAll[ActionStep](engine, RecL5ActionStep)
}
