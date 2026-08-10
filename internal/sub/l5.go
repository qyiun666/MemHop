// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 action chain operations of the sub layer: query / create / update / delete.

package sub

import (
	"sort"
	"strings"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// L5UpdateFields 动作链部分更新字段，nil = 不更新。
type L5UpdateFields struct {
	Title         *string           `json:"title,omitempty"`
	Trigger       *string           `json:"trigger,omitempty"`
	Status        *core.ChainStatus `json:"status,omitempty"`
	Confidence    *float32          `json:"confidence,omitempty"`
	SuccessRate   *float32          `json:"success_rate,omitempty"`
	TriggerCount  *uint32           `json:"trigger_count,omitempty"`
	LastTriggered *int64            `json:"last_triggered,omitempty"`
}

// GetL5 按 id 查询动作链。
func (db *DB) GetL5(id string) (*core.ActionChainSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	return repo.GetChainL5(db.engine, id)
}

// CreateL5 新建动作链，返回链 ID（hex）。
func (db *DB) CreateL5(title, trigger string) (string, error) {
	if title == "" || trigger == "" {
		return "", common.NewError(common.ErrInvalidQuery, "title and trigger are required")
	}
	id, err := repo.CreateChainL5(db.engine, title, trigger)
	if err != nil {
		return "", err
	}
	return common.FormatHash(id), nil
}

// UpdateL5 部分更新动作链字段（读-改-写，未指定的字段保持不变）。
func (db *DB) UpdateL5(id string, fields *L5UpdateFields) error {
	if fields == nil {
		return nil
	}
	chain, err := repo.GetChainL5(db.engine, id)
	if err != nil {
		return err
	}
	if fields.Title != nil {
		chain.Title = *fields.Title
	}
	if fields.Trigger != nil {
		chain.Trigger = *fields.Trigger
	}
	if fields.Status != nil {
		chain.Status = *fields.Status
	}
	if fields.Confidence != nil {
		chain.Confidence = *fields.Confidence
	}
	if fields.SuccessRate != nil {
		chain.SuccessRate = *fields.SuccessRate
	}
	if fields.TriggerCount != nil {
		chain.TriggerCount = *fields.TriggerCount
	}
	if fields.LastTriggered != nil {
		chain.LastTriggered = *fields.LastTriggered
	}
	return repo.UpdateChainL5(db.engine, id, chain)
}

// DeleteL5 删除动作链，级联清理其 ActionStep。
func (db *DB) DeleteL5(id string) error {
	if _, err := common.ParseID(id); err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse chain id", err)
	}
	if !repo.DeleteChainL5(db.engine, id) {
		return common.NewError(common.ErrIO, "delete chain", nil)
	}
	return nil
}

// L5ListQuery 动作链列表查询条件。
type L5ListQuery struct {
	Status          *string `json:"status,omitempty"`            // 状态字符串："draft"/"active"/"deprecated"
	MinTriggerCount *uint32 `json:"min_trigger_count,omitempty"` // 触发次数下限
	Keyword         string  `json:"keyword,omitempty"`           // Title 子串匹配（大小写不敏感）
}

// ListL5 列出动作链：过滤 → 按 UpdatedAt 降序 → 返回全量。
func (db *DB) ListL5(q L5ListQuery) ([]core.ActionChainSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	kw := strings.ToLower(q.Keyword)
	all := repo.ListChainsL5(db.engine)
	filtered := make([]core.ActionChainSlot, 0, len(all))
	for _, chain := range all {
		if q.Status != nil && chain.Status.String() != *q.Status {
			continue
		}
		if q.MinTriggerCount != nil && chain.TriggerCount < *q.MinTriggerCount {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(chain.Title), kw) {
			continue
		}
		filtered = append(filtered, chain)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt > filtered[j].UpdatedAt
	})
	if filtered == nil {
		return []core.ActionChainSlot{}, nil
	}
	return filtered, nil
}
