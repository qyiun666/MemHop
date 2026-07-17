// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/query/crud"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
)

// GetL5 loads an L5 action chain by hex ID and returns it as CrystalSummary.
func (m *MemHop) GetL5(id string) (*crud.CrystalSummary, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	chain, err := crud.GetL5(m.engine, id)
	if err != nil {
		return nil, err
	}
	summary := crud.ToCrystalSummary(chain)
	return &summary, nil
}

// DeleteL5 deletes an L5 action chain and all its steps.
func (m *MemHop) DeleteL5(id string) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	return crud.DeleteL5(m.engine, id)
}

// ListCrystals lists L5 action chains with pagination.
func (m *MemHop) ListCrystals(q crud.CrystalListQuery) (*crud.CrystalListResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return crud.ListCrystals(m.engine, q)
}

// CreateActionChain creates a new L5 action chain.
func (m *MemHop) CreateActionChain(input crud.L5ChainInput) (string, error) {
	if m.closed.Load() {
		return "", mherrors.ErrClosed
	}
	return crud.CreateL5Chain(m.engine, input)
}

// AppendActionStep adds a step to an existing chain.
func (m *MemHop) AppendActionStep(chainID string, step crud.L5StepInput) (string, error) {
	if m.closed.Load() {
		return "", mherrors.ErrClosed
	}
	id, err := hash.ParseID(chainID)
	if err != nil {
		return "", mherrors.NewError(mherrors.ErrInvalidQuery, "invalid chain id", err)
	}
	return crud.AppendL5Step(m.engine, id, step)
}

// UpdateChainConfidence applies EMA confidence update based on success/failure.
func (m *MemHop) UpdateChainConfidence(chainID string, success bool) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	id, err := hash.ParseID(chainID)
	if err != nil {
		return mherrors.NewError(mherrors.ErrInvalidQuery, "invalid chain id", err)
	}
	return crud.UpdateL5Confidence(m.engine, id, success)
}

// IncrChainTrigger increments the trigger count and updates last triggered time.
func (m *MemHop) IncrChainTrigger(chainID string) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	id, err := hash.ParseID(chainID)
	if err != nil {
		return mherrors.NewError(mherrors.ErrInvalidQuery, "invalid chain id", err)
	}
	return crud.IncrL5Trigger(m.engine, id)
}

// BatchDeleteCrystals deletes multiple L5 action chains and their steps.
func (m *MemHop) BatchDeleteCrystals(ids []string) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	return crud.BatchDeleteL5(m.engine, ids)
}

// BatchUpdateChains applies field updates to multiple L5 chains.
func (m *MemHop) BatchUpdateChains(updates []crud.L5ChainUpdate) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	return crud.BatchUpdateL5(m.engine, updates)
}
