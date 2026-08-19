// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"context"

	"github.com/qyiun666/MemHop/internal/common"
)

// Dream runs the L2 compression over the given scene (or all active scenes
// when sceneID is empty), then L1 rebuild/decay.
func (db *DB) Dream(ctx context.Context, sceneID string) (bool, error) {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return false, common.NewError(common.ErrClosed, "database is closed")
	}
	if sceneID == "" && !db.DB.HasActiveScenes() {
		return true, nil // no active scenes: nothing to do, succeed
	}
	ok, err := db.DB.RunDream(ctx, sceneID)
	if err != nil {
		return false, err
	}
	if ok {
		db.DB.TouchLastDreamAt()
	}
	return ok, nil
}
