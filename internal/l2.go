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
	all := repo.CollectAllScenesL2(db.engine)
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
	if !repo.MergeScenesL2(db.engine, primaryID, secondaryIDs) {
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
	scenes := repo.ListScenesL2(db.engine, []string{sceneID})
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
			arc, err := core.ReadArchiveSlot(db.engine, ref)
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
