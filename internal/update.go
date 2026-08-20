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

// Update appends an agent reply to the specified topic. It returns
// (true, nil) on success and (false, err) on any failure so hosts no longer
// have to guess whether a false result was validation, LLM, or storage.
func (db *DB) Update(topicID string, text string, timestamp int64) (bool, error) {
	if err := db.beginRead(); err != nil {
		return false, err
	}
	defer db.mu.RUnlock()
	if text == "" || timestamp <= 0 {
		return false, common.NewError(common.ErrInvalidQuery, "Update requires text and a positive timestamp")
	}
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		return false, err
	}
	// Validate the topic before any write or LLM call: a missing topic must
	// not leave an orphan L4 archive behind.
	topics, err := repo.ListTopicsL2(db.engine, topicID, 0, 3)
	if err != nil {
		return false, err
	}
	if len(topics) == 0 {
		return false, common.NewError(common.ErrNotFound, "topic not found")
	}
	topic := topics[0]
	keywords, err := db.llm.ExtractKeywords(context.Background(), text)
	if err != nil {
		return false, err
	}
	archiveID, err := repo.AppendArchiveL4(db.engine, topicID, 1, core.ContentText, text, timestamp)
	if err != nil {
		return false, err
	}
	if !repo.UpdateTopicL4RefsL2(db.engine, topicID, []uint64{archiveID}) {
		return false, common.NewError(common.ErrIO, "update topic l4 ref", nil)
	}
	if !repo.UpdateTopicL2(db.engine, topicID, keywords, timestamp) {
		return false, common.NewError(common.ErrIO, "update topic keywords", nil)
	}
	// Update BM25: uncompressed topics carry User+Agent keywords (compressed
	// use FusedKeywords); topic.AgentKeywords is stale here, use fresh ones.
	all := make([]string, 0, len(topic.UserKeywords)+len(keywords)+len(topic.FusedKeywords))
	all = append(all, topic.UserKeywords...)
	all = append(all, keywords...)
	all = append(all, topic.FusedKeywords...)
	terms := index.Tokenize(strings.Join(all, " "))
	db.sparseIndex.AddDocument(parsedID, terms, uint32(len(terms)))
	return true, nil
}
