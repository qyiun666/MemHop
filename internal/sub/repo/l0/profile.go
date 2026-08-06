// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 画像操作：单例 ProfileSlot，固定 ID = hash("profile")（与 crud/l0_ops.go
// 既有约定一致）。外部接口更新字段与 dream 蒸馏更新共用 UpdateProfile。
package l0

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// GetProfile 读取 L0 画像单例，不存在返回 ErrNotFound。
func GetProfile(engine *storage.StorageEngine) (*model.ProfileSlot, error) {
	slot, err := record.ReadProfileSlot(engine, hash.HashID("profile"))
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrNotFound, "profile not found", err)
	}
	return slot, nil
}

// UpdateProfile 全量覆盖写回画像单例（ID 强制为固定 ID）。
func UpdateProfile(engine *storage.StorageEngine, slot *model.ProfileSlot) error {
	slot.IDHash = hash.HashID("profile")
	return record.WriteProfileSlot(engine, slot.IDHash, slot)
}
