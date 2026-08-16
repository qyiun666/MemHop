// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 knowledge hypergraph tools.

package main

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop"
)

type knowledgeImportArgs struct {
	Items []memhop.L3ImportItem `json:"items"`
	Mode  memhop.L3ImportMode   `json:"mode,omitempty"` // "Skip" / "Merge" / "Overwrite"（默认 Overwrite）
}

type knowledgeUpdateArgs struct {
	ID   string  `json:"id"`
	Name *string `json:"name,omitempty"`
}

type knowledgeDeleteArgs struct {
	ID string `json:"id"`
}

type knowledgeNodesArgs struct {
	GraphID  string   `json:"graph_id"`
	IDs      []string `json:"ids,omitempty"`
	Keyword  string   `json:"keyword,omitempty"`
	NodeType string   `json:"node_type,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

type knowledgeSubgraphArgs struct {
	GraphID     string   `json:"graph_id"`
	StartNodeID string   `json:"start_node_id"`
	MaxDepth    int      `json:"max_depth,omitempty"`
	EdgeKinds   []string `json:"edge_kinds,omitempty"` // related/causal/part_of/sequence/dependency/custom
}

func registerL3Tools(s *mcp.Server, db *memhop.DB) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_get",
		Description: "读取 L3 知识图谱（超图槽位 + 全部节点与边）。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("图谱 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[knowledgeDeleteArgs, memhop.L3Graph](func(a knowledgeDeleteArgs) (memhop.L3Graph, error) {
		g, err := db.GetL3(a.ID)
		if err != nil {
			return memhop.L3Graph{}, err
		}
		return *g, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_list",
		Description: "列出所有 L3 知识图谱槽位（不含节点/边，详情用 memhop_knowledge_get）。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[[]memhop.HypergraphSlot](func() ([]memhop.HypergraphSlot, error) {
		return db.ListL3()
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_import",
		Description: "批量导入 L3 知识节点：按 Domain 归组为图谱，已有节点按 mode 处理（Skip 跳过 / Merge 合并 / Overwrite 覆写，默认 Overwrite）。",
		InputSchema: objSchema(map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":     strProp("节点标题，必填"),
						"domain":    strProp("归属图谱域名（缺失则按空串归组）"),
						"node_type": strProp("节点类型，如 concept/fact"),
						"content":   strProp("节点内容"),
						"keywords":  arrProp("关键词", "string"),
					},
					"required": []string{"title"},
				},
			},
			"mode": strProp("重复处理模式：Skip / Merge / Overwrite（默认 Overwrite）"),
		}, "items"),
	}, handle[knowledgeImportArgs, memhop.L3ImportResult](func(a knowledgeImportArgs) (memhop.L3ImportResult, error) {
		res, err := db.ImportL3(a.Items, a.Mode)
		if err != nil {
			return memhop.L3ImportResult{}, err
		}
		return *res, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_update",
		Description: "更新 L3 图谱槽位元信息（当前仅支持改名 name）。",
		InputSchema: objSchema(map[string]any{
			"id":   strProp("图谱 ID（16 位 hex），必填"),
			"name": strProp("新名称"),
		}, "id"),
	}, handle[knowledgeUpdateArgs, memhop.L3Graph](func(a knowledgeUpdateArgs) (memhop.L3Graph, error) {
		g, err := db.UpdateL3(a.ID, a.Name)
		if err != nil {
			return memhop.L3Graph{}, err
		}
		return *g, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_delete",
		Description: "删除 L3 图谱（级联删除其全部节点与边）。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("图谱 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[knowledgeDeleteArgs, updateResult](func(a knowledgeDeleteArgs) (updateResult, error) {
		return updateResult{OK: true}, db.DeleteL3(a.ID)
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_nodes",
		Description: "查询 L3 节点：GraphID 必填，ids/keyword/node_type 三选一（ids 优先）。",
		InputSchema: objSchema(map[string]any{
			"graph_id":  strProp("图谱 ID（16 位 hex），必填"),
			"ids":       arrProp("按节点 ID 精确查询", "string"),
			"keyword":   strProp("标题/内容/关键词子串匹配（忽略大小写）"),
			"node_type": strProp("按节点类型过滤"),
			"limit":     intProp("返回条数上限（<=0 不限）"),
		}, "graph_id"),
	}, handle[knowledgeNodesArgs, []memhop.HypergraphNode](func(a knowledgeNodesArgs) ([]memhop.HypergraphNode, error) {
		return db.QueryL3Nodes(memhop.L3NodeQuery{
			GraphID:  a.GraphID,
			IDs:      a.IDs,
			Keyword:  a.Keyword,
			NodeType: a.NodeType,
			Limit:    a.Limit,
		})
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_subgraph",
		Description: "从起始节点做 BFS 抽取子图（max_depth 跳数，edge_kinds 限定可达边的类型）。",
		InputSchema: objSchema(map[string]any{
			"graph_id":      strProp("图谱 ID（16 位 hex），必填"),
			"start_node_id": strProp("起始节点 ID（16 位 hex），必填"),
			"max_depth":     intProp("BFS 深度（<=0 视为 1）"),
			"edge_kinds":    arrProp("限定边类型：related/causal/part_of/sequence/dependency/custom", "string"),
		}, "graph_id", "start_node_id"),
	}, handle[knowledgeSubgraphArgs, memhop.L3Subgraph](func(a knowledgeSubgraphArgs) (memhop.L3Subgraph, error) {
		var kinds []memhop.GraphEdgeKind
		for _, k := range a.EdgeKinds {
			kind, err := parseEdgeKind(k)
			if err != nil {
				return memhop.L3Subgraph{}, err
			}
			kinds = append(kinds, kind)
		}
		sg, err := db.QueryL3Subgraph(a.GraphID, a.StartNodeID, a.MaxDepth, kinds)
		if err != nil {
			return memhop.L3Subgraph{}, err
		}
		return *sg, nil
	}))
}

// parseEdgeKind maps an edge-kind string to its enum value.
func parseEdgeKind(s string) (memhop.GraphEdgeKind, error) {
	switch s {
	case "related":
		return memhop.EdgeRelated, nil
	case "causal":
		return memhop.EdgeCausal, nil
	case "part_of":
		return memhop.EdgePartOf, nil
	case "sequence":
		return memhop.EdgeSequence, nil
	case "dependency":
		return memhop.EdgeDependency, nil
	case "custom":
		return memhop.EdgeCustom, nil
	}
	return 0, invalidEdgeKindError(s)
}

// invalidEdgeKindError reports an unsupported edge-kind string.
func invalidEdgeKindError(s string) error {
	return fmt.Errorf("invalid edge_kind %q: must be one of related/causal/part_of/sequence/dependency/custom", s)
}
