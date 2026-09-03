// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 trajectory operations of the internal layer: host-appended event log
// with one trajectory per agent turn, keyed by that turn's L2 topic id
// (Search issues the id, Update settles the turn into it), plus Crystallize
// (L6 → L5) as an explicit host-triggered step over one turn's events. Plan
// nodes share the record space under their own key (the plan id). Retention
// is internal: Dream drops events older than trajectoryRetention, and no
// delete API is exposed. Every write keeps the domain's TrajIndex in sync
// under the domain lock.

package internal

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/trajectory"
)

// maxTrajectoryPayload caps a single event payload (no raw token streams).
const maxTrajectoryPayload = 4 * 1024

// trajectoryRetention bounds the L6 event log: Dream drops events older
// than this. L6 is a process index — durable products live in L4/L5.
const trajectoryRetention = 7 * 24 * time.Hour

// PlanStatus is the string surface of a plan node's lifecycle.
type PlanStatus string

const (
	PlanPending    PlanStatus = "pending"
	PlanInProgress PlanStatus = "in_progress"
	PlanRunning    PlanStatus = "running"
	PlanDone       PlanStatus = "done"
	PlanFailed     PlanStatus = "failed"
)

func toStatusU8(s PlanStatus) (uint8, error) {
	switch s {
	case PlanPending:
		return core.StatusPending, nil
	case PlanInProgress:
		return core.StatusInProgress, nil
	case PlanRunning:
		return core.StatusRunning, nil
	case PlanDone:
		return core.StatusDone, nil
	case PlanFailed:
		return core.StatusFailed, nil
	default:
		return 0, common.NewError(common.ErrInvalidQuery, "invalid plan status: "+string(s))
	}
}

func statusToString(u uint8) PlanStatus {
	switch u {
	case core.StatusPending:
		return PlanPending
	case core.StatusInProgress:
		return PlanInProgress
	case core.StatusRunning:
		return PlanRunning
	case core.StatusDone:
		return PlanDone
	case core.StatusFailed:
		return PlanFailed
	default:
		return PlanPending
	}
}

// parsePlanID parses a host plan id and rejects 0: AppendTrajectory writes
// bare turn events with PlanID=0, so 0 is a reserved sentinel and never a
// valid plan. Accepting it would let PlanReplace delete every bare event of
// the domain (DeletePlanRecords matches those records).
func parsePlanID(planID string) (uint64, error) {
	ph, err := common.ParseID(planID)
	if err != nil {
		return 0, common.NewError(common.ErrInvalidQuery, "parse plan id", err)
	}
	if ph == 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "plan id 0000000000000000 is reserved")
	}
	return ph, nil
}

// AppendTrajectory appends one event to the turn opened by Search, keyed by
// that turn's topic id; Seq comes from the domain's TrajIndex (max + 1), so
// the host never counts sequences.
func (db *DB) AppendTrajectory(agentID uint64, turnID string, ev core.TrajectorySlot) error {
	ac, parsed, err := db.lockSession(agentID, turnID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	if ev.EventType == "" || ev.Timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "EventType and Timestamp are required")
	}
	if len(ev.Payload) > maxTrajectoryPayload {
		ev.Payload = common.TruncateUTF8(ev.Payload, maxTrajectoryPayload)
	}
	maxSeq, _ := ac.Traj.MaxSeq(parsed)
	ev.SessionID = parsed
	// The key IS the turn's topic, so the record's own topic link cannot
	// disagree with it.
	ev.TopicID = parsed
	ev.Seq = maxSeq + 1
	// An appended event must never masquerade as a plan node: force the
	// record to bare-event semantics so host-supplied plan-node fields
	// (NodeType/PlanID/ParentID/NodePath/Status/Summary/PlanNodeRef) are
	// always cleared on write.
	ev.NodeType = core.NodeTypeEvent
	ev.PlanID = 0
	ev.ParentID = 0
	ev.NodePath = ""
	ev.Status = 0
	ev.Summary = ""
	ev.PlanNodeRef = 0
	idHash, err := repo.AppendTrajectory(db.engine, agentID, ev)
	if err != nil {
		return err
	}
	ac.Traj.Append(parsed, ev.Seq, idHash, ev.Timestamp)
	return nil
}

// ReadTrajectory returns one turn's trajectory events ordered by Seq; turnID
// is the topic id Search issued for that turn.
func (db *DB) ReadTrajectory(agentID uint64, turnID string) ([]core.TrajectorySlot, error) {
	ac, parsed, err := db.lockSession(agentID, turnID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	return trajectory.ReadTurn(db.engine, agentID, ac, parsed), nil
}

// ListTrajectorySessions summarizes every turn of the domain's L6 log under
// the domain lock (same serialization contract as Append).
func (db *DB) ListTrajectorySessions(agentID uint64) ([]core.TrajectorySessionSummary, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	sums := ac.Traj.Summaries()
	out := make([]core.TrajectorySessionSummary, 0, len(sums))
	for _, s := range sums {
		out = append(out, core.TrajectorySessionSummary{
			SessionID:    common.FormatHash(s.SessionID),
			Steps:        s.Steps,
			LastAppendAt: s.LastAt,
		})
	}
	slices.SortFunc(out, func(x, y core.TrajectorySessionSummary) int {
		return cmp.Compare(x.SessionID, y.SessionID)
	})
	return out, nil
}

// PlanAppend appends one event to a plan node (planID hex, nodePath like
// "1.2.1") without advancing the plan. If the node does not exist it is
// created as a pending plan node.
func (db *DB) PlanAppend(agentID uint64, planID string, nodePath string, ev core.TrajectorySlot) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	ph, err := parsePlanID(planID)
	if err != nil {
		return err
	}
	nodeID, err := db.ensurePlanNode(ac, agentID, ph, nodePath)
	if err != nil {
		return err
	}
	return db.appendPlanEventLocked(ac, agentID, ph, nodeID, ev)
}

// PlanCommit advances a plan node to a status (with optional summary) and
// appends the step event, then rolls up Done children summaries into any
// parent Summary (Model A: a parent becomes Done only when the host
// explicitly commits it here).
func (db *DB) PlanCommit(agentID uint64, planID string, nodePath string, ev core.TrajectorySlot, status PlanStatus, summary string) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	ph, err := parsePlanID(planID)
	if err != nil {
		return err
	}
	u8, err := toStatusU8(status)
	if err != nil {
		return err
	}
	nodeID, err := db.ensurePlanNode(ac, agentID, ph, nodePath)
	if err != nil {
		return err
	}
	if err := db.updatePlanNodeLocked(ac, agentID, nodeID, u8, summary); err != nil {
		return err
	}
	if err := db.appendPlanEventLocked(ac, agentID, ph, nodeID, ev); err != nil {
		return err
	}
	return db.rollupPlanTreeLocked(ac, agentID, ph)
}

// PlanState returns the plan tree view of the actual stored statuses (no
// auto-fold: a parent becomes Done only via explicit host PlanCommit).
func (db *DB) PlanState(agentID uint64, planID string) (*PlanTree, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	ph, err := parsePlanID(planID)
	if err != nil {
		return nil, err
	}
	return db.buildPlanTreeLocked(ac, agentID, ph)
}

// ensurePlanNode resolves nodePath to a plan node id, creating the node
// chain (root then children along path) as pending when missing.
func (db *DB) ensurePlanNode(ac *domain.Context, agentID uint64, planID uint64, nodePath string) (uint64, error) {
	ids, err := splitNodePath(nodePath)
	if err != nil {
		return 0, err
	}
	var parentID uint64
	var pathSoFar []string
	for _, seg := range ids {
		pathSoFar = append(pathSoFar, seg)
		np := strings.Join(pathSoFar, ".")
		id := core.HashPlanNode(planID, np)
		node, err := core.ReadTrajectorySlot(db.engine, agentID, id)
		// A transient read error (IO/closed/corruption) is a real failure; only
		// a genuine "record not found" means the node does not exist yet, so we
		// create it. Swallowing transient errors would reset a live node back to
		// pending.
		if err != nil && common.CodeOf(err) != common.ErrNotFound {
			return 0, err
		}
		if err != nil || node == nil || node.NodeType != core.NodeTypePlan {
			node = &core.TrajectorySlot{
				IDHash: id, SessionID: planID, Seq: uint64(len(pathSoFar)),
				NodeType: core.NodeTypePlan, PlanID: planID, ParentID: parentID,
				NodePath: np, Status: core.StatusPending, Timestamp: time.Now().UnixMilli(),
			}
			if _, err := repo.WritePlanNode(db.engine, agentID, node); err != nil {
				return 0, err
			}
			ac.Plans.UpsertNode(planID, node)
		}
		parentID = id
	}
	return parentID, nil
}

func splitNodePath(nodePath string) ([]string, error) {
	if nodePath == "" {
		return nil, common.NewError(common.ErrInvalidQuery, "nodePath required")
	}
	parts := strings.Split(nodePath, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil, common.NewError(common.ErrInvalidQuery, "invalid nodePath: "+nodePath)
		}
		out = append(out, p)
	}
	return out, nil
}

// planEventTypes is the vocabulary for plan-bound events: the documented
// trajectory step types plus plan_step for plan progress commits. Plain
// AppendTrajectory stays free-form (host-owned turns).
var planEventTypes = map[string]struct{}{
	"llm_request": {}, "llm_output": {}, "tool_call": {}, "tool_result": {},
	"subagent_spawn": {}, "subagent_done": {}, "context_inject": {},
	"ask_user": {}, "user_reply": {}, "plan_step": {},
}

// appendPlanEventLocked writes one event bound to a plan node by filling
// PlanNodeRef, then reuses the existing per-turn Sequencer + TrajIndex.
// The domain lock is already held (lockAgent path).
func (db *DB) appendPlanEventLocked(ac *domain.Context, agentID, planID, nodeID uint64, ev core.TrajectorySlot) error {
	if ev.EventType == "" || ev.Timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "EventType and Timestamp are required")
	}
	if _, ok := planEventTypes[ev.EventType]; !ok {
		return common.NewError(common.ErrInvalidQuery, "unknown plan event type: "+ev.EventType)
	}
	if len(ev.Payload) > maxTrajectoryPayload {
		ev.Payload = common.TruncateUTF8(ev.Payload, maxTrajectoryPayload)
	}
	maxSeq, _ := ac.Traj.MaxSeq(planID)
	ev.SessionID = planID
	ev.Seq = maxSeq + 1
	ev.PlanNodeRef = nodeID
	// An appended plan event is a plain event bound to the node: force the
	// record to event semantics so a host cannot inject NodeType=Plan (or
	// other plan-node fields) and pollute the tree view.
	ev.NodeType = core.NodeTypeEvent
	ev.PlanID = planID
	ev.ParentID = 0
	ev.NodePath = ""
	ev.Status = 0
	ev.Summary = ""
	idHash, err := repo.AppendTrajectory(db.engine, agentID, ev)
	if err != nil {
		return err
	}
	ac.Traj.Append(planID, ev.Seq, idHash, ev.Timestamp)
	ac.Plans.UpsertEvent(planID, nodeID, ev)
	return nil
}

// isTerminalStatus reports whether a plan-node status is a final state (done
// or failed); only these record a FinishedAt.
func isTerminalStatus(u uint8) bool {
	return u == core.StatusDone || u == core.StatusFailed
}

// updatePlanNodeLocked sets a plan node's status/summary, re-reading the
// stored node so it preserves its derived IDHash. It deliberately does NOT
// touch the event TrajIndex: plan nodes are not per-turn events and must not
// occupy their Seq space, otherwise a deep then shallow commit would collapse
// the per-plan event Seq and overwrite a prior event.
func (db *DB) updatePlanNodeLocked(ac *domain.Context, agentID, nodeID uint64, status uint8, summary string) error {
	node, err := core.ReadTrajectorySlot(db.engine, agentID, nodeID)
	if err != nil {
		return err
	}
	node.Status = status
	if summary != "" {
		node.Summary = summary
	}
	now := time.Now().UnixMilli()
	if isTerminalStatus(status) && node.FinishedAt == 0 {
		node.FinishedAt = now
	}
	node.Timestamp = now
	if _, err := repo.WritePlanNode(db.engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(node.PlanID, node)
	return nil
}

// updatePlanNodeSummaryLocked sets a plan node's Summary without touching its
// Status (Model A: a node's Status changes only via explicit host commit).
func (db *DB) updatePlanNodeSummaryLocked(ac *domain.Context, agentID, nodeID uint64, summary string) error {
	node, err := core.ReadTrajectorySlot(db.engine, agentID, nodeID)
	if err != nil {
		return err
	}
	node.Summary = summary
	node.Timestamp = time.Now().UnixMilli()
	if _, err := repo.WritePlanNode(db.engine, agentID, node); err != nil {
		return err
	}
	ac.Plans.UpsertNode(node.PlanID, node)
	return nil
}

// Crystallize extracts L5 capability candidates from one turn's trajectory
// (L6 → L5), keyed by the topic id Search issued for that turn — or by a plan
// id, when the host bound these turns' events to a plan tree. The LLM
// receives the existing capability catalog so repeated crystallization
// reuses or merges instead of duplicating. The whole pipeline holds the
// domain lock, exactly as Update and Dream do: another operation on this
// agent waits (so a slow LLM round-trip stalls same-domain writes), while
// other domains stay parallel.
func (db *DB) Crystallize(ctx context.Context, agentID uint64, turnID string) (*CrystallizeResult, error) {
	ac, parsed, err := db.lockSession(agentID, turnID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	// Events land in Seq order; only the payload budget can shorten the turn.
	events := trajectory.TrimByBudget(trajectory.ReadTurn(db.engine, agentID, ac, parsed), trajectory.MaxCrystallizePayload)
	if len(events) == 0 {
		return nil, common.NewError(common.ErrNotFound, "no trajectory for turn "+turnID)
	}
	existing := capability.ActiveOnly(core.CollectAllCapabilities(db.engine, agentID))
	out, err := llmops.Crystallize(ctx, db.llm, events, existing)
	if err != nil {
		return nil, err
	}

	if db.closed.Load() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	result := &CrystallizeResult{CreatedIDs: []string{}, ReusedIDs: []string{}, MergedIDs: []string{}}
	for _, cand := range out.Capabilities {
		if err := trajectory.ApplyCandidate(db.engine, agentID, cand, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}
