// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 话题列表查询：按场景 + 深度筛选，按用户时间戳排序。
package l2

import (
	"sort"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// ListAllTopics 遍历并返回全部话题，按 UserTimestamp 升序排序。
func ListAllTopics(engine *storage.StorageEngine) []model.TopicSlot {
	all := record.CollectAllTopics(engine)
	sort.Slice(all, func(i, j int) bool { return all[i].UserTimestamp < all[j].UserTimestamp })
	return all
}

// ListTopics 查询指定场景中 depth 深度的所有话题（depth==0 视为 1），
// 按 UserTimestamp 升序返回。
func ListTopics(engine *storage.StorageEngine, sceneID string, depth uint8) ([]model.TopicSlot, error) {
	sceneHash, err := hash.ParseID(sceneID)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse scene id", err)
	}
	if depth == 0 {
		depth = 1
	}
	var out []model.TopicSlot
	for _, topic := range record.CollectAllTopics(engine) {
		if topic.SceneID == sceneHash && topic.Depth == depth {
			out = append(out, topic)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserTimestamp < out[j].UserTimestamp })
	return out, nil
}
