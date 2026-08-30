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
// api facade: PlanCommit advances a node, PlanState returns the folded tree
// with string statuses and hex-free ids, and a nested commit folds to root.
func TestSurfacePlanTriForm(t *testing.T) {
	db := openSurfaceDB(t)
	planID := common.FormatHash(common.HashID("plan-1"))
	// Root in_progress → child done → child done → root folds to done.
	if err := db.PlanCommit(planID, "1", TrajectorySlot{EventType: "plan_step", Timestamp: 1000}, "in_progress", ""); err != nil {
		t.Fatalf("commit root: %v", err)
	}
	if err := db.PlanCommit(planID, "1.1", TrajectorySlot{EventType: "plan_step", Timestamp: 1001}, "done", "step A"); err != nil {
		t.Fatalf("commit 1.1: %v", err)
	}
	if err := db.PlanCommit(planID, "1.2", TrajectorySlot{EventType: "plan_step", Timestamp: 1002}, "done", "step B"); err != nil {
		t.Fatalf("commit 1.2: %v", err)
	}
	tree, err := db.PlanState(planID)
	if err != nil {
		t.Fatalf("plan state: %v", err)
	}
	if tree.Root.Status != "done" {
		t.Fatalf("root should fold to done, got %s", tree.Root.Status)
	}
	if tree.TotalCount != 3 || tree.DoneCount != 3 {
		t.Fatalf("counts: total=%d done=%d", tree.TotalCount, tree.DoneCount)
	}
	if tree.Root.Summary == "" {
		t.Fatal("root summary should be concatenated from children")
	}
	// Child nodes carry string status and are well-formed.
	if len(tree.Root.Children) != 2 {
		t.Fatalf("root should have 2 children, got %d", len(tree.Root.Children))
	}
	for _, c := range tree.Root.Children {
		if c.Status != "done" || c.Summary == "" {
			t.Fatalf("child should be done with summary: %+v", c)
		}
	}
	// PlanAppend does not advance; it just binds an event to a node.
	if err := db.PlanAppend(planID, "1.1.1", TrajectorySlot{EventType: "tool_call", Timestamp: 2000}); err != nil {
		t.Fatalf("plan append: %v", err)
	}
}

// TestSurfaceListScenesByL3 verifies scenes anchored to an L3 domain are
// listed with hex ids after a scoped Search creates/anchors them, and that
// two different L3 domains yield DISJOINT scene sets (the exclusion branch).
func TestSurfaceListScenesByL3(t *testing.T) {
	db := openSurfaceDB(t)
	ctx := context.Background()
	l3A := common.FormatHash(common.HashID("l3-proj-a"))
	l3B := common.FormatHash(common.HashID("l3-proj-b"))
	// Two separate scoped Searches anchor two distinct scenes to different
	// L3 domains (distinct text+timestamp ⇒ distinct scene names/ids).
	if _, err := db.Search(ctx, SearchQuery{Text: "rust ownership", AutoCreate: true, Timestamp: 1000, L3ID: &l3A}); err != nil {
		t.Fatalf("search A: %v", err)
	}
	if _, err := db.Search(ctx, SearchQuery{Text: "rust borrow checker", AutoCreate: true, Timestamp: 2000, L3ID: &l3B}); err != nil {
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
