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

// ev builds the event a host hands to an append: of an event, only these three
// fields are the host's to supply.
func ev(eventType string, ts int64) core.ArchiveSlot {
	return core.ArchiveSlot{EventType: eventType, Content: eventType, CreatedAt: ts}
}

// nodeEvents reads the events bound to one node path from the topic's content
// track, Seq ascending. A node holds no list of its events: the path is stamped on
// the event, and that is all the attribution a reader needs.
func nodeEvents(t *testing.T, db *DB, topicID uint64, nodePath string) []core.ArchiveSlot {
	t.Helper()
	var out []core.ArchiveSlot
	for _, arc := range core.CollectAllArchives(db.engine, core.DefaultAgentID) {
		if arc.ContextID == topicID && arc.Kind == core.KindEvent && arc.NodePath == nodePath {
			out = append(out, arc)
		}
	}
	slices.SortFunc(out, func(a, b core.ArchiveSlot) int { return cmp.Compare(a.Seq, b.Seq) })
	return out
}

func TestAppendTrajectorySeqAutoIncrement(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	session := common.FormatHash(99)
	for i := 1; i <= 3; i++ {
		if err := db.AppendTrajectory(core.DefaultAgentID, session, "", ev("llm_request", int64(i))); err != nil {
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
	// Seq 1 and 2 belong to the turn's two originals even before the turn is
	// settled, so an event appended mid-turn cannot be overwritten by the settle.
	for i, e := range events {
		if want := uint64(i) + core.LastUtteranceSeq + 1; e.Seq != want {
			t.Fatalf("seq[%d] = %d, want %d", i, e.Seq, want)
		}
	}
}

func TestAppendTrajectoryValidation(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	bare := core.ArchiveSlot{CreatedAt: 1}
	if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(1), "", bare); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("empty event type: want ErrInvalidQuery, got %v", err)
	}
	if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(1), "", core.ArchiveSlot{EventType: "tool_call"}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("zero timestamp: want ErrInvalidQuery, got %v", err)
	}
	// A plan-bound write is refused by the same contract, and the zero key is
	// refused before it: neither may create a node on its way out.
	if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(1), "1", bare); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("bound empty type: want ErrInvalidQuery, got %v", err)
	}
	if err := db.AppendTrajectory(core.DefaultAgentID, "0000000000000000", "", ev("x", 1)); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("reserved key: want ErrInvalidQuery, got %v", err)
	}
}

func TestAppendTrajectoryPayloadRefused(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	key := common.FormatHash(3)
	long := strings.Repeat("x", content.MaxEventPayload+100)
	if err := db.AppendTrajectory(core.DefaultAgentID, key, "", core.ArchiveSlot{
		EventType: "tool_call", Content: long, CreatedAt: 1,
	}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("an over-budget payload must be refused with ErrInvalidQuery, got %v", err)
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, key)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("a refused append must store nothing, got %d events", len(events))
	}
	// exactly at the budget still writes
	if err := db.AppendTrajectory(core.DefaultAgentID, key, "", core.ArchiveSlot{
		EventType: "tool_call", Content: strings.Repeat("x", content.MaxEventPayload), CreatedAt: 1,
	}); err != nil {
		t.Fatalf("payload at the budget limit should append: %v", err)
	}
}

func TestListAndDreamPruneTrajectorySessions(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	a, b := common.FormatHash(11), common.FormatHash(22)
	fresh := time.Now().Add(-time.Hour).UnixMilli()
	appendOne := func(id string, ts int64) {
		if err := db.AppendTrajectory(core.DefaultAgentID, id, "", ev("llm_request", ts)); err != nil {
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

	// Dream drops content older than the 7-day retention window even when
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

// A turn that only ever spoke has no trajectory. Listing it would tell a host the
// turn recorded operations it never did — the content index carries both kinds, so
// the event tally has to be the one that filters.
func TestListTrajectorySessionsIgnoresDialogueOnlyTurn(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)
	if _, err := db.Update(core.DefaultAgentID, turnOf(sceneID, topicID)); err != nil {
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
		if err := db.AppendTrajectory(core.DefaultAgentID, session, "", ev("llm_request", int64(i))); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// Simulate the idle sweep dropping the agent context: the next access
	// must rebuild the content index from records and continue Seq.
	delete(db.agents, core.DefaultAgentID)
	if err := db.AppendTrajectory(core.DefaultAgentID, session, "", ev("tool_call", 3)); err != nil {
		t.Fatalf("append after rebuild: %v", err)
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, session)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 3 || events[2].Seq != 5 {
		t.Fatalf("seq must continue after context rebuild: %+v", events)
	}
}

func TestPlanCommitUpdatesNode(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	if err := db.PlanCommit(core.DefaultAgentID, pid, "1", ev("plan_step", 1000),
		PlanStep{Status: PlanDone, Summary: "made it"}); err != nil {
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

func TestPlanAppendCreatesNodeAndEvent(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	pid := common.FormatHash(9)
	if err := db.AppendTrajectory(core.DefaultAgentID, pid, "1.2.1", ev("llm_request", 1000)); err != nil {
		t.Fatal(err)
	}
	node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, "1.2.1"))
	if err != nil {
		t.Fatal(err)
	}
	if node.Status != core.StatusPending {
		t.Fatalf("node not created as pending: %+v", node)
	}
	events := nodeEvents(t, db, 9, "1.2.1")
	if len(events) != 1 || events[0].EventType != "llm_request" {
		t.Fatalf("want 1 llm_request event on node, got %+v", events)
	}
}

// EnsureNode must build the parent chain with correct ParentID.
func TestPlanAppendBuildsParentChain(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(9), "1.2.1", ev("llm_request", 1000)); err != nil {
		t.Fatal(err)
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
	if leaf.Status != core.StatusPending {
		t.Fatalf("leaf not pending plan node: %+v", leaf)
	}
}

// Seq is one space a topic shares between its originals and its events, so an
// event appended before the turn is settled must still be there afterwards: the
// settle writes Seq 1 and 2 and must not reach down into the event range.
func TestSettledTurnKeepsEventsAppendedBeforeIt(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)

	for i, name := range []string{"llm_request", "tool_call"} {
		if err := db.AppendTrajectory(core.DefaultAgentID, common.FormatHash(topicID), "",
			ev(name, int64(100+i))); err != nil {
			t.Fatalf("append %s: %v", name, err)
		}
	}
	if _, err := db.Update(core.DefaultAgentID, turnOf(sceneID, topicID)); err != nil {
		t.Fatalf("update: %v", err)
	}

	events, err := db.ReadTrajectory(core.DefaultAgentID, common.FormatHash(topicID))
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

// Model A: a parent becomes Done only when the host commits it, and the
// bottom-up rollup of Done children's summaries fills an empty parent Summary
// without ever overwriting one the host wrote.
func TestPlanCommitRollupModelA(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	commit := func(topicID, path string, status PlanStatus, summary string, ts int64) {
		t.Helper()
		err := db.PlanCommit(core.DefaultAgentID, topicID, path, ev("plan_step", ts),
			PlanStep{Status: status, Summary: summary})
		if err != nil {
			t.Fatalf("commit %s: %v", path, err)
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
	// One child still pending: the parent is not Done and the counts say so.
	commit(partial, "1", PlanInProgress, "", 1000)
	commit(partial, "1.1", PlanDone, "step A", 1001)
	commit(partial, "1.2", PlanPending, "", 1002)
	if tree := state(partial); tree.Roots[0].Status == PlanDone ||
		tree.TotalCount != 3 || tree.DoneCount != 1 {
		t.Fatalf("a partially done parent was folded: %+v", tree)
	}
	// Every child Done still leaves the parent as the host left it.
	commit(partial, "1.2", PlanDone, "step B", 1003)
	if tree := state(partial); tree.Roots[0].Status != PlanInProgress {
		t.Fatalf("parent auto-folded without a host commit: %+v", tree.Roots[0])
	}
	// The host commits the parent with a blank Summary → Done children fold up.
	commit(partial, "1", PlanDone, "", 1004)
	if tree := state(partial); tree.DoneCount != 3 ||
		tree.Roots[0].Summary != "step A; step B" {
		t.Fatalf("rollup into a blank parent summary: %+v", tree.Roots[0])
	}

	// A summary the host wrote in the same commit survives the rollup.
	own := common.FormatHash(6)
	commit(own, "1.1", PlanDone, "step A", 1011)
	commit(own, "1.2", PlanDone, "step B", 1012)
	commit(own, "1", PlanDone, "parent's own words", 1013)
	if tree := state(own); tree.Roots[0].Summary != "parent's own words" {
		t.Fatalf("rollup overwrote the host summary: %+v", tree.Roots[0])
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

	// Done plan committed long ago, with a FRESH event bound to the step.
	doneID := common.FormatHash(9)
	if err := db.PlanCommit(core.DefaultAgentID, doneID, "1", ev("plan_step", old),
		PlanStep{Status: PlanDone, Summary: "fin"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendTrajectory(core.DefaultAgentID, doneID, "1", ev("note", now)); err != nil {
		t.Fatal(err)
	}
	doneNode := core.HashPlanNode(9, "1")
	age(doneNode)

	// In-flight plan: an aged Done root plus a child committed just now. The
	// tree is exempt as a whole, so the stale root survives with it.
	liveID := common.FormatHash(8)
	if err := db.PlanCommit(core.DefaultAgentID, liveID, "1", ev("plan_step", old),
		PlanStep{Status: PlanDone, Summary: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, liveID, "1.1", ev("plan_step", now),
		PlanStep{Status: PlanInProgress}); err != nil {
		t.Fatal(err)
	}
	liveRoot := core.HashPlanNode(8, "1")
	age(liveRoot)

	// Abandoned plan: non-Done and nothing committed inside the window.
	staleID := common.FormatHash(7)
	if err := db.PlanCommit(core.DefaultAgentID, staleID, "1", ev("plan_step", old),
		PlanStep{Status: PlanInProgress}); err != nil {
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
	events, err := db.ReadTrajectory(core.DefaultAgentID, doneID)
	if err != nil {
		t.Fatalf("the turn's event track must survive its own pruned tree: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "note" {
		t.Fatalf("want the fresh note event only, got %+v", events)
	}
}

// An appended event is forced to content-of-kind-event semantics: the fields a
// host has no business choosing — Kind, its Seq, the topic it belongs to, the
// role and the medium — are assigned by the library, so an append cannot smuggle
// a record into the transcript or forge a plan node.
func TestAppendEventCannotForgeContentFields(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	topicID := common.FormatHash(9)
	if err := db.AppendTrajectory(core.DefaultAgentID, topicID, "1", core.ArchiveSlot{
		Kind: core.KindUtterance, Seq: core.SeqUser, ContextID: 4242,
		Role: core.RoleDream, ContentType: core.ContentVideo, NodePath: "9.9",
		EventType: "llm_request", Content: "payload", CreatedAt: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if n := countRecords(db.engine, core.DefaultAgentID, core.RecL6PlanNode); n != 1 {
		t.Fatalf("plan nodes = %d, want the single node the path created", n)
	}
	node, err := core.ReadPlanNode(db.engine, core.DefaultAgentID, core.HashPlanNode(9, "1"))
	if err != nil {
		t.Fatalf("node for path 1 not created: %v", err)
	}
	if node.Status != core.StatusPending {
		t.Fatalf("created node must be pending, got %d", node.Status)
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
	if landed.ContextID != 9 || landed.IDHash != core.HashContent(9, landed.Seq) {
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
}

// Forest contract: two top-level steps yield two roots, and Done/Total
// covers both subtrees.
func TestPlanStateForestMultipleRoots(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	if err := db.PlanCommit(core.DefaultAgentID, topicID, "1", ev("plan_step", 1001),
		PlanStep{Status: PlanDone, Summary: "step one"}); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, topicID, "2", ev("plan_step", 1002),
		PlanStep{Status: PlanInProgress}); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, topicID, "2.1", ev("plan_step", 1003),
		PlanStep{Status: PlanDone, Summary: "sub"}); err != nil {
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

// A plan-bound event names itself: any EventType a bare turn event takes is
// accepted here too and stored verbatim, while the write contract that remains
// is still checked before the tree moves.
func TestPlanEventNamesAreHostOwned(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	if err := db.AppendTrajectory(core.DefaultAgentID, topicID, "1", ev("sandbox_ask", 1000)); err != nil {
		t.Fatalf("a host-named plan event must be accepted: %v", err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, topicID, "1", ev("host_step", 1001),
		PlanStep{Status: PlanDone}); err != nil {
		t.Fatalf("host-named commit event: %v", err)
	}
	events, err := db.ReadTrajectory(core.DefaultAgentID, topicID)
	if err != nil || len(events) != 2 {
		t.Fatalf("plan events: %+v err=%v", events, err)
	}
	if events[0].EventType != "sandbox_ask" || events[1].EventType != "host_step" {
		t.Fatalf("the engine rewrote the host's event names: %q %q",
			events[0].EventType, events[1].EventType)
	}

	if err := db.AppendTrajectory(core.DefaultAgentID, topicID, "2.1",
		core.ArchiveSlot{CreatedAt: 1002}); common.CodeOf(err) != common.ErrInvalidQuery {
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
	if err := db.PlanCommit(core.DefaultAgentID, topicID, "1", ev("plan_step", 1000),
		PlanStep{Title: "r", PlanType: "plan", Status: PlanPending}); err != nil {
		t.Fatal(err)
	}
	if err := db.PlanCommit(core.DefaultAgentID, topicID, "1.1", ev("plan_step", 1100),
		PlanStep{Title: "a", PlanType: "step", Status: PlanDone, Summary: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendTrajectory(core.DefaultAgentID, topicID, "1.1", ev("tool_call", 1200)); err != nil {
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

func TestPlanCommit_FinishedAt(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	defer db.Close()
	topicID := common.FormatHash(9)
	if err := db.PlanCommit(core.DefaultAgentID, topicID, "1", ev("plan_step", 100),
		PlanStep{Status: PlanDone, Summary: "fin"}); err != nil {
		t.Fatal(err)
	}
	tree, err := db.PlanState(core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatal(err)
	}
	first := tree.Roots[0].FinishedAt
	if first == 0 {
		t.Fatal("terminal commit must set FinishedAt")
	}
	// A non-terminal commit must not clear it.
	if err := db.PlanCommit(core.DefaultAgentID, topicID, "1", ev("plan_step", 200),
		PlanStep{Status: PlanInProgress}); err != nil {
		t.Fatal(err)
	}
	tree2, _ := db.PlanState(core.DefaultAgentID, topicID)
	if tree2.Roots[0].FinishedAt != first {
		t.Fatalf("non-terminal commit cleared FinishedAt: %d -> %d", first, tree2.Roots[0].FinishedAt)
	}
	// A re-terminal commit preserves the original FinishedAt.
	if err := db.PlanCommit(core.DefaultAgentID, topicID, "1", ev("plan_step", 300),
		PlanStep{Status: PlanDone, Summary: "fin2"}); err != nil {
		t.Fatal(err)
	}
	tree3, _ := db.PlanState(core.DefaultAgentID, topicID)
	if tree3.Roots[0].FinishedAt != first {
		t.Fatalf("re-terminal commit changed FinishedAt: %d -> %d", first, tree3.Roots[0].FinishedAt)
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

	for _, name := range []string{"llm_request", "tool_call"} {
		if err := db.AppendTrajectory(core.DefaultAgentID, turnID, "", ev(name, 1000)); err != nil {
			t.Fatalf("append %s: %v", name, err)
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
	for _, e := range events {
		if e.ContextID != settled {
			t.Fatalf("event %s landed under key %d, want the turn's %d", e.EventType, e.ContextID, settled)
		}
	}
}
