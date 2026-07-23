// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package model

// L4 聊天记录（Archive）：存储用户与 agent 的历史对话消息
// 每条消息归属于一个 L2 Context（场景），构成完整的对话上下文
// 支持检索、回放、蒸馏到 L3 Knowledge
type ArchiveSlot struct {
	IDHash      uint64      `json:"id_hash"`            // 消息唯一哈希标识
	ContentType ContentType `json:"content_type"`       // 内容类型（TEXT/IMAGE/CODE 等）
	Role        uint8       `json:"role"`               // 消息角色：0=user, 1=agent, 2=system
	ContextID   uint64      `json:"context_id"`         // 所属 L2 Context（场景）ID
	CreatedAt   int64       `json:"created_at"`         // 创建时间戳（毫秒）
	Content     string      `json:"content"`            // 消息正文
	Metadata    *string     `json:"metadata,omitempty"` // 可选元数据（JSON 格式扩展字段）
}
