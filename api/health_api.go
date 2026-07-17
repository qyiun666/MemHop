// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/query/health"
	"memhop/internal/query/crud"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
)

// HealthCheck returns the current health status of the database.
func (m *MemHop) HealthCheck() (*health.HealthStatus, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	layerCounts := health.CountLayers(m.engine)
	issues := health.CollectIssues(m.encoder, layerCounts)
	return &health.HealthStatus{
		OK:                len(issues) == 0,
		DBSizeBytes:       m.engine.FileSize(),
		LayerCounts:       layerCounts,
		EncoderConfigured: m.encoder != nil && m.encoder.IsAvailable(),
		Issues:            issues,
	}, nil
}

// SessionStatus returns the current session activation state.
func (m *MemHop) SessionStatus() (*crud.SessionStatus, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	rawIDs := m.sessionMgr.GetActiveTopicIDs()
	hexIDs := make([]string, len(rawIDs))
	for i, id := range rawIDs {
		hexIDs[i] = hash.FormatHash(id)
	}
	return &crud.SessionStatus{
		ActiveTopicIDs: hexIDs,
		Count:          len(hexIDs),
		IsEmpty:        len(hexIDs) == 0,
	}, nil
}
