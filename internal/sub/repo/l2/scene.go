// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 场景操作：SceneSlot 落盘为 RecL2Scene 记录，场景查询与创建走 record 原语。
package l2

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// ListScenes 按场景 id 列表查询场景，不存在的场景跳过。
func ListScenes(engine *storage.StorageEngine, ids []string) []model.SceneSlot {
	var out []model.SceneSlot
	for _, id := range ids {
		sceneHash, err := hash.ParseID(id)
		if err != nil {
			continue
		}
		slot, err := record.ReadSceneSlot(engine, sceneHash)
		if err != nil {
			continue
		}
		out = append(out, *slot)
	}
	return out
}

// CreateScene 新增场景：ID 由场景名哈希生成并写入文件，返回场景 ID。
func CreateScene(engine *storage.StorageEngine, name string) (uint64, error) {
	slot := model.NewSceneSlot(name)
	if err := record.WriteSceneSlot(engine, slot.SceneID, &slot); err != nil {
		return 0, err
	}
	return slot.SceneID, nil
}
