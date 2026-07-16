// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 HyperedgeSlot + SceneEdge — edges in the hypergraph skeleton (hyperedge.rs).

package model

// HyperedgeSlot is the L1 hyperedge connecting multiple ContextNodes.
// Inline: up to 8 node_ptrs; overflow_page for larger sets.
type HyperedgeSlot struct {
	IDHash       uint64        `json:"id_hash"`
	Kind         HyperedgeKind `json:"kind"`
	NodePtrs     []uint64      `json:"node_ptrs"`
	Weight       float32       `json:"weight"`
	CreatedAt    int64         `json:"created_at"`
	UpdatedAt    int64         `json:"updated_at"`
	Version      uint32        `json:"version"`
	OverflowPage uint32        `json:"overflow_page"`
}

// SceneEdge is the v2 renamed type for L1 hyperedges.
type SceneEdge struct {
	IDHash    uint64        `json:"id_hash"`
	Kind      HyperedgeKind `json:"kind"`
	NodeIDs   []uint64      `json:"node_ids"`
	Weight    float32       `json:"weight"`
	CreatedAt int64         `json:"created_at"`
}
