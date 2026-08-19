// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 API of the public facade: thin delegation to the internal layer
// DB methods, reusing the DB instance returned by Open.

package api

import (
	"github.com/qyiun666/MemHop/internal/common"
)

func (db *DB) GetL0() (*ProfileSlot, error) {
	return db.DB.GetL0()
}

func (db *DB) UpdateL0(slot *ProfileSlot) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.UpdateL0(slot)
}
