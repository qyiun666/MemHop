// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 operation trajectory operations: host-appended event log per session.
// Short-lived by design; purged by the host via DeleteTrajectory or
// PruneTrajectoryBefore (Dream does not participate in L6).
package repo

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// AppendTrajectory writes one trajectory event; ID = hash(sessionID:seq).
// Re-writing the same sessionID+seq points the index at the newest record
// (append-only upsert).
func AppendTrajectory(engine *core.StorageEngine, agentID uint64, ev core.TrajectorySlot) error {
	ev.IDHash = common.HashID(fmt.Sprintf("%d:%d", ev.SessionID, ev.Seq))
	return core.WriteTrajectorySlot(engine, agentID, ev.IDHash, &ev)
}

// ReadTrajectory returns all events of a session ordered by Seq ascending.
func ReadTrajectory(engine *core.StorageEngine, agentID uint64, sessionID uint64) ([]core.TrajectorySlot, error) {
	var out []core.TrajectorySlot
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.SessionID == sessionID {
			out = append(out, ev)
		}
	}
	if out == nil {
		out = []core.TrajectorySlot{}
	}
	slices.SortFunc(out, func(a, b core.TrajectorySlot) int {
		return cmp.Compare(a.Seq, b.Seq)
	})
	return out, nil
}

// DeleteTrajectory batch-deletes all events of a session; no-op when the
// session has no trajectory.
func DeleteTrajectory(engine *core.StorageEngine, agentID uint64, sessionID uint64) error {
	var targets []uint64
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.SessionID == sessionID {
			targets = append(targets, ev.IDHash)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	_, err := engine.DeleteRecordBatch(agentID, targets)
	return err
}

// ListTrajectorySessions summarizes every session of the domain's L6 log
// (event count and latest timestamp each), sorted by external hex id for a
// deterministic order.
func ListTrajectorySessions(engine *core.StorageEngine, agentID uint64) ([]core.TrajectorySessionSummary, error) {
	type agg struct {
		steps int
		last  int64
	}
	bySession := make(map[uint64]agg)
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		a := bySession[ev.SessionID]
		a.steps++
		if ev.Timestamp > a.last {
			a.last = ev.Timestamp
		}
		bySession[ev.SessionID] = a
	}
	out := make([]core.TrajectorySessionSummary, 0, len(bySession))
	for sid, a := range bySession {
		out = append(out, core.TrajectorySessionSummary{
			SessionID:    common.FormatHash(sid),
			Steps:        a.steps,
			LastAppendAt: a.last,
		})
	}
	slices.SortFunc(out, func(x, y core.TrajectorySessionSummary) int {
		return cmp.Compare(x.SessionID, y.SessionID)
	})
	return out, nil
}

// PruneTrajectoryBefore deletes events strictly older than before (Unix ms)
// across every session of the domain and returns how many were removed;
// newer events are untouched.
func PruneTrajectoryBefore(engine *core.StorageEngine, agentID uint64, before int64) (int, error) {
	var targets []uint64
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.Timestamp < before {
			targets = append(targets, ev.IDHash)
		}
	}
	if len(targets) == 0 {
		return 0, nil
	}
	if _, err := engine.DeleteRecordBatch(agentID, targets); err != nil {
		return 0, common.NewError(common.ErrIO, "prune trajectory", err)
	}
	return len(targets), nil
}
