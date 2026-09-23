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
// is created the same way as a child — ParentSeq 0. The parent step has to exist
// already: an unknown parent is refused, not quietly grown. Callers hold ac.Mu.
func CreateNode(ac *domain.Context, agentID uint64, spec NodeSpec) (uint32, error) {
	if spec.ParentSeq != 0 && !ac.Plans.HasSeq(spec.TopicID, spec.ParentSeq) {
		return 0, common.NewError(common.ErrNotFound,
			fmt.Sprintf("plan step %d of turn %s does not exist",
				spec.ParentSeq, common.FormatHash(spec.TopicID)))
	}
	now := time.Now().UnixMilli()
	// The turn's own event track reserves ordinals: a step swept past the retention
	// window can leave an event naming it, and the new step must not inherit that work.
	seq := ac.Plans.NextSeq(spec.TopicID, ac.L4.MaxNodeSeq(spec.TopicID))
	idHash := core.HashPlanNode(spec.TopicID, seq)
	// The ordinal comes from the mirror, whose collection skips records it cannot
	// decode — but the address derives from (topic, seq), so a skipped record still
	// looks free. Any read failing for another reason stops the create: a slot this
	// call cannot prove empty is not a slot it overwrites.
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
	if err := writeNode(ac, agentID, node); err != nil {
		return 0, err
	}
	return seq, nil
}

// UpdateNode restates one step: its Status plus Title/Summary, where a blank
// field keeps what is stored, so an update never erases a title or a summary a
// fold already produced. A terminal Status stamps FinishedAt exactly once, and a
// step restated back to in progress loses it. Updating a node writes one node
// record and nothing else — it cannot add, reorder or overwrite any other record.
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
	return writeNode(ac, agentID, node)
}

// UpdateNodeSummaryLocked sets a plan node's Summary only — a Status changes
// where a caller writes it, never because a fold ran. Callers hold ac.Mu.
func UpdateNodeSummaryLocked(ac *domain.Context, agentID, nodeID uint64, summary string) error {
	node, err := core.ReadPlanNode(ac.Engine, agentID, nodeID)
	if err != nil {
		return err
	}
	node.Summary = summary
	node.UpdatedAt = time.Now().UnixMilli()
	return writeNode(ac, agentID, node)
}

// writeNode stores one plan node record and puts the same value into the domain's
// plan cache: a step the caller just wrote is readable from the cache by the next
// call in the same locked pass. A failed write leaves the cache untouched.
// Callers hold ac.Mu.
func writeNode(ac *domain.Context, agentID uint64, node *core.PlanNode) error {
	if err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(node.TopicID, node)
	return nil
}
