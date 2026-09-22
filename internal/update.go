// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Settle of the composition root: distills the content a turn already appended
// under its own topic id. Settle writes no content — the host owns what a turn
// recorded — so one call reads that topic's utterances and gives them a keyword
// track. The settle steps live in internal/turn.

package internal

import (
	"cmp"
	"slices"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/content"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/scene"
	"github.com/qyiun666/MemHop/internal/turn"
)

// Settle distills one turn's appended utterances into the keyword track of the
// topic id Search issued for it, and returns the topic as stored — the track
// among it, so the host that just closed a turn reads back what the turn was
// distilled into without a second read. The distill runs before the topic is
// written, so a failed LLM call leaves the scene exactly as it was — no
// contentless topic. Settling the same topic id twice rewrites that track from
// whatever the topic holds now. What may be settled is a turn topic of the named
// scene only — a Dream-fused topic, another scene's topic, or an id that names
// some other record is refused.
//
// A topic with no utterances left is refused with ErrInvalidQuery before any LLM
// call: the retention window reclaimed what was said, so there is nothing to
// distill and inventing an empty keyword track would read back as the real one.
func (db *DB) Settle(agentID uint64, sceneID, topicID string) (*core.TopicSlot, error) {
	ac, parsedTopic, err := db.lockSession(agentID, topicID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	parsedScene, err := common.ParseID(sceneID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	// A turn must land in a scene the host already opened with Search; an
	// unknown id is rejected before any write so nothing settles in a scene
	// nobody owns.
	slot, err := core.ReadSceneSlot(db.engine, agentID, parsedScene)
	if err != nil {
		return nil, err
	}
	if err := turn.SettleTarget(parsedScene, parsedTopic, slot.TurnSeq); err != nil {
		return nil, err
	}
	utterances, err := content.Read(agentID, ac, parsedTopic, core.KindUtterance)
	if err != nil {
		return nil, err
	}
	if len(utterances) == 0 {
		return nil, common.NewError(common.ErrInvalidQuery, "this turn holds no content to distill")
	}
	// Extract on the domain's cancellable context: a Close racing an
	// in-flight Settle cancels the LLM call instead of waiting a full
	// round-trip behind the lifecycle barrier.
	keywords, err := llmops.ExtractKeywords(ac.OpCtx, ac.LLM, content.RenderForDistill(utterances))
	if err != nil {
		return nil, common.NewError(common.ErrLLM, "distill turn", err)
	}
	if len(keywords) == 0 {
		return nil, common.NewError(common.ErrLLM, "turn distillation produced no keywords", nil)
	}
	topic, err := repo.CreateTurnTopicL2(db.engine, agentID, parsedScene, parsedTopic, keywords,
		slices.MinFunc(utterances, byCreatedAt).CreatedAt,
		slices.MaxFunc(utterances, byCreatedAt).CreatedAt)
	if err != nil {
		// The settle is the last step of a turn: whatever stopped it — a stored
		// record that will not read back or a write that failed — is the reason the
		// host hears, with its own code.
		return nil, common.NewError(common.CodeOf(err), "create turn topic", err)
	}
	ac.SyncL2Meta(topic)
	db.consolidateScene(ac, parsedScene)
	return topic, nil
}

func byCreatedAt(a, b core.ArchiveSlot) int {
	return cmp.Compare(a.CreatedAt, b.CreatedAt)
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
