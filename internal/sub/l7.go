// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L7 trajectory operations of the sub layer: host-appended event log per
// session plus Crystallize (L7 → L5) as an explicit host-triggered step.
// Dream does not participate in L7; the host purges via DeleteTrajectory.

package sub

import (
	"context"
	"unicode/utf8"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
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

// CrystallizeResult reports the plugins created from a trajectory.
type CrystallizeResult struct {
	PluginIDs []string `json:"plugin_ids"`
}

// Crystallize extracts reusable plugins from a session's trajectory
// (L7 → L5) and persists them with Path = sessionID. The plugin ID
// (hash(name:trigger)) makes repeated crystallization idempotent. The
// LLM call runs outside both locks; writes take the write lock.
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
	db.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, common.NewError(common.ErrNotFound, "no trajectory for session")
	}
	out, err := db.llm.Crystallize(ctx, events)
	if err != nil {
		return nil, err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed.Load() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	result := &CrystallizeResult{PluginIDs: []string{}}
	path := sessionID
	for _, p := range out.Plugins {
		// Re-crystallizing the same name:trigger reuses the plugin ID, keeps
		// runtime fields (Confidence/SuccessRate/TriggerCount/...), and
		// refreshes the manifest and type label.
		pluginID, _, err := repo.CreateOrUpdatePluginL5(db.engine, p.Name, p.Trigger, p.PluginType, p.Manifest, &path)
		if err != nil {
			return nil, err
		}
		result.PluginIDs = append(result.PluginIDs, common.FormatHash(pluginID))
	}
	return result, nil
}
