// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L7 trajectory operations of the internal layer: host-appended event log per
// session plus Crystallize (L7 → L5) as an explicit host-triggered step.
// Dream does not participate in L7; the host purges via DeleteTrajectory.

package internal

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// maxTrajectoryPayload caps a single event payload (no raw token streams).
const maxTrajectoryPayload = 4 * 1024

// AppendTrajectory appends one event to a session's trajectory; Seq is
// derived from the current max + 1, so the host never counts sequences.
// The write lock comes from the internal layer to serialize Seq allocation.
func (db *DB) AppendTrajectory(sessionID string, ev core.TrajectorySlot) error {
	parsed, err := common.ParseID(sessionID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse session id", err)
	}
	if ev.EventType == "" || ev.Timestamp <= 0 {
		return common.NewError(common.ErrInvalidQuery, "EventType and Timestamp are required")
	}
	events, err := repo.ReadTrajectory(db.engine, parsed)
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
		ev.Payload = truncateUTF8(ev.Payload, maxTrajectoryPayload)
	}
	ev.SessionID = parsed
	ev.Seq = maxSeq + 1
	return repo.AppendTrajectory(db.engine, ev)
}

// truncateUTF8 cuts s to at most max bytes without splitting a UTF-8 rune.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	t := s[:max]
	for len(t) > 0 && !utf8.ValidString(t) {
		t = t[:len(t)-1]
	}
	return t
}

// ReadTrajectory returns a session's trajectory events ordered by Seq.
func (db *DB) ReadTrajectory(sessionID string) ([]core.TrajectorySlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	parsed, err := common.ParseID(sessionID)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse session id", err)
	}
	return repo.ReadTrajectory(db.engine, parsed)
}

// DeleteTrajectory removes a session's trajectory events; write lock comes
// from the internal layer.
func (db *DB) DeleteTrajectory(sessionID string) error {
	parsed, err := common.ParseID(sessionID)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse session id", err)
	}
	return repo.DeleteTrajectory(db.engine, parsed)
}

// CrystallizeResult reports L5 capabilities created/reused/merged from a
// trajectory. Crystallized capabilities are drafts until the host activates
// them.
type CrystallizeResult struct {
	CreatedIDs []string `json:"created_ids"`
	ReusedIDs  []string `json:"reused_ids"`
	MergedIDs  []string `json:"merged_ids"`
	Errors     []string `json:"errors,omitempty"`
}

// Crystallize extracts L5 capability candidates from a session's trajectory
// (L7 → L5). The LLM receives the existing capability catalog so repeated
// crystallization reuses or merges instead of duplicating. The LLM call runs
// outside both locks; writes take the write lock.
func (db *DB) Crystallize(ctx context.Context, sessionID string) (*CrystallizeResult, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	parsed, err := common.ParseID(sessionID)
	if err != nil {
		db.mu.RUnlock()
		return nil, common.NewError(common.ErrInvalidQuery, "parse session id", err)
	}
	events, err := repo.ReadTrajectory(db.engine, parsed)
	existing := activeCapabilities(repo.ListCapabilitiesL5(db.engine))
	db.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, common.NewError(common.ErrNotFound, "no trajectory for session")
	}
	out, err := db.llm.Crystallize(ctx, events, existing)
	if err != nil {
		return nil, err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed.Load() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	result := &CrystallizeResult{CreatedIDs: []string{}, ReusedIDs: []string{}, MergedIDs: []string{}}
	for _, cand := range out.Capabilities {
		// reuse/merge candidates locate an existing capability by name or
		// ReuseID, so their payload may be minimal (a reuse decision does
		// not require a full type/resources). Only create candidates need
		// the complete import validation.
		action := strings.ToLower(strings.TrimSpace(cand.Action))
		if action != "reuse" && action != "merge" {
			if err := validateCapabilityImport(&cand.Capability); err != nil {
				result.Errors = append(result.Errors, cand.Capability.Name+": "+err.Error())
				continue
			}
		}
		id, disposition, err := db.applyCrystallizedCapability(cand, sessionID)
		if err != nil {
			return nil, err
		}
		switch disposition {
		case "reuse":
			result.ReusedIDs = append(result.ReusedIDs, id)
		case "merge":
			result.MergedIDs = append(result.MergedIDs, id)
		default:
			result.CreatedIDs = append(result.CreatedIDs, id)
		}
	}
	return result, nil
}

func activeCapabilities(caps []core.Capability) []core.Capability {
	out := caps[:0]
	for _, cap := range caps {
		if cap.Status == core.CapabilityActive {
			out = append(out, cap)
		}
	}
	return out
}

func (db *DB) applyCrystallizedCapability(cand CrystallizeCapability, sessionID string) (string, string, error) {
	now := time.Now().UnixMilli()
	action := strings.ToLower(strings.TrimSpace(cand.Action))
	if action == "" {
		action = "create"
	}
	cap := buildCrystallizedCapability(cand.Capability, sessionID, now)

	// Name is the canonical identity. A create candidate whose name already
	// exists is always treated as reuse: crystallization must never silently
	// overwrite an active capability.
	if existing, id, ok := db.findCrystallizeTarget(cap, cand.ReuseID); ok {
		if action == "merge" {
			if err := mergeCapabilityDefinition(db.engine, existing, cap, sessionID, now); err != nil {
				return "", "", err
			}
			return id, "merge", nil
		}
		return id, "reuse", nil
	}
	if _, err := repo.UpsertCapabilityL5(db.engine, cap); err != nil {
		return "", "", err
	}
	return common.FormatHash(cap.IDHash), "create", nil
}

// buildCrystallizedCapability assembles the draft capability record from a
// crystallize candidate; lifecycle fields are left to the caller.
func buildCrystallizedCapability(in CapabilityImport, _ string, now int64) *core.Capability {
	return &core.Capability{
		Name:      in.Name,
		Version:   defaultString(in.Version, "1"),
		Type:      in.Type,
		Summary:   in.Summary,
		Trigger:   in.Trigger,
		Resources: in.Resources,
		Workflow:  in.Workflow,
		Status:    core.CapabilityDraft,
		Origin:    core.CapabilityOriginCrystallized,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// findCrystallizeTarget locates an existing capability by name ID (canonical
// identity) then explicit ReuseID. found=false means a new record must be
// created.
func (db *DB) findCrystallizeTarget(cap *core.Capability, reuseID string) (*core.Capability, string, bool) {
	nameID := common.FormatHash(core.CapabilityID(cap.Name))
	if existing, err := repo.GetCapabilityL5(db.engine, nameID); err == nil {
		return existing, nameID, true
	}
	if reuseID != "" {
		if existing, err := repo.GetCapabilityL5(db.engine, reuseID); err == nil {
			return existing, common.FormatHash(existing.IDHash), true
		}
	}
	return nil, "", false
}

func mergeCapabilityDefinition(engine *core.StorageEngine, existing, incoming *core.Capability, _ string, now int64) error {
	existing.Version = incoming.Version
	existing.Type = incoming.Type
	existing.Summary = incoming.Summary
	existing.Trigger = incoming.Trigger
	existing.Resources = incoming.Resources
	existing.Workflow = incoming.Workflow
	existing.UpdatedAt = now
	_, err := repo.UpsertCapabilityL5(engine, existing)
	return err
}
