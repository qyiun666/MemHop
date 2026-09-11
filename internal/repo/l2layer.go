// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene record primitives plus topic compression planning.
package repo

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// MaxDepth: topic depth threshold that triggers deletion on sinking.
const MaxDepth = 4

// CompressTopicsL2 sinks every listed topic one level deeper under parentID;
// topics reaching MaxDepth are deleted instead of rewritten. The group's own
// timestamp bounds are not collected here: a caller that needs them must be the
// one choosing the parent id they derive from, and it already has the records.
func CompressTopicsL2(engine *core.StorageEngine, agentID uint64, ids []uint64, parentID uint64) error {
	var writes []core.RecordEntry
	var deletes []uint64
	for _, id := range ids {
		topic, err := core.ReadTopicLenient(engine, agentID, id)
		switch {
		case err != nil && common.CodeOf(err) == common.ErrNotFound:
			continue // already gone: this member just drops out of the group
		case err != nil:
			// The fused parent is already on disk by the time this runs. Skipping
			// a member we could not read would leave the scene showing both the
			// group's summary and that member's own originals, so the read
			// failure is the caller's to roll back, not to swallow.
			return common.NewError(common.ErrIO, "read topic to sink", err)
		case topic == nil:
			// The ids come from the topic listing, so one naming a foreign record
			// is the cache and the disk disagreeing.
			return common.NewError(common.ErrIO, common.FormatHash(id)+" names no topic record", nil)
		}
		topic.Depth++
		topic.ParentID = &parentID
		if topic.Depth >= MaxDepth {
			deletes = append(deletes, topic.ID)
			continue
		}
		entry, err := core.TopicEntry(agentID, topic)
		if err != nil {
			return err
		}
		writes = append(writes, entry)
	}
	if len(writes) > 0 {
		if _, err := engine.WriteRecordBatch(writes); err != nil {
			return err
		}
	}
	if len(deletes) > 0 {
		if _, err := engine.DeleteRecordBatch(agentID, deletes); err != nil {
			return err
		}
	}
	return nil
}

// Delete targets of DeleteL2.
const (
	DeleteScenesL2 uint8 = iota + 1 // ids are scenes: their topics at every depth, then the scene records
	DeleteTopicsL2                  // ids are topics
)

// TopicIDsBySceneL2 enumerates every topic (any depth) owned by one of the
// given scenes. The scan is strict: the list is what a cascade tombstones
// afterwards, and a topic that merely would not read must not be dropped from it.
func TopicIDsBySceneL2(engine *core.StorageEngine, agentID uint64, sceneIDs ...uint64) ([]uint64, error) {
	topics, err := core.CollectAllTopicsStrict(engine, agentID)
	if err != nil {
		return nil, err
	}
	set := common.ToSet(sceneIDs)
	var ids []uint64
	for _, topic := range topics {
		if _, ok := set[topic.SceneID]; ok {
			ids = append(ids, topic.ID)
		}
	}
	return ids, nil
}

// DeleteL2 batch-deletes: DeleteScenesL2 treats ids as scene IDs (all topics
// of the scene plus the scene record itself); DeleteTopicsL2 treats them as
// topic IDs. An unreadable topic aborts the batch with that cause rather than
// returning a delete that left a record alive.
func DeleteL2(engine *core.StorageEngine, agentID uint64, ids []uint64, target uint8) error {
	var targets []uint64
	switch target {
	case DeleteScenesL2: // scenes
		sceneTopics, err := TopicIDsBySceneL2(engine, agentID, ids...)
		if err != nil {
			return err
		}
		targets = append(sceneTopics, ids...) // the scene records themselves
	case DeleteTopicsL2: // topics
		topics, err := core.CollectAllTopicsStrict(engine, agentID)
		if err != nil {
			return err
		}
		idSet := common.ToSet(ids)
		for _, topic := range topics {
			if _, ok := idSet[topic.ID]; ok {
				targets = append(targets, topic.ID)
			}
		}
	default:
		return common.NewError(common.ErrInvalidQuery, "unknown L2 delete target")
	}
	if len(targets) == 0 {
		return nil
	}
	_, err := engine.DeleteRecordBatch(agentID, targets)
	return err
}

// MergeScenesL2 rewrites topics of the secondary scenes to the primary
// scene in one batch, then deletes the secondary scene records (now empty).
func MergeScenesL2(engine *core.StorageEngine, agentID uint64, primaryID uint64, secondaryIDs []uint64) error {
	topics, err := core.CollectAllTopicsStrict(engine, agentID)
	if err != nil {
		return err
	}
	secondarySet := common.ToSet(secondaryIDs)
	var writes []core.RecordEntry
	for _, topic := range topics {
		if _, ok := secondarySet[topic.SceneID]; !ok {
			continue
		}
		topic.SceneID = primaryID
		entry, err := core.TopicEntry(agentID, &topic)
		if err != nil {
			return err
		}
		writes = append(writes, entry)
	}
	if len(writes) > 0 {
		if _, err := engine.WriteRecordBatch(writes); err != nil {
			return err
		}
	}
	return DeleteL2(engine, agentID, secondaryIDs, DeleteScenesL2)
}

// OpenSceneTurn opens the scene's next turn: it bumps the scene's
// turn counter and returns the updated record so the caller reads back the seq
// it just allocated rather than a stale snapshot. TurnSeq is load-bearing — the
// caller hashes it into the turn's topic id, so a failed write must surface as
// an error, not a lost increment. Reads and writes of one domain are serialized
// by its lock, so no increment is ever racing away.
func OpenSceneTurn(engine *core.StorageEngine, agentID uint64, sceneID uint64) (*core.SceneSlot, error) {
	slot, err := core.ReadSceneSlot(engine, agentID, sceneID)
	if err != nil {
		return nil, err
	}
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

// CreateSceneL2WithID creates a scene under a caller-chosen id. An existing
// scene is reused as-is — the name is only ever written on creation, so a
// repeated call for the same id never renames it.
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

// CollectAllScenesL2 returns every scene record of the agent domain. How many
// topics a scene holds is derived by whoever needs it — the surface read already
// has the topic set in hand — so this layer does not scan topics to fill a count.
// A scene the index names but the engine cannot read is reported rather than
// skipped: a listing quietly missing one session is indistinguishable from a
// session that was deleted.
func CollectAllScenesL2(engine *core.StorageEngine, agentID uint64) ([]core.SceneSlot, error) {
	return core.CollectAllStrict[core.SceneSlot](engine, agentID, core.RecL2Scene)
}

// TopicClosureL2 gathers a topic and its recursive children (any depth); the
// result is empty when the root topic does not exist (DeleteTopic then reports
// ErrNotFound). The scan is strict because the result is what a cascade deletes:
// a child that would not read back would survive its own parent. The archives
// each topic owns are not collected here: they are addressed by the topic's own
// id, so the caller hands it this closure and the archive index supplies the rest.
func TopicClosureL2(engine *core.StorageEngine, agentID uint64, root uint64) ([]uint64, error) {
	topics, err := core.CollectAllTopicsStrict(engine, agentID)
	if err != nil {
		return nil, err
	}
	have := make(map[uint64]struct{})
	children := make(map[uint64][]uint64)
	for _, t := range topics {
		have[t.ID] = struct{}{}
		if t.ParentID != nil {
			children[*t.ParentID] = append(children[*t.ParentID], t.ID)
		}
	}
	if _, ok := have[root]; !ok {
		return nil, nil
	}
	closure := []uint64{root}
	for i := 0; i < len(closure); i++ {
		closure = append(closure, children[closure[i]]...)
	}
	return closure, nil
}
