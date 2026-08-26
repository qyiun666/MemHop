// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene operations of the internal layer: list / get / merge / active set.

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func (db *DB) ListScenes() ([]core.SceneSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	all := repo.CollectAllScenesL2(db.engine, core.DefaultAgentID)
	if all == nil {
		return []core.SceneSlot{}, nil
	}
	return all, nil
}

// ActiveSceneIDs returns a copy of the in-memory active scene IDs (the
// Dream consolidation targets).
func (db *DB) ActiveSceneIDs() []uint64 {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return append([]uint64(nil), db.activeScenes...)
}

// MergeScenes rewrites all topics of secondary scenes to the primary scene
// and deletes the secondary records; caller holds the write lock.
func (db *DB) MergeScenes(primaryID string, secondaryIDs []string) error {
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
	if !repo.MergeScenesL2(db.engine, core.DefaultAgentID, primaryID, secondaryIDs) {
		return common.NewError(common.ErrIO, "merge scenes", nil)
	}
	removed := common.ToSet(hashes)
	// Mirror the scene retarget in the L2MetaIndex so cached candidates
	// match the merged records (storage write already done).
	db.retargetL2Meta(primaryHash, removed)
	// Drop merged secondary scenes so Dream does not spin empty goroutines.
	kept := db.activeScenes[:0]
	for _, sid := range db.activeScenes {
		if _, drop := removed[sid]; !drop {
			kept = append(kept, sid)
		}
	}
	db.activeScenes = kept
	return nil
}

// SceneMessage is one L4 archive message inside a scene context topic.
type SceneMessage struct {
	Role      uint8  `json:"role"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"`
}

// SceneContextTopic is one depth-1 topic with its L4 messages and child count.
type SceneContextTopic struct {
	TopicID    string         `json:"topic_id"`
	Depth      int            `json:"depth"`
	Keywords   []string       `json:"keywords"`
	L4IDs      []string       `json:"l4_ids,omitempty"` // 话题内的 L4 档案 ID,供按 ID 拉取原文
	Messages   []SceneMessage `json:"messages,omitempty"`
	ChildCount int            `json:"child_count"`
}

// SceneContext is a scene's full depth-1 conversation context.
type SceneContext struct {
	SceneName  string              `json:"scene_name"`
	TopicCount int                 `json:"topic_count"`
	Topics     []SceneContextTopic `json:"topics"`
}

// SceneContext returns one scene's topics sorted by user timestamp with
// their L4 messages; unknown scenes return an error.
func (db *DB) SceneContext(sceneID string) (*SceneContext, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	if _, err := common.ParseID(sceneID); err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	scenes := repo.ListScenesL2(db.engine, core.DefaultAgentID, []string{sceneID})
	if len(scenes) == 0 {
		return nil, common.NewError(common.ErrNotFound, "scene not found", nil)
	}
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  db.engine,
		MetaIdx: db.l2Meta,
		SceneID: sceneID,
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
		st := SceneContextTopic{
			TopicID:    common.FormatHash(t.ID),
			Depth:      int(t.Depth),
			Keywords:   append(append(append([]string{}, t.FusedKeywords...), t.UserKeywords...), t.AgentKeywords...),
			ChildCount: children[t.ID],
			L4IDs:      make([]string, 0, len(t.L4Refs)),
		}
		for _, ref := range t.L4Refs {
			st.L4IDs = append(st.L4IDs, common.FormatHash(ref))
			arc, err := core.ReadArchiveSlot(db.engine, core.DefaultAgentID, ref)
			if err != nil {
				continue
			}
			st.Messages = append(st.Messages, SceneMessage{Role: arc.Role, Content: arc.Content, CreatedAt: arc.CreatedAt})
		}
		out.Topics = append(out.Topics, st)
	}
	out.TopicCount = len(out.Topics)
	return out, nil
}

// DeleteTopic removes a topic and its whole subtree (children at any
// depth), the L4 archives they reference, and their L2Meta/sparse entries,
// so the deleted topic no longer surfaces in retrieval. The surviving
// parent (if any) has its ChildrenIDs pruned. Deleting a missing topic
// returns ErrNotFound. The caller must hold the write lock.
func (db *DB) DeleteTopic(topicID string) error {
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse topic id", err)
	}
	topics, archives := collectTopicClosure(db.engine, core.DefaultAgentID, parsedID)
	if len(topics) == 0 {
		return common.NewError(common.ErrNotFound, "topic not found")
	}
	if err := db.pruneParentChild(parsedID); err != nil {
		return err
	}
	return db.deleteTopics(topics, archives)
}

// pruneParentChild removes the deleted topic from its surviving parent's
// ChildrenIDs and refreshes the parent record and L2Meta entry, so no
// dangling child reference survives the deletion.
func (db *DB) pruneParentChild(topicID uint64) error {
	root, err := core.ReadTopicSlot(db.engine, core.DefaultAgentID, topicID)
	if err != nil || root == nil || root.ParentID == nil {
		return err
	}
	parentID := *root.ParentID
	parent, err := core.ReadTopicSlot(db.engine, core.DefaultAgentID, parentID)
	if err != nil || parent == nil {
		return err
	}
	parent.ChildrenIDs = removeOnceUint64(parent.ChildrenIDs, topicID)
	if err := core.WriteTopicSlot(db.engine, core.DefaultAgentID, parentID, parent); err != nil {
		return err
	}
	db.syncL2Meta(parentID)
	return nil
}

// removeOnceUint64 removes the first occurrence of v from s (no-op when
// absent); s is returned unchanged when v is not found.
func removeOnceUint64(s []uint64, v uint64) []uint64 {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

// DeleteScene removes a scene: its scene record, every topic (all depths),
// the referenced L4 archives, the L2Meta/sparse entries, and its active-set
// membership, so the scene disappears from listings and retrieval. The
// caller must hold the write lock.
func (db *DB) DeleteScene(sceneID string) error {
	sceneHash, err := common.ParseID(sceneID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	if _, err := core.ReadSceneSlot(db.engine, core.DefaultAgentID, sceneHash); err != nil {
		return common.NewError(common.ErrNotFound, "scene not found")
	}
	var (
		topics   []uint64
		archives []uint64
	)
	for _, t := range core.CollectAllTopics(db.engine, core.DefaultAgentID) {
		if t.SceneID == sceneHash {
			topics = append(topics, t.ID)
			archives = append(archives, t.L4Refs...)
		}
	}
	if !repo.DeleteL2(db.engine, core.DefaultAgentID, []string{sceneID}, 1) {
		return common.NewError(common.ErrIO, "delete scene", nil)
	}
	if err := repo.DeleteArchivesL4(db.engine, core.DefaultAgentID, common.DedupSorted(archives)); err != nil {
		return err
	}
	// Drop the L1 scene node right away (its ID is derivable without an
	// index); incident hyperedges are cleaned by the next Dream's rebuild.
	if err := repo.DeleteSceneNodeL1(db.engine, core.DefaultAgentID, sceneHash); err != nil {
		return err
	}
	for _, id := range topics {
		db.l2Meta.Remove(id)
		db.sparseIndex.RemoveDocument(id)
	}
	kept := db.activeScenes[:0]
	for _, sid := range db.activeScenes {
		if sid != sceneHash {
			kept = append(kept, sid)
		}
	}
	db.activeScenes = kept
	return nil
}

// collectTopicClosure gathers a topic, its recursive children (any depth)
// and the L4 archives referenced by any of them. topics is empty when the
// root topic does not exist (DeleteTopic then reports ErrNotFound).
func collectTopicClosure(engine *core.StorageEngine, agentID uint64, root uint64) (topics, archives []uint64) {
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

// deleteTopics removes the given topics (with their L2Meta/sparse entries)
// and the given archives in one engine pass.
func (db *DB) deleteTopics(topics, archives []uint64) error {
	ids := make([]string, 0, len(topics))
	for _, id := range topics {
		ids = append(ids, common.FormatHash(id))
	}
	if !repo.DeleteL2(db.engine, core.DefaultAgentID, ids, 2) {
		return common.NewError(common.ErrIO, "delete topics", nil)
	}
	if err := repo.DeleteArchivesL4(db.engine, core.DefaultAgentID, archives); err != nil {
		return err
	}
	for _, id := range topics {
		db.l2Meta.Remove(id)
		db.sparseIndex.RemoveDocument(id)
	}
	return nil
}
