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

// L5UpdateFields holds partial chain update fields; nil = not updated.
type L5UpdateFields struct {
	Title         *string           `json:"title,omitempty"`
	Trigger       *string           `json:"trigger,omitempty"`
	Status        *core.ChainStatus `json:"status,omitempty"`
	Confidence    *float32          `json:"confidence,omitempty"`
	SuccessRate   *float32          `json:"success_rate,omitempty"`
	TriggerCount  *uint32           `json:"trigger_count,omitempty"`
	LastTriggered *int64            `json:"last_triggered,omitempty"`
}

func (db *DB) GetL5(id string) (*core.ActionChainSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	return repo.GetChainL5(db.engine, id)
}

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

// UpdateL5 partially updates chain fields (read-modify-write; unset fields
// stay unchanged).
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

// DeleteL5 deletes a chain, cascading to its ActionSteps.
func (db *DB) DeleteL5(id string) error {
	if _, err := common.ParseID(id); err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse chain id", err)
	}
	if !repo.DeleteChainL5(db.engine, id) {
		return common.NewError(common.ErrIO, "delete chain", nil)
	}
	return nil
}

type L5ListQuery struct {
	Status          *string `json:"status,omitempty"`            // "draft"/"active"/"deprecated"
	MinTriggerCount *uint32 `json:"min_trigger_count,omitempty"` // lower bound
	Keyword         string  `json:"keyword,omitempty"`           // Title substring (case-insensitive)
}

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
