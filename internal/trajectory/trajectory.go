// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package trajectory holds the L6 turn-trajectory small methods: reading
// one turn's events through the domain index, the payload-budget trim and
// the crystallize write steps that fold LLM candidates into L5. The big
// methods (AppendTrajectory, ReadTrajectory, Crystallize, ...) stay in the
// composition root with the domain lock.

package trajectory

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// MaxCrystallizePayload caps the trajectory payload bytes fed to one
// crystallize LLM call; over-budget events drop from the oldest.
const MaxCrystallizePayload = 128 * 1024

// MaxEventPayload caps a single event payload (no raw token streams). An
// event over the budget is refused: the payload is the host's own record of
// what happened, and silently shortening it would leave a truncated event
// that reads exactly like a complete one.
const MaxEventPayload = 4 * 1024

// ValidateEvent checks what every L6 write path requires of an event, before
// any record or node is touched.
func ValidateEvent(ev core.TrajectorySlot) error {
	if ev.EventType == "" || ev.Timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "EventType and Timestamp are required")
	}
	if len(ev.Payload) > MaxEventPayload {
		return common.NewError(common.ErrInvalidQuery,
			fmt.Sprintf("payload of %d bytes exceeds the %d-byte event budget", len(ev.Payload), MaxEventPayload))
	}
	return nil
}

// ReadTurn loads one turn's events (Seq ascending) via the domain index. A
// record the index names but the engine cannot read is an error, not a
// shorter transcript: a silently missing event is indistinguishable from one
// that was never written.
func ReadTurn(engine *core.StorageEngine, agentID uint64, ac *domain.Context, sessionID uint64) ([]core.TrajectorySlot, error) {
	hashes := ac.Traj.EventHashes(sessionID)
	out := make([]core.TrajectorySlot, 0, len(hashes))
	for _, h := range hashes {
		ev, err := core.ReadTrajectorySlot(engine, agentID, h)
		if err != nil {
			return nil, common.NewError(common.ErrIO, "read trajectory event", err)
		}
		out = append(out, *ev)
	}
	return out, nil
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
