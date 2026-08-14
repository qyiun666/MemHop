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

// Thin wrapper; see internal/sub/l5.go ((db *DB) GetPlugin).
func (db *DB) GetPlugin(id string) (*core.PluginSlot, error) {
	return db.DB.GetPlugin(id)
}

// Thin wrapper; write op, delegates under the write lock.
func (db *DB) ImportPlugin(path string) (string, error) {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return "", common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.ImportPlugin(path)
}

// Thin wrapper; write op, delegates under the write lock.
func (db *DB) DeletePlugin(id string) error {
	db.DB.Lock()
	defer db.DB.Unlock()
	if db.DB.IsClosed() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.DB.DeletePlugin(id)
}

// Thin wrapper; see internal/sub/l5.go ((db *DB) ListPlugins).
func (db *DB) ListPlugins(q sub.PluginListQuery) ([]core.PluginSlot, error) {
	return db.DB.ListPlugins(q)
}
