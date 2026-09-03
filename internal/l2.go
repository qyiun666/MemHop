// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene big methods of the composition root: list / anchor / merge /
// delete and the deep scene-context read. The scene steps live in
// internal/scene.

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/scene"
)

func (db *DB) ListScenes(agentID uint64) ([]core.SceneSlot, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	all := repo.CollectAllScenesL2(db.engine, agentID)
	if all == nil {
		return []core.SceneSlot{}, nil
	}
	return all, nil
}

// ListScenesByL3 returns all scenes anchored to the given L3 domain id
// (project/目录), i.e. scenes whose organizational L3 anchor equals it.
func (db *DB) ListScenesByL3(agentID uint64, l3ID string) ([]core.SceneSlot, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	hash, err := common.ParseID(l3ID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
	}
	all := repo.CollectAllScenesL2(db.engine, agentID)
	var out []core.SceneSlot
	for _, s := range all {
		if s.L3ID == hash {
			out = append(out, s)
		}
	}
	if out == nil {
		return []core.SceneSlot{}, nil
	}
	return out, nil
}

// SetSceneL3ID anchors a scene to an L3 domain (project/目录). Normal
// routing is write-once; pass force=true to correct a mis-anchored scene,
// or an empty l3ID to clear the anchor (both take the overwrite path).
func (db *DB) SetSceneL3ID(agentID uint64, sceneID string, l3ID string, force bool) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	var l3Hash uint64
	if l3ID != "" {
		if l3Hash, err = common.ParseID(l3ID); err != nil {
			return common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
		}
	}
	if force || l3Hash == 0 {
		return repo.OverwriteSceneL3ID(db.engine, agentID, sceneHash, l3Hash)
	}
	return repo.SetSceneL3ID(db.engine, agentID, sceneHash, l3Hash)
}

// SetSceneName renames a scene. The library names a fresh scene
// "session:<id>"; this is the host's chance to give it a human title. Every
// later scene write (OpenSceneTurn bumps counters on the read-back slot) and
// Dream leave SceneName alone, so the name persists until it is set again.
func (db *DB) SetSceneName(agentID uint64, sceneID string, name string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	if name == "" {
		return common.NewError(common.ErrInvalidQuery, "scene name is required")
	}
	slot, err := core.ReadSceneSlot(db.engine, agentID, sceneHash)
	if err != nil {
		return err
	}
	slot.SceneName = name
	return core.WriteSceneSlot(db.engine, agentID, sceneHash, slot)
}

// MergeScenes rewrites all topics of secondary scenes to the primary scene
// and deletes the secondary records.
func (db *DB) MergeScenes(agentID uint64, primaryID string, secondaryIDs []string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	primaryHash, err := common.ParseID(primaryID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse primary scene id", err)
	}
	hashes, ok := common.ParseAll(secondaryIDs)
	if !ok {
		return common.NewError(common.ErrInvalidQuery, "parse secondary scene ids")
	}
	if len(hashes) == 0 {
		return common.NewError(common.ErrInvalidQuery, "secondary scene ids are required")
	}
	if _, dup := common.ToSet(hashes)[primaryHash]; dup {
		return common.NewError(common.ErrInvalidQuery, "primary scene id must not be a secondary", nil)
	}
	if !repo.MergeScenesL2(db.engine, agentID, primaryHash, hashes) {
		return common.NewError(common.ErrIO, "merge scenes", nil)
	}
	// Mirror the scene retarget in the L2MetaIndex so cached topics match the
	// merged records (storage write already done).
	ac.RetargetL2Meta(primaryHash, common.ToSet(hashes))
	return nil
}

// SceneContext returns one scene's topics sorted by user timestamp with
// their L4 messages; unknown scenes return an error.
func (db *DB) SceneContext(agentID uint64, sceneID string) (*SceneContext, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	scenes := repo.ListScenesL2(db.engine, agentID, []uint64{sceneHash})
	if len(scenes) == 0 {
		return nil, common.NewError(common.ErrNotFound, "scene not found", nil)
	}
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  db.engine,
		AgentID: agentID,
		MetaIdx: ac.L2Meta,
		SceneID: sceneHash,
		Depth:   2,
		Num:     2,
	})
	if err != nil {
		return nil, err
	}
	children := make(map[uint64]int)
	for _, t := range topics {
		if t.ParentID != nil {
			children[*t.ParentID]++
		}
	}
	out := &SceneContext{SceneName: scenes[0].SceneName}
	for _, t := range topics {
		out.Topics = append(out.Topics, scene.ContextTopic(db.engine, agentID, t, children))
	}
	out.TopicCount = len(out.Topics)
	return out, nil
}

// DeleteTopic removes a topic and its whole subtree (children at any
// depth), the L4 archives they reference, and their L2Meta cache entries,
// so the deleted topic no longer surfaces in any scene read. The surviving
// parent (if any) has its ChildrenIDs pruned. Deleting a missing topic
// returns ErrNotFound.
func (db *DB) DeleteTopic(agentID uint64, topicID string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse topic id", err)
	}
	topics, archives := repo.TopicClosureL2(db.engine, agentID, parsedID)
	if len(topics) == 0 {
		return common.NewError(common.ErrNotFound, "topic not found")
	}
	if err := scene.PruneParentChild(ac, parsedID); err != nil {
		return err
	}
	return scene.DeleteTopics(ac, agentID, topics, archives)
}

// DeleteScene removes a scene: its scene record, every topic (all depths),
// the referenced L4 archives, and their L2Meta cache entries, so the scene
// disappears from listings and reads.
func (db *DB) DeleteScene(agentID uint64, sceneID string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	if _, err := core.ReadSceneSlot(db.engine, agentID, sceneHash); err != nil {
		return err
	}
	var (
		topics   []uint64
		archives []uint64
	)
	for _, t := range core.CollectAllTopics(db.engine, agentID) {
		if t.SceneID == sceneHash {
			topics = append(topics, t.ID)
			archives = append(archives, t.L4Refs...)
		}
	}
	if !repo.DeleteL2(db.engine, agentID, []uint64{sceneHash}, repo.DeleteScenesL2) {
		return common.NewError(common.ErrIO, "delete scene", nil)
	}
	if err := repo.DeleteArchivesL4(db.engine, agentID, common.DedupSorted(archives)); err != nil {
		return err
	}
	// Drop the L1 scene node right away (its ID is derivable without an
	// index); incident hyperedges are cleaned by the next Dream's rebuild.
	if err := repo.DeleteSceneNodeL1(db.engine, agentID, sceneHash); err != nil {
		return err
	}
	ac.RemoveTopicsFromIndices(topics)
	return nil
}
