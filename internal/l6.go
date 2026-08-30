// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 trajectory operations of the internal layer: host-appended event log
// with one trajectory per agent turn (search starts it, update ends it),
// plus Crystallize (L6 → L5) as an explicit host-triggered step that folds
// in sibling turns of the same L2 topic. Retention is internal: Dream drops
// events older than trajectoryRetention, and no delete API is exposed.
// Every write keeps the domain's TrajIndex in sync under the domain lock.

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
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// maxTrajectoryPayload caps a single event payload (no raw token streams).
const maxTrajectoryPayload = 4 * 1024

// maxCrystallizePayload caps the trajectory payload bytes fed to one
// crystallize LLM call; over-budget events drop from the oldest.
const maxCrystallizePayload = 128 * 1024

// trajectoryRetention bounds the L6 event log: Dream drops events older
// than this. L6 is a process index — durable products live in L4/L5.
const trajectoryRetention = 7 * 24 * time.Hour

// PlanStatus is the string surface of a plan node's lifecycle.
type PlanStatus string

const (
	PlanPending    PlanStatus = "pending"
	PlanInProgress PlanStatus = "in_progress"
	PlanDone       PlanStatus = "done"
	PlanFailed     PlanStatus = "failed"
)

func toStatusU8(s PlanStatus) (uint8, error) {
	switch s {
	case PlanPending:
		return core.StatusPending, nil
	case PlanInProgress:
		return core.StatusInProgress, nil
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
	case core.StatusDone:
		return PlanDone
	case core.StatusFailed:
		return PlanFailed
	default:
		return PlanPending
	}
}

// AppendTrajectory appends one event to a turn's trajectory; Seq comes from
// the domain's TrajIndex (max + 1), so the host never counts sequences.
func (db *DB) AppendTrajectory(agentID uint64, sessionID string, ev core.TrajectorySlot) error {
	ac, parsed, err := db.lockSession(agentID, sessionID)
	if err != nil {
		return err
	}
	defer ac.mu.Unlock()
	if ev.EventType == "" || ev.Timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "EventType and Timestamp are required")
	}
	if len(ev.Payload) > maxTrajectoryPayload {
		ev.Payload = common.TruncateUTF8(ev.Payload, maxTrajectoryPayload)
	}
	maxSeq, _ := ac.traj.MaxSeq(parsed)
	ev.SessionID = parsed
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
	ac.traj.Append(parsed, ev.Seq, idHash, ev.Timestamp, ev.TopicID)
	return nil
}

// ReadTrajectory returns a turn's trajectory events ordered by Seq.
func (db *DB) ReadTrajectory(agentID uint64, sessionID string) ([]core.TrajectorySlot, error) {
	ac, parsed, err := db.lockSession(agentID, sessionID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	return readTurn(db.engine, agentID, ac, parsed), nil
}

// ListTrajectorySessions summarizes every turn of the domain's L6 log under
// the domain lock (same serialization contract as Append).
func (db *DB) ListTrajectorySessions(agentID uint64) ([]core.TrajectorySessionSummary, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	sums := ac.traj.Summaries()
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
	defer ac.mu.Unlock()
	ph, err := common.ParseID(planID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse plan id", err)
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
	defer ac.mu.Unlock()
	ph, err := common.ParseID(planID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse plan id", err)
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
	defer ac.mu.Unlock()
	ph, err := common.ParseID(planID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse plan id", err)
	}
	return db.buildPlanTreeLocked(ac, agentID, ph)
}

// ensurePlanNode resolves nodePath to a plan node id, creating the node
// chain (root then children along path) as pending when missing.
func (db *DB) ensurePlanNode(ac *agentContext, agentID uint64, planID uint64, nodePath string) (uint64, error) {
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

// appendPlanEventLocked writes one event bound to a plan node by filling
// PlanNodeRef, then reuses the existing per-turn Sequencer + TrajIndex.
// The domain lock is already held (lockAgent path).
func (db *DB) appendPlanEventLocked(ac *agentContext, agentID, planID, nodeID uint64, ev core.TrajectorySlot) error {
	if ev.EventType == "" || ev.Timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "EventType and Timestamp are required")
	}
	if len(ev.Payload) > maxTrajectoryPayload {
		ev.Payload = common.TruncateUTF8(ev.Payload, maxTrajectoryPayload)
	}
	maxSeq, _ := ac.traj.MaxSeq(planID)
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
	ac.traj.Append(planID, ev.Seq, idHash, ev.Timestamp, ev.TopicID)
	return nil
}

// updatePlanNodeLocked sets a plan node's status/summary, re-reading the
// stored node so it preserves its derived IDHash. It deliberately does NOT
// touch the event TrajIndex: plan nodes are not per-turn events and must not
// occupy their Seq space, otherwise a deep then shallow commit would collapse
// the per-plan event Seq and overwrite a prior event.
func (db *DB) updatePlanNodeLocked(ac *agentContext, agentID, nodeID uint64, status uint8, summary string) error {
	node, err := core.ReadTrajectorySlot(db.engine, agentID, nodeID)
	if err != nil {
		return err
	}
	node.Status = status
	if summary != "" {
		node.Summary = summary
	}
	node.Timestamp = time.Now().UnixMilli()
	if _, err := repo.WritePlanNode(db.engine, agentID, node); err != nil {
		return err
	}
	return nil
}

// updatePlanNodeSummaryLocked sets a plan node's Summary without touching its
// Status (Model A: a node's Status changes only via explicit host commit).
func (db *DB) updatePlanNodeSummaryLocked(ac *agentContext, agentID, nodeID uint64, summary string) error {
	node, err := core.ReadTrajectorySlot(db.engine, agentID, nodeID)
	if err != nil {
		return err
	}
	node.Summary = summary
	node.Timestamp = time.Now().UnixMilli()
	if _, err := repo.WritePlanNode(db.engine, agentID, node); err != nil {
		return err
	}
	return nil
}

// Crystallize extracts L5 capability candidates from a turn's trajectory
// (L6 → L5); when the turn resolves to an L2 topic, sibling turns of the
// same topic fold in, because capabilities are cross-turn patterns. The LLM
// receives the existing capability catalog so repeated crystallization
// reuses or merges instead of duplicating. The whole pipeline runs under
// the agent's domain lock; the LLM call holds no storage lock beyond the
// engine's own short-lived record locks.
func (db *DB) Crystallize(ctx context.Context, agentID uint64, sessionID string) (*CrystallizeResult, error) {
	ac, parsed, err := db.lockSession(agentID, sessionID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	events := db.trajectoryForCrystallize(ac, agentID, parsed)
	if len(events) == 0 {
		return nil, common.NewError(common.ErrNotFound, "no trajectory for session")
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
		if err := db.applyCrystallizeCandidate(agentID, cand, sessionID, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// trajectoryForCrystallize loads the turn's trajectory under ac.mu; when the
// turn carries an L2 topic id, sibling turns of the same topic fold in and
// the oldest events drop beyond the payload budget.
func (db *DB) trajectoryForCrystallize(ac *agentContext, agentID, sessionID uint64) []core.TrajectorySlot {
	events := readTurn(db.engine, agentID, ac, sessionID)
	if len(events) == 0 {
		return nil
	}
	var topicID uint64
	for _, ev := range events {
		if ev.TopicID != 0 {
			topicID = ev.TopicID
			break
		}
	}
	if topicID != 0 {
		seen := make(map[uint64]struct{}, len(events))
		for _, ev := range events {
			seen[ev.IDHash] = struct{}{}
		}
		for _, h := range ac.traj.TopicEvents(topicID) {
			if _, dup := seen[h]; dup {
				continue
			}
			if ev, err := core.ReadTrajectorySlot(db.engine, agentID, h); err == nil {
				events = append(events, *ev)
			}
		}
	}
	sortTrajectory(events)
	return trimTrajectoryByBudget(events, maxCrystallizePayload)
}

// sortTrajectory orders events chronologically; the (turn, seq) tiebreak
// keeps a turn's internal order when the host reuses timestamps.
func sortTrajectory(events []core.TrajectorySlot) {
	slices.SortFunc(events, func(a, b core.TrajectorySlot) int {
		return cmp.Or(
			cmp.Compare(a.Timestamp, b.Timestamp),
			cmp.Compare(a.SessionID, b.SessionID),
			cmp.Compare(a.Seq, b.Seq),
		)
	})
}

// trimTrajectoryByBudget keeps the newest events within budget payload
// bytes (at least one). ponytail: dropping the oldest is lossy for very
// long-running topics; map-reduce induction over chunks is the upgrade path.
func trimTrajectoryByBudget(events []core.TrajectorySlot, budget int) []core.TrajectorySlot {
	total := 0
	start := len(events)
	for start > 0 {
		p := len(events[start-1].Payload)
		if total+p > budget {
			break
		}
		total += p
		start--
	}
	if start == len(events) && len(events) > 0 {
		start = len(events) - 1
	}
	return events[start:]
}

// readTurn loads one turn's events (Seq ascending) via the domain index;
// corrupt records are skipped, mirroring the scan-based reader it replaces.
func readTurn(engine *core.StorageEngine, agentID uint64, ac *agentContext, sessionID uint64) []core.TrajectorySlot {
	hashes := ac.traj.EventHashes(sessionID)
	out := make([]core.TrajectorySlot, 0, len(hashes))
	for _, h := range hashes {
		if ev, err := core.ReadTrajectorySlot(engine, agentID, h); err == nil {
			out = append(out, *ev)
		}
	}
	return out
}

// applyCrystallizeCandidate folds one LLM candidate into the result.
// reuse/merge candidates locate an existing capability by name or
// ReuseID, so their payload may be minimal (a reuse decision does not
// require a full type/resources); only create candidates run the complete
// import validation, otherwise the candidate is recorded as skipped.
func (db *DB) applyCrystallizeCandidate(agentID uint64, cand CrystallizeCapability, sessionID string, result *CrystallizeResult) error {
	action := strings.ToLower(strings.TrimSpace(cand.Action))
	detail := CrystallizeDetail{Name: cand.Capability.Name}
	if action != "reuse" && action != "merge" {
		if err := capability.Validate(&cand.Capability); err != nil {
			result.Errors = append(result.Errors, cand.Capability.Name+": "+err.Error())
			detail.Action = "skip"
			detail.Reason = err.Error()
			result.Details = append(result.Details, detail)
			return nil
		}
	}
	id, disposition, err := db.applyCrystallizedCapability(agentID, cand, sessionID)
	if err != nil {
		return err
	}
	detail.Action = disposition // create | reuse | merge
	detail.CapabilityID = id
	result.Details = append(result.Details, detail)
	switch disposition {
	case "reuse":
		result.ReusedIDs = append(result.ReusedIDs, id)
	case "merge":
		result.MergedIDs = append(result.MergedIDs, id)
	default:
		result.CreatedIDs = append(result.CreatedIDs, id)
	}
	return nil
}

func (db *DB) applyCrystallizedCapability(agentID uint64, cand CrystallizeCapability, sessionID string) (string, string, error) {
	now := time.Now().UnixMilli()
	action := strings.ToLower(strings.TrimSpace(cand.Action))
	if action == "" {
		action = "create"
	}
	cap := capability.BuildCrystallized(&cand.Capability, now)

	// Name is the canonical identity. A create candidate whose name already
	// exists is always treated as reuse: crystallization must never silently
	// overwrite an active capability.
	if existing, id, ok := db.findCrystallizeTarget(agentID, cap, cand.ReuseID); ok {
		if action == "merge" {
			capability.MergeDefinition(existing, cap, now)
			if _, err := repo.UpsertCapabilityL5(db.engine, agentID, existing); err != nil {
				return "", "", err
			}
			return id, "merge", nil
		}
		return id, "reuse", nil
	}
	if _, err := repo.UpsertCapabilityL5(db.engine, agentID, cap); err != nil {
		return "", "", err
	}
	return common.FormatHash(cap.IDHash), "create", nil
}

// findCrystallizeTarget locates an existing capability by name ID (canonical
// identity) then explicit ReuseID. found=false means a new record must be
// created.
func (db *DB) findCrystallizeTarget(agentID uint64, cap *core.Capability, reuseID string) (*core.Capability, string, bool) {
	nameIDHash := core.CapabilityID(cap.Name)
	if existing, err := repo.GetCapabilityL5(db.engine, agentID, nameIDHash); err == nil {
		return existing, common.FormatHash(nameIDHash), true
	}
	if reuseID != "" {
		reuseHash, err := common.ParseID(reuseID)
		if err == nil {
			if existing, err := repo.GetCapabilityL5(db.engine, agentID, reuseHash); err == nil {
				return existing, common.FormatHash(existing.IDHash), true
			}
		}
	}
	return nil, "", false
}
