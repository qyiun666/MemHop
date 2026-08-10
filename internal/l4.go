// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 API of the internal assembly layer: thin delegation to the sub layer
// DB methods, reusing the DB instance returned by Open.

package memhop

import (
	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// SearchL4 薄层：实现见 internal/sub/l4.go（(db *DB) SearchL4）。
func (db *DB) SearchL4(q sub.L4Query) ([]core.ArchiveSlot, error) {
	return db.DB.SearchL4(q)
}

// GetArchive 薄层：实现见 internal/sub/l4.go（(db *DB) GetArchive）。
func (db *DB) GetArchive(id string) (*core.ArchiveSlot, error) {
	return db.DB.GetArchive(id)
}
