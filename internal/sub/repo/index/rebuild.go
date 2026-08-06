// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// 检索索引统一重建：一次扫盘重建 sparse（仅 depth≤2 话题）、L1Reverse、L2Meta。
// 调用时机：Open、Dream 压缩后、批量写入后需要索引与磁盘一致时。
package index

import (
	"encoding/json"
	"strings"

	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// RebuildSearchIndexes scans the engine once and rebuilds all retrieval
// indexes: the BM25 sparse index (only L2 topics with Depth <= 2), the L1
// reverse index and the L2 meta index.
func RebuildSearchIndexes(engine *storage.StorageEngine) (*SparseIndex, *L1ReverseIndex, *L2MetaIndex, error) {
	sparse := NewSparseIndex()
	err := engine.IterIndexByType(storage.RecL2Topic, func(idHash uint64) error {
		topic, err := readTopicForIndex(engine, idHash)
		if err != nil {
			return nil // 单条损坏/解析失败不影响整体重建
		}
		if topic.Depth > 2 {
			return nil // 只索引 depth≤2 的话题
		}
		text := strings.Join(topic.UserKeywords, " ")
		if len(topic.FusedKeywords) > 0 {
			text += " " + strings.Join(topic.FusedKeywords, " ")
		}
		terms := Tokenize(text)
		sparse.AddDocument(idHash, terms, uint32(len(terms)))
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return sparse, BuildL1ReverseIndex(engine), BuildL2MetaFromEngine(engine), nil
}

// readTopicForIndex reads and deserializes an L2 TopicSlot record.
func readTopicForIndex(engine *storage.StorageEngine, idHash uint64) (*model.TopicSlot, error) {
	_, data, err := engine.ReadRecord(idHash)
	if err != nil {
		return nil, err
	}
	var topic model.TopicSlot
	if err := json.Unmarshal(data, &topic); err != nil {
		return nil, err
	}
	return &topic, nil
}
