// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"github.com/qyiun666/memhop/memhop/internal/hash"
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
)

// GetL5 loads an L5 action chain by hex ID and returns it as CrystalSummary.
func (m *MemHop) GetL5(id string) (*query.CrystalSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	chain, err := query.GetL5(m.engine, id)
	if err != nil {
		return nil, err
	}
	return actionChainToSummary(chain), nil
}

// DeleteL5 deletes an L5 action chain and all its steps.
func (m *MemHop) DeleteL5(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	return query.DeleteL5(m.engine, id)
}

// ListCrystals lists L5 action chains with pagination.
func (m *MemHop) ListCrystals(q query.CrystalListQuery) (*query.CrystalListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return query.ListCrystals(m.engine, q)
}

func actionChainToSummary(c *model.ActionChainSlot) *query.CrystalSummary {
	var lastTriggered *int64
	if c.LastTriggered > 0 {
		t := c.LastTriggered
		lastTriggered = &t
	}
	return &query.CrystalSummary{
		ID:            hash.FormatHash(c.IDHash),
		Title:         c.Title,
		Condition:     c.Trigger,
		Status:        c.Status.String(),
		TriggerCount:  c.TriggerCount,
		SuccessRate:   c.SuccessRate,
		LastTriggered: lastTriggered,
		CreatedAt:     c.CreatedAt,
	}
}
