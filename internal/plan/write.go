// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package plan

import (
	"fmt"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// CreateNode adds one step to a turn's plan tree and returns its ordinal. A root
// is created the same way as a child — ParentSeq 0 — so a step has exactly one
// path into the tree. The step it hangs under has to exist already: creating
// under an unknown parent is refused rather than satisfied by quietly growing one,
// which is how a mistyped parent would otherwise end up as a branch nobody
// planned. Callers hold ac.Mu.
func CreateNode(ac *domain.Context, agentID uint64, spec NodeSpec) (uint32, error) {
	if spec.ParentSeq != 0 && !ac.Plans.HasSeq(spec.TopicID, spec.ParentSeq) {
		return 0, common.NewError(common.ErrNotFound,
			fmt.Sprintf("plan step %d of turn %s does not exist",
				spec.ParentSeq, common.FormatHash(spec.TopicID)))
	}
	now := time.Now().UnixMilli()
	seq := ac.Plans.NextSeq(spec.TopicID)
	idHash := core.HashPlanNode(spec.TopicID, seq)
	// The offered ordinal comes from the mirror, and a record the mirror's collection
	// could not decode is missing from it in exactly the shape a free ordinal has —
	// the address is derived from (topic, seq), so the number survives a payload that
	// does not. A read that fails for any reason but "absent" therefore stops the
	// create: a slot this call cannot prove empty is not a slot it overwrites.
	if _, err := core.ReadPlanNode(ac.Engine, agentID, idHash); err != nil && common.CodeOf(err) != common.ErrNotFound {
		return 0, common.NewError(common.CodeOf(err), "read the address of the new plan step", err)
	}
	node := &core.PlanNode{
		IDHash:    idHash,
		TopicID:   spec.TopicID,
		Seq:       seq,
		ParentSeq: spec.ParentSeq,
		Title:     spec.Title,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
		return 0, err
	}
	ac.Plans.UpsertNode(spec.TopicID, node)
	return seq, nil
}

// UpdateNode applies one step's restatement to a plan node: its status plus the
// node's own Title/Summary, where a field left blank keeps what is stored —
// updating a step never erases its title or a summary already folded into it. A
// terminal status records FinishedAt exactly once, and a step restated back to
// in progress loses it: a completion time left on a step that is running again
// would read as a finished one. Updating a node writes one node record and
// nothing else — restating a step cannot add, reorder or overwrite any other
// record.
// Callers hold ac.Mu.
func UpdateNode(ac *domain.Context, agentID uint64, step Step) error {
	u8, err := StatusToU8(step.Status)
	if err != nil {
		return err
	}
	node, err := core.ReadPlanNode(ac.Engine, agentID, core.HashPlanNode(step.TopicID, step.Seq))
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
	now := time.Now().UnixMilli()
	if IsTerminalStatus(node.Status) {
		if node.FinishedAt == 0 {
			node.FinishedAt = now
		}
	} else {
		node.FinishedAt = 0
	}
	node.UpdatedAt = now
	if _, err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(node.TopicID, node)
	return nil
}

// UpdateNodeSummaryLocked sets a plan node's Summary without touching its
// Status — a node's Status changes only where a caller writes it, never because
// a fold ran. Callers hold ac.Mu.
func UpdateNodeSummaryLocked(ac *domain.Context, agentID, nodeID uint64, summary string) error {
	node, err := core.ReadPlanNode(ac.Engine, agentID, nodeID)
	if err != nil {
		return err
	}
	node.Summary = summary
	node.UpdatedAt = time.Now().UnixMilli()
	if _, err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(node.TopicID, node)
	return nil
}
