// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 API of the public facade: thin delegation to the internal layer
// DB methods, reusing the DB instance returned by Open.

package api

import "github.com/qyiun666/MemHop/internal/repo/core"

func (db *DB) GetL0() (*ProfileSlot, error) {
	return db.DB.GetL0(core.DefaultAgentID)
}

func (db *DB) UpdateL0(slot *ProfileSlot) error {
	return db.DB.UpdateL0(core.DefaultAgentID, slot)
}
