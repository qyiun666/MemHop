// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for the L6 plan tree and trajectory log. A host
// drives this the way meowagent does: `Search` opens a turn and hands back that
// turn's topic id, which is also the key of the plan tree the turn works on. The
// host keeps its own dotted step paths, writes a whole-tree snapshot every turn,
// commits the steps that reached a terminal state, and after a restart reads the
// tree back with a turn topic it already holds. SyncPlanTree/PlanCommit return
// nothing at all, so every assertion below reads the tree back through PlanState
// instead of trusting the call that changed it.

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

func TestInterfaceSyncPlanTree(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	topicID := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	root := memhop.PlanNode{NodePath: "1", Title: "重构引擎", Type: "plan", Children: []memhop.PlanNode{
		{NodePath: "1.1", Title: "拆包", Type: "step", Status: string(memhop.PlanStatusDone), Summary: "小方法已下沉"},
		{NodePath: "1.2", Title: "补测试", Type: "step"},
	}}
	if err := db.SyncPlanTree(topicID, &root); err != nil {
		t.Fatalf("SyncPlanTree: %v", err)
	}
	tree := mustPlanState(t, db, topicID)
	if tree.TotalCount != 3 || tree.DoneCount != 1 {
		t.Fatalf("tree = %d nodes %d done, want 3/1: %+v", tree.TotalCount, tree.DoneCount, tree)
	}
	if got := findPlanNode(t, tree, "1"); got.Title != "重构引擎" || got.Type != "plan" || got.ChildCount != 2 ||
		got.Status != string(memhop.PlanStatusPending) {
		t.Fatalf("root = %+v", got)
	}
	if got := findPlanNode(t, tree, "1.1"); got.Summary != "小方法已下沉" || got.FinishedAt == 0 {
		t.Fatalf("done child = %+v, want its summary and a stamped FinishedAt", got)
	}

	// The call a host makes on every turn: a partial snapshot. A blank field
	// inherits the stored value, otherwise re-syncing rewinds a step the host
	// already finished and erases the conclusion folded into it.
	err := db.SyncPlanTree(topicID, &memhop.PlanNode{NodePath: "1", Children: []memhop.PlanNode{
		{NodePath: "1.1"}, {NodePath: "1.2"},
	}})
	if err != nil {
		t.Fatalf("re-sync with blank fields: %v", err)
	}
	kept := mustPlanState(t, db, topicID)
	if got := findPlanNode(t, kept, "1.1"); got.Status != string(memhop.PlanStatusDone) || got.Summary != "小方法已下沉" {
		t.Fatalf("blank re-sync rewound a done step: %+v", got)
	}
	if got := findPlanNode(t, kept, "1"); got.Title != "重构引擎" {
		t.Fatalf("blank re-sync erased the root title: %+v", got)
	}

	// A snapshot whose paths do not nest is refused before anything is written.
	if err := db.SyncPlanTree(topicID, &memhop.PlanNode{NodePath: "1", Children: []memhop.PlanNode{
		{NodePath: "9.9"},
	}}); err == nil {
		t.Fatal("a child path outside its parent should be refused")
	}
	if got := mustPlanState(t, db, topicID); got.TotalCount != 3 {
		t.Fatalf("the refused snapshot changed the tree: %+v", got)
	}

	// Dropping a step deletes it along with the events bound to it.
	mustAppend(t, db, topicID, "1.2", planEvent(ts, "tool_call", `{"tool":"grep","pattern":"SyncPlanTree"}`))
	if got := findPlanNode(t, mustPlanState(t, db, topicID), "1.2"); got.TrajCount != 1 {
		t.Fatalf("step 1.2 TrajCount = %d, want the one bound event counted", got.TrajCount)
	}
	if err := db.SyncPlanTree(topicID, &memhop.PlanNode{NodePath: "1", Children: []memhop.PlanNode{
		{NodePath: "1.1"},
	}}); err != nil {
		t.Fatalf("SyncPlanTree dropping 1.2: %v", err)
	}
	dropped := mustPlanState(t, db, topicID)
	if dropped.TotalCount != 2 {
		t.Fatalf("tree still has %d nodes after dropping a step: %+v", dropped.TotalCount, dropped)
	}
	if events := mustReadTrajectory(t, db, topicID); len(events) != 0 {
		t.Fatalf("the deleted step left its bound events behind: %+v", events)
	}
	// The key itself is now empty. It must stop being advertised, and an
	// attempt to crystallize it must be reported as "nothing there" rather
	// than as a read failure over a record that no longer exists.
	if sums, err := db.ListTrajectorySessions(); err != nil {
		t.Fatalf("ListTrajectorySessions: %v", err)
	} else {
		for _, s := range sums {
			if s.SessionID == topicID {
				t.Fatalf("the emptied plan key is still listed: %+v", s)
			}
		}
	}
	if _, err := db.Crystallize(context.Background(), topicID, nil); err == nil {
		t.Fatal("crystallizing an emptied plan key should fail")
	}
}

// Committing a plan is how a parent's conclusion gets folded out of its
// children — and how a refused commit is required to leave nothing behind.
func TestInterfacePlanCommitRollup(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	topicID := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()
	if err := db.SyncPlanTree(topicID, &memhop.PlanNode{NodePath: "1", Title: "父", Children: []memhop.PlanNode{
		{NodePath: "1.1", Title: "子一"}, {NodePath: "1.2", Title: "子二"},
	}}); err != nil {
		t.Fatalf("SyncPlanTree: %v", err)
	}

	commit := func(path, status, summary string, ev memhop.TrajectorySlot) error {
		return db.PlanCommit(topicID, path, ev, status, summary)
	}
	if err := commit("1.1", string(memhop.PlanStatusDone), "改动收敛到 3 个文件", planEvent(ts, "plan_step", "第一步完成")); err != nil {
		t.Fatalf("commit child 1.1: %v", err)
	}
	if err := commit("1.2", string(memhop.PlanStatusDone), "测试全绿", planEvent(ts+100, "plan_step", "第二步完成")); err != nil {
		t.Fatalf("commit child 1.2: %v", err)
	}

	// The rollup runs after every commit, so it must not pre-fill a parent the
	// host has not committed: a parent is Done only because the host said so.
	if got := findPlanNode(t, mustPlanState(t, db, topicID), "1"); got.Status != string(memhop.PlanStatusPending) || got.Summary != "" {
		t.Fatalf("an uncommitted parent was folded: %+v", got)
	}

	if err := commit("1", string(memhop.PlanStatusDone), "", planEvent(ts+200, "plan_step", "全部完成")); err != nil {
		t.Fatalf("commit parent: %v", err)
	}
	done := findPlanNode(t, mustPlanState(t, db, topicID), "1")
	if done.Summary != "改动收敛到 3 个文件; 测试全绿" {
		t.Fatalf("rolled-up summary = %q", done.Summary)
	}
	if done.FinishedAt == 0 {
		t.Fatal("a terminal commit must stamp FinishedAt once")
	}

	// Both refusals are checked before the node is touched, so the status, the
	// rolled-up summary and the event log all stay exactly as they were.
	if err := commit("1", "finished", "越权摘要", planEvent(ts+300, "plan_step", "x")); err == nil {
		t.Fatal("an unknown plan status should be refused")
	}
	if err := commit("1", string(memhop.PlanStatusDone), "越权摘要", planEvent(ts+300, "", "x")); err == nil {
		t.Fatal("an event without an EventType should be refused")
	}
	after := findPlanNode(t, mustPlanState(t, db, topicID), "1")
	if after.Summary != done.Summary || after.FinishedAt != done.FinishedAt {
		t.Fatalf("a refused commit moved the node: %+v", after)
	}
	events := mustReadTrajectory(t, db, topicID)
	if len(events) != 3 {
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

func TestInterfacePlanWipe(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	topicID := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()
	if err := db.SyncPlanTree(topicID, &memhop.PlanNode{NodePath: "1", Title: "旧任务", Children: []memhop.PlanNode{
		{NodePath: "1.1", Title: "旧步骤"},
	}}); err != nil {
		t.Fatalf("SyncPlanTree: %v", err)
	}
	mustAppend(t, db, topicID, "1.1", planEvent(ts, "tool_call", `{"tool":"file_read"}`))

	// An unrelated next task must not land on the old tree by path, so the host
	// wipes the tree with a nil snapshot and keeps the key it already holds.
	if err := db.SyncPlanTree(topicID, nil); err != nil {
		t.Fatalf("nil wipe: %v", err)
	}
	if tree := mustPlanState(t, db, topicID); len(tree.Roots) != 0 || tree.TotalCount != 0 {
		t.Fatalf("wiped plan still has nodes: %+v", tree)
	}
	if events := mustReadTrajectory(t, db, topicID); len(events) != 0 {
		t.Fatalf("wiped plan still has events: %+v", events)
	}

	mustAppend(t, db, topicID, "1", planEvent(ts+1, "plan_step", "新任务的第一步"))
	restarted := mustReadTrajectory(t, db, topicID)
	if len(restarted) != 1 || restarted[0].Seq != 1 {
		t.Fatalf("after the wipe the Seq space did not restart: %+v", restarted)
	}

	// Seeding the next task's tree is a plain single-node sync.
	if err := db.SyncPlanTree(topicID, &memhop.PlanNode{NodePath: "1", Title: "另一个任务"}); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	seeded := mustPlanState(t, db, topicID)
	if seeded.TotalCount != 1 {
		t.Fatalf("seeded plan = %+v, want one root", seeded)
	}
	root := seeded.Roots[0]
	if root.NodePath != "1" || root.Title != "另一个任务" || root.Status != string(memhop.PlanStatusPending) {
		t.Fatalf("seeded root = %+v", root)
	}
	if events := mustReadTrajectory(t, db, topicID); len(events) != 1 || events[0].Seq != 1 {
		t.Fatalf("seeding must not disturb the restarted log: %+v", events)
	}
}
