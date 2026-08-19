// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

// Thin wrapper; see internal/sub/update.go ((db *DB) Update).
func (db *DB) Update(topicID string, text string, timestamp int64) (bool, error) {
	return db.DB.Update(topicID, text, timestamp)
}
