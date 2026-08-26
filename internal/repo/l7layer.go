// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L7 operation trajectory operations: host-appended event log per session.
// Short-lived by design; purged by the host via DeleteTrajectory (Dream
// does not participate in L7).
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
