// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for the L5 plan tree and the turn's event track (L4
// content). A host drives this the way meowagent does, one loop iteration per
// turn: `Search` reads the scene and hands back the topic id of the turn about
// to run, `PlanSet` declares that turn's whole plan (the LLM re-plans every
// turn, so the tree is restated rather than stepped through), each step's work
// goes into L4 with `AppendArchive` bound to one declared step, and `Update`
// settles the turn's dialogue into its topic. After a restart the host reads the
// tree back with a turn topic it already holds: PlanSet returns nothing, so every
// assertion below reads the tree through PlanState instead of trusting the call
// that changed it.

package test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// mustDeclare states one turn's plan: the steps the host's LLM planned, at
// whatever depth its dotted paths nest.
func mustDeclare(t *testing.T, db *testDB, key string, steps ...memhop.PlanStep) {
	t.Helper()
	if err := db.PlanSet(key, steps); err != nil {
		t.Fatalf("PlanSet(%s, %v): %v", key, steps, err)
	}
}

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

// mustEvents reads one topic's event track: the same key with the kind condition,
// which is all a host has left for that read.
func mustEvents(t *testing.T, db *testDB, key string) []memhop.ArchiveSlot {
	t.Helper()
	kind := memhop.KindEvent
	events, err := db.SearchL4(memhop.L4Query{TopicID: &key, Kind: &kind})
	if err != nil {
		t.Fatalf("read events of %s: %v", key, err)
	}
	return events
}

func planEvent(ts int64, kind, payload string) memhop.ArchiveSlot {
	return memhop.ArchiveSlot{Kind: memhop.KindEvent, EventType: kind, Content: payload, CreatedAt: ts}
}

// mustAppend writes one event, binding it to a plan step when nodePath names one.
func mustAppend(t *testing.T, db *testDB, key, nodePath string, ev memhop.ArchiveSlot) {
	t.Helper()
	ev.NodePath = nodePath
	if err := db.AppendArchive(key, ev); err != nil {
		t.Fatalf("AppendArchive(%s, %q): %v", key, nodePath, err)
	}
}

// renderTree flattens a plan forest into one line, so a before/after comparison
// says what moved instead of leaking a Go map diff.
func renderTree(t *testing.T, db *testDB, key string) string {
	t.Helper()
	var b strings.Builder
	var walk func([]memhop.PlanNodeView)
	walk = func(nodes []memhop.PlanNodeView) {
		for _, n := range nodes {
			b.WriteString(n.NodePath + "=" + n.Status + "/" + n.Summary + "/" + n.Title + " ")
			walk(n.Children)
		}
	}
	walk(mustPlanState(t, db, key).Roots)
	return b.String()
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

	mustDeclare(t, db, first, memhop.PlanStep{NodePath: "1", Title: "第一步", Status: "in_progress"})
	mustAppend(t, db, first, "1", planEvent(ts, "plan_step", "第一步"))
	if got := mustPlanState(t, db, first); got.TotalCount != 1 {
		t.Fatalf("first turn's tree = %+v, want one node", got)
	}
	if other := mustPlanState(t, db, second); other.TotalCount != 0 {
		t.Fatalf("the next turn inherited a tree: %+v", other)
	}
	// The turn's own read carries both faces of that key: the step event and the
	// node the host declared.
	events := mustEvents(t, db, first)
	if len(events) != 1 || events[0].TopicID != first || events[0].NodePath != "1" {
		t.Fatalf("turn records = %+v, want the step event keyed to %s", events, first)
	}
}

// A declared tree folds a parent's conclusion out of its children once every
// child has settled, and a refused declaration leaves nothing behind.
func TestInterfacePlanDeclareAndFold(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	topicID := openTurn(t, db, sceneID)

	pending, done := memhop.PlanStatusPending, memhop.PlanStatusDone
	mustDeclare(t, db, topicID,
		memhop.PlanStep{NodePath: "1", Title: "父", Status: pending},
		memhop.PlanStep{NodePath: "1.1", Title: "子一", Status: done, Summary: "改动收敛到 3 个文件"},
		memhop.PlanStep{NodePath: "1.2", Title: "子二", Status: done, Summary: "测试全绿"},
	)
	// The fold is not a verdict on the parent: a parent is Done only because the
	// host declared it so, so an open parent keeps an empty Summary even with
	// every child settled.
	if got := findPlanNode(t, mustPlanState(t, db, topicID), "1"); got.Status != string(pending) || got.Summary != "" {
		t.Fatalf("an undeclared-done parent was folded: %+v", got)
	}
	mustDeclare(t, db, topicID, memhop.PlanStep{NodePath: "1", Status: done})

	tree := mustPlanState(t, db, topicID)
	folded := findPlanNode(t, tree, "1")
	if folded.Summary != "改动收敛到 3 个文件; 测试全绿" {
		t.Fatalf("rolled-up summary = %q", folded.Summary)
	}
	// Restating a step with a blank title keeps the stored one.
	if folded.Title != "父" {
		t.Fatalf("a blank title erased the stored one: %+v", folded)
	}
	if folded.FinishedAt == 0 {
		t.Fatal("a declared terminal step must carry a completion time")
	}
	// Every node comes back as a view the host can render without a second
	// call: string status, its own summary, and its own path.
	if tree.TotalCount != 3 || tree.DoneCount != 3 {
		t.Fatalf("counts: total=%d done=%d, want 3/3", tree.TotalCount, tree.DoneCount)
	}
	if len(folded.Children) != 2 {
		t.Fatalf("children = %+v, want two", folded.Children)
	}
	for _, c := range folded.Children {
		if c.Status != string(done) || c.Summary == "" || c.NodePath == "" {
			t.Fatalf("a child view lost its fields: %+v", c)
		}
	}
	// A refused declaration moves nothing: an unknown status, a path with a blank
	// segment, and one step named twice are all refused before the tree moves.
	before := renderTree(t, db, topicID)
	for name, steps := range map[string][]memhop.PlanStep{
		"unknown status":  {{NodePath: "1.1", Status: "finished"}},
		"blank segment":   {{NodePath: "1..3", Status: done}},
		"same step twice": {{NodePath: "1", Status: pending}, {NodePath: "1", Status: done}},
	} {
		if err := db.PlanSet(topicID, steps); err == nil {
			t.Fatalf("%s: a refused declaration was accepted", name)
		}
		if got := renderTree(t, db, topicID); got != before {
			t.Fatalf("%s: a refused declaration moved the tree\n before %s\n after  %s", name, before, got)
		}
	}
}

// The host's LLM re-plans every turn, so a turn's tree is a restatement of the
// plan as of that turn: step 3 exists alone first, gains siblings, then splits
// into its own sub-steps. Each turn owns a tree, and no later turn's
// restatement rewrites an earlier turn's nodes.
func TestInterfacePlanReplannedAcrossTurns(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	first := openTurn(t, db, sceneID)
	second := openTurn(t, db, sceneID)
	third := openTurn(t, db, sceneID)
	done := memhop.PlanStatusDone

	mustDeclare(t, db, first, memhop.PlanStep{NodePath: "1", Title: "还没想清楚", Status: done})
	mustDeclare(t, db, second,
		memhop.PlanStep{NodePath: "1", Status: done},
		memhop.PlanStep{NodePath: "2", Status: done},
		memhop.PlanStep{NodePath: "3", Title: "跑测试", Status: done, Summary: "全绿"},
		memhop.PlanStep{NodePath: "4", Status: done},
	)
	// Step 3 is the one that turned out to have parts. The parent is declared as
	// soon as its children are, so the fold can name what the whole step did.
	mustDeclare(t, db, third,
		memhop.PlanStep{NodePath: "1", Status: done},
		memhop.PlanStep{NodePath: "2", Status: done},
		memhop.PlanStep{NodePath: "3", Title: "跑测试", Status: done},
		memhop.PlanStep{NodePath: "3.1", Status: done, Summary: "单测"},
		memhop.PlanStep{NodePath: "3.2", Status: done, Summary: "集成"},
		memhop.PlanStep{NodePath: "3.3", Status: done, Summary: "基准"},
		memhop.PlanStep{NodePath: "4", Status: done},
	)

	secondTree := mustPlanState(t, db, second)
	if secondTree.TotalCount != 4 || len(secondTree.Roots) != 4 {
		t.Fatalf("second turn's flat plan = %+v, want four roots", secondTree)
	}
	// The earlier turn's node for step 3 keeps the summary that turn declared; a
	// restatement under a new topic id never reaches back into it.
	if got := findPlanNode(t, secondTree, "3").Summary; got != "全绿" {
		t.Fatalf("a later turn rewrote the earlier tree: %q", got)
	}

	thirdTree := mustPlanState(t, db, third)
	step3 := findPlanNode(t, thirdTree, "3")
	if len(step3.Children) != 3 {
		t.Fatalf("step 3 did not split: %+v", step3.Children)
	}
	if step3.Summary != "单测; 集成; 基准" {
		t.Fatalf("the split step folded to %q", step3.Summary)
	}
	if firstTree := mustPlanState(t, db, first); firstTree.TotalCount != 1 {
		t.Fatalf("the first turn's tree grew: %+v", firstTree)
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
	turnEvents := mustEvents(t, db, turnID)
	if len(turnEvents) != 1 {
		t.Fatalf("turn events = %+v, want the one appended", turnEvents)
	}
	if e := turnEvents[0]; e.TopicID != turnID || e.NodePath != "" {
		t.Fatalf("bare turn event = %+v, want keyed to %s and bound to no step", e, turnID)
	}

	mustDeclare(t, db, planTurn, memhop.PlanStep{NodePath: "1", Status: "in_progress"})
	mustAppend(t, db, planTurn, "1", planEvent(ts+1, "plan_step", "开始"))
	mustAppend(t, db, planTurn, "1", planEvent(ts+2, "tool_call", `{"tool":"bash","cmd":"go test"}`))
	planEvents := mustEvents(t, db, planTurn)
	// Seq is one space a topic shares with its dialogue, which holds slots 1 and 2,
	// so a topic's first event is 3.
	if len(planEvents) != 2 || planEvents[0].Seq != 3 || planEvents[1].Seq != 4 {
		t.Fatalf("plan events = %+v, want Seq 3 and 4", planEvents)
	}

	// Over budget is refused rather than shortened: a truncated event reads
	// exactly like a complete one, and nothing is written either way.
	tooBig := planEvent(ts+3, "tool_result", strings.Repeat("x", 4097))
	tooBig.NodePath = "1"
	if err := db.AppendArchive(planTurn, tooBig); err == nil {
		t.Fatal("a payload over the 4 KiB event budget should be refused")
	}
	if again := mustEvents(t, db, planTurn); len(again) != 2 {
		t.Fatalf("the refused event landed anyway: %+v", again)
	}

	// Both keys of the domain, so a host can pick one to work off afterwards.
	sums, err := db.ListTrajectorySessions()
	if err != nil {
		t.Fatalf("ListTrajectorySessions: %v", err)
	}
	eventCount := map[string]int{}
	for _, s := range sums {
		eventCount[s.SessionID] = s.Events
	}
	if eventCount[turnID] != 1 || eventCount[planTurn] != 2 {
		t.Fatalf("trajectory sessions = %+v, want %s:1 event and %s:2 events", sums, turnID, planTurn)
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

// The turn topic id is the only handle a host holds, so a restart must recover
// both faces of that key from disk: the trajectory events in Seq order with
// their payloads intact, and the plan tree with each node's own title,
// status and folded summary. Rebuilding the trajectory index and the plan cache
// is exactly where a re-keyed layer breaks silently.
func TestInterfacePlanAndTrajectorySurviveReopen(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "reopen.meh")
	db := newTestDB(t, openMockMulti(t, path, llm.srv.URL))
	sceneID := openSession(t, db)
	turnID := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	mustAppend(t, db, turnID, "", planEvent(ts, "tool_call", `{"tool":"bash"}`))
	mustDeclare(t, db, turnID, memhop.PlanStep{NodePath: "1.1", Title: "调研",
		Status: memhop.PlanStatusDone, Summary: "结论一"})
	mustAppend(t, db, turnID, "1.1", planEvent(ts+1, "plan_step", "第一步"))
	if err := db.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened := newTestDB(t, openMockMulti(t, path, llm.srv.URL))
	events := mustEvents(t, reopened, turnID)
	if len(events) != 2 || events[0].Seq != 3 || events[1].Seq != 4 {
		t.Fatalf("events after reopen = %+v, want Seq 3 and 4 rebuilt from records", events)
	}
	if events[0].EventType != "tool_call" || events[0].Content != `{"tool":"bash"}` {
		t.Fatalf("the event body did not survive the reopen: %+v", events[0])
	}
	if events[0].TopicID != turnID {
		t.Fatalf("rebuilt index keyed the event away from its turn: %+v", events[0])
	}
	leaf := findPlanNode(t, mustPlanState(t, reopened, turnID), "1.1")
	if leaf.Status != string(memhop.PlanStatusDone) || leaf.Title != "调研" ||
		leaf.Summary != "结论一" || leaf.FinishedAt == 0 {
		t.Fatalf("node fields lost on reopen: %+v", leaf)
	}
}
