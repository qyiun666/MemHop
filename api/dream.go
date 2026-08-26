// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"context"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Dream runs the L2 compression over the given scene (or all active scenes
// when sceneID is empty), then L1 rebuild/decay. RunDream takes the domain
// lock itself and marks the Dream timestamp on success.
func (db *DB) Dream(ctx context.Context, sceneID string) (bool, error) {
	if db.DB.IsClosed() {
		return false, common.NewError(common.ErrClosed, "database is closed")
	}
	if sceneID == "" && !db.DB.HasActiveScenes() {
		return true, nil // no active scenes: nothing to do, succeed
	}
	return db.DB.RunDream(ctx, core.DefaultAgentID, sceneID)
}
