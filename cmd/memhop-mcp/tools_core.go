// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Core tools: search / update / dream / checkpoint / status — the memory
// loop entry points of the api package.

package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

type searchArgs struct {
	Text         string  `json:"text"`
	DirectedL2ID *string `json:"directed_l2_id,omitempty"`
	DirectedL3ID *string `json:"directed_l3_id,omitempty"`
	AutoCreate   bool    `json:"auto_create,omitempty"`
	Timestamp    int64   `json:"timestamp"`
}

type updateArgs struct {
	TopicID   string `json:"topic_id"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
}

type dreamArgs struct {
	SceneID string `json:"scene_id"`
}

type dreamResult struct {
	Consolidated bool                `json:"consolidated"`
	Report       *memhop.DreamReport `json:"report,omitempty"`
}

type statusResult struct {
	Closed          bool `json:"closed"`
	HasActiveScenes bool `json:"has_active_scenes"`
}

// registerCoreTools installs the memory-loop entry points; one register
// function per tool keeps each declaration list short.
func registerCoreTools(s *mcp.Server, db *memhop.Session) {
	registerSearchTool(s, db)
	registerUpdateTool(s, db)
	registerDreamTool(s, db)
	registerMaintenanceTools(s, db)
}

func registerSearchTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_search",
		Description: "搜索记忆并写入用户原文：三通道混合检索（BM25 + 向量 + 实体模糊），RRF 融合。返回 L0 画像、命中的 L2 场景上下文与关联上下文；auto_create=true 时自动建新话题并返回 new_topic_id（16 位 hex）。timestamp 为 Unix 毫秒，必填。",
		InputSchema: objSchema(map[string]any{
			"text":           strProp("用户原文，必填"),
			"directed_l2_id": strProp("限定在某个 L2 场景内检索（16 位 hex，可选）"),
			"directed_l3_id": strProp("限定在引用该 L3 知识的话题内检索（16 位 hex，可选）"),
			"auto_create":    boolProp("无命中时自动创建新话题（可选，默认 false）"),
			"timestamp":      intProp("Unix 毫秒时间戳，必填"),
		}, "text", "timestamp"),
	}, handleWithCtx[searchArgs, memhop.SearchResult](func(ctx context.Context, a searchArgs) (memhop.SearchResult, error) {
		res, err := db.Search(ctx, memhop.SearchQuery{
			Text:         a.Text,
			DirectedL2ID: a.DirectedL2ID,
			DirectedL3ID: a.DirectedL3ID,
			AutoCreate:   a.AutoCreate,
			Timestamp:    a.Timestamp,
		})
		if err != nil {
			return memhop.SearchResult{}, err
		}
		return *res, nil
	}))
}

func registerUpdateTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_update",
		Description: "将一条对话消息写入既有话题（不建新话题）：更新 L2 话题元数据、L4 档案与检索索引。返回是否成功写入。",
		InputSchema: objSchema(map[string]any{
			"topic_id":  strProp("话题 ID（16 位 hex），必填"),
			"text":      strProp("消息内容，必填"),
			"timestamp": intProp("Unix 毫秒时间戳，必填"),
		}, "topic_id", "text", "timestamp"),
	}, handle[updateArgs, updateResult](func(a updateArgs) (updateResult, error) {
		err := db.Update(a.TopicID, a.Text, a.Timestamp)
		return updateResult{OK: err == nil}, err
	}))
}

func registerDreamTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_dream",
		Description: "对指定场景执行梦境巩固（睡眠模拟）：L2 话题压缩融合、L1 节点同步、衰减与 L0 画像蒸馏。耗时较长；返回是否实际发生巩固（consolidated）与结构化报告 report（各阶段名称/状态/耗时、L2 压缩计数、L1 增删计数、L0 是否蒸馏）。",
		InputSchema: objSchema(map[string]any{
			"scene_id": strProp("场景 ID（16 位 hex），必填"),
		}, "scene_id"),
	}, handle[dreamArgs, dreamResult](func(a dreamArgs) (dreamResult, error) {
		rep, err := db.Dream(context.Background(), a.SceneID)
		out := dreamResult{}
		if rep != nil {
			out.Consolidated = rep.ConsolidatedScenes > 0
			out.Report = rep
		}
		return out, err
	}))
}

// registerMaintenanceTools installs the two no-argument lifecycle tools.
func registerMaintenanceTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_checkpoint",
		Description: "将当前状态持久化到磁盘（索引快照 + A/B header），不关闭数据库。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[updateResult](func() (updateResult, error) {
		return updateResult{OK: true}, db.Checkpoint()
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_status",
		Description: "数据库健康状态：是否已关闭、是否有激活场景（dream 巩固目标）。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[statusResult](func() (statusResult, error) {
		return statusResult{Closed: db.IsClosed(), HasActiveScenes: db.HasActiveScenes()}, nil
	}))
}
