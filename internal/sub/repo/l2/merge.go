// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 场景合并：把副场景的所有话题改挂到主场景，然后删除副场景记录。
// 所有话题改写先收集，最后一次性批量落盘（一次 fsync）。
package l2

import (
	"encoding/json"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// MergeScenes 遍历所有话题：SceneID 属于副场景 id 列表的，改写为主场景 id
// 并收集，遍历结束后一次性批量写回；随后复用 DeleteL2 的场景模式删除副场景
// （此时副场景下已无话题，只删场景记录本身）。成功返回 true。
func MergeScenes(engine *storage.StorageEngine, primaryID string, secondaryIDs []string) bool {
	primaryHash, err := hash.ParseID(primaryID)
	if err != nil {
		return false
	}
	secondaryHashes, ok := parseAll(secondaryIDs)
	if !ok {
		return false
	}
	secondarySet := toSet(secondaryHashes)
	var writes []storage.RecordEntry
	for _, topic := range record.CollectAllTopics(engine) {
		if _, ok := secondarySet[topic.SceneID]; !ok {
			continue
		}
		topic.SceneID = primaryHash
		data, err := json.Marshal(&topic)
		if err != nil {
			return false
		}
		writes = append(writes, storage.RecordEntry{
			RecordType: storage.RecL2Topic,
			IDHash:     topic.ID,
			Data:       data,
		})
	}
	if len(writes) > 0 {
		if _, err := engine.WriteRecordBatch(writes); err != nil {
			return false
		}
	}
	return DeleteL2(engine, secondaryIDs, 1)
}
