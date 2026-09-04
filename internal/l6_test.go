// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"cmp"
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/dream"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/trajectory"
)

// planNodeEvents reads one node's bound events (Seq ascending) through the
// single-scan aggregate; the repo-level CollectNodeEvents was removed.
func planNodeEvents(t *testing.T, db *DB, planID, nodeID uint64) []core.TrajectorySlot {
	t.Helper()
	for _, agg := range repo.CollectPlanAggregates(db.engine, core.DefaultAgentID) {
		if agg.PlanID != planID {
			continue
		}
		var out []core.TrajectorySlot
		for _, ev := range agg.Events {
			if ev.PlanNodeRef == nodeID {
				out = append(out, ev)
			}
		}
		slices.SortFunc(out, func(a, b core.TrajectorySlot) int { return cmp.Compare(a.Seq, b.Seq) })
		return out
	}
	return nil
}

func TestAppendTrajectorySeqAutoIncrement(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	session := common.FormatHash(99)
	for i := 1; i <= 3; i++ {
		if err := db.AppendTrajectory(core.DefaultAgentID, session, "", core.TrajectorySlot{EventType: "llm_request", Timestamp: int64(i)}); err != nil {
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
	if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(1), "", core.TrajectorySlot{Timestamp: 1}); err == nil {
		t.Fatal("empty event type should fail")
	}
	if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(1), "", core.TrajectorySlot{EventType: "tool_call"}); err == nil {
		t.Fatal("zero timestamp should fail")
	}
}

func TestAppendTrajectoryPayloadRefused(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	key := common.FormatHash(3)
	long := strings.Repeat("x", trajectory.MaxEventPayload+100)
	if err := db.AppendTrajectory(core.DefaultAgentID, key, "", core.TrajectorySlot{EventType: "tool_call", Payload: long, Timestamp: 1}); err == nil {
		t.Fatal("an over-budget payload must be refused, not truncated")
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, key)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("a refused append must store nothing, got %d events", len(events))
	}
	// exactly at the budget still writes
	if err := db.AppendTrajectory(core.DefaultAgentID, key, "", core.TrajectorySlot{
		EventType: "tool_call", Payload: strings.Repeat("x", trajectory.MaxEventPayload), Timestamp: 1}); err != nil {
		t.Fatalf("payload at the budget limit should append: %v", err)
	}
}

func TestListAndDreamPruneTrajectorySessions(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	a, b := common.FormatHash(11), common.FormatHash(22)
	fresh := time.Now().Add(-time.Hour).UnixMilli()
	appendOne := func(id string, ts int64) {
		if err := db.AppendTrajectory(core.DefaultAgentID, id, "", core.TrajectorySlot{EventType: "llm_request", Timestamp: ts}); err != nil {
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
		if err := db.AppendTrajectory(core.DefaultAgentID, session, "", core.TrajectorySlot{EventType: "llm_request", Timestamp: int64(i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// Simulate the idle sweep dropping the agent context: the next access
	// must rebuild the trajectory index from records and continue Seq.
	delete(db.agents, core.DefaultAgentID)
	if err := db.AppendTrajectory(core.DefaultAgentID, session, "", core.TrajectorySlot{EventType: "tool_call", Timestamp: 3}); err != nil {
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
	if err := db.AppendTrajectory(core.DefaultAgentID, pid, "1.2.1", core.TrajectorySlot{EventType: "llm_request", Timestamp: 1000}); err != nil {
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
	events := planNodeEvents(t, db, 9, nodeID)
	if len(events) != 1 || events[0].EventType != "llm_request" {
		t.Fatalf("want 1 llm_request event on node, got %+v", events)
	}
}

// ensurePlanNode must build the parent chain with correct ParentID.
func TestPlanAppendBuildsParentChain(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(9), "1.2.1", core.TrajectorySlot{EventType: "llm_request", Timestamp: 1000}); err != nil {
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
	evs := planNodeEvents(t, db, 9, leafID)
	if len(evs) != 1 || evs[0].Seq != 1 {
		t.Fatalf("first event seq should be 1, got %+v", evs)
	}
	// 再回提提交浅层根节点，事件 Seq 应继续从 2 起，不发生覆盖
	if err := db.PlanCommit(core.DefaultAgentID, common.FormatHash(9), "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 2000}, PlanDone, "root done"); err != nil {
		t.Fatal(err)
	}
	rootID := core.HashPlanNode(9, "1")
	rootEvs := planNodeEvents(t, db, 9, rootID)
	if len(rootEvs) != 1 || rootEvs[0].Seq != 2 {
		t.Fatalf("root event seq should be 2, got %+v", rootEvs)
	}
	// 两个事件 ID 必须不同（未被覆盖）
	if evs[0].IDHash == rootEvs[0].IDHash {
		t.Fatalf("event ids must not collide: %d", evs[0].IDHash)
	}
}

// Model A: a parent becomes Done only when the host explicitly commits it.
// Rollup merges Done children summaries into the host-completed parent.
func TestPlanStateParentExplicitCompletion(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	planID := common.FormatHash(9)
	// Children done first.
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1.1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1001}, PlanDone, "step A"); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1.2", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1002}, PlanDone, "step B"); err != nil {
		t.Fatal(err)
	}
	// Parent is NOT auto-folded while not explicitly committed done.
	tree, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Status == PlanDone {
		t.Fatalf("parent must NOT auto-fold: got %s", tree.Roots[0].Status)
	}
	if tree.TotalCount != 3 {
		t.Fatalf("total=%d, want 3 (root + 2 children)", tree.TotalCount)
	}
	// Host explicitly completes the parent → it becomes Done and rolls up.
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1003}, PlanDone, ""); err != nil {
		t.Fatal(err)
	}
	tree, err = db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Status != PlanDone {
		t.Fatalf("root should be done after explicit commit, got %s", tree.Roots[0].Status)
	}
	if tree.Roots[0].Summary != "step A; step B" {
		t.Fatalf("root summary should roll up children, got %q", tree.Roots[0].Summary)
	}
	if tree.TotalCount != 3 || tree.DoneCount != 3 {
		t.Fatalf("counts: total=%d done=%d", tree.TotalCount, tree.DoneCount)
	}
}

// Model A: a host-provided parent Summary must NOT be overwritten by the
// Done-children rollup.
func TestPlanStateRollupDoesNotOverwriteHostSummary(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	planID := common.FormatHash(9)
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1.1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1001}, PlanDone, "step A"); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1.2", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1002}, PlanDone, "step B"); err != nil {
		t.Fatal(err)
	}
	// Host commits the parent done WITH its own summary → must be preserved.
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1003}, PlanDone, "custom parent summary"); err != nil {
		t.Fatal(err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Summary != "custom parent summary" {
		t.Fatalf("host parent summary must not be overwritten, got %q", tree.Roots[0].Summary)
	}
}

// Model A: a parent whose children are only partially Done must NOT be
// auto-folded; it stays in_progress until the host explicitly commits it Done.
func TestPlanStateParentNotAutoFoldedPartial(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	planID := common.FormatHash(9)
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1000}, PlanInProgress, ""); err != nil {
		t.Fatal(err)
	}
	// Only one of two children is done; the other stays pending.
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1.1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1001}, PlanDone, "step A"); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1.2", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1002}, PlanPending, ""); err != nil {
		t.Fatal(err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Status == PlanDone {
		t.Fatalf("parent must NOT auto-fold while children only partially done, got %s", tree.Roots[0].Status)
	}
	if tree.TotalCount != 3 || tree.DoneCount != 1 {
		t.Fatalf("counts: total=%d done=%d, want total=3 done=1", tree.TotalCount, tree.DoneCount)
	}
}

// TestDreamPrunesExpiredPlanNodes verifies the retention sweep semantics: an
// expired plan whose nodes are all Done is swept together with its bound
// events (cascade — no orphan PlanNodeRef), a plan that still holds a non-Done
// node is exempt only while it is also active inside the window, and a
// non-Done plan the host went silent on past the window is abandoned and swept
// like any other record so L6 stays bounded.
func TestDreamPrunesExpiredPlanNodes(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	doneID, activeID, staleID := common.FormatHash(9), common.FormatHash(8), common.FormatHash(7)
	old := time.Now().Add(-dream.TrajectoryRetention - time.Hour).UnixMilli()
	age := func(id uint64) {
		node, err := core.ReadTrajectorySlot(db.engine, core.DefaultAgentID, id)
		if err != nil {
			t.Fatalf("read node for aging: %v", err)
		}
		node.Timestamp = old
		if _, err := repo.WritePlanNode(db.engine, core.DefaultAgentID, node); err != nil {
			t.Fatal(err)
		}
	}
	// Done plan: committed long ago, plus a FRESH event bound to the node
	// (the cascade must remove it even though it is inside the window).
	if err := db.PlanCommit(core.DefaultAgentID, doneID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: old}, PlanDone, "fin"); err != nil {
		t.Fatal(err)
	}
	doneNode := core.HashPlanNode(9, "1")
	if err := db.AppendTrajectory(core.DefaultAgentID, doneID, "1", core.TrajectorySlot{EventType: "llm_request", Timestamp: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	age(doneNode)
	// In-flight plan: expired node but a fresh bound event → still active, so
	// the tree survives mid-task.
	if err := db.PlanCommit(core.DefaultAgentID, activeID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: old}, PlanInProgress, ""); err != nil {
		t.Fatal(err)
	}
	activeNode := core.HashPlanNode(8, "1")
	if err := db.AppendTrajectory(core.DefaultAgentID, activeID, "1", core.TrajectorySlot{EventType: "llm_request", Timestamp: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	age(activeNode)
	// Abandoned plan: non-Done and nothing touched it inside the window.
	if err := db.PlanCommit(core.DefaultAgentID, staleID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: old}, PlanInProgress, ""); err != nil {
		t.Fatal(err)
	}
	staleNode := core.HashPlanNode(7, "1")
	age(staleNode)

	if _, err := db.RunDream(context.Background(), core.DefaultAgentID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ReadTrajectorySlot(db.engine, core.DefaultAgentID, doneNode); err == nil {
		t.Fatal("expired all-done plan node should be pruned")
	}
	if _, err := core.ReadTrajectorySlot(db.engine, core.DefaultAgentID, staleNode); err == nil {
		t.Fatal("non-Done plan silent past the window is abandoned and must be pruned")
	}
	for _, ev := range core.CollectAllTrajectories(db.engine, core.DefaultAgentID) {
		if ev.PlanNodeRef == doneNode {
			t.Fatalf("bound event must cascade with its pruned node: %+v", ev)
		}
	}
	if _, err := core.ReadTrajectorySlot(db.engine, core.DefaultAgentID, activeNode); err != nil {
		t.Fatalf("active plan node must be exempt from the sweep: %v", err)
	}
}

// TestPlanAppendCannotInjectNodeType verifies an appended plan event is forced
// to bare-event semantics: no node-only field survives the write, so a host
// cannot inject a plan-node record that would pollute the tree view.
func TestPlanAppendCannotInjectNodeType(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	planID := common.FormatHash(9)
	// Host tries to inject plan-node fields on an event; all of them are forced
	// back to event semantics.
	if err := db.AppendTrajectory(core.DefaultAgentID, planID, "1", core.TrajectorySlot{
		EventType: "llm_request", Timestamp: 1000, NodeType: core.NodeTypePlan,
		Status: core.StatusDone, Summary: "injected", NodePath: "9.9", PlanType: "plan",
	}); err != nil {
		t.Fatal(err)
	}
	nodes := repo.CollectPlanNodes(db.engine, core.DefaultAgentID, 9)
	if len(nodes) != 1 {
		t.Fatalf("want exactly 1 plan node, got %d", len(nodes))
	}
	if nodes[0].NodeType != core.NodeTypePlan {
		t.Fatalf("plan node should be NodeTypePlan, got %d", nodes[0].NodeType)
	}
	// The appended event must be an Event, not a node.
	events := 0
	for _, e := range core.CollectAllTrajectories(db.engine, core.DefaultAgentID) {
		if e.NodeType != core.NodeTypeEvent {
			continue
		}
		events++
		// The library stamps the step the event actually bound to; the host's
		// forged "9.9" must not survive.
		if e.NodePath != "1" {
			t.Fatalf("appended event must carry the bound node's path, got %q", e.NodePath)
		}
		if e.Status != 0 || e.Summary != "" || e.PlanType != "" || e.ParentID != 0 {
			t.Fatalf("appended event kept node fields: %+v", e)
		}
	}
	if events != 1 {
		t.Fatalf("want exactly 1 event, got %d", events)
	}

	// The bare turn path forces the same shape.
	turnID := common.FormatHash(77)
	if err := db.AppendTrajectory(core.DefaultAgentID, turnID, "", core.TrajectorySlot{
		EventType: "tool_call", Timestamp: 1100, PlanType: "step", Summary: "injected",
	}); err != nil {
		t.Fatal(err)
	}
	for _, e := range core.CollectAllTrajectories(db.engine, core.DefaultAgentID) {
		if e.SessionID != 77 {
			continue
		}
		if e.PlanType != "" || e.Summary != "" || e.TopicID != 77 {
			t.Fatalf("bare turn event shape: %+v", e)
		}
	}
}

// Forest contract: two top-level steps yield two roots, and Done/Total
// covers both subtrees.
func TestPlanStateForestMultipleRoots(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	planID := common.FormatHash(9)
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1001}, PlanDone, "step one"); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, planID, "2", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1002}, PlanInProgress, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, planID, "2.1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1003}, PlanDone, "sub"); err != nil {
		t.Fatal(err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Roots) != 2 {
		t.Fatalf("want 2 roots, got %d", len(tree.Roots))
	}
	if tree.Roots[0].NodePath != "1" || tree.Roots[1].NodePath != "2" {
		t.Fatalf("roots must be path-ordered: %+v", tree.Roots)
	}
	if len(tree.Roots[1].Children) != 1 || tree.Roots[1].Children[0].NodePath != "2.1" {
		t.Fatalf("second root lost its subtree: %+v", tree.Roots[1])
	}
	if tree.TotalCount != 3 || tree.DoneCount != 2 {
		t.Fatalf("forest stats total=%d done=%d, want 3/2", tree.TotalCount, tree.DoneCount)
	}
}

// PlanReplace clears every node and bound event of a plan, keeps the
// planID, seeds a titled root, and restarts the event Seq space.
func TestPlanReplaceClearsAndSeedsRoot(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	planID := common.FormatHash(9)
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1001}, PlanDone, "old step"); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, planID, "2", core.TrajectorySlot{EventType: "plan_step", Timestamp: 1002}, PlanPending, ""); err != nil {
		t.Fatal(err)
	}

	if err := db.PlanReplace(core.DefaultAgentID, planID, "rewritten plan"); err != nil {
		t.Fatal(err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Roots) != 1 || tree.Roots[0].Title != "rewritten plan" || tree.Roots[0].Status != PlanPending {
		t.Fatalf("replace must seed one titled pending root: %+v", tree.Roots)
	}
	if tree.TotalCount != 1 || tree.DoneCount != 0 {
		t.Fatalf("stats must reset: total=%d done=%d", tree.TotalCount, tree.DoneCount)
	}
	// Old nodes and bound events are gone; the new event Seq restarts at 1.
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 2001}, PlanInProgress, ""); err != nil {
		t.Fatal(err)
	}
	aggs := repo.CollectPlanAggregates(db.engine, core.DefaultAgentID)
	if len(aggs) != 1 || len(aggs[0].Events) != 1 {
		t.Fatalf("old events leaked into the replaced plan: %+v", aggs)
	}
	// Replacing without a title keeps the plan empty (no root seeded).
	if err := db.PlanReplace(core.DefaultAgentID, planID, ""); err != nil {
		t.Fatal(err)
	}
	tree, err = db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Roots) != 0 || tree.TotalCount != 0 {
		t.Fatalf("title-less replace must leave an empty plan: %+v", tree)
	}
}

// Plan events must use the documented vocabulary.
func TestPlanEventVocabularyRejectsUnknown(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	planID := common.FormatHash(9)
	if err := db.AppendTrajectory(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "made_up_event", Timestamp: 1000}); err == nil {
		t.Fatal("unknown plan event type must be rejected")
	}
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "nope", Timestamp: 1001}, PlanDone, ""); err == nil {
		t.Fatal("unknown plan commit event type must be rejected")
	}
	if err := db.AppendTrajectory(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "tool_call", Timestamp: 1002}); err != nil {
		t.Fatalf("documented event type must be accepted: %v", err)
	}
}

func TestSyncPlanTree_AddEditDelete(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	planID := common.FormatHash(9)
	first := &PlanNode{
		NodePath: "1", Title: "root", PlanType: "plan", Status: PlanPending,
		Children: []PlanNode{
			{NodePath: "1.1", Title: "step a", PlanType: "step", Status: PlanRunning, Summary: "s1"},
			{NodePath: "1.2", Title: "step b", PlanType: "step", Status: PlanDone},
		},
	}
	if err := db.SyncPlanTree(core.DefaultAgentID, planID, first); err != nil {
		t.Fatal(err)
	}
	// Bind a real event to the node that will be deleted, so the cascade is asserted.
	if err := db.AppendTrajectory(core.DefaultAgentID, planID, "1.2", core.TrajectorySlot{EventType: "llm_request", Timestamp: 1500}); err != nil {
		t.Fatal(err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Roots) != 1 || tree.Roots[0].Title != "root" || tree.Roots[0].Type != "plan" {
		t.Fatalf("first sync root: %+v", tree.Roots)
	}
	if len(tree.Roots[0].Children) != 2 {
		t.Fatalf("first sync children = %d want 2", len(tree.Roots[0].Children))
	}
	if got := tree.Roots[0].Children[0]; got.Title != "step a" || got.Status != PlanRunning || got.Type != "step" || got.Summary != "s1" {
		t.Fatalf("step a: %+v", got)
	}

	second := &PlanNode{
		NodePath: "1", Title: "root2", PlanType: "plan", Status: PlanDone,
		Children: []PlanNode{
			{NodePath: "1.1", Title: "step a v2", PlanType: "step", Status: PlanDone, Summary: "s1v2"},
			{NodePath: "1.3", Title: "step c", PlanType: "step", Status: PlanPending},
		},
	}
	if err := db.SyncPlanTree(core.DefaultAgentID, planID, second); err != nil {
		t.Fatal(err)
	}
	tree2, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if tree2.Roots[0].Title != "root2" || tree2.Roots[0].Status != PlanDone {
		t.Fatalf("second sync root: %+v", tree2.Roots[0])
	}
	if len(tree2.Roots[0].Children) != 2 {
		t.Fatalf("second sync children = %d want 2", len(tree2.Roots[0].Children))
	}
	if got := tree2.Roots[0].Children[0]; got.Title != "step a v2" || got.Status != PlanDone || got.FinishedAt == 0 {
		t.Fatalf("edited step a: %+v", got)
	}
	if got := tree2.Roots[0].Children[1]; got.NodePath != "1.3" || got.Status != PlanPending {
		t.Fatalf("added step c: %+v", got)
	}
	// The deleted node record and its bound event are gone — no orphan PlanNodeRef.
	gone := core.HashPlanNode(9, "1.2")
	if _, err := core.ReadTrajectorySlot(db.engine, core.DefaultAgentID, gone); err == nil {
		t.Fatal("deleted node record must be gone")
	}
	for _, ev := range core.CollectAllTrajectories(db.engine, core.DefaultAgentID) {
		if ev.PlanNodeRef == gone {
			t.Fatalf("orphan PlanNodeRef survived: %+v", ev)
		}
	}
	// SyncPlanTree must never synthesize a plan_step event.
	for _, agg := range repo.CollectPlanAggregates(db.engine, core.DefaultAgentID) {
		for _, e := range agg.Events {
			if e.EventType == "plan_step" {
				t.Fatalf("SyncPlanTree must not emit plan_step: %+v", e)
			}
		}
	}
}

// A partial snapshot must never rewind finished work: the fields the host
// leaves blank inherit what is stored, so re-syncing a tree without re-sending
// every field keeps a done step done and keeps a folded summary intact.
func TestSyncPlanTreeInheritsBlankFields(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	planID := common.FormatHash(11)
	full := &PlanNode{
		NodePath: "1", Title: "research", PlanType: "plan", Status: PlanDone,
		Summary: "folded: three findings",
		Children: []PlanNode{
			{NodePath: "1.1", Title: "read", PlanType: "step", Status: PlanDone, Summary: "read 5 papers"},
		},
	}
	if err := db.SyncPlanTree(core.DefaultAgentID, planID, full); err != nil {
		t.Fatal(err)
	}

	blank := &PlanNode{NodePath: "1", Children: []PlanNode{{NodePath: "1.1"}}}
	if err := db.SyncPlanTree(core.DefaultAgentID, planID, blank); err != nil {
		t.Fatal(err)
	}

	tree, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Roots[0]
	if root.Status != PlanDone || root.Title != "research" || root.Type != "plan" ||
		root.Summary != "folded: three findings" {
		t.Fatalf("blank snapshot rewrote stored fields: %+v", root)
	}
	child := root.Children[0]
	if child.Status != PlanDone || child.Summary != "read 5 papers" {
		t.Fatalf("blank snapshot rewound a done child: %+v", child)
	}
	if root.FinishedAt == 0 {
		t.Fatal("terminal status lost its FinishedAt")
	}
	// An explicit value still wins over the stored one.
	if err := db.SyncPlanTree(core.DefaultAgentID, planID, &PlanNode{
		NodePath: "1", Title: "renamed",
	}); err != nil {
		t.Fatal(err)
	}
	tree, err = db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Title != "renamed" || tree.Roots[0].Status != PlanDone {
		t.Fatalf("explicit update ignored or status rewound: %+v", tree.Roots[0])
	}
}

func TestSyncPlanTree_NoEventProduced(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	planID := common.FormatHash(9)
	root := &PlanNode{
		NodePath: "1", Title: "r", PlanType: "plan", Status: PlanPending,
		Children: []PlanNode{{NodePath: "1.1", Title: "a", PlanType: "step", Status: PlanRunning}},
	}
	if err := db.SyncPlanTree(core.DefaultAgentID, planID, root); err != nil {
		t.Fatal(err)
	}
	for _, agg := range repo.CollectPlanAggregates(db.engine, core.DefaultAgentID) {
		if agg.PlanID != 9 {
			continue
		}
		if len(agg.Events) != 0 {
			t.Fatalf("SyncPlanTree produced %d events: %+v", len(agg.Events), agg.Events)
		}
	}
	tree, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].TrajCount != 0 {
		t.Fatalf("TrajCount = %d want 0", tree.Roots[0].TrajCount)
	}
}

func TestPlanCache_ConsistentWithDisk(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	planID := common.FormatHash(9)
	root := &PlanNode{
		NodePath: "1", Title: "r", PlanType: "plan", Status: PlanPending,
		Children: []PlanNode{{NodePath: "1.1", Title: "a", PlanType: "step", Status: PlanDone, Summary: "s"}},
	}
	if err := db.SyncPlanTree(core.DefaultAgentID, planID, root); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendTrajectory(core.DefaultAgentID, planID, "1.1", core.TrajectorySlot{EventType: "tool_call", Timestamp: 1200}); err != nil {
		t.Fatal(err)
	}
	ac := db.agents[core.DefaultAgentID]
	if ac == nil {
		t.Fatal("agent context missing")
	}
	cached := ac.Plans.Aggregate(9)
	if cached == nil {
		t.Fatal("cached aggregate missing")
	}
	var disk *repo.PlanAggregate
	for _, agg := range repo.CollectPlanAggregates(db.engine, core.DefaultAgentID) {
		if agg.PlanID == 9 {
			disk = &agg
			break
		}
	}
	if disk == nil {
		t.Fatal("disk aggregate missing")
	}
	if !reflect.DeepEqual(cached.Nodes, disk.Nodes) {
		t.Fatalf("Nodes mismatch:\n cached=%+v\n disk=%+v", cached.Nodes, disk.Nodes)
	}
	if !reflect.DeepEqual(cached.EventCount, disk.EventCount) {
		t.Fatalf("EventCount mismatch: cached=%v disk=%v", cached.EventCount, disk.EventCount)
	}
}

func TestPlanCommit_FinishedAt(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	planID := common.FormatHash(9)
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 100}, PlanDone, "fin"); err != nil {
		t.Fatal(err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	first := tree.Roots[0].FinishedAt
	if first == 0 {
		t.Fatal("terminal commit must set FinishedAt")
	}
	// A non-terminal commit must not clear it.
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 200}, PlanInProgress, ""); err != nil {
		t.Fatal(err)
	}
	tree2, _ := db.PlanState(core.DefaultAgentID, planID)
	if tree2.Roots[0].FinishedAt != first {
		t.Fatalf("non-terminal commit cleared FinishedAt: %d -> %d", first, tree2.Roots[0].FinishedAt)
	}
	// A re-terminal commit preserves the original FinishedAt.
	if err := db.PlanCommit(core.DefaultAgentID, planID, "1", core.TrajectorySlot{EventType: "plan_step", Timestamp: 300}, PlanDone, "fin2"); err != nil {
		t.Fatal(err)
	}
	tree3, _ := db.PlanState(core.DefaultAgentID, planID)
	if tree3.Roots[0].FinishedAt != first {
		t.Fatalf("re-terminal commit changed FinishedAt: %d -> %d", first, tree3.Roots[0].FinishedAt)
	}
}

func TestPlanStatusRunning(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	planID := common.FormatHash(9)
	root := &PlanNode{NodePath: "1", Title: "t", PlanType: "step", Status: PlanRunning}
	if err := db.SyncPlanTree(core.DefaultAgentID, planID, root); err != nil {
		t.Fatal(err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Status != PlanRunning {
		t.Fatalf("status = %q want %q", tree.Roots[0].Status, PlanRunning)
	}
}

// One turn runs on one id: the topic Search opened is where the host's events
// land, what Update settles, and what Crystallize reads back — no host-minted
// turn key and no timestamp derivation anywhere in between.
func TestTurnRunsOnOneTopicID(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)

	res, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	turnID := common.FormatHash(res.NewTopicID)

	for _, ev := range []core.TrajectorySlot{
		{EventType: "llm_request", Timestamp: 1000},
		{EventType: "tool_call", Timestamp: 1500},
	} {
		if err := db.AppendTrajectory(core.DefaultAgentID, turnID, "", ev); err != nil {
			t.Fatalf("append %s: %v", ev.EventType, err)
		}
	}
	settled, err := db.Update(core.DefaultAgentID, turnOf(res.Scene.SceneID, res.NewTopicID))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if settled != res.NewTopicID {
		t.Fatalf("Update settled topic %d, want the opened %d", settled, res.NewTopicID)
	}
	if _, err := core.ReadTopicLenient(db.engine, core.DefaultAgentID, settled); err != nil {
		t.Fatalf("the turn topic is not readable: %v", err)
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, turnID)
	if err != nil {
		t.Fatalf("read trajectory: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want the turn's 2", len(events))
	}
	for _, ev := range events {
		if ev.TopicID != settled {
			t.Fatalf("event %s bound to topic %d, want %d", ev.EventType, ev.TopicID, settled)
		}
	}
}
