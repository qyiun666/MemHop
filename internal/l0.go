// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 profile operations of the internal layer: thin wrappers over the repo layer.

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// GetL0 reads the profile singleton. An absent profile is returned as an
// empty, non-nil ProfileSlot; storage/corruption errors are surfaced.
func (db *DB) GetL0() (*core.ProfileSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	slot, err := repo.GetProfileL0(db.engine)
	if err != nil {
		if common.CodeOf(err) == common.ErrNotFound {
			return &core.ProfileSlot{}, nil
		}
		return nil, err
	}
	return slot, nil
}

// UpdateL0 overwrites the profile (ID forced to hash("profile")); the
// write lock comes from the internal layer.
func (db *DB) UpdateL0(slot *core.ProfileSlot) error {
	if slot == nil {
		return common.NewError(common.ErrInvalidQuery, "UpdateL0: slot is required")
	}
	return repo.UpdateProfileL0(db.engine, slot)
}
