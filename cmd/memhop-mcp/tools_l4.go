// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 archive tools.

package main

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop"
)

type archiveSearchArgs struct {
	Keyword string   `json:"keyword,omitempty"` // mode 1: content substring
	Start   int64    `json:"start,omitempty"`   // mode 2: time range [start, end] (ms), both required
	End     int64    `json:"end,omitempty"`
	IDs     []string `json:"ids,omitempty"`      // mode 3: by archive id
	TopicID *string  `json:"topic_id,omitempty"` // extra: only archives of this topic
}

type archiveGetArgs struct {
	ID string `json:"id"`
}

func registerL4Tools(s *mcp.Server, db *memhop.DB) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_archive_search",
		Description: "检索 L4 对话档案。三种模式互斥（keyword 子串 > 时间区间 [start,end] > ids 精确），topic_id 在所有模式下作为附加过滤。",
		InputSchema: objSchema(map[string]any{
			"keyword":  strProp("模式 1：内容子串匹配"),
			"start":    intProp("模式 2：时间区间起点（Unix ms），需与 end 同时提供"),
			"end":      intProp("模式 2：时间区间终点（Unix ms）"),
			"ids":      arrProp("模式 3：按档案 ID 精确查询", "string"),
			"topic_id": strProp("附加过滤：仅返回该 topic 的档案（16 位 hex）"),
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
		Description: "按 ID 读取单条 L4 对话档案。",
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
