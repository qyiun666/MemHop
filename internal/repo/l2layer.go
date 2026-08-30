// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene record primitives plus topic compression planning.
package repo

import (
	"math"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// MaxDepth: topic depth threshold that triggers deletion on sinking.
const MaxDepth = 4

type CompressResult struct {
	L3Refs         []uint64 // deduplicated merged L3 refs
	UserTimestamp  int64    // earliest user timestamp
	AgentTimestamp int64    // latest agent timestamp
}

// CompressTopicsL2 compresses topics under a scene.
func CompressTopicsL2(engine *core.StorageEngine, agentID uint64, ids []uint64, parentID uint64) (*CompressResult, error) {
	result := &CompressResult{
		UserTimestamp:  math.MaxInt64,
		AgentTimestamp: math.MinInt64,
	}
	writes, deletes, err := planCompressedWrites(engine, agentID, ids, parentID, result)
	if err != nil {
		return result, err
	}
	if len(writes) > 0 {
		if _, err := engine.WriteRecordBatch(writes); err != nil {
			return result, err
		}
	}
	if len(deletes) > 0 {
		if _, err := engine.DeleteRecordBatch(agentID, deletes); err != nil {
			return result, err
		}
	}
	finalizeCompressResult(result)
	return result, nil
}

// planCompressedWrites sinks every existing topic one level deeper under
// parentID (collecting its L3 refs and timestamp bounds into result) and
// plans the batch: topics at MaxDepth are deleted instead of rewritten.
func planCompressedWrites(engine *core.StorageEngine, agentID uint64, ids []uint64, parentID uint64, result *CompressResult) ([]core.RecordEntry, []uint64, error) {
	var writes []core.RecordEntry
	var deletes []uint64
	for _, id := range ids {
		topic, err := core.ReadTopicLenient(engine, agentID, id)
		if err != nil || topic == nil {
			continue // skip missing or non-topic records
		}
		topic.Depth++
		topic.ParentID = &parentID
		foldCompressBounds(result, topic)
		if topic.Depth >= MaxDepth {
			deletes = append(deletes, topic.ID)
			continue
		}
		entry, err := core.TopicEntry(agentID, topic)
		if err != nil {
			return nil, nil, err
		}
		writes = append(writes, entry)
	}
	return writes, deletes, nil
}

// foldCompressBounds accumulates one sunk topic into the group aggregate.
func foldCompressBounds(result *CompressResult, topic *core.TopicSlot) {
	result.L3Refs = append(result.L3Refs, topic.L3Refs...)
	if topic.UserTimestamp < result.UserTimestamp {
		result.UserTimestamp = topic.UserTimestamp
	}
	if topic.AgentTimestamp > result.AgentTimestamp {
		result.AgentTimestamp = topic.AgentTimestamp
	}
}

// finalizeCompressResult resets untouched sentinel bounds to zero and
// deduplicates the collected L3 refs.
func finalizeCompressResult(result *CompressResult) {
	if result.UserTimestamp == math.MaxInt64 {
		result.UserTimestamp = 0
	}
	if result.AgentTimestamp == math.MinInt64 {
		result.AgentTimestamp = 0
	}
	result.L3Refs = common.DedupSorted(result.L3Refs)
}

// DeleteL2 batch-deletes: num==1 treats ids as scene IDs (all topics of the
// scene plus the scene record itself); num==2 treats them as topic IDs.
func DeleteL2(engine *core.StorageEngine, agentID uint64, ids []uint64, num uint8) bool {
	var targets []uint64
	switch num {
	case 1: // scenes
		sceneSet := common.ToSet(ids)
		for _, topic := range core.CollectAllTopics(engine, agentID) {
			if _, ok := sceneSet[topic.SceneID]; ok {
				targets = append(targets, topic.ID)
			}
		}
		targets = append(targets, ids...) // the scene records themselves
	case 2: // topics
		idSet := common.ToSet(ids)
		for _, topic := range core.CollectAllTopics(engine, agentID) {
			if _, ok := idSet[topic.ID]; ok {
				targets = append(targets, topic.ID)
			}
		}
	default:
		return false
	}
	_, err := engine.DeleteRecordBatch(agentID, targets)
	return err == nil
}

// MergeScenesL2 rewrites topics of the secondary scenes to the primary
// scene in one batch, then deletes the secondary scene records (now empty).
func MergeScenesL2(engine *core.StorageEngine, agentID uint64, primaryID uint64, secondaryIDs []uint64) bool {
	secondarySet := common.ToSet(secondaryIDs)
	var writes []core.RecordEntry
	for _, topic := range core.CollectAllTopics(engine, agentID) {
		if _, ok := secondarySet[topic.SceneID]; !ok {
			continue
		}
		topic.SceneID = primaryID
		entry, err := core.TopicEntry(agentID, &topic)
		if err != nil {
			return false
		}
		writes = append(writes, entry)
	}
	if len(writes) > 0 {
		if _, err := engine.WriteRecordBatch(writes); err != nil {
			return false
		}
	}
	return DeleteL2(engine, agentID, secondaryIDs, 1)
}

// TouchSceneUsage bumps the scene record's retrieval-hit counters (folded
// from the former L6 usage record into SceneSlot). Best-effort by design:
// concurrent hits may lose an increment; Dream only distinguishes
// HitCount == 0, so the impact is nil.
func TouchSceneUsage(engine *core.StorageEngine, agentID uint64, sceneID uint64, ts int64) error {
	slot, err := core.ReadSceneSlot(engine, agentID, sceneID)
	if err != nil {
		return err
	}
	slot.HitCount++
	slot.LastHitAt = ts
	return core.WriteSceneSlot(engine, agentID, sceneID, slot)
}

func ListScenesL2(engine *core.StorageEngine, agentID uint64, ids []uint64) []core.SceneSlot {
	var out []core.SceneSlot
	for _, sceneHash := range ids {
		slot, err := core.ReadSceneSlot(engine, agentID, sceneHash)
		if err != nil {
			continue
		}
		out = append(out, *slot)
	}
	return out
}

// CreateSceneL2 creates a scene; the ID is the hash of the name.
func CreateSceneL2(engine *core.StorageEngine, agentID uint64, name string) (uint64, error) {
	slot := core.NewSceneSlot(name)
	if err := core.WriteSceneSlot(engine, agentID, slot.SceneID, &slot); err != nil {
		return 0, err
	}
	return slot.SceneID, nil
}

// SetSceneL3ID assigns a scene's organizational L3 domain (project/目录) id,
// but only when the scene has no domain yet. A scene already owning a domain
// (whether this one or another) is left untouched, so a Directed route can
// never steal an already-anchored scene from its domain.
func SetSceneL3ID(engine *core.StorageEngine, agentID uint64, sceneID uint64, l3ID uint64) error {
	slot, err := core.ReadSceneSlot(engine, agentID, sceneID)
	if err != nil {
		return err
	}
	if slot.L3ID != 0 {
		return nil
	}
	slot.L3ID = l3ID
	return core.WriteSceneSlot(engine, agentID, sceneID, slot)
}

// CollectAllScenesL2 returns every scene with TopicCount set to the number
// of depth-1 root topics under it (single pass over all topics).
func CollectAllScenesL2(engine *core.StorageEngine, agentID uint64) []core.SceneSlot {
	var out []core.SceneSlot
	for idHash := range engine.IndexByType(agentID, core.RecL2Scene) {
		slot, err := core.ReadSceneSlot(engine, agentID, idHash)
		if err != nil {
			continue
		}
		out = append(out, *slot)
	}
	if len(out) == 0 {
		return out
	}
	counts := make(map[uint64]int, len(out))
	for _, topic := range core.CollectAllTopics(engine, agentID) {
		if topic.Depth == 1 {
			counts[topic.SceneID]++
		}
	}
	for i := range out {
		out[i].TopicCount = counts[out[i].SceneID]
	}
	return out
}

// RecoverDeletedScenesL2 re-appends the pre-delete payload of every L2
// scene record whose newest frame is a tombstone, returning the restored
// scene IDs. Frames reclaimed by compaction cannot be recovered.
func RecoverDeletedScenesL2(engine *core.StorageEngine, agentID uint64) ([]uint64, error) {
	payloads, err := engine.ScanDeletedPayloads(agentID, core.RecL2Scene)
	if err != nil {
		return nil, err
	}
	if len(payloads) == 0 {
		return nil, nil
	}
	writes := make([]core.RecordEntry, 0, len(payloads))
	ids := make([]uint64, 0, len(payloads))
	for id, data := range payloads {
		writes = append(writes, core.RecordEntry{AgentID: agentID, RecordType: core.RecL2Scene, IDHash: id, Data: data})
		ids = append(ids, id)
	}
	if _, err := engine.WriteRecordBatch(writes); err != nil {
		return nil, err
	}
	return ids, nil
}

// TopicClosureL2 gathers a topic, its recursive children (any depth) and the L4
// archives referenced by any of them; topics is empty when the root topic
// does not exist (DeleteTopic then reports ErrNotFound).
func TopicClosureL2(engine *core.StorageEngine, agentID uint64, root uint64) (topics, archives []uint64) {
	all := core.CollectAllTopics(engine, agentID)
	byID := make(map[uint64]core.TopicSlot, len(all))
	children := make(map[uint64][]uint64, len(all))
	for _, t := range all {
		byID[t.ID] = t
		if t.ParentID != nil {
			children[*t.ParentID] = append(children[*t.ParentID], t.ID)
		}
	}
	if _, ok := byID[root]; !ok {
		return nil, nil
	}
	topics = append(topics, root)
	for i := 0; i < len(topics); i++ {
		topics = append(topics, children[topics[i]]...)
	}
	for _, id := range topics {
		archives = append(archives, byID[id].L4Refs...)
	}
	return topics, common.DedupSorted(archives)
}
