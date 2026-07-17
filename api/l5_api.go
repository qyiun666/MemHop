// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/core"
	"memhop/internal/core/model"
	"memhop/internal/core/query"
	"memhop/internal/hash"
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

// CreateActionChain creates a new L5 action chain.
func (m *MemHop) CreateActionChain(input query.L5ChainInput) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", core.ErrClosed
	}
	return query.CreateL5Chain(m.engine, input)
}

// AppendActionStep adds a step to an existing chain.
func (m *MemHop) AppendActionStep(chainID string, step query.L5StepInput) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", core.ErrClosed
	}
	id, err := hash.ParseID(chainID)
	if err != nil {
		return "", core.NewError(core.ErrInvalidQuery, "invalid chain id", err)
	}
	return query.AppendL5Step(m.engine, id, step)
}

// UpdateChainConfidence applies EMA confidence update based on success/failure.
func (m *MemHop) UpdateChainConfidence(chainID string, success bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	id, err := hash.ParseID(chainID)
	if err != nil {
		return core.NewError(core.ErrInvalidQuery, "invalid chain id", err)
	}
	return query.UpdateL5Confidence(m.engine, id, success)
}

// IncrChainTrigger increments the trigger count and updates last triggered time.
func (m *MemHop) IncrChainTrigger(chainID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	id, err := hash.ParseID(chainID)
	if err != nil {
		return core.NewError(core.ErrInvalidQuery, "invalid chain id", err)
	}
	return query.IncrL5Trigger(m.engine, id)
}

// BatchDeleteCrystals deletes multiple L5 action chains and their steps.
func (m *MemHop) BatchDeleteCrystals(ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	return query.BatchDeleteL5(m.engine, ids)
}

// BatchUpdateChains applies field updates to multiple L5 chains.
func (m *MemHop) BatchUpdateChains(updates []query.L5ChainUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	return query.BatchUpdateL5(m.engine, updates)
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
