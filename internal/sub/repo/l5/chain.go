// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 动作链操作：新建/查询/更新/删除（删除级联清理其 ActionStep）。
package l5

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/common/timeutil"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// CreateChain 新建动作链，ID = hash(title:trigger)，返回链 ID。
func CreateChain(engine *storage.StorageEngine, title, trigger string) (uint64, error) {
	chainID := hash.HashID(fmt.Sprintf("%s:%s", title, trigger))
	now := timeutil.NowMs()
	chain := &model.ActionChainSlot{
		IDHash:    chainID,
		Title:     title,
		Trigger:   trigger,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := record.WriteActionChainSlot(engine, chainID, chain); err != nil {
		return 0, err
	}
	return chainID, nil
}

// GetChain 按 id 查询动作链。
func GetChain(engine *storage.StorageEngine, id string) (*model.ActionChainSlot, error) {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse chain id", err)
	}
	return record.ReadActionChainSlot(engine, idHash)
}

// UpdateChain 全量覆盖写回动作链（ID 以参数为准）。
func UpdateChain(engine *storage.StorageEngine, id string, slot *model.ActionChainSlot) error {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return mherrors.NewError(mherrors.ErrInvalidQuery, "parse chain id", err)
	}
	slot.IDHash = idHash
	slot.UpdatedAt = timeutil.NowMs()
	return record.WriteActionChainSlot(engine, idHash, slot)
}

// DeleteChain 删除动作链：收集该链全部 ActionStep + 链记录，一次性批量落盘。
func DeleteChain(engine *storage.StorageEngine, id string) bool {
	chainHash, err := hash.ParseID(id)
	if err != nil {
		return false
	}
	var targets []uint64
	for _, step := range record.CollectAllActionSteps(engine) {
		if step.ChainID == chainHash {
			targets = append(targets, step.IDHash)
		}
	}
	targets = append(targets, chainHash)
	_, err = engine.DeleteRecordBatch(targets)
	return err == nil
}
