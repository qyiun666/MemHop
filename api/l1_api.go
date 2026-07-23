// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/common/mherrors"
	"memhop/internal/query/crud"
)

// GetL1Graph returns the full L1 layer graph (nodes + edges) for visualization.
func (m *MemHop) GetL1Graph(sceneID *string) (*crud.L1Graph, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	sceneFilter := crud.ParseSceneFilter(sceneID)
	return crud.LoadL1Graph(m.engine, sceneFilter)
}
