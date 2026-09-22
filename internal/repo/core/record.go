// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package core provides typed Read/Write helpers for storage record types;
// typed slot access should go through this package. Every accessor is
// scoped by agentID: records of different agents never collide even when
// they share the same idHash.
package core

import (
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
)

// typeLabel names T without Go's package qualifier or pointer star, so an error
// reads "unmarshal TopicSlot" rather than "unmarshal core.TopicSlot".
func typeLabel(v any) string {
	t := strings.TrimPrefix(fmt.Sprintf("%T", v), "*")
	return t[strings.LastIndex(t, ".")+1:]
}

// readJSON decodes the record at id as T. rt is part of the read: an id names
// exactly one record type, and a typed reader that ignored the frame's type
// would decode a foreign slot into T — the caller's next write would then
// convert that record. A mismatch reports ErrNotFound, the same answer an
// absent id gives.
func readJSON[T any](engine *StorageEngine, agentID, id uint64, rt uint8) (*T, error) {
	stored, data, err := engine.ReadRecord(agentID, id)
	if err != nil {
		return nil, err
	}
	if stored != rt {
		return nil, common.NewError(common.ErrNotFound, "record not found")
	}
	var slot T
	if err := json.Unmarshal(data, &slot); err != nil {
		return nil, common.NewError(common.ErrDeserialization, "unmarshal "+typeLabel(slot), err)
	}
	return &slot, nil
}

func writeJSON[T any](engine *StorageEngine, agentID uint64, rt uint8, id uint64, v *T) error {
	data, err := json.Marshal(v)
	if err != nil {
		return common.NewError(common.ErrSerialization, "marshal "+typeLabel(v), err)
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

// IterAll iterates over all records of type rt inside one agent domain, dropping
// the ones that will not read back. A rebuild may answer from what survives; a set
// that decides a deletion or an overwrite may not, and uses CollectAllStrict.
// Each drop is logged with its id: a mirror built on this scan hands the dropped
// record's slot again. An id already tombstone but still named by the index is
// not damage and stays quiet.
func IterAll[T any](engine *StorageEngine, agentID uint64, rt uint8) iter.Seq[T] {
	return func(yield func(T) bool) {
		for idHash := range engine.IndexByType(agentID, rt) {
			slot, err := readJSON[T](engine, agentID, idHash, rt)
			if err != nil {
				if common.CodeOf(err) != common.ErrNotFound {
					slog.Warn("core: a record will not read back and is left out of the scan",
						"agent", common.FormatHash(agentID),
						"record", common.FormatHash(idHash), "err", err)
				}
				continue
			}
			if !yield(*slot) {
				return
			}
		}
	}
}

// CollectAllStrict reads one agent domain's whole rt set, reporting the first
// member that will not read back rather than skipping it: where the set decides
// a deletion or an overwrite, a member that merely would not read is not
// evidence that the domain does not hold it.
func CollectAllStrict[T any](engine *StorageEngine, agentID uint64, rt uint8) ([]T, error) {
	var out []T
	for idHash := range engine.IndexByType(agentID, rt) {
		slot, err := readJSON[T](engine, agentID, idHash, rt)
		if err != nil {
			// The index and one record read are not one atomic step, so an id the
			// sweep has already tombstoned is a legitimate hole, not a damage report.
			if common.CodeOf(err) == common.ErrNotFound {
				continue
			}
			// Named by id: this refusal stops a write, and the engine has no read
			// face that shows a damaged record, so an unnamed one would leave the
			// host with no way to find what to repair or compact away.
			return nil, common.NewError(common.CodeOf(err),
				fmt.Sprintf("record %s will not read", common.FormatHash(idHash)), err)
		}
		out = append(out, *slot)
	}
	return out, nil
}

// ReadProfileSlot re-derives the MBTI type word from the stored dimensions:
// the axes are the only fact on disk, so the word every reader sees is a
// function of them rather than a second copy that could drift.
func ReadProfileSlot(engine *StorageEngine, agentID, id uint64) (*ProfileSlot, error) {
	slot, err := readJSON[ProfileSlot](engine, agentID, id, RecL0Profile)
	if err != nil {
		return nil, err
	}
	slot.MBTI.Type = DeriveMBTIType(slot.MBTI)
	return slot, nil
}

func WriteProfileSlot(engine *StorageEngine, agentID, id uint64, slot *ProfileSlot) error {
	return writeJSON(engine, agentID, RecL0Profile, id, slot)
}

func ReadSceneNode(engine *StorageEngine, agentID, id uint64) (*SceneNode, error) {
	return readJSON[SceneNode](engine, agentID, id, RecL1SceneNode)
}

func WriteSceneNode(engine *StorageEngine, agentID, id uint64, slot *SceneNode) error {
	return writeJSON(engine, agentID, RecL1SceneNode, id, slot)
}

func ReadSceneEdge(engine *StorageEngine, agentID, id uint64) (*SceneEdge, error) {
	return readJSON[SceneEdge](engine, agentID, id, RecL1Hyperedge)
}

func WriteSceneEdge(engine *StorageEngine, agentID, id uint64, slot *SceneEdge) error {
	return writeJSON(engine, agentID, RecL1Hyperedge, id, slot)
}

func CollectAllSceneNodes(engine *StorageEngine, agentID uint64) []SceneNode {
	return slices.Collect(IterAll[SceneNode](engine, agentID, RecL1SceneNode))
}

func ReadSceneSlot(engine *StorageEngine, agentID, id uint64) (*SceneSlot, error) {
	return readJSON[SceneSlot](engine, agentID, id, RecL2Scene)
}

func WriteSceneSlot(engine *StorageEngine, agentID, id uint64, slot *SceneSlot) error {
	return writeJSON(engine, agentID, RecL2Scene, id, slot)
}

func ReadTopicSlot(engine *StorageEngine, agentID, id uint64) (*TopicSlot, error) {
	return readJSON[TopicSlot](engine, agentID, id, RecL2Topic)
}

func WriteTopicSlot(engine *StorageEngine, agentID, id uint64, slot *TopicSlot) error {
	return writeJSON(engine, agentID, RecL2Topic, id, slot)
}

// CollectAllTopicsStrict is the strict topic scan, for a caller whose next move
// deletes or rewrites records keyed on this enumeration.
func CollectAllTopicsStrict(engine *StorageEngine, agentID uint64) ([]TopicSlot, error) {
	return CollectAllStrict[TopicSlot](engine, agentID, RecL2Topic)
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
		return nil, common.NewError(common.ErrDeserialization, "unmarshal TopicSlot", err)
	}
	return &topic, nil
}

func ReadHypergraphNode(engine *StorageEngine, agentID, id uint64) (*HypergraphNode, error) {
	return readJSON[HypergraphNode](engine, agentID, id, RecL3GraphNode)
}

func WriteHypergraphNode(engine *StorageEngine, agentID, id uint64, slot *HypergraphNode) error {
	return writeJSON(engine, agentID, RecL3GraphNode, id, slot)
}

func WriteHypergraphEdge(engine *StorageEngine, agentID, id uint64, slot *HypergraphEdge) error {
	return writeJSON(engine, agentID, RecL3GraphEdge, id, slot)
}

func ReadGraphSlot(engine *StorageEngine, agentID, id uint64) (*HypergraphSlot, error) {
	return readJSON[HypergraphSlot](engine, agentID, id, RecL3GraphSlot)
}

func WriteGraphSlot(engine *StorageEngine, agentID, id uint64, slot *HypergraphSlot) error {
	return writeJSON(engine, agentID, RecL3GraphSlot, id, slot)
}

// CollectAllGraphSlots reads the pool's graph slots with the strict scan: every
// caller decides something from the list (label resolution, anchoring, listing),
// so a slot that will not read back must stop the read rather than look like a
// label the pool does not hold.
func CollectAllGraphSlots(engine *StorageEngine, agentID uint64) ([]HypergraphSlot, error) {
	return CollectAllStrict[HypergraphSlot](engine, agentID, RecL3GraphSlot)
}

func ReadArchiveSlot(engine *StorageEngine, agentID, id uint64) (*ArchiveSlot, error) {
	return readJSON[ArchiveSlot](engine, agentID, id, RecL4Archive)
}

func WriteArchiveSlot(engine *StorageEngine, agentID, id uint64, slot *ArchiveSlot) error {
	return writeJSON(engine, agentID, RecL4Archive, id, slot)
}

func CollectAllArchives(engine *StorageEngine, agentID uint64) []ArchiveSlot {
	return slices.Collect(IterAll[ArchiveSlot](engine, agentID, RecL4Archive))
}

func ReadPlanNode(engine *StorageEngine, agentID, id uint64) (*PlanNode, error) {
	return readJSON[PlanNode](engine, agentID, id, RecL5PlanNode)
}

func WritePlanNode(engine *StorageEngine, agentID, id uint64, node *PlanNode) error {
	return writeJSON(engine, agentID, RecL5PlanNode, id, node)
}

func CollectAllPlanNodes(engine *StorageEngine, agentID uint64) []PlanNode {
	return slices.Collect(IterAll[PlanNode](engine, agentID, RecL5PlanNode))
}

// CollectAllPlanNodesStrict is CollectAllPlanNodes for a caller that decides
// which nodes to tombstone from the set it reads.
func CollectAllPlanNodesStrict(engine *StorageEngine, agentID uint64) ([]PlanNode, error) {
	return CollectAllStrict[PlanNode](engine, agentID, RecL5PlanNode)
}
