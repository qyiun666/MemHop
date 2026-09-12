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
	"github.com/qyiun666/MemHop/internal/content"
	"github.com/qyiun666/MemHop/internal/dream"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ev builds the event a host hands to an append: an event declares its kind, names
// itself, and carries content and a timestamp.
func ev(eventType string, ts int64) core.ArchiveSlot {
	return core.ArchiveSlot{Kind: core.KindEvent, EventType: eventType, Content: eventType, CreatedAt: ts}
}

// onStep names the plan step an event belongs to: the ordinal goes on the record,
// and it never creates the step.
func onStep(slot core.ArchiveSlot, seq uint32) core.ArchiveSlot {
	slot.NodeSeq = seq
	return slot
}

// add creates one step of a turn's tree and fails the test if the call was
// refused, so a tree-shaped test reads as the sequence of steps it builds.
func add(t *testing.T, db *DB, topicID string, parentSeq uint32, title string) uint32 {
	t.Helper()
	seq, err := db.PlanNodeAdd(core.DefaultAgentID, topicID, parentSeq, title)
	if err != nil {
		t.Fatalf("PlanNodeAdd(parent=%d, title=%q): %v", parentSeq, title, err)
	}
	return seq
}

// restate applies one step's status (and optional summary) to a tree.
func restate(t *testing.T, db *DB, topicID string, seq uint32, status PlanStatus, summary string) {
	t.Helper()
	err := db.PlanNodeUpdate(core.DefaultAgentID, topicID, PlanStep{
		Seq: seq, Status: status, Summary: summary})
	if err != nil {
		t.Fatalf("PlanNodeUpdate(step=%d, status=%s): %v", seq, status, err)
	}
}

// eventsOf reads one topic's event track the way a host does: the same key with the
// kind condition, in Seq order.
func (db *DB) eventsOf(agentID uint64, topicHex string) ([]core.ArchiveSlot, error) {
	kind := core.KindEvent
	return db.SearchL4(agentID, L4Query{TopicID: &topicHex, Kind: &kind})
}

// stepEvents reads the events bound to one step from the topic's content track,
// Seq ascending. A node holds no list of its events: the ordinal is stamped on the
// event, and that is all the attribution a reader needs.
func stepEvents(t *testing.T, db *DB, topicID uint64, seq uint32) []core.ArchiveSlot {
	t.Helper()
	var out []core.ArchiveSlot
	for _, arc := range core.CollectAllArchives(db.engine, core.DefaultAgentID) {
		if arc.TopicID == topicID && arc.Kind == core.KindEvent && arc.NodeSeq == seq {
			out = append(out, arc)
		}
	}
	slices.SortFunc(out, func(a, b core.ArchiveSlot) int { return cmp.Compare(a.Seq, b.Seq) })
	return out
}

// A step's ordinal is handed out from the plan mirror, and that mirror is built from
// the records which still decode — so a plan node whose payload does not is invisible
// to it while its ordinal lives on in the address it was stored at. Creating there
// would replace a step this engine cannot read, and the events bound to that ordinal
// would then read as the new step's work, so the create path asks the disk first.
func TestPlanCreateRefusesAnAddressItCannotRead(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	const topic = uint64(99)
	topicID := common.FormatHash(topic)
	address := core.HashPlanNode(topic, 1)
	const corrupt = `{"id":`
	if _, err := db.engine.WriteRecord(core.DefaultAgentID, core.RecL5PlanNode, address,
		[]byte(corrupt)); err != nil {
		t.Fatalf("write the step no mirror can list: %v", err)
	}

	if _, err := db.PlanCreate(core.DefaultAgentID, topicID, "重铸的一步"); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("creating at an address that does not decode must report its own code, got %v", err)
	}
	if _, data, err := db.engine.ReadRecord(core.DefaultAgentID, address); err != nil || string(data) != corrupt {
		t.Fatalf("the refused create overwrote the record it could not read: %q err=%v", data, err)
	}
}

func TestAppendArchiveAllocatesAboveDialogueSlots(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	session := common.FormatHash(99)
	for i := 1; i <= 3; i++ {
		if err := db.AppendArchive(core.DefaultAgentID, session, ev("llm_request", int64(i))); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	events, err := db.eventsOf(core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}
	// Slots 1 and 2 belong to dialogue, so an event appended before a single
	// original is spoken still lands above them: the two tracks cannot collide by
	// accident, whichever order the host appends in.
	for i, e := range events {
		if want := uint64(i) + core.LastUtteranceSeq + 1; e.Seq != want {
			t.Fatalf("seq[%d] = %d, want %d", i, e.Seq, want)
		}
	}
}

func TestAppendArchiveValidation(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	bare := core.ArchiveSlot{Kind: core.KindEvent, Content: "step", CreatedAt: 1}
	if err := db.AppendArchive(core.DefaultAgentID, common.FormatHash(1), bare); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("empty event type: want ErrInvalidQuery, got %v", err)
	}
	if err := db.AppendArchive(core.DefaultAgentID, common.FormatHash(1), core.ArchiveSlot{Kind: core.KindEvent, Content: "step", EventType: "tool_call"}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("zero timestamp: want ErrInvalidQuery, got %v", err)
	}
	// A plan-bound write is refused by the same contract, and the zero key is
	// refused before it: neither may create a node on its way out.
	if err := db.AppendArchive(core.DefaultAgentID, common.FormatHash(1), onStep(bare, 1)); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("bound empty type: want ErrInvalidQuery, got %v", err)
	}
	if err := db.AppendArchive(core.DefaultAgentID, "0000000000000000", ev("x", 1)); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("reserved key: want ErrInvalidQuery, got %v", err)
	}
}

func TestAppendEventPayloadRefused(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	key := common.FormatHash(3)
	long := strings.Repeat("x", content.MaxEventPayload+100)
	if err := db.AppendArchive(core.DefaultAgentID, key, core.ArchiveSlot{
		Kind: core.KindEvent, EventType: "tool_call", Content: long, CreatedAt: 1,
	}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an over-budget payload must be refused with ErrInvalidQuery, got %v", err)
	}
	events, err := db.eventsOf(core.DefaultAgentID, key)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("a refused append must store nothing, got %d events", len(events))
	}
	// exactly at the budget still writes — the budget is the whole record, so a
	// one-byte name leaves the rest to the body
	if err := db.AppendArchive(core.DefaultAgentID, key, core.ArchiveSlot{
		Kind: core.KindEvent, EventType: "t",
		Content: strings.Repeat("x", content.MaxEventPayload-1), CreatedAt: 1,
	}); err != nil {
		t.Fatalf("payload at the budget limit should append: %v", err)
	}
	// The name is part of the record. A caller that puts the bulk there instead of
	// in Content is carrying the same oversized text, so it is refused the same way
	// and stores nothing.
	if err := db.AppendArchive(core.DefaultAgentID, key, core.ArchiveSlot{
		Kind: core.KindEvent, EventType: strings.Repeat("n", content.MaxEventPayload),
		Content: "x", CreatedAt: 1,
	}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an over-budget event name must be refused with ErrInvalidQuery, got %v", err)
	}
	events, err = db.eventsOf(core.DefaultAgentID, key)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("a refused append must store nothing, got %d events", len(events))
	}
}

// Dream drops content past the retention window even with nothing to
// consolidate: a turn's expired event goes while its fresh one stays, and a turn
// whose every event expired reads back empty.
func TestDreamPrunesExpiredEvents(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	a, b := common.FormatHash(11), common.FormatHash(22)
	fresh := time.Now().Add(-time.Hour).UnixMilli()
	appendOne := func(id string, ts int64) {
		if err := db.AppendArchive(core.DefaultAgentID, id, ev("llm_request", ts)); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	appendOne(a, 100)
	appendOne(a, fresh)
	appendOne(b, 500)

	if events, err := db.eventsOf(core.DefaultAgentID, a); err != nil || len(events) != 2 {
		t.Fatalf("both of a's events are inside the window: %+v err=%v", events, err)
	}

	// No active scenes, so the consolidation stages return early — the two
	// pruning stages run unconditionally and are what this exercises.
	if _, err := db.RunDream(context.Background(), core.DefaultAgentID, 0); err != nil {
		t.Fatalf("dream: %v", err)
	}
	events, err := db.eventsOf(core.DefaultAgentID, a)
	if err != nil || len(events) != 1 || events[0].CreatedAt != fresh {
		t.Fatalf("only a's fresh event survives: %+v err=%v", events, err)
	}
	events, err = db.eventsOf(core.DefaultAgentID, b)
	if err != nil || len(events) != 0 {
		t.Fatalf("pruned session must read empty: %+v err=%v", events, err)
	}
}

// A turn that only ever spoke owns its two originals and holds no events: the
// content index carries both kinds, so the event read has to be the one that
// filters.
func TestDialogueOnlyTurnHoldsNoEvents(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	appendTurn(t, db, topicID, 1000)
	if err := settle(db, sceneID, topicID); err != nil {
		t.Fatalf("update: %v", err)
	}
	if owned := archivesOfTopic(t, db.engine, topicID); len(owned) != 2 {
		t.Fatalf("the settled turn should own its two originals, got %d", len(owned))
	}
	events, err := db.eventsOf(core.DefaultAgentID, common.FormatHash(topicID))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("a dialogue-only turn reported events: %+v", events)
	}
}

func TestTrajectorySeqContinuesAfterContextRebuild(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	session := common.FormatHash(77)
	for i := 1; i <= 2; i++ {
		if err := db.AppendArchive(core.DefaultAgentID, session, ev("llm_request", int64(i))); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// Simulate the idle sweep dropping the agent context: the next access
	// must rebuild the content index from records and continue Seq.
	delete(db.agents, core.DefaultAgentID)
	if err := db.AppendArchive(core.DefaultAgentID, session, ev("tool_call", 3)); err != nil {
		t.Fatalf("append after rebuild: %v", err)
	}
	events, err := db.eventsOf(core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 3 || events[2].Seq != 5 {
		t.Fatalf("seq must continue after context rebuild: %+v", events)
	}
}

// A tree starts empty and a step's ordinal is the library's to hand out: the first
// create of a turn is step 1 and each step after it is one higher, so a host can
// address what it created without reading the tree back.
func TestPlanCreateHandsOutOrdinals(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	first, err := db.PlanCreate(core.DefaultAgentID, pid, "第一步")
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 {
		t.Fatalf("a turn's first step = %d, want 1", first)
	}
	if second := add(t, db, pid, 0, "第二步"); second != 2 {
		t.Fatalf("second root = %d, want 2", second)
	}
	if child := add(t, db, pid, 2, "子步"); child != 3 {
		t.Fatalf("a child continues the same count: %d, want 3", child)
	}
	// A second turn counts from its own start: ordinals are per tree, not global.
	other := common.FormatHash(10)
	if got, err := db.PlanCreate(core.DefaultAgentID, other, "别的轮"); err != nil || got != 1 {
		t.Fatalf("another turn's first step = %d/%v, want 1", got, err)
	}

	restate(t, db, pid, first, PlanDone, "made it")
	node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, 1))
	if err != nil {
		t.Fatal(err)
	}
	if node.Status != core.StatusDone || node.Summary != "made it" {
		t.Fatalf("step restatement lost: %+v", node)
	}
}

// A step is created in progress with no status to state, and it keeps its own
// creation time while a later update moves only the update time.
func TestPlanNodeCreateStampsTimes(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	pid := common.FormatHash(9)
	seq, err := db.PlanCreate(core.DefaultAgentID, pid, "r")
	if err != nil {
		t.Fatal(err)
	}
	node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, seq))
	if err != nil {
		t.Fatal(err)
	}
	if node.Status != core.StatusInProgress {
		t.Fatalf("a fresh step starts as %d, want in progress", node.Status)
	}
	if node.CreatedAt == 0 || node.UpdatedAt != node.CreatedAt {
		t.Fatalf("a created step carries one timestamp pair: %+v", node)
	}
	restate(t, db, pid, seq, PlanDone, "fin")
	aged, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, seq))
	if err != nil {
		t.Fatal(err)
	}
	if aged.CreatedAt != node.CreatedAt {
		t.Fatalf("an update moved CreatedAt: %d -> %d", node.CreatedAt, aged.CreatedAt)
	}
	if aged.FinishedAt == 0 {
		t.Fatal("a step driven to done carries a completion time")
	}
}

// Updating one step reaches no other: there is no whole-tree restatement whose
// omissions a host must reason about, so the only step that moves is the one named.
func TestPlanNodeUpdateLeavesOtherStepsAlone(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	first := add(t, db, pid, 0, "一")
	second := add(t, db, pid, 0, "二")
	restate(t, db, pid, second, PlanDone, "keep me")
	restate(t, db, pid, first, PlanDone, "first")

	node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, second))
	if err != nil {
		t.Fatalf("the other step is gone: %v", err)
	}
	if node.Status != core.StatusDone || node.Summary != "keep me" {
		t.Fatalf("an untouched step was restated: %+v", node)
	}
}

// Every refusal the plan write face makes is a whole refusal: nothing lands, so a
// host never has to diff its own writes against the store to find what applied.
func TestPlanWritesRefuseWithoutLeavingTrace(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	seq := add(t, db, pid, 0, "一")

	if _, err := db.PlanNodeAdd(core.DefaultAgentID, pid, 77, "挂在没有的步骤下"); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("a step under an unknown parent: want ErrNotFound, got %v", err)
	}
	if err := db.PlanNodeUpdate(core.DefaultAgentID, pid,
		PlanStep{Seq: seq, Status: PlanStatus("finished")}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("unknown status: want ErrInvalidQuery, got %v", err)
	}
	if err := db.PlanNodeUpdate(core.DefaultAgentID, pid,
		PlanStep{Seq: seq}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("blank status: want ErrInvalidQuery, got %v", err)
	}
	if err := db.PlanNodeUpdate(core.DefaultAgentID, pid,
		PlanStep{Seq: 77, Status: PlanDone}); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("updating a step nobody created: want ErrNotFound, got %v", err)
	}
	// The one step this test created is still the only record on disk, and still
	// in progress: every refusal above left the tree exactly as it found it.
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL5PlanNode); n != 1 {
		t.Fatalf("refused writes left %d plan nodes, want 1", n)
	}
	node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, seq))
	if err != nil || node.Status != core.StatusInProgress {
		t.Fatalf("a refused update moved a step: %+v err=%v", node, err)
	}
}

// An event binds to a step the host created; it never grows the tree. Naming a
// step the plan does not hold is the plan and the record disagreeing, so the
// append reports that instead of quietly inventing a step.
func TestEventBindsOnlyToACreatedStep(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	if err := db.AppendArchive(core.DefaultAgentID, pid, onStep(ev("llm_request", 1000), 3)); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an event on a step that does not exist: want ErrInvalidQuery, got %v", err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL5PlanNode); n != 0 {
		t.Fatalf("the refused append created %d nodes", n)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive); n != 0 {
		t.Fatalf("the refused append stored %d content records", n)
	}

	seq := add(t, db, pid, 0, "一步")
	if err := db.AppendArchive(core.DefaultAgentID, pid, onStep(ev("llm_request", 1001), seq)); err != nil {
		t.Fatalf("binding to a created step: %v", err)
	}
	events := stepEvents(t, db, 9, seq)
	if len(events) != 1 || events[0].EventType != "llm_request" {
		t.Fatalf("want 1 llm_request event on the step, got %+v", events)
	}
}

// A step's read covers its branch: once a step is split, the work it did is
// attributed to the children, so "what did this step do" that answers only for the
// parent's own records is a partial answer.
func TestStepReadCoversItsSubtree(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	root := add(t, db, pid, 0, "调研")
	child := add(t, db, pid, root, "读码")
	grandchild := add(t, db, pid, child, "改码")
	sibling := add(t, db, pid, 0, "别的活")

	for _, seq := range []uint32{root, child, grandchild, sibling} {
		if err := db.AppendArchive(core.DefaultAgentID, pid, onStep(ev("tool_call", int64(1000+seq)), seq)); err != nil {
			t.Fatalf("append on step %d: %v", seq, err)
		}
	}
	kind := core.KindEvent
	topic := pid
	inRoot, err := db.SearchL4(core.DefaultAgentID, L4Query{TopicID: &topic, Kind: &kind, NodeSeq: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(inRoot) != 3 {
		t.Fatalf("step %d's read covers %d records, want its whole branch (3)", root, len(inRoot))
	}
	inChild, err := db.SearchL4(core.DefaultAgentID, L4Query{TopicID: &topic, Kind: &kind, NodeSeq: child})
	if err != nil {
		t.Fatal(err)
	}
	if len(inChild) != 2 || inChild[1].NodeSeq != grandchild {
		t.Fatalf("a step's branch = %+v, want itself and its child", inChild)
	}
	// A leaf reads only its own, and the other root's work stays out of it.
	inLeaf, err := db.SearchL4(core.DefaultAgentID, L4Query{TopicID: &topic, Kind: &kind, NodeSeq: grandchild})
	if err != nil {
		t.Fatal(err)
	}
	if len(inLeaf) != 1 || inLeaf[0].NodeSeq != grandchild {
		t.Fatalf("a leaf's read = %+v, want just its own record", inLeaf)
	}
	// A step filter without the turn it lives inside addresses nothing, so it is
	// refused rather than answered with a domain-wide scan.
	if _, err := db.SearchL4(core.DefaultAgentID, L4Query{NodeSeq: root}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("a step filter with no turn: want ErrInvalidQuery, got %v", err)
	}
}

// Forest contract: two top-level steps yield two roots, the nesting a host created
// comes back as Children, and Done/Total covers both subtrees.
func TestPlanStateForestMultipleRoots(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	r1 := add(t, db, topicID, 0, "step one")
	r2 := add(t, db, topicID, 0, "two")
	sub := add(t, db, topicID, r2, "sub")
	restate(t, db, topicID, r1, PlanDone, "step one")
	restate(t, db, topicID, sub, PlanDone, "sub")

	tree, err := db.PlanState(core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Roots) != 2 {
		t.Fatalf("want 2 roots, got %d", len(tree.Roots))
	}
	if tree.Roots[0].Seq != r1 || tree.Roots[1].Seq != r2 {
		t.Fatalf("roots must be creation-ordered: %+v", tree.Roots)
	}
	if tree.Roots[1].ParentSeq != 0 {
		t.Fatalf("a root must say it has no parent, got %d", tree.Roots[1].ParentSeq)
	}
	if len(tree.Roots[1].Children) != 1 ||
		tree.Roots[1].Children[0].Seq != sub || tree.Roots[1].Children[0].ParentSeq != r2 {
		t.Fatalf("second root lost its subtree: %+v", tree.Roots[1])
	}
	if tree.TotalCount != 3 || tree.DoneCount != 2 {
		t.Fatalf("forest stats total=%d done=%d, want 3/2", tree.TotalCount, tree.DoneCount)
	}
	// A title the host never gave falls back to the ordinal rather than going blank.
	nameless := add(t, db, topicID, 0, "")
	if tree := mustTree(t, db, topicID); !slices.ContainsFunc(tree.Roots,
		func(r PlanNodeView) bool { return r.Seq == nameless && r.Title == "4" }) {
		t.Fatalf("an untitled step must render by its ordinal: %+v", tree.Roots)
	}
}

func mustTree(t *testing.T, db *DB, topicID string) *PlanTree {
	t.Helper()
	tree, err := db.PlanState(core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatalf("PlanState(%s): %v", topicID, err)
	}
	return tree
}

// A child whose parent record expired still reads back as a root with its own
// subtree: an unresolved parent link must not hide the work the tree still holds.
func TestPlanStateOrphansSurfaceAsRoots(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	topicID := common.FormatHash(9)
	parent := add(t, db, topicID, 0, "父")
	child := add(t, db, topicID, parent, "子")
	parentID := core.HashPlanNode(9, parent)
	if err := repo.DeletePlanNodesByIDs(db.engine, core.DefaultAgentID,
		[]uint64{parentID}); err != nil {
		t.Fatal(err)
	}
	db.agents[core.DefaultAgentID].Plans.RemoveNodes(9, []uint64{parentID})

	tree := mustTree(t, db, topicID)
	if len(tree.Roots) != 1 || tree.Roots[0].Seq != child {
		t.Fatalf("the orphaned child must surface as a root: %+v", tree.Roots)
	}
}

// Model A: a parent becomes Done only where the host says so, and the bottom-up
// rollup of settled children's summaries fills an empty parent Summary without
// ever overwriting one the host wrote.
func TestPlanRollupModelA(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))

	partial := common.FormatHash(9)
	root := add(t, db, partial, 0, "父")
	a := add(t, db, partial, root, "子A")
	b := add(t, db, partial, root, "子B")
	// One child still open: the parent is not Done and the counts say so.
	restate(t, db, partial, a, PlanDone, "step A")
	if tree := mustTree(t, db, partial); tree.Roots[0].Status == PlanDone ||
		tree.TotalCount != 3 || tree.DoneCount != 1 {
		t.Fatalf("a partially done parent was folded: %+v", tree)
	}
	// Every child settled still leaves the parent as the host left it.
	restate(t, db, partial, b, PlanDone, "step B")
	if tree := mustTree(t, db, partial); tree.Roots[0].Status != PlanInProgress {
		t.Fatalf("parent auto-folded without a host declaration: %+v", tree.Roots[0])
	}
	// The host declares the parent Done with a blank Summary → children fold up.
	restate(t, db, partial, root, PlanDone, "")
	if tree := mustTree(t, db, partial); tree.DoneCount != 3 ||
		tree.Roots[0].Summary != "step A; step B" {
		t.Fatalf("rollup into a blank parent summary: %+v", tree.Roots[0])
	}

	// A summary the host wrote on the parent survives the rollup.
	own := common.FormatHash(6)
	ownRoot := add(t, db, own, 0, "父")
	ownA := add(t, db, own, ownRoot, "a")
	ownB := add(t, db, own, ownRoot, "b")
	restate(t, db, own, ownA, PlanDone, "step A")
	restate(t, db, own, ownB, PlanDone, "step B")
	restate(t, db, own, ownRoot, PlanDone, "parent's own words")
	if tree := mustTree(t, db, own); tree.Roots[0].Summary != "parent's own words" {
		t.Fatalf("rollup overwrote the host summary: %+v", tree.Roots[0])
	}
}

// A fold taken while a child is still open is a partial answer wearing a
// finished one's clothes, so the parent waits for every branch to settle — and a
// failed child settles its branch just as a done one does.
func TestPlanRollupWaitsForEveryChild(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	topicID := common.FormatHash(4)
	root := add(t, db, topicID, 0, "root")
	c1 := add(t, db, topicID, root, "c1")
	c2 := add(t, db, topicID, root, "c2")
	restate(t, db, topicID, root, PlanDone, "")
	restate(t, db, topicID, c1, PlanDone, "settled")

	if tree := mustTree(t, db, topicID); tree.Roots[0].Summary != "" {
		t.Fatalf("a parent with an open child was folded: %+v", tree.Roots[0])
	}
	restate(t, db, topicID, c2, PlanFailed, "gave up")
	if got := mustTree(t, db, topicID).Roots[0]; got.Summary != "settled; gave up" {
		t.Fatalf("fold once every child settled = %q", got.Summary)
	}
}

// Retention semantics: a plan node ages on its own clock and takes nothing with
// it. An expired tree is swept while the turn's events stay readable; an in-flight
// plan keeps even its stale nodes, and events arriving on a dead tree no longer
// hold that tree alive.
func TestDreamPrunePlanNodesAndContent(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	old := time.Now().Add(-dream.ContentRetention - time.Hour).UnixMilli()
	now := time.Now().UnixMilli()
	age := func(topicID uint64, seq uint32) {
		t.Helper()
		node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(topicID, seq))
		if err != nil {
			t.Fatalf("read node for aging: %v", err)
		}
		node.UpdatedAt = old
		if err := repo.WritePlanNode(db.engine, core.DefaultAgentID, node); err != nil {
			t.Fatal(err)
		}
	}

	// All-Done plan created long ago, with a FRESH event bound to the step.
	doneID := common.FormatHash(9)
	doneStep := add(t, db, doneID, 0, "fin")
	restate(t, db, doneID, doneStep, PlanDone, "fin")
	if err := db.AppendArchive(core.DefaultAgentID, doneID, onStep(ev("note", now), doneStep)); err != nil {
		t.Fatal(err)
	}
	age(9, doneStep)

	// In-flight plan: an aged Done root plus a child created just now. The tree is
	// exempt as a whole, so the stale root survives with it.
	liveID := common.FormatHash(8)
	liveRoot := add(t, db, liveID, 0, "root")
	restate(t, db, liveID, liveRoot, PlanDone, "root")
	add(t, db, liveID, liveRoot, "child")
	age(8, liveRoot)

	// Abandoned plan: not done, and nothing written inside the window.
	staleID := common.FormatHash(7)
	staleStep := add(t, db, staleID, 0, "half")
	age(7, staleStep)

	if _, err := db.RunDream(context.Background(), core.DefaultAgentID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ReadPlanNode(db.engine, core.DefaultAgentID,
		core.HashPlanNode(9, doneStep)); err == nil {
		t.Fatal("expired all-done plan node should be pruned")
	}
	if _, err := core.ReadPlanNode(db.engine, core.DefaultAgentID,
		core.HashPlanNode(7, staleStep)); err == nil {
		t.Fatal("a plan silent past the window is abandoned and must be pruned")
	}
	if _, err := core.ReadPlanNode(db.engine, core.DefaultAgentID,
		core.HashPlanNode(8, liveRoot)); err != nil {
		t.Fatalf("an in-flight plan must keep even its stale root: %v", err)
	}
	// The swept node cascades nothing: the event bound to it is content, ages on
	// its own clock, and is still readable.
	events, err := db.eventsOf(core.DefaultAgentID, doneID)
	if err != nil {
		t.Fatalf("the turn's event track must survive its own pruned tree: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "note" {
		t.Fatalf("want the fresh note event only, got %+v", events)
	}
}

// An event append is forced to content-of-kind-event semantics: the topic it belongs
// to and the slot it lands in are the library's, and so are the speaker and the
// medium an event has no use for — an append cannot smuggle a record into the
// transcript. The kinds also cannot wear each other's axes.
func TestAppendEventCannotForgeContentFields(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	topicID := common.FormatHash(9)
	seq := add(t, db, topicID, 0, "一步")
	if err := db.AppendArchive(core.DefaultAgentID, topicID, onStep(core.ArchiveSlot{
		Kind: core.KindEvent, TopicID: 4242,
		Role: core.RoleDream, ContentType: core.ContentVideo,
		EventType: "llm_request", Content: "payload", CreatedAt: 1000,
	}, seq)); err != nil {
		t.Fatal(err)
	}
	// The content write left the tree exactly where the create put it: one step,
	// still in progress. An append that could advance or add a step would make the
	// plan a second record of what happened instead of the host's intent.
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL5PlanNode); n != 1 {
		t.Fatalf("plan nodes = %d, want only the created one", n)
	}
	node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, seq))
	if err != nil {
		t.Fatalf("the created step vanished: %v", err)
	}
	if node.Status != core.StatusInProgress {
		t.Fatalf("an event append advanced its step: %d", node.Status)
	}

	var landed *core.ArchiveSlot
	for _, arc := range core.CollectAllArchives(db.engine, core.DefaultAgentID) {
		if arc.Kind == core.KindEvent {
			clone := arc
			landed = &clone
		}
	}
	if landed == nil {
		t.Fatal("the event record is missing")
	}
	if landed.TopicID != 9 || landed.IDHash != core.HashContent(9, landed.Seq) {
		t.Fatalf("an append must land under the topic it addressed: %+v", landed)
	}
	if landed.Seq != core.LastUtteranceSeq+1 {
		t.Fatalf("forged Seq survived: %d", landed.Seq)
	}
	if landed.Role != 0 || landed.ContentType != core.ContentText {
		t.Fatalf("forged role or medium survived: %+v", landed)
	}
	if landed.NodeSeq != seq {
		t.Fatalf("event must carry the step it actually bound to, got %d", landed.NodeSeq)
	}

	// The axes stay apart: an utterance that names an event, hangs on a step, or
	// claims the consolidation role is refused outright, and so is a record whose
	// kind nobody can name.
	for name, slot := range map[string]core.ArchiveSlot{
		"utterance with an event name": {Kind: core.KindUtterance, Role: core.RoleUser, EventType: "tool_call", Content: "x", CreatedAt: 1},
		"utterance on a plan step":     {Kind: core.KindUtterance, Role: core.RoleUser, NodeSeq: 1, Content: "x", CreatedAt: 1},
		"host-written dream role":      {Kind: core.KindUtterance, Role: core.RoleDream, Content: "x", CreatedAt: 1},
		"undefined kind":               {Kind: core.ArchiveKind(7), EventType: "tool_call", Content: "x", CreatedAt: 1},
		"empty content":                {Kind: core.KindEvent, EventType: "tool_call", CreatedAt: 1},
	} {
		if err := db.AppendArchive(core.DefaultAgentID, topicID, slot); common.CodeOf(err) != common.ErrInvalidQuery {
			t.Fatalf("%s: want ErrInvalidQuery, got %v", name, err)
		}
	}
}

// Seq is one space a topic shares between its originals and its events, so events
// appended while a turn runs are still there after its dialogue lands: the originals
// take Seq 1 and 2 and never reach down into the event range.
func TestSettledTurnKeepsEventsAppendedBeforeIt(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)

	for i, name := range []string{"llm_request", "tool_call"} {
		if err := db.AppendArchive(core.DefaultAgentID, common.FormatHash(topicID), ev(name, int64(100+i))); err != nil {
			t.Fatalf("append %s: %v", name, err)
		}
	}
	appendTurn(t, db, topicID, 1000)
	if err := settle(db, sceneID, topicID); err != nil {
		t.Fatalf("update: %v", err)
	}

	events, err := db.eventsOf(core.DefaultAgentID, common.FormatHash(topicID))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("settling the turn cost %d event(s): %+v", 2-len(events), events)
	}
	owned := archivesOfTopic(t, db.engine, topicID)
	if len(owned) != 4 {
		t.Fatalf("topic owns %d records, want 2 originals + 2 events", len(owned))
	}
	// archivesOfTopic is a deliberate index-free record scan, so it yields map
	// order: what this pins is **which slots exist**, not the order they come back
	// in. Read-path ordering is the index's contract and is pinned where it is
	// actually owed — TestQueryArchivesL4OrdersBySeqNotTimestamp (repo) and
	// TestSceneContextTopicOrdersBySeqNotWriteOrder (scene).
	var utterances, eventSeqs []uint64
	for _, arc := range owned {
		if arc.Kind == core.KindEvent {
			eventSeqs = append(eventSeqs, arc.Seq)
			continue
		}
		utterances = append(utterances, arc.Seq)
	}
	slices.Sort(utterances)
	slices.Sort(eventSeqs)
	if len(utterances) != 2 || utterances[0] != core.SeqUser || utterances[1] != core.SeqAgent {
		t.Fatalf("originals must hold Seq 1 and 2: %v", utterances)
	}
	if len(eventSeqs) != 2 || eventSeqs[0] != 3 || eventSeqs[1] != 4 {
		t.Fatalf("events must sit above the utterance slots: %v", eventSeqs)
	}
}

// A step event names itself: any EventType a bare turn event takes is accepted
// here too and stored verbatim, and the content contract is still checked before
// anything lands on the tree.
func TestPlanEventNamesAreHostOwned(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	seq := add(t, db, topicID, 0, "一步")
	for i, name := range []string{"sandbox_ask", "host_step"} {
		if err := db.AppendArchive(core.DefaultAgentID, topicID,
			onStep(ev(name, int64(1000+i)), seq)); err != nil {
			t.Fatalf("a host-named plan event must be accepted: %v", err)
		}
	}
	events, err := db.eventsOf(core.DefaultAgentID, topicID)
	if err != nil || len(events) != 2 {
		t.Fatalf("plan events: %+v err=%v", events, err)
	}
	if events[0].EventType != "sandbox_ask" || events[1].EventType != "host_step" {
		t.Fatalf("the engine rewrote the host's event names: %q %q",
			events[0].EventType, events[1].EventType)
	}

	if err := db.AppendArchive(core.DefaultAgentID, topicID, onStep(core.ArchiveSlot{Kind: core.KindEvent, Content: "step", CreatedAt: 1002}, 9)); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("empty event type: want ErrInvalidQuery, got %v", err)
	}
	if tree := mustTree(t, db, topicID); tree.TotalCount != 1 {
		t.Fatalf("a refused append built a step: total=%d", tree.TotalCount)
	}
}

func TestPlanCache_ConsistentWithDisk(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	root := add(t, db, topicID, 0, "r")
	child := add(t, db, topicID, root, "a")
	restate(t, db, topicID, child, PlanDone, "s")
	if err := db.AppendArchive(core.DefaultAgentID, topicID, onStep(ev("tool_call", 1200), child)); err != nil {
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
	diskAggs, err := repo.CollectPlanNodes(db.engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("collect plan nodes: %v", err)
	}
	var disk *repo.PlanAggregate
	for _, agg := range diskAggs {
		if agg.TopicID == 9 {
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
	if cached.HasNonDone != disk.HasNonDone {
		t.Fatalf("HasNonDone mismatch: cached=%v disk=%v", cached.HasNonDone, disk.HasNonDone)
	}
}

func TestPlanNodeUpdateFinishedAt(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	seq := add(t, db, topicID, 0, "一步")
	restate(t, db, topicID, seq, PlanDone, "fin")

	first := mustTree(t, db, topicID).Roots[0].FinishedAt
	if first == 0 {
		t.Fatal("a step driven to a terminal status must carry a completion time")
	}
	// Re-opening the step clears it. FinishedAt answers "when did this step
	// finish", and a step the host re-opened has not finished — keeping the earlier
	// stamp would hand back a completed-looking node that the same tree says is
	// still running.
	restate(t, db, topicID, seq, PlanInProgress, "")
	if got := mustTree(t, db, topicID).Roots[0].FinishedAt; got != 0 {
		t.Fatalf("re-opening a step must clear FinishedAt: %d -> %d", first, got)
	}
	// Finishing it again is a new completion, so it carries a new time rather
	// than the stamp of the one the host withdrew.
	restate(t, db, topicID, seq, PlanDone, "fin2")
	if got := mustTree(t, db, topicID).Roots[0].FinishedAt; got < first {
		t.Fatalf("a re-completed step lost its completion time: %d", got)
	}
}

// One turn runs on one id: the topic Search opened is where the host's events and
// dialogue land, what Update distills, and what an L4 read under a Kind condition
// returns — no host-minted turn key and no timestamp derivation anywhere in
// between.
func TestTurnRunsOnOneTopicID(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)

	res, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	turnID := common.FormatHash(res.NewTopicID)

	for _, name := range []string{"llm_request", "tool_call"} {
		if err := db.AppendArchive(core.DefaultAgentID, turnID, ev(name, 1000)); err != nil {
			t.Fatalf("append %s: %v", name, err)
		}
	}
	settled := res.NewTopicID
	appendTurn(t, db, settled, 1000)
	if err := settle(db, res.Scene.SceneID, settled); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := core.ReadTopicLenient(db.engine, core.DefaultAgentID, settled); err != nil {
		t.Fatalf("the turn topic is not readable: %v", err)
	}
	events, err := db.eventsOf(core.DefaultAgentID, turnID)
	if err != nil {
		t.Fatalf("read trajectory: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want the turn's 2", len(events))
	}
	for _, e := range events {
		if e.TopicID != settled {
			t.Fatalf("event %s landed under key %d, want the turn's %d", e.EventType, e.TopicID, settled)
		}
	}
}
