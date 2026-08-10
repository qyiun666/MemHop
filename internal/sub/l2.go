// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene operations of the sub layer: list / merge.

package sub

import (
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// ListScenes 列出全部场景（SceneSlot：scene_id + scene_name）。
func (db *DB) ListScenes() ([]core.SceneSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	all := repo.CollectAllScenesL2(db.engine)
	if all == nil {
		return []core.SceneSlot{}, nil
	}
	return all, nil
}

// MergeScenes 将 secondaryIDs 场景的全部话题改写归属到主场景，并删除副场景记录。
// 无锁实现：调用方（根层薄层）已持写锁。
func (db *DB) MergeScenes(primaryID string, secondaryIDs []string) error {
	if _, err := common.ParseID(primaryID); err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse primary scene id", err)
	}
	if len(secondaryIDs) == 0 {
		return common.NewError(common.ErrInvalidQuery, "secondary scene ids are required")
	}
	if !repo.MergeScenesL2(db.engine, primaryID, secondaryIDs) {
		return common.NewError(common.ErrIO, "merge scenes", nil)
	}
	// 移除已合并副场景，避免 Dream 对空场景空跑 goroutine。
	if hashes, ok := common.ParseAll(secondaryIDs); ok {
		removed := common.ToSet(hashes)
		kept := db.activeScenes[:0]
		for _, sid := range db.activeScenes {
			if _, drop := removed[sid]; !drop {
				kept = append(kept, sid)
			}
		}
		db.activeScenes = kept
	}
	return nil
}
