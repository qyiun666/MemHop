// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 超图节点操作：创建与按图查询。
package l3

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/timeutil"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// CreateNode 创建超图节点，ID = hash(graphID:title)，返回节点 ID。
func CreateNode(engine *storage.StorageEngine, graphID, title, nodeType, content string, keywords []string) (uint64, error) {
	graphHash, err := parseGraphID(graphID)
	if err != nil {
		return 0, err
	}
	nodeID := hash.HashID(fmt.Sprintf("%s:%s", graphID, title))
	now := timeutil.NowMs()
	node := &model.HypergraphNode{
		IDHash:    nodeID,
		GraphID:   graphHash,
		Title:     title,
		NodeType:  nodeType,
		Content:   content,
		Keywords:  keywords,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := record.WriteHypergraphNode(engine, nodeID, node); err != nil {
		return 0, err
	}
	return nodeID, nil
}

// ListNode 按图查询节点。
func ListNode(engine *storage.StorageEngine, graphID string) []model.HypergraphNode {
	graphHash, err := hash.ParseID(graphID)
	if err != nil {
		return nil
	}
	var out []model.HypergraphNode
	for _, node := range record.CollectAllHypergraphNodes(engine) {
		if node.GraphID == graphHash {
			out = append(out, node)
		}
	}
	return out
}
