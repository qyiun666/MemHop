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
		if e.Seq != uint64(i+1) {
			t.Fatalf("seq must be 1-based increasing, got %d at %d", e.Seq, i)
		}
	}
	// Crystallize runs (stub returns no candidates) and yields a well-formed result.
	cr, err := db.Crystallize(ctx, sessionID)
	if err != nil || cr == nil || cr.CreatedIDs == nil {
		t.Fatalf("crystallize: %v", err)
	}
}

// TestSurfacePlanTriForm exercises the plan tri-form end-to-end through the
// api facade: PlanCommit advances a node, PlanState returns the tree with
// string statuses and hex-free ids, and under Model A a parent becomes Done
// only when the host explicitly commits it (then Done children roll up).
func TestSurfacePlanTriForm(t *testing.T) {
	db := openSurfaceDB(t)
	planID := common.FormatHash(common.HashID("plan-1"))
	// Root in_progress → child done → child done → host completes root → done.
	if err := db.PlanCommit(planID, "1", TrajectorySlot{EventType: "plan_step", Timestamp: 1000}, "in_progress", ""); err != nil {
		t.Fatalf("commit root: %v", err)
	}
	if err := db.PlanCommit(planID, "1.1", TrajectorySlot{EventType: "plan_step", Timestamp: 1001}, "done", "step A"); err != nil {
		t.Fatalf("commit 1.1: %v", err)
	}
	if err := db.PlanCommit(planID, "1.2", TrajectorySlot{EventType: "plan_step", Timestamp: 1002}, "done", "step B"); err != nil {
		t.Fatalf("commit 1.2: %v", err)
	}
	// Model A: root is NOT auto-folded by its children; the host must commit it.
	tree, err := db.PlanState(planID)
	if err != nil {
		t.Fatalf("plan state: %v", err)
	}
	if tree.Roots[0].Status == "done" {
		t.Fatalf("root must NOT auto-fold before explicit host commit, got %s", tree.Roots[0].Status)
	}
	// Host explicitly completes the parent → it becomes Done and rolls up.
	if err := db.PlanCommit(planID, "1", TrajectorySlot{EventType: "plan_step", Timestamp: 1003}, "done", ""); err != nil {
		t.Fatalf("commit root done: %v", err)
	}
	tree, err = db.PlanState(planID)
	if err != nil {
		t.Fatalf("plan state: %v", err)
	}
	if tree.Roots[0].Status != "done" {
		t.Fatalf("root should be done after explicit commit, got %s", tree.Roots[0].Status)
	}
	if tree.TotalCount != 3 || tree.DoneCount != 3 {
		t.Fatalf("counts: total=%d done=%d", tree.TotalCount, tree.DoneCount)
	}
	if tree.Roots[0].Summary == "" {
		t.Fatal("root summary should be concatenated from children")
	}
	// Child nodes carry string status and are well-formed.
	if len(tree.Roots[0].Children) != 2 {
		t.Fatalf("root should have 2 children, got %d", len(tree.Roots[0].Children))
	}
	for _, c := range tree.Roots[0].Children {
		if c.Status != "done" || c.Summary == "" {
			t.Fatalf("child should be done with summary: %+v", c)
		}
	}
	// PlanAppend does not advance; it just binds an event to a node.
	if err := db.AppendTrajectory(planID, "1.1.1", TrajectorySlot{EventType: "tool_call", Timestamp: 2000}); err != nil {
		t.Fatalf("plan append: %v", err)
	}
}

// TestSurfacePlanReplaceForest covers the host restart-recovery loop: two
// top-level steps form a two-root forest, and PlanReplace wipes that tree and
// reseeds a single pending root under the same plan id.
func TestSurfacePlanReplaceForest(t *testing.T) {
	db := openSurfaceDB(t)
	planID := NewPlanID("plan-replace")
	for _, step := range []string{"1", "2"} {
		if err := db.PlanCommit(planID, step, TrajectorySlot{EventType: "plan_step", Timestamp: 1000}, "pending", ""); err != nil {
			t.Fatalf("commit %s: %v", step, err)
		}
	}
	tree, err := db.PlanState(planID)
	if err != nil || len(tree.Roots) != 2 || tree.TotalCount != 2 {
		t.Fatalf("forest shape: roots=%d total=%d err=%v", len(tree.Roots), tree.TotalCount, err)
	}

	if err := db.PlanReplace(planID, "rewrite"); err != nil {
		t.Fatalf("replace: %v", err)
	}
	tree, err = db.PlanState(planID)
	if err != nil || len(tree.Roots) != 1 || tree.Roots[0].Title != "rewrite" || tree.Roots[0].Status != "pending" {
		t.Fatalf("reseeded root: %+v err=%v", tree.Roots, err)
	}

	if !isHexID(planID) {
		t.Fatalf("plan id must be a 16-hex token: %q", planID)
	}
}

// TestSurfaceSyncPlanTree locks the public contract: SyncPlanTree writes a
// whole tree (add/edit/delete) without emitting plan_step, and PlanState
// surfaces the node Type + FinishedAt fields.
func TestSurfaceSyncPlanTree(t *testing.T) {
	db := openSurfaceDB(t)
	planID := NewPlanID("sync-plan")
	root := &PlanNode{
		NodePath: "1", Title: "root", Type: "plan", Status: "running",
		Children: []PlanNode{
			{NodePath: "1.1", Title: "step a", Type: "step", Status: "done", Summary: "s"},
			{NodePath: "1.2", Title: "tool x", Type: "tool_call", Status: "failed"},
		},
	}
	if err := db.SyncPlanTree(planID, root); err != nil {
		t.Fatalf("sync: %v", err)
	}
	tree, err := db.PlanState(planID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Roots) != 1 || tree.Roots[0].Title != "root" || tree.Roots[0].Type != "plan" || tree.Roots[0].Status != "running" {
		t.Fatalf("root: %+v", tree.Roots)
	}
	children := tree.Roots[0].Children
	if len(children) != 2 {
		t.Fatalf("children = %d want 2", len(children))
	}
	if children[0].Status != "done" || children[0].Type != "step" || children[0].FinishedAt == 0 || children[0].Summary != "s" {
		t.Fatalf("step a: %+v", children[0])
	}
	if children[1].Type != "tool_call" || children[1].Status != "failed" || children[1].FinishedAt == 0 {
		t.Fatalf("tool x: %+v", children[1])
	}
	// Second sync deletes a node and edits the root; must reflect.
	root2 := &PlanNode{
		NodePath: "1", Title: "root", Type: "plan", Status: "done",
		Children: []PlanNode{{NodePath: "1.1", Title: "step a", Type: "step", Status: "done"}},
	}
	if err := db.SyncPlanTree(planID, root2); err != nil {
		t.Fatalf("sync2: %v", err)
	}
	tree2, err := db.PlanState(planID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree2.Roots[0].Children) != 1 || tree2.Roots[0].Children[0].NodePath != "1.1" {
		t.Fatalf("delete not reflected: %+v", tree2.Roots[0].Children)
	}
	if tree2.Roots[0].TrajCount != 0 {
		t.Fatalf("sync must not emit plan_step events: %+v", tree2.Roots[0])
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

// TestSurfaceReservedPlanID locks the planID=0 guard: the all-zero hex id is
// the sentinel AppendTrajectory writes on bare turn events, so no plan entry
// point may accept it — PlanReplace(0) used to delete every turn event of the
// domain.
func TestSurfaceReservedPlanID(t *testing.T) {
	db := openSurfaceDB(t)
	const zero = "0000000000000000"
	turn := common.FormatHash(common.HashID("reserved-plan-id"))
	now := time.Now().UnixMilli()
	for i := 0; i < 3; i++ {
		if err := db.AppendTrajectory(turn, "", TrajectorySlot{EventType: "llm_request", Timestamp: now}); err != nil {
			t.Fatal(err)
		}
	}
	ev := TrajectorySlot{EventType: "plan_step", Timestamp: now}
	calls := map[string]func() error{
		"AppendNode":  func() error { return db.AppendTrajectory(zero, "1", ev) },
		"PlanCommit":  func() error { return db.PlanCommit(zero, "1", ev, "done", "") },
		"PlanState":   func() error { _, err := db.PlanState(zero); return err },
		"PlanReplace": func() error { return db.PlanReplace(zero, "x") },
		"SyncPlanTree": func() error {
			return db.SyncPlanTree(zero, &PlanNode{NodePath: "1", Title: "t"})
		},
	}
	for name, call := range calls {
		if err := call(); common.CodeOf(err) != common.ErrInvalidQuery {
			t.Fatalf("%s(zero planID): err=%v, want ErrInvalidQuery", name, err)
		}
	}
	got, err := db.ReadTrajectory(turn)
	if err != nil || len(got) != 3 {
		t.Fatalf("bare turn events must survive: %d err=%v", len(got), err)
	}
}

// TestSurfaceAppendTrajectoryPlanBranch pins the merged write entry point: one
// method covers both a bare turn event (empty nodePath) and an event bound to a
// plan node, and the two land under different trajectory keys.
func TestSurfaceAppendTrajectoryPlanBranch(t *testing.T) {
	db := openSurfaceDB(t)
	now := time.Now().UnixMilli()
	turn := common.FormatHash(common.HashID("merged-turn"))
	planID := NewPlanID("merged-plan")

	if err := db.AppendTrajectory(turn, "", TrajectorySlot{EventType: "llm_request", Timestamp: now}); err != nil {
		t.Fatalf("bare turn event: %v", err)
	}
	if err := db.AppendTrajectory(planID, "1.1", TrajectorySlot{EventType: "tool_call", Payload: "p", Timestamp: now + 1}); err != nil {
		t.Fatalf("plan-bound event: %v", err)
	}

	evs, err := db.ReadTrajectory(turn)
	if err != nil || len(evs) != 1 || evs[0].TopicID != turn || evs[0].PlanID != "" {
		t.Fatalf("turn key: %+v err=%v", evs, err)
	}
	evs, err = db.ReadTrajectory(planID)
	if err != nil || len(evs) != 1 || evs[0].PlanID != planID || evs[0].TopicID != "" {
		t.Fatalf("plan key: %+v err=%v", evs, err)
	}
	// The bound event created its node chain, so the tree view sees it.
	tree, err := db.PlanState(planID)
	if err != nil || tree.TotalCount != 2 {
		t.Fatalf("node chain from the bound event: %+v err=%v", tree, err)
	}
	// Plan-bound events use the step vocabulary; a turn event does not.
	if err := db.AppendTrajectory(planID, "1", TrajectorySlot{EventType: "whatever", Timestamp: now + 2}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("unknown plan event type: want ErrInvalidQuery, got %v", err)
	}
}

// TestSurfaceIDContract locks the host-facing id surface: the library issues
// every id, so the facade exposes no integer-to-hex bridge. Plan names mint
// stable hex tokens deterministically, and the default domain constant opens a
// session.
func TestSurfaceIDContract(t *testing.T) {
	if got, want := NewPlanID("cat-42"), NewPlanID("cat-42"); got != want {
		t.Fatalf("plan id must be deterministic: %s vs %s", want, got)
	}
	planID := NewPlanID("cat-42")
	if !isHexID(planID) || planID == common.FormatHash(common.HashID("cat-42")) {
		t.Fatalf("plan id %q is not a namespaced 16-hex token", planID)
	}
	if other := NewPlanID("cat-43"); other == planID {
		t.Fatalf("distinct names must mint distinct ids")
	}
	if planID == DefaultAgentID {
		t.Fatal("a minted plan id must never be the reserved all-zero token")
	}

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
	if _, err := sess.Search(SearchQuery{}); err != nil {
		t.Fatalf("default domain search: %v", err)
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
