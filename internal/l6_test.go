// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestAppendTrajectorySeqAutoIncrement(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	session := common.FormatHash(99)
	for i := 1; i <= 3; i++ {
		if err := db.AppendTrajectory(core.DefaultAgentID, session, core.TrajectorySlot{EventType: "llm_request", Timestamp: int64(i)}); err != nil {
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

func TestListAndDreamPruneTrajectorySessions(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	a, b := common.FormatHash(11), common.FormatHash(22)
	fresh := time.Now().Add(-time.Hour).UnixMilli()
	appendOne := func(id string, ts int64) {
		if err := db.AppendTrajectory(core.DefaultAgentID, id, core.TrajectorySlot{EventType: "llm_request", Timestamp: ts}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	appendOne(a, 100)
	appendOne(a, fresh)
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
	if sum := byID[a]; sum.Steps != 2 || sum.LastAppendAt != fresh {
		t.Fatalf("session a summary mismatch: %+v", sum)
	}
	if sum := byID[b]; sum.Steps != 1 || sum.LastAppendAt != 500 {
		t.Fatalf("session b summary mismatch: %+v", sum)
	}

	// Dream drops events older than the 7-day retention window even when
	// there is nothing to consolidate (no active scenes → early return).
	if _, err := db.RunDream(context.Background(), core.DefaultAgentID, 0); err != nil {
		t.Fatalf("dream: %v", err)
	}
	list, err = db.ListTrajectorySessions(core.DefaultAgentID)
	if err != nil || len(list) != 1 || list[0].SessionID != a || list[0].Steps != 1 {
		t.Fatalf("only session a's fresh event survives: %+v err=%v", list, err)
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, b)
	if err != nil || len(events) != 0 {
		t.Fatalf("pruned session must read empty: %+v err=%v", events, err)
	}
}

func TestTrajectorySeqContinuesAfterContextRebuild(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	session := common.FormatHash(77)
	for i := 1; i <= 2; i++ {
		if err := db.AppendTrajectory(core.DefaultAgentID, session, core.TrajectorySlot{EventType: "llm_request", Timestamp: int64(i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// Simulate the idle sweep dropping the agent context: the next access
	// must rebuild the trajectory index from records and continue Seq.
	delete(db.agents, core.DefaultAgentID)
	if err := db.AppendTrajectory(core.DefaultAgentID, session, core.TrajectorySlot{EventType: "tool_call", Timestamp: 3}); err != nil {
		t.Fatalf("append after rebuild: %v", err)
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 3 || events[2].Seq != 3 {
		t.Fatalf("seq must continue after context rebuild: %+v", events)
	}
}

func TestTrimTrajectoryByBudgetKeepsNewest(t *testing.T) {
	events := []core.TrajectorySlot{
		{Payload: strings.Repeat("a", 60)},
		{Payload: strings.Repeat("b", 60)},
		{Payload: strings.Repeat("c", 60)},
	}
	if got := trimTrajectoryByBudget(events, 100); len(got) != 1 || got[0].Payload[0] != 'c' {
		t.Fatalf("trim = %+v, want only the newest event", got)
	}
	if got := trimTrajectoryByBudget(events, 1000); len(got) != 3 {
		t.Fatalf("under budget must keep all: %+v", got)
	}
	if got := trimTrajectoryByBudget(events, 1); len(got) != 1 || got[0].Payload[0] != 'c' {
		t.Fatalf("tiny budget must still keep the newest: %+v", got)
	}
}

func TestPlanCommitUpdatesNode(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	if err := db.PlanCommit(core.DefaultAgentID, pid, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1000}, PlanDone, "made it"); err != nil {
		t.Fatal(err)
	}
	id := core.HashPlanNode(9, "1")
	node, err := core.ReadTrajectorySlot(db.engine, core.DefaultAgentID, id)
	if err != nil {
		t.Fatal(err)
	}
	if node.NodeType != core.NodeTypePlan {
		t.Fatalf("want NodeTypePlan, got %d", node.NodeType)
	}
	if node.Status != core.StatusDone {
		t.Fatalf("want done, got %d", node.Status)
	}
	if node.Summary != "made it" {
		t.Fatalf("want made it, got %s", node.Summary)
	}
}

func TestPlanAppendCreatesNodeAndEvent(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	if err := db.PlanAppend(core.DefaultAgentID, pid, "1.2.1", core.TrajectorySlot{EventType: "llm_request", Timestamp: 1000}); err != nil {
		t.Fatal(err)
	}
	// 节点应已创建为 pending
	nodeID := core.HashPlanNode(9, "1.2.1")
	node, err := core.ReadTrajectorySlot(db.engine, core.DefaultAgentID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if node.NodeType != core.NodeTypePlan || node.Status != core.StatusPending {
		t.Fatalf("node not created as pending plan: %+v", node)
	}
	// 事件应挂到该节点
	events := repo.CollectNodeEvents(db.engine, core.DefaultAgentID, nodeID)
	if len(events) != 1 || events[0].EventType != "llm_request" {
		t.Fatalf("want 1 llm_request event on node, got %+v", events)
	}
}

// ensurePlanNode must build the parent chain with correct ParentID.
func TestPlanAppendBuildsParentChain(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.PlanAppend(core.DefaultAgentID, common.FormatHash(9), "1.2.1", core.TrajectorySlot{EventType: "llm_request", Timestamp: 1000}); err != nil {
		t.Fatal(err)
	}
	rootID := core.HashPlanNode(9, "1")
	midID := core.HashPlanNode(9, "1.2")
	leafID := core.HashPlanNode(9, "1.2.1")
	root, _ := core.ReadTrajectorySlot(db.engine, core.DefaultAgentID, rootID)
	mid, _ := core.ReadTrajectorySlot(db.engine, core.DefaultAgentID, midID)
	leaf, _ := core.ReadTrajectorySlot(db.engine, core.DefaultAgentID, leafID)
	if root.ParentID != 0 {
		t.Fatalf("root parent should be 0, got %d", root.ParentID)
	}
	if mid.ParentID != rootID {
		t.Fatalf("mid parent should be rootID %d, got %d", rootID, mid.ParentID)
	}
	if leaf.ParentID != midID {
		t.Fatalf("leaf parent should be midID %d, got %d", midID, leaf.ParentID)
	}
	if leaf.NodeType != core.NodeTypePlan || leaf.Status != core.StatusPending {
		t.Fatalf("leaf not pending plan node: %+v", leaf)
	}
}

// Events must start at Seq 1 and never collide with a plan-node Seq,
// whether nodes are committed shallow or deep first.
func TestPlanEventSeqStartsAtOneNoCollision(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	// 先提交一个深节点，事件 Seq 应为 1（不被节点 Seq=3 污染）
	if err := db.PlanCommit(core.DefaultAgentID, common.FormatHash(9), "1.2.1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1000}, PlanInProgress, ""); err != nil {
		t.Fatal(err)
	}
	leafID := core.HashPlanNode(9, "1.2.1")
	evs := repo.CollectNodeEvents(db.engine, core.DefaultAgentID, leafID)
	if len(evs) != 1 || evs[0].Seq != 1 {
		t.Fatalf("first event seq should be 1, got %+v", evs)
	}
	// 再回提提交浅层根节点，事件 Seq 应继续从 2 起，不发生覆盖
	if err := db.PlanCommit(core.DefaultAgentID, common.FormatHash(9), "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 2000}, PlanDone, "root done"); err != nil {
		t.Fatal(err)
	}
	rootID := core.HashPlanNode(9, "1")
	rootEvs := repo.CollectNodeEvents(db.engine, core.DefaultAgentID, rootID)
	if len(rootEvs) != 1 || rootEvs[0].Seq != 2 {
		t.Fatalf("root event seq should be 2, got %+v", rootEvs)
	}
	// 两个事件 ID 必须不同（未被覆盖）
	if evs[0].IDHash == rootEvs[0].IDHash {
		t.Fatalf("event ids must not collide: %d", evs[0].IDHash)
	}
}
