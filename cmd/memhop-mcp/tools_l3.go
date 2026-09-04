// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 tools: knowledge hypergraph operations (get/list/import/update/delete
// plus node query and subgraph BFS).

package main

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

type knowledgeImportArgs struct {
	Items []knowledgeImportItem `json:"items"`
	Mode  string                `json:"mode"` // Skip | Merge | Overwrite
}

type knowledgeImportItem struct {
	Title     string             `json:"title"`
	Domain    string             `json:"domain"`
	NodeType  string             `json:"node_type"`
	Content   string             `json:"content"`
	Keywords  []string           `json:"keywords"`
	SourceRef string             `json:"source_ref,omitempty"`
	Related   []knowledgeRelated `json:"related,omitempty"`
}

// knowledgeRelated is one import-time hyperedge: the far side of the relation
// (one title = a binary edge, several = one N-ary hyperedge over the item plus
// all of them) and an optional edge kind name (empty = related).
type knowledgeRelated struct {
	Titles []string `json:"titles"`
	Kind   string   `json:"kind,omitempty"`
}

type knowledgeUpdateArgs struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

type knowledgeIDArgs struct {
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
	EdgeKinds   []string `json:"edge_kinds,omitempty"`
}

// edgeKindNames maps human-readable edge kind names to GraphEdgeKind
// constants; unknown names produce an error before touching the DB.
var edgeKindNames = map[string]memhop.GraphEdgeKind{
	"related":    memhop.EdgeRelated,
	"causal":     memhop.EdgeCausal,
	"part_of":    memhop.EdgePartOf,
	"sequence":   memhop.EdgeSequence,
	"dependency": memhop.EdgeDependency,
	"custom":     memhop.EdgeCustom,
}

// parseImportMode converts the JSON string mode to L3ImportMode.
func parseImportMode(s string) (memhop.L3ImportMode, error) {
	switch s {
	case "Skip":
		return memhop.L3ImportSkip, nil
	case "Merge":
		return memhop.L3ImportMerge, nil
	case "Overwrite":
		return memhop.L3ImportOverwrite, nil
	}
	return "", fmt.Errorf("invalid import mode %q (want Skip, Merge or Overwrite)", s)
}

// parseEdgeKinds converts JSON string edge kinds to GraphEdgeKind values.
func parseEdgeKinds(kinds []string) ([]memhop.GraphEdgeKind, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	out := make([]memhop.GraphEdgeKind, 0, len(kinds))
	for _, k := range kinds {
		v, ok := edgeKindNames[k]
		if !ok {
			return nil, fmt.Errorf("invalid edge kind %q (want related, causal, part_of, sequence, dependency or custom)", k)
		}
		out = append(out, v)
	}
	return out, nil
}

// registerL3Tools installs the hypergraph tools; each register function
// owns one cohesive tool group.
func registerL3Tools(s *mcp.Server, db *memhop.Session) {
	registerKnowledgeReadTools(s, db)
	registerKnowledgeImportTool(s, db)
	registerKnowledgeWriteTools(s, db)
	registerKnowledgeQueryTools(s, db)
}

func registerKnowledgeReadTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_get",
		Description: "读取一个 L3 知识超图（图元信息 + 全部节点与边）。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("知识图 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[knowledgeIDArgs, memhop.L3Graph](func(a knowledgeIDArgs) (memhop.L3Graph, error) {
		g, err := db.GetL3(a.ID)
		if err != nil {
			return memhop.L3Graph{}, err
		}
		return *g, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_list",
		Description: "列出所有 L3 知识超图（仅图元信息，不含节点）。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[[]memhop.HypergraphSlot](func() ([]memhop.HypergraphSlot, error) {
		return db.ListL3()
	}))
}

func registerKnowledgeImportTool(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_import",
		Description: "批量导入 L3 知识条目（按标题+领域匹配既有图）：mode=Skip 跳过已存在、Merge 合并节点、Overwrite 覆盖。source_ref 存位置引用（如 file:line）；related 在同图内按标题建边（目标可在同批后文），同一对节点可并存不同 kind 的关系，重导入同批不会重复建边。返回 graph_ids（本批写入的图，可直接用于把场景挂到图上）+ created_ids/updated_ids（**节点** ID，不是图 ID）+ edges_created/skipped_count；逐条失败进 errors，批次不中断。",
		InputSchema: objSchema(map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": objSchema(map[string]any{
					"title":      strProp("条目标题，必填"),
					"domain":     strProp("所属领域，必填"),
					"node_type":  strProp("节点类型"),
					"content":    strProp("条目内容，必填"),
					"keywords":   arrProp("关键词列表", "string"),
					"source_ref": strProp("位置引用（file:line / URL，可选）"),
					"related": map[string]any{
						"type": "array",
						"items": objSchema(map[string]any{
							"titles": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "这条关系的另一侧节点标题，至少一个（同图内，可在同批后文）；给多个就是一条 N 元超边",
							},
							"kind": strProp("边类型：related | causal | part_of | sequence | dependency | custom，缺省 related"),
						}, "titles"),
						"description": "同图关系边列表（可选）。一条 related 项 = 一条超边，成员是本条目 + titles 全部目标；给两个以上目标就是 N 元事实（「这些属于同一组」），不拆成两两边。",
					},
				}, "title", "domain", "content"),
				"description": "知识条目列表，必填",
			},
			"mode": strProp("导入模式：Skip | Merge | Overwrite，必填"),
		}, "items", "mode"),
	}, handle[knowledgeImportArgs, memhop.L3ImportResult](func(a knowledgeImportArgs) (memhop.L3ImportResult, error) {
		mode, err := parseImportMode(a.Mode)
		if err != nil {
			return memhop.L3ImportResult{}, err
		}
		items, err := toImportItems(a.Items)
		if err != nil {
			return memhop.L3ImportResult{}, err
		}
		res, err := db.ImportL3(items, mode)
		if err != nil {
			return memhop.L3ImportResult{}, err
		}
		return *res, nil
	}))
}

// toImportItems maps JSON import items to the api DTO; relation kinds are
// validated before any DB call.
func toImportItems(in []knowledgeImportItem) ([]memhop.L3ImportItem, error) {
	items := make([]memhop.L3ImportItem, 0, len(in))
	for _, it := range in {
		item := memhop.L3ImportItem{
			Title:     it.Title,
			Domain:    it.Domain,
			NodeType:  it.NodeType,
			Content:   it.Content,
			Keywords:  it.Keywords,
			SourceRef: it.SourceRef,
		}
		if len(it.Related) > 0 {
			item.Related = make([]memhop.L3Relation, 0, len(it.Related))
			for _, r := range it.Related {
				kind := memhop.EdgeRelated
				if r.Kind != "" {
					v, ok := edgeKindNames[r.Kind]
					if !ok {
						return nil, fmt.Errorf("invalid relation kind %q in item %q (want related, causal, part_of, sequence, dependency or custom)", r.Kind, it.Title)
					}
					kind = v
				}
				item.Related = append(item.Related, memhop.L3Relation{Titles: r.Titles, Kind: kind})
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func registerKnowledgeWriteTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_update",
		Description: "重命名一个 L3 知识超图（name 缺省则不修改）。新名称不能被其他图占用——domain 标签就是 ImportL3 寻图的方式，撞名会报 ErrInvalidQuery 而不是让该 domain 每次解析到不同的图。改用自己当前的名字是成功的空操作。",
		InputSchema: objSchema(map[string]any{
			"id":   strProp("知识图 ID（16 位 hex），必填"),
			"name": strProp("新名称（可选）"),
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
		Description: "删除一个 L3 知识超图及其全部节点与边，并清掉命名该图的 L2 场景锚点（删完不会有场景还挂在已消失的项目域下）。只想改单个节点用 memhop_knowledge_nodes + 删除节点，别用整图删除。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("知识图 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[knowledgeIDArgs, updateResult](func(a knowledgeIDArgs) (updateResult, error) {
		return updateResult{OK: true}, db.DeleteL3(a.ID)
	}))
}

func registerKnowledgeQueryTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_knowledge_nodes",
		Description: "按条件查询 L3 节点：ID 列表、关键词（模糊匹配标题）、节点类型；limit<=0 表示不限制。",
		InputSchema: objSchema(map[string]any{
			"graph_id":  strProp("知识图 ID（16 位 hex），必填"),
			"ids":       arrProp("节点 ID 列表（16 位 hex，可选）", "string"),
			"keyword":   strProp("关键词（模糊匹配标题，可选）"),
			"node_type": strProp("节点类型过滤（可选）"),
			"limit":     intProp("返回上限（<=0 不限制）"),
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
		Description: "从起始节点 BFS 遍历 L3 子图（max_depth<=0 表示 1 层）；edge_kinds 限制可达边类型（related/causal/part_of/sequence/dependency/custom）。",
		InputSchema: objSchema(map[string]any{
			"graph_id":      strProp("知识图 ID（16 位 hex），必填"),
			"start_node_id": strProp("起始节点 ID（16 位 hex），必填"),
			"max_depth":     intProp("BFS 深度（<=0 表示 1 层）"),
			"edge_kinds":    arrProp("边类型过滤（可选）", "string"),
		}, "graph_id", "start_node_id"),
	}, handle[knowledgeSubgraphArgs, memhop.L3Subgraph](func(a knowledgeSubgraphArgs) (memhop.L3Subgraph, error) {
		kinds, err := parseEdgeKinds(a.EdgeKinds)
		if err != nil {
			return memhop.L3Subgraph{}, err
		}
		sub, err := db.QueryL3Subgraph(a.GraphID, a.StartNodeID, a.MaxDepth, kinds)
		if err != nil {
			return memhop.L3Subgraph{}, err
		}
		return *sub, nil
	}))
}
