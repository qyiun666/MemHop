// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"context"
	"strings"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	"github.com/qyiun666/MemHop/internal/sub/repo/index"
)

// Update appends an agent reply to the specified topic.
func (db *DB) Update(topicID string, text string, timestamp int64) bool {
	if err := db.beginRead(); err != nil {
		return false
	}
	defer db.mu.RUnlock()
	if text == "" || timestamp <= 0 {
		return false
	}
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		return false
	}
	keywords, err := db.llm.ExtractKeywords(context.Background(), text)
	if err != nil {
		return false
	}
	topicIDStr := common.FormatHash(parsedID)
	archiveID, err := repo.AppendArchiveL4(db.engine, topicIDStr, 1, core.ContentText, text, timestamp)
	if err != nil {
		return false
	}
	// Read back the topic (missing fails) and write agent keywords.
	topics, err := repo.ListTopicsL2(db.engine, topicIDStr, 0, 3)
	if err != nil {
		return false
	}
	topic := topics[0]
	if !repo.UpdateTopicL4RefsL2(db.engine, topicIDStr, []uint64{archiveID}) {
		return false
	}
	if !repo.UpdateTopicL2(db.engine, topicIDStr, keywords, timestamp) {
		return false
	}
	// Update BM25: uncompressed topics carry User+Agent keywords (compressed
	// use FusedKeywords); topic.AgentKeywords is stale here, use fresh ones.
	all := make([]string, 0, len(topic.UserKeywords)+len(keywords)+len(topic.FusedKeywords))
	all = append(all, topic.UserKeywords...)
	all = append(all, keywords...)
	all = append(all, topic.FusedKeywords...)
	terms := index.Tokenize(strings.Join(all, " "))
	db.sparseIndex.AddDocument(parsedID, terms, uint32(len(terms)))
	return true
}
