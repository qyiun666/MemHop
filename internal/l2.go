// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene operations of the internal layer: list / get / merge / active set.

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func (db *DB) ListScenes(agentID uint64) ([]core.SceneSlot, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	all := repo.CollectAllScenesL2(db.engine, agentID)
	if all == nil {
		return []core.SceneSlot{}, nil
	}
	return all, nil
}

// ActiveSceneIDs returns a copy of the agent's in-memory active scene IDs
// (the Dream consolidation targets).
func (db *DB) ActiveSceneIDs(agentID uint64) []uint64 {
	ac := db.peekContext(agentID)
	if ac == nil {
		return nil
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return append([]uint64(nil), ac.activeScenes...)
}

// MergeScenes rewrites all topics of secondary scenes to the primary scene
// and deletes the secondary records.
func (db *DB) MergeScenes(agentID uint64, primaryID string, secondaryIDs []string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.mu.Unlock()
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
	removed := common.ToSet(hashes)
	// Mirror the scene retarget in the L2MetaIndex so cached candidates
	// match the merged records (storage write already done).
	ac.retargetL2Meta(primaryHash, removed)
	// Drop merged secondary scenes so Dream does not spin empty goroutines.
	ac.dropActiveScenes(removed)
	return nil
}

// SceneContext returns one scene's topics sorted by user timestamp with
// their L4 messages; unknown scenes return an error.
func (db *DB) SceneContext(agentID uint64, sceneID string) (*SceneContext, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
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
		MetaIdx: ac.l2Meta,
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
		out.Topics = append(out.Topics, db.sceneContextTopic(agentID, t, children))
	}
	out.TopicCount = len(out.Topics)
	return out, nil
}

// sceneContextTopic renders one topic of a scene context: merged keyword
// tracks, child count, and its L4 messages (unreadable archives are
// skipped, the id ref is still reported).
func (db *DB) sceneContextTopic(agentID uint64, t core.TopicSlot, children map[uint64]int) SceneContextTopic {
	st := SceneContextTopic{
		TopicID:    common.FormatHash(t.ID),
		Depth:      int(t.Depth),
		Keywords:   append(append(append([]string{}, t.FusedKeywords...), t.UserKeywords...), t.AgentKeywords...),
		ChildCount: children[t.ID],
		L4IDs:      make([]string, 0, len(t.L4Refs)),
	}
	for _, ref := range t.L4Refs {
		st.L4IDs = append(st.L4IDs, common.FormatHash(ref))
		arc, err := core.ReadArchiveSlot(db.engine, agentID, ref)
		if err != nil {
			continue
		}
		st.Messages = append(st.Messages, SceneMessage{Role: arc.Role, Content: arc.Content, CreatedAt: arc.CreatedAt})
	}
	return st
}

// DeleteTopic removes a topic and its whole subtree (children at any
// depth), the L4 archives they reference, and their L2Meta/sparse entries,
// so the deleted topic no longer surfaces in retrieval. The surviving
// parent (if any) has its ChildrenIDs pruned. Deleting a missing topic
// returns ErrNotFound.
func (db *DB) DeleteTopic(agentID uint64, topicID string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.mu.Unlock()
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse topic id", err)
	}
	topics, archives := repo.TopicClosureL2(db.engine, agentID, parsedID)
	if len(topics) == 0 {
		return common.NewError(common.ErrNotFound, "topic not found")
	}
	if err := ac.pruneParentChild(db, parsedID); err != nil {
		return err
	}
	return ac.deleteTopics(db, agentID, topics, archives)
}

// pruneParentChild removes the deleted topic from its surviving parent's
// ChildrenIDs and refreshes the parent record and L2Meta entry, so no
// dangling child reference survives the deletion.
func (ac *agentContext) pruneParentChild(db *DB, topicID uint64) error {
	root, err := core.ReadTopicSlot(db.engine, ac.id, topicID)
	if err != nil || root == nil || root.ParentID == nil {
		return err
	}
	parentID := *root.ParentID
	parent, err := core.ReadTopicSlot(db.engine, ac.id, parentID)
	if err != nil || parent == nil {
		return err
	}
	parent.ChildrenIDs = common.RemoveOnce(parent.ChildrenIDs, topicID)
	if err := core.WriteTopicSlot(db.engine, ac.id, parentID, parent); err != nil {
		return err
	}
	ac.syncL2Meta(db, parentID)
	return nil
}

// DeleteScene removes a scene: its scene record, every topic (all depths),
// the referenced L4 archives, the L2Meta/sparse entries, and its active-set
// membership, so the scene disappears from listings and retrieval.
func (db *DB) DeleteScene(agentID uint64, sceneID string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.mu.Unlock()
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	if _, err := core.ReadSceneSlot(db.engine, agentID, sceneHash); err != nil {
		return common.NewError(common.ErrNotFound, "scene not found")
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
	if !repo.DeleteL2(db.engine, agentID, []uint64{sceneHash}, 1) {
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
	ac.removeTopicsFromIndices(topics)
	ac.dropActiveScenes(map[uint64]struct{}{sceneHash: {}})
	return nil
}

// deleteTopics removes the given topics (with their L2Meta/sparse entries)
// and the given archives in one engine pass.
func (ac *agentContext) deleteTopics(db *DB, agentID uint64, topics, archives []uint64) error {
	if !repo.DeleteL2(db.engine, agentID, topics, 2) {
		return common.NewError(common.ErrIO, "delete topics", nil)
	}
	if err := repo.DeleteArchivesL4(db.engine, agentID, archives); err != nil {
		return err
	}
	ac.removeTopicsFromIndices(topics)
	return nil
}
