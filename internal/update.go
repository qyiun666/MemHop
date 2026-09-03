// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 write path: Update sinks one finished turn into the topic Search opened
// for it. One turn is one write — the turn's originals arrive in the same
// call as its texts, so the hot path distills exactly once per turn.

package internal

import (
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Update writes one finished turn into the topic id Search issued for it: both
// originals become L4 archives under a depth-1 topic whose keywords come from
// a single distillation call. The distill runs before any write, so a failed
// LLM call leaves the scene exactly as it was — no orphan archive, no
// contentless topic. Settling the same topic id twice rewrites that turn: the
// topic ends pointing at the new pair and the archives it no longer references
// are tombstoned, so a retry that changed the texts leaves nothing behind.
func (db *DB) Update(agentID uint64, in TurnUpdate) (uint64, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return 0, err
	}
	defer ac.mu.Unlock()

	sceneID, topicID, err := turnTargets(in)
	if err != nil {
		return 0, err
	}
	// A turn must land in a scene the host already opened with Search; an
	// unknown id is rejected before any write so nothing settles in a scene
	// nobody owns.
	if _, err := core.ReadSceneSlot(db.engine, agentID, sceneID); err != nil {
		return 0, err
	}
	// Extract on the domain's cancellable context: a Close/DeleteAgent racing
	// an in-flight Update cancels the LLM call instead of waiting a full
	// round-trip behind the lifecycle barrier.
	keywords, err := llmops.ExtractTurnKeywords(ac.opCtx, db.llm, in.UserText, in.AgentText)
	if err != nil {
		return 0, common.NewError(common.ErrLLM, "distill turn", err)
	}
	if len(keywords) == 0 {
		return 0, common.NewError(common.ErrLLM, "turn distillation produced no keywords", nil)
	}
	// The topic rewrite below clears L4Refs, so the refs of an earlier settle
	// of this same turn must be read first: they are what this turn supersedes.
	previous, err := turnL4Refs(db.engine, agentID, topicID)
	if err != nil {
		return 0, err
	}
	if !repo.CreateTurnTopicL2(db.engine, agentID, sceneID, topicID, keywords, in.UserTS, in.AgentTS) {
		return 0, common.NewError(common.ErrIO, "create turn topic", nil)
	}
	refs, err := db.writeTurnArchives(agentID, topicID, in)
	if err != nil {
		return 0, err
	}
	if !repo.UpdateTopicL4RefsL2(db.engine, agentID, topicID, refs) {
		return 0, common.NewError(common.ErrIO, "update topic l4 ref", nil)
	}
	if dropped := dropRetained(previous, refs); len(dropped) > 0 {
		if err := repo.DeleteArchivesL4(db.engine, agentID, dropped); err != nil {
			return 0, err
		}
	}
	ac.syncL2Meta(db, topicID)
	db.consolidateScene(ac, sceneID)
	return topicID, nil
}

// turnL4Refs returns the L4 refs stored on a turn topic; a topic that does not
// exist yet (the first settle of a turn) yields nil.
func turnL4Refs(engine *core.StorageEngine, agentID, topicID uint64) ([]uint64, error) {
	topic, err := core.ReadTopicLenient(engine, agentID, topicID)
	if err != nil {
		if common.CodeOf(err) == common.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if topic == nil {
		return nil, nil
	}
	return topic.L4Refs, nil
}

// dropRetained yields the ids of before that no longer appear in after.
func dropRetained(before, after []uint64) []uint64 {
	keep := common.ToSet(after)
	var out []uint64
	for _, id := range before {
		if _, ok := keep[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// turnTargets validates a turn's payload and resolves the scene it settles
// into plus the topic id Search minted for it.
func turnTargets(in TurnUpdate) (uint64, uint64, error) {
	if in.UserText == "" || in.AgentText == "" {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires both the user and the agent text")
	}
	if in.UserTS <= 0 || in.AgentTS <= 0 {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires positive timestamps for both messages")
	}
	if in.AgentTS < in.UserTS {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires the agent timestamp not earlier than the user timestamp")
	}
	if !in.UserType.Valid() || !in.AgentType.Valid() {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires a defined content type on both sides")
	}
	sceneID, err := common.ParseID(in.SceneID)
	if err != nil {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	topicID, err := common.ParseID(in.TopicID)
	if err != nil {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "parse topic id", err)
	}
	if topicID == 0 {
		return 0, 0, common.NewError(common.ErrInvalidQuery, "Update requires the topic id Search issued for this turn")
	}
	return sceneID, topicID, nil
}

// writeTurnArchives appends the turn's two originals as L4 archives, each
// under the content type the host declared. The returned ids go to the
// topic's L4Refs, which persist id-sorted — conversation order comes from the
// archives' timestamps, not from this slice.
func (db *DB) writeTurnArchives(agentID, topicID uint64, in TurnUpdate) ([]uint64, error) {
	userRef, err := repo.AppendArchiveL4(db.engine, agentID, topicID, core.RoleUser, in.UserType, in.UserText, in.UserTS)
	if err != nil {
		return nil, err
	}
	agentRef, err := repo.AppendArchiveL4(db.engine, agentID, topicID, core.RoleAgent, in.AgentType, in.AgentText, in.AgentTS)
	if err != nil {
		return nil, err
	}
	return []uint64{userRef, agentRef}, nil
}

// consolidateScene keeps one scene's read surface bounded: once its depth-1
// topic count passes the threshold, a background Dream compresses it (the
// scene is compressed by a later hit if this Dream is already in flight).
// Best-effort and asynchronous — Update never waits on the pipeline. A zero
// threshold disables the trigger.
func (db *DB) consolidateScene(ac *agentContext, sceneID uint64) {
	t := db.config.Defaults.SceneDreamTopicThreshold
	if t <= 0 || len(ac.sceneSurfaceTopics(sceneID)) <= t {
		return
	}
	db.triggerSceneDream(ac, sceneID)
}
