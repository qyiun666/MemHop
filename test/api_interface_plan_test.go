// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for the L5 plan tree and the turn's event track (L4
// content). A host drives this the way meowagent does, one loop iteration per
// turn: `Search` reads the scene and hands back the topic id of the turn about to
// run, the host plans a step at a time with `PlanCreate`/`PlanNodeAdd` and
// restates one with `PlanNodeUpdate`, each step's work goes into L4 with
// `AppendArchive` bound to one created step, and `Update` settles the turn's
// dialogue into its topic. After a restart the host reads the tree back with a
// turn topic it already holds: the tree is addressed by the ordinals the library
// returned, so what a host must survive a restart holding is the turn's topic id
// — every assertion below reads the tree through `PlanState` rather than trusting
// the call that changed it.

package test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// mustCreate adds one step of a turn's plan tree and returns its ordinal. parent
// 0 hangs it at the top level; this is the only way a step comes into existence.
func mustCreate(t *testing.T, db *testDB, key string, parent uint32, title string) uint32 {
	t.Helper()
	seq, err := db.PlanNodeAdd(key, parent, title)
	if err != nil {
		t.Fatalf("PlanNodeAdd(%s, parent=%d, title=%q): %v", key, parent, title, err)
	}
	return seq
}

// mustUpdate restates one step: its status, plus a summary where the host has one.
func mustUpdate(t *testing.T, db *testDB, key string, seq uint32, status memhop.PlanStatus, summary string) {
	t.Helper()
	if err := db.PlanNodeUpdate(key, memhop.PlanStep{Seq: seq, Status: status, Summary: summary}); err != nil {
		t.Fatalf("PlanNodeUpdate(%s, step=%d, %s): %v", key, seq, status, err)
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

// findPlanNode looks a library-issued step ordinal up in the forest.
func findPlanNode(t *testing.T, tree memhop.PlanTree, seq uint32) memhop.PlanNodeView {
	t.Helper()
	var found *memhop.PlanNodeView
	var walk func([]memhop.PlanNodeView)
	walk = func(nodes []memhop.PlanNodeView) {
		for i := range nodes {
			if nodes[i].Seq == seq {
				found = &nodes[i]
				return
			}
			walk(nodes[i].Children)
		}
	}
	walk(tree.Roots)
	if found == nil {
		t.Fatalf("step %d missing from plan tree %+v", seq, tree)
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

// mustAppend writes one event, binding it to a plan step when nodeSeq names one
// (0 leaves it bound to nothing).
func mustAppend(t *testing.T, db *testDB, key string, nodeSeq uint32, ev memhop.ArchiveSlot) {
	t.Helper()
	ev.NodeSeq = nodeSeq
	if err := db.AppendArchive(key, ev); err != nil {
		t.Fatalf("AppendArchive(%s, step=%d): %v", key, nodeSeq, err)
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
			b.WriteString(strconv.FormatUint(uint64(n.Seq), 10) +
				"=" + n.Status + "/" + n.Summary + "/" + n.Title + " ")
			walk(n.Children)
		}
	}
	walk(mustPlanState(t, db, key).Roots)
	return b.String()
}

// One turn is one plan, and the turn's topic id is the only handle: the tree it
// opens reads back from that id, and two turns never share a tree — not even a
// step ordinal, which addresses a step inside one turn and nowhere else.
func TestInterfacePlanTreeLivesOnItsTurn(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	first := openTurn(t, db, sceneID)
	second := openTurn(t, db, sceneID)
	if first == second {
		t.Fatal("two turns of one scene share a topic id")
	}
	ts := time.Now().UnixMilli()

	step := mustCreate(t, db, first, 0, "第一步")
	mustAppend(t, db, first, step, planEvent(ts, "plan_step", "第一步"))
	if got := mustPlanState(t, db, first); got.TotalCount != 1 {
		t.Fatalf("first turn's tree = %+v, want one node", got)
	}
	if other := mustPlanState(t, db, second); other.TotalCount != 0 {
		t.Fatalf("the next turn inherited a tree: %+v", other)
	}
	// The turn's own read carries both faces of that key: the step event and the
	// node the host created.
	events := mustEvents(t, db, first)
	if len(events) != 1 || events[0].TopicID != first || events[0].NodeSeq != step {
		t.Fatalf("turn records = %+v, want the step event keyed to %s", events, first)
	}

	// An ordinal is an address inside one turn, so the next turn has no step 1 to
	// hang a child on yet — and it must not find the previous turn's.
	if _, err := db.PlanNodeAdd(second, step, "越界"); err == nil {
		t.Fatal("a step of another turn was accepted as a parent")
	}
	if got := mustPlanState(t, db, second); got.TotalCount != 0 {
		t.Fatalf("the refused create left a step behind: %+v", got)
	}
}

// A created tree folds a parent's conclusion out of its children once every child
// has settled, and a refused write leaves nothing behind.
func TestInterfacePlanCreateAndFold(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	topicID := openTurn(t, db, sceneID)
	done := memhop.PlanStatusDone

	root := mustCreate(t, db, topicID, 0, "父")
	c1 := mustCreate(t, db, topicID, root, "子一")
	c2 := mustCreate(t, db, topicID, root, "子二")
	mustUpdate(t, db, topicID, c1, done, "改动收敛到 3 个文件")
	mustUpdate(t, db, topicID, c2, done, "测试全绿")

	// The fold is not a verdict on the parent: a parent is Done only because the
	// host says so, so an open parent keeps an empty Summary even with every child
	// settled. A fresh step starts in progress with no status to state.
	if got := findPlanNode(t, mustPlanState(t, db, topicID), root); got.Status != string(memhop.PlanStatusInProgress) || got.Summary != "" {
		t.Fatalf("an undeclared-done parent was folded: %+v", got)
	}
	mustUpdate(t, db, topicID, root, done, "")

	tree := mustPlanState(t, db, topicID)
	folded := findPlanNode(t, tree, root)
	if folded.Summary != "改动收敛到 3 个文件; 测试全绿" {
		t.Fatalf("rolled-up summary = %q", folded.Summary)
	}
	// Restating a step with a blank title keeps the stored one.
	if folded.Title != "父" {
		t.Fatalf("a blank title erased the stored one: %+v", folded)
	}
	if folded.FinishedAt == 0 {
		t.Fatal("a step driven to a terminal status must carry a completion time")
	}
	// Every node comes back as a view the host can render without a second call:
	// string status, its own summary, and the ordinal it is addressed by.
	if tree.TotalCount != 3 || tree.DoneCount != 3 {
		t.Fatalf("counts: total=%d done=%d, want 3/3", tree.TotalCount, tree.DoneCount)
	}
	if len(folded.Children) != 2 {
		t.Fatalf("children = %+v, want two", folded.Children)
	}
	for _, want := range []uint32{c1, c2} {
		var c memhop.PlanNodeView
		for _, child := range folded.Children {
			if child.Seq == want {
				c = child
			}
		}
		if c.Status != string(done) || c.Summary == "" || c.ParentSeq != root {
			t.Fatalf("a child view lost its fields: %+v", c)
		}
	}

	// A refused write moves nothing, whichever way it was refused.
	before := renderTree(t, db, topicID)
	refused := map[string]func() error{
		"unknown status": func() error {
			return db.PlanNodeUpdate(topicID, memhop.PlanStep{Seq: c1, Status: "finished"})
		},
		"blank status": func() error {
			return db.PlanNodeUpdate(topicID, memhop.PlanStep{Seq: c1, Summary: "s"})
		},
		"a parent that is not there": func() error {
			_, err := db.PlanNodeAdd(topicID, 42, "凭空")
			return err
		},
		"restating a step that is not there": func() error {
			return db.PlanNodeUpdate(topicID, memhop.PlanStep{Seq: 42, Status: done})
		},
	}
	for name, call := range refused {
		if err := call(); err == nil {
			t.Fatalf("%s: a refused write was accepted", name)
		}
		if got := renderTree(t, db, topicID); got != before {
			t.Fatalf("%s: a refused write moved the tree\n before %s\n after  %s", name, before, got)
		}
	}
}

// Each turn plans its own tree, and building one never reaches back into a tree
// an earlier turn opened: the ordinals, the titles and the folded summaries a turn
// wrote stay that turn's.
func TestInterfacePlanTreesStayPerTurn(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	first := openTurn(t, db, sceneID)
	second := openTurn(t, db, sceneID)
	third := openTurn(t, db, sceneID)
	done := memhop.PlanStatusDone

	f1 := mustCreate(t, db, first, 0, "还没想清楚")
	mustUpdate(t, db, first, f1, done, "")

	// The second turn lays out four steps and finishes them.
	s1 := mustCreate(t, db, second, 0, "")
	s2 := mustCreate(t, db, second, 0, "")
	s3 := mustCreate(t, db, second, 0, "跑测试")
	s4 := mustCreate(t, db, second, 0, "")
	for _, tc := range []struct {
		seq     uint32
		summary string
	}{{s1, ""}, {s2, ""}, {s3, "全绿"}, {s4, ""}} {
		mustUpdate(t, db, second, tc.seq, done, tc.summary)
	}

	// The third turn is where "跑测试" turns out to have parts: its own step 3
	// splits into three, and the parent is settled last so the fold can name what
	// the whole step did.
	t1 := mustCreate(t, db, third, 0, "")
	t3 := mustCreate(t, db, third, 0, "跑测试")
	for _, title := range []string{"单测", "集成", "基准"} {
		part := mustCreate(t, db, third, t3, title)
		mustUpdate(t, db, third, part, done, title)
	}
	mustUpdate(t, db, third, t1, done, "")
	mustUpdate(t, db, third, t3, done, "")

	secondTree := mustPlanState(t, db, second)
	if secondTree.TotalCount != 4 || len(secondTree.Roots) != 4 {
		t.Fatalf("second turn's flat plan = %+v, want four roots", secondTree)
	}
	// The earlier turn's step keeps the summary that turn wrote; a later turn's
	// work never reaches back into it.
	if got := findPlanNode(t, secondTree, s3).Summary; got != "全绿" {
		t.Fatalf("a later turn rewrote the earlier tree: %q", got)
	}

	thirdTree := mustPlanState(t, db, third)
	step3 := findPlanNode(t, thirdTree, t3)
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

func TestInterfaceTurnEventsKeyToTheirOwnTurn(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	turnID := openTurn(t, db, sceneID)
	planTurn := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	// A bare turn event takes any EventType the host names and is keyed to the
	// turn it logs, so the log cannot disagree with the turn.
	mustAppend(t, db, turnID, 0, planEvent(ts, "host_note", "本轮没有工具调用"))
	turnEvents := mustEvents(t, db, turnID)
	if len(turnEvents) != 1 {
		t.Fatalf("turn events = %+v, want the one appended", turnEvents)
	}
	if e := turnEvents[0]; e.TopicID != turnID || e.NodeSeq != 0 {
		t.Fatalf("bare turn event = %+v, want keyed to %s and bound to no step", e, turnID)
	}

	step := mustCreate(t, db, planTurn, 0, "开始")
	mustAppend(t, db, planTurn, step, planEvent(ts+1, "plan_step", "开始"))
	mustAppend(t, db, planTurn, step, planEvent(ts+2, "tool_call", `{"tool":"bash","cmd":"go test"}`))
	planEvents := mustEvents(t, db, planTurn)
	// Seq is one space a topic shares with its dialogue, which holds slots 1 and 2,
	// so a topic's first event is 3.
	if len(planEvents) != 2 || planEvents[0].Seq != 3 || planEvents[1].Seq != 4 {
		t.Fatalf("plan events = %+v, want Seq 3 and 4", planEvents)
	}

	// Over budget is refused rather than shortened: a truncated event reads
	// exactly like a complete one, and nothing is written either way.
	tooBig := planEvent(ts+3, "tool_result", strings.Repeat("x", 4097))
	tooBig.NodeSeq = step
	if err := db.AppendArchive(planTurn, tooBig); err == nil {
		t.Fatal("a payload over the 4 KiB event budget should be refused")
	}
	if again := mustEvents(t, db, planTurn); len(again) != 2 {
		t.Fatalf("the refused event landed anyway: %+v", again)
	}
}

// The turn topic id is the only handle a host holds, so a restart must recover
// both faces of that key from disk: the trajectory events in Seq order with
// their payloads intact, and the plan tree with each node's own title, status
// and folded summary. Rebuilding the trajectory index and the plan cache is
// exactly where a re-keyed layer breaks silently.
func TestInterfacePlanAndTrajectorySurviveReopen(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "reopen.meh")
	db := newTestDB(t, openMockDB(t, path, llm.srv.URL))
	sceneID := openSession(t, db)
	turnID := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	mustAppend(t, db, turnID, 0, planEvent(ts, "tool_call", `{"tool":"bash"}`))
	root := mustCreate(t, db, turnID, 0, "计划")
	leaf := mustCreate(t, db, turnID, root, "调研")
	mustUpdate(t, db, turnID, leaf, memhop.PlanStatusDone, "结论一")
	mustAppend(t, db, turnID, leaf, planEvent(ts+1, "plan_step", "第一步"))
	if err := db.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened := newTestDB(t, openMockDB(t, path, llm.srv.URL))
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
	// The ordinals the reopened cache hands out are the ones the tree was built
	// with, not a fresh count: a host that kept only the turn's topic id still
	// finds its steps by the number it was given.
	if again := mustPlanState(t, reopened, turnID); again.TotalCount != 2 {
		t.Fatalf("tree after reopen = %+v, want both steps rebuilt", again)
	}
	node := findPlanNode(t, mustPlanState(t, reopened, turnID), leaf)
	if node.Status != string(memhop.PlanStatusDone) || node.Title != "调研" ||
		node.Summary != "结论一" || node.FinishedAt == 0 || node.ParentSeq != root {
		t.Fatalf("node fields lost on reopen: %+v", node)
	}
	if next, err := reopened.PlanNodeAdd(turnID, 0, "再加一步"); err != nil || next != 3 {
		t.Fatalf("the reopened tree resumed at %d (%v), want one above its highest step", next, err)
	}
}
