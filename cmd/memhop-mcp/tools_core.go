// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Core loop tools: the memory recall/store/consolidate cycle plus
// persistence and status.

package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop"
)

type searchArgs struct {
	Text         string  `json:"text"`
	Timestamp    int64   `json:"timestamp"`
	AutoCreate   bool    `json:"auto_create,omitempty"`
	DirectedL2ID *string `json:"directed_l2_id,omitempty"`
	DirectedL3ID *string `json:"directed_l3_id,omitempty"`
}

type updateArgs struct {
	TopicID   string `json:"topic_id"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
}

type updateResult struct {
	OK bool `json:"ok"`
}

type dreamArgs struct {
	SceneID string `json:"scene_id"`
}

type dreamResult struct {
	Consolidated bool `json:"consolidated"`
}

type statusResult struct {
	Closed          bool `json:"closed"`
	HasActiveScenes bool `json:"has_active_scenes"`
}

func registerCoreTools(s *mcp.Server, db *memhop.DB) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_search",
		Description: "核心记忆检索：回忆与用户侧写入。以文本检索匹配上下文（BM25 + 向量 + 实体模糊三通道 RRF），同时将用户原文提取事实后追加到最佳匹配话题。返回匹配 topic、对应的 L4 archive 全文与本轮新建 topic ID（供 memhop_update 追加 Agent 回复）。",
		InputSchema: objSchema(map[string]any{
			"text":           strProp("搜索文本（对话原文），必填"),
			"timestamp":      intProp("消息的 Unix 毫秒时间戳，必填"),
			"auto_create":    boolProp("自动创建新话题"),
			"directed_l2_id": strProp("定向 L2 检索：仅在该主题子树内搜索（16 位 hex）"),
			"directed_l3_id": strProp("定向 L3 检索（16 位 hex）"),
		}, "text", "timestamp"),
	}, handle[searchArgs, memhop.SearchResult](func(a searchArgs) (memhop.SearchResult, error) {
		res, err := db.Search(memhop.SearchQuery{
			Text:         a.Text,
			Timestamp:    a.Timestamp,
			AutoCreate:   a.AutoCreate,
			DirectedL2ID: a.DirectedL2ID,
			DirectedL3ID: a.DirectedL3ID,
		})
		if err != nil {
			return memhop.SearchResult{}, err
		}
		return *res, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_update",
		Description: "Agent 侧写入：将 Agent 回复追加到 memhop_search 返回的 topic（role=agent），并同步更新话题关键词与稀疏索引。",
		InputSchema: objSchema(map[string]any{
			"topic_id":  strProp("目标 topic ID（memhop_search 返回的 new_topic_id，16 位 hex），必填"),
			"text":      strProp("Agent 回复原文，必填"),
			"timestamp": intProp("Unix 毫秒时间戳，必填"),
		}, "topic_id", "text", "timestamp"),
	}, handle[updateArgs, updateResult](func(a updateArgs) (updateResult, error) {
		ok, err := db.Update(a.TopicID, a.Text, a.Timestamp)
		if err != nil {
			return updateResult{}, err
		}
		return updateResult{OK: ok}, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_dream",
		Description: "记忆巩固周期（模拟睡眠）：五阶段流水线（L2 压缩 → L1 重建 → L1 衰减 → L0 画像 → L0 蒸馏）。会调用 LLM，耗时长，客户端需放宽超时。scene_id 可选：指定仅在该场景内巩固；不传则巩固内存中全部激活场景。",
		InputSchema: objSchema(map[string]any{
			"scene_id": strProp("可选：指定仅在该场景内巩固（16 位 hex）；不传则巩固全部激活场景"),
		}),
	}, handle[dreamArgs, dreamResult](func(a dreamArgs) (dreamResult, error) {
		// Use Dream (not RunDream): Dream takes the DB write lock and checks
		// the closed flag, matching the single-agent serial contract.
		ok, err := db.Dream(context.Background(), a.SceneID)
		return dreamResult{Consolidated: ok}, err
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_checkpoint",
		Description: "将当前状态持久化到磁盘（索引快照 + A/B header），不关闭数据库。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[updateResult](func() (updateResult, error) {
		return updateResult{OK: true}, db.Checkpoint()
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_status",
		Description: "数据库健康状态：是否已关闭、是否有激活场景。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[statusResult](func() (statusResult, error) {
		return statusResult{Closed: db.IsClosed(), HasActiveScenes: db.HasActiveScenes()}, nil
	}))
}
