// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 tools: the read face of the scene hypergraph. There is no write tool —
// Dream builds the nodes and the edges between them and is the only writer.

package main

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

func registerL1Tools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_l1_nodes",
		Description: "列出本租户全部 L1 场景节点（顺序稳定，id 为 16 位 hex）。节点与它们之间的共现边都由 Dream 建立并衰减，本工具只读、没有对应的写工具。importance/valence/arousal 是巩固算出来的值；edge_ids 本身没有读取口——两个节点共享同一个 edge id 就意味着 Dream 判定这两个会话相关，这是本层结构信息唯一的出口。",
		InputSchema: objSchema(nil),
	}, handleNoArgs(func() ([]memhop.SceneNodeView, error) {
		return db.ListL1()
	}))
}
