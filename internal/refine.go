// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

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
	ac, err := db.contextFor(agentID)
	if err != nil {
		return err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		return err
	}
	// Validate the topic before any LLM call: a missing topic must not
	// leave a half-written refine behind.
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  db.engine,
		AgentID: agentID,
		MetaIdx: ac.l2Meta,
		SceneID: topicID,
		Depth:   0,
		Num:     3,
	})
	if err != nil {
		return err
	}
	if len(topics) == 0 {
		return common.NewError(common.ErrNotFound, "topic not found")
	}
	topic := topics[0]
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
	ids := make([]string, 0, len(topic.L4Refs))
	for _, id := range topic.L4Refs {
		ids = append(ids, common.FormatHash(id))
	}
	archives := repo.QueryArchiveL4(db.engine, agentID, 3, "", 0, 0, ids)
	parts := make([]string, 0, len(archives))
	for _, a := range archives {
		parts = append(parts, a.Content)
	}
	keywords, err := db.llm.ExtractKeywords(ctx, strings.Join(parts, "\n"))
	if err != nil {
		return err
	}
	// Never persist an empty extraction: the guard guarantees the dual
	// track is non-empty, so clearing it with no replacement would be
	// destructive and unrecoverable (later calls would skip as refined).
	if len(keywords) == 0 {
		return common.NewError(common.ErrLLM, "refine extracted no keywords")
	}
	if !repo.RefineTopicKeywordsL2(db.engine, agentID, topicID, keywords) {
		return common.NewError(common.ErrIO, "refine topic keywords", nil)
	}
	// Refresh the L2Meta entry then rebuild the BM25 document: AddDocument
	// replaces the old user/agent terms. storage → l2meta → sparse order.
	ac.syncL2Meta(db, parsedID)
	terms := index.Tokenize(strings.Join(keywords, " "))
	ac.sparseIndex.AddDocument(parsedID, terms, uint32(len(terms)))
	return nil
}
