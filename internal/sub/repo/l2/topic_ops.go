// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 话题新增与更新原语。
package l2

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// CreateTopic 新增话题：ID 由 ComputeTopicID 生成（sceneID + 双时间戳哈希），
// 初始 depth=1，写入文件后返回新话题 ID。
func CreateTopic(
	engine *storage.StorageEngine,
	sceneID string,
	userKeywords, agentKeywords []string,
	userTS, agentTS int64,
) (uint64, error) {
	sceneHash, err := hash.ParseID(sceneID)
	if err != nil {
		return 0, mherrors.NewError(mherrors.ErrInvalidQuery, "parse scene id", err)
	}
	topic := model.TopicSlot{
		ID:             model.ComputeTopicID(sceneHash, userTS, agentTS),
		SceneID:        sceneHash,
		Depth:          1,
		UserKeywords:   userKeywords,
		UserTimestamp:  userTS,
		AgentKeywords:  agentKeywords,
		AgentTimestamp: agentTS,
	}
	if err := record.WriteTopicSlot(engine, topic.ID, &topic); err != nil {
		return 0, err
	}
	return topic.ID, nil
}

// UpdateTopic 全量覆盖话题的关键词与时间戳并写回文件。
func UpdateTopic(
	engine *storage.StorageEngine,
	id string,
	userKeywords, agentKeywords []string,
	userTS, agentTS int64,
) error {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return mherrors.NewError(mherrors.ErrInvalidQuery, "parse topic id", err)
	}
	topic, err := record.ReadTopicLenient(engine, idHash)
	if err != nil {
		return err
	}
	if topic == nil {
		return mherrors.ErrNotFound
	}
	topic.UserKeywords = userKeywords
	topic.UserTimestamp = userTS
	topic.AgentKeywords = agentKeywords
	topic.AgentTimestamp = agentTS
	return record.WriteTopicSlot(engine, idHash, topic)
}
