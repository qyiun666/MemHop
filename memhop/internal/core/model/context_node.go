// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 ContextNode + SceneNode — hypergraph skeleton nodes (context_node.rs).

package model

// ContextNode is the L1 lightweight graph node in the hypergraph skeleton.
// Points to one L2 ContextSlot; carries vector ref + importance, no text.
type ContextNode struct {
	IDHash        uint64   `json:"id_hash"`
	ContextID     uint64   `json:"context_id"`
	VectorPageRef uint64   `json:"vector_page_ref"`
	Importance    float32  `json:"importance"`
	Valence       float64  `json:"valence"`
	Arousal       float64  `json:"arousal"`
	CreatedAt     int64    `json:"created_at"`
	UpdatedAt     int64    `json:"updated_at"`
	Version       uint32   `json:"version"`
	EdgePtrs      []uint64 `json:"edge_ptrs"`
}

// SceneNode is the v2 renamed type that replaces ContextNode.
// Scene-level node pointing to multiple topics.
type SceneNode struct {
	IDHash        uint64   `json:"id_hash"`
	SceneID       uint64   `json:"scene_id"`
	TopicIDs      []uint64 `json:"topic_ids"`
	Depth         uint32   `json:"depth"`
	VectorPageRef uint64   `json:"vector_page_ref"`
	Importance    float32  `json:"importance"`
	Valence       float64  `json:"valence"`
	Arousal       float64  `json:"arousal"`
	CreatedAt     int64    `json:"created_at"`
	UpdatedAt     int64    `json:"updated_at"`
	EdgeIDs       []uint64 `json:"edge_ids"`
}
