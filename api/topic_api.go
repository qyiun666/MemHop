// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// v0.60.0 Topic domain: L0 profile write + L2 specialized operations that
// don't fit the generic Get/List/Delete/Update surface.

package memhop

import (
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
	"memhop/internal/query/crud"
)

// Topic performs an L0 / L2 sub-operation identified by op.Kind.
//
// Supported operations:
//   - TOpSetProfile — overwrite L0 profile with op.ProfileDelta (op.ProfileDelta required)
//   - TOpMerge      — merge secondary L2 topics into a primary (op.PrimaryID + op.MergeIDs required)
//   - TOpSceneTree  — return the full L2 scene tree (op.SceneID required)
func (m *MemHop) Topic(op TopicOp) (*TopicResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	switch op.Kind {
	case TOpSetProfile:
		if op.ProfileDelta == nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "TOpSetProfile requires ProfileDelta")
		}
		// Invalidate profile cache on write.
		m.profileCache = nil
		if err := crud.WriteProfile(m.engine, *op.ProfileDelta); err != nil {
			return nil, err
		}
		return &TopicResult{}, nil

	case TOpMerge:
		if op.PrimaryID == "" {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "TOpMerge requires PrimaryID")
		}
		r, err := crud.MergeL2(m.engine, m.getL1Reverse(), m.sparseIndex, m.getL2Meta(), op.PrimaryID, op.MergeIDs)
		if err != nil {
			return nil, err
		}
		return &TopicResult{Merge: r}, nil

	case TOpSceneTree:
		if op.SceneID == "" {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "TOpSceneTree requires SceneID")
		}
		sceneHash, err := hash.ParseID(op.SceneID)
		if err != nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse scene_id", err)
		}
		r, err := crud.GetSceneTree(m.engine, m.getL2Meta(), sceneHash)
		if err != nil {
			return nil, err
		}
		return &TopicResult{SceneTree: r}, nil

	default:
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "unsupported TopicOpKind")
	}
}
