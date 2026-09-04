// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 tools: trajectory (host-appended operation events) plus crystallize
// (L6 → L5 capability extraction).

package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

type trajectoryAppendArgs struct {
	SessionID string `json:"session_id"`
	EventType string `json:"event_type"`
	Payload   string `json:"payload,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

type sessionIDArgs struct {
	SessionID string `json:"session_id"`
}

// registerL6Tools installs the trajectory and crystallize tools; each
// register function owns one cohesive tool group.
func registerL6Tools(s *mcp.Server, db *memhop.Session) {
	registerTrajectoryAppendTool(s, db)
	registerTrajectoryReadTools(s, db)
	registerCrystallizeTool(s, db)
}

func registerTrajectoryAppendTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_trajectory_append",
		Description: "向本轮轨迹追加一条 L6 操作事件（每轮一个键：本轮 memhop_search 铸出的话题 ID，Seq 自动分配）。本工具只写轮内事件，event_type 由宿主自定（惯例：llm_request/llm_output/tool_call/tool_result/subagent_spawn/subagent_done/context_inject/ask_user/user_reply）；事件的 topic_id 一律由该键回填，不用也不要传。Payload 超过 4KB 会被截断；轨迹只追加，超出保留窗口由 Dream 自动清理。",
		InputSchema: objSchema(map[string]any{
			"session_id": strProp("本轮轨迹键 = 本轮 search 铸出的话题 ID（16 位 hex），必填"),
			"event_type": strProp("事件类型，必填"),
			"payload":    strProp("事件内容（超过 4KB 截断）"),
			"timestamp":  intProp("Unix 毫秒时间戳，必填"),
		}, "session_id", "event_type", "timestamp"),
	}, handle[trajectoryAppendArgs, updateResult](func(a trajectoryAppendArgs) (updateResult, error) {
		slot := toTrajectorySlot(a)
		return updateResult{OK: true}, db.AppendTrajectory(a.SessionID, "", slot)
	}))
}

// toTrajectorySlot maps the JSON append request into the api DTO. Seq, the
// session id and the event's topic id are assigned by the library from the key.
func toTrajectorySlot(a trajectoryAppendArgs) memhop.TrajectorySlot {
	return memhop.TrajectorySlot{
		EventType: a.EventType,
		Payload:   a.Payload,
		Timestamp: a.Timestamp,
	}
}

// registerTrajectoryReadTools installs the trajectory read surface: the
// domain-wide session list plus per-turn reads. Retention is automatic —
// Dream drops events older than 7 days — so there are no delete tools.
func registerTrajectoryReadTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_trajectory_sessions",
		Description: "列出本租户全部 L6 轮轨迹（每轮一条：16 位 hex 轮 ID、事件数、最后追加时间），用于发现可结晶的轮次；超过 7 天的轨迹由 Dream 自动清理。",
		InputSchema: objSchema(nil),
	}, handleNoArgs(func() ([]memhop.TrajectorySessionSummary, error) {
		return db.ListTrajectorySessions()
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_trajectory_read",
		Description: "读取轮轨迹的全部操作事件（按 Seq 升序）。",
		InputSchema: objSchema(map[string]any{
			"session_id": strProp("轮轨迹 ID（16 位 hex），必填"),
		}, "session_id"),
	}, handle[sessionIDArgs, []memhop.TrajectorySlot](func(a sessionIDArgs) ([]memhop.TrajectorySlot, error) {
		return db.ReadTrajectory(a.SessionID)
	}))
}

func registerCrystallizeTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_crystallize",
		Description: "从轮轨迹提取可复用 L5 能力候选（L6 → L5）。本轮轨迹带 L2 话题 ID 时自动聚合同话题的跨轮轨迹再蒸馏。调用 LLM，耗时长；候选保存为 draft，需宿主激活；重复结晶按名称和指纹去重。",
		InputSchema: objSchema(map[string]any{
			"session_id": strProp("会话 ID（16 位 hex），必填"),
		}, "session_id"),
	}, handle[sessionIDArgs, memhop.CrystallizeResult](func(a sessionIDArgs) (memhop.CrystallizeResult, error) {
		res, err := db.Crystallize(context.Background(), a.SessionID)
		if err != nil {
			return memhop.CrystallizeResult{}, err
		}
		return *res, nil
	}))
}
