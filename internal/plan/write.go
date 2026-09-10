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

// ValidateDeclaration checks a host's whole-tree declaration before anything is
// written: every step names a well-formed path and a status the engine can name,
// and no path is named twice in one declaration. A declaration this refuses
// leaves the tree exactly as it was — half an applied restatement would leave
// the host unable to tell which of its steps landed.
func ValidateDeclaration(steps []Step) error {
	seen := make(map[string]struct{}, len(steps))
	for _, s := range steps {
		if _, err := SplitNodePath(s.NodePath); err != nil {
			return err
		}
		if _, err := StatusToU8(s.Status); err != nil {
			return err
		}
		if _, dup := seen[s.NodePath]; dup {
			return common.NewError(common.ErrInvalidQuery,
				"the plan declares the same step twice: "+s.NodePath)
		}
		seen[s.NodePath] = struct{}{}
	}
	return nil
}

// SetNodes applies one declaration of a turn's plan: every named node is created
// along its path when missing, then restated. Nodes the declaration does not
// mention are left exactly as they are — the library never infers from an absent
// step that the host withdrew it, because "the host stopped listing it" and
// "the host only restated part of the tree" look identical from here. Withdrawing
// a step is what declaring a new turn's tree does. Callers hold ac.Mu.
func SetNodes(ac *domain.Context, agentID, topicID uint64, steps []Step) error {
	for _, s := range steps {
		id, err := EnsureNode(ac, agentID, topicID, s.NodePath)
		if err != nil {
			return err
		}
		if err := CommitNode(ac, agentID, id, s); err != nil {
			return err
		}
	}
	return nil
}

// CommitNode applies one declared step to a plan node: its status plus the node's
// own Title/Summary, where a field Step leaves blank keeps what is
// stored — restating a step never erases its title or a summary already
// folded into it. A terminal status records FinishedAt exactly once, and a step
// restated back to a non-terminal status loses it: a completion time left on a
// step that is running again would read as a finished one. Writing a node never
// touches the L4 content track — a step's events are separate records, so
// restating a tree cannot add, reorder or overwrite what a turn recorded.
// Callers hold ac.Mu.
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
