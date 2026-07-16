// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
)

// GetProfile loads the L0 profile slot.
func (m *MemHop) GetProfile() (*model.ProfileSlot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return query.LoadProfileSlot(m.engine)
}

// SetProfile overwrites the agent profile with the given delta.
func (m *MemHop) SetProfile(delta query.ProfileDelta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	return query.WriteProfile(m.engine, delta)
}
