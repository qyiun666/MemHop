// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 ActionChainSlot + ActionStep — procedural knowledge (action_chain.rs).

package model

// ActionChainSlot holds L5 procedural knowledge as ordered action sequences.
type ActionChainSlot struct {
	IDHash        uint64      `json:"id_hash"`
	Title         string      `json:"title"`
	Trigger       string      `json:"trigger"`
	Status        ChainStatus `json:"status"`
	Confidence    float32     `json:"confidence"`
	SuccessRate   float32     `json:"success_rate"`
	TriggerCount  uint32      `json:"trigger_count"`
	LastTriggered int64       `json:"last_triggered"`
	CreatedAt     int64       `json:"created_at"`
	UpdatedAt     int64       `json:"updated_at"`
	Version       uint32      `json:"version"`
}

// ActionStep is an individual step within an ActionChainSlot.
type ActionStep struct {
	IDHash     uint64  `json:"id_hash"`
	ChainID    uint64  `json:"chain_id"`
	StepOrder  uint16  `json:"step_order"`
	Action     string  `json:"action"`
	Parameters *string `json:"parameters,omitempty"` // JSON format
	CreatedAt  int64   `json:"created_at"`
}
