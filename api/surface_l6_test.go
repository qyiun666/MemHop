// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 trajectory and crystallize surface tests.

package api

import (
	"context"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
)

func TestSurfaceL6TrajectoryLifecycle(t *testing.T) {
	db := openSurfaceDB(t)
	sessionA := common.FormatHash(common.HashID("lifecycle-a"))
	sessionB := common.FormatHash(common.HashID("lifecycle-b"))
	appendOne := func(id string, ts int64) {
		if err := db.AppendTrajectory(id, "", TrajectorySlot{EventType: "llm_request", Timestamp: ts}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	fresh := time.Now().Add(-time.Hour).UnixMilli()
	appendOne(sessionA, 100)
	appendOne(sessionA, fresh)
	appendOne(sessionB, 1_700_000_050_000)

	list, err := db.ListTrajectorySessions()
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v err=%v, want 2 sessions", list, err)
	}
	byID := make(map[string]TrajectorySessionSummary, len(list))
	for _, sum := range list {
		byID[sum.SessionID] = sum
	}
	if sum := byID[sessionA]; sum.Steps != 2 || sum.LastAppendAt != fresh {
		t.Fatalf("summary a mismatch: %+v", sum)
	}
	if sum := byID[sessionB]; sum.Steps != 1 || sum.LastAppendAt != 1_700_000_050_000 {
		t.Fatalf("summary b mismatch: %+v", sum)
	}

	// Dream drops events older than the 7-day retention window even when
	// there is nothing to consolidate; no delete API is exposed.
	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("dream: %v", err)
	}
	list, err = db.ListTrajectorySessions()
	if err != nil || len(list) != 1 || list[0].SessionID != sessionA || list[0].Steps != 1 {
		t.Fatalf("surviving list = %+v err=%v, want only sessionA's fresh event", list, err)
	}
	// The enumerated hex ID must feed ReadTrajectory / Crystallize directly.
	if got, err := db.ReadTrajectory(list[0].SessionID); err != nil || len(got) != 1 {
		t.Fatalf("read enumerated session: %d err=%v", len(got), err)
	}
}

func TestSurfaceL6Trajectory(t *testing.T) {
	db := openSurfaceDB(t)
	ctx := context.Background()
	sessionID := common.FormatHash(common.HashID("session-42"))
	events := []TrajectorySlot{
		{EventType: "llm_request", Payload: "user asks", Timestamp: 1_700_000_040_000},
		{EventType: "tool_call", Payload: "search", Timestamp: 1_700_000_040_100},
		{EventType: "llm_output", Payload: "replied", Timestamp: 1_700_000_040_200},
	}
	for _, ev := range events {
		if err := db.AppendTrajectory(sessionID, "", ev); err != nil {
			t.Fatalf("append trajectory: %v", err)
		}
	}
	// Missing required fields must be rejected.
	if err := db.AppendTrajectory(sessionID, "", TrajectorySlot{Payload: "no type"}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("append invalid event: want ErrInvalidQuery, got %v", err)
	}
	got, err := db.ReadTrajectory(sessionID)
	if err != nil || len(got) != len(events) {
		t.Fatalf("read trajectory: got %d err=%v", len(got), err)
	}
	for i, e := range got {
		want := events[i]
		// Slots 1 and 2 belong to the turn's two originals, so the library
		// starts a topic's events at 3 and counts up from there.
		if e.Seq != uint64(i+3) {
			t.Fatalf("seq must ascend from the first free slot, got %d at %d", e.Seq, i)
		}
		// The read must hand back what was written, field for field: a facade
		// mapping that drops a column is otherwise invisible at this surface.
		if e.EventType != want.EventType || e.Payload != want.Payload || e.Timestamp != want.Timestamp {
			t.Fatalf("event[%d] lost its body on the way back: got %+v want %+v", i, e, want)
		}
		if !isHexID(e.IDHash) || e.SessionID != sessionID {
			t.Fatalf("event[%d] ids: hash=%q session=%q", i, e.IDHash, e.SessionID)
		}
	}
	// Crystallize runs (stub returns no candidates) and yields a well-formed output.
	cr, err := db.Crystallize(ctx, sessionID, nil)
	if err != nil || cr == nil || cr.Capabilities == nil {
		t.Fatalf("crystallize: %v", err)
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
// record leaves its L6 key unset with, so no entry point — write or read — may
// accept it.
func TestSurfaceReservedTopicID(t *testing.T) {
	db := openSurfaceDB(t)
	const zero = "0000000000000000"
	turn := mustTurnKey(t, db)
	now := time.Now().UnixMilli()
	for i := 0; i < 3; i++ {
		if err := db.AppendTrajectory(turn, "", TrajectorySlot{EventType: "llm_request", Timestamp: now}); err != nil {
			t.Fatal(err)
		}
	}
	ev := TrajectorySlot{EventType: "plan_step", Timestamp: now}
	ctx := context.Background()
	calls := map[string]func() error{
		"AppendBare":     func() error { return db.AppendTrajectory(zero, "", ev) },
		"AppendNode":     func() error { return db.AppendTrajectory(zero, "1", ev) },
		"PlanCommit":     func() error { return db.PlanCommit(zero, "1", ev, PlanStep{Status: "done", Summary: ""}) },
		"PlanState":      func() error { _, err := db.PlanState(zero); return err },
		"ReadTrajectory": func() error { _, err := db.ReadTrajectory(zero); return err },
		"Crystallize":    func() error { _, err := db.Crystallize(ctx, zero, nil); return err },
	}
	for name, call := range calls {
		if err := call(); common.CodeOf(err) != common.ErrInvalidQuery {
			t.Fatalf("%s(zero topic id): err=%v, want ErrInvalidQuery", name, err)
		}
	}
	got, err := db.ReadTrajectory(turn)
	if err != nil || len(got) != 3 {
		t.Fatalf("bare turn events must survive: %d err=%v", len(got), err)
	}
}

// TestSurfaceAppendTrajectoryPlanBranch pins the merged write entry point: one
// method covers both a bare turn event (empty nodePath) and an event bound to a
// plan step, and the two land under the two different turns that produced them.
func TestSurfaceAppendTrajectoryPlanBranch(t *testing.T) {
	db := openSurfaceDB(t)
	now := time.Now().UnixMilli()
	turn := mustTurnKey(t, db)
	planTurn := mustTurnKey(t, db)

	if err := db.AppendTrajectory(turn, "", TrajectorySlot{EventType: "llm_request", Timestamp: now}); err != nil {
		t.Fatalf("bare turn event: %v", err)
	}
	if err := db.AppendTrajectory(planTurn, "1.1", TrajectorySlot{EventType: "tool_call", Payload: "p", Timestamp: now + 1}); err != nil {
		t.Fatalf("plan-bound event: %v", err)
	}

	evs, err := db.ReadTrajectory(turn)
	if err != nil || len(evs) != 1 || evs[0].SessionID != turn || evs[0].NodePath != "" {
		t.Fatalf("turn key: %+v err=%v", evs, err)
	}
	// A turn that only logged plain events owns no tree: a bare event references
	// no node, so its key is not a plan at all.
	if bare, err := db.PlanState(turn); err != nil || bare.TotalCount != 0 {
		t.Fatalf("bare turn events invented a plan: %+v err=%v", bare, err)
	}
	evs, err = db.ReadTrajectory(planTurn)
	if err != nil || len(evs) != 1 || evs[0].SessionID != planTurn || evs[0].NodePath != "1.1" {
		t.Fatalf("plan key: %+v err=%v", evs, err)
	}
	// The bound event created its node chain, so the tree view sees it — under
	// the very key the event was written with.
	tree, err := db.PlanState(planTurn)
	if err != nil || tree.TotalCount != 2 {
		t.Fatalf("node chain from the bound event: %+v err=%v", tree, err)
	}
	// The plan path names events exactly as the bare path does: the engine takes
	// the host's own word and refuses only an empty one.
	if err := db.AppendTrajectory(planTurn, "1", TrajectorySlot{EventType: "sandbox_ask", Timestamp: now + 2}); err != nil {
		t.Fatalf("host-named plan event: %v", err)
	}
	if err := db.AppendTrajectory(planTurn, "1", TrajectorySlot{Timestamp: now + 3}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("empty plan event type: want ErrInvalidQuery, got %v", err)
	}
	// A malformed dotted path is refused before the tree is touched. nodePath
	// shapes the tree itself (every missing segment is created), so an empty
	// segment must never reach storage as a nameless step.
	for _, bad := range []string{"1..2", "1.", ".1", "1.1."} {
		if err := db.AppendTrajectory(planTurn, bad, TrajectorySlot{EventType: "x", Timestamp: now + 4}); CodeOf(err) != ErrInvalidQuery {
			t.Fatalf("nodePath %q: want ErrInvalidQuery, got %v", bad, err)
		}
		if still, err := db.PlanState(planTurn); err != nil || still.TotalCount != 2 {
			t.Fatalf("nodePath %q left a half-built chain: %+v err=%v", bad, still, err)
		}
	}
	// A plan-bound event carries the step it landed on, and the id it comes back
	// with is a library-issued hex token the host never builds.
	bound, berr := db.ReadTrajectory(planTurn)
	if berr != nil || len(bound) != 2 {
		t.Fatalf("plan-bound events: %+v err=%v", bound, berr)
	}
	if bound[1].NodePath == "" || bound[1].NodePath == bound[0].NodePath {
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
