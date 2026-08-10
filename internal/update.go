// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

// Update 薄层：实现见 internal/sub/update.go（(db *DB) Update），复用 Open 返回的 db。
func (db *DB) Update(topicID string, text string, timestamp int64) bool {
	return db.DB.Update(topicID, text, timestamp)
}
