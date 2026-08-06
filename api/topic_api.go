// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Topic domain: L0 profile write + L2 specialized operations that
// don't fit the generic Get/List/Delete/Update surface.

package memhop

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/query/crud"
)

// Topic performs an L0 / L2 sub-operation identified by op.Kind.
//
// Supported operations:
//   - TOpSetProfile  — overwrite L0 profile with op.ProfileDelta (op.ProfileDelta required)
//   - TOpMerge       — merge secondary L2 topics into a primary (op.PrimaryID + op.MergeIDs required)
//   - TOpSceneTree   — return the full L2 scene tree (op.SceneID required)
//   - TOpListScenes  — list all scenes aggregated from the L2MetaIndex
//   - TOpDeleteScene — delete every L2 topic of a scene (op.SceneID required)
//   - TOpMergeScenes — rewrite all secondary-scene topics into the primary scene
//     (op.PrimarySceneID + op.SecondarySceneID required)
func (m *MemHop) Topic(op TopicOp) (*TopicResult, error) {
	if err := m.beginRead(); err != nil {
		return nil, err
	}
	defer m.mu.RUnlock()
	switch op.Kind {
	case TOpSetProfile:
		if op.ProfileDelta == nil {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "TOpSetProfile requires ProfileDelta")
		}
		// Invalidate profile cache on write.
		m.profileCache.Store(nil)
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

	case TOpListScenes:
		return &TopicResult{Scenes: crud.ListScenes(m.getL2Meta())}, nil

	case TOpDeleteScene:
		if op.SceneID == "" {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "TOpDeleteScene requires SceneID")
		}
		if err := crud.DeleteScene(m.engine, m.getL1Reverse(), m.sparseIndex, m.getL2Meta(), op.SceneID); err != nil {
			return nil, err
		}
		return &TopicResult{}, nil

	case TOpMergeScenes:
		if op.PrimarySceneID == "" || op.SecondarySceneID == "" {
			return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "TOpMergeScenes requires PrimarySceneID and SecondarySceneID")
		}
		r, err := crud.MergeScenes(m.engine, m.getL2Meta(), op.PrimarySceneID, op.SecondarySceneID)
		if err != nil {
			return nil, err
		}
		return &TopicResult{MergeScenes: r}, nil

	default:
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "unsupported TopicOpKind")
	}
}
