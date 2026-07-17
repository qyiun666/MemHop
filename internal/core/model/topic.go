// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package model

import (
	"fmt"
	"memhop/internal/hash"
)

// TopicSlot is the L2 dual-track conversation node.
//
// Tree structure: parent_id (nil = depth-1 root) + children_ids.
// Depth 1 = raw conversation turn, 2 = compressed group, 3 = meta summary;
// depth >= 4 triggers subtree deletion during dream compression.
type TopicSlot struct {
	ID          uint64   `json:"id"`                  // 节点唯一 ID
	SceneID     uint64   `json:"scene_id"`            // 所属场景 ID
	ParentID    *uint64  `json:"parent_id,omitempty"` // 父节点 ID（nil 表示 depth-1 根节点）
	ChildrenIDs []uint64 `json:"children_ids"`        // 子节点 ID 列表
	Depth       uint8    `json:"depth"`               // 树深度（1=原始会话轮, 2=压缩组, 3=元摘要, >=4 触发子树删除）

	// User track — 用户侧
	UserKeywords  []string `json:"user_keywords"`  // 用户关键词
	UserTimestamp int64    `json:"user_timestamp"` // 用户消息时间戳
	UserL4Refs    []uint64 `json:"user_l4_refs"`   // 用户侧关联的 L4 档案 ID 列表
	UserL3Refs    []uint64 `json:"user_l3_refs"`   // 用户侧关联的 L3 超图节点 ID 列表

	// Agent track — 助手侧
	AgentKeywords  []string `json:"agent_keywords"`  // agent关键词
	AgentTimestamp int64    `json:"agent_timestamp"` // agent消息时间戳
	AgentL4Refs    []uint64 `json:"agent_l4_refs"`   // agent侧关联的 L4 档案 ID 列表
	AgentL3Refs    []uint64 `json:"agent_l3_refs"`   // agent侧关联的 L3 超图节点 ID 列表

	// Compression fields (depth >= 2) — 融合字段（深度 >= 2 时填充）
	FusedKeywords []string `json:"fused_keywords"`          // 融合后的关键词列表
	FusedSummary  *string  `json:"fused_summary,omitempty"` // 融合摘要

	// Retrieval 检索
	CentroidPageRef uint64 `json:"centroid_page_ref"` // 本体向量嵌入页面引用（f16）

	// Metadata 元数据
	CreatedAt int64 `json:"created_at"` // 创建时间戳（毫秒）
	UpdatedAt int64 `json:"updated_at"` // 更新时间戳（毫秒）
}

func ComputeTopicID(sceneID uint64, userTS, agentTS int64) uint64 {
	combined := fmt.Sprintf("%d:%d:%d", sceneID, userTS, agentTS)
	return hash.HashID(combined)
}
