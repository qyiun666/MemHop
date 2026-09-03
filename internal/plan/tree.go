// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package plan

import (
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
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
	planType   string
	finishedAt int64
	trajCount  int
	children   []*planNode
}

// PlanNodeView is the external tree node; nodes are keyed by the
// host-assigned NodePath, no numeric IDs are exposed.
type PlanNodeView struct {
	NodePath   string         `json:"node_path"`
	Title      string         `json:"title"`
	Status     PlanStatus     `json:"status"`
	Type       string         `json:"type"`
	Summary    string         `json:"summary"`
	FinishedAt int64          `json:"finished_at"`
	ChildCount int            `json:"child_count"`
	TrajCount  int            `json:"traj_count"`
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

// PlanSummary is one plan's footprint for ListPlans (host restart recovery).
type PlanSummary struct {
	PlanID       string `json:"plan_id"`
	CreatedAt    int64  `json:"created_at"`
	LastActiveAt int64  `json:"last_active_at"`
	NodeCount    int    `json:"node_count"`
	DoneCount    int    `json:"done_count"`
	TotalCount   int    `json:"total_count"`
	Active       bool   `json:"active"`
}

// BuildTree assembles the plan forest from ONE aggregate scan and
// reads each node's bound-event count from the same pass (no per-node
// rescans). Callers hold ac.Mu.
func BuildTree(ac *domain.Context, agentID, planID uint64) (*PlanTree, error) {
	nodes, eventCount := aggregate(ac, planID)
	roots := Forest(nodes, eventCount)
	views := make([]PlanNodeView, 0, len(roots))
	for _, r := range roots {
		views = append(views, ToNodeView(r))
	}
	done, total := CountForest(views)
	return &PlanTree{Roots: views, DoneCount: done, TotalCount: total}, nil
}

// Summarize renders every plan of the domain from the in-memory plan
// cache in a single pass. Callers hold ac.Mu.
func Summarize(ac *domain.Context) []PlanSummary {
	aggs := ac.Plans.All()
	out := make([]PlanSummary, 0, len(aggs))
	for _, agg := range aggs {
		var views []PlanNodeView
		for _, r := range Forest(agg.Nodes, agg.EventCount) {
			views = append(views, ToNodeView(r))
		}
		done, total := CountForest(views)
		out = append(out, PlanSummary{
			PlanID:       common.FormatHash(agg.PlanID),
			CreatedAt:    agg.CreatedAt,
			LastActiveAt: agg.LastActiveAt,
			NodeCount:    len(agg.Nodes),
			DoneCount:    done,
			TotalCount:   total,
			Active:       agg.HasNonDone,
		})
	}
	return out
}

// aggregate returns one plan's nodes and per-node event counts from
// the agent's in-memory plan cache (no engine scan per op); (nil, nil) when
// the plan is unknown.
func aggregate(ac *domain.Context, planID uint64) ([]core.TrajectorySlot, map[uint64]int) {
	agg := ac.Plans.Aggregate(planID)
	if agg == nil {
		return nil, nil
	}
	return agg.Nodes, agg.EventCount
}

// Forest links stored nodes into root trees. Nodes arrive (Seq,
// NodePath)-ordered, so roots keep their creation order. A node whose parent
// record is missing is surfaced as a root instead of vanishing.
func Forest(nodes []core.TrajectorySlot, eventCount map[uint64]int) []*planNode {
	byNode := make(map[uint64]*planNode, len(nodes))
	for i := range nodes {
		byNode[nodes[i].IDHash] = &planNode{
			id: nodes[i].IDHash, nodePath: nodes[i].NodePath, title: nodes[i].Title,
			status: nodes[i].Status, summary: nodes[i].Summary, planType: nodes[i].PlanType,
			finishedAt: nodes[i].FinishedAt, trajCount: eventCount[nodes[i].IDHash],
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

func ToNodeView(n *planNode) PlanNodeView {
	title := n.title
	if title == "" {
		title = n.nodePath
	}
	out := PlanNodeView{
		NodePath: n.nodePath, Title: title, Status: StatusToString(n.status),
		Type: n.planType, Summary: n.summary, FinishedAt: n.finishedAt,
		ChildCount: len(n.children), TrajCount: n.trajCount,
		Children: make([]PlanNodeView, 0, len(n.children)),
	}
	for _, c := range n.children {
		out.Children = append(out.Children, ToNodeView(c))
	}
	return out
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

// RollupTree summarizes every root of the plan forest bottom-up:
// for every node, its Summary becomes the concatenation of its Done
// children's summaries. It NEVER changes a node's Status — a parent becomes
// Done only when the host explicitly commits it (Model A). Callers hold
// ac.Mu.
func RollupTree(ac *domain.Context, agentID, planID uint64) error {
	nodes, _ := aggregate(ac, planID)
	for _, root := range Forest(nodes, nil) {
		if err := rollupNode(ac, agentID, root); err != nil {
			return err
		}
	}
	return nil
}

// rollupNode recurses children first, then sets the node's Summary to
// the concatenation of its Done children's summaries. It only updates
// Summary, never Status (Model A: explicit parent completion). It does NOT
// clobber a host-provided summary: when the node already has a Summary
// (host-provided or previously rolled up), it is preserved; only an empty
// Summary is backfilled from the Done children.
func rollupNode(ac *domain.Context, agentID uint64, n *planNode) error {
	for _, c := range n.children {
		if err := rollupNode(ac, agentID, c); err != nil {
			return err
		}
	}
	if len(n.children) == 0 {
		return nil
	}
	// Model A: a parent becomes Done only via explicit host commit. Only a
	// Done parent folds its Done children's Summaries into its own; a
	// not-yet-Done parent is left untouched so an incremental rollup (which
	// runs after every PlanCommit) cannot pre-fill it and later fool the
	// "preserve a host-provided Summary" check into keeping a stale value.
	if n.status != core.StatusDone {
		return nil
	}
	var parts []string
	for _, c := range n.children {
		if c.status == core.StatusDone && c.summary != "" {
			parts = append(parts, c.summary)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	summary := strings.Join(parts, "; ")
	if n.summary != "" {
		return nil
	}
	if err := UpdateNodeSummaryLocked(ac, agentID, n.id, summary); err != nil {
		return err
	}
	n.summary = summary
	return nil
}
