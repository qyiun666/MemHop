// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/common/mherrors"
	"memhop/internal/core/model"
	"memhop/internal/query/crud"
)

// GetProfile loads the L0 profile slot.
func (m *MemHop) GetProfile() (*model.ProfileSlot, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return crud.LoadProfileSlot(m.engine)
}

// SetProfile overwrites the agent profile with the given delta.
func (m *MemHop) SetProfile(delta crud.ProfileDelta) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	// Invalidate profile cache on write.
	m.profileCache = nil
	return crud.WriteProfile(m.engine, delta)
}
