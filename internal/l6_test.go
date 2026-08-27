// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestAppendTrajectorySeqAutoIncrement(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	session := common.FormatHash(99)
	for i := 1; i <= 3; i++ {
		if err := db.AppendTrajectory(core.DefaultAgentID, session, core.TrajectorySlot{EventType: "turn_start", Timestamp: int64(i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}
	for i, ev := range events {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("seq[%d] = %d, want %d", i, ev.Seq, i+1)
		}
	}
}

func TestAppendTrajectoryValidation(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(1), core.TrajectorySlot{Timestamp: 1}); err == nil {
		t.Fatal("empty event type should fail")
	}
	if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(1), core.TrajectorySlot{EventType: "tool_call"}); err == nil {
		t.Fatal("zero timestamp should fail")
	}
}

func TestAppendTrajectoryPayloadTruncated(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	long := strings.Repeat("x", maxTrajectoryPayload+100)
	if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(3), core.TrajectorySlot{EventType: "tool_call", Payload: long, Timestamp: 1}); err != nil {
		t.Fatalf("append: %v", err)
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, common.FormatHash(3))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 || len(events[0].Payload) > maxTrajectoryPayload {
		t.Fatalf("payload not truncated: %d bytes", len(events[0].Payload))
	}
}

func TestListAndPruneTrajectorySessions(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	a, b := common.FormatHash(11), common.FormatHash(22)
	appendOne := func(id string, ts int64) {
		if err := db.AppendTrajectory(core.DefaultAgentID, id, core.TrajectorySlot{EventType: "turn_start", Timestamp: ts}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	appendOne(a, 100)
	appendOne(a, 900)
	appendOne(b, 500)

	list, err := db.ListTrajectorySessions(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 sessions, got %+v", list)
	}
	byID := make(map[string]core.TrajectorySessionSummary, len(list))
	for _, sum := range list {
		byID[sum.SessionID] = sum
	}
	if sum := byID[a]; sum.Steps != 2 || sum.LastAppendAt != 900 {
		t.Fatalf("session a summary mismatch: %+v", sum)
	}
	if sum := byID[b]; sum.Steps != 1 || sum.LastAppendAt != 500 {
		t.Fatalf("session b summary mismatch: %+v", sum)
	}

	pruned, err := db.PruneTrajectory(core.DefaultAgentID, 550)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned != 2 {
		t.Fatalf("pruned = %d, want 2 (a@100, b@500)", pruned)
	}
	list, err = db.ListTrajectorySessions(core.DefaultAgentID)
	if err != nil || len(list) != 1 || list[0].SessionID != a {
		t.Fatalf("only session a survives: %+v err=%v", list, err)
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, b)
	if err != nil || len(events) != 0 {
		t.Fatalf("pruned session must read empty: %+v err=%v", events, err)
	}
}

func TestDeleteTrajectorySub(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	session := common.FormatHash(77)
	if err := db.AppendTrajectory(core.DefaultAgentID, session, core.TrajectorySlot{EventType: "turn_start", Timestamp: 1}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := db.DeleteTrajectory(core.DefaultAgentID, session); err != nil {
		t.Fatalf("delete: %v", err)
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("want empty trajectory, got %d events", len(events))
	}
}

func TestTrajectoryStats(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	session := common.FormatHash(55)

	// Empty session: zero-valued stats, no error.
	stats, err := db.TrajectoryStats(core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("stats on empty session: %v", err)
	}
	if stats.Steps != 0 || len(stats.ToolUsage) != 0 || stats.LastAppendAt != 0 {
		t.Fatalf("empty stats = %+v, want zeros", stats)
	}

	// Mixed event types with out-of-order timestamps.
	for _, ev := range []core.TrajectorySlot{
		{EventType: "turn_start", Timestamp: 400},
		{EventType: "tool_call", Timestamp: 100},
		{EventType: "tool_call", Timestamp: 200},
		{EventType: "tool_result", Timestamp: 300},
	} {
		if err := db.AppendTrajectory(core.DefaultAgentID, session, ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	stats, err = db.TrajectoryStats(core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Steps != 4 {
		t.Fatalf("Steps = %d, want 4", stats.Steps)
	}
	if stats.ToolUsage["tool_call"] != 2 || stats.ToolUsage["tool_result"] != 1 || stats.ToolUsage["turn_start"] != 1 {
		t.Fatalf("ToolUsage = %v, want tool_call:2 tool_result:1 turn_start:1", stats.ToolUsage)
	}
	if stats.LastAppendAt != 400 {
		t.Fatalf("LastAppendAt = %d, want 400 (max timestamp, not last append order)", stats.LastAppendAt)
	}
}
