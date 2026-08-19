// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search API of the public facade: thin delegation to the internal
// layer DB method, reusing the DB instance returned by Open.

package api

// Thin wrapper; see internal/search.go ((db *DB) Search).
func (db *DB) Search(q SearchQuery) (*SearchResult, error) {
	return db.DB.Search(q)
}
