// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 tools: the content of a topic — search, retrieval by id, and the one write
// path that puts dialogue or an operation event into a turn.

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
	Kind    *string  `json:"kind,omitempty"`
	NodeSeq uint32   `json:"node_seq,omitempty"`
	Type    *string  `json:"content_type,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}

// archiveAppendArgs is one content record as a host hands it over the wire: kind
// says which track it belongs to, role says who spoke (utterances only), and seq
// left at 0 lets the library pick the slot.
type archiveAppendArgs struct {
	TopicID     string `json:"topic_id"`
	Content     string `json:"content"`
	Timestamp   int64  `json:"timestamp"`
	Kind        string `json:"kind,omitempty"`
	Role        string `json:"role,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	EventType   string `json:"event_type,omitempty"`
	NodeSeq     uint32 `json:"node_seq,omitempty"`
	Seq         uint64 `json:"seq,omitempty"`
}

// contentTypeNames maps human-readable content type names to ContentType
// constants; unknown names produce an error before touching the DB.
var contentTypeNames = map[string]memhop.ContentType{
	"text": memhop.ContentText, "image": memhop.ContentImage,
	"video": memhop.ContentVideo, "document": memhop.ContentDocument,
	"audio": memhop.ContentAudio, "code": memhop.ContentCode,
	"other": memhop.ContentOther,
}

// kindNames and roleNames are the wire vocabulary for the two axes an appended
// record declares. The library's own consolidation role is absent from roleNames
// on purpose: a host that could name it could forge a fused summary.
var (
	kindNames = map[string]memhop.ArchiveKind{
		"utterance": memhop.KindUtterance, "event": memhop.KindEvent,
	}
	roleNames = map[string]uint8{
		"user": memhop.RoleUser, "agent": memhop.RoleAgent, "system": memhop.RoleSystem,
	}
)

type archiveGetArgs struct {
	ID string `json:"id"`
}

func registerL4Tools(s *mcp.Server, db *memhop.Session) {
	// archiveSearchDefaultLimit caps a call that names no limit: an unbounded
	// archive read returns the domain's whole original set, and here that set
	// lands directly in an LLM's context.
	const archiveSearchDefaultLimit = 50

	description := fmt.Sprintf("检索 L4 内容（一轮的对话原文与其操作事件同住此层）：keyword（子串，忽略大小写）/ 时间范围 [start,end]（毫秒）/ ID 列表 / topic_id / kind / node_seq / content_type 都是过滤条件，填了的全部按 AND 组合；一个都不填即全域扫描。node_seq 是轮次内的步骤序号（由 Go 侧计划写面发号），取该步及其全部子步归因的记录，必须与 topic_id 同填。结果按 Seq 升序，limit 只保留最新的 N 条（缺省 %d，可填更大值）。text/document/code 存原文，image/audio/video 等媒体类型的 content 为路径。", archiveSearchDefaultLimit)

	s.AddTool(&mcp.Tool{
		Name:        "memhop_archive_search",
		Description: description,
		InputSchema: objSchema(map[string]any{
			"keyword":      strProp("内容关键词（子串匹配，忽略大小写）"),
			"start":        intProp("时间范围起点（毫秒）"),
			"end":          intProp("时间范围终点（毫秒）"),
			"ids":          arrProp("档案 ID 列表", "string"),
			"topic_id":     strProp("限定话题 ID（16 位 hex）"),
			"kind":         strProp("内容种类过滤：utterance（对话原文）| event（操作事件）；不填即两种都要"),
			"node_seq":     intProp("只取该计划步骤及其全部子步归因的记录（轮次内步骤序号，如 1、2）；须与 topic_id 同填，不填即不加这条约束"),
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
		var kind *memhop.ArchiveKind
		if a.Kind != nil {
			v, err := resolveArchiveKind(*a.Kind)
			if err != nil {
				return nil, err
			}
			kind = &v
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
			Kind:    kind,
			NodeSeq: a.NodeSeq,
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

	registerArchiveAppendTool(s, db)
}

// registerArchiveAppendTool installs the one content write: without it an MCP host
// could read a turn's content but never produce it, since Update no longer carries
// texts.
func registerArchiveAppendTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_archive_append",
		Description: "向本轮写一条 L4 内容（每轮一个键：本轮 memhop_search 铸出的话题 ID）。kind=utterance 写谁说了什么（role 必填：user/agent/system），kind=event 写发生了什么（event_type 必填，由宿主自定；惯例：llm_request/llm_output/tool_call/tool_result/subagent_spawn/subagent_done/context_inject/ask_user/user_reply）。seq 不填即库分配：事件恒从 3 起，槽 1/2 留给对话；显式写一个已被占用的 seq 是覆写而非报错（重放因此收敛）。内容超预算直接拒写、不截断（事件 4KB、原文 64KB）。带 node_seq 的事件必须绑到本轮计划里已创建的步骤——步骤由 Go 侧 PlanCreate/PlanNodeAdd 创建（本工具面不暴露计划写面），所以纯 MCP 宿主用不了 node_seq。一切校验先于写入，被拒不留下任何记录与节点。",
		InputSchema: objSchema(map[string]any{
			"topic_id":     strProp("本轮话题 ID（16 位 hex），必填"),
			"kind":         strProp("utterance（对话原文，缺省）| event（操作事件）"),
			"role":         strProp("说话者：user | agent | system（kind=utterance 必填）"),
			"content":      strProp("内容本体；媒体类型的 content 存路径"),
			"content_type": strProp("text（缺省）| image | video | document | audio | code | other"),
			"event_type":   strProp("事件名（kind=event 必填，任意非空宿主命名）"),
			"node_seq":     intProp("事件绑到哪一步（仅 kind=event，轮次内步骤序号）；该步必须已由 Go 侧 PlanCreate/PlanNodeAdd 创建，本工具面不建节点"),
			"seq":          intProp("写入的槽位；0/不填 = 库自动分配（自动分配跳过 1/2）"),
			"timestamp":    intProp("Unix 毫秒时间戳，必填"),
		}, "topic_id", "content", "timestamp"),
	}, handle[archiveAppendArgs, updateResult](func(a archiveAppendArgs) (updateResult, error) {
		slot, err := toAppendSlot(a)
		if err != nil {
			return updateResult{}, err
		}
		return updateResult{OK: true}, db.AppendArchive(a.TopicID, slot)
	}))
}

// toAppendSlot turns the wire arguments into the content DTO, resolving the three
// name-valued axes before the DB is touched so an unknown name is a bad argument
// rather than a silent default.
func toAppendSlot(a archiveAppendArgs) (memhop.ArchiveSlot, error) {
	kind, err := resolveArchiveKind(a.Kind)
	if err != nil {
		return memhop.ArchiveSlot{}, err
	}
	ct, err := resolveContentType(a.ContentType)
	if err != nil {
		return memhop.ArchiveSlot{}, err
	}
	slot := memhop.ArchiveSlot{
		Kind: kind, Seq: a.Seq, ContentType: ct,
		EventType: a.EventType, NodeSeq: a.NodeSeq,
		CreatedAt: a.Timestamp, Content: a.Content,
	}
	if kind != memhop.KindUtterance {
		return slot, nil
	}
	role, err := resolveRole(a.Role)
	if err != nil {
		return memhop.ArchiveSlot{}, err
	}
	slot.Role = role
	return slot, nil
}
