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

// CompressResult carries the timestamp bounds of one fused group, collected
// while its member topics sink a level deeper.
type CompressResult struct {
	UserTimestamp  int64 // earliest user timestamp
	AgentTimestamp int64 // latest agent timestamp
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
// parentID (collecting timestamp bounds into result) and plans the batch:
// topics at MaxDepth are deleted instead of rewritten.
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

// foldCompressBounds accumulates one sunk topic's timestamp bounds into the
// group aggregate.
func foldCompressBounds(result *CompressResult, topic *core.TopicSlot) {
	if topic.UserTimestamp < result.UserTimestamp {
		result.UserTimestamp = topic.UserTimestamp
	}
	if topic.AgentTimestamp > result.AgentTimestamp {
		result.AgentTimestamp = topic.AgentTimestamp
	}
}

// finalizeCompressResult resets untouched sentinel bounds to zero.
func finalizeCompressResult(result *CompressResult) {
	if result.UserTimestamp == math.MaxInt64 {
		result.UserTimestamp = 0
	}
	if result.AgentTimestamp == math.MinInt64 {
		result.AgentTimestamp = 0
	}
}

// Delete targets of DeleteL2.
const (
	DeleteScenesL2 uint8 = iota + 1 // ids are scenes: their topics at every depth, then the scene records
	DeleteTopicsL2                  // ids are topics
)

// DeleteL2 batch-deletes: DeleteScenesL2 treats ids as scene IDs (all topics
// of the scene plus the scene record itself); DeleteTopicsL2 treats them as
// topic IDs.
func DeleteL2(engine *core.StorageEngine, agentID uint64, ids []uint64, target uint8) bool {
	var targets []uint64
	switch target {
	case DeleteScenesL2: // scenes
		sceneSet := common.ToSet(ids)
		for _, topic := range core.CollectAllTopics(engine, agentID) {
			if _, ok := sceneSet[topic.SceneID]; ok {
				targets = append(targets, topic.ID)
			}
		}
		targets = append(targets, ids...) // the scene records themselves
	case DeleteTopicsL2: // topics
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
	return DeleteL2(engine, agentID, secondaryIDs, DeleteScenesL2)
}

// OpenSceneTurn records one Search read of a scene and opens the turn it
// starts: it bumps the usage counters and the turn counter, then returns the
// updated record so the caller reads back the seq it just allocated rather
// than a stale snapshot. Unlike the usage counters, TurnSeq is load-bearing —
// the caller hashes it into the turn's topic id, so a failed write must
// surface as an error, not a lost increment. Reads and writes of one domain
// are serialized by its lock, so no increment is ever racing away.
func OpenSceneTurn(engine *core.StorageEngine, agentID uint64, sceneID uint64, ts int64) (*core.SceneSlot, error) {
	slot, err := core.ReadSceneSlot(engine, agentID, sceneID)
	if err != nil {
		return nil, err
	}
	slot.HitCount++
	slot.LastHitAt = ts
	slot.TurnSeq++
	if err := core.WriteSceneSlot(engine, agentID, sceneID, slot); err != nil {
		return nil, err
	}
	return slot, nil
}

// ListScenesL2 reads the named scenes. An id that names no scene is skipped;
// a scene record that cannot be read is an error — a listing quietly missing
// one session is indistinguishable from a session that was deleted.
func ListScenesL2(engine *core.StorageEngine, agentID uint64, ids []uint64) ([]core.SceneSlot, error) {
	var out []core.SceneSlot
	for _, sceneHash := range ids {
		slot, err := core.ReadSceneSlot(engine, agentID, sceneHash)
		if err != nil {
			if common.CodeOf(err) == common.ErrNotFound {
				continue
			}
			return nil, err
		}
		out = append(out, *slot)
	}
	return out, nil
}

// CreateSceneL2WithID creates a scene under the ID the host owns (its session
// id). An existing scene is reused as-is — the name is only ever written on
// creation, so a repeated Search for the same session never renames it.
func CreateSceneL2WithID(engine *core.StorageEngine, agentID uint64, sceneID uint64, name string) error {
	if _, err := core.ReadSceneSlot(engine, agentID, sceneID); err == nil {
		return nil
	}
	slot := core.NewSceneSlot(sceneID, name)
	return core.WriteSceneSlot(engine, agentID, sceneID, &slot)
}

// SetSceneL3ID assigns a scene's organizational L3 domain (project/目录) id,
// but only when the scene has no domain yet. A scene already owning a domain
// (whether the same one or a different one) is left untouched, so a Directed
// route can never steal an already-anchored scene from its domain.
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
func CollectAllScenesL2(engine *core.StorageEngine, agentID uint64) ([]core.SceneSlot, error) {
	var out []core.SceneSlot
	for idHash := range engine.IndexByType(agentID, core.RecL2Scene) {
		slot, err := core.ReadSceneSlot(engine, agentID, idHash)
		if err != nil {
			// The index names this record, so the engine failing to read it is
			// not "no scenes" — reporting it keeps a corrupt domain from
			// looking like an empty one.
			if common.CodeOf(err) == common.ErrNotFound {
				continue
			}
			return nil, err
		}
		out = append(out, *slot)
	}
	if len(out) == 0 {
		return out, nil
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
	return out, nil
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
