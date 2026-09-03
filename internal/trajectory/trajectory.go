// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package trajectory holds the L6 turn-trajectory small methods: reading
// one turn's events through the domain index, the payload-budget trim and
// the crystallize write steps that fold LLM candidates into L5. The big
// methods (AppendTrajectory, ReadTrajectory, Crystallize, ...) stay in the
// composition root with the domain lock.

package trajectory

import (
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// MaxCrystallizePayload caps the trajectory payload bytes fed to one
// crystallize LLM call; over-budget events drop from the oldest.
const MaxCrystallizePayload = 128 * 1024

// ReadTurn loads one turn's events (Seq ascending) via the domain index;
// corrupt records are skipped, mirroring the scan-based reader it replaces.
func ReadTurn(engine *core.StorageEngine, agentID uint64, ac *domain.Context, sessionID uint64) []core.TrajectorySlot {
	hashes := ac.Traj.EventHashes(sessionID)
	out := make([]core.TrajectorySlot, 0, len(hashes))
	for _, h := range hashes {
		if ev, err := core.ReadTrajectorySlot(engine, agentID, h); err == nil {
			out = append(out, *ev)
		}
	}
	return out
}

// TrimByBudget keeps the newest events within budget payload
// bytes (at least one). ponytail: dropping the oldest is lossy for very
// long turns; map-reduce induction over chunks is the upgrade path.
func TrimByBudget(events []core.TrajectorySlot, budget int) []core.TrajectorySlot {
	total := 0
	start := len(events)
	for start > 0 {
		p := len(events[start-1].Payload)
		if total+p > budget {
			break
		}
		total += p
		start--
	}
	if start == len(events) && len(events) > 0 {
		start = len(events) - 1
	}
	return events[start:]
}
