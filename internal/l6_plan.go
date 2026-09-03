// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// planNode is the in-memory tree node while building/folding. It carries the
// node's derived IDHash so folding can re-persist it via updatePlanNodeLocked.
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

// buildPlanTreeLocked assembles the plan forest from ONE aggregate scan and
// reads each node's bound-event count from the same pass (no per-node
// rescans).
func (db *DB) buildPlanTreeLocked(ac *domain.Context, agentID, planID uint64) (*PlanTree, error) {
	nodes, eventCount := db.planAggregateLocked(ac, planID)
	roots := planForest(nodes, eventCount)
	views := make([]PlanNodeView, 0, len(roots))
	for _, r := range roots {
		views = append(views, toPlanNodeView(r))
	}
	done, total := countForest(views)
	return &PlanTree{Roots: views, DoneCount: done, TotalCount: total}, nil
}

// planAggregateLocked returns one plan's nodes and per-node event counts from
// the agent's in-memory planCache (no engine scan per op); (nil, nil) when the
// plan is unknown.
func (db *DB) planAggregateLocked(ac *domain.Context, planID uint64) ([]core.TrajectorySlot, map[uint64]int) {
	agg := ac.Plans.Aggregate(planID)
	if agg == nil {
		return nil, nil
	}
	return agg.Nodes, agg.EventCount
}

// planForest links stored nodes into root trees. Nodes arrive (Seq,
// NodePath)-ordered, so roots keep their creation order. A node whose parent
// record is missing is surfaced as a root instead of vanishing.
func planForest(nodes []core.TrajectorySlot, eventCount map[uint64]int) []*planNode {
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

func toPlanNodeView(n *planNode) PlanNodeView {
	title := n.title
	if title == "" {
		title = n.nodePath
	}
	out := PlanNodeView{
		NodePath: n.nodePath, Title: title, Status: statusToString(n.status),
		Type: n.planType, Summary: n.summary, FinishedAt: n.finishedAt,
		ChildCount: len(n.children), TrajCount: n.trajCount,
		Children: make([]PlanNodeView, 0, len(n.children)),
	}
	for _, c := range n.children {
		out.Children = append(out.Children, toPlanNodeView(c))
	}
	return out
}

func countForest(views []PlanNodeView) (done, total int) {
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

// rollupPlanTreeLocked summarizes every root of the plan forest bottom-up:
// for every node, its Summary becomes the concatenation of its Done
// children's summaries. It NEVER changes a node's Status — a parent becomes
// Done only when the host explicitly commits it (Model A).
func (db *DB) rollupPlanTreeLocked(ac *domain.Context, agentID, planID uint64) error {
	nodes, _ := db.planAggregateLocked(ac, planID)
	for _, root := range planForest(nodes, nil) {
		if err := db.rollupNodeLocked(ac, agentID, root); err != nil {
			return err
		}
	}
	return nil
}

// rollupNodeLocked recurses children first, then sets the node's Summary to
// the concatenation of its Done children's summaries. It only updates
// Summary, never Status (Model A: explicit parent completion). It does NOT
// clobber a host-provided summary: when the node already has a Summary
// (host-provided or previously rolled up), it is preserved; only an empty
// Summary is backfilled from the Done children.
func (db *DB) rollupNodeLocked(ac *domain.Context, agentID uint64, n *planNode) error {
	for _, c := range n.children {
		if err := db.rollupNodeLocked(ac, agentID, c); err != nil {
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
	if err := db.updatePlanNodeSummaryLocked(ac, agentID, n.id, summary); err != nil {
		return err
	}
	n.summary = summary
	return nil
}

// PlanReplace wipes one plan's whole node set and bound events (the host
// re-plans by replacing the entire tree), keeping the planID so host
// references survive. A non-empty rootTitle seeds a fresh pending root "1"
// carrying the title; an empty title leaves the plan empty. The plan's event
// Seq space restarts at 1 because every bound event is removed.
func (db *DB) PlanReplace(agentID uint64, planID string, rootTitle string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	ph, err := parsePlanID(planID)
	if err != nil {
		return err
	}
	if _, err := repo.DeletePlanRecords(db.engine, agentID, ph); err != nil {
		return err
	}
	ac.Traj.RemoveSession(ph)
	ac.Plans.RemovePlan(ph)
	if rootTitle == "" {
		return nil
	}
	rootID, err := db.ensurePlanNode(ac, agentID, ph, "1")
	if err != nil {
		return err
	}
	node, err := core.ReadTrajectorySlot(db.engine, agentID, rootID)
	if err != nil {
		return err
	}
	node.Title = rootTitle
	node.Timestamp = time.Now().UnixMilli()
	if _, err := repo.WritePlanNode(db.engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(node.PlanID, node)
	return nil
}

// ListPlans summarizes every plan of the domain from the in-memory planCache
// in a single pass so a host can recover its plan trees after a restart
// (planID discovery + PlanState).
func (db *DB) ListPlans(agentID uint64) ([]PlanSummary, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	aggs := ac.Plans.All()
	out := make([]PlanSummary, 0, len(aggs))
	for _, agg := range aggs {
		var views []PlanNodeView
		for _, r := range planForest(agg.Nodes, agg.EventCount) {
			views = append(views, toPlanNodeView(r))
		}
		done, total := countForest(views)
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
	return out, nil
}

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

// SyncPlanTree replaces a whole plan tree from the host's authoritative
// snapshot. It mutates only node structure/fields (add missing nodes, update
// the fields the snapshot fills, delete vanished nodes with their bound events)
// and never produces a plan_step event nor touches the event Seq space. A blank
// Title/PlanType/Status/Summary inherits that node's stored value, so a partial
// snapshot never rewinds a completed step or erases a folded summary. A node
// reaches a terminal status via its input Status and records FinishedAt once.
// The planID is preserved so host references survive a re-plan.
func (db *DB) SyncPlanTree(agentID uint64, planID string, root *PlanNode) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	ph, err := parsePlanID(planID)
	if err != nil {
		return err
	}
	if root == nil || root.NodePath == "" {
		return common.NewError(common.ErrInvalidQuery, "plan root required")
	}
	newPaths := make(map[string]struct{})
	if err := collectPlanPaths(root, "", newPaths); err != nil {
		return err
	}
	existing := make(map[string]core.TrajectorySlot)
	for _, n := range repo.CollectPlanNodes(db.engine, agentID, ph) {
		existing[n.NodePath] = n
	}
	if err := db.syncPlanNodeLocked(ac, agentID, ph, root); err != nil {
		return err
	}
	for p := range existing {
		if _, ok := newPaths[p]; ok {
			continue
		}
		// Delete only the shallowest vanished node: an ancestor that is also
		// vanished cascade-deletes this subtree, so it is skipped here.
		if parent := parentPlanPath(p); parent != "" {
			if _, parentExists := existing[parent]; parentExists {
				if _, parentKept := newPaths[parent]; !parentKept {
					continue
				}
			}
		}
		if _, err := repo.DeletePlanNodeBranch(db.engine, agentID, ph, p); err != nil {
			return err
		}
		ac.Plans.RemoveNodeBranch(ph, p)
	}
	return nil
}

// syncPlanNodeLocked writes one PlanNode (then, depth-first, its children)
// without appending any event. ensurePlanNode guarantees the parent chain
// exists; a field the input leaves blank inherits the stored value, so a
// partial snapshot never rewinds a completed step or erases a folded summary.
// A terminal input Status records FinishedAt exactly once.
func (db *DB) syncPlanNodeLocked(ac *domain.Context, agentID, planID uint64, n *PlanNode) error {
	nodeID, err := db.ensurePlanNode(ac, agentID, planID, n.NodePath)
	if err != nil {
		return err
	}
	node, err := core.ReadTrajectorySlot(db.engine, agentID, nodeID)
	if err != nil {
		return err
	}
	status := n.Status
	if status == "" {
		status = statusToString(node.Status)
	}
	u8, err := toStatusU8(status)
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
	if isTerminalStatus(u8) && node.FinishedAt == 0 {
		node.FinishedAt = now
	}
	node.Timestamp = now
	if _, err := repo.WritePlanNode(db.engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(planID, node)
	for i := range n.Children {
		if err := db.syncPlanNodeLocked(ac, agentID, planID, &n.Children[i]); err != nil {
			return err
		}
	}
	return nil
}

// collectPlanPaths validates the input tree and records every node path,
// enforcing non-empty paths, a strict parent-descendant prefix, and no dupes.
func collectPlanPaths(n *PlanNode, parent string, out map[string]struct{}) error {
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
		if err := collectPlanPaths(&child, n.NodePath, out); err != nil {
			return err
		}
	}
	return nil
}

// parentPlanPath returns the parent path of a dotted node path ("" for a root).
func parentPlanPath(p string) string {
	i := strings.LastIndexByte(p, '.')
	if i < 0 {
		return ""
	}
	return p[:i]
}
