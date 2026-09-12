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

// RestoreSunkTopicsL2 undoes one sink: every listed topic that now hangs on
// parentID comes back up a level with no parent. The parent link is the whole test,
// so a member the failed batch never reached is left exactly as it stands rather
// than guessed at — and a member it did reach is named by the very parent this call
// is erasing. A rolled-back sink is the difference between a turn the next Dream can
// pick again and one that reads as neither a turn nor a fused group.
func RestoreSunkTopicsL2(engine *core.StorageEngine, agentID uint64, ids []uint64, parentID uint64) error {
	for _, id := range ids {
		topic, err := core.ReadTopicLenient(engine, agentID, id)
		switch {
		case err != nil && common.CodeOf(err) == common.ErrNotFound:
			continue // this member is gone; there is nothing to bring back
		case err != nil:
			return common.NewError(common.ErrIO, "read topic to restore", err)
		case topic == nil || topic.ParentID == nil || *topic.ParentID != parentID:
			continue // never sunk under this parent: not this group's to undo
		}
		if topic.Depth > 1 {
			topic.Depth--
		}
		topic.ParentID = nil
		if err := core.WriteTopicSlot(engine, agentID, topic.ID, topic); err != nil {
			return err
		}
	}
	return nil
}

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

// DeleteL2Records tombstones the given L2 ids — scene slots and topics in any
// mix — and reads nothing. The set is complete by the time this runs: every
// caller derives it from a strict enumeration (TopicIDsBySceneL2,
// TopicClosureL2) or wrote it itself, so re-scanning here would only add a second
// answer to a question already answered. An id the disk does not hold simply
// produces no tombstone.
func DeleteL2Records(engine *core.StorageEngine, agentID uint64, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := engine.DeleteRecordBatch(agentID, ids)
	return err
}

// MergeScenesL2 rewrites topics of the secondary scenes to the primary
// scene in one batch, then deletes the secondary scene records (now empty).
// The domain scan happens once, before either batch: once the topics have moved,
// a second refusal would leave the scene records of a domain whose contents
// already say they belong to another one.
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
	return DeleteL2Records(engine, agentID, secondaryIDs)
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

// CreateSceneL2 stores a scene record built by the caller. An existing record
// under that id is left exactly as stored — name and anchor are creation-time
// fields, so a repeated call for the same id neither renames the scene nor moves
// its domain.
func CreateSceneL2(engine *core.StorageEngine, agentID uint64, slot *core.SceneSlot) error {
	_, err := core.ReadSceneSlot(engine, agentID, slot.SceneID)
	if err == nil {
		return nil
	}
	if common.CodeOf(err) != common.ErrNotFound {
		// "Not there" and "will not read" are different answers, and only the first
		// one may be written over: a scene record that exists but came back unreadable
		// still holds the turn counter that mints this domain's turn ids, and storing a
		// fresh one resets it — the next turns would be issued ids the domain holds.
		return common.NewError(common.CodeOf(err), "create scene: the record under that id will not read", err)
	}
	return core.WriteSceneSlot(engine, agentID, slot.SceneID, slot)
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
