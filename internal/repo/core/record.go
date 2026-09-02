// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package core provides typed Read/Write helpers for storage record types;
// typed slot access should go through this package. Every accessor is
// scoped by agentID: records of different agents never collide even when
// they share the same idHash.
package core

import (
	"encoding/json"
	"iter"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
)

func readJSON[T any](engine *StorageEngine, agentID, id uint64, label string) (*T, error) {
	_, data, err := engine.ReadRecord(agentID, id)
	if err != nil {
		return nil, err
	}
	var slot T
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, common.NewError(common.ErrDeserialization, "unmarshal "+label, err)
	}
	return &slot, nil
}

func writeJSON[T any](engine *StorageEngine, agentID uint64, rt uint8, id uint64, v *T, label string) error {
	data, err := json.Marshal(v)
	if err != nil {
		return common.NewError(common.ErrSerialization, "marshal "+label, err)
	}
	_, err = engine.WriteRecord(agentID, rt, id, data)
	return err
}

// TopicEntry builds a RecordEntry for one topic inside an agent domain;
// the single serialization point for batched L2 topic writes.
func TopicEntry(agentID uint64, topic *TopicSlot) (RecordEntry, error) {
	data, err := json.Marshal(topic)
	if err != nil {
		return RecordEntry{}, common.NewError(common.ErrSerialization, "marshal TopicSlot", err)
	}
	return RecordEntry{AgentID: agentID, RecordType: RecL2Topic, IDHash: topic.ID, Data: data}, nil
}

// IterAll iterates over all records of type rt inside one agent domain;
// corrupt or unparsable records are skipped, preserving the historical
// scan tolerance.
func IterAll[T any](engine *StorageEngine, agentID uint64, rt uint8) iter.Seq[T] {
	return func(yield func(T) bool) {
		for idHash := range engine.IndexByType(agentID, rt) {
			slot, err := readJSON[T](engine, agentID, idHash, "")
			if err != nil {
				continue // skip corrupt records; keep scanning
			}
			if !yield(*slot) {
				return
			}
		}
	}
}

func ReadProfileSlot(engine *StorageEngine, agentID, id uint64) (*ProfileSlot, error) {
	return readJSON[ProfileSlot](engine, agentID, id, "ProfileSlot")
}

func WriteProfileSlot(engine *StorageEngine, agentID, id uint64, slot *ProfileSlot) error {
	return writeJSON(engine, agentID, RecL0Profile, id, slot, "ProfileSlot")
}

func ReadSceneNode(engine *StorageEngine, agentID, id uint64) (*SceneNode, error) {
	return readJSON[SceneNode](engine, agentID, id, "SceneNode")
}

func WriteSceneNode(engine *StorageEngine, agentID, id uint64, slot *SceneNode) error {
	return writeJSON(engine, agentID, RecL1SceneNode, id, slot, "SceneNode")
}

func ReadSceneEdge(engine *StorageEngine, agentID, id uint64) (*SceneEdge, error) {
	return readJSON[SceneEdge](engine, agentID, id, "SceneEdge")
}

func WriteSceneEdge(engine *StorageEngine, agentID, id uint64, slot *SceneEdge) error {
	return writeJSON(engine, agentID, RecL1Hyperedge, id, slot, "SceneEdge")
}

func CollectAllSceneNodes(engine *StorageEngine, agentID uint64) []SceneNode {
	return slices.Collect(IterAll[SceneNode](engine, agentID, RecL1SceneNode))
}

func ReadSceneSlot(engine *StorageEngine, agentID, id uint64) (*SceneSlot, error) {
	rt, data, err := engine.ReadRecord(agentID, id)
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

func WriteSceneSlot(engine *StorageEngine, agentID, id uint64, slot *SceneSlot) error {
	return writeJSON(engine, agentID, RecL2Scene, id, slot, "SceneSlot")
}

func ReadTopicSlot(engine *StorageEngine, agentID, id uint64) (*TopicSlot, error) {
	return readJSON[TopicSlot](engine, agentID, id, "TopicSlot")
}

func WriteTopicSlot(engine *StorageEngine, agentID, id uint64, slot *TopicSlot) error {
	return writeJSON(engine, agentID, RecL2Topic, id, slot, "TopicSlot")
}

func CollectAllTopics(engine *StorageEngine, agentID uint64) []TopicSlot {
	return slices.Collect(IterAll[TopicSlot](engine, agentID, RecL2Topic))
}

// ReadTopicLenient returns (nil, nil) for non-RecL2Topic records instead of
// unmarshalling garbage.
func ReadTopicLenient(engine *StorageEngine, agentID, idHash uint64) (*TopicSlot, error) {
	rt, data, err := engine.ReadRecord(agentID, idHash)
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

func ReadHypergraphNode(engine *StorageEngine, agentID, id uint64) (*HypergraphNode, error) {
	return readJSON[HypergraphNode](engine, agentID, id, "HypergraphNode")
}

func WriteHypergraphNode(engine *StorageEngine, agentID, id uint64, slot *HypergraphNode) error {
	return writeJSON(engine, agentID, RecL3GraphNode, id, slot, "HypergraphNode")
}

func WriteHypergraphEdge(engine *StorageEngine, agentID, id uint64, slot *HypergraphEdge) error {
	return writeJSON(engine, agentID, RecL3GraphEdge, id, slot, "HypergraphEdge")
}

func ReadGraphSlot(engine *StorageEngine, agentID, id uint64) (*HypergraphSlot, error) {
	return readJSON[HypergraphSlot](engine, agentID, id, "HypergraphSlot")
}

func WriteGraphSlot(engine *StorageEngine, agentID, id uint64, slot *HypergraphSlot) error {
	return writeJSON(engine, agentID, RecL3GraphSlot, id, slot, "HypergraphSlot")
}

func CollectAllGraphSlots(engine *StorageEngine, agentID uint64) []HypergraphSlot {
	return slices.Collect(IterAll[HypergraphSlot](engine, agentID, RecL3GraphSlot))
}

func CollectAllHypergraphNodes(engine *StorageEngine, agentID uint64) []HypergraphNode {
	return slices.Collect(IterAll[HypergraphNode](engine, agentID, RecL3GraphNode))
}

func CollectAllHypergraphEdges(engine *StorageEngine, agentID uint64) []HypergraphEdge {
	return slices.Collect(IterAll[HypergraphEdge](engine, agentID, RecL3GraphEdge))
}

func ReadArchiveSlot(engine *StorageEngine, agentID, id uint64) (*ArchiveSlot, error) {
	return readJSON[ArchiveSlot](engine, agentID, id, "ArchiveSlot")
}

func WriteArchiveSlot(engine *StorageEngine, agentID, id uint64, slot *ArchiveSlot) error {
	return writeJSON(engine, agentID, RecL4Archive, id, slot, "ArchiveSlot")
}

func CollectAllArchives(engine *StorageEngine, agentID uint64) []ArchiveSlot {
	return slices.Collect(IterAll[ArchiveSlot](engine, agentID, RecL4Archive))
}

func ReadCapability(engine *StorageEngine, agentID, id uint64) (*Capability, error) {
	return readJSON[Capability](engine, agentID, id, "Capability")
}

func WriteCapability(engine *StorageEngine, agentID, id uint64, slot *Capability) error {
	return writeJSON(engine, agentID, RecL5Capability, id, slot, "Capability")
}

func CollectAllCapabilities(engine *StorageEngine, agentID uint64) []Capability {
	return slices.Collect(IterAll[Capability](engine, agentID, RecL5Capability))
}

func ReadTrajectorySlot(engine *StorageEngine, agentID, id uint64) (*TrajectorySlot, error) {
	return readJSON[TrajectorySlot](engine, agentID, id, "TrajectorySlot")
}

func WriteTrajectorySlot(engine *StorageEngine, agentID, id uint64, slot *TrajectorySlot) error {
	return writeJSON(engine, agentID, RecL6Trajectory, id, slot, "TrajectorySlot")
}

func CollectAllTrajectories(engine *StorageEngine, agentID uint64) []TrajectorySlot {
	return slices.Collect(IterAll[TrajectorySlot](engine, agentID, RecL6Trajectory))
}
