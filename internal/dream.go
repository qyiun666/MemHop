// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"context"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

// Dream runs the L2 compression over active scenes, then L1 rebuild/decay.
func (db *DB) Dream(ctx context.Context) (bool, error) {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return false, common.NewError(common.ErrClosed, "database is closed")
	}
	if !db.DB.HasActiveScenes() {
		return true, nil // no active scenes: nothing to do, succeed
	}
	ok, err := db.DB.RunDream(ctx)
	if err != nil {
		return false, err
	}
	if ok {
		db.DB.TouchLastDreamAt()
	}
	return ok, nil
}
