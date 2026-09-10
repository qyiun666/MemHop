// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 big methods of the composition root. L5 records one thing per agent turn: the
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

// PlanCreate opens a turn's plan tree: it creates the turn's first root step and
// returns that step's ordinal. A turn's tree is keyed by the topic id Search
// issued for it, so `topicID` is the whole address of the tree — and the ordinal
// the call hands back is what later reads and updates that step by.
func (db *DB) PlanCreate(agentID uint64, topicID, title string) (uint32, error) {
	return db.PlanNodeAdd(agentID, topicID, 0, title)
}

// PlanNodeAdd adds one step to a turn's plan tree and returns its ordinal. A
// parentSeq of 0 hangs it at the top level, so this is also how a second root
// joins the forest; any other value names a step this tree already holds —
// CreateNode refuses a step whose parent is missing rather than quietly growing a
// branch to hang it on. This is the only way a node comes into existence: no
// write elsewhere, an event included, creates one.
func (db *DB) PlanNodeAdd(agentID uint64, topicID string, parentSeq uint32, title string) (uint32, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return 0, err
	}
	defer ac.Mu.Unlock()
	th, err := content.ParseTopicID(topicID)
	if err != nil {
		return 0, err
	}
	// A new step lands in progress, so its parent cannot fold a summary from it:
	// the rollup would run and change nothing, which is why no RollupTree call
	// follows a create.
	return plan.CreateNode(ac, agentID, plan.NodeSpec{
		TopicID: th, ParentSeq: parentSeq, Title: title,
	})
}

// PlanNodeUpdate restates one step of a turn's tree: its status, and its own
// Title/Summary, where a field left blank keeps what the node holds. Then the
// tree is rolled up bottom-up, because this is the write that can settle a
// branch: once every direct child of a Done parent has reached a terminal
// status, the parent gets its summary. A refused update changes nothing — the
// status word is checked before the node is read, so no half-applied step can
// leave the host unsure which of its writes landed. Nothing here touches the
// turn's content: the events a step produced are L4 records the host appends
// itself, so restating a step can never rewrite what a turn recorded.
func (db *DB) PlanNodeUpdate(agentID uint64, topicID string, step plan.Step) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	th, err := content.ParseTopicID(topicID)
	if err != nil {
		return err
	}
	step.TopicID = th
	if err := plan.UpdateNode(ac, agentID, step); err != nil {
		return err
	}
	return plan.RollupTree(ac, agentID, th)
}

// PlanState returns the plan tree of one turn (the topic id that opened it) as
// the actual stored statuses — no auto-fold: a parent becomes Done only where
// the host declared it so.
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
