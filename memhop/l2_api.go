// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// GetL2 loads a single L2 topic by hex ID and returns it as TopicDetail.
func (m *MemHop) GetL2(id string) (*query.TopicDetail, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	slot, err := query.GetL2(m.engine, id)
	if err != nil {
		return nil, err
	}
	detail := slotToTopicDetail(slot)
	return &detail, nil
}

// ListL2 lists L2 topics with pagination and keyword filter.
func (m *MemHop) ListL2(q query.TopicListQuery) (*query.TopicListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return query.ListL2(m.engine, q)
}

// DeleteL2 deletes an L2 topic and all associated data.
func (m *MemHop) DeleteL2(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	return query.DeleteL2(m.engine, m.l1Reverse, m.sparseIndex, m.l2Meta, id)
}

// MergeL2 merges multiple L2 topics into a primary topic.
func (m *MemHop) MergeL2(primaryID string, mergeIDs []string) (*query.MergeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return query.MergeL2(m.engine, m.l1Reverse, m.sparseIndex, m.l2Meta, primaryID, mergeIDs)
}

// GetSceneTree lists the full tree of nodes within a scene.
// sceneID is a 16-character hex string.
func (m *MemHop) GetSceneTree(sceneID string) (*query.SceneTreeResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	sceneHash, err := hash.ParseID(sceneID)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse scene_id", err)
	}
	return query.GetSceneTree(m.engine, m.l2Meta, sceneHash)
}

// slotToTopicDetail converts a TopicSlot to a TopicDetail.
func slotToTopicDetail(ctx *model.TopicSlot) query.TopicDetail {
	var parentID *string
	if ctx.ParentID != nil {
		s := hash.FormatHash(*ctx.ParentID)
		parentID = &s
	}
	return query.TopicDetail{
		ID:             hash.FormatHash(ctx.ID),
		ParentID:       parentID,
		Depth:          ctx.Depth,
		SceneID:        hash.FormatHash(ctx.SceneID),
		UserKeywords:   ctx.UserKeywords,
		UserTimestamp:  ctx.UserTimestamp,
		AgentKeywords:  ctx.AgentKeywords,
		AgentTimestamp: ctx.AgentTimestamp,
		FusedKeywords:  ctx.FusedKeywords,
		FusedSummary:   ctx.FusedSummary,
		ChildrenIDs:    formatUint64s(ctx.ChildrenIDs),
		UserL4Refs:     formatUint64s(ctx.UserL4Refs),
		UserL3Refs:     formatUint64s(ctx.UserL3Refs),
		AgentL4Refs:    formatUint64s(ctx.AgentL4Refs),
		AgentL3Refs:    formatUint64s(ctx.AgentL3Refs),
		CreatedAt:      ctx.CreatedAt,
		UpdatedAt:      ctx.UpdatedAt,
	}
}

func formatUint64s(ids []uint64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = hash.FormatHash(id)
	}
	return out
}
