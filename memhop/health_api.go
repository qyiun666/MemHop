// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// HealthCheck returns the current health status of the database.
func (m *MemHop) HealthCheck() (*query.HealthStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	layerCounts := m.countLayers()
	issues := collectIssues(m.encoder, layerCounts)
	return &query.HealthStatus{
		OK:                len(issues) == 0,
		DBSizeBytes:       m.engine.FileSize(),
		LayerCounts:       layerCounts,
		EncoderConfigured: m.encoder != nil && m.encoder.IsAvailable(),
		Issues:            issues,
	}, nil
}

// SessionStatus returns the current session activation state.
func (m *MemHop) SessionStatus() (*query.SessionStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	rawIDs := m.sessionMgr.GetActiveTopicIDs()
	hexIDs := make([]string, len(rawIDs))
	for i, id := range rawIDs {
		hexIDs[i] = hash.FormatHash(id)
	}
	return &query.SessionStatus{
		ActiveTopicIDs: hexIDs,
		Count:          len(hexIDs),
		IsEmpty:        len(hexIDs) == 0,
	}, nil
}

func (m *MemHop) countLayers() map[string]int {
	counts := map[string]int{
		"l0_profile": 0, "l1_engram": 0, "l2_topic": 0,
		"l3_knowledge": 0, "l4_archive": 0, "l5_crystal": 0,
	}
	profileHash := hash.HashID("profile")
	m.engine.IterIndex(func(idHash, _ uint64) bool {
		rt, _, err := m.engine.ReadRecord(idHash)
		if err != nil {
			return true
		}
		switch rt {
		case storage.RecL0Profile:
			if idHash == profileHash {
				counts["l0_profile"]++
			}
		case storage.RecL1SceneNode:
			counts["l1_engram"]++
		case storage.RecL2Topic:
			counts["l2_topic"]++
		case storage.RecL3GraphSlot:
			counts["l3_knowledge"]++
		case storage.RecL4Archive:
			counts["l4_archive"]++
		case storage.RecL5ActionChain:
			counts["l5_crystal"]++
		}
		return true
	})
	return counts
}

func collectIssues(
	enc interface{ IsAvailable() bool },
	counts map[string]int,
) []string {
	var issues []string
	if enc == nil || !enc.IsAvailable() {
		issues = append(issues, "encoder not available")
	}
	if counts["l2_topic"] == 0 {
		issues = append(issues, "no L2 topics")
	}
	return issues
}
