// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 超级图（关联记忆图）：SceneNode（节点）+ HyperedgeSlot/SceneEdge（边）。
// L1 的目的是把 L2 Topic 摘要中的用户关键词、agent 关键词等关联起来，
// 方便检索 L2 上下文时找到相关的 L2 场景。

package model

// ============================================================================
// SceneNode — L1 超级图节点
// ============================================================================

// SceneNode 是 L1 超级图节点，关联多个独立的 L2 Topic。
type SceneNode struct {
	IDHash        uint64   `json:"id_hash"`         // 节点唯一哈希标识
	SceneID       uint64   `json:"scene_id"`        // 所属 L2 Scene ID
	TopicIDs      []uint64 `json:"topic_ids"`       // 关联的 L2 Topic ID 列表
	Depth         uint32   `json:"depth"`           // 场景层级深度
	VectorPageRef uint64   `json:"vector_page_ref"` // 向量嵌入页面引用（f16）
	Importance    float32  `json:"importance"`      // 重要性分数（Dream 衰减用）
	Valence       float64  `json:"valence"`         // 情感效价（正/负，影响衰减速率）
	Arousal       float64  `json:"arousal"`         // 情感唤醒度（强度，影响衰减速率）
	CreatedAt     int64    `json:"created_at"`      // 创建时间戳（毫秒）
	UpdatedAt     int64    `json:"updated_at"`      // 最后更新时间戳（毫秒）
	EdgeIDs       []uint64 `json:"edge_ids"`        // 关联的超图边 ID 列表
}

// ============================================================================
// HyperedgeSlot — L1 超边（存储层格式）
// ============================================================================

// HyperedgeSlot 是 L1 超边，连接多个 SceneNode。
// 内联优化：≤8 个 node_ptrs 直接内联；超出时用 overflow_page 分页存储。
type HyperedgeSlot struct {
	IDHash       uint64        `json:"id_hash"`       // 边唯一哈希标识
	Kind         HyperedgeKind `json:"kind"`          // 边的语义类型（共现/因果/语义/时序/层级/序列）
	NodePtrs     []uint64      `json:"node_ptrs"`     // 关联的 L1 SceneNode ID 列表
	Weight       float32       `json:"weight"`        // 边权重，用于衰减和排序
	CreatedAt    int64         `json:"created_at"`    // 创建时间戳（毫秒）
	UpdatedAt    int64         `json:"updated_at"`    // 最后更新时间戳（毫秒）
	Version      uint32        `json:"version"`       // 版本号，用于乐观锁
	OverflowPage uint32        `json:"overflow_page"` // inline 溢出页面索引（≤8 内联，超限分页）
}

// ============================================================================
// SceneEdge — L1 超边（逻辑层格式，v2 重命名）
// ============================================================================

// SceneEdge 是 HyperedgeSlot 的 v2 简化版本，用于上层衰减逻辑。
// 去掉了 Version 和 OverflowPage，增加了 LastDecayAt 追踪衰减进度。
type SceneEdge struct {
	IDHash    uint64        `json:"id_hash"`    // 边唯一哈希标识
	Kind      HyperedgeKind `json:"kind"`       // 边的语义类型（共现/因果/语义/时序/层级/序列）
	NodeIDs   []uint64      `json:"node_ids"`   // 关联的 L1 SceneNode ID 列表
	Weight    float32       `json:"weight"`     // 边权重，用于衰减和排序
	CreatedAt int64         `json:"created_at"` // 创建时间戳（毫秒）
	// LastDecayAt 是上次衰减时间戳（毫秒）；0 表示从未衰减过，
	// 此时首次衰减从 CreatedAt 开始计算。
	LastDecayAt int64 `json:"last_decay_at"`
}
