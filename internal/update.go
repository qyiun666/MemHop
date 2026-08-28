// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 topic write path: the three operations that mutate an existing topic
// and refresh its retrieval indexes — Update (agent reply + keyword track),
// AppendL4Message (pure storage append) and RefineTopicKeywords (N:N fused
// keyword re-extraction). All three share the loadTopicForWrite guard.

package internal

import (
	"context"
	"strings"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// Update appends an agent reply to the specified topic. It returns an error
// on any failure (validation, LLM, or storage); nil means the reply was
// appended and all indexes were refreshed.
func (db *DB) Update(agentID uint64, topicID string, text string, timestamp int64) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.mu.Unlock()
	if text == "" || timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "Update requires text and a positive timestamp")
	}
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		return err
	}
	// Validate the topic before any write or LLM call: a missing topic must
	// not leave an orphan L4 archive behind.
	topic, err := ac.loadTopicForWrite(db, parsedID)
	if err != nil {
		return err
	}
	// Extract on the domain's cancellable context: a Close/DeleteAgent that
	// races an in-flight Update cancels the LLM call instead of waiting a
	// full round-trip behind the lifecycle barrier, and cancellation aborts
	// before any record is written.
	keywords, err := llmops.ExtractKeywords(ac.opCtx, db.llm, text)
	if err != nil {
		return err
	}
	archiveID, err := repo.AppendArchiveL4(db.engine, agentID, parsedID, core.RoleAgent, core.ContentText, text, timestamp)
	if err != nil {
		return err
	}
	if !repo.UpdateTopicL4RefsL2(db.engine, agentID, parsedID, []uint64{archiveID}) {
		return common.NewError(common.ErrIO, "update topic l4 ref", nil)
	}
	if !repo.UpdateTopicL2(db.engine, agentID, parsedID, keywords, timestamp) {
		return common.NewError(common.ErrIO, "update topic keywords", nil)
	}
	// Update BM25: uncompressed topics carry User+Agent keywords (compressed
	// use FusedKeywords); topic.AgentKeywords is stale here, use fresh ones.
	// Refresh the L2Meta entry first per the storage → l2meta → sparse order.
	ac.syncL2Meta(db, parsedID)
	all := make([]string, 0, len(topic.UserKeywords)+len(keywords)+len(topic.FusedKeywords))
	all = append(all, topic.UserKeywords...)
	all = append(all, keywords...)
	all = append(all, topic.FusedKeywords...)
	terms := index.Tokenize(strings.Join(all, " "))
	ac.sparseIndex.AddDocument(parsedID, terms, uint32(len(terms)))
	return db.triggerCapacityDream(ac, agentID)
}

// triggerCapacityDream keeps the active scene set within capacity: when the
// set is full, the oldest scene gets a background Dream so the next
// activation has room instead of silently evicting it (best-effort, never
// fails Update). Pre-check compressibility: a full Dream runs index
// rebuilds + LLM distill even when no group was merged, so scenes below the
// compress threshold (few topics keep raw detail) are skipped here. The
// Dream is scheduled in the background and never blocks the caller.
func (db *DB) triggerCapacityDream(ac *agentContext, agentID uint64) error {
	capacity := db.config.Defaults.Capacity
	if capacity <= 0 || len(ac.activeScenes) < capacity {
		return nil
	}
	oldest := ac.activeScenes[0]
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  db.engine,
		AgentID: agentID,
		MetaIdx: ac.l2Meta,
		SceneID: oldest,
		Depth:   1,
		Num:     2,
	})
	if err == nil && len(topics) >= db.config.Defaults.DreamCompressMinTopics {
		db.triggerSceneDream(ac, oldest)
	}
	return nil
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

// RefineTopicKeywords re-extracts keywords from all L4 messages in the
// topic (L4Refs order), stores them as FusedKeywords and clears the
// user/agent keyword tracks (timestamps preserved). Pure refine: depth
// unchanged, no compression, no scene effects. Guarded: only topics with
// more than two L4 messages AND a non-empty user/agent track are refined;
// everything else is a no-op returning nil (idempotent — a refined topic
// has empty tracks and is skipped on later calls). Hosts call it after an
// N:N turn where AppendL4Message appended messages that never entered the
// keyword tracks (Search → AppendL4Message ×N → Update → refine). The ctx
// cancels LLM keyword extraction.
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
	// Validate the topic before any LLM call: a missing topic must not
	// leave a half-written refine behind.
	topic, err := ac.loadTopicForWrite(db, parsedID)
	if err != nil {
		return err
	}
	// Guard: only N:N topics (more than one user+agent pair) with a live
	// dual-track need refining. A 1:1 topic's tracks already cover its
	// messages; a refined topic has empty tracks and must not be
	// re-extracted (idempotent no-op). This also covers empty L4Refs.
	if len(topic.L4Refs) <= 2 ||
		(len(topic.UserKeywords) == 0 && len(topic.AgentKeywords) == 0) {
		return nil
	}
	// Read all L4 originals in L4Refs order (mode 3 returns in input
	// order, missing skipped) and join them as the extraction source.
	keywords, err := llmops.ExtractKeywords(ctx, db.llm, db.joinedL4Messages(agentID, topic))
	if err != nil {
		return err
	}
	// Never persist an empty extraction: the guard guarantees the dual
	// track is non-empty, so clearing it with no replacement would be
	// destructive and unrecoverable (later calls would skip as refined).
	if len(keywords) == 0 {
		return common.NewError(common.ErrLLM, "refine extracted no keywords")
	}
	if !repo.RefineTopicKeywordsL2(db.engine, agentID, parsedID, keywords) {
		return common.NewError(common.ErrIO, "refine topic keywords", nil)
	}
	// Refresh the L2Meta entry then rebuild the BM25 document: AddDocument
	// replaces the old user/agent terms. storage → l2meta → sparse order.
	ac.syncL2Meta(db, parsedID)
	terms := index.Tokenize(strings.Join(keywords, " "))
	ac.sparseIndex.AddDocument(parsedID, terms, uint32(len(terms)))
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
