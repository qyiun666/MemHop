// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 超级图操作：SceneNode 写盘 + L1ReverseIndex 索引同步一体化。
// dream 通过新的 L2 depth≤2 话题调 CreateNode/UpdateNode 更新 L1；
// search 调 FindAssociatedNodes 按选中场景找关联上下文。
package l1

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/common/timeutil"
	"github.com/qyiun666/MemHop/internal/repo/index"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// CreateNode 新建 L1 节点：写入 SceneNode 记录并注册到 L1 反查索引，
// 返回节点 ID。ID = hash(sceneID:topics)。
func CreateNode(engine *storage.StorageEngine, l1Idx *index.L1ReverseIndex, sceneID string, topicIDs []uint64) (uint64, error) {
	sceneHash, err := hash.ParseID(sceneID)
	if err != nil {
		return 0, mherrors.NewError(mherrors.ErrInvalidQuery, "parse scene id", err)
	}
	// ID = hash(sceneID:topics)，与 spec 公式一致
	nodeID := hash.HashID(fmt.Sprintf("%s:%v", sceneID, topicIDs))
	now := timeutil.NowMs()
	node := &model.SceneNode{
		IDHash:    nodeID,
		SceneID:   sceneHash,
		TopicIDs:  topicIDs,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := record.WriteSceneNode(engine, nodeID, node); err != nil {
		return 0, err
	}
	l1Idx.Add(sceneHash, nodeID)
	return nodeID, nil
}

// UpdateNode 全量覆盖写回节点（ID 以参数为准）并同步反查索引：
// 先从所有场景移除旧注册，再按新 SceneID 注册。
func UpdateNode(engine *storage.StorageEngine, l1Idx *index.L1ReverseIndex, id string, slot *model.SceneNode) error {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return mherrors.NewError(mherrors.ErrInvalidQuery, "parse node id", err)
	}
	if _, err := record.ReadSceneNode(engine, idHash); err != nil {
		return err
	}
	slot.IDHash = idHash
	slot.UpdatedAt = timeutil.NowMs()
	if err := record.WriteSceneNode(engine, idHash, slot); err != nil {
		return err
	}
	l1Idx.RemoveNode(idHash)
	l1Idx.Add(slot.SceneID, idHash)
	return nil
}

// RebuildL1Index 全量扫盘重建 L1 反查索引（dream 压缩后可整体刷新）。
func RebuildL1Index(engine *storage.StorageEngine) *index.L1ReverseIndex {
	return index.BuildL1ReverseIndex(engine)
}

// ListNodes 按场景查询节点（nil 表示全部）。
func ListNodes(engine *storage.StorageEngine, sceneID *string) []model.SceneNode {
	var sceneHash uint64
	filter := false
	if sceneID != nil {
		h, err := hash.ParseID(*sceneID)
		if err != nil {
			return nil
		}
		sceneHash = h
		filter = true
	}
	var out []model.SceneNode
	for _, node := range record.CollectAllSceneNodes(engine) {
		if filter && node.SceneID != sceneHash {
			continue
		}
		out = append(out, node)
	}
	return out
}

// FindAssociatedNodes 根据选中的场景列表通过 L1 反查索引找关联节点，
// 返回节点记录（上层取 node.TopicIDs 即关联上下文）。
func FindAssociatedNodes(engine *storage.StorageEngine, l1Idx *index.L1ReverseIndex, sceneIDs []string) []model.SceneNode {
	ctxSet := make(map[uint64]struct{}, len(sceneIDs))
	for _, sid := range sceneIDs {
		h, err := hash.ParseID(sid)
		if err != nil {
			continue
		}
		ctxSet[h] = struct{}{}
	}
	var out []model.SceneNode
	for _, nodeID := range l1Idx.FindAssociated(ctxSet) {
		node, err := record.ReadSceneNode(engine, nodeID)
		if err != nil {
			continue
		}
		out = append(out, *node)
	}
	return out
}
