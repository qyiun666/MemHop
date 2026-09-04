// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 tools: archive (dialogue original) search and retrieval.

package main

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

type archiveSearchArgs struct {
	Keyword string   `json:"keyword,omitempty"`
	Start   int64    `json:"start,omitempty"`
	End     int64    `json:"end,omitempty"`
	IDs     []string `json:"ids,omitempty"`
	TopicID *string  `json:"topic_id,omitempty"`
	Type    *string  `json:"content_type,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}

// contentTypeNames maps human-readable content type names to ContentType
// constants; unknown names produce an error before touching the DB.
var contentTypeNames = map[string]memhop.ContentType{
	"text": memhop.ContentText, "image": memhop.ContentImage,
	"video": memhop.ContentVideo, "document": memhop.ContentDocument,
	"audio": memhop.ContentAudio, "code": memhop.ContentCode,
	"other": memhop.ContentOther,
}

type archiveGetArgs struct {
	ID string `json:"id"`
}

func registerL4Tools(s *mcp.Server, db *memhop.Session) {
	// archiveSearchDefaultLimit caps a call that names no limit: an unbounded
	// archive read returns the domain's whole original set, and here that set
	// lands directly in an LLM's context.
	const archiveSearchDefaultLimit = 50

	description := fmt.Sprintf("检索 L4 对话原文档案：keyword（子串，忽略大小写）/ 时间范围 [start,end]（毫秒）/ ID 列表 / topic_id / content_type 都是过滤条件，填了的全部按 AND 组合；一个都不填即全域扫描。结果按 CreatedAt 升序，limit 只保留最新的 N 条（缺省 %d，可填更大值）。text/document/code 存原文，image/audio/video 等媒体类型的 content 为路径。", archiveSearchDefaultLimit)

	s.AddTool(&mcp.Tool{
		Name:        "memhop_archive_search",
		Description: description,
		InputSchema: objSchema(map[string]any{
			"keyword":      strProp("内容关键词（子串匹配，忽略大小写）"),
			"start":        intProp("时间范围起点（毫秒）"),
			"end":          intProp("时间范围终点（毫秒）"),
			"ids":          arrProp("档案 ID 列表", "string"),
			"topic_id":     strProp("限定话题 ID（16 位 hex）"),
			"content_type": strProp("内容类型过滤：text | image | video | document | audio | code | other"),
			"limit":        intProp("只返回最新 N 条（缺省 50；<=0 也按缺省处理）"),
		}),
	}, handle[archiveSearchArgs, []memhop.ArchiveSlot](func(a archiveSearchArgs) ([]memhop.ArchiveSlot, error) {
		var ct *memhop.ContentType
		if a.Type != nil {
			v, err := resolveContentType(*a.Type)
			if err != nil {
				return nil, err
			}
			ct = &v
		}
		limit := a.Limit
		if limit <= 0 {
			limit = archiveSearchDefaultLimit
		}
		return db.SearchL4(memhop.L4Query{
			Keyword: a.Keyword,
			Start:   a.Start,
			End:     a.End,
			IDs:     a.IDs,
			TopicID: a.TopicID,
			Type:    ct,
			Limit:   limit,
		})
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_archive_get",
		Description: "按 ID 读取一条 L4 对话原文档案。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("档案 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[archiveGetArgs, memhop.ArchiveSlot](func(a archiveGetArgs) (memhop.ArchiveSlot, error) {
		slots, err := db.SearchL4(memhop.L4Query{IDs: []string{a.ID}})
		if err != nil {
			return memhop.ArchiveSlot{}, err
		}
		if len(slots) == 0 {
			return memhop.ArchiveSlot{}, fmt.Errorf("archive %s not found", a.ID)
		}
		return slots[0], nil
	}))
}
