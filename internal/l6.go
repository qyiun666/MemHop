// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 big methods of the composition root. L6 records one thing per agent turn: the
// plan tree keyed by the topic id Search issues for it. The event track that shares
// that key is L4 content — written by AppendArchive and read back through SearchL4
// with a Kind condition — so this file keeps only the enumeration no L4 read gives
// (which turns hold events) and the plan steps. Crystallize is an explicit
// host-triggered step over one key's events. Retention is internal: Dream drops
// records older than the retention window, and no delete API is exposed. Every
// write keeps the domain's L4Index and PlanCache in sync under the domain lock. The
// plan steps live in internal/plan, the content steps in internal/content.

package internal

import (
	"cmp"
	"context"
	"slices"

	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/content"
	"github.com/qyiun666/MemHop/internal/plan"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ListTrajectorySessions enumerates the turns that hold events under the domain
// lock (same serialization contract as AppendArchive). A turn holding only
// dialogue is absent from this list — it recorded no operations.
func (db *DB) ListTrajectorySessions(agentID uint64) ([]core.TrajectorySessionSummary, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	sums := ac.L4.EventSummaries()
	out := make([]core.TrajectorySessionSummary, 0, len(sums))
	for _, s := range sums {
		out = append(out, core.TrajectorySessionSummary{
			SessionID:    common.FormatHash(s.TopicID),
			Events:       s.Events,
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
// rollup exactly as they were.
func (db *DB) PlanCommit(agentID uint64, topicID string, nodePath string, ev core.ArchiveSlot, step plan.Step) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	th, err := content.ParseTopicID(topicID)
	if err != nil {
		return err
	}
	// Checked before the tree moves: an unknown status must not leave a node
	// chain created behind it.
	if _, err := plan.StatusToU8(step.Status); err != nil {
		return err
	}
	// The step being committed owns these two fields: the event is an event, and
	// it belongs to this node whatever the caller named on the record.
	ev.Kind = core.KindEvent
	ev.NodePath = nodePath
	if err := content.ValidateAppend(ev); err != nil {
		return err
	}
	nodeID, err := plan.EnsureNode(ac, agentID, th, nodePath)
	if err != nil {
		return err
	}
	if err := plan.CommitNode(ac, agentID, nodeID, step); err != nil {
		return err
	}
	if _, err := content.Append(ac, agentID, th, ev); err != nil {
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
	th, err := content.ParseTopicID(topicID)
	if err != nil {
		return nil, err
	}
	return plan.BuildTree(ac, th)
}

// Crystallize extracts reusable capability candidates from one turn's events
// (L4 → host), keyed by the topic id Search issued for that turn. existing
// lists the cards the host already knows (its own capability directory) so the
// LLM can reuse or merge instead of duplicating. The engine returns candidates
// only: storing them is the host's job, so there is no L5 record layer behind
// this call anymore. Only the event kind is read — a turn's dialogue originals
// and its plan tree are both out of the prompt. The pipeline holds the
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
	stored, err := content.Read(db.engine, agentID, ac, parsed, core.KindEvent)
	if err != nil {
		return nil, err
	}
	events := content.TrimByBudget(stored, content.MaxCrystallizePayload)
	if len(events) == 0 {
		return nil, common.NewError(common.ErrNotFound, "no trajectory for turn "+turnID)
	}
	return llmops.Crystallize(ctx, db.llm, events, existing)
}
