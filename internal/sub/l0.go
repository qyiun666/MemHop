// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 profile operations of the sub layer: thin wrappers over the repo layer.

package sub

import (
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// GetL0 reads the profile singleton; returns an empty profile when absent.
func (db *DB) GetL0() (*core.ProfileSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	slot, err := repo.GetProfileL0(db.engine)
	if err != nil {
		return &core.ProfileSlot{}, nil
	}
	return slot, nil
}

// UpdateL0 overwrites the profile (ID forced to hash("profile")); the
// write lock comes from the internal layer.
func (db *DB) UpdateL0(slot *core.ProfileSlot) error {
	return repo.UpdateProfileL0(db.engine, slot)
}
