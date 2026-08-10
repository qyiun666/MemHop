// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 API of the internal assembly layer: thin delegation to the sub layer
// DB methods, reusing the DB instance returned by Open.

package memhop

import (
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// ListScenes 薄层：实现见 internal/sub/l2.go（(db *DB) ListScenes）。
func (db *DB) ListScenes() ([]core.SceneSlot, error) {
	return db.DB.ListScenes()
}

// MergeScenes 薄层：写操作，持写锁后委托 sub 实现。
func (db *DB) MergeScenes(primaryID string, secondaryIDs []string) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.MergeScenes(primaryID, secondaryIDs)
}
