// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import "github.com/qyiun666/MemHop/internal/repo/core"

// Thin wrapper; see internal/update.go ((db *DB) Update).
func (db *DB) Update(topicID string, text string, timestamp int64) error {
	return db.DB.Update(core.DefaultAgentID, topicID, text, timestamp)
}
