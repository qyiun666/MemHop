// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// 检索索引统一重建：一次扫盘重建 sparse（仅 depth≤2 话题）、L1Reverse、L2Meta。
// 调用时机：Open、Dream 压缩后、批量写入后需要索引与磁盘一致时。
package index

import (
	"encoding/json"
	"strings"

	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// RebuildSearchIndexes scans the engine once and rebuilds all retrieval
// indexes: the BM25 sparse index (only L2 topics with Depth <= 2), the L1
// reverse index and the L2 meta index.
func RebuildSearchIndexes(engine *core.StorageEngine) (*SparseIndex, *L1ReverseIndex, *L2MetaIndex, error) {
	sparse, l1Reverse, l2Meta := buildIndexesFromEngine(engine)
	return sparse, l1Reverse, l2Meta, nil
}

// BuildL1ReverseIndex scans the engine for L1 ContextNode records.
func BuildL1ReverseIndex(engine *core.StorageEngine) *L1ReverseIndex {
	_, l1Reverse, _ := buildIndexesFromEngine(engine)
	return l1Reverse
}

// BuildL2MetaFromEngine scans the storage engine for L2 TopicSlot records.
func BuildL2MetaFromEngine(engine *core.StorageEngine) *L2MetaIndex {
	_, _, l2Meta := buildIndexesFromEngine(engine)
	return l2Meta
}

// buildIndexesFromEngine scans the engine once and builds the sparse, L1
// reverse and L2 meta indexes in a single pass. Corrupt or unparsable
// records are skipped without aborting the rebuild.
func buildIndexesFromEngine(engine *core.StorageEngine) (*SparseIndex, *L1ReverseIndex, *L2MetaIndex) {
	sparse := NewSparseIndex()
	l1Reverse := NewL1ReverseIndex()
	l2Meta := NewL2MetaIndex()
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return true // 单条损坏不影响整体重建
		}
		switch rt {
		case core.RecL1SceneNode:
			var node core.SceneNode
			if json.Unmarshal(data, &node) == nil && node.SceneID != 0 {
				l1Reverse.Add(node.SceneID, idHash)
			}
		case core.RecL2Topic:
			var topic topicSlotJSON
			if json.Unmarshal(data, &topic) != nil {
				return true // 单条解析失败不影响整体重建
			}
			if topic.Depth <= 2 {
				// 未压缩话题的语义载体是 User+Agent 双侧关键词，压缩话题是 FusedKeywords。
				text := strings.Join(topic.UserKeywords, " ")
				if len(topic.AgentKeywords) > 0 {
					text += " " + strings.Join(topic.AgentKeywords, " ")
				}
				if len(topic.FusedKeywords) > 0 {
					text += " " + strings.Join(topic.FusedKeywords, " ")
				}
				terms := Tokenize(text)
				sparse.AddDocument(idHash, terms, uint32(len(terms)))
			}
			l2Meta.insertMeta(topicToL2Meta(idHash, &topic))
		}
		return true
	})
	return sparse, l1Reverse, l2Meta
}
