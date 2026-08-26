// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 API of the public facade: thin delegation to the internal layer
// DB methods, reusing the DB instance returned by Open.

package api

import (
	"context"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Thin wrapper; the domain lock serializes Seq allocation in
// AppendTrajectory so concurrent appends to the same session cannot
// overwrite each other.
func (db *DB) AppendTrajectory(sessionID string, ev TrajectorySlot) error {
	return db.DB.AppendTrajectory(core.DefaultAgentID, sessionID, ev)
}

// Thin wrapper; see internal/l6.go ((db *DB) ReadTrajectory).
func (db *DB) ReadTrajectory(sessionID string) ([]TrajectorySlot, error) {
	return db.DB.ReadTrajectory(core.DefaultAgentID, sessionID)
}

// Thin wrapper; see internal/l6.go ((db *DB) TrajectoryStats).
func (db *DB) TrajectoryStats(sessionID string) (*TrajectoryStats, error) {
	return db.DB.TrajectoryStats(core.DefaultAgentID, sessionID)
}

// Thin wrapper; see internal/l6.go ((db *DB) DeleteTrajectory).
func (db *DB) DeleteTrajectory(sessionID string) error {
	return db.DB.DeleteTrajectory(core.DefaultAgentID, sessionID)
}

// Thin wrapper; see internal/l6.go ((db *DB) Crystallize).
func (db *DB) Crystallize(ctx context.Context, sessionID string) (*CrystallizeResult, error) {
	return db.DB.Crystallize(ctx, core.DefaultAgentID, sessionID)
}
