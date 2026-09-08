// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for the L6 plan tree and trajectory log. A host
// drives this the way meowagent does: `Search` opens a turn and hands back that
// turn's topic id, which is also the key of the plan tree the turn works on. The
// host keeps its own dotted step paths and commits each step as it advances,
// and after a restart reads the tree back with a turn topic it already holds.
// PlanCommit returns nothing at all, so every assertion below reads the tree
// back through PlanState instead of trusting the call that changed it.

package test

import (
	"context"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

func mustPlanState(t *testing.T, db *testDB, topicID string) memhop.PlanTree {
	t.Helper()
	tree, err := db.PlanState(topicID)
	if err != nil {
		t.Fatalf("PlanState(%s): %v", topicID, err)
	}
	return *tree
}

// findPlanNode looks a host-assigned dotted path up in the forest.
func findPlanNode(t *testing.T, tree memhop.PlanTree, path string) memhop.PlanNodeView {
	t.Helper()
	var found *memhop.PlanNodeView
	var walk func([]memhop.PlanNodeView)
	walk = func(nodes []memhop.PlanNodeView) {
		for i := range nodes {
			if nodes[i].NodePath == path {
				found = &nodes[i]
				return
			}
			walk(nodes[i].Children)
		}
	}
	walk(tree.Roots)
	if found == nil {
		t.Fatalf("path %q missing from plan tree %+v", path, tree)
	}
	return *found
}

func mustReadTrajectory(t *testing.T, db *testDB, key string) []memhop.TrajectorySlot {
	t.Helper()
	events, err := db.ReadTrajectory(key)
	if err != nil {
		t.Fatalf("ReadTrajectory(%s): %v", key, err)
	}
	return events
}

func planEvent(ts int64, kind, payload string) memhop.TrajectorySlot {
	return memhop.TrajectorySlot{EventType: kind, Payload: payload, Timestamp: ts}
}

func mustAppend(t *testing.T, db *testDB, key, nodePath string, ev memhop.TrajectorySlot) {
	t.Helper()
	if err := db.AppendTrajectory(key, nodePath, ev); err != nil {
		t.Fatalf("AppendTrajectory(%s, %q): %v", key, nodePath, err)
	}
}

// One turn is one plan, and the turn's topic id is the only handle: the tree it
// opens reads back from that id, and two turns never share a tree.
func TestInterfacePlanTreeLivesOnItsTurn(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	first := openTurn(t, db, sceneID)
	second := openTurn(t, db, sceneID)
	if first == second {
		t.Fatal("two turns of one scene share a topic id")
	}
	ts := time.Now().UnixMilli()

	mustAppend(t, db, first, "1", planEvent(ts, "plan_step", "第一步"))
	if got := mustPlanState(t, db, first); got.TotalCount != 1 {
		t.Fatalf("first turn's tree = %+v, want one node", got)
	}
	if other := mustPlanState(t, db, second); other.TotalCount != 0 {
		t.Fatalf("the next turn inherited a tree: %+v", other)
	}
	// The turn's own read carries both faces of that key: the step event and the
	// node the event created.
	events := mustReadTrajectory(t, db, first)
	if len(events) != 1 || events[0].SessionID != first || events[0].NodePath != "1" {
		t.Fatalf("turn records = %+v, want the step event keyed to %s", events, first)
	}
}

// Committing a plan is how a parent's conclusion gets folded out of its
// children — and how a refused commit is required to leave nothing behind. The
// tree itself is grown by committing steps, keyed by the turn that opened it.
func TestInterfacePlanCommitRollup(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	topicID := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	commit := func(path, title, status, summary string, ev memhop.TrajectorySlot) error {
		return db.PlanCommit(topicID, path, ev, memhop.PlanStep{
			Title: title, Type: "step", Status: status, Summary: summary})
	}
	pending, done := string(memhop.PlanStatusPending), string(memhop.PlanStatusDone)
	if err := commit("1", "父", pending, "", planEvent(ts, "plan_step", "开工")); err != nil {
		t.Fatalf("commit parent: %v", err)
	}
	if err := commit("1.1", "子一", done, "改动收敛到 3 个文件", planEvent(ts+100, "plan_step", "第一步完成")); err != nil {
		t.Fatalf("commit child 1.1: %v", err)
	}
	if err := commit("1.2", "子二", done, "测试全绿", planEvent(ts+200, "plan_step", "第二步完成")); err != nil {
		t.Fatalf("commit child 1.2: %v", err)
	}

	// The rollup runs after every commit, so it must not pre-fill a parent the
	// host has not committed: a parent is Done only because the host said so.
	if got := findPlanNode(t, mustPlanState(t, db, topicID), "1"); got.Status != pending || got.Summary != "" {
		t.Fatalf("an uncommitted parent was folded: %+v", got)
	}

	// Committing the parent again with a blank title keeps the stored one.
	if err := commit("1", "", done, "", planEvent(ts+300, "plan_step", "全部完成")); err != nil {
		t.Fatalf("commit parent done: %v", err)
	}
	folded := findPlanNode(t, mustPlanState(t, db, topicID), "1")
	if folded.Summary != "改动收敛到 3 个文件; 测试全绿" {
		t.Fatalf("rolled-up summary = %q", folded.Summary)
	}
	if folded.Title != "父" {
		t.Fatalf("a blank title erased the stored one: %+v", folded)
	}
	if folded.FinishedAt == 0 {
		t.Fatal("a terminal commit must stamp FinishedAt once")
	}

	// Both refusals are checked before the node is touched, so the status, the
	// rolled-up summary and the event log all stay exactly as they were.
	if err := commit("1", "", "finished", "越权摘要", planEvent(ts+400, "plan_step", "x")); err == nil {
		t.Fatal("an unknown plan status should be refused")
	}
	if err := commit("1", "", done, "越权摘要", planEvent(ts+400, "", "x")); err == nil {
		t.Fatal("an event without an EventType should be refused")
	}
	after := findPlanNode(t, mustPlanState(t, db, topicID), "1")
	if after.Summary != folded.Summary || after.FinishedAt != folded.FinishedAt {
		t.Fatalf("a refused commit moved the node: %+v", after)
	}
	events := mustReadTrajectory(t, db, topicID)
	if len(events) != 4 {
		t.Fatalf("refused commits wrote events: %+v", events)
	}
	// The read says which step each event belongs to — the host cannot derive
	// that hash, so the stamp is the only attribution available on the surface.
	for _, e := range events {
		if e.SessionID != topicID || e.NodePath == "" {
			t.Fatalf("plan-bound event lost its attribution: %+v", e)
		}
	}
}

func TestInterfaceTrajectoryKeysAndCrystallize(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	turnID := openTurn(t, db, sceneID)
	planTurn := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	// A bare turn event takes any EventType the host names and is keyed to the
	// turn it logs, so the log cannot disagree with the turn.
	mustAppend(t, db, turnID, "", planEvent(ts, "host_note", "本轮没有工具调用"))
	turnEvents := mustReadTrajectory(t, db, turnID)
	if len(turnEvents) != 1 {
		t.Fatalf("turn events = %+v, want the one appended", turnEvents)
	}
	if e := turnEvents[0]; e.SessionID != turnID || e.NodePath != "" || e.PlanNodeRef != "" {
		t.Fatalf("bare turn event = %+v, want keyed to %s with no plan fields", e, turnID)
	}

	mustAppend(t, db, planTurn, "1", planEvent(ts+1, "plan_step", "开始"))
	mustAppend(t, db, planTurn, "1", planEvent(ts+2, "tool_call", `{"tool":"bash","cmd":"go test"}`))
	planEvents := mustReadTrajectory(t, db, planTurn)
	if len(planEvents) != 2 || planEvents[0].Seq != 1 || planEvents[1].Seq != 2 {
		t.Fatalf("plan events = %+v, want one Seq space per plan starting at 1", planEvents)
	}

	// Over budget is refused rather than shortened: a truncated event reads
	// exactly like a complete one, and nothing is written either way.
	if err := db.AppendTrajectory(planTurn, "1", planEvent(ts+3, "tool_result", strings.Repeat("x", 4097))); err == nil {
		t.Fatal("a payload over the 4 KiB event budget should be refused")
	}
	if again := mustReadTrajectory(t, db, planTurn); len(again) != 2 {
		t.Fatalf("the refused event landed anyway: %+v", again)
	}

	// Both keys of the domain, so a host can pick one to work off afterwards.
	sums, err := db.ListTrajectorySessions()
	if err != nil {
		t.Fatalf("ListTrajectorySessions: %v", err)
	}
	steps := map[string]int{}
	for _, s := range sums {
		steps[s.SessionID] = s.Steps
	}
	if steps[turnID] != 1 || steps[planTurn] != 2 {
		t.Fatalf("trajectory sessions = %+v, want %s:1 step and %s:2 steps", sums, turnID, planTurn)
	}

	// Crystallizing a turn works off everything that turn logged — its plain
	// events and the steps it committed alike. The engine returns candidates
	// only, so nothing lands anywhere. The mock replies with a fixed
	// three-action roster without reading the (nil) existing catalog, so the
	// reuse/merge candidates here also pin the passthrough contract.
	res, err := db.Crystallize(context.Background(), planTurn, nil)
	if err != nil {
		t.Fatalf("Crystallize(plan turn): %v", err)
	}
	if len(res.Capabilities) != 3 {
		t.Fatalf("crystallize result = %+v, want the mock's three candidates", res)
	}

	// A turn the host never logged has nothing to crystallize — reported, not
	// answered with an empty result.
	if _, err := db.Crystallize(context.Background(), openTurn(t, db, sceneID), nil); err == nil {
		t.Fatal("crystallizing a key with no events should fail")
	}
}
