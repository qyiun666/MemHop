// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for the L5 plan tree and the turn's event track (L4
// content). A host drives this the way meowagent does, one loop iteration per
// turn: `Search` reads the scene and opens the turn about to run, the host adds
// steps one at a time with `PlanNodeAdd` (parentSeq 0 opens the tree) and restates
// one with `PlanNodeUpdate`, each step's work goes into L4 with `AppendArchive`
// bound to one created step, and `Update` closes the turn's dialogue into its
// topic. None of those writes names the turn: the tree, the events and the close all
// go to the turn the domain holds open, so a turn is planned start to finish before
// the next one opens. What a host still carries across a restart is the turn's topic
// id, and what it can still read of an older turn through that id is its event track —
// the plan tree of a turn that is no longer the open one has no read. Every assertion
// below reads the tree through `PlanState` rather than trusting the call that changed
// it.

package test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// mustCreate adds one step to the open turn's plan tree and returns its ordinal.
// parent 0 hangs it at the top level; this is the only way a step comes into
// existence.
func mustCreate(t *testing.T, db *testDB, parent uint32, title string) uint32 {
	t.Helper()
	seq, err := db.PlanNodeAdd(parent, title)
	if err != nil {
		t.Fatalf("PlanNodeAdd(parent=%d, title=%q): %v", parent, title, err)
	}
	return seq
}

// mustUpdate restates one step of the open turn's tree: its status, plus a summary
// where the host has one.
func mustUpdate(t *testing.T, db *testDB, seq uint32, status memhop.PlanStatus, summary string) {
	t.Helper()
	if err := db.PlanNodeUpdate(memhop.PlanStep{Seq: seq, Status: status, Summary: summary}); err != nil {
		t.Fatalf("PlanNodeUpdate(step=%d, %s): %v", seq, status, err)
	}
}

// mustPlanState reads the open turn's tree.
func mustPlanState(t *testing.T, db *testDB) memhop.PlanTree {
	t.Helper()
	tree, err := db.PlanState()
	if err != nil {
		t.Fatalf("PlanState: %v", err)
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

// mustEvents reads one turn's event track: its own topic id with the kind
// condition, which is all a host has left for that read — and the only one that
// still reaches a turn other than the open one.
func mustEvents(t *testing.T, db *testDB, key string) []memhop.ArchiveSlot {
	t.Helper()
	kind := memhop.KindEvent
	events, err := db.SearchL4(memhop.L4Query{TopicID: &key, Kind: &kind})
	if err != nil {
		t.Fatalf("read events of %s: %v", key, err)
	}
	return events
}

func planEvent(ts int64, kind, payload string) memhop.ArchiveInput {
	return memhop.ArchiveInput{Kind: memhop.KindEvent, EventType: kind, Content: payload, CreatedAt: ts}
}

// mustAppend writes one event into the open turn's content, binding it to a plan
// step when nodeSeq names one (0 leaves it bound to nothing).
func mustAppend(t *testing.T, db *testDB, nodeSeq uint32, ev memhop.ArchiveInput) {
	t.Helper()
	ev.NodeSeq = nodeSeq
	if _, err := db.AppendArchive(ev); err != nil {
		t.Fatalf("AppendArchive(step=%d): %v", nodeSeq, err)
	}
}

// renderTree flattens the open turn's plan forest into one line, so a before/after
// comparison says what moved instead of leaking a Go map diff.
func renderTree(t *testing.T, db *testDB) string {
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
	walk(mustPlanState(t, db).Roots)
	return b.String()
}

// One turn is one plan, and the open turn is the only handle a write has: the tree
// it opens reads back under the topic Search minted for it, and two turns never
// share a tree — not even a step ordinal, which addresses a step inside one turn and
// nowhere else. So the turns run one at a time: open, plan, close.
func TestInterfacePlanTreeLivesOnItsTurn(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	ts := time.Now().UnixMilli()

	first := openTurn(t, db, sceneID)
	step := mustCreate(t, db, 0, "第一步")
	mustAppend(t, db, step, planEvent(ts, "plan_step", "第一步"))
	if got := mustPlanState(t, db); got.TotalCount != 1 {
		t.Fatalf("first turn's tree = %+v, want one node", got)
	}
	if closed, err := turn(db.Session, "先做第一步", "第一步做完了"); err != nil {
		t.Fatalf("close the first turn: %v", err)
	} else if closed != first {
		t.Fatalf("the close settled %s, want the turn Search minted (%s)", closed, first)
	}
	// The turn's own read carries both faces of that key: the step event and the
	// node the host created.
	events := mustEvents(t, db, first)
	if len(events) != 1 || events[0].TopicID != first || events[0].NodeSeq != step {
		t.Fatalf("turn records = %+v, want the step event keyed to %s", events, first)
	}

	// The next turn opens on an empty tree: reading hands the host no plan.
	second := openTurn(t, db, sceneID)
	if second == first {
		t.Fatal("two turns of one scene share a topic id")
	}
	if got := mustPlanState(t, db); got.TotalCount != 0 {
		t.Fatalf("the next turn inherited a tree: %+v", got)
	}
	// An ordinal is an address inside one turn, so this turn has no step to hang a
	// child on yet — and the number the previous turn used is not one either.
	if _, err := db.PlanNodeAdd(step, "越界"); err == nil {
		t.Fatal("a step of another turn was accepted as a parent")
	}
	if got := mustPlanState(t, db); got.TotalCount != 0 {
		t.Fatalf("the refused create left a step behind: %+v", got)
	}
}

// A created tree folds a parent's conclusion out of its children once every child
// has settled, and a refused write leaves nothing behind.
func TestInterfacePlanAddAndFold(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	openTurn(t, db, sceneID)
	done := memhop.PlanStatusDone

	root := mustCreate(t, db, 0, "父")
	c1 := mustCreate(t, db, root, "子一")
	c2 := mustCreate(t, db, root, "子二")
	mustUpdate(t, db, c1, done, "改动收敛到 3 个文件")
	mustUpdate(t, db, c2, done, "测试全绿")

	// The fold is not a verdict on the parent: a parent is Done only because the
	// host says so, so an open parent keeps an empty Summary even with every child
	// settled. A fresh step starts in progress with no status to state.
	if got := findPlanNode(t, mustPlanState(t, db), root); got.Status != string(memhop.PlanStatusInProgress) || got.Summary != "" {
		t.Fatalf("an undeclared-done parent was folded: %+v", got)
	}
	mustUpdate(t, db, root, done, "")

	tree := mustPlanState(t, db)
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
	before := renderTree(t, db)
	refused := map[string]func() error{
		"unknown status": func() error {
			return db.PlanNodeUpdate(memhop.PlanStep{Seq: c1, Status: "finished"})
		},
		"blank status": func() error {
			return db.PlanNodeUpdate(memhop.PlanStep{Seq: c1, Summary: "s"})
		},
		"a parent that is not there": func() error {
			_, err := db.PlanNodeAdd(42, "凭空")
			return err
		},
		"restating a step that is not there": func() error {
			return db.PlanNodeUpdate(memhop.PlanStep{Seq: 42, Status: done})
		},
	}
	for name, call := range refused {
		if err := call(); err == nil {
			t.Fatalf("%s: a refused write was accepted", name)
		}
		if got := renderTree(t, db); got != before {
			t.Fatalf("%s: a refused write moved the tree\n before %s\n after  %s", name, before, got)
		}
	}
}

// Each turn plans its own tree, and building one never reaches back into a tree an
// earlier turn opened: the ordinals, the titles and the folded summaries a turn wrote
// stay that turn's. A plan tree is addressed by the turn that is open, so the turns
// are played one after another — and each one's tree is read while that turn is still
// the domain's, which is the only moment the public surface can name it.
func TestInterfacePlanTreesStayPerTurn(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	done := memhop.PlanStatusDone

	// The first turn thinks one step ahead and closes.
	first := openTurn(t, db, sceneID)
	f1 := mustCreate(t, db, 0, "还没想清楚")
	mustUpdate(t, db, f1, done, "")
	if closed, err := turn(db.Session, "先想清楚第一步", "想清楚了"); err != nil {
		t.Fatalf("close the first turn: %v", err)
	} else if closed != first {
		t.Fatalf("closed %s, want the first turn (%s)", closed, first)
	}
	if got := mustPlanState(t, db); got.TotalCount != 1 {
		t.Fatalf("the first turn's tree grew before it closed: %+v", got)
	}

	// The second turn lays out four steps and finishes them. Its tree starts empty:
	// the step the earlier turn numbered 1 is not inherited, and this turn's own
	// first step takes that same number under its own key.
	second := openTurn(t, db, sceneID)
	if got := mustPlanState(t, db); got.TotalCount != 0 {
		t.Fatalf("the second turn inherited the first's tree: %+v", got)
	}
	s1 := mustCreate(t, db, 0, "")
	s2 := mustCreate(t, db, 0, "")
	s3 := mustCreate(t, db, 0, "跑测试")
	s4 := mustCreate(t, db, 0, "")
	if s1 != f1 {
		t.Fatalf("ordinals are not per-turn: first turn %d, second %d", f1, s1)
	}
	for _, tc := range []struct {
		seq     uint32
		summary string
	}{{s1, ""}, {s2, ""}, {s3, "全绿"}, {s4, ""}} {
		mustUpdate(t, db, tc.seq, done, tc.summary)
	}
	secondTree := mustPlanState(t, db)
	if secondTree.TotalCount != 4 || len(secondTree.Roots) != 4 {
		t.Fatalf("second turn's flat plan = %+v, want four roots", secondTree)
	}
	// The step this turn wrote carries this turn's summary — and it shares its
	// ordinal with the first turn's single step, whose tree held one node.
	if got := findPlanNode(t, secondTree, s3).Summary; got != "全绿" {
		t.Fatalf("the second turn's step 3 reads %q, want 全绿", got)
	}
	if closed, err := turn(db.Session, "跑完测试", "全绿"); err != nil {
		t.Fatalf("close the second turn: %v", err)
	} else if closed != second {
		t.Fatalf("closed %s, want the second turn (%s)", closed, second)
	}

	// The third turn is where "跑测试" turns out to have parts: its own step 3
	// splits into three, and the parent is settled last so the fold can name what
	// the whole step did.
	third := openTurn(t, db, sceneID)
	if got := mustPlanState(t, db); got.TotalCount != 0 {
		t.Fatalf("the third turn inherited a tree: %+v", got)
	}
	t1 := mustCreate(t, db, 0, "")
	t3 := mustCreate(t, db, 0, "跑测试")
	for _, title := range []string{"单测", "集成", "基准"} {
		part := mustCreate(t, db, t3, title)
		mustUpdate(t, db, part, done, title)
	}
	mustUpdate(t, db, t1, done, "")
	mustUpdate(t, db, t3, done, "")

	thirdTree := mustPlanState(t, db)
	step3 := findPlanNode(t, thirdTree, t3)
	if len(step3.Children) != 3 {
		t.Fatalf("step 3 did not split: %+v", step3.Children)
	}
	if step3.Summary != "单测; 集成; 基准" {
		t.Fatalf("the split step folded to %q", step3.Summary)
	}
	if thirdTree.TotalCount != 5 {
		t.Fatalf("third turn's tree = %+v, want its own five steps", thirdTree)
	}
	if closed, err := turn(db.Session, "把跑测试拆开做", "三步都绿了"); err != nil {
		t.Fatalf("close the third turn: %v", err)
	} else if closed != third {
		t.Fatalf("closed %s, want the third turn (%s)", closed, third)
	}
}

func TestInterfaceTurnEventsKeyToTheirOwnTurn(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	ts := time.Now().UnixMilli()

	// Turn one: a bare turn event takes any EventType the host names and is keyed to
	// the turn it logs, so the log cannot disagree with the turn.
	turnID := openTurn(t, db, sceneID)
	mustAppend(t, db, 0, planEvent(ts, "host_note", "本轮没有工具调用"))
	turnEvents := mustEvents(t, db, turnID)
	if len(turnEvents) != 1 {
		t.Fatalf("turn events = %+v, want the one appended", turnEvents)
	}
	if e := turnEvents[0]; e.TopicID != turnID || e.NodeSeq != 0 {
		t.Fatalf("bare turn event = %+v, want keyed to %s and bound to no step", e, turnID)
	}
	if _, err := turn(db.Session, "这轮什么都没干", "确实没有"); err != nil {
		t.Fatalf("close the first turn: %v", err)
	}

	// Turn two: a step, and two events bound to it. The read of each turn is
	// addressed by its own id, which is why the first turn's track is still a
	// witness that nothing from this turn leaked into it.
	planTurn := openTurn(t, db, sceneID)
	step := mustCreate(t, db, 0, "开始")
	mustAppend(t, db, step, planEvent(ts+1, "plan_step", "开始"))
	mustAppend(t, db, step, planEvent(ts+2, "tool_call", `{"tool":"bash","cmd":"go test"}`))
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
	if _, err := db.AppendArchive(tooBig); err == nil {
		t.Fatal("a payload over the 4 KiB event budget should be refused")
	}
	if again := mustEvents(t, db, planTurn); len(again) != 2 {
		t.Fatalf("the refused event landed anyway: %+v", again)
	}
	if still := mustEvents(t, db, turnID); len(still) != 1 {
		t.Fatalf("the second turn's events landed in the first turn's track: %+v", still)
	}
}

// A restart clears the domain's memory of which turn was open, so what has to come
// back from disk is what the records can still answer: the turn's trajectory events
// in Seq order with their payloads and their step attribution, and the plan cache
// rebuilt well enough to still expand one step into its subtree. The subtree read is
// the witness that the tree itself was rebuilt and not just the content index —
// an unkeyed cache answers a subtree with the step alone.
func TestInterfacePlanAndTrajectorySurviveReopen(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "reopen.meh")
	db := newTestDB(t, openMockDB(t, path, llm.srv.URL))
	sceneID := openSession(t, db)
	turnID := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	mustAppend(t, db, 0, planEvent(ts, "tool_call", `{"tool":"bash"}`))
	root := mustCreate(t, db, 0, "计划")
	leaf := mustCreate(t, db, root, "调研")
	mustUpdate(t, db, leaf, memhop.PlanStatusDone, "结论一")
	mustAppend(t, db, root, planEvent(ts+1, "plan_step", "计划开工"))
	mustAppend(t, db, leaf, planEvent(ts+2, "plan_step", "第一步"))

	// The node fields are read while the turn is still the open one — no call
	// reaches a closed turn's tree.
	before := mustPlanState(t, db)
	node := findPlanNode(t, before, leaf)
	if node.Status != string(memhop.PlanStatusDone) || node.Title != "调研" ||
		node.Summary != "结论一" || node.FinishedAt == 0 || node.ParentSeq != root {
		t.Fatalf("node fields before the restart: %+v", node)
	}
	if before.TotalCount != 2 {
		t.Fatalf("tree before the restart = %+v, want the two steps", before)
	}
	if _, err := turn(db.Session, "按计划调研", "第一步有结论了"); err != nil {
		t.Fatalf("close the turn: %v", err)
	}
	if err := db.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened := newTestDB(t, openMockDB(t, path, llm.srv.URL))
	events := mustEvents(t, reopened, turnID)
	if len(events) != 3 || events[0].Seq != 3 || events[1].Seq != 4 || events[2].Seq != 5 {
		t.Fatalf("events after reopen = %+v, want Seq 3, 4 and 5 rebuilt from records", events)
	}
	if events[0].EventType != "tool_call" || events[0].Content != `{"tool":"bash"}` {
		t.Fatalf("the event body did not survive the reopen: %+v", events[0])
	}
	for _, e := range events {
		if e.TopicID != turnID {
			t.Fatalf("rebuilt index keyed an event away from its turn: %+v", e)
		}
	}
	// Asking for one step's work means that step and everything nested under it, and
	// the set comes out of the plan cache: the child's event only answers to the
	// parent's query if the reopened cache rebuilt the edge between them.
	underRoot, err := reopened.SearchL4(memhop.L4Query{TopicID: &turnID, NodeSeq: root})
	if err != nil {
		t.Fatalf("read the root's subtree after reopen: %v", err)
	}
	if len(underRoot) != 2 || underRoot[0].Seq != 4 || underRoot[1].Seq != 5 {
		t.Fatalf("root subtree after reopen = %+v, want the root's own event and its child's", underRoot)
	}
	underLeaf, err := reopened.SearchL4(memhop.L4Query{TopicID: &turnID, NodeSeq: leaf})
	if err != nil {
		t.Fatalf("read the leaf's subtree after reopen: %v", err)
	}
	if len(underLeaf) != 1 || underLeaf[0].Seq != 5 {
		t.Fatalf("leaf subtree after reopen = %+v, want the leaf's own event alone", underLeaf)
	}
	// The other half of the same rebuild: a turn opened after the restart starts its
	// own tree at 1 rather than continuing the count of the turn that closed.
	openTurn(t, reopened, sceneID)
	if got := mustPlanState(t, reopened); got.TotalCount != 0 {
		t.Fatalf("the reopened cache handed a new turn an old tree: %+v", got)
	}
	if next, err := reopened.PlanNodeAdd(0, "新轮的第一步"); err != nil || next != 1 {
		t.Fatalf("the new turn's first step = %d (%v), want ordinal 1", next, err)
	}
}

// The number AppendArchive hands back is the record's address: naming it again rewrites
// that slot in place rather than stacking a second version of one fact, which is what
// makes a host's at-least-once write loop converge. Every earlier call site discarded the
// return and re-read the slot, so the replay contract itself was unproven.
func TestInterfaceAppendReturnsTheAddressAReplayRewrites(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	turnID := openTurn(t, db, sceneID)
	ts := time.Now().UnixMilli()

	first, err := db.AppendArchive(planEvent(ts, "tool_call", "读 config.go"))
	if err != nil {
		t.Fatalf("append the call: %v", err)
	}
	second, err := db.AppendArchive(planEvent(ts+1, "tool_result", "四个字段"))
	if err != nil {
		t.Fatalf("append the result: %v", err)
	}
	if second != first+1 {
		t.Fatalf("auto-allocation handed out %d then %d, want consecutive slots", first, second)
	}

	again, err := db.AppendArchive(memhop.ArchiveInput{
		Kind: memhop.KindEvent, Seq: first, EventType: "tool_call",
		Content: "读 config.rs", CreatedAt: ts + 2,
	})
	if err != nil {
		t.Fatalf("replay the address: %v", err)
	}
	if again != first {
		t.Fatalf("the replay answered %d, want the address it named (%d)", again, first)
	}
	events := mustEvents(t, db, turnID)
	if len(events) != 2 {
		t.Fatalf("the turn's event track = %+v, want the two slots it took", events)
	}
	if events[0].Seq != first || events[0].Content != "读 config.rs" {
		t.Fatalf("naming the slot did not rewrite it: %+v", events[0])
	}
	if events[1].Seq != second || events[1].Content != "四个字段" {
		t.Fatalf("the slot nobody named moved: %+v", events[1])
	}
	// The id follows (topic, Seq), so the key a host already holds still addresses the
	// rewritten record — a replay does not cost it a fresh id to track.
	byID, err := db.SearchL4(memhop.L4Query{IDs: []string{events[0].ID}})
	if err != nil || len(byID) != 1 || byID[0].Content != "读 config.rs" {
		t.Fatalf("reading the replayed record back by its id = %+v err %v", byID, err)
	}
}

// PlanNodeUpdate restates one step, and a field the host leaves out is not a request to
// erase it: the title and the summary a previous round wrote stay. Only Status has no
// blank spelling. The title half was pinned; the summary half — the one a host fills in
// when a step finishes — is what this case adds.
func TestInterfacePlanUpdateKeepsTheSummaryItWasNotGiven(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	openTurn(t, db, sceneID)

	step := mustCreate(t, db, 0, "定方案")
	mustUpdate(t, db, step, memhop.PlanStatusDone, "结论：读路径走 mmap，零拷贝")
	if got := findPlanNode(t, mustPlanState(t, db), step); got.Summary != "结论：读路径走 mmap，零拷贝" {
		t.Fatalf("the first restatement did not land: %+v", got)
	}

	if err := db.PlanNodeUpdate(memhop.PlanStep{Seq: step, Status: memhop.PlanStatusInProgress}); err != nil {
		t.Fatalf("restating only the status: %v", err)
	}
	got := findPlanNode(t, mustPlanState(t, db), step)
	if got.Summary != "结论：读路径走 mmap，零拷贝" || got.Title != "定方案" {
		t.Fatalf("an update that sent neither field erased one of them: %+v", got)
	}
	if got.FinishedAt != 0 {
		t.Fatalf("re-opening a settled step must drop its finish time, got %d", got.FinishedAt)
	}
	// A field the host does send still replaces what was there — keeping the blank is not
	// a refusal to write.
	if err := db.PlanNodeUpdate(memhop.PlanStep{Seq: step, Status: memhop.PlanStatusDone, Summary: "结论改为写侧批量提交"}); err != nil {
		t.Fatalf("restating the summary: %v", err)
	}
	if again := findPlanNode(t, mustPlanState(t, db), step); again.Summary != "结论改为写侧批量提交" {
		t.Fatalf("a stated summary did not replace the stored one: %+v", again)
	}
}

// A folded parent summary is the branch's conclusion, so it has to keep following the
// branch: a step added under a parent the host already declared Done, or a settled child
// re-opened and finished with different text, must not leave the parent stating a summary
// of the branch as it looked earlier. Host text is a different thing — a summary the host
// wrote itself is never clobbered, and the two are told apart by the fold's own shape
// (children's conclusions joined in creation order), not by extra state on the record.
func TestInterfaceParentFoldFollowsTheBranchItSummarizes(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	openTurn(t, db, sceneID)

	root := mustCreate(t, db, 0, "总任务")
	c1 := mustCreate(t, db, root, "步骤一")
	c2 := mustCreate(t, db, root, "步骤二")
	mustUpdate(t, db, c1, memhop.PlanStatusDone, "一的结论")
	mustUpdate(t, db, c2, memhop.PlanStatusDone, "二的结论")
	mustUpdate(t, db, root, memhop.PlanStatusDone, "")
	if got := findPlanNode(t, mustPlanState(t, db), root); got.Summary != "一的结论; 二的结论" {
		t.Fatalf("the first fold = %q, want both conclusions in plan order", got.Summary)
	}

	// A step planned after the parent closed: while it is open, the branch is not
	// settled, so the parent keeps the fold it has rather than growing a partial one.
	c3 := mustCreate(t, db, root, "步骤三")
	if got := findPlanNode(t, mustPlanState(t, db), root); got.Summary != "一的结论; 二的结论" {
		t.Fatalf("an unsettled branch rewrote the fold: %q", got.Summary)
	}
	mustUpdate(t, db, c3, memhop.PlanStatusDone, "三的结论")
	if got := findPlanNode(t, mustPlanState(t, db), root); got.Summary != "一的结论; 二的结论; 三的结论" {
		t.Fatalf("the settled branch did not re-fold: %q, want the third conclusion added", got.Summary)
	}

	// Re-opening a settled child unsettles the branch, and finishing it with different
	// words re-derives the parent's conclusion.
	mustUpdate(t, db, c1, memhop.PlanStatusInProgress, "")
	if got := findPlanNode(t, mustPlanState(t, db), root); got.Summary != "一的结论; 二的结论; 三的结论" {
		t.Fatalf("a branch with an open child re-folded: %q", got.Summary)
	}
	mustUpdate(t, db, c1, memhop.PlanStatusDone, "一改了口")
	if got := findPlanNode(t, mustPlanState(t, db), root); got.Summary != "一改了口; 二的结论; 三的结论" {
		t.Fatalf("the re-settled branch did not re-fold: %q", got.Summary)
	}

	// Host text outlives every later rollup.
	mustUpdate(t, db, root, memhop.PlanStatusDone, "宿主自己写的收口")
	mustUpdate(t, db, c2, memhop.PlanStatusDone, "二改了口")
	got := findPlanNode(t, mustPlanState(t, db), root)
	if got.Summary != "宿主自己写的收口" {
		t.Fatalf("a rollup overwrote the host's own parent summary: %q", got.Summary)
	}
	if child := findPlanNode(t, mustPlanState(t, db), c2); child.Summary != "二改了口" {
		t.Fatalf("the restated child lost its own conclusion: %+v", child)
	}
}
