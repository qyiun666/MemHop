// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 动作链操作：新建/查询/更新/删除（删除级联清理其 ActionStep）。
package repo

import (
	"fmt"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	"github.com/qyiun666/MemHop/internal/sub/repo/index"
)

// CreateChainL5 新建动作链，ID = hash(title:trigger)，返回链 ID。
func CreateChainL5(engine *core.StorageEngine, title, trigger string) (uint64, error) {
	chainID := common.HashID(fmt.Sprintf("%s:%s", title, trigger))
	now := time.Now().UnixMilli()
	chain := &core.ActionChainSlot{
		IDHash:    chainID,
		Title:     title,
		Trigger:   trigger,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := core.WriteActionChainSlot(engine, chainID, chain); err != nil {
		return 0, err
	}
	return chainID, nil
}

// GetChainL5 按 id 查询动作链。
func GetChainL5(engine *core.StorageEngine, id string) (*core.ActionChainSlot, error) {
	idHash, err := common.ParseID(id)
	if err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse chain id", err)
	}
	return core.ReadActionChainSlot(engine, idHash)
}

// UpdateChainL5 全量覆盖写回动作链（ID 以参数为准）。
func UpdateChainL5(engine *core.StorageEngine, id string, slot *core.ActionChainSlot) error {
	idHash, err := common.ParseID(id)
	if err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse chain id", err)
	}
	slot.IDHash = idHash
	slot.UpdatedAt = time.Now().UnixMilli()
	return core.WriteActionChainSlot(engine, idHash, slot)
}

// DeleteChainL5 删除动作链：收集该链全部 ActionStep + 链记录，一次性批量落盘。
func DeleteChainL5(engine *core.StorageEngine, id string) bool {
	chainHash, err := common.ParseID(id)
	if err != nil {
		return false
	}
	var targets []uint64
	for _, step := range core.CollectAllActionSteps(engine) {
		if step.ChainID == chainHash {
			targets = append(targets, step.IDHash)
		}
	}
	targets = append(targets, chainHash)
	_, err = engine.DeleteRecordBatch(targets)
	return err == nil
}

// ListChainsL5 返回全部动作链，未排序。
func ListChainsL5(engine *core.StorageEngine) []core.ActionChainSlot {
	return core.CollectAllActionChains(engine)
}

// MatchChainsL5 返回标题或触发条件命中任一查询分词的 L5 动作链。
func MatchChainsL5(engine *core.StorageEngine, query string) []core.ActionChainSlot {
	terms := index.Tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	var out []core.ActionChainSlot
	for _, chain := range core.CollectAllActionChains(engine) {
		text := strings.ToLower(chain.Title + " " + chain.Trigger)
		for _, term := range terms {
			if strings.Contains(text, strings.ToLower(term)) {
				out = append(out, chain)
				break
			}
		}
	}
	return out
}
