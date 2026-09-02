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
		if err := db.AppendTrajectory(id, TrajectorySlot{EventType: "llm_request", Timestamp: ts}); err != nil {
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
		if err := db.AppendTrajectory(sessionID, ev); err != nil {
			t.Fatalf("append trajectory: %v", err)
		}
	}
	// Missing required fields must be rejected.
	if err := db.AppendTrajectory(sessionID, TrajectorySlot{Payload: "no type"}); CodeOf(err) != ErrInvalidQuery {
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
	if err := db.PlanAppend(planID, "1.1.1", TrajectorySlot{EventType: "tool_call", Timestamp: 2000}); err != nil {
		t.Fatalf("plan append: %v", err)
	}
}

// TestSurfacePlanReplaceAndListPlans covers the host restart-recovery loop:
// two top-level steps form two roots, PlanReplace wipes and reseeds one
// plan, and ListPlans discovers every plan with stats.
func TestSurfacePlanReplaceAndListPlans(t *testing.T) {
	db := openSurfaceDB(t)
	planID := common.FormatHash(common.HashID("plan-replace"))
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

	plans, err := db.ListPlans()
	if err != nil || len(plans) != 1 {
		t.Fatalf("list plans: %+v err=%v", plans, err)
	}
	p := plans[0]
	if p.PlanID != planID || p.NodeCount != 1 || p.TotalCount != 1 || !p.Active || !isHexID(p.PlanID) {
		t.Fatalf("plan summary wrong: %+v", p)
	}
}

// TestSurfaceSyncPlanTree locks the public contract: SyncPlanTree writes a
// whole tree (add/edit/delete) without emitting plan_step, and PlanState
// surfaces the node Type + FinishedAt fields.
func TestSurfaceSyncPlanTree(t *testing.T) {
	db := openSurfaceDB(t)
	planID := common.FormatHash(common.HashID("sync-plan"))
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
	plans, err := db.ListPlans()
	if err != nil || len(plans) != 1 || plans[0].NodeCount != 2 {
		t.Fatalf("list plans: %+v err=%v", plans, err)
	}
}

// TestSurfaceListScenesByL3 verifies scenes anchored to an L3 domain are
// listed with hex ids once a session opening anchors them, and that two
// different L3 domains yield DISJOINT scene sets (the exclusion branch).
func TestSurfaceListScenesByL3(t *testing.T) {
	db := openSurfaceDB(t)
	l3A := common.FormatHash(common.HashID("l3-proj-a"))
	l3B := common.FormatHash(common.HashID("l3-proj-b"))
	// Two session openings, each anchored to its own L3 project domain.
	if _, err := db.Search(SearchQuery{L3ID: l3A}); err != nil {
		t.Fatalf("search A: %v", err)
	}
	if _, err := db.Search(SearchQuery{L3ID: l3B}); err != nil {
		t.Fatalf("search B: %v", err)
	}
	scenesA, err := db.ListScenesByL3(l3A)
	if err != nil {
		t.Fatalf("list by l3 A: %v", err)
	}
	if len(scenesA) == 0 {
		t.Fatal("ListScenesByL3(A) should return the l3A-anchored scene")
	}
	for _, sc := range scenesA {
		if sc.L3ID != l3A {
			t.Fatalf("scene %s should be anchored to %s, got %s", sc.SceneID, l3A, sc.L3ID)
		}
		if !isHexID(sc.SceneID) {
			t.Fatalf("scene id %s should be 16 hex", sc.SceneID)
		}
	}
	scenesB, err := db.ListScenesByL3(l3B)
	if err != nil {
		t.Fatalf("list by l3 B: %v", err)
	}
	if len(scenesB) == 0 {
		t.Fatal("ListScenesByL3(B) should return the l3B-anchored scene")
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

// TestSurfaceSetSceneL3IDCorrection verifies the admin correction entry: a
// non-force Set cannot move an anchored scene, force=true re-anchors it, and
// an empty l3ID clears the anchor.
func TestSurfaceSetSceneL3IDCorrection(t *testing.T) {
	db := openSurfaceDB(t)
	l3A := common.FormatHash(common.HashID("cor-a"))
	l3B := common.FormatHash(common.HashID("cor-b"))
	if _, err := db.Search(SearchQuery{L3ID: l3A}); err != nil {
		t.Fatalf("search: %v", err)
	}
	scenesA, err := db.ListScenesByL3(l3A)
	if err != nil || len(scenesA) == 0 {
		t.Fatalf("expected an l3A-anchored scene: %+v err=%v", scenesA, err)
	}
	sceneID := scenesA[0].SceneID

	// Write-once: a non-force Set to another domain is a no-op.
	if err := db.SetSceneL3ID(sceneID, l3B, false); err != nil {
		t.Fatalf("non-force set: %v", err)
	}
	if scenes, _ := db.ListScenesByL3(l3A); len(scenes) != 1 {
		t.Fatalf("non-force set must not move the anchor: %+v", scenes)
	}
	// Force re-anchors the scene to l3B.
	if err := db.SetSceneL3ID(sceneID, l3B, true); err != nil {
		t.Fatalf("force set: %v", err)
	}
	if scenes, _ := db.ListScenesByL3(l3A); len(scenes) != 0 {
		t.Fatalf("force set must leave l3A: %+v", scenes)
	}
	if scenes, _ := db.ListScenesByL3(l3B); len(scenes) != 1 {
		t.Fatalf("force set must land in l3B: %+v", scenes)
	}
	// Empty l3ID clears the anchor.
	if err := db.SetSceneL3ID(sceneID, "", false); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if scenes, _ := db.ListScenesByL3(l3B); len(scenes) != 0 {
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
		if err := db.AppendTrajectory(turn, TrajectorySlot{EventType: "llm_request", Timestamp: now}); err != nil {
			t.Fatal(err)
		}
	}
	ev := TrajectorySlot{EventType: "plan_step", Timestamp: now}
	calls := map[string]func() error{
		"PlanAppend":  func() error { return db.PlanAppend(zero, "1", ev) },
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
