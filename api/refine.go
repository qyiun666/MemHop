// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import "context"

// Thin wrapper; see internal/refine.go ((db *DB) RefineTopicKeywords).
// The ctx cancels LLM keyword extraction.
func (db *DB) RefineTopicKeywords(ctx context.Context, topicID string) error {
	return db.DB.RefineTopicKeywords(ctx, topicID)
}
