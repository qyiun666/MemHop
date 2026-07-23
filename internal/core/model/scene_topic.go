// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 场景与话题：SceneSlot（场景容器）+ TopicSlot（话题节点树）。
// L2 按多个 scene 场景分，每个场景下是多个聊天记录摘要（TopicSlot 树结构）。

package model

import (
	"fmt"

	"memhop/internal/common/hash"
)

// ============================================================================
// SceneSlot — L2 场景容器
// ============================================================================

// SceneSlot 是 L2 的场景容器，一个场景包含多个会话 Topic。
type SceneSlot struct {
	SceneID   uint64 `json:"scene_id"`   // 场景唯一 ID（由场景名哈希生成）
	SceneName string `json:"scene_name"` // 场景名称
}

// NewSceneSlot 从名称创建 SceneSlot，ID 由名称 xxhash64 生成。
func NewSceneSlot(name string) SceneSlot {
	return SceneSlot{
		SceneID:   hash.HashID(name),
		SceneName: name,
	}
}

// ============================================================================
// TopicSlot — L2 双轨话题节点
// ============================================================================

// TopicSlot 是 L2 双轨会话节点（用户- Agent 双轨道）。
//
// 树结构：parent_id（nil = depth-1 根节点）+ children_ids。
// Depth 1 = 原始会话轮，2 = 压缩组，3 = 元摘要；
// depth >= 4 触发子树删除（Dream 压缩时）。
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
	AgentKeywords  []string `json:"agent_keywords"`  // agent 关键词
	AgentTimestamp int64    `json:"agent_timestamp"` // agent 消息时间戳
	AgentL4Refs    []uint64 `json:"agent_l4_refs"`   // agent 侧关联的 L4 档案 ID 列表
	AgentL3Refs    []uint64 `json:"agent_l3_refs"`   // agent 侧关联的 L3 超图节点 ID 列表

	// Compression fields (depth >= 2) — 融合字段（深度 >= 2 时填充）
	FusedKeywords []string `json:"fused_keywords"`          // 融合后的关键词列表
	FusedSummary  *string  `json:"fused_summary,omitempty"` // 融合摘要

	// Retrieval 检索
	CentroidPageRef uint64 `json:"centroid_page_ref"` // 本体向量嵌入页面引用（f16）

	// Metadata 元数据
	CreatedAt int64  `json:"created_at"` // 创建时间戳（毫秒）
	UpdatedAt int64  `json:"updated_at"` // 更新时间戳（毫秒）
	Version   uint32 `json:"version"`    // 版本号，用于乐观锁
}

// ComputeTopicID 根据 sceneID 和用户/agent 时间戳计算 Topic 唯一 ID。
func ComputeTopicID(sceneID uint64, userTS, agentTS int64) uint64 {
	combined := fmt.Sprintf("%d:%d:%d", sceneID, userTS, agentTS)
	return hash.HashID(combined)
}
