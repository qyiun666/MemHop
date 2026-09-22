// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Update of the composition root: the one call that closes a turn. It records
// what the turn opened with and what it ended with, then distills the topic's
// utterances into its keyword track. Which turn is the domain's to remember —
// Search minted it — so a host closing a turn names no ids. The steps of the
// distillation live in internal/turn.

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

// outcomeEvent names the record Update writes for a turn's ending. The name is
// the library's — it is the one event a closing call always owns — while what it
// says happened stays the host's own word.
const outcomeEvent = "turn_outcome"

// Update closes the turn Search opened: the stimulus and the answer land on Seq 1
// and Seq 2, the two slots a topic's dialogue is looked for on, so closing the same
// turn again rewrites those two lines instead of accumulating versions. Outcome is
// the host's word for the arm that ended the turn and is recorded as one event per
// call — a suspension and the resume that followed are two facts, not one line
// written twice. The turn's utterances are then distilled into the topic's keyword
// track and the topic comes back as stored, that track among its fields.
//
// Every record is checked as a batch before any is written, so a record that breaks the
// write contract leaves nothing behind. A close that cannot distill is a different
// refusal: the records it wrote stay — replaying the close rewrites those two dialogue
// slots in place — and no topic is created, so an empty track never appears on the read
// surface looking like a distilled one. One LLM call, inside the domain lock; either
// refusal leaves the turn open, so the host can close it again. Nothing here writes the
// turn's other content — what the host recorded while the turn ran stays what it
// appended, and a turn whose originals the retention window already reclaimed is
// refused instead of settled into an empty track.
func (db *DB) Update(agentID uint64, end core.TurnEnd) (*core.TopicSlot, error) {
	ac, err := db.lockTurn(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()

	records := turnEndRecords(end)
	if len(records) == 0 {
		return nil, common.NewError(common.ErrInvalidQuery,
			"a turn closes with an input, an output or an outcome")
	}
	for _, in := range records {
		if err := content.ValidateAppend(in); err != nil {
			return nil, err
		}
	}
	for _, in := range records {
		if _, err := content.Append(ac, agentID, ac.Turn, in); err != nil {
			return nil, err
		}
	}
	return db.settleLocked(ac, agentID, ac.Scene, ac.Turn)
}

// turnEndRecords turns one closing call into the records it writes. An empty field
// writes nothing: a turn that opened without a stimulus and one with nothing to say
// are both real, and the pair still distills from whatever the turn holds.
func turnEndRecords(end core.TurnEnd) []core.ArchiveSlot {
	var records []core.ArchiveSlot
	if end.Input != "" {
		records = append(records, core.ArchiveSlot{
			Kind: core.KindUtterance, Seq: core.SeqUser, Role: core.RoleUser,
			ContentType: core.ContentText, Content: end.Input, CreatedAt: end.CreatedAt,
		})
	}
	if end.Output != "" {
		records = append(records, core.ArchiveSlot{
			Kind: core.KindUtterance, Seq: core.SeqAgent, Role: core.RoleAgent,
			ContentType: core.ContentText, Content: end.Output, CreatedAt: end.CreatedAt,
		})
	}
	if end.Outcome != "" {
		records = append(records, core.ArchiveSlot{
			Kind: core.KindEvent, EventType: outcomeEvent,
			Content: end.Outcome, CreatedAt: end.CreatedAt,
		})
	}
	return records
}

// settleLocked distills one turn's utterances into its topic's keyword track.
// Callers hold ac.Mu and have settled which turn this is; the scene and the topic
// are checked against each other rather than trusted, because a turn key is only
// meaningful as one of the named scene's counted turns. Only a turn topic may
// settle: a Dream-fused topic, another scene's topic, or an id naming some other
// record is refused.
func (db *DB) settleLocked(ac *domain.Context, agentID, sceneID, topicID uint64) (*core.TopicSlot, error) {
	slot, err := core.ReadSceneSlot(db.engine, agentID, sceneID)
	if err != nil {
		return nil, err
	}
	if err := turn.SettleTarget(sceneID, topicID, slot.TurnSeq); err != nil {
		return nil, err
	}
	utterances, err := content.Read(agentID, ac, topicID, core.KindUtterance)
	if err != nil {
		return nil, err
	}
	if len(utterances) == 0 {
		return nil, common.NewError(common.ErrInvalidQuery, "this turn holds no content to distill")
	}
	// Extract on the domain's cancellable context: a Close racing an
	// in-flight Update cancels the LLM call instead of waiting a full
	// round-trip behind the lifecycle barrier.
	keywords, err := llmops.ExtractKeywords(ac.OpCtx, ac.LLM, content.RenderForDistill(utterances))
	if err != nil {
		return nil, common.NewError(common.ErrLLM, "distill turn", err)
	}
	if len(keywords) == 0 {
		return nil, common.NewError(common.ErrLLM, "turn distillation produced no keywords", nil)
	}
	topic, err := repo.CreateTurnTopicL2(db.engine, agentID, sceneID, topicID, keywords,
		slices.MinFunc(utterances, byCreatedAt).CreatedAt,
		slices.MaxFunc(utterances, byCreatedAt).CreatedAt)
	if err != nil {
		// A read-back or write failure keeps its own code: the host hears
		// what actually stopped the close.
		return nil, common.NewError(common.CodeOf(err), "create turn topic", err)
	}
	ac.SyncL2Meta(topic)
	db.consolidateScene(ac, sceneID)
	return topic, nil
}

func byCreatedAt(a, b core.ArchiveSlot) int {
	return cmp.Compare(a.CreatedAt, b.CreatedAt)
}

// consolidateScene keeps one scene's read surface bounded: once its depth-1
// topic count passes the threshold, a background Dream compresses it (the
// scene is compressed by a later hit if this Dream is already in flight).
// Best-effort and asynchronous — closing a turn never waits on the pipeline. A zero
// threshold disables the trigger.
func (db *DB) consolidateScene(ac *domain.Context, sceneID uint64) {
	t := db.config.Defaults.SceneDreamTopicThreshold
	if t <= 0 || len(scene.SurfaceTopics(ac, sceneID)) <= t {
		return
	}
	db.triggerSceneDream(ac, sceneID)
}
