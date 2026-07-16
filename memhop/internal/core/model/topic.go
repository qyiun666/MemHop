// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 TopicSlot — dual-track conversation node (context.rs).
// Core data model of the MemHop memory database.

package model

import (
	"fmt"

	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// TopicSlot is the L2 dual-track conversation node.
//
// Tree structure: parent_id (nil = depth-1 root) + children_ids.
// Depth 1 = raw conversation turn, 2 = compressed group, 3 = meta summary;
// depth >= 4 triggers subtree deletion during dream compression.
type TopicSlot struct {
	ID          uint64   `json:"id"`
	SceneID     uint64   `json:"scene_id"`
	ParentID    *uint64  `json:"parent_id,omitempty"`
	ChildrenIDs []uint64 `json:"children_ids"`
	Depth       uint8    `json:"depth"`

	// User track
	UserKeywords  []string `json:"user_keywords"`
	UserTimestamp int64    `json:"user_timestamp"`
	UserL4Refs    []uint64 `json:"user_l4_refs"`
	UserL3Refs    []uint64 `json:"user_l3_refs"`

	// Agent track
	AgentKeywords  []string `json:"agent_keywords"`
	AgentTimestamp int64    `json:"agent_timestamp"`
	AgentL4Refs    []uint64 `json:"agent_l4_refs"`
	AgentL3Refs    []uint64 `json:"agent_l3_refs"`

	// Compression fields (depth >= 2)
	FusedKeywords []string `json:"fused_keywords"`
	FusedSummary  *string  `json:"fused_summary,omitempty"`

	// Retrieval
	CentroidPageRef uint64 `json:"centroid_page_ref"`

	// Metadata
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	Version   uint32 `json:"version"`
}

// ComputeTopicID produces an idempotent ID: xxhash64("scene_id:user_ts:agent_ts").
// Compatible with Rust TopicSlot::compute_id.
func ComputeTopicID(sceneID uint64, userTS, agentTS int64) uint64 {
	combined := fmt.Sprintf("%d:%d:%d", sceneID, userTS, agentTS)
	return hash.HashID(combined)
}
