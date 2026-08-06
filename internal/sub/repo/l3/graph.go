// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 超图操作：HypergraphSlot（图容器）的创建、查询与级联删除。
package l3

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/common/timeutil"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// CreateGraph 导入/创建超图，ID = hash(name)，返回图 ID。
func CreateGraph(engine *storage.StorageEngine, name string, source model.HypergraphSource) (uint64, error) {
	graphID := hash.HashID(name)
	now := timeutil.NowMs()
	slot := &model.HypergraphSlot{
		IDHash:    graphID,
		Name:      name,
		Source:    source,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := record.WriteGraphSlot(engine, graphID, slot); err != nil {
		return 0, err
	}
	return graphID, nil
}

// ListGraphs 返回全部超图列表。
func ListGraphs(engine *storage.StorageEngine) []model.HypergraphSlot {
	return record.CollectAllGraphSlots(engine)
}

// DeleteGraph 级联删除：收集该图全部 node/edge + 图记录，一次性批量落盘。
func DeleteGraph(engine *storage.StorageEngine, id string) bool {
	graphHash, err := hash.ParseID(id)
	if err != nil {
		return false
	}
	var targets []uint64
	for _, node := range record.CollectAllHypergraphNodes(engine) {
		if node.GraphID == graphHash {
			targets = append(targets, node.IDHash)
		}
	}
	for _, edge := range record.CollectAllHypergraphEdges(engine) {
		if edge.GraphID == graphHash {
			targets = append(targets, edge.IDHash)
		}
	}
	targets = append(targets, graphHash)
	_, err = engine.DeleteRecordBatch(targets)
	return err == nil
}

// parseGraphID 解析图 id，失败返回错误。
func parseGraphID(id string) (uint64, error) {
	graphHash, err := hash.ParseID(id)
	if err != nil {
		return 0, mherrors.NewError(mherrors.ErrInvalidQuery, "parse graph id", err)
	}
	return graphHash, nil
}
