// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Turn-keyed surface: event enumeration, crystallize, and the plan tree.

package api

import (
	"context"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
)

// event builds one turn event as a host appends it: the kind is what makes it a
// record of what happened rather than a line of dialogue.
func event(eventType, payload string, ts int64) ArchiveSlot {
	return ArchiveSlot{Kind: KindEvent, EventType: eventType, Content: payload, CreatedAt: ts}
}

// onStep names the plan step an event belongs to.
func onStep(slot ArchiveSlot, seq uint32) ArchiveSlot {
	slot.NodeSeq = seq
	return slot
}

// eventsOf reads one topic's event track the only way a host can now: the same key
// with the kind condition, in Seq order.
func eventsOf(t *testing.T, db *Session, topicID string) []ArchiveSlot {
	t.Helper()
	kind := KindEvent
	out, err := db.SearchL4(L4Query{TopicID: &topicID, Kind: &kind})
	if err != nil {
		t.Fatalf("read events of %s: %v", topicID, err)
	}
	return out
}

// Dream drops events past the 7-day retention window even when there is nothing
// to consolidate, and no delete API is exposed: a turn keeps the event still
// inside the window and loses the one outside it.
func TestSurfaceDreamPrunesExpiredEvents(t *testing.T) {
	db := openSurfaceDB(t)
	sessionA := common.FormatHash(common.HashID("lifecycle-a"))
	sessionB := common.FormatHash(common.HashID("lifecycle-b"))
	appendOne := func(id string, ts int64) {
		if err := db.AppendArchive(id, event("llm_request", "asked", ts)); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	fresh := time.Now().Add(-time.Hour).UnixMilli()
	appendOne(sessionA, 100)
	appendOne(sessionA, fresh)
	appendOne(sessionB, 1_700_000_050_000)

	if got := eventsOf(t, db, sessionA); len(got) != 2 {
		t.Fatalf("both of A's events are inside the window: %+v", got)
	}

	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("dream: %v", err)
	}
	got := eventsOf(t, db, sessionA)
	if len(got) != 1 || got[0].CreatedAt != fresh {
		t.Fatalf("only A's fresh event survives: %+v", got)
	}
	if rest := eventsOf(t, db, sessionB); len(rest) != 0 {
		t.Fatalf("B's expired event survives: %+v", rest)
	}
}

func TestSurfaceArchiveAppendAndRead(t *testing.T) {
	db := openSurfaceDB(t)
	sessionID := common.FormatHash(common.HashID("session-42"))
	events := []ArchiveSlot{
		event("llm_request", "user asks", 1_700_000_040_000),
		event("tool_call", "search", 1_700_000_040_100),
		event("llm_output", "replied", 1_700_000_040_200),
	}
	for _, ev := range events {
		if err := db.AppendArchive(sessionID, ev); err != nil {
			t.Fatalf("append content: %v", err)
		}
	}
	// Missing required fields must be rejected.
	if err := db.AppendArchive(sessionID, ArchiveSlot{Kind: KindEvent, Content: "no type"}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("append invalid event: want ErrInvalidQuery, got %v", err)
	}
	got := eventsOf(t, db, sessionID)
	if len(got) != len(events) {
		t.Fatalf("read events: got %d, want %d", len(got), len(events))
	}
	for i, e := range got {
		want := events[i]
		// Slots 1 and 2 belong to the turn's dialogue, so the library starts a
		// topic's events at 3 and counts up from there.
		if e.Seq != uint64(i+3) {
			t.Fatalf("seq must ascend from the first free slot, got %d at %d", e.Seq, i)
		}
		// The read must hand back what was written, field for field: a facade
		// mapping that drops a column is otherwise invisible at this surface.
		if e.Kind != KindEvent || e.EventType != want.EventType || e.Content != want.Content ||
			e.CreatedAt != want.CreatedAt {
			t.Fatalf("event[%d] lost its body on the way back: got %+v want %+v", i, e, want)
		}
		if !isHexID(e.IDHash) || e.TopicID != sessionID {
			t.Fatalf("event[%d] ids: hash=%q context=%q", i, e.IDHash, e.TopicID)
		}
	}
}

// TestSurfaceListScenesByProject verifies scenes anchored to an L3 domain are
// listed with hex ids once a session opening anchors them, and that two
// different L3 domains yield DISJOINT scene sets (the exclusion branch).
func TestSurfaceListScenesByProject(t *testing.T) {
	db := openSurfaceDB(t)
	l3A := l3Graph(t, db, "l3-proj-a")
	l3B := l3Graph(t, db, "l3-proj-b")
	// Two session openings, each anchored to its own L3 project domain.
	if _, err := db.Search(SearchQuery{L3ID: l3A}); err != nil {
		t.Fatalf("search A: %v", err)
	}
	if _, err := db.Search(SearchQuery{L3ID: l3B}); err != nil {
		t.Fatalf("search B: %v", err)
	}
	scenesA, err := db.ListScenes(l3A)
	if err != nil {
		t.Fatalf("list by l3 A: %v", err)
	}
	if len(scenesA) == 0 {
		t.Fatal("ListScenes(A) should return the l3A-anchored scene")
	}
	for _, sc := range scenesA {
		if sc.L3ID != l3A {
			t.Fatalf("scene %s should be anchored to %s, got %s", sc.SceneID, l3A, sc.L3ID)
		}
		if !isHexID(sc.SceneID) {
			t.Fatalf("scene id %s should be 16 hex", sc.SceneID)
		}
	}
	scenesB, err := db.ListScenes(l3B)
	if err != nil {
		t.Fatalf("list by l3 B: %v", err)
	}
	if len(scenesB) == 0 {
		t.Fatal("ListScenes(B) should return the l3B-anchored scene")
	}
	// Cross-cutting: the l3A list must not contain any scene the l3B list
	// holds, i.e. the two domain lists are disjoint (exclusion branch).
	sceneIDsB := make(map[string]struct{}, len(scenesB))
	for _, sc := range scenesB {
		sceneIDsB[sc.SceneID] = struct{}{}
	}
	for _, sc := range scenesA {
		if _, dup := sceneIDsB[sc.SceneID]; dup {
			t.Fatalf("scene %s leaked into both l3A and l3B lists", sc.SceneID)
		}
	}
}

// TestSurfaceUpdateSceneAnchor verifies the anchor correction path: re-anchoring
// a scene that already has a different domain is rejected (never a silent
// no-op), Force moves it, and an empty L3ID clears it.
func TestSurfaceUpdateSceneAnchor(t *testing.T) {
	db := openSurfaceDB(t)
	l3A := l3Graph(t, db, "cor-a")
	l3B := l3Graph(t, db, "cor-b")
	if _, err := db.Search(SearchQuery{L3ID: l3A}); err != nil {
		t.Fatalf("search: %v", err)
	}
	scenesA, err := db.ListScenes(l3A)
	if err != nil || len(scenesA) == 0 {
		t.Fatalf("expected an l3A-anchored scene: %+v err=%v", scenesA, err)
	}
	sceneID := scenesA[0].SceneID

	// Write-once: a non-force move to another domain is rejected and changes nothing.
	if _, err := db.UpdateScene(sceneID, ScenePatch{L3ID: &l3B}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("non-force re-anchor: want ErrInvalidQuery, got %v", err)
	}
	if scenes, _ := db.ListScenes(l3A); len(scenes) != 1 {
		t.Fatalf("non-force set must not move the anchor: %+v", scenes)
	}
	// Force re-anchors the scene to l3B and hands the written scene back, so the
	// host reads its anchor off the reply instead of listing the domain.
	got, err := db.UpdateScene(sceneID, ScenePatch{L3ID: &l3B, Force: true})
	if err != nil {
		t.Fatalf("force re-anchor: %v", err)
	}
	if got.SceneID != sceneID || !isHexID(got.L3ID) {
		t.Fatalf("written scene = %+v, want the same hex id + a hex anchor", got)
	}
	if got.L3ID != l3B {
		t.Fatalf("anchor = %q, want %q", got.L3ID, l3B)
	}
	if scenes, _ := db.ListScenes(l3B); len(scenes) != 1 {
		t.Fatalf("force set must land in l3B: %+v", scenes)
	}
	// Clearing needs no Force: the scene simply becomes unanchored again.
	clearTo := ""
	if got, err = db.UpdateScene(sceneID, ScenePatch{L3ID: &clearTo}); err != nil {
		t.Fatalf("clear anchor: %v", err)
	} else if got.L3ID != "" {
		t.Fatalf("clear must drop the anchor, got %q", got.L3ID)
	}
	if scenes, _ := db.ListScenes(l3B); len(scenes) != 0 {
		t.Fatalf("clear must drop the anchor: %+v", scenes)
	}
}

// TestSurfaceReservedTopicID locks the all-zero guard: 0 is the value every
// record leaves its turn key unset with, so no entry point that addresses a turn —
// write or read — may accept it.
func TestSurfaceReservedTopicID(t *testing.T) {
	db := openSurfaceDB(t)
	const zero = "0000000000000000"
	turn := mustTurnKey(t, db)
	now := time.Now().UnixMilli()
	for i := 0; i < 3; i++ {
		if err := db.AppendArchive(turn, event("llm_request", "asked", now)); err != nil {
			t.Fatal(err)
		}
	}
	ev := event("plan_step", "stepped", now)
	calls := map[string]func() error{
		"AppendBare":     func() error { return db.AppendArchive(zero, ev) },
		"AppendStep":     func() error { return db.AppendArchive(zero, onStep(ev, 1)) },
		"PlanCreate":     func() error { _, err := db.PlanCreate(zero, "一步"); return err },
		"PlanNodeAdd":    func() error { _, err := db.PlanNodeAdd(zero, 0, "一步"); return err },
		"PlanNodeUpdate": func() error { return db.PlanNodeUpdate(zero, PlanStep{Seq: 1, Status: "done"}) },
		"PlanState":      func() error { _, err := db.PlanState(zero); return err },
	}
	for name, call := range calls {
		if err := call(); common.CodeOf(err) != common.ErrInvalidQuery {
			t.Fatalf("%s(zero topic id): err=%v, want ErrInvalidQuery", name, err)
		}
	}
	if got := eventsOf(t, db, turn); len(got) != 3 {
		t.Fatalf("bare turn events must survive: %d", len(got))
	}
}

// TestSurfaceAppendArchivePlanBranch pins the split write surface: the plan write
// face creates a tree one step at a time and AppendArchive writes content — both a
// bare turn event (no NodeSeq) and an event bound to one created step — and the
// two land under the two different turns that produced them.
func TestSurfaceAppendArchivePlanBranch(t *testing.T) {
	db := openSurfaceDB(t)
	now := time.Now().UnixMilli()
	turn := mustTurnKey(t, db)
	planTurn := mustTurnKey(t, db)

	if err := db.AppendArchive(turn, event("llm_request", "asked", now)); err != nil {
		t.Fatalf("bare turn event: %v", err)
	}
	// A turn that only logged plain events owns no tree: a bare event references
	// no step, so its key is not a plan at all.
	if bare, err := db.PlanState(turn); err != nil || bare.TotalCount != 0 {
		t.Fatalf("bare turn events invented a plan: %+v err=%v", bare, err)
	}
	// An event cannot open a step. Creating the step first is the whole point: a
	// step the plan does not hold is the host's plan and its record disagreeing.
	if err := db.AppendArchive(planTurn, onStep(event("tool_call", "p", now+1), 2)); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("event on a step that does not exist: want ErrInvalidQuery, got %v", err)
	}
	if empty, err := db.PlanState(planTurn); err != nil || empty.TotalCount != 0 {
		t.Fatalf("the refused event built a step: %+v err=%v", empty, err)
	}

	root, err := db.PlanCreate(planTurn, "父")
	if err != nil {
		t.Fatal(err)
	}
	child, err := db.PlanNodeAdd(planTurn, root, "子")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AppendArchive(planTurn, onStep(event("tool_call", "p", now+1), child)); err != nil {
		t.Fatalf("step-bound event: %v", err)
	}
	evs := eventsOf(t, db, planTurn)
	if len(evs) != 1 || evs[0].TopicID != planTurn || evs[0].NodeSeq != child {
		t.Fatalf("plan key: %+v", evs)
	}
	// The plan branch takes the host's own event name exactly as the bare path
	// does, and refuses only an empty one.
	if err := db.AppendArchive(planTurn, onStep(event("sandbox_ask", "asked", now+2), root)); err != nil {
		t.Fatalf("host-named plan event: %v", err)
	}
	if err := db.AppendArchive(planTurn, onStep(ArchiveSlot{Kind: KindEvent, Content: "x", CreatedAt: now + 3}, root)); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("empty plan event type: want ErrInvalidQuery, got %v", err)
	}
	// An ordinal no step of this turn holds can never name one, so the same rule
	// refuses it — and nothing about the tree changes on the way out. An integer
	// address has no malformed spelling to catch: a step either exists or does not.
	if err := db.AppendArchive(planTurn, onStep(event("x", "p", now+4), 77)); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("an ordinal nobody created: want ErrInvalidQuery, got %v", err)
	}
	if still, err := db.PlanState(planTurn); err != nil || still.TotalCount != 2 {
		t.Fatalf("a refused event changed the tree: %+v err=%v", still, err)
	}
	// An event that claims the dialogue track while naming a step is refused: the
	// two kinds do not share axes.
	if err := db.AppendArchive(planTurn, onStep(ArchiveSlot{
		Kind: KindUtterance, Role: RoleUser, EventType: "tool_call", Content: "x", CreatedAt: now + 5,
	}, root)); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("utterance wearing an event: want ErrInvalidQuery, got %v", err)
	}
	// A bound event carries the step it landed on, and the id it comes back with is
	// a library-issued hex token the host never builds.
	bound := eventsOf(t, db, planTurn)
	if len(bound) != 2 {
		t.Fatalf("plan-bound events: %+v", bound)
	}
	if bound[0].NodeSeq == 0 || bound[0].NodeSeq == bound[1].NodeSeq {
		t.Fatalf("plan-bound event lost its step attribution: %+v", bound)
	}
	if !isHexID(bound[1].IDHash) {
		t.Fatalf("event id is not a library hex token: %q", bound[1].IDHash)
	}
}

// TestSurfaceIDContract locks the host-facing id surface: the library issues
// every id, so the facade exposes no integer-to-hex bridge, and the turn key a
// plan is addressed by is one of them — hex-rendered, library-minted, and never
// the reserved all-zero token. The default domain constant opens a session.
func TestSurfaceIDContract(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	m, err := OpenMulti(surfaceConfig(t, llm.URL))
	if err != nil {
		t.Fatalf("openmulti: %v", err)
	}
	defer m.Close()
	sess, err := m.Session(DefaultAgentID)
	if err != nil {
		t.Fatalf("default domain session: %v", err)
	}
	res, err := sess.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("default domain search: %v", err)
	}
	if !isHexID(res.NewTopicID) || res.NewTopicID == DefaultAgentID {
		t.Fatalf("turn topic %q is not a library-minted non-zero hex token", res.NewTopicID)
	}
}

// TestSurfaceDreamUnknownScene: naming a scene that does not exist is an
// error, not a zero-valued report that looks like a successful no-op.
func TestSurfaceDreamUnknownScene(t *testing.T) {
	db := openSurfaceDB(t)
	ghost := common.FormatHash(common.HashID("no-such-scene"))
	rep, err := db.Dream(context.Background(), ghost)
	if CodeOf(err) != ErrNotFound {
		t.Fatalf("dream unknown scene: rep=%v want ErrNotFound, got %v", rep, err)
	}
	// Dreaming an empty domain (no scene named) stays a clean no-op.
	if rep, err := db.Dream(context.Background(), ""); err != nil || rep == nil {
		t.Fatalf("dream empty domain: rep=%v err=%v", rep, err)
	}
}
