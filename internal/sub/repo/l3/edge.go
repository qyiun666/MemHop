// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 超图边操作：创建与按图查询。
package l3

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/timeutil"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// CreateEdge 创建超图边，ID = hash(graphID:nodeIDs)，返回边 ID。
func CreateEdge(engine *storage.StorageEngine, graphID string, kind model.GraphEdgeKind, nodeIDs []uint64, weight float32) (uint64, error) {
	graphHash, err := parseGraphID(graphID)
	if err != nil {
		return 0, err
	}
	edgeID := hash.HashID(fmt.Sprintf("%s:%v", graphID, nodeIDs))
	edge := &model.HypergraphEdge{
		IDHash:    edgeID,
		GraphID:   graphHash,
		Kind:      kind,
		NodeIDs:   nodeIDs,
		Weight:    weight,
		CreatedAt: timeutil.NowMs(),
	}
	if err := record.WriteHypergraphEdge(engine, edgeID, edge); err != nil {
		return 0, err
	}
	return edgeID, nil
}

// ListEdge 按图查询边。
func ListEdge(engine *storage.StorageEngine, graphID string) []model.HypergraphEdge {
	graphHash, err := hash.ParseID(graphID)
	if err != nil {
		return nil
	}
	var out []model.HypergraphEdge
	for _, edge := range record.CollectAllHypergraphEdges(engine) {
		if edge.GraphID == graphHash {
			out = append(out, edge)
		}
	}
	return out
}
