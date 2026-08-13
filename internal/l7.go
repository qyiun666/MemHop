// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L7 API of the internal assembly layer: thin delegation to the sub layer
// DB methods, reusing the DB instance returned by Open.

package memhop

import (
	"context"

	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// Thin wrapper; write op, delegates under the write lock. The write lock
// serializes Seq allocation in AppendTrajectory so concurrent appends to
// the same session cannot overwrite each other.
func (db *DB) AppendTrajectory(sessionID string, ev core.TrajectorySlot) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.AppendTrajectory(sessionID, ev)
}

// Thin wrapper; see internal/sub/l7.go ((db *DB) ReadTrajectory).
func (db *DB) ReadTrajectory(sessionID string) ([]core.TrajectorySlot, error) {
	return db.DB.ReadTrajectory(sessionID)
}

// Thin wrapper; write op, delegates under the write lock.
func (db *DB) DeleteTrajectory(sessionID string) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.DeleteTrajectory(sessionID)
}

// Thin wrapper; see internal/sub/l7.go ((db *DB) Crystallize).
func (db *DB) Crystallize(ctx context.Context, sessionID string) (*sub.CrystallizeResult, error) {
	return db.DB.Crystallize(ctx, sessionID)
}
