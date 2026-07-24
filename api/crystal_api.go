// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// v0.60.0 Crystal domain: L5 action chain create / append / batch operations.

package memhop

import (
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
	"memhop/internal/query/crud"
)

// Crystal performs an L5 sub-operation identified by op.Kind. See
// CrystalOpKind constants for supported operations and required op fields.
func (m *MemHop) Crystal(op CrystalOp) (*CrystalResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	switch op.Kind {
	case COpCreateChain:
		if op.ChainInput == nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "COpCreateChain requires ChainInput")
		}
		id, err := crud.CreateL5Chain(m.engine, *op.ChainInput)
		if err != nil {
			return nil, err
		}
		return &CrystalResult{ChainID: id}, nil

	case COpAppendStep:
		if op.StepInput == nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "COpAppendStep requires StepInput")
		}
		id, err := hash.ParseID(op.ChainID)
		if err != nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "invalid chain id", err)
		}
		stepID, err := crud.AppendL5Step(m.engine, id, *op.StepInput)
		if err != nil {
			return nil, err
		}
		return &CrystalResult{StepID: stepID}, nil

	case COpUpdateConfidence:
		id, err := hash.ParseID(op.ChainID)
		if err != nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "invalid chain id", err)
		}
		return &CrystalResult{}, crud.UpdateL5Confidence(m.engine, id, op.Success)

	case COpIncrTrigger:
		id, err := hash.ParseID(op.ChainID)
		if err != nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "invalid chain id", err)
		}
		return &CrystalResult{}, crud.IncrL5Trigger(m.engine, id)

	case COpBatchDelete:
		return &CrystalResult{}, crud.BatchDeleteL5(m.engine, op.IDs)

	case COpBatchUpdate:
		return &CrystalResult{}, crud.BatchUpdateL5(m.engine, op.Updates)

	default:
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "unsupported CrystalOpKind")
	}
}
