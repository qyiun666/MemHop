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
	SceneID string `json:"scene_id"`
	L3ID    string `json:"l3_id"`
}

type updateArgs struct {
	SceneID   string `json:"scene_id"`
	TopicID   string `json:"topic_id"`
	UserText  string `json:"user_text"`
	UserTS    int64  `json:"user_ts"`
	UserType  string `json:"user_type,omitempty"`
	AgentText string `json:"agent_text"`
	AgentTS   int64  `json:"agent_ts"`
	AgentType string `json:"agent_type,omitempty"`
}

// turnResult reports the topic one finished turn settled into.
type turnResult struct {
	OK      bool   `json:"ok"`
	TopicID string `json:"topic_id,omitempty"`
}

type dreamArgs struct {
	SceneID string `json:"scene_id"`
}

type dreamResult struct {
	Consolidated bool                `json:"consolidated"`
	Report       *memhop.DreamReport `json:"report,omitempty"`
}

type statusResult struct {
	Closed     bool `json:"closed"`
	SceneCount int  `json:"scene_count"`
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
		Description: "读取一个场景（= 宿主会话）的记忆并开启本轮：返回 L0 画像、该场景 depth-1 话题集（每个话题带提炼关键词与 L4 原文 ID，即宿主本轮的上下文），以及 new_topic_id —— 本次读取为即将进行的这一轮铸出的话题 ID，memhop_update 与 L6 轨迹写入都按它落笔。scene_id 为空时新建场景并返回其 id（16 位 hex，名字由库生成）；非空时必须已存在。l3_id 只在新建时生效。",
		InputSchema: objSchema(map[string]any{
			"scene_id": strProp("场景 ID（16 位 hex），可选；留空 = 新建场景"),
			"l3_id":    strProp("新建场景挂靠的 L3 项目域 ID（16 位 hex，可选）"),
		}),
	}, handle[searchArgs, memhop.SearchResult](func(a searchArgs) (memhop.SearchResult, error) {
		res, err := db.Search(memhop.SearchQuery{
			SceneID: a.SceneID,
			L3ID:    a.L3ID,
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
		Description: "沉淀一整轮对话：把用户原文与 agent 原文各写为 L4 档案，并用一次提炼产出该轮话题的关键词。topic_id 用 memhop_search 返回的 new_topic_id（一轮一个话题）；scene_id 必须是已存在场景。同一 topic_id 重复沉淀是覆盖而不是新增，所以超时后可安全重试。user_type/agent_type 声明该侧内容的类型（text/image/video/document/audio/code/other，缺省 text），非文本侧把媒体路径或 URL 写在对应 text 字段里，关键词仍从该字段提炼。返回该话题的 topic_id（16 位 hex）。",
		InputSchema: objSchema(map[string]any{
			"scene_id":   strProp("场景 ID（16 位 hex），必填，须已存在"),
			"topic_id":   strProp("本轮话题 ID（16 位 hex），必填，取自 memhop_search 的 new_topic_id"),
			"user_text":  strProp("用户原文（非文本时填媒体路径/URL），必填"),
			"user_ts":    intProp("用户消息 Unix 毫秒时间戳，必填"),
			"user_type":  strProp("用户侧内容类型（可选）：text | image | video | document | audio | code | other，缺省 text"),
			"agent_text": strProp("agent 回复原文（非文本时填媒体路径/URL），必填"),
			"agent_ts":   intProp("agent 回复 Unix 毫秒时间戳，必填"),
			"agent_type": strProp("agent 侧内容类型（可选）：text | image | video | document | audio | code | other，缺省 text"),
		}, "scene_id", "topic_id", "user_text", "user_ts", "agent_text", "agent_ts"),
	}, handle[updateArgs, turnResult](func(a updateArgs) (turnResult, error) {
		userType, err := resolveContentType(a.UserType)
		if err != nil {
			return turnResult{}, err
		}
		agentType, err := resolveContentType(a.AgentType)
		if err != nil {
			return turnResult{}, err
		}
		topicID, err := db.Update(memhop.TurnUpdate{
			SceneID:   a.SceneID,
			TopicID:   a.TopicID,
			UserText:  a.UserText,
			UserTS:    a.UserTS,
			UserType:  userType,
			AgentText: a.AgentText,
			AgentTS:   a.AgentTS,
			AgentType: agentType,
		})
		return turnResult{OK: err == nil, TopicID: topicID}, err
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
		Description: "将当前状态持久化到磁盘（记录索引 + A/B header），不关闭数据库。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[updateResult](func() (updateResult, error) {
		return updateResult{OK: true}, db.Checkpoint()
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_status",
		Description: "数据库健康状态：是否已关闭、已登记多少场景（= 宿主会话数）。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[statusResult](func() (statusResult, error) {
		scenes, err := db.ListScenes()
		return statusResult{Closed: db.IsClosed(), SceneCount: len(scenes)}, err
	}))
}
