// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package plan

import (
	"strings"

	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// planNode is the in-memory tree node while building/folding. It carries the
// node's derived IDHash so folding can re-persist it via
// UpdateNodeSummaryLocked.
type planNode struct {
	id         uint64
	nodePath   string
	title      string
	status     uint8
	summary    string
	finishedAt int64
	children   []*planNode
}

// PlanNodeView is the external tree node; nodes are keyed by the
// host-assigned NodePath, no numeric IDs are exposed.
type PlanNodeView struct {
	NodePath   string         `json:"node_path"`
	Title      string         `json:"title"`
	Status     PlanStatus     `json:"status"`
	Summary    string         `json:"summary"`
	FinishedAt int64          `json:"finished_at"`
	ChildCount int            `json:"child_count"`
	Children   []PlanNodeView `json:"children"`
}

// PlanTree is the external forest view of one plan. A plan may hold several
// roots (flat step lists produce one root per top-level step); Done/Total
// cover every root. Nodes whose parent record is missing surface as roots
// too, so an expired root never hides its live subtree.
type PlanTree struct {
	Roots      []PlanNodeView `json:"roots"`
	DoneCount  int            `json:"done_count"`
	TotalCount int            `json:"total_count"`
}

// BuildTree assembles one turn's plan forest from the agent's in-memory plan
// cache, so the read costs no engine scan. Callers hold ac.Mu.
func BuildTree(ac *domain.Context, topicID uint64) (*PlanTree, error) {
	roots := Forest(aggregate(ac, topicID))
	views := make([]PlanNodeView, 0, len(roots))
	for _, r := range roots {
		v, err := ToNodeView(r)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	done, total := CountForest(views)
	return &PlanTree{Roots: views, DoneCount: done, TotalCount: total}, nil
}

// aggregate returns the nodes of one turn's plan from the domain's plan cache
// (nil when no node lives under that topic). Event counts are not here: a
// turn's events are L4 content, and a tree reports its steps, not what
// happened during them.
func aggregate(ac *domain.Context, topicID uint64) []core.PlanNode {
	agg := ac.Plans.Aggregate(topicID)
	if agg == nil {
		return nil
	}
	return agg.Nodes
}

// Forest links stored nodes into root trees. Nodes arrive NodePath-ordered, so
// roots keep their creation order. A node whose parent record is missing is
// surfaced as a root instead of vanishing.
func Forest(nodes []core.PlanNode) []*planNode {
	byNode := make(map[uint64]*planNode, len(nodes))
	for i := range nodes {
		byNode[nodes[i].IDHash] = &planNode{
			id: nodes[i].IDHash, nodePath: nodes[i].NodePath, title: nodes[i].Title,
			status: nodes[i].Status, summary: nodes[i].Summary,
			finishedAt: nodes[i].FinishedAt,
		}
	}
	var roots []*planNode
	for i := range nodes {
		cur := byNode[nodes[i].IDHash]
		if nodes[i].ParentID == 0 {
			roots = append(roots, cur)
			continue
		}
		if p, ok := byNode[nodes[i].ParentID]; ok {
			p.children = append(p.children, cur)
		} else {
			roots = append(roots, cur)
		}
	}
	return roots
}

// ToNodeView renders one tree node for the surface. An undefined stored status
// is reported: the tree would otherwise show a step the engine cannot name as
// one that has not started.
func ToNodeView(n *planNode) (PlanNodeView, error) {
	status, err := StatusToString(n.status)
	if err != nil {
		return PlanNodeView{}, err
	}
	title := n.title
	if title == "" {
		title = n.nodePath
	}
	out := PlanNodeView{
		NodePath: n.nodePath, Title: title, Status: status,
		Summary: n.summary, FinishedAt: n.finishedAt,
		ChildCount: len(n.children),
		Children:   make([]PlanNodeView, 0, len(n.children)),
	}
	for _, c := range n.children {
		cv, err := ToNodeView(c)
		if err != nil {
			return PlanNodeView{}, err
		}
		out.Children = append(out.Children, cv)
	}
	return out, nil
}

func CountForest(views []PlanNodeView) (done, total int) {
	for _, v := range views {
		d, t := countTree(v)
		done += d
		total += t
	}
	return done, total
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

// RollupTree walks one turn's plan forest bottom-up: a node's Summary becomes
// the concatenation of its children's summaries. It NEVER changes a node's
// Status — a parent becomes Done only when the host declares it so (Model A).
// Callers hold ac.Mu.
func RollupTree(ac *domain.Context, agentID, topicID uint64) error {
	for _, root := range Forest(aggregate(ac, topicID)) {
		if err := rollupNode(ac, agentID, root); err != nil {
			return err
		}
	}
	return nil
}

// rollupNode recurses children first, then backfills this node's Summary from
// its children's. Three things must hold for a fold: the node itself is Done
// (Model A — an unfinished parent has no conclusion to carry), its own Summary
// is empty (a host-written or previously folded one is never clobbered), and
// every direct child has reached a final state. That last one is why a partial
// fold is worse than none: "did three things; two of them" is a summary that
// reads exactly like a complete one, and nothing on the parent says the third
// child was still open when it was written. A failed child with no summary
// contributes no text but still settles its branch.
func rollupNode(ac *domain.Context, agentID uint64, n *planNode) error {
	for _, c := range n.children {
		if err := rollupNode(ac, agentID, c); err != nil {
			return err
		}
	}
	if len(n.children) == 0 || n.status != core.StatusDone || n.summary != "" {
		return nil
	}
	parts := make([]string, 0, len(n.children))
	for _, c := range n.children {
		if !IsTerminalStatus(c.status) {
			return nil
		}
		if c.summary != "" {
			parts = append(parts, c.summary)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	// Children arrive in NodePath order, so a folded summary reads in the order
	// the steps were planned, not the order they happened to be written.
	summary := strings.Join(parts, "; ")
	if err := UpdateNodeSummaryLocked(ac, agentID, n.id, summary); err != nil {
		return err
	}
	n.summary = summary
	return nil
}
