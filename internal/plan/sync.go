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

// PlanNode is the host-supplied full plan tree for SyncPlanTree; NodePath is
// assigned by the host (meowagent keeps a monotonic ChildSeq allocator), and a
// child's path must extend its parent's. Status is the string surface
// (pending/in_progress/running/done/failed); a blank Title/PlanType/Status/
// Summary inherits the stored node instead of clearing it.
type PlanNode struct {
	NodePath string
	Title    string
	PlanType string // plan/step/tool_call; empty = plain node
	Status   PlanStatus
	Summary  string
	Children []PlanNode
}

// SyncNodeLocked writes one PlanNode (then, depth-first, its children)
// without appending any event. EnsureNode guarantees the parent chain
// exists; a field the input leaves blank inherits the stored value, so a
// partial snapshot never rewinds a completed step or erases a folded summary.
// A terminal input Status records FinishedAt exactly once. Callers hold
// ac.Mu.
func SyncNodeLocked(ac *domain.Context, agentID, planID uint64, n *PlanNode) error {
	nodeID, err := EnsureNode(ac, agentID, planID, n.NodePath)
	if err != nil {
		return err
	}
	node, err := core.ReadTrajectorySlot(ac.Engine, agentID, nodeID)
	if err != nil {
		return err
	}
	status := n.Status
	if status == "" {
		status = StatusToString(node.Status)
	}
	u8, err := StatusToU8(status)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "invalid status for "+n.NodePath, err)
	}
	now := time.Now().UnixMilli()
	if n.Title != "" {
		node.Title = n.Title
	}
	if n.PlanType != "" {
		node.PlanType = n.PlanType
	}
	if n.Summary != "" {
		node.Summary = n.Summary
	}
	node.Status = u8
	if IsTerminalStatus(u8) && node.FinishedAt == 0 {
		node.FinishedAt = now
	}
	node.Timestamp = now
	if _, err := repo.WritePlanNode(ac.Engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(planID, node)
	for i := range n.Children {
		if err := SyncNodeLocked(ac, agentID, planID, &n.Children[i]); err != nil {
			return err
		}
	}
	return nil
}

// CollectPaths validates the input tree and records every node path,
// enforcing non-empty paths, a strict parent-descendant prefix, and no dupes.
func CollectPaths(n *PlanNode, parent string, out map[string]struct{}) error {
	if n.NodePath == "" {
		return common.NewError(common.ErrInvalidQuery, "node path required")
	}
	if parent != "" && !strings.HasPrefix(n.NodePath, parent+".") {
		return common.NewError(common.ErrInvalidQuery, "node path "+n.NodePath+" not under parent "+parent)
	}
	if _, dup := out[n.NodePath]; dup {
		return common.NewError(common.ErrInvalidQuery, "duplicate node path "+n.NodePath)
	}
	out[n.NodePath] = struct{}{}
	for _, child := range n.Children {
		if err := CollectPaths(&child, n.NodePath, out); err != nil {
			return err
		}
	}
	return nil
}

// ParentPath returns the parent path of a dotted node path ("" for a root).
func ParentPath(p string) string {
	i := strings.LastIndexByte(p, '.')
	if i < 0 {
		return ""
	}
	return p[:i]
}
