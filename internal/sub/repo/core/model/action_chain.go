// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 ActionChainSlot + ActionStep — procedural knowledge (action_chain.rs).

package model

// L5 动作链
type ActionChainSlot struct {
	IDHash        uint64      `json:"id_hash"`        // 动作链唯一哈希标识（xxhash64）
	Title         string      `json:"title"`          // 动作链描述性标题
	Trigger       string      `json:"trigger"`        // 触发条件（关键词 / 场景描述）
	Status        ChainStatus `json:"status"`         // 生命周期状态（0=draft, 1=active, 2=deprecated）
	Confidence    float32     `json:"confidence"`     // 置信度（0.0 ~ 1.0）
	SuccessRate   float32     `json:"success_rate"`   // 历史执行成功率（0.0 ~ 1.0）
	TriggerCount  uint32      `json:"trigger_count"`  // 累计触发次数
	LastTriggered int64       `json:"last_triggered"` // 最后一次触发时间戳（毫秒）
	CreatedAt     int64       `json:"created_at"`     // 创建时间戳（毫秒）
	UpdatedAt     int64       `json:"updated_at"`     // 最后更新时间戳（毫秒）
}

// ActionStep is an individual step within an ActionChainSlot.
type ActionStep struct {
	IDHash     uint64  `json:"id_hash"`              // 步骤唯一哈希标识（xxhash64）
	ChainID    uint64  `json:"chain_id"`             // 所属 ActionChainSlot 的 IDHash
	StepOrder  uint16  `json:"step_order"`           // 执行顺序序号（0-based）
	Action     string  `json:"action"`               // 动作指令描述
	Parameters *string `json:"parameters,omitempty"` // 可选参数（JSON 格式）
	CreatedAt  int64   `json:"created_at"`           // 创建时间戳（毫秒）
}
