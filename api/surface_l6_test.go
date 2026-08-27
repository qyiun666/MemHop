// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 trajectory and crystallize surface tests.

package api

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

func TestSurfaceL6TrajectoryLifecycle(t *testing.T) {
	db, _ := openSurfaceDB(t)
	sessionA := common.FormatHash(common.HashID("lifecycle-a"))
	sessionB := common.FormatHash(common.HashID("lifecycle-b"))
	appendOne := func(id string, ts int64) {
		if err := db.AppendTrajectory(id, TrajectorySlot{EventType: "turn_start", Timestamp: ts}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	appendOne(sessionA, 100)
	appendOne(sessionA, 200)
	appendOne(sessionB, 1_700_000_050_000)

	list, err := db.ListTrajectorySessions()
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v err=%v, want 2 sessions", list, err)
	}
	byID := make(map[string]TrajectorySessionSummary, len(list))
	for _, sum := range list {
		byID[sum.SessionID] = sum
	}
	if sum := byID[sessionA]; sum.Steps != 2 || sum.LastAppendAt != 200 {
		t.Fatalf("summary a mismatch: %+v", sum)
	}
	if sum := byID[sessionB]; sum.Steps != 1 || sum.LastAppendAt != 1_700_000_050_000 {
		t.Fatalf("summary b mismatch: %+v", sum)
	}

	pruned, err := db.PruneTrajectory(1_000)
	if err != nil || pruned != 2 {
		t.Fatalf("prune = %d err=%v, want 2 old events removed", pruned, err)
	}
	list, err = db.ListTrajectorySessions()
	if err != nil || len(list) != 1 || list[0].SessionID != sessionB {
		t.Fatalf("surviving list = %+v err=%v, want only sessionB", list, err)
	}
	// The enumerated hex ID must feed DeleteTrajectory directly.
	if err := db.DeleteTrajectory(list[0].SessionID); err != nil {
		t.Fatalf("delete enumerated session: %v", err)
	}
	if list, err = db.ListTrajectorySessions(); err != nil || len(list) != 0 {
		t.Fatalf("final list = %+v err=%v, want empty", list, err)
	}
}

func TestSurfaceL6Trajectory(t *testing.T) {
	db, _ := openSurfaceDB(t)
	ctx := context.Background()
	sessionID := common.FormatHash(common.HashID("session-42"))
	events := []TrajectorySlot{
		{EventType: "turn_start", Payload: "user asks", Timestamp: 1_700_000_040_000},
		{EventType: "tool_call", Payload: "search", Timestamp: 1_700_000_040_100},
		{EventType: "turn_end", Payload: "replied", Timestamp: 1_700_000_040_200},
	}
	for _, ev := range events {
		if err := db.AppendTrajectory(sessionID, ev); err != nil {
			t.Fatalf("append trajectory: %v", err)
		}
	}
	// Missing required fields must be rejected.
	if err := db.AppendTrajectory(sessionID, TrajectorySlot{Payload: "no type"}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("append invalid event: want ErrInvalidQuery, got %v", err)
	}
	got, err := db.ReadTrajectory(sessionID)
	if err != nil || len(got) != len(events) {
		t.Fatalf("read trajectory: got %d err=%v", len(got), err)
	}
	for i, e := range got {
		if e.Seq != uint64(i+1) {
			t.Fatalf("seq must be 1-based increasing, got %d at %d", e.Seq, i)
		}
	}
	stats, err := db.TrajectoryStats(sessionID)
	if err != nil || stats.Steps != len(events) || stats.ToolUsage == nil {
		t.Fatalf("trajectory stats: %+v err=%v", stats, err)
	}
	// Crystallize runs (stub returns no candidates) and yields a well-formed result.
	cr, err := db.Crystallize(ctx, sessionID)
	if err != nil || cr == nil || cr.CreatedIDs == nil {
		t.Fatalf("crystallize: %v", err)
	}
	if err := db.DeleteTrajectory(sessionID); err != nil {
		t.Fatalf("delete trajectory: %v", err)
	}
}
