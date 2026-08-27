// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 tools: trajectory (host-appended operation events) plus crystallize
// (L6 → L5 capability extraction).

package main

import (
	"context"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

type trajectoryAppendArgs struct {
	SessionID string  `json:"session_id"`
	EventType string  `json:"event_type"`
	Payload   string  `json:"payload,omitempty"`
	L4Ref     *string `json:"l4_ref,omitempty"`
	Timestamp int64   `json:"timestamp"`
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
		Description: "向会话追加一条 L6 操作轨迹事件（Seq 自动分配）。event_type 示例：turn_start/tool_call/tool_result/subagent_spawn/subagent_done/context_inject/llm_request/llm_output/turn_end。Payload 超过 4KB 会被截断。",
		InputSchema: objSchema(map[string]any{
			"session_id": strProp("会话 ID（16 位 hex），必填"),
			"event_type": strProp("事件类型，必填"),
			"payload":    strProp("事件内容（不超过 4KB）"),
			"l4_ref":     strProp("关联的 L4 档案 ID（16 位 hex，可选）"),
			"timestamp":  intProp("Unix 毫秒时间戳，必填"),
		}, "session_id", "event_type", "timestamp"),
	}, handle[trajectoryAppendArgs, updateResult](func(a trajectoryAppendArgs) (updateResult, error) {
		slot, err := toTrajectorySlot(a)
		if err != nil {
			return updateResult{}, err
		}
		return updateResult{OK: true}, db.AppendTrajectory(a.SessionID, slot)
	}))
}

// toTrajectorySlot parses the hex session/l4 refs of an append request
// into the api DTO (Seq is assigned by the store).
func toTrajectorySlot(a trajectoryAppendArgs) (memhop.TrajectorySlot, error) {
	sessionID, err := strconv.ParseUint(a.SessionID, 16, 64)
	if err != nil {
		return memhop.TrajectorySlot{}, err
	}
	var l4Ref *uint64
	if a.L4Ref != nil {
		v, err := strconv.ParseUint(*a.L4Ref, 16, 64)
		if err != nil {
			return memhop.TrajectorySlot{}, err
		}
		l4Ref = &v
	}
	return memhop.TrajectorySlot{
		SessionID: sessionID,
		EventType: a.EventType,
		Payload:   a.Payload,
		L4Ref:     l4Ref,
		Timestamp: a.Timestamp,
	}, nil
}

// registerTrajectoryReadTools installs read/stats/delete over one session.
func registerTrajectoryReadTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_trajectory_read",
		Description: "读取会话的 L6 操作轨迹（按 Seq 升序）。",
		InputSchema: objSchema(map[string]any{
			"session_id": strProp("会话 ID（16 位 hex），必填"),
		}, "session_id"),
	}, handle[sessionIDArgs, []memhop.TrajectorySlot](func(a sessionIDArgs) ([]memhop.TrajectorySlot, error) {
		return db.ReadTrajectory(a.SessionID)
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_trajectory_stats",
		Description: "返回会话 L6 轨迹统计：事件总数、各事件类型计数、最后事件时间戳，供宿主判断该会话是否值得结晶（工具调用数阈值）。",
		InputSchema: objSchema(map[string]any{
			"session_id": strProp("会话 ID（16 位 hex），必填"),
		}, "session_id"),
	}, handle[sessionIDArgs, memhop.TrajectoryStats](func(a sessionIDArgs) (memhop.TrajectoryStats, error) {
		stats, err := db.TrajectoryStats(a.SessionID)
		if err != nil {
			return memhop.TrajectoryStats{}, err
		}
		return *stats, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_trajectory_delete",
		Description: "删除会话的整条 L6 操作轨迹。",
		InputSchema: objSchema(map[string]any{
			"session_id": strProp("会话 ID（16 位 hex），必填"),
		}, "session_id"),
	}, handle[sessionIDArgs, updateResult](func(a sessionIDArgs) (updateResult, error) {
		return updateResult{OK: true}, db.DeleteTrajectory(a.SessionID)
	}))
}

func registerCrystallizeTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_crystallize",
		Description: "从会话轨迹提取可复用 L5 能力候选（L6 → L5）。调用 LLM，耗时长；候选保存为 draft，需宿主激活；重复结晶按名称和指纹去重。",
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
