// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for what a host gets when it asks for something
// contradictory. Every case here came out of a probe that ran the call and read the
// answer rather than the doc: a destroyed scene still holding an open turn, two Search
// flags that name two different conversations, a named-read list carrying the key that
// can never name a record, and a merge list that repeats one of its deletions. Each of
// those used to be answered by quietly dropping the part nobody could honour.

package test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// A deleted scene takes the turn it was holding with it: the writes of that round answer
// that no turn is open rather than landing on a scene that no longer exists, and the next
// read starts a conversation the domain actually holds.
func TestInterfaceDeletedSceneClosesTheWritesOnIt(t *testing.T) {
	llm := newMockLLM(t)
	db := newTestDB(t, openMockDB(t, filepath.Join(t.TempDir(), "gone.meh"), llm.srv.URL))

	gone := openSession(t, db)
	res, err := db.Search(memhop.SearchQuery{SceneID: gone})
	if err != nil || res.Scene.SceneID != gone {
		t.Fatalf("open a turn on the named scene: %+v err %v", res, err)
	}
	if _, err := db.AppendArchive(planEvent(time.Now().UnixMilli(), "tool_call", "先记一条")); err != nil {
		t.Fatalf("append while the turn lives: %v", err)
	}
	if _, err := db.PlanNodeAdd(0, "先立一步"); err != nil {
		t.Fatalf("plan a step while the turn lives: %v", err)
	}
	if err := db.DeleteScene(gone); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}

	ts := time.Now().UnixMilli()
	if _, err := db.Update(memhop.TurnEnd{Input: "a", Output: "b", CreatedAt: ts}); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("update on a destroyed scene: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.AppendArchive(planEvent(ts+1, "tool_call", "删后再记")); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("append on a destroyed scene: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.PlanNodeAdd(0, "删后再计划"); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("plan on a destroyed scene: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.PlanState(); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("reading a plan nobody holds: want ErrInvalidQuery, got %v", err)
	}
	// The domain is not stuck: the next read holds a live scene, and its writes work.
	again, err := db.Search(memhop.SearchQuery{})
	if err != nil || again.Scene.SceneID == gone {
		t.Fatalf("the next read should hold a live scene, got %+v err %v", again, err)
	}
	if got, err := db.PlanState(); err != nil || got.TotalCount != 0 {
		t.Fatalf("a fresh turn inherited a plan: %+v err %v", got, err)
	}
	if _, err := db.PlanNodeAdd(0, "新的一段里立一步"); err != nil {
		t.Fatalf("planning after the recovery read: %v", err)
	}
	if got := mustPlanState(t, db); got.TotalCount != 1 {
		t.Fatalf("the plan after recovery = %+v, want the one step this turn created", got)
	}
}

// SceneID names a conversation to go on; NewScene asks for a different one. Sending both
// used to continue the named scene and drop the flag, which reads as "new session" to a
// host that then writes round one into the old transcript.
func TestInterfaceSearchRefusesContradictorySceneFlags(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	openTurn(t, db, sceneID)

	if _, err := db.Search(memhop.SearchQuery{SceneID: sceneID, NewScene: true}); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("naming a scene while asking for a fresh one: want ErrInvalidQuery, got %v", err)
	}
	// The refusal changes nothing: the same scene is still the one the domain holds.
	res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil || res.Scene.SceneID != sceneID {
		t.Fatalf("after the refusal the domain reads a different scene: %+v err %v", res, err)
	}
	// Each flag on its own keeps doing exactly what it says.
	fresh, err := db.Search(memhop.SearchQuery{NewScene: true})
	if err != nil || fresh.Scene.SceneID == sceneID {
		t.Fatalf("NewScene alone: %+v err %v, want another conversation", fresh, err)
	}
}

// SearchL4{IDs} is the one read a host answers by counting rows, so the reserved key that
// can never name a record is refused where it appears — the same reason a filter that can
// match nothing is refused instead of answered with an empty set. A well-formed id that
// simply is not there stays a legitimate absence.
func TestInterfaceSearchL4RefusesTheReservedKeyInAList(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	openTurn(t, db, sceneID)
	if _, err := db.AppendArchive(planEvent(time.Now().UnixMilli(), "tool_call", "一条要留下的事件")); err != nil {
		t.Fatalf("append: %v", err)
	}
	held, err := db.SearchL4(memhop.L4Query{Keyword: "一条要留下的事件"})
	if err != nil || len(held) != 1 {
		t.Fatalf("read the record back: %+v err %v", held, err)
	}

	if _, err := db.SearchL4(memhop.L4Query{IDs: []string{held[0].ID, "0000000000000000"}}); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("a list carrying the reserved key: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.SearchL4(memhop.L4Query{IDs: []string{"0000000000000000"}}); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("the reserved key alone: want ErrInvalidQuery, got %v", err)
	}
	// Unknown but well-formed ids are absences, not mistakes: the list answers with what
	// is there and no error.
	got, err := db.SearchL4(memhop.L4Query{IDs: []string{held[0].ID, "aaaaaaaaaaaaaaaa"}})
	if err != nil || len(got) != 1 || got[0].ID != held[0].ID {
		t.Fatalf("named read over a mix of present and absent = %+v err %v", got, err)
	}
}

// A merge deletes records, so its id list has to say what it means once: a repeated
// secondary used to be accepted, retargeting and tombstoning the same topics twice on the
// strength of a list the caller had lost track of.
func TestInterfaceMergeScenesRefusesADuplicatedSecondary(t *testing.T) {
	db, _ := openTestDB(t)
	primary := openSession(t, db)
	secondary := openSession(t, db)
	openTurn(t, db, secondary)
	if _, err := turn(db.Session, "被并那一方的第一轮", "答"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	third := openSession(t, db)

	if err := db.MergeScenes(primary, []string{secondary, secondary}); memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("a secondary listed twice: want ErrInvalidQuery, got %v", err)
	}
	if scenes, err := db.ListScenes(""); err != nil || len(scenes) != 3 {
		t.Fatalf("the refused merge already moved scenes: %+v err %v", scenes, err)
	}
	// The same call, said once, works — and the duplicate rule does not reach across lists
	// that merely name each other.
	if err := db.MergeScenes(primary, []string{secondary, third}); err != nil {
		t.Fatalf("merging two distinct secondaries: %v", err)
	}
	if scenes, err := db.ListScenes(""); err != nil || len(scenes) != 1 || scenes[0].SceneID != primary {
		t.Fatalf("after the merge the roster = %+v err %v, want the primary alone", scenes, err)
	}
}

// A scene id and the turn's own topic id arrive in the same Search result, so mixing them
// is the likeliest key mistake a host can make — and a scene never owns archives, so the
// read would answer empty and a round would look like it recorded nothing. An id that
// names no record at all stays an empty answer, because a turn that is still open holds
// content before it holds a topic.
func TestInterfaceSearchL4RefusesASceneIDWhereATurnIDBelongs(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	turnID := openTurn(t, db, sceneID)
	if _, err := db.AppendArchive(planEvent(time.Now().UnixMilli(), "tool_call", "轮中还开着时记一条")); err != nil {
		t.Fatalf("append: %v", err)
	}

	open := turnID
	rows, err := db.SearchL4(memhop.L4Query{TopicID: &open})
	if err != nil || len(rows) != 1 {
		t.Fatalf("an open turn's own key = %+v err %v, want the one event it holds", rows, err)
	}
	_, err = db.SearchL4(memhop.L4Query{TopicID: &sceneID})
	if memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("a scene id where the turn key belongs: want ErrInvalidQuery, got %v", err)
	}
	if !strings.Contains(err.Error(), "NewTopicID") {
		t.Fatalf("the refusal does not point at the key the host meant: %v", err)
	}
	// An id that names nothing is absence, not a mistake.
	ghost := "bbbbbbbbbbbbbbbb"
	if rows, err := db.SearchL4(memhop.L4Query{TopicID: &ghost}); err != nil || len(rows) != 0 {
		t.Fatalf("an id naming no record = %+v err %v, want an empty answer", rows, err)
	}
	closed, err := db.Update(memhop.TurnEnd{Input: "问", Output: "答", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("close the turn: %v", err)
	}
	key := closed.ID
	if rows, err := db.SearchL4(memhop.L4Query{TopicID: &key}); err != nil || len(rows) != 3 {
		t.Fatalf("after the close the turn holds %+v err %v, want two dialogue lines plus the event", rows, err)
	}
}

// Consolidation and a live round overlap in every host loop, and the difference between them
// is a record: an open turn owns content but no topic yet. So a pass must fuse what has
// settled and leave the open round's records exactly where that round put them — readable by
// the key the round's own read minted, and closable afterwards.
func TestInterfaceDreamLeavesTheOpenTurnAlone(t *testing.T) {
	llm := newMockLLM(t)
	db := newTestDB(t, openMockDB(t, filepath.Join(t.TempDir(), "open_turn.meh"), llm.srv.URL,
		func(d *memhop.MemHopDefaults) { d.DreamCompressMinTopics = 2 }))
	sceneID := openSession(t, db)
	for i := 0; i < 4; i++ {
		openTurn(t, db, sceneID)
		if _, err := turn(db.Session, "可被巩固的一轮", "答"); err != nil {
			t.Fatalf("close turn %d: %v", i, err)
		}
	}
	open := openTurn(t, db, sceneID)
	if _, err := db.AppendArchive(memhop.ArchiveInput{
		Kind: memhop.KindEvent, EventType: "tool_call", Content: "这一轮正在做的事", CreatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("append while the turn is open: %v", err)
	}

	rep, err := db.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if rep.L2TopicsCompressed == 0 {
		t.Fatalf("nothing consolidated, so this case proves nothing about the open round: %+v", rep)
	}
	kind := memhop.KindEvent
	events, err := db.SearchL4(memhop.L4Query{TopicID: &open, Kind: &kind})
	if err != nil || len(events) != 1 || events[0].Content != "这一轮正在做的事" {
		t.Fatalf("the consolidation pass disturbed the open round: %+v err %v", events, err)
	}
	ctx, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	for _, row := range ctx.Topics {
		if row.TopicID == open {
			t.Fatalf("an unsettled round appeared as a topic: %+v", row)
		}
	}
	topic, err := db.Update(memhop.TurnEnd{Input: "把开着的那轮收掉", Output: "答", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("closing the round after the pass: %v", err)
	}
	if topic.ID != open || topic.Depth != 1 {
		t.Fatalf("the round closed into a different topic: %+v", topic)
	}
	utter := memhop.KindUtterance
	lines, err := db.SearchL4(memhop.L4Query{TopicID: &open, Kind: &utter})
	if err != nil || len(lines) != 2 {
		t.Fatalf("the closed round's dialogue = %+v err %v, want the two sides it ended with", lines, err)
	}
	if events, err := db.SearchL4(memhop.L4Query{TopicID: &open, Kind: &kind}); err != nil || len(events) != 1 {
		t.Fatalf("the event written while the round was open no longer reads back: %+v err %v", events, err)
	}
}
