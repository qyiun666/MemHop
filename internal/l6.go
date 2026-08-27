// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 trajectory operations of the internal layer: host-appended event log per
// session plus Crystallize (L6 → L5) as an explicit host-triggered step.
// Dream does not participate in L6; the host purges via DeleteTrajectory.

package internal

import (
	"context"
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

// AppendTrajectory appends one event to a session's trajectory; Seq is
// derived from the current max + 1, so the host never counts sequences.
// The domain lock serializes Seq allocation.
func (db *DB) AppendTrajectory(agentID uint64, sessionID string, ev core.TrajectorySlot) error {
	ac, parsed, err := db.lockSession(agentID, sessionID)
	if err != nil {
		return err
	}
	defer ac.mu.Unlock()
	if ev.EventType == "" || ev.Timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "EventType and Timestamp are required")
	}
	events, err := repo.ReadTrajectory(db.engine, agentID, parsed)
	if err != nil {
		return err
	}
	var maxSeq uint64
	for _, e := range events {
		if e.Seq > maxSeq {
			maxSeq = e.Seq
		}
	}
	if len(ev.Payload) > maxTrajectoryPayload {
		ev.Payload = common.TruncateUTF8(ev.Payload, maxTrajectoryPayload)
	}
	ev.SessionID = parsed
	ev.Seq = maxSeq + 1
	return repo.AppendTrajectory(db.engine, agentID, ev)
}

// ReadTrajectory returns a session's trajectory events ordered by Seq.
func (db *DB) ReadTrajectory(agentID uint64, sessionID string) ([]core.TrajectorySlot, error) {
	ac, parsed, err := db.lockSession(agentID, sessionID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	return repo.ReadTrajectory(db.engine, agentID, parsed)
}

// TrajectoryStats returns per-session statistics over the trajectory log.
func (db *DB) TrajectoryStats(agentID uint64, sessionID string) (*TrajectoryStats, error) {
	ac, parsed, err := db.lockSession(agentID, sessionID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	events, err := repo.ReadTrajectory(db.engine, agentID, parsed)
	if err != nil {
		return nil, err
	}
	stats := &TrajectoryStats{ToolUsage: make(map[string]int, 8)}
	for _, e := range events {
		stats.Steps++
		stats.ToolUsage[e.EventType]++
		if e.Timestamp > stats.LastAppendAt {
			stats.LastAppendAt = e.Timestamp
		}
	}
	return stats, nil
}

// DeleteTrajectory removes a session's trajectory events; the domain lock
// comes from the internal layer.
func (db *DB) DeleteTrajectory(agentID uint64, sessionID string) error {
	ac, parsed, err := db.lockSession(agentID, sessionID)
	if err != nil {
		return err
	}
	defer ac.mu.Unlock()
	return repo.DeleteTrajectory(db.engine, agentID, parsed)
}

// ListTrajectorySessions summarizes every session of the domain's L6 log
// under the domain lock (same serialization contract as Append).
func (db *DB) ListTrajectorySessions(agentID uint64) ([]core.TrajectorySessionSummary, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	return repo.ListTrajectorySessions(db.engine, agentID)
}

// PruneTrajectory deletes trajectory events older than the given Unix-ms
// cutoff across all sessions of this domain, reporting the removed count.
func (db *DB) PruneTrajectory(agentID uint64, before int64) (int, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return 0, err
	}
	defer ac.mu.Unlock()
	return repo.PruneTrajectoryBefore(db.engine, agentID, before)
}

// Crystallize extracts L5 capability candidates from a session's trajectory
// (L6 → L5). The LLM receives the existing capability catalog so repeated
// crystallization reuses or merges instead of duplicating. The whole pipeline
// runs under the agent's domain lock; the LLM call holds no storage lock
// beyond the engine's own short-lived record locks.
func (db *DB) Crystallize(ctx context.Context, agentID uint64, sessionID string) (*CrystallizeResult, error) {
	ac, parsed, err := db.lockSession(agentID, sessionID)
	if err != nil {
		return nil, err
	}
	defer ac.mu.Unlock()
	events, err := repo.ReadTrajectory(db.engine, agentID, parsed)
	if err != nil {
		return nil, err
	}
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
	nameID := common.FormatHash(core.CapabilityID(cap.Name))
	if existing, err := repo.GetCapabilityL5(db.engine, agentID, nameID); err == nil {
		return existing, nameID, true
	}
	if reuseID != "" {
		if existing, err := repo.GetCapabilityL5(db.engine, agentID, reuseID); err == nil {
			return existing, common.FormatHash(existing.IDHash), true
		}
	}
	return nil, "", false
}
