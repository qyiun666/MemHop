// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 write path: Update sinks one finished turn into the host's scene (the
// only distilling call of the hot path), AppendL4Message appends further
// messages to an existing topic, and RefineTopicKeywords re-distills a topic
// from all of its originals. All three share the loadTopicForWrite guard.

package internal

import (
	"context"
	"strings"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Update writes one finished turn: both originals become L4 archives under a
// new depth-1 topic whose keywords come from a single distillation call. The
// distill runs before any write, so a failed LLM call leaves the scene exactly
// as it was — no orphan archive, no contentless topic. It returns the new
// topic id so the host can keep appending this turn's intermediate messages
// with AppendL4Message.
func (db *DB) Update(agentID uint64, in TurnUpdate) (uint64, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return 0, err
	}
	defer ac.mu.Unlock()

	sceneID, err := turnSceneID(in)
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
	topicID := core.ComputeTurnTopicID(sceneID, in.UserTS, in.AgentTS)
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
	ac.syncL2Meta(db, topicID)
	db.consolidateScene(ac, sceneID)
	return topicID, nil
}

// turnSceneID validates a turn's payload and resolves its host scene id.
func turnSceneID(in TurnUpdate) (uint64, error) {
	if in.UserText == "" || in.AgentText == "" {
		return 0, common.NewError(common.ErrInvalidQuery, "Update requires both the user and the agent text")
	}
	if in.UserTS <= 0 || in.AgentTS <= 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "Update requires positive timestamps for both messages")
	}
	if in.AgentTS < in.UserTS {
		return 0, common.NewError(common.ErrInvalidQuery, "Update requires the agent timestamp not earlier than the user timestamp")
	}
	id, err := common.ParseID(in.SceneID)
	if err != nil {
		return 0, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
	}
	return id, nil
}

// writeTurnArchives appends the turn's two originals as L4 archives in speaker
// order and returns their ids for the topic's L4Refs.
func (db *DB) writeTurnArchives(agentID, topicID uint64, in TurnUpdate) ([]uint64, error) {
	userRef, err := repo.AppendArchiveL4(db.engine, agentID, topicID, core.RoleUser, core.ContentText, in.UserText, in.UserTS)
	if err != nil {
		return nil, err
	}
	agentRef, err := repo.AppendArchiveL4(db.engine, agentID, topicID, core.RoleAgent, core.ContentText, in.AgentText, in.AgentTS)
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

// AppendL4Message appends one message to an existing topic: pure storage
// append — no keyword extraction, no LLM call. The new record id is
// appended to the topic's L4Refs (append + DedupSorted). role must be one
// of core.RoleUser / core.RoleAgent (RoleSystem / RoleDream also defined);
// contentType must be a defined core.ContentType — text-like types carry
// the original text in Content, media types (image/audio/video) carry a
// path or URI the host resolves. Returns the new L4 record id (uint64
// hash); hosts format it with common.FormatHash.
func (db *DB) AppendL4Message(agentID uint64, topicID string, text string, timestamp int64, role uint8, contentType core.ContentType) (uint64, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return 0, err
	}
	defer ac.mu.Unlock()
	if text == "" || timestamp <= 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "AppendL4Message requires text and a positive timestamp")
	}
	if role > core.RoleDream {
		return 0, common.NewError(common.ErrInvalidQuery, "AppendL4Message: undefined role")
	}
	if contentType > core.ContentCode && contentType != core.ContentOther {
		return 0, common.NewError(common.ErrInvalidQuery, "AppendL4Message: undefined content type")
	}
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		return 0, err
	}
	// Validate the topic before any write: a missing topic must not leave
	// an orphan L4 archive behind (same guard as Update).
	if _, err := ac.loadTopicForWrite(db, parsedID); err != nil {
		return 0, err
	}
	archiveID, err := repo.AppendArchiveL4(db.engine, agentID, parsedID, role, contentType, text, timestamp)
	if err != nil {
		return 0, err
	}
	if !repo.UpdateTopicL4RefsL2(db.engine, agentID, parsedID, []uint64{archiveID}) {
		return 0, common.NewError(common.ErrIO, "update topic l4 ref", nil)
	}
	return archiveID, nil
}

// RefineTopicKeywords re-distills one topic's keyword track from all of its L4
// originals (L4Refs order), replacing what the turn-level extraction produced.
// Hosts call it after an N:N turn where AppendL4Message added messages the
// original distillation never saw; each call costs one LLM round trip and the
// library does not infer when one is needed. The ctx cancels the extraction.
func (db *DB) RefineTopicKeywords(ctx context.Context, agentID uint64, topicID string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.mu.Unlock()
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		return err
	}
	// Validate the topic before any LLM call: a missing topic must not leave
	// a half-written refine behind.
	topic, err := ac.loadTopicForWrite(db, parsedID)
	if err != nil {
		return err
	}
	if len(topic.L4Refs) == 0 {
		return nil // nothing to distill from
	}
	// Read all L4 originals in L4Refs order (mode 3 returns in input
	// order, missing skipped) and join them as the extraction source.
	keywords, err := llmops.ExtractKeywords(ctx, db.llm, db.joinedL4Messages(agentID, topic))
	if err != nil {
		return err
	}
	// Never persist an empty extraction: it would erase the topic's only
	// keyword track with nothing to recover it from.
	if len(keywords) == 0 {
		return common.NewError(common.ErrLLM, "refine extracted no keywords")
	}
	if !repo.RefineTopicKeywordsL2(db.engine, agentID, parsedID, keywords) {
		return common.NewError(common.ErrIO, "refine topic keywords", nil)
	}
	ac.syncL2Meta(db, parsedID)
	return nil
}

// joinedL4Messages returns the topic's L4 archive contents concatenated in
// L4Refs order (missing archives skipped) as the keyword extraction source.
func (db *DB) joinedL4Messages(agentID uint64, topic *core.TopicSlot) string {
	archives := repo.QueryArchiveL4(db.engine, agentID, 3, "", 0, 0, topic.L4Refs)
	parts := make([]string, 0, len(archives))
	for _, a := range archives {
		parts = append(parts, a.Content)
	}
	return strings.Join(parts, "\n")
}
