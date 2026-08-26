// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// Update appends an agent reply to the specified topic. It returns an error
// on any failure (validation, LLM, or storage); nil means the reply was
// appended and all indexes were refreshed.
func (db *DB) Update(topicID string, text string, timestamp int64) error {
	if err := db.beginRead(); err != nil {
		return err
	}
	defer db.mu.RUnlock()
	if text == "" || timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "Update requires text and a positive timestamp")
	}
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		return err
	}
	// Validate the topic before any write or LLM call: a missing topic must
	// not leave an orphan L4 archive behind.
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  db.engine,
		MetaIdx: db.l2Meta,
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
	keywords, err := db.llm.ExtractKeywords(context.Background(), text)
	if err != nil {
		return err
	}
	archiveID, err := repo.AppendArchiveL4(db.engine, core.DefaultAgentID, topicID, 1, core.ContentText, text, timestamp)
	if err != nil {
		return err
	}
	if !repo.UpdateTopicL4RefsL2(db.engine, core.DefaultAgentID, topicID, []uint64{archiveID}) {
		return common.NewError(common.ErrIO, "update topic l4 ref", nil)
	}
	if !repo.UpdateTopicL2(db.engine, core.DefaultAgentID, topicID, keywords, timestamp) {
		return common.NewError(common.ErrIO, "update topic keywords", nil)
	}
	// Update BM25: uncompressed topics carry User+Agent keywords (compressed
	// use FusedKeywords); topic.AgentKeywords is stale here, use fresh ones.
	// Refresh the L2Meta entry first per the storage → l2meta → sparse order.
	db.syncL2Meta(parsedID)
	all := make([]string, 0, len(topic.UserKeywords)+len(keywords)+len(topic.FusedKeywords))
	all = append(all, topic.UserKeywords...)
	all = append(all, keywords...)
	all = append(all, topic.FusedKeywords...)
	terms := index.Tokenize(strings.Join(all, " "))
	db.sparseIndex.AddDocument(parsedID, terms, uint32(len(terms)))
	// Full active set: compress the oldest scene so the next activation has
	// room instead of silently evicting it (best-effort, never fails Update).
	// Pre-check compressibility: a full Dream runs index rebuilds + LLM
	// distill even when no group was merged, so scenes below the compress
	// threshold (few topics keep raw detail) are skipped here. The Dream is
	// scheduled in the background and never blocks this call.
	if capacity := db.config.Defaults.Capacity; capacity > 0 && len(db.activeScenes) >= capacity {
		oldest := db.activeScenes[0]
		if topics, err := repo.ListTopicsL2(repo.TopicListQuery{
			Engine:  db.engine,
			MetaIdx: db.l2Meta,
			SceneID: common.FormatHash(oldest),
			Depth:   1,
			Num:     2,
		}); err == nil && len(topics) >= db.config.Defaults.DreamCompressMinTopics {
			db.triggerSceneDream(oldest)
		}
	}
	return nil
}
