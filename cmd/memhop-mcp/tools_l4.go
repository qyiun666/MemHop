// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 tools: archive (dialogue original) search and retrieval.

package main

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

type archiveSearchArgs struct {
	Keyword string   `json:"keyword,omitempty"`
	Start   int64    `json:"start,omitempty"`
	End     int64    `json:"end,omitempty"`
	IDs     []string `json:"ids,omitempty"`
	TopicID *string  `json:"topic_id,omitempty"`
}

type archiveGetArgs struct {
	ID string `json:"id"`
}

func registerL4Tools(s *mcp.Server, db *memhop.AgentSession) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_archive_search",
		Description: "检索 L4 对话原文档案：按内容关键词 / 时间范围 [start,end]（毫秒）/ ID 列表三种模式之一，可附加 topic_id 限定话题。",
		InputSchema: objSchema(map[string]any{
			"keyword":  strProp("内容关键词（模式 1：子串匹配）"),
			"start":    intProp("时间范围起点（毫秒，模式 2）"),
			"end":      intProp("时间范围终点（毫秒，模式 2）"),
			"ids":      arrProp("档案 ID 列表（模式 3）", "string"),
			"topic_id": strProp("限定话题 ID（16 位 hex，可选附加条件）"),
		}),
	}, handle[archiveSearchArgs, []memhop.ArchiveSlot](func(a archiveSearchArgs) ([]memhop.ArchiveSlot, error) {
		return db.SearchL4(memhop.L4Query{
			Keyword: a.Keyword,
			Start:   a.Start,
			End:     a.End,
			IDs:     a.IDs,
			TopicID: a.TopicID,
		})
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_archive_get",
		Description: "按 ID 读取一条 L4 对话原文档案。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("档案 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[archiveGetArgs, memhop.ArchiveSlot](func(a archiveGetArgs) (memhop.ArchiveSlot, error) {
		slot, err := db.GetArchive(a.ID)
		if err != nil {
			return memhop.ArchiveSlot{}, err
		}
		return *slot, nil
	}))
}
