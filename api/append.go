// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import "github.com/qyiun666/MemHop/internal/repo/core"

// Thin wrapper; see internal/append.go ((db *DB) AppendL4Message).
func (db *DB) AppendL4Message(topicID string, text string, timestamp int64, role uint8) (uint64, error) {
	return db.DB.AppendL4Message(core.DefaultAgentID, topicID, text, timestamp, role)
}
