// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 删除原语：按场景或按话题批量删除，只删记录本身（不做级联清理）。
package l2

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// DeleteL2 批量删除。num==1 时 ids 是场景 id 列表：遍历所有话题删除 SceneID
// 匹配的，遍历结束后再删除场景记录本身；num==2 时 ids 是话题 id 列表：遍历
// 所有话题删除 id 匹配的。全部收集后一次性批量落盘（一次 fsync）。任何一步
// 失败返回 false。
func DeleteL2(engine *storage.StorageEngine, ids []string, num uint8) bool {
	hashes, ok := parseAll(ids)
	if !ok {
		return false
	}
	var targets []uint64
	switch num {
	case 1: // 场景
		sceneSet := toSet(hashes)
		for _, topic := range record.CollectAllTopics(engine) {
			if _, ok := sceneSet[topic.SceneID]; ok {
				targets = append(targets, topic.ID)
			}
		}
		targets = append(targets, hashes...) // 场景记录本身
	case 2: // 话题
		idSet := toSet(hashes)
		for _, topic := range record.CollectAllTopics(engine) {
			if _, ok := idSet[topic.ID]; ok {
				targets = append(targets, topic.ID)
			}
		}
	default:
		return false
	}
	_, err := engine.DeleteRecordBatch(targets)
	return err == nil
}

// parseAll 把 id 字符串列表解析为 hash 列表，任一解析失败返回 false。
func parseAll(ids []string) ([]uint64, bool) {
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		h, err := hash.ParseID(id)
		if err != nil {
			return nil, false
		}
		out = append(out, h)
	}
	return out, true
}

// toSet 把 id 列表转成集合。
func toSet(ids []uint64) map[uint64]struct{} {
	s := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		s[id] = struct{}{}
	}
	return s
}
