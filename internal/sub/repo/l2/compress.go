// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 压缩纯数据链：把一组话题挂到指定父话题下（ParentID 改写 + Depth 下沉），
// 深度达到阈值的直接删除（只删话题记录），聚合返回 L3Refs 与时间戳边界。
// 所有修改先收集，最后一次性批量落盘（一次 fsync 写入 + 一次 fsync 删除）。
package l2

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// MaxDepth 话题下沉后触发删除的深度阈值。
const MaxDepth = 4

// CompressResult 是压缩聚合结果。
type CompressResult struct {
	L3Refs         []uint64 // L3 合体去重后的引用列表
	UserTimestamp  int64    // 最早的用户时间戳
	AgentTimestamp int64    // 最晚的 agent 时间戳
}

// CompressTopics 遍历话题 id 列表：每个话题 ParentID 改为 parentID、Depth+1，
// depth >= MaxDepth 的直接删除，其余写回文件。全部收集后一次性批量落盘。
// 返回去重后的 L3Refs、最早的 UserTimestamp 和 最晚的 AgentTimestamp。
func CompressTopics(engine *storage.StorageEngine, ids []uint64, parentID uint64) (*CompressResult, error) {
	result := &CompressResult{
		UserTimestamp:  math.MaxInt64,
		AgentTimestamp: math.MinInt64,
	}
	var writes []storage.RecordEntry
	var deletes []uint64
	for _, id := range ids {
		topic, err := record.ReadTopicLenient(engine, id)
		if err != nil || topic == nil {
			continue // 记录不存在或非话题类型：跳过
		}
		topic.Depth++
		topic.ParentID = &parentID

		result.L3Refs = append(result.L3Refs, topic.L3Refs...)
		if topic.UserTimestamp < result.UserTimestamp {
			result.UserTimestamp = topic.UserTimestamp
		}
		if topic.AgentTimestamp > result.AgentTimestamp {
			result.AgentTimestamp = topic.AgentTimestamp
		}

		if topic.Depth >= MaxDepth {
			deletes = append(deletes, topic.ID)
			continue
		}
		data, err := json.Marshal(topic)
		if err != nil {
			return result, err
		}
		writes = append(writes, storage.RecordEntry{
			RecordType: storage.RecL2Topic,
			IDHash:     topic.ID,
			Data:       data,
		})
	}
	if len(writes) > 0 {
		if _, err := engine.WriteRecordBatch(writes); err != nil {
			return result, err
		}
	}
	if len(deletes) > 0 {
		if _, err := engine.DeleteRecordBatch(deletes); err != nil {
			return result, err
		}
	}
	if result.UserTimestamp == math.MaxInt64 {
		result.UserTimestamp = 0
	}
	if result.AgentTimestamp == math.MinInt64 {
		result.AgentTimestamp = 0
	}
	result.L3Refs = dedupSorted(result.L3Refs)
	return result, nil
}

// dedupSorted 排序并去重 id 列表。
func dedupSorted(ids []uint64) []uint64 {
	if len(ids) < 2 {
		return ids
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := ids[:1]
	for i := 1; i < len(ids); i++ {
		if ids[i] != out[len(out)-1] {
			out = append(out, ids[i])
		}
	}
	return out
}
