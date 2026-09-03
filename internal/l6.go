// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 big methods of the composition root: host-appended event log with one
// trajectory per agent turn, keyed by that turn's L2 topic id (Search issues
// the id, Update settles the turn into it), plus Crystallize (L6 → L5) as an
// explicit host-triggered step over one turn's events. Plan nodes share the
// record space under their own key (the plan id). Retention is internal:
// Dream drops events older than trajectoryRetention, and no delete API is
// exposed. Every write keeps the domain's TrajIndex in sync under the
// domain lock. The plan and trajectory steps live in internal/plan and
// internal/trajectory.

package internal

import (
	"cmp"
	"context"
	"slices"

	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/plan"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/trajectory"
)

// AppendTrajectory appends one event to the turn opened by Search, keyed by
// that turn's topic id; Seq comes from the domain's TrajIndex (max + 1), so
// the host never counts sequences.
func (db *DB) AppendTrajectory(agentID uint64, turnID string, ev core.TrajectorySlot) error {
	ac, parsed, err := db.lockSession(agentID, turnID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	if ev.EventType == "" || ev.Timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "EventType and Timestamp are required")
	}
	if len(ev.Payload) > trajectory.MaxEventPayload {
		ev.Payload = common.TruncateUTF8(ev.Payload, trajectory.MaxEventPayload)
	}
	maxSeq, _ := ac.Traj.MaxSeq(parsed)
	ev.SessionID = parsed
	// The key IS the turn's topic, so the record's own topic link cannot
	// disagree with it.
	ev.TopicID = parsed
	ev.Seq = maxSeq + 1
	// An appended event must never masquerade as a plan node: force the
	// record to bare-event semantics so host-supplied plan-node fields
	// (NodeType/PlanID/ParentID/NodePath/Status/Summary/PlanNodeRef) are
	// always cleared on write.
	ev.NodeType = core.NodeTypeEvent
	ev.PlanID = 0
	ev.ParentID = 0
	ev.NodePath = ""
	ev.Status = 0
	ev.Summary = ""
	ev.PlanNodeRef = 0
	idHash, err := repo.AppendTrajectory(db.engine, agentID, ev)
	if err != nil {
		return err
	}
	ac.Traj.Append(parsed, ev.Seq, idHash, ev.Timestamp)
	return nil
}

// ReadTrajectory returns one turn's trajectory events ordered by Seq; turnID
// is the topic id Search issued for that turn.
func (db *DB) ReadTrajectory(agentID uint64, turnID string) ([]core.TrajectorySlot, error) {
	ac, parsed, err := db.lockSession(agentID, turnID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	return trajectory.ReadTurn(db.engine, agentID, ac, parsed), nil
}

// ListTrajectorySessions summarizes every turn of the domain's L6 log under
// the domain lock (same serialization contract as Append).
func (db *DB) ListTrajectorySessions(agentID uint64) ([]core.TrajectorySessionSummary, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	sums := ac.Traj.Summaries()
	out := make([]core.TrajectorySessionSummary, 0, len(sums))
	for _, s := range sums {
		out = append(out, core.TrajectorySessionSummary{
			SessionID:    common.FormatHash(s.SessionID),
			Steps:        s.Steps,
			LastAppendAt: s.LastAt,
		})
	}
	slices.SortFunc(out, func(x, y core.TrajectorySessionSummary) int {
		return cmp.Compare(x.SessionID, y.SessionID)
	})
	return out, nil
}

// PlanAppend appends one event to a plan node (planID hex, nodePath like
// "1.2.1") without advancing the plan. If the node does not exist it is
// created as a pending plan node.
func (db *DB) PlanAppend(agentID uint64, planID string, nodePath string, ev core.TrajectorySlot) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	ph, err := plan.ParsePlanID(planID)
	if err != nil {
		return err
	}
	nodeID, err := plan.EnsureNode(ac, agentID, ph, nodePath)
	if err != nil {
		return err
	}
	return plan.AppendEventLocked(ac, agentID, ph, nodeID, ev)
}

// PlanCommit advances a plan node to a status (with optional summary) and
// appends the step event, then rolls up Done children summaries into any
// parent Summary (Model A: a parent becomes Done only when the host
// explicitly commits it here).
func (db *DB) PlanCommit(agentID uint64, planID string, nodePath string, ev core.TrajectorySlot, status PlanStatus, summary string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	ph, err := plan.ParsePlanID(planID)
	if err != nil {
		return err
	}
	u8, err := plan.StatusToU8(status)
	if err != nil {
		return err
	}
	nodeID, err := plan.EnsureNode(ac, agentID, ph, nodePath)
	if err != nil {
		return err
	}
	if err := plan.UpdateNodeLocked(ac, agentID, nodeID, u8, summary); err != nil {
		return err
	}
	if err := plan.AppendEventLocked(ac, agentID, ph, nodeID, ev); err != nil {
		return err
	}
	return plan.RollupTree(ac, agentID, ph)
}

// PlanState returns the plan tree view of the actual stored statuses (no
// auto-fold: a parent becomes Done only via explicit host PlanCommit).
func (db *DB) PlanState(agentID uint64, planID string) (*PlanTree, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	ph, err := plan.ParsePlanID(planID)
	if err != nil {
		return nil, err
	}
	return plan.BuildTree(ac, agentID, ph)
}

// Crystallize extracts L5 capability candidates from one turn's trajectory
// (L6 → L5), keyed by the topic id Search issued for that turn — or by a plan
// id, when the host bound these turns' events to a plan tree. The LLM
// receives the existing capability catalog so repeated crystallization
// reuses or merges instead of duplicating. The whole pipeline holds the
// domain lock, exactly as Update and Dream do: another operation on this
// agent waits (so a slow LLM round-trip stalls same-domain writes), while
// other domains stay parallel.
func (db *DB) Crystallize(ctx context.Context, agentID uint64, turnID string) (*CrystallizeResult, error) {
	ac, parsed, err := db.lockSession(agentID, turnID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	// Events land in Seq order; only the payload budget can shorten the turn.
	events := trajectory.TrimByBudget(trajectory.ReadTurn(db.engine, agentID, ac, parsed), trajectory.MaxCrystallizePayload)
	if len(events) == 0 {
		return nil, common.NewError(common.ErrNotFound, "no trajectory for turn "+turnID)
	}
	existing := capability.ActiveOnly(core.CollectAllCapabilities(db.engine, agentID))
	out, err := llmops.Crystallize(ctx, db.llm, events, existing)
	if err != nil {
		return nil, err
	}

	if db.closed.Load() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	result := &CrystallizeResult{CreatedIDs: []string{}, ReusedIDs: []string{}, MergedIDs: []string{}}
	for _, cand := range out.Capabilities {
		if err := trajectory.ApplyCandidate(db.engine, agentID, cand, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}
