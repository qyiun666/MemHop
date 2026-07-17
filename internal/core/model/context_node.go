// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package model

// L1 超级图
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
