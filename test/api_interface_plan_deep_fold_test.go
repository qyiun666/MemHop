// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Acceptance item 6's "return the plan as context, compressed automatically", measured at a depth
// nothing has driven: the fold is pinned two levels down, where a parent reads what its children
// hold. Two claims exist only at depth — a fold has to reach the **top** of what the host renders
// into its prompt, or the root it pastes is a step with no conclusion; and a change underneath has
// to re-derive **every** level above it in the same pass, not just the step that was touched.
//
// What happens while a branch is unsettled is the decision
// TestInterfaceParentFoldFollowsTheBranchItSummarizes already made at two levels — hold the last
// complete fold rather than grow a partial one or take a conclusion back. This asserts that answer
// one level deeper, so it cannot be quietly re-litigated through the depth-3 path.

package test

import (
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfacePlanFoldReachesTheRootThroughThreeLevels(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	openTurn(t, db, sceneID)
	done, reopen := memhop.PlanStatusDone, memhop.PlanStatusInProgress

	root := mustCreate(t, db, 0, "定位回归")
	mid := mustCreate(t, db, root, "二分范围")
	leaf := mustCreate(t, db, mid, "最小复现")

	// A settled leaf folds nothing above it while its ancestors are open: a step reports a
	// conclusion only once the host has declared that step finished.
	mustUpdate(t, db, leaf, done, "第 7 步引入的")
	tree := mustPlanState(t, db)
	if got := findPlanNode(t, tree, mid); got.Summary != "" {
		t.Fatalf("an open middle step already folded: %+v", got)
	}
	if got := findPlanNode(t, tree, root); got.Summary != "" {
		t.Fatalf("an open root already folded: %+v", got)
	}

	// The middle step finishing folds it from its settled child; the root is still open, so it
	// waits rather than reading like a finished plan.
	mustUpdate(t, db, mid, done, "")
	tree = mustPlanState(t, db)
	if got := findPlanNode(t, tree, mid); got.Summary != "第 7 步引入的" {
		t.Fatalf("the middle step did not fold from its settled child: %+v", got)
	}
	if got := findPlanNode(t, tree, root); got.Summary != "" {
		t.Fatalf("the root folded while still open: %+v", got)
	}

	// The case the two-level fold never reached: the root finishing has to fold from the middle
	// step's *folded* text in the same pass, so the deepest conclusion arrives at the top of what
	// the host renders, verbatim and without a second call.
	mustUpdate(t, db, root, done, "")
	tree = mustPlanState(t, db)
	if got := findPlanNode(t, tree, root); got.Summary != "第 7 步引入的" {
		t.Fatalf("the root's fold lost the leaf's conclusion: %+v", got)
	}
	if len(tree.Roots) != 1 || len(tree.Roots[0].Children) != 1 ||
		len(tree.Roots[0].Children[0].Children) != 1 {
		t.Fatalf("three levels did not come back nested: %+v", tree.Roots)
	}
	if tree.TotalCount != 3 || tree.DoneCount != 3 {
		t.Fatalf("counts over three levels: total=%d done=%d, want 3/3", tree.TotalCount, tree.DoneCount)
	}

	// An unsettled branch holds the fold at every level above it: no partial fold, no withdrawal.
	// The cost of that choice is the window — the summary stays readable while the step that
	// produced it says it is working again — and what carries the difference is the step's own
	// `Status`, which is why the read below checks both.
	mustUpdate(t, db, leaf, reopen, "")
	tree = mustPlanState(t, db)
	if got := findPlanNode(t, tree, mid); got.Summary != "第 7 步引入的" {
		t.Fatalf("an unsettled branch took back the middle step's fold: %+v", got)
	}
	if got := findPlanNode(t, tree, root); got.Summary != "第 7 步引入的" {
		t.Fatalf("an unsettled branch re-folded the root: %+v", got)
	}
	if got := findPlanNode(t, tree, leaf); got.Summary != "第 7 步引入的" ||
		got.Status != string(memhop.PlanStatusInProgress) || got.FinishedAt != 0 {
		t.Fatalf("re-opening the leaf disturbed its own record: %+v", got)
	}
	// Settling it again with other words re-derives both levels in one pass — a fold that only
	// rewrote the step that changed would leave the root quoting a conclusion the tree replaced.
	mustUpdate(t, db, leaf, done, "其实是第 9 步的并发")
	tree = mustPlanState(t, db)
	if got := findPlanNode(t, tree, mid); got.Summary != "其实是第 9 步的并发" {
		t.Fatalf("the middle step did not re-fold from the new conclusion: %+v", got)
	}
	if got := findPlanNode(t, tree, root); got.Summary != "其实是第 9 步的并发" {
		t.Fatalf("the root still quotes the conclusion the tree replaced: %+v", got)
	}

	// Host text at the middle level is nobody else's to rewrite, and the root folds from it just
	// as it folds from a derived one — mixed ownership at depth.
	mustUpdate(t, db, mid, done, "范围收在 7~9 步")
	tree = mustPlanState(t, db)
	if got := findPlanNode(t, tree, mid); got.Summary != "范围收在 7~9 步" {
		t.Fatalf("the host's own text at the middle level was overwritten: %+v", got)
	}
	if got := findPlanNode(t, tree, root); got.Summary != "范围收在 7~9 步" {
		t.Fatalf("the root did not fold from the host's text below it: %+v", got)
	}
	// And once the middle step's text is the host's, the walk has no business near it or near
	// what the root derived from it: re-opening the leaf leaves both levels as the host left them.
	mustUpdate(t, db, leaf, reopen, "")
	tree = mustPlanState(t, db)
	if got := findPlanNode(t, tree, mid); got.Summary != "范围收在 7~9 步" {
		t.Fatalf("the walk disturbed the host's own text: %+v", got)
	}
	if got := findPlanNode(t, tree, root); got.Summary != "范围收在 7~9 步" {
		t.Fatalf("an unsettled branch below a host-owned fold re-folded the root: %+v", got)
	}
}
