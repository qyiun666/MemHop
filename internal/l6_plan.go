// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 plan big methods of the composition root: whole-tree sync and wipe.
// The plan mechanics live in internal/plan.

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/plan"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SyncPlanTree replaces a whole plan tree from the host's authoritative
// snapshot. It mutates only node structure/fields (add missing nodes, update
// the fields the snapshot fills, delete vanished nodes with their bound events)
// and never produces a plan_step event nor touches the event Seq space. A blank
// Title/PlanType/Status/Summary inherits that node's stored value, so a partial
// snapshot never rewinds a completed step or erases a folded summary. A node
// reaches a terminal status via its input Status and records FinishedAt once.
// The planID is preserved so host references survive a re-plan.
//
// A nil root wipes the plan instead: every node and bound event is removed and
// the event Seq space restarts at 1, while the planID is kept so the host's
// next task can start on the id it already holds.
func (db *DB) SyncPlanTree(agentID uint64, planID string, root *PlanNode) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	ph, err := plan.ParsePlanID(planID)
	if err != nil {
		return err
	}
	if root == nil {
		if _, err := repo.DeletePlanRecords(db.engine, agentID, ph); err != nil {
			return err
		}
		ac.Traj.RemoveSession(ph)
		ac.Plans.RemovePlan(ph)
		return nil
	}
	if root.NodePath == "" {
		return common.NewError(common.ErrInvalidQuery, "plan root required")
	}
	newPaths := make(map[string]struct{})
	if err := plan.CollectPaths(root, "", newPaths); err != nil {
		return err
	}
	existing := make(map[string]core.TrajectorySlot)
	for _, n := range repo.CollectPlanNodes(db.engine, agentID, ph) {
		existing[n.NodePath] = n
	}
	if err := plan.SyncNodeLocked(ac, agentID, ph, root); err != nil {
		return err
	}
	for p := range existing {
		if _, ok := newPaths[p]; ok {
			continue
		}
		// Delete only the shallowest vanished node: an ancestor that is also
		// vanished cascade-deletes this subtree, so it is skipped here.
		if parent := plan.ParentPath(p); parent != "" {
			if _, parentExists := existing[parent]; parentExists {
				if _, parentKept := newPaths[parent]; !parentKept {
					continue
				}
			}
		}
		deleted, err := repo.DeletePlanNodeBranch(db.engine, agentID, ph, p)
		if err != nil {
			return err
		}
		// Mirror the removal in both caches that name those records: the plan
		// cache for the tree view, the trajectory index for the event log. An
		// index entry left pointing at a deleted record makes every later read
		// of the plan's trajectory fail.
		ac.Plans.RemoveNodeBranch(ph, p)
		ac.Traj.RemoveEvents(ph, deleted)
	}
	return nil
}
