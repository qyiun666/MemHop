// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 API of the internal assembly layer: thin delegation to the sub layer
// DB methods, reusing the DB instance returned by Open.

package memhop

import (
	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// GetL5 薄层：实现见 internal/sub/l5.go（(db *DB) GetL5）。
func (db *DB) GetL5(id string) (*core.ActionChainSlot, error) {
	return db.DB.GetL5(id)
}

// CreateL5 薄层：写操作，持写锁后委托 sub 实现。
func (db *DB) CreateL5(title, trigger string) (string, error) {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return "", common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.CreateL5(title, trigger)
}

// UpdateL5 薄层：写操作，持写锁后委托 sub 实现。
func (db *DB) UpdateL5(id string, fields *sub.L5UpdateFields) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.UpdateL5(id, fields)
}

// DeleteL5 薄层：写操作，持写锁后委托 sub 实现。
func (db *DB) DeleteL5(id string) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.DeleteL5(id)
}

// ListL5 薄层：实现见 internal/sub/l5.go（(db *DB) ListL5）。
func (db *DB) ListL5(q sub.L5ListQuery) ([]core.ActionChainSlot, error) {
	return db.DB.ListL5(q)
}
