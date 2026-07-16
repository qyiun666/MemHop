// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 ArchiveSlot — raw conversation storage (archive.rs).
// Immutable ground truth; no version field.

package model

// ArchiveSlot stores raw conversation content.
// Text content is stored inline; non-text media store file paths.
type ArchiveSlot struct {
	IDHash      uint64      `json:"id_hash"`
	ContentType ContentType `json:"content_type"`
	Role        uint8       `json:"role"` // 0=user, 1=agent, 2=system
	ContextID   uint64      `json:"context_id"`
	CreatedAt   int64       `json:"created_at"`
	Content     string      `json:"content"`
	Metadata    *string     `json:"metadata,omitempty"`
}
