// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/query/crud"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
)

// GetL2 loads a single L2 topic by hex ID and returns it as TopicDetail.
func (m *MemHop) GetL2(id string) (*crud.TopicDetail, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	slot, err := crud.GetL2(m.engine, id)
	if err != nil {
		return nil, err
	}
	detail := crud.ToTopicDetail(slot)
	return &detail, nil
}

// ListL2 lists L2 topics with pagination and keyword filter.
func (m *MemHop) ListL2(q crud.TopicListQuery) (*crud.TopicListResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return crud.ListL2(m.engine, q)
}

// DeleteL2 deletes an L2 topic and all associated data.
func (m *MemHop) DeleteL2(id string) error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	return crud.DeleteL2(m.engine, m.l1Reverse, m.sparseIndex, m.l2Meta, id)
}

// MergeL2 merges multiple L2 topics into a primary topic.
func (m *MemHop) MergeL2(primaryID string, mergeIDs []string) (*crud.MergeResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return crud.MergeL2(m.engine, m.l1Reverse, m.sparseIndex, m.l2Meta, primaryID, mergeIDs)
}

// GetSceneTree lists the full tree of nodes within a scene.
// sceneID is a 16-character hex string.
func (m *MemHop) GetSceneTree(sceneID string) (*crud.SceneTreeResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	sceneHash, err := hash.ParseID(sceneID)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse scene_id", err)
	}
	return crud.GetSceneTree(m.engine, m.l2Meta, sceneHash)
}
