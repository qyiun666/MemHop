// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/core"
	"memhop/internal/core/query"
)

// GetL1Graph returns the full L1 layer graph (nodes + edges) for visualization.
func (m *MemHop) GetL1Graph(sceneID *string) (*query.L1Graph, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	sceneFilter := query.ParseSceneFilter(sceneID)
	return query.LoadL1Graph(m.engine, sceneFilter)
}
