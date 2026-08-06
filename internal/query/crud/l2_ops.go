// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 TopicSlot CRUD operations.

package crud

import (
	"sort"
	"strings"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/repo/core/index"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
	"github.com/qyiun666/MemHop/internal/repo/l2"
)

// GetL2 loads a single L2 context by hex ID.
func GetL2(engine *storage.StorageEngine, id string) (*model.TopicSlot, error) {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse l2 id", err)
	}
	return record.ReadTopicSlot(engine, idHash)
}

// ListL2 lists L2 contexts with pagination and keyword filter.
func ListL2(engine *storage.StorageEngine, q TopicListQuery) (*TopicListResult, error) {
	all := record.CollectAllTopics(engine)
	filterByKeyword(&all, q.Keyword)
	skip, take := paginationParams(q.Page, q.PageSize)
	total := len(all)
	items := make([]TopicSummary, 0, take)
	for i := skip; i < skip+take && i < total; i++ {
		items = append(items, toTopicSummary(&all[i]))
	}
	return &TopicListResult{
		Items:    items,
		Total:    total,
		Page:     q.Page,
		PageSize: q.PageSize,
		HasMore:  skip+take < total,
	}, nil
}

// UpdateL2 partially updates an L2 context.
func UpdateL2(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	id string,
	fields UpdateL2Fields,
	timestamp int64,
) (*TopicDetail, error) {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrInvalidQuery, "parse l2 id", err)
	}
	ctx, err := record.ReadTopicSlot(engine, idHash)
	if err != nil {
		return nil, err
	}
	indexChanged := applyL2Updates(ctx, fields)
	if indexChanged {
		ReindexTopic(sparse, ctx)
	}
	if err := record.WriteTopicSlot(engine, idHash, ctx); err != nil {
		return nil, err
	}
	detail := ToTopicDetail(ctx)
	return &detail, nil
}

// DeleteL2 deletes an L2 context and all associated data.
func DeleteL2(
	engine *storage.StorageEngine,
	l1Reverse *index.L1ReverseIndex,
	sparse *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	id string,
) error {
	return l2.DeleteL2(engine, l1Reverse, sparse, l2Meta, id)
}

// MergeL2 merges multiple L2 contexts into a primary context.
func MergeL2(
	engine *storage.StorageEngine,
	l1Reverse *index.L1ReverseIndex,
	sparse *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	primaryID string,
	mergeIDs []string,
) (*MergeResult, error) {
	return l2.MergeL2(engine, l1Reverse, sparse, l2Meta, primaryID, mergeIDs)
}

// GetSceneTree lists the full tree of nodes within a scene.
func GetSceneTree(
	engine *storage.StorageEngine,
	l2Meta *index.L2MetaIndex,
	sceneID uint64,
) (*SceneTreeResult, error) {
	return l2.GetSceneTree(engine, l2Meta, sceneID)
}

// ListScenes aggregates all scenes from the L2MetaIndex byScene view.
func ListScenes(l2Meta *index.L2MetaIndex) []SceneSummary {
	return l2.ListScenes(l2Meta)
}

// DeleteScene deletes every L2 topic belonging to the scene.
func DeleteScene(
	engine *storage.StorageEngine,
	l1Reverse *index.L1ReverseIndex,
	sparse *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	sceneID string,
) error {
	return l2.DeleteScene(engine, l1Reverse, sparse, l2Meta, sceneID)
}

// MergeScenes rewrites all secondary-scene topics to the primary scene.
func MergeScenes(
	engine *storage.StorageEngine,
	l2Meta *index.L2MetaIndex,
	primaryID string,
	secondaryID string,
) (*MergeScenesResult, error) {
	return l2.MergeScenes(engine, l2Meta, primaryID, secondaryID)
}

// --- internal helpers ---

// L2MetaFromTopic converts a TopicSlot to an L2Meta entry (local copy to avoid cycle).
func L2MetaFromTopic(t *model.TopicSlot) *index.L2Meta {
	l3Refs := append(append([]uint64{}, t.UserL3Refs...), t.AgentL3Refs...)
	return &index.L2Meta{
		IDHash:       t.ID,
		Title:        strings.Join(t.UserKeywords, ", "),
		Depth:        t.Depth,
		SceneID:      t.SceneID,
		ChildrenIDs:  t.ChildrenIDs,
		VectorOffset: t.CentroidPageRef,
		ArchiveCount: len(t.UserL4Refs) + len(t.AgentL4Refs),
		L3Refs:       l3Refs,
	}
}

func filterByKeyword(all *[]model.TopicSlot, keyword *string) {
	if keyword == nil {
		return
	}
	kw := strings.ToLower(*keyword)
	filtered := make([]model.TopicSlot, 0, len(*all))
	for _, ctx := range *all {
		text := strings.ToLower(strings.Join(ctx.UserKeywords, " "))
		if strings.Contains(text, kw) {
			filtered = append(filtered, ctx)
		}
	}
	*all = filtered
}

func toTopicSummary(ctx *model.TopicSlot) TopicSummary {
	turnCount := len(ctx.UserL4Refs) + len(ctx.AgentL4Refs)
	l3Count := len(ctx.UserL3Refs) + len(ctx.AgentL3Refs)
	return TopicSummary{
		ID:            hash.FormatHash(ctx.ID),
		Depth:         ctx.Depth,
		SceneID:       hash.FormatHash(ctx.SceneID),
		UserKeywords:  ctx.UserKeywords,
		AgentKeywords: ctx.AgentKeywords,
		FusedKeywords: ctx.FusedKeywords,
		TurnCount:     turnCount,
		IsActive:      false,
		L4Count:       turnCount,
		L3Count:       l3Count,
	}
}

func ToTopicDetail(ctx *model.TopicSlot) TopicDetail {
	return l2.ToTopicDetail(ctx)
}

func applyL2Updates(ctx *model.TopicSlot, fields UpdateL2Fields) bool {
	changed := false
	if fields.UserKeywords != nil {
		ctx.UserKeywords = fields.UserKeywords
		changed = true
	}
	if fields.AgentKeywords != nil {
		ctx.AgentKeywords = fields.AgentKeywords
	}
	if fields.L3Refs != nil {
		refs := parseUint64Slice(fields.L3Refs)
		sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
		ctx.UserL3Refs = dedupUint64(refs)
		ctx.AgentL3Refs = nil
	}
	return changed
}

func ReindexTopic(sparse *index.SparseIndex, ctx *model.TopicSlot) {
	sparse.RemoveDocument(ctx.ID)
	text := strings.Join(ctx.UserKeywords, " ")
	if len(ctx.FusedKeywords) > 0 {
		text += " " + strings.Join(ctx.FusedKeywords, " ")
	}
	terms := index.Tokenize(text)
	sparse.AddDocument(ctx.ID, terms, uint32(len(terms)))
}

// --- pagination & format helpers ---

func paginationParams(page, pageSize int) (skip, take int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	return (page - 1) * pageSize, pageSize
}

func parseUint64Slice(ids []string) []uint64 {
	out := make([]uint64, 0, len(ids))
	for _, s := range ids {
		h, err := hash.ParseID(s)
		if err == nil {
			out = append(out, h)
		}
	}
	return out
}

func dedupUint64(ids []uint64) []uint64 {
	if len(ids) == 0 {
		return ids
	}
	out := []uint64{ids[0]}
	for i := 1; i < len(ids); i++ {
		if ids[i] != ids[i-1] {
			out = append(out, ids[i])
		}
	}
	return out
}
