// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search API of the public facade: thin delegation to the internal
// layer DB method, reusing the DB instance returned by Open.

package api

import (
	"context"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Thin wrapper; see internal/search.go ((db *DB) Search). The ctx cancels
// LLM keyword extraction, encoder calls and the internally triggered Dream.
func (db *DB) Search(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	return db.DB.Search(ctx, core.DefaultAgentID, q)
}
