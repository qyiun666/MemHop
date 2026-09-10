// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Update of the composition root: sinks one finished turn into the topic
// Search opened for it. One turn is one write — the turn's originals arrive
// in the same call as its texts, so the hot path distills exactly once per
// turn. The settle steps live in internal/turn.

package internal

import (
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/scene"
	"github.com/qyiun666/MemHop/internal/turn"
)

// Update writes one finished turn into the topic id Search issued for it: both
// originals become the topic's two L4 content slots, and the topic's keywords
// come from a single distillation call. The distill runs before any write, so a
// failed LLM call leaves the scene exactly as it was — no orphan record, no
// contentless topic. Settling the same topic id twice rewrites that turn in
// place, so a retry that changed the texts leaves no second version behind; what
// it does not rewrite is a slot this turn never filled, which stays until the
// topic is deleted or the retention window passes. What may be settled is a turn
// topic of the named scene only — a Dream-fused topic, another scene's topic, or
// an id that names some other record is refused.
func (db *DB) Update(agentID uint64, in TurnUpdate) (uint64, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return 0, err
	}
	defer ac.Mu.Unlock()

	sceneID, topicID, err := turn.Targets(in)
	if err != nil {
		return 0, err
	}
	// A turn must land in a scene the host already opened with Search; an
	// unknown id is rejected before any write so nothing settles in a scene
	// nobody owns.
	slot, err := core.ReadSceneSlot(db.engine, agentID, sceneID)
	if err != nil {
		return 0, err
	}
	if err := turn.SettleTarget(sceneID, topicID, slot.TurnSeq); err != nil {
		return 0, err
	}
	// Extract on the domain's cancellable context: a Close/DeleteAgent racing
	// an in-flight Update cancels the LLM call instead of waiting a full
	// round-trip behind the lifecycle barrier.
	keywords, err := llmops.ExtractTurnKeywords(ac.OpCtx, db.llm, in.UserText, in.AgentText)
	if err != nil {
		return 0, common.NewError(common.ErrLLM, "distill turn", err)
	}
	if len(keywords) == 0 {
		return 0, common.NewError(common.ErrLLM, "turn distillation produced no keywords", nil)
	}
	if !repo.CreateTurnTopicL2(db.engine, agentID, sceneID, topicID, keywords, in.UserTS, in.AgentTS) {
		return 0, common.NewError(common.ErrIO, "create turn topic", nil)
	}
	// The two originals land on the topic's own Seq 1 and 2, so a replayed turn
	// rewrites them where they stand: nothing has to be enumerated and retired.
	if err := turn.WriteArchives(ac, agentID, topicID, in); err != nil {
		return 0, err
	}
	ac.SyncL2Meta(topicID)
	db.consolidateScene(ac, sceneID)
	return topicID, nil
}

// consolidateScene keeps one scene's read surface bounded: once its depth-1
// topic count passes the threshold, a background Dream compresses it (the
// scene is compressed by a later hit if this Dream is already in flight).
// Best-effort and asynchronous — Update never waits on the pipeline. A zero
// threshold disables the trigger.
func (db *DB) consolidateScene(ac *domain.Context, sceneID uint64) {
	t := db.config.Defaults.SceneDreamTopicThreshold
	if t <= 0 || len(scene.SurfaceTopics(ac, sceneID)) <= t {
		return
	}
	db.triggerSceneDream(ac, sceneID)
}
