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

// EnsureNode resolves nodePath to a plan node id under `topicID` (the turn that
// opened the plan), creating the node chain — root first, then each segment —
// as pending when missing. Callers hold ac.Mu.
func EnsureNode(ac *domain.Context, agentID uint64, topicID uint64, nodePath string) (uint64, error) {
	ids, err := SplitNodePath(nodePath)
	if err != nil {
		return 0, err
	}
	var parentID uint64
	var pathSoFar []string
	for _, seg := range ids {
		pathSoFar = append(pathSoFar, seg)
		np := strings.Join(pathSoFar, ".")
		id := core.HashPlanNode(topicID, np)
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
				IDHash: id, SessionID: topicID, Seq: uint64(len(pathSoFar)),
				NodeType: core.NodeTypePlan, ParentID: parentID,
				NodePath: np, Status: core.StatusPending, Timestamp: time.Now().UnixMilli(),
			}
			if _, err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
				return 0, err
			}
			ac.Plans.UpsertNode(topicID, node)
		}
		parentID = id
	}
	return parentID, nil
}

// AppendEventLocked writes one event bound to a plan node under `topicID`,
// stamping the record with the node it hangs on and its path, then reuses the
// per-key Sequencer + TrajIndex. nodePath names the step: PlanNodeRef is a
// library hash nothing on the public surface derives, so the path is what lets
// ReadTrajectory attribute an event to a step. An event names itself — the
// engine never branches on EventType, so it takes any non-empty name a bare
// turn event takes. Callers that create or advance a node first run the same
// check before they touch the tree, so a refused write leaves the status, the
// summary and the rollup exactly as they were. Callers hold ac.Mu.
func AppendEventLocked(ac *domain.Context, agentID, topicID uint64, nodePath string, ev core.TrajectorySlot) error {
	if err := trajectory.ValidateEvent(ev); err != nil {
		return err
	}
	// An unknown key is the first event of that key, so Seq starts at 1.
	maxSeq, _ := ac.Traj.MaxSeq(topicID)
	ev.SessionID = topicID
	ev.Seq = maxSeq + 1
	ev.PlanNodeRef = core.HashPlanNode(topicID, nodePath)
	ev.NodePath = nodePath
	// An appended plan event is a plain event bound to the node: force the
	// record to event semantics so a host cannot inject NodeType=Plan (or
	// other plan-node fields) and pollute the tree view.
	ev.NodeType = core.NodeTypeEvent
	ev.ParentID = 0
	ev.Status = 0
	ev.Summary = ""
	ev.Title = ""
	ev.PlanType = ""
	idHash, err := repo.AppendTrajectory(ac.Engine, agentID, ev)
	if err != nil {
		return err
	}
	ac.Traj.Append(topicID, ev.Seq, idHash, ev.Timestamp)
	ac.Plans.UpsertEvent(topicID, ev.PlanNodeRef, ev)
	return nil
}

// CommitNode applies one host commit to a plan node: its status plus the
// node's own Title/PlanType/Summary, where a field Step leaves blank keeps what
// is stored — re-committing a step never erases its title or a summary already
// folded into it. An unknown status is refused before anything is written. A
// terminal status records FinishedAt exactly once. It deliberately does NOT
// touch the event TrajIndex: plan nodes are not events and must not occupy the
// Seq space, otherwise a deep then shallow commit would collapse it and
// overwrite a prior event. Callers hold ac.Mu.
func CommitNode(ac *domain.Context, agentID, nodeID uint64, step Step) error {
	u8, err := StatusToU8(step.Status)
	if err != nil {
		return err
	}
	node, err := core.ReadTrajectorySlot(ac.Engine, agentID, nodeID)
	if err != nil {
		return err
	}
	node.Status = u8
	if step.Summary != "" {
		node.Summary = step.Summary
	}
	if step.Title != "" {
		node.Title = step.Title
	}
	if step.PlanType != "" {
		node.PlanType = step.PlanType
	}
	now := time.Now().UnixMilli()
	if IsTerminalStatus(node.Status) && node.FinishedAt == 0 {
		node.FinishedAt = now
	}
	node.Timestamp = now
	if _, err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(node.SessionID, node)
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
	ac.Plans.UpsertNode(node.SessionID, node)
	return nil
}
