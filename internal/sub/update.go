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
	// 读回话题（不存在即失败），写入 agent 关键词。
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
	// 更新 BM25 索引：未压缩话题的语义载体是 User+Agent 双侧关键词（压缩话题是 FusedKeywords）。
	// 注意 topic 是 UpdateTopicL2 之前读回的，其 AgentKeywords 为旧值，这里用刚提取的 keywords（新 agent 侧）。
	all := make([]string, 0, len(topic.UserKeywords)+len(keywords)+len(topic.FusedKeywords))
	all = append(all, topic.UserKeywords...)
	all = append(all, keywords...)
	all = append(all, topic.FusedKeywords...)
	terms := index.Tokenize(strings.Join(all, " "))
	db.sparseIndex.AddDocument(parsedID, terms, uint32(len(terms)))
	return true
}
