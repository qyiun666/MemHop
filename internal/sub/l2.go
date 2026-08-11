// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 scene operations of the sub layer: list / merge.

package sub

import (
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

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

// MergeScenes rewrites all topics of secondary scenes to the primary scene
// and deletes the secondary records; caller holds the write lock.
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
	// Drop merged secondary scenes so Dream does not spin empty goroutines.
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
