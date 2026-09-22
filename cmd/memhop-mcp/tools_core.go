// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Core tools: search / update / dream / checkpoint / status / compact — the
// memory loop entry points of the api package, plus the file-level maintenance
// pair.

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
	SceneID string `json:"scene_id"`
	TopicID string `json:"topic_id"`
}

type dreamArgs struct {
	SceneID string `json:"scene_id"`
}

type dreamResult struct {
	Consolidated bool                `json:"consolidated"`
	Report       *memhop.DreamReport `json:"report,omitempty"`
}

type statusResult struct {
	Closed      bool  `json:"closed"`
	SceneCount  int   `json:"scene_count"`
	FileBytes   int64 `json:"file_bytes"`
	RecordCount int64 `json:"record_count"`
}

// registerCoreTools installs the memory-loop entry points; one register
// function per tool keeps each declaration list short.
func registerCoreTools(s *mcp.Server, m *memhop.DB, db *memhop.Session, reg *tenantRegistry) {
	registerSearchTool(s, db)
	registerUpdateTool(s, db)
	registerDreamTool(s, db)
	registerMaintenanceTools(s, m, db, reg)
}

func registerSearchTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_search",
		Description: "读取一个场景（= 宿主会话）的记忆并开启本轮：返回 L0 画像、该场景 depth-1 话题集（每个话题带提炼关键词、两个时间界和宿主给它起的名字，即宿主本轮的上下文；话题记录上不挂原文 id，说了什么按话题 id 用 memhop_archive_search 取回），以及 new_topic_id —— 本次读取为即将进行的这一轮铸出的话题 ID，memhop_update、事件追加与计划写入都按它落笔。scene_id 为空时新建场景并返回其 id（16 位 hex，名字由库生成）；非空时必须已存在。l3_id 只在新建场景时生效：scene_id 非空又填了 l3_id 直接拒（ErrInvalidQuery），不是悄悄忽略——已存在场景的锚不在本工具改，MCP 工具面也没有改锚的口。",
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
		Description: "为本轮收口（settle）：把该轮话题已存下的内容（由 memhop_archive_append 逐条写入）用一次提炼写成该话题的关键词轨，并返回收口后的话题记录——fused_keywords 就是这一轮被蒸出的关键词，宿主不必再读一次。本工具不写内容；topic_id 用 memhop_search 返回的 new_topic_id（一轮一个话题），scene_id 必须是已存在场景。同一 topic_id 重复收口只是重新提炼它当前的内容，所以超时后可安全重试。该轮当前没有可提炼的对话原文时直接拒绝（ErrInvalidQuery）且不发起任何 LLM 调用——先确认 append 真的落盘（拿的是同一个 topic_id），原文超出保留窗（默认 7 天，宿主可配）只是这一条的其中一种成因。",
		InputSchema: objSchema(map[string]any{
			"scene_id": strProp("场景 ID（16 位 hex），必填，须已存在"),
			"topic_id": strProp("本轮话题 ID（16 位 hex），必填，取自 memhop_search 的 new_topic_id"),
		}, "scene_id", "topic_id"),
	}, handle[updateArgs, memhop.TopicSlot](func(a updateArgs) (memhop.TopicSlot, error) {
		topic, err := db.Settle(a.SceneID, a.TopicID)
		if err != nil {
			return memhop.TopicSlot{}, err
		}
		return *topic, nil
	}))
}

func registerDreamTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_dream",
		Description: "执行梦境巩固（睡眠模拟）：L2 话题压缩融合、L1 节点同步、衰减与 L0 画像蒸馏，并清理超出保留窗口的 L4 内容与 L5 计划节点。scene_id 留空即巩固本域的每一个场景——库只在 Dream 里做这些修剪与重建，一个不再写入的域也要至少一次 Dream 才会收缩。耗时较长；返回结构化报告 report（各阶段名称/状态/耗时、L2 压缩计数、L1 增删计数、L0 是否蒸馏）与 consolidated 一个布尔——它说的只是「这一趟真的做过 L2 融合压缩」，保留窗清理、L1 重建与 L0 蒸馏都不算它，所以要看这次到底做了什么就读 report。某个阶段失败时报告也照样带回（错误文本在前，报告 JSON 在后），它是「已经做到哪一步」的唯一凭据——保留窗清理可能已经生效，只是压缩没做成。",
		InputSchema: objSchema(map[string]any{
			"scene_id": strProp("场景 ID（16 位 hex），可选；留空即全域巩固"),
		}),
	}, handlePartial[dreamArgs, dreamResult](func(ctx context.Context, a dreamArgs) (dreamResult, error) {
		rep, err := db.Dream(ctx, a.SceneID)
		out := dreamResult{}
		if rep != nil {
			out.Consolidated = rep.ConsolidatedScenes > 0
			out.Report = rep
		}
		return out, err
	}))
}

// registerMaintenanceTools installs the lifecycle and diagnostics tools: the
// two no-argument lifecycle calls and the file-level compaction.
func registerMaintenanceTools(s *mcp.Server, m *memhop.DB, db *memhop.Session, reg *tenantRegistry) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_checkpoint",
		Description: "将当前状态持久化到磁盘（记录索引 + A/B header），不关闭数据库。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[updateResult](func() (updateResult, error) {
		return updateResult{OK: true}, m.Checkpoint()
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_status",
		Description: "数据库健康状态：closed 是整个共享文件的状态（多租户共用一份库），scene_count 只数本租户域里已登记的场景（= 本租户的宿主会话数）；file_bytes 是共享文件当前大小，record_count 是全文件可达记录数——两者是判断 memhop_compact 是否该跑的依据，被删记录占着的字节算进 file_bytes 而不算进 record_count，这个差额就是压缩能换回来的空间。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[statusResult](func() (statusResult, error) {
		scenes, err := db.ListScenes("")
		if err != nil {
			return statusResult{}, err
		}
		stats, err := m.Stats()
		if err != nil {
			return statusResult{}, err
		}
		return statusResult{Closed: m.IsClosed(), SceneCount: len(scenes),
			FileBytes: stats.FileBytes, RecordCount: stats.RecordCount}, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_compact",
		Description: "把共享文件原地压缩重写：删除是墓碑、覆写留旧帧，删掉的内容在压缩前不还字节。流程是先关库静默（此后进行中的调用以 [5002] 失败、需重试）→ 在 db-dir 内的固定临时名写出全量净本 → 原子换名覆盖原文件 → 重开；已确认的写入一条不丢。每个租户条目随之失效，下一个请求自动重建。耗时与文件大小成正比且期间整库不可写——请在租户安静时调用。返回压缩前后的文件字节数。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[compactResult](func() (compactResult, error) {
		return reg.compact()
	}))
}
