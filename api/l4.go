// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 API of the public facade: thin delegation to the internal layer
// DB methods, reusing the DB instance returned by Open.

package api

// Thin wrapper; see internal/l4.go ((db *DB) SearchL4).
func (db *DB) SearchL4(q L4Query) ([]ArchiveSlot, error) {
	return db.DB.SearchL4(q)
}

// Thin wrapper; see internal/l4.go ((db *DB) GetArchive).
func (db *DB) GetArchive(id string) (*ArchiveSlot, error) {
	return db.DB.GetArchive(id)
}
