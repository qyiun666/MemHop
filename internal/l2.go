// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene big methods of the composition root: list / metadata patch /
// merge / delete and the deep scene-context read. The scene steps live in
// internal/scene.

package internal

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/scene"
)

// ListScenes returns the domain's scenes, optionally filtered by their L3
// project-domain anchor: an empty l3ID lists every scene.
func (db *DB) ListScenes(agentID uint64, l3ID string) ([]core.SceneSlot, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	all, err := repo.CollectAllScenesL2(db.engine, agentID)
	if err != nil {
		return nil, err
	}
	if l3ID == "" {
		if all == nil {
			return []core.SceneSlot{}, nil
		}
		return all, nil
	}
	l3Hash, err := common.ParseID(l3ID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
	}
	out := []core.SceneSlot{}
	for _, s := range all {
		if s.L3ID == l3Hash {
			out = append(out, s)
		}
	}
	return out, nil
}

// UpdateScene corrects a scene's host-facing metadata in one write and returns
// the scene as stored afterwards: a non-nil Name renames it, a non-nil L3ID
// anchors it, and an empty L3ID clears the anchor. nil fields keep their stored
// value, so the library's own "session:<id>" naming and the turn history are
// never touched by accident. Anchoring is write-once: moving a scene that
// already has a *different* domain over needs Force, while clearing always does.
// Handing back the written scene is what makes a host's read-back cheap — it
// does not have to list every scene to confirm one anchor.
func (db *DB) UpdateScene(agentID uint64, sceneID string, patch ScenePatch) (core.SceneSlot, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return core.SceneSlot{}, err
	}
	defer ac.Mu.Unlock()
	if patch.Name != nil && *patch.Name == "" {
		return core.SceneSlot{}, common.NewError(common.ErrInvalidQuery, "scene name is required")
	}
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return core.SceneSlot{}, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	var l3Hash uint64
	if patch.L3ID != nil && *patch.L3ID != "" {
		if l3Hash, err = common.ParseID(*patch.L3ID); err != nil {
			return core.SceneSlot{}, common.NewError(common.ErrInvalidQuery, "parse l3 id", err)
		}
		if _, err := core.ReadGraphSlot(db.engine, agentID, l3Hash); err != nil {
			return core.SceneSlot{}, err
		}
	}
	slot, err := core.ReadSceneSlot(db.engine, agentID, sceneHash)
	if err != nil {
		return core.SceneSlot{}, err
	}
	if patch.Name != nil {
		slot.SceneName = *patch.Name
	}
	if patch.L3ID != nil {
		// Only replacing one anchor with another is destructive enough to
		// need Force: the old value is lost. Clearing is reversible (the
		// scene is unanchored and can take any domain again), so it does not.
		if l3Hash != 0 && slot.L3ID != 0 && slot.L3ID != l3Hash && !patch.Force {
			return core.SceneSlot{}, common.NewError(common.ErrInvalidQuery,
				fmt.Sprintf("scene %s is anchored to %s; pass Force to re-anchor", sceneID, common.FormatHash(slot.L3ID)))
		}
		slot.L3ID = l3Hash
	}
	if err := core.WriteSceneSlot(db.engine, agentID, sceneHash, slot); err != nil {
		return core.SceneSlot{}, err
	}
	return *slot, nil
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
	// A merge destroys records, so every id it names must still be a scene.
	// Naming one the host no longer holds is a mistake to report, not a fold to
	// pretend succeeded: the batch delete below keys on these ids, so a stale
	// one can otherwise take the primary's own record with it.
	if err := db.requireScenes(agentID, append([]uint64{primaryHash}, hashes...)...); err != nil {
		return err
	}
	if !repo.MergeScenesL2(db.engine, agentID, primaryHash, hashes) {
		return common.NewError(common.ErrIO, "merge scenes", nil)
	}
	// Mirror the scene retarget in the L2MetaIndex so cached topics match the
	// merged records (storage write already done).
	ac.RetargetL2Meta(primaryHash, common.ToSet(hashes))
	return nil
}

// requireScenes resolves every named id against the scene records and reports
// the first that is not one. A read failure is returned as it stands: "cannot
// read" is not "no such scene".
func (db *DB) requireScenes(agentID uint64, ids ...uint64) error {
	scenes, err := repo.ListScenesL2(db.engine, agentID, ids)
	if err != nil {
		return err
	}
	have := make(map[uint64]struct{}, len(scenes))
	for _, s := range scenes {
		have[s.SceneID] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := have[id]; !ok {
			return common.NewError(common.ErrNotFound, "scene not found: "+common.FormatHash(id), nil)
		}
	}
	return nil
}

// SceneContext returns one scene's transcript — topics with depth <= 2 in user
// timestamp order plus their L4 messages — and writes nothing. The depth is
// deliberate: a fused group's originals live on the children Dream sank, so
// stopping at depth 1 (what Search returns) would hide them. Unknown scenes
// return an error.
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
	scenes, err := repo.ListScenesL2(db.engine, agentID, []uint64{sceneHash})
	if err != nil {
		return nil, err
	}
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
		st, err := scene.ContextTopic(db.engine, agentID, t, children)
		if err != nil {
			return nil, err
		}
		out.Topics = append(out.Topics, st)
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
