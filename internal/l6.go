// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 big methods of the composition root: one key per agent turn — the topic
// id Search issues for it and Update settles it — holding that turn's
// trajectory events and the plan tree it opened, so a single read of a turn
// yields both its event log and its step statuses. Crystallize is an explicit
// host-triggered step over one key's events. Retention is internal: Dream
// drops records older than the retention window, and no delete API is
// exposed. Every write keeps the domain's TrajIndex and PlanCache in sync
// under the domain lock. The plan and trajectory steps live in internal/plan
// and internal/trajectory.

package internal

import (
	"cmp"
	"context"
	"slices"

	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/plan"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/trajectory"
)

// AppendTrajectory appends one event to the L6 log of `topicID`, the turn
// Search issued it for. With an empty nodePath the record is a bare turn
// event; with a nodePath it hangs on that plan node, which is created as
// pending when missing — so this is also how a host adds a step. Seq comes
// from the domain's TrajIndex (max + 1), so the host never counts sequences.
// An event that does not satisfy the write contract is refused before
// anything is stored, including before a node is created or advanced.
func (db *DB) AppendTrajectory(agentID uint64, topicID string, nodePath string, ev core.TrajectorySlot) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	th, err := trajectory.ParseTopicID(topicID)
	if err != nil {
		return err
	}
	if err := trajectory.ValidateEvent(ev); err != nil {
		return err
	}
	if nodePath == "" {
		return appendTurnEvent(ac, agentID, th, ev)
	}
	if _, err := plan.EnsureNode(ac, agentID, th, nodePath); err != nil {
		return err
	}
	return plan.AppendEventLocked(ac, agentID, th, nodePath, ev)
}

// appendTurnEvent writes one bare turn event under its topic id, forcing
// event semantics so a host-supplied plan-node field cannot pollute the tree.
// The domain lock must be held.
func appendTurnEvent(ac *domain.Context, agentID, topicID uint64, ev core.TrajectorySlot) error {
	// An unknown key is the first event of that turn, so Seq starts at 1.
	maxSeq, _ := ac.Traj.MaxSeq(topicID)
	ev.SessionID = topicID
	ev.Seq = maxSeq + 1
	ev.NodeType = core.NodeTypeEvent
	ev.ParentID = 0
	ev.NodePath = ""
	ev.Status = 0
	ev.Summary = ""
	ev.Title = ""
	ev.FinishedAt = 0
	ev.PlanType = ""
	ev.PlanNodeRef = 0
	idHash, err := repo.AppendTrajectory(ac.Engine, agentID, ev)
	if err != nil {
		return err
	}
	ac.Traj.Append(topicID, ev.Seq, idHash, ev.Timestamp)
	return nil
}

// ReadTrajectory returns one turn's L6 records ordered by Seq; turnID is the
// topic id Search issued for that turn.
func (db *DB) ReadTrajectory(agentID uint64, turnID string) ([]core.TrajectorySlot, error) {
	ac, parsed, err := db.lockSession(agentID, turnID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	return trajectory.ReadTurn(db.engine, agentID, ac, parsed)
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

// PlanCommit advances a plan node and appends the step event, then rolls up
// Done children summaries into any parent Summary (Model A: a parent becomes
// Done only when the host explicitly commits it here). `topicID` names the turn
// that owns the plan, and a node missing along nodePath is created — this is
// how a host adds a step. step carries the node's own fields; one left blank
// keeps what is stored. Both the status and the event are validated first: a
// commit this call refuses leaves the node's status, its summary and the
// rollup untouched.
func (db *DB) PlanCommit(agentID uint64, topicID string, nodePath string, ev core.TrajectorySlot, step plan.Step) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	th, err := trajectory.ParseTopicID(topicID)
	if err != nil {
		return err
	}
	// Checked before the tree moves: an unknown status must not leave a node
	// chain created behind it.
	if _, err := plan.StatusToU8(step.Status); err != nil {
		return err
	}
	if err := trajectory.ValidateEvent(ev); err != nil {
		return err
	}
	nodeID, err := plan.EnsureNode(ac, agentID, th, nodePath)
	if err != nil {
		return err
	}
	if err := plan.CommitNode(ac, agentID, nodeID, step); err != nil {
		return err
	}
	if err := plan.AppendEventLocked(ac, agentID, th, nodePath, ev); err != nil {
		return err
	}
	return plan.RollupTree(ac, agentID, th)
}

// PlanState returns the plan tree of one turn (the topic id that opened it) as
// the actual stored statuses — no auto-fold: a parent becomes Done only via
// explicit host PlanCommit.
func (db *DB) PlanState(agentID uint64, topicID string) (*PlanTree, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	th, err := trajectory.ParseTopicID(topicID)
	if err != nil {
		return nil, err
	}
	return plan.BuildTree(ac, agentID, th)
}

// Crystallize extracts reusable capability candidates from one turn's
// trajectory (L6 → host), keyed by the topic id Search issued for that turn.
// existing lists the cards the host already knows (its own capability
// directory) so the LLM can reuse or merge instead of duplicating. The
// engine returns candidates only: storing them is the host's job, so there
// is no L5 record layer behind this call anymore. The pipeline holds the
// domain lock, exactly as Update and Dream do: another operation on this
// agent waits (so a slow LLM round-trip stalls same-domain writes), while
// other domains stay parallel.
func (db *DB) Crystallize(ctx context.Context, agentID uint64, turnID string, existing []capability.CapabilityImport) (*llmops.CrystallizeOutput, error) {
	ac, parsed, err := db.lockSession(agentID, turnID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	// Events land in Seq order; only the payload budget can shorten the turn.
	stored, err := trajectory.ReadTurn(db.engine, agentID, ac, parsed)
	if err != nil {
		return nil, err
	}
	events := trajectory.TrimByBudget(stored, trajectory.MaxCrystallizePayload)
	if len(events) == 0 {
		return nil, common.NewError(common.ErrNotFound, "no trajectory for turn "+turnID)
	}
	return llmops.Crystallize(ctx, db.llm, events, existing)
}
