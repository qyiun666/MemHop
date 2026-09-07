// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package plan

import (
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/trajectory"
)

// EnsureNode resolves nodePath to a plan node id, creating the node
// chain (root then children along path) as pending when missing. Callers
// hold ac.Mu.
func EnsureNode(ac *domain.Context, agentID uint64, planID uint64, nodePath string) (uint64, error) {
	ids, err := SplitNodePath(nodePath)
	if err != nil {
		return 0, err
	}
	var parentID uint64
	var pathSoFar []string
	for _, seg := range ids {
		pathSoFar = append(pathSoFar, seg)
		np := strings.Join(pathSoFar, ".")
		id := core.HashPlanNode(planID, np)
		node, err := core.ReadTrajectorySlot(ac.Engine, agentID, id)
		// A transient read error (IO/closed/corruption) is a real failure; only
		// a genuine "record not found" means the node does not exist yet, so we
		// create it. Swallowing transient errors would reset a live node back to
		// pending.
		if err != nil && common.CodeOf(err) != common.ErrNotFound {
			return 0, err
		}
		if err != nil || node == nil || node.NodeType != core.NodeTypePlan {
			node = &core.TrajectorySlot{
				IDHash: id, SessionID: planID, Seq: uint64(len(pathSoFar)),
				NodeType: core.NodeTypePlan, PlanID: planID, ParentID: parentID,
				NodePath: np, Status: core.StatusPending, Timestamp: time.Now().UnixMilli(),
			}
			if _, err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
				return 0, err
			}
			ac.Plans.UpsertNode(planID, node)
		}
		parentID = id
	}
	return parentID, nil
}

// AppendEventLocked writes one event bound to a plan node by filling
// PlanNodeRef and the node's path, then reuses the existing per-turn Sequencer
// + TrajIndex. nodePath is the caller's own path string: stamping it on the
// record is what lets ReadTrajectory say which step an event belongs to, since
// PlanNodeRef is a library hash nothing on the public surface derives. A plan
// bound event names itself: the engine never branches on EventType, so it takes
// any non-empty name a bare turn event takes. Callers that create or advance a
// node first run the same check before they touch the tree, so a refused write
// leaves the status, the summary and the rollup exactly as they were.
// Callers hold ac.Mu.
func AppendEventLocked(ac *domain.Context, agentID, planID, nodeID uint64, nodePath string, ev core.TrajectorySlot) error {
	if err := trajectory.ValidateEvent(ev); err != nil {
		return err
	}
	// An unknown key is the first event of that key, so Seq starts at 1.
	maxSeq, _ := ac.Traj.MaxSeq(planID)
	ev.SessionID = planID
	ev.Seq = maxSeq + 1
	ev.PlanNodeRef = nodeID
	ev.NodePath = nodePath
	// An appended plan event is a plain event bound to the node: force the
	// record to event semantics so a host cannot inject NodeType=Plan (or
	// other plan-node fields) and pollute the tree view.
	ev.NodeType = core.NodeTypeEvent
	ev.PlanID = planID
	ev.ParentID = 0
	ev.Status = 0
	ev.Summary = ""
	ev.PlanType = ""
	idHash, err := repo.AppendTrajectory(ac.Engine, agentID, ev)
	if err != nil {
		return err
	}
	ac.Traj.Append(planID, ev.Seq, idHash, ev.Timestamp)
	ac.Plans.UpsertEvent(planID, nodeID, ev)
	return nil
}

// UpdateNodeLocked sets a plan node's status/summary, re-reading the
// stored node so it preserves its derived IDHash. It deliberately does NOT
// touch the event TrajIndex: plan nodes are not per-turn events and must not
// occupy their Seq space, otherwise a deep then shallow commit would collapse
// the per-plan event Seq and overwrite a prior event. Callers hold ac.Mu.
func UpdateNodeLocked(ac *domain.Context, agentID, nodeID uint64, status uint8, summary string) error {
	node, err := core.ReadTrajectorySlot(ac.Engine, agentID, nodeID)
	if err != nil {
		return err
	}
	node.Status = status
	if summary != "" {
		node.Summary = summary
	}
	now := time.Now().UnixMilli()
	if IsTerminalStatus(status) && node.FinishedAt == 0 {
		node.FinishedAt = now
	}
	node.Timestamp = now
	if _, err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(node.PlanID, node)
	return nil
}

// UpdateNodeSummaryLocked sets a plan node's Summary without touching its
// Status (Model A: a node's Status changes only via explicit host commit).
// Callers hold ac.Mu.
func UpdateNodeSummaryLocked(ac *domain.Context, agentID, nodeID uint64, summary string) error {
	node, err := core.ReadTrajectorySlot(ac.Engine, agentID, nodeID)
	if err != nil {
		return err
	}
	node.Summary = summary
	node.Timestamp = time.Now().UnixMilli()
	if _, err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(node.PlanID, node)
	return nil
}
