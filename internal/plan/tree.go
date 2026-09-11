// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package plan

import (
	"strconv"
	"strings"

	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// planNode is the in-memory tree node while building/folding. It carries the
// node's derived IDHash so folding can re-persist it via
// UpdateNodeSummaryLocked.
type planNode struct {
	id         uint64
	seq        uint32
	parentSeq  uint32
	title      string
	status     uint8
	summary    string
	createdAt  int64
	finishedAt int64
	updatedAt  int64
	children   []*planNode
}

// PlanNodeView is the external tree node; a step is addressed by Seq, the
// ordinal the library handed out inside its turn, and ParentSeq says which step
// it hangs under (0 = a root). No record hash crosses the surface.
type PlanNodeView struct {
	Seq        uint32         `json:"seq"`
	ParentSeq  uint32         `json:"parent_seq"`
	Title      string         `json:"title"`
	Status     PlanStatus     `json:"status"`
	Summary    string         `json:"summary"`
	CreatedAt  int64          `json:"created_at"`
	FinishedAt int64          `json:"finished_at"`
	UpdatedAt  int64          `json:"updated_at"`
	ChildCount int            `json:"child_count"`
	Children   []PlanNodeView `json:"children"`
}

// PlanTree is the external forest view of one plan. A plan may hold several
// roots (each root a step created with no parent); Done/Total cover every root.
// Nodes whose parent record is missing surface as roots too, so an expired root
// never hides its live subtree.
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
// (nil when no node lives under that topic). The aggregate carries nodes only:
// what happened during a step is not a property of the node.
func aggregate(ac *domain.Context, topicID uint64) []core.PlanNode {
	agg := ac.Plans.Aggregate(topicID)
	if agg == nil {
		return nil
	}
	return agg.Nodes
}

// Forest links stored nodes into root trees by their parent ordinal. Nodes
// arrive Seq-ascending, so roots and children alike keep the order they were
// created in. A node whose parent record is missing is surfaced as a root instead
// of vanishing.
func Forest(nodes []core.PlanNode) []*planNode {
	bySeq := make(map[uint32]*planNode, len(nodes))
	for i := range nodes {
		n := nodes[i]
		bySeq[n.Seq] = &planNode{
			id: n.IDHash, seq: n.Seq, parentSeq: n.ParentSeq, title: n.Title,
			status: n.Status, summary: n.Summary,
			createdAt: n.CreatedAt, finishedAt: n.FinishedAt,
			updatedAt: n.UpdatedAt,
		}
	}
	var roots []*planNode
	for i := range nodes {
		cur := bySeq[nodes[i].Seq]
		if nodes[i].ParentSeq == 0 {
			roots = append(roots, cur)
			continue
		}
		if p, ok := bySeq[nodes[i].ParentSeq]; ok {
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
		title = strconv.FormatUint(uint64(n.seq), 10)
	}
	out := PlanNodeView{
		Seq: n.seq, ParentSeq: n.parentSeq, Title: title, Status: status,
		Summary: n.summary, CreatedAt: n.createdAt,
		FinishedAt: n.finishedAt, UpdatedAt: n.updatedAt,
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
// Status — a parent's Done is never inferred from its children's.
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
// its children's. Three things must hold for a fold: the node itself is Done (an
// unfinished parent has no conclusion to carry), its own Summary is empty (one
// already written, by a caller or an earlier fold, is never clobbered), and
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
	// Children arrive in creation order, so a folded summary reads in the order
	// the steps were planned, not the order they happened to be written.
	summary := strings.Join(parts, "; ")
	if err := UpdateNodeSummaryLocked(ac, agentID, n.id, summary); err != nil {
		return err
	}
	n.summary = summary
	return nil
}
