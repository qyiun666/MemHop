// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 profile operations of the sub layer: thin wrappers over the repo layer.

package sub

import (
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// GetL0 读取 L0 画像单例；不存在返回 ErrNotFound。
func (db *DB) GetL0() (*core.ProfileSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	return repo.GetProfileL0(db.engine)
}

// UpdateL0 全量覆盖写回画像（ID 强制固定为 hash("profile")）。
// 写锁由 internal 层组合（Lock/Unlock），此处不重复加锁。
func (db *DB) UpdateL0(slot *core.ProfileSlot) error {
	return repo.UpdateProfileL0(db.engine, slot)
}
