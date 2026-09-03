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

// planEventTypes is the vocabulary for plan-bound events: the documented
// trajectory step types plus plan_step for plan progress commits. Plain
// AppendTrajectory stays free-form (host-owned turns).
var planEventTypes = map[string]struct{}{
	"llm_request": {}, "llm_output": {}, "tool_call": {}, "tool_result": {},
	"subagent_spawn": {}, "subagent_done": {}, "context_inject": {},
	"ask_user": {}, "user_reply": {}, "plan_step": {},
}

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
// PlanNodeRef, then reuses the existing per-turn Sequencer + TrajIndex.
// Callers hold ac.Mu.
func AppendEventLocked(ac *domain.Context, agentID, planID, nodeID uint64, ev core.TrajectorySlot) error {
	if ev.EventType == "" || ev.Timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "EventType and Timestamp are required")
	}
	if _, ok := planEventTypes[ev.EventType]; !ok {
		return common.NewError(common.ErrInvalidQuery, "unknown plan event type: "+ev.EventType)
	}
	if len(ev.Payload) > trajectory.MaxEventPayload {
		ev.Payload = common.TruncateUTF8(ev.Payload, trajectory.MaxEventPayload)
	}
	maxSeq, _ := ac.Traj.MaxSeq(planID)
	ev.SessionID = planID
	ev.Seq = maxSeq + 1
	ev.PlanNodeRef = nodeID
	// An appended plan event is a plain event bound to the node: force the
	// record to event semantics so a host cannot inject NodeType=Plan (or
	// other plan-node fields) and pollute the tree view.
	ev.NodeType = core.NodeTypeEvent
	ev.PlanID = planID
	ev.ParentID = 0
	ev.NodePath = ""
	ev.Status = 0
	ev.Summary = ""
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
