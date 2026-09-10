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
		node, err := core.ReadPlanNode(ac.Engine, agentID, id)
		// A transient read error (IO/closed/corruption) is a real failure; only
		// a genuine "record not found" means the node does not exist yet, so we
		// create it. Swallowing transient errors would reset a live node back to
		// pending.
		if err != nil && common.CodeOf(err) != common.ErrNotFound {
			return 0, err
		}
		if node == nil {
			node = &core.PlanNode{
				IDHash: id, TopicID: topicID, ParentID: parentID, NodePath: np,
				Status: core.StatusPending, UpdatedAt: time.Now().UnixMilli(),
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

// CommitNode applies one host commit to a plan node: its status plus the node's
// own Title/Summary, where a field Step leaves blank keeps what is
// stored — re-committing a step never erases its title or a summary already
// folded into it. An unknown status is refused before anything is written. A
// terminal status records FinishedAt exactly once. Writing a node never touches
// the L4 content track: a step's events are separate records, so committing a
// tree cannot add, reorder or overwrite a turn's content. Callers hold ac.Mu.
func CommitNode(ac *domain.Context, agentID, nodeID uint64, step Step) error {
	u8, err := StatusToU8(step.Status)
	if err != nil {
		return err
	}
	node, err := core.ReadPlanNode(ac.Engine, agentID, nodeID)
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
	if IsTerminalStatus(node.Status) && node.FinishedAt == 0 {
		node.FinishedAt = now
	}
	node.UpdatedAt = now
	if _, err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(node.TopicID, node)
	return nil
}

// UpdateNodeSummaryLocked sets a plan node's Summary without touching its
// Status (Model A: a node's Status changes only via explicit host commit).
// Callers hold ac.Mu.
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
