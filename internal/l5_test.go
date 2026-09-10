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

// onNode names the plan step an event belongs to: the path goes on the record, and
// a step missing along it is what creates the step.
func onNode(slot core.ArchiveSlot, nodePath string) core.ArchiveSlot {
	slot.NodePath = nodePath
	return slot
}

// eventsOf reads one topic's event track the way a host does: the same key with the
// kind condition, in Seq order.
func (db *DB) eventsOf(agentID uint64, topicHex string) ([]core.ArchiveSlot, error) {
	kind := core.KindEvent
	return db.SearchL4(agentID, L4Query{TopicID: &topicHex, Kind: &kind})
}

// nodeEvents reads the events bound to one node path from the topic's content
// track, Seq ascending. A node holds no list of its events: the path is stamped on
// the event, and that is all the attribution a reader needs.
func nodeEvents(t *testing.T, db *DB, topicID uint64, nodePath string) []core.ArchiveSlot {
	t.Helper()
	var out []core.ArchiveSlot
	for _, arc := range core.CollectAllArchives(db.engine, core.DefaultAgentID) {
		if arc.TopicID == topicID && arc.Kind == core.KindEvent && arc.NodePath == nodePath {
			out = append(out, arc)
		}
	}
	slices.SortFunc(out, func(a, b core.ArchiveSlot) int { return cmp.Compare(a.Seq, b.Seq) })
	return out
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
	if err := db.AppendArchive(core.DefaultAgentID, common.FormatHash(1), onNode(bare, "1")); common.CodeOf(err) != common.ErrInvalidQuery {
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
	// exactly at the budget still writes
	if err := db.AppendArchive(core.DefaultAgentID, key, core.ArchiveSlot{
		Kind: core.KindEvent, EventType: "tool_call", Content: strings.Repeat("x", content.MaxEventPayload), CreatedAt: 1,
	}); err != nil {
		t.Fatalf("payload at the budget limit should append: %v", err)
	}
}

func TestListAndDreamPruneTrajectorySessions(t *testing.T) {
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
	if sum := byID[a]; sum.Events != 2 || sum.LastAppendAt != fresh {
		t.Fatalf("session a summary mismatch: %+v", sum)
	}
	if sum := byID[b]; sum.Events != 1 || sum.LastAppendAt != 500 {
		t.Fatalf("session b summary mismatch: %+v", sum)
	}

	// Dream drops content older than the 7-day retention window even when
	// there is nothing to consolidate (no active scenes → early return).
	if _, err := db.RunDream(context.Background(), core.DefaultAgentID, 0); err != nil {
		t.Fatalf("dream: %v", err)
	}
	list, err = db.ListTrajectorySessions(core.DefaultAgentID)
	if err != nil || len(list) != 1 || list[0].SessionID != a || list[0].Events != 1 {
		t.Fatalf("only session a's fresh event survives: %+v err=%v", list, err)
	}
	events, err := db.eventsOf(core.DefaultAgentID, b)
	if err != nil || len(events) != 0 {
		t.Fatalf("pruned session must read empty: %+v err=%v", events, err)
	}
}

// A turn that only ever spoke has no trajectory. Listing it would tell a host the
// turn recorded operations it never did — the content index carries both kinds, so
// the event tally has to be the one that filters.
func TestListTrajectorySessionsIgnoresDialogueOnlyTurn(t *testing.T) {
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
	list, err := db.ListTrajectorySessions(core.DefaultAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("a dialogue-only turn was reported as having a trajectory: %+v", list)
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

func TestPlanSetRestatesNode(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	if err := db.PlanSet(core.DefaultAgentID, pid, []PlanStep{
		{NodePath: "1", Status: PlanDone, Summary: "made it"}}); err != nil {
		t.Fatal(err)
	}
	node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, "1"))
	if err != nil {
		t.Fatal(err)
	}
	if node.Status != core.StatusDone {
		t.Fatalf("want done, got %d", node.Status)
	}
	if node.Summary != "made it" {
		t.Fatalf("want made it, got %s", node.Summary)
	}
}

// A declaration says what it lists and nothing else. Leaving a step out is not
// how the host withdraws it — the library would have to read a partial
// restatement and a complete one the same way — so the unlisted node keeps its
// stored state and withdrawing is done by declaring a new turn's tree.
func TestPlanSetLeavesUndeclaredNodesAlone(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	if err := db.PlanSet(core.DefaultAgentID, pid, []PlanStep{
		{NodePath: "1", Status: PlanInProgress}, {NodePath: "2", Status: PlanDone, Summary: "keep me"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanSet(core.DefaultAgentID, pid, []PlanStep{
		{NodePath: "1", Status: PlanDone, Summary: "first"}}); err != nil {
		t.Fatal(err)
	}
	node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, "2"))
	if err != nil {
		t.Fatalf("the unlisted node is gone: %v", err)
	}
	if node.Status != core.StatusDone || node.Summary != "keep me" {
		t.Fatalf("an unlisted step was restated: %+v", node)
	}
}

// A declaration is refused as a whole: nothing it names lands on the tree, so a
// host never has to diff its own plan against the store to find what applied.
func TestPlanSetRefusesAmbiguousDeclaration(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	refused := map[string][]PlanStep{
		"same step twice": {{NodePath: "1", Status: PlanDone}, {NodePath: "1", Status: PlanPending}},
		"blank segment":   {{NodePath: "1..2", Status: PlanDone}},
		"empty path":      {{Status: PlanDone}},
		"unknown status":  {{NodePath: "1", Status: PlanStatus("finished")}},
		"blank status":    {{NodePath: "1"}},
	}
	for name, steps := range refused {
		if err := db.PlanSet(core.DefaultAgentID, pid, steps); common.CodeOf(err) != common.ErrInvalidQuery {
			t.Fatalf("%s: want ErrInvalidQuery, got %v", name, err)
		}
	}
	if _, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, "1")); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("a refused declaration left a node behind: %v", err)
	}
}

// An event binds to a step the host planned; it never grows the tree. Naming a
// step the plan never declared is the plan and the record disagreeing, so the
// append reports that instead of quietly inventing a step.
func TestEventBindsOnlyToADeclaredStep(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	if err := db.AppendArchive(core.DefaultAgentID, pid, onNode(ev("llm_request", 1000), "1.2.1")); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an event on an undeclared step: want ErrInvalidQuery, got %v", err)
	}
	if _, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, "1.2.1")); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("the refused append created a node: %v", err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL4Archive); n != 0 {
		t.Fatalf("the refused append stored %d content records", n)
	}

	if err := db.PlanSet(core.DefaultAgentID, pid, []PlanStep{
		{NodePath: "1.2.1", Status: PlanInProgress}}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendArchive(core.DefaultAgentID, pid, onNode(ev("llm_request", 1001), "1.2.1")); err != nil {
		t.Fatalf("binding to a declared step: %v", err)
	}
	events := nodeEvents(t, db, 9, "1.2.1")
	if len(events) != 1 || events[0].EventType != "llm_request" {
		t.Fatalf("want 1 llm_request event on the step, got %+v", events)
	}
}

// Naming a deep path in a declaration is enough: the ancestors it implies are
// created as pending steps with the right ParentID.
func TestPlanSetBuildsParentChain(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.PlanSet(core.DefaultAgentID, common.FormatHash(9), []PlanStep{
		{NodePath: "1.2.1", Status: PlanInProgress}}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendArchive(core.DefaultAgentID, common.FormatHash(9), onNode(ev("llm_request", 1000), "1.2.1")); err != nil {
		t.Fatalf("an event on a step the declaration implied: %v", err)
	}
	rootID := core.HashPlanNode(9, "1")
	midID := core.HashPlanNode(9, "1.2")
	leafID := core.HashPlanNode(9, "1.2.1")
	root, _ := core.ReadPlanNode(db.engine, core.DefaultAgentID, rootID)
	mid, _ := core.ReadPlanNode(db.engine, core.DefaultAgentID, midID)
	leaf, _ := core.ReadPlanNode(db.engine, core.DefaultAgentID, leafID)
	if root.ParentID != 0 {
		t.Fatalf("root parent should be 0, got %d", root.ParentID)
	}
	if mid.ParentID != rootID {
		t.Fatalf("mid parent should be rootID %d, got %d", rootID, mid.ParentID)
	}
	if leaf.ParentID != midID {
		t.Fatalf("leaf parent should be midID %d, got %d", midID, leaf.ParentID)
	}
	// The leaf carries what the host declared; only the ancestors it implied are
	// left pending.
	if leaf.Status != core.StatusInProgress {
		t.Fatalf("leaf not restated: %+v", leaf)
	}
	if root.Status != core.StatusPending {
		t.Fatalf("an implied ancestor is not pending: %+v", root)
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

// Model A: a parent becomes Done only when the host declares it so, and the
// bottom-up rollup of settled children's summaries fills an empty parent
// Summary without ever overwriting one the host wrote.
func TestPlanSetRollupModelA(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	declare := func(topicID string, steps ...PlanStep) {
		t.Helper()
		if err := db.PlanSet(core.DefaultAgentID, topicID, steps); err != nil {
			t.Fatalf("declare %v: %v", steps, err)
		}
	}
	state := func(topicID string) *PlanTree {
		t.Helper()
		tree, err := db.PlanState(core.DefaultAgentID, topicID)
		if err != nil {
			t.Fatalf("PlanState(%s): %v", topicID, err)
		}
		return tree
	}

	partial := common.FormatHash(9)
	// One child still open: the parent is not Done and the counts say so.
	declare(partial, PlanStep{NodePath: "1", Status: PlanInProgress},
		PlanStep{NodePath: "1.1", Status: PlanDone, Summary: "step A"},
		PlanStep{NodePath: "1.2", Status: PlanPending})
	if tree := state(partial); tree.Roots[0].Status == PlanDone ||
		tree.TotalCount != 3 || tree.DoneCount != 1 {
		t.Fatalf("a partially done parent was folded: %+v", tree)
	}
	// Every child settled still leaves the parent as the host left it.
	declare(partial, PlanStep{NodePath: "1.2", Status: PlanDone, Summary: "step B"})
	if tree := state(partial); tree.Roots[0].Status != PlanInProgress {
		t.Fatalf("parent auto-folded without a host declaration: %+v", tree.Roots[0])
	}
	// The host declares the parent Done with a blank Summary → children fold up.
	declare(partial, PlanStep{NodePath: "1", Status: PlanDone})
	if tree := state(partial); tree.DoneCount != 3 ||
		tree.Roots[0].Summary != "step A; step B" {
		t.Fatalf("rollup into a blank parent summary: %+v", tree.Roots[0])
	}

	// A summary the host declared alongside the parent survives the rollup.
	own := common.FormatHash(6)
	declare(own, PlanStep{NodePath: "1.1", Status: PlanDone, Summary: "step A"},
		PlanStep{NodePath: "1.2", Status: PlanDone, Summary: "step B"},
		PlanStep{NodePath: "1", Status: PlanDone, Summary: "parent's own words"})
	if tree := state(own); tree.Roots[0].Summary != "parent's own words" {
		t.Fatalf("rollup overwrote the host summary: %+v", tree.Roots[0])
	}
}

// A fold taken while a child is still open is a partial answer wearing a
// finished one's clothes, so the parent waits for every branch to settle.
func TestPlanSetRollupWaitsForEveryChild(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	topicID := common.FormatHash(4)
	if err := db.PlanSet(core.DefaultAgentID, topicID, []PlanStep{
		{NodePath: "1", Status: PlanDone},
		{NodePath: "1.1", Status: PlanDone, Summary: "settled"},
		{NodePath: "1.2", Status: PlanInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Roots[0].Summary != "" {
		t.Fatalf("a parent with an open child was folded: %+v", tree.Roots[0])
	}
	if err := db.PlanSet(core.DefaultAgentID, topicID, []PlanStep{
		{NodePath: "1.2", Status: PlanFailed, Summary: "gave up"},
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.PlanState(core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Roots[0]; got.Summary != "settled; gave up" {
		t.Fatalf("fold once every child settled = %q", got.Summary)
	}
}

// Retention semantics after the merge: a plan node ages on its own clock and
// takes nothing with it. An expired tree is swept while the turn's events stay
// readable; an in-flight plan keeps even its stale nodes, and events arriving on
// a dead tree no longer hold that tree alive.
func TestDreamPrunePlanNodesAndContent(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	old := time.Now().Add(-dream.ContentRetention - time.Hour).UnixMilli()
	now := time.Now().UnixMilli()
	age := func(id uint64) {
		t.Helper()
		node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, id)
		if err != nil {
			t.Fatalf("read node for aging: %v", err)
		}
		node.UpdatedAt = old
		if _, err := repo.WritePlanNode(db.engine, core.DefaultAgentID, node); err != nil {
			t.Fatal(err)
		}
	}

	// All-Done plan declared long ago, with a FRESH event bound to the step.
	doneID := common.FormatHash(9)
	if err := db.PlanSet(core.DefaultAgentID, doneID, []PlanStep{
		{NodePath: "1", Status: PlanDone, Summary: "fin"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendArchive(core.DefaultAgentID, doneID, onNode(ev("note", now), "1")); err != nil {
		t.Fatal(err)
	}
	doneNode := core.HashPlanNode(9, "1")
	age(doneNode)

	// In-flight plan: an aged Done root plus a child declared just now. The
	// tree is exempt as a whole, so the stale root survives with it.
	liveID := common.FormatHash(8)
	if err := db.PlanSet(core.DefaultAgentID, liveID, []PlanStep{
		{NodePath: "1", Status: PlanDone, Summary: "root"},
		{NodePath: "1.1", Status: PlanInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	liveRoot := core.HashPlanNode(8, "1")
	age(liveRoot)

	// Abandoned plan: non-Done and nothing declared inside the window.
	staleID := common.FormatHash(7)
	if err := db.PlanSet(core.DefaultAgentID, staleID, []PlanStep{
		{NodePath: "1", Status: PlanInProgress}}); err != nil {
		t.Fatal(err)
	}
	staleNode := core.HashPlanNode(7, "1")
	age(staleNode)

	if _, err := db.RunDream(context.Background(), core.DefaultAgentID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, doneNode); err == nil {
		t.Fatal("expired all-done plan node should be pruned")
	}
	if _, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, staleNode); err == nil {
		t.Fatal("non-Done plan silent past the window is abandoned and must be pruned")
	}
	if _, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, liveRoot); err != nil {
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
	if err := db.PlanSet(core.DefaultAgentID, topicID, []PlanStep{
		{NodePath: "1", Status: PlanPending}}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendArchive(core.DefaultAgentID, topicID, onNode(core.ArchiveSlot{
		Kind: core.KindEvent, TopicID: 4242,
		Role: core.RoleDream, ContentType: core.ContentVideo,
		EventType: "llm_request", Content: "payload", CreatedAt: 1000,
	}, "1")); err != nil {
		t.Fatal(err)
	}
	// The content write left the tree exactly where the declaration put it: one
	// node, still pending. An append that could advance or add a step would make
	// the plan a second record of what happened instead of the host's intent.
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL5PlanNode); n != 1 {
		t.Fatalf("plan nodes = %d, want only the declared one", n)
	}
	node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, "1"))
	if err != nil {
		t.Fatalf("the declared node vanished: %v", err)
	}
	if node.Status != core.StatusPending {
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
	if landed.NodePath != "1" {
		t.Fatalf("event must carry the step it actually bound to, got %q", landed.NodePath)
	}

	// The axes stay apart: an utterance that names an event, hangs on a step, or
	// claims the consolidation role is refused outright, and so is a record whose
	// kind nobody can name.
	for name, slot := range map[string]core.ArchiveSlot{
		"utterance with an event name": {Kind: core.KindUtterance, Role: core.RoleUser, EventType: "tool_call", Content: "x", CreatedAt: 1},
		"utterance on a plan step":     {Kind: core.KindUtterance, Role: core.RoleUser, NodePath: "1", Content: "x", CreatedAt: 1},
		"host-written dream role":      {Kind: core.KindUtterance, Role: core.RoleDream, Content: "x", CreatedAt: 1},
		"undefined kind":               {Kind: core.ArchiveKind(7), EventType: "tool_call", Content: "x", CreatedAt: 1},
		"empty content":                {Kind: core.KindEvent, EventType: "tool_call", CreatedAt: 1},
	} {
		if err := db.AppendArchive(core.DefaultAgentID, topicID, slot); common.CodeOf(err) != common.ErrInvalidQuery {
			t.Fatalf("%s: want ErrInvalidQuery, got %v", name, err)
		}
	}
}

// Forest contract: two top-level steps yield two roots, and Done/Total
// covers both subtrees.
func TestPlanStateForestMultipleRoots(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	if err := db.PlanSet(core.DefaultAgentID, topicID, []PlanStep{
		{NodePath: "1", Status: PlanDone, Summary: "step one"},
		{NodePath: "2", Status: PlanInProgress},
		{NodePath: "2.1", Status: PlanDone, Summary: "sub"},
	}); err != nil {
		t.Fatal(err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, topicID)
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

// A step event names itself: any EventType a bare turn event takes is accepted
// here too and stored verbatim, and the content contract is still checked before
// anything lands on the tree.
func TestPlanEventNamesAreHostOwned(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	if err := db.PlanSet(core.DefaultAgentID, topicID, []PlanStep{
		{NodePath: "1", Status: PlanDone}}); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"sandbox_ask", "host_step"} {
		if err := db.AppendArchive(core.DefaultAgentID, topicID,
			onNode(ev(name, int64(1000+i)), "1")); err != nil {
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

	if err := db.AppendArchive(core.DefaultAgentID, topicID, onNode(core.ArchiveSlot{Kind: core.KindEvent, Content: "step", CreatedAt: 1002}, "2.1")); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("empty event type: want ErrInvalidQuery, got %v", err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatal(err)
	}
	if tree.TotalCount != 1 {
		t.Fatalf("a refused append built a node chain: total=%d", tree.TotalCount)
	}
}

func TestPlanCache_ConsistentWithDisk(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	if err := db.PlanSet(core.DefaultAgentID, topicID, []PlanStep{
		{NodePath: "1", Title: "r", Status: PlanPending},
		{NodePath: "1.1", Title: "a", Status: PlanDone, Summary: "s"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendArchive(core.DefaultAgentID, topicID, onNode(ev("tool_call", 1200), "1.1")); err != nil {
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
	for _, agg := range repo.CollectPlanNodes(db.engine, core.DefaultAgentID) {
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

func TestPlanSetFinishedAt(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	declare := func(status PlanStatus, summary string) {
		t.Helper()
		if err := db.PlanSet(core.DefaultAgentID, topicID, []PlanStep{
			{NodePath: "1", Status: status, Summary: summary}}); err != nil {
			t.Fatal(err)
		}
	}
	declare(PlanDone, "fin")
	tree, err := db.PlanState(core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatal(err)
	}
	first := tree.Roots[0].FinishedAt
	if first == 0 {
		t.Fatal("a declared terminal step must carry a completion time")
	}
	// Restating the step as work in progress clears it. FinishedAt answers "when
	// did this step finish", and a step the host re-opened has not finished —
	// keeping the earlier stamp would hand back a completed-looking node that the
	// same tree says is still running.
	declare(PlanInProgress, "")
	tree2, _ := db.PlanState(core.DefaultAgentID, topicID)
	if tree2.Roots[0].FinishedAt != 0 {
		t.Fatalf("re-opening a step must clear FinishedAt: %d -> %d", first, tree2.Roots[0].FinishedAt)
	}
	// Finishing it again is a new completion, so it carries a new time rather
	// than the stamp of the one the host withdrew.
	declare(PlanDone, "fin2")
	tree3, _ := db.PlanState(core.DefaultAgentID, topicID)
	if tree3.Roots[0].FinishedAt < first {
		t.Fatalf("a re-completed step lost its completion time: %d", tree3.Roots[0].FinishedAt)
	}
}

// One turn runs on one id: the topic Search opened is where the host's events and
// dialogue land, what Update distills, and what Crystallize reads back — no
// host-minted turn key and no timestamp derivation anywhere in between.
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
