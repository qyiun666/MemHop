// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L6 tools: the trajectory enumeration a stateless host needs to find work, plus
// crystallize. A turn's operation events are L4 content of kind event — written by
// memhop_archive_append and read by memhop_trajectory_read — so what is left in the
// L6 layer itself is the plan tree, whose write and read faces stay on the Go side.

package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

type sessionIDArgs struct {
	SessionID string `json:"session_id"`
}

// registerL6Tools installs the trajectory read surface and crystallize; each
// register function owns one cohesive tool group.
func registerL6Tools(s *mcp.Server, db *memhop.Session) {
	registerTrajectoryReadTools(s, db)
	registerCrystallizeTool(s, db)
}

// registerTrajectoryReadTools installs the domain-wide event footprint plus the
// per-turn event read. Retention is automatic — Dream drops content and plan nodes
// older than 7 days — so there are no delete tools.
func registerTrajectoryReadTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_trajectory_sessions",
		Description: "列出本租户下记了操作事件的轮次（每轮一条：16 位 hex 轮 ID、事件数、最后追加时间），用于发现可结晶的轮次；只记了对话的轮次不在列，超过 7 天的事件由 Dream 自动清理。",
		InputSchema: objSchema(nil),
	}, handleNoArgs(func() ([]memhop.TrajectorySessionSummary, error) {
		return db.ListTrajectorySessions()
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_trajectory_read",
		Description: "读取本轮的全部操作事件（按 Seq 升序）。本轮的计划节点不在这一读里——它们住在 L6，Go 侧用 PlanState 取。",
		InputSchema: objSchema(map[string]any{
			"session_id": strProp("轮轨迹 ID（16 位 hex），必填"),
		}, "session_id"),
	}, handle[sessionIDArgs, []memhop.ArchiveSlot](func(a sessionIDArgs) ([]memhop.ArchiveSlot, error) {
		kind := memhop.KindEvent
		return db.SearchL4(memhop.L4Query{TopicID: &a.SessionID, Kind: &kind})
	}))
}

func registerCrystallizeTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_crystallize",
		Description: "从轮轨迹提取可复用能力候选（LLM 提炼，耗时长）：返回候选列表（action=create|reuse|merge + reuse_id=已有卡名 + 完整卡载荷）。引擎不落盘——校验、去重与写盘（如写 draft 文档）全部由宿主完成。",
		InputSchema: objSchema(map[string]any{
			"session_id": strProp("会话 ID（16 位 hex），必填"),
		}, "session_id"),
	}, handle[sessionIDArgs, memhop.CrystallizeOutput](func(a sessionIDArgs) (memhop.CrystallizeOutput, error) {
		out, err := db.Crystallize(context.Background(), a.SessionID, nil)
		if err != nil {
			return memhop.CrystallizeOutput{}, err
		}
		return *out, nil
	}))
}
