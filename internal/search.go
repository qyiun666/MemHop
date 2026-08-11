// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search API of the internal assembly layer: thin delegation to the sub
// layer DB method, reusing the DB instance returned by Open.

package memhop

import "github.com/qyiun666/MemHop/internal/sub"

// Thin wrapper; see internal/sub/search.go ((db *DB) Search).
func (db *DB) Search(q sub.SearchQuery) (*sub.SearchResult, error) {
	return db.DB.Search(q)
}
