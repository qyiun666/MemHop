// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 big methods of the composition root. L5 is one turn's plan tree, keyed by the
// topic id Search issued for that turn, with each step addressed by its ordinal
// inside the tree. The events a step produced are L4 content under that same key:
// the host appends them and reads them back through the L4 surface with a Kind
// condition. Retention is internal — Dream drops plan records older than the
// retention window and no delete API is exposed. Every write keeps the domain's
// PlanCache in sync under the domain lock. The steps themselves live in
// internal/plan.

package internal

import (
	"github.com/qyiun666/MemHop/internal/plan"
)

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
	ac, th, err := db.lockSession(agentID, topicID)
	if err != nil {
		return 0, err
	}
	defer ac.Mu.Unlock()
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
// status, the parent gets its summary. A restatement that is refused changes
// nothing — the status word is checked before the node is read, so no
// half-applied step can leave the host unsure which of its writes landed. The
// rollup is a separate step after that write has landed: if it fails, the step is
// restated and only the parent's folded summary is missing, which the next
// restatement of that branch folds again. Nothing here touches the turn's
// content: the events a step produced are L4 records the host appends itself, so
// restating a step can never rewrite what a turn recorded.
func (db *DB) PlanNodeUpdate(agentID uint64, topicID string, step plan.Step) error {
	ac, th, err := db.lockSession(agentID, topicID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	step.TopicID = th
	if err := plan.UpdateNode(ac, agentID, step); err != nil {
		return err
	}
	return plan.RollupTree(ac, agentID, th)
}

// PlanState returns the plan tree of one turn (the topic id that opened it) as
// the actual stored statuses — no auto-fold: a parent becomes Done only where
// the host declared it so. A turn that holds no tree answers with an empty one
// rather than an error, the same answer a turn whose tree the retention window
// swept gives: whether the key is known is a question about L2, and this read
// spans none of it.
func (db *DB) PlanState(agentID uint64, topicID string) (*PlanTree, error) {
	ac, th, err := db.lockSession(agentID, topicID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	return plan.BuildTree(ac, th)
}
