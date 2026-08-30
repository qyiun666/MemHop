// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"strings"

	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// planNode is the in-memory tree node while building/folding. It carries the
// node's derived IDHash so folding can re-persist it via updatePlanNodeLocked.
type planNode struct {
	id          uint64
	nodePath    string
	status      uint8
	summary     string
	trajCount   int
	lastSummary string
	children    []*planNode
}

// PlanNodeView is the external tree node (uint64 ids; api layer maps to hex).
type PlanNodeView struct {
	NodePath    string         `json:"node_path"`
	Title       string         `json:"title"`
	Status      PlanStatus     `json:"status"`
	Summary     string         `json:"summary"`
	ChildCount  int            `json:"child_count"`
	TrajCount   int            `json:"traj_count"`
	LastSummary string         `json:"last_summary"`
	Children    []PlanNodeView `json:"children"`
}

// PlanTree is the external tree root view.
type PlanTree struct {
	Root       PlanNodeView `json:"root"`
	DoneCount  int          `json:"done_count"`
	TotalCount int          `json:"total_count"`
}

// buildPlanTreeLocked assembles the plan tree from CollectPlanNodes and
// counts each node's bound events. It walks stored nodes (which carry their
// derived IDHash), not view round-trips.
func (db *DB) buildPlanTreeLocked(ac *agentContext, agentID, planID uint64) (*PlanTree, error) {
	nodes := repo.CollectPlanNodes(db.engine, agentID, planID)
	byNode := make(map[uint64]*planNode, len(nodes))
	for i := range nodes {
		byNode[nodes[i].IDHash] = &planNode{
			id: nodes[i].IDHash, nodePath: nodes[i].NodePath,
			status: nodes[i].Status, summary: nodes[i].Summary,
		}
	}
	// Attach children and count events.
	for i := range nodes {
		cur := byNode[nodes[i].IDHash]
		cur.trajCount = db.countNodeEvents(ac, agentID, nodes[i].IDHash)
		if nodes[i].ParentID == 0 {
			continue
		}
		if p, ok := byNode[nodes[i].ParentID]; ok {
			p.children = append(p.children, cur)
		}
	}
	// Find root: the node with ParentID==0.
	var root *planNode
	for _, n := range nodes {
		if n.ParentID == 0 {
			root = byNode[n.IDHash]
			break
		}
	}
	if root == nil {
		return &PlanTree{}, nil
	}
	view := toPlanNodeView(root)
	done, total := countTree(view)
	return &PlanTree{Root: view, DoneCount: done, TotalCount: total}, nil
}

// countNodeEvents returns how many event records (NodeType=event) carry the
// node as their PlanNodeRef.
func (db *DB) countNodeEvents(_ *agentContext, agentID, nodeID uint64) int {
	return len(repo.CollectNodeEvents(db.engine, agentID, nodeID))
}

func toPlanNodeView(n *planNode) PlanNodeView {
	out := PlanNodeView{
		NodePath: n.nodePath, Title: n.nodePath, Status: statusToString(n.status),
		Summary: n.summary, ChildCount: len(n.children), TrajCount: n.trajCount,
		LastSummary: n.lastSummary, Children: make([]PlanNodeView, 0, len(n.children)),
	}
	for _, c := range n.children {
		out.Children = append(out.Children, toPlanNodeView(c))
	}
	return out
}

func countTree(v PlanNodeView) (done, total int) {
	total = 1
	if v.Status == PlanDone {
		done = 1
	}
	for _, c := range v.Children {
		d, t := countTree(c)
		done += d
		total += t
	}
	return done, total
}

// foldPlanTreeLocked folds every done-parent in the plan tree bottom-up:
// a node whose children are all Done becomes Done with the concatenation of
// its children summaries. It re-derives node ids from stored records.
func (db *DB) foldPlanTreeLocked(ac *agentContext, agentID, planID uint64) error {
	nodes := repo.CollectPlanNodes(db.engine, agentID, planID)
	byNode := make(map[uint64]*planNode, len(nodes))
	for i := range nodes {
		byNode[nodes[i].IDHash] = &planNode{
			id: nodes[i].IDHash, nodePath: nodes[i].NodePath,
			status: nodes[i].Status, summary: nodes[i].Summary,
		}
	}
	for i := range nodes {
		if nodes[i].ParentID == 0 {
			continue
		}
		if p, ok := byNode[nodes[i].ParentID]; ok {
			p.children = append(p.children, byNode[nodes[i].IDHash])
		}
	}
	// Find root and fold bottom-up.
	var root *planNode
	for _, n := range nodes {
		if n.ParentID == 0 {
			root = byNode[n.IDHash]
			break
		}
	}
	if root == nil {
		return nil
	}
	return db.foldNodeLocked(ac, agentID, root)
}

// foldNodeLocked recursively folds children first, then, if all children are
// Done and this node is not, sets it to Done with the concatenated summaries.
func (db *DB) foldNodeLocked(ac *agentContext, agentID uint64, n *planNode) error {
	for _, c := range n.children {
		if err := db.foldNodeLocked(ac, agentID, c); err != nil {
			return err
		}
	}
	if len(n.children) > 0 && allDone(n.children) && n.status != core.StatusDone {
		var parts []string
		for _, c := range n.children {
			if c.summary != "" {
				parts = append(parts, c.summary)
			}
		}
		if err := db.updatePlanNodeLocked(ac, agentID, n.id, core.StatusDone, strings.Join(parts, "; ")); err != nil {
			return err
		}
	}
	return nil
}

func allDone(children []*planNode) bool {
	if len(children) == 0 {
		return false
	}
	for _, c := range children {
		if c.status != core.StatusDone {
			return false
		}
	}
	return true
}
