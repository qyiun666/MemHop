// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 TopicSlot CRUD operations.

package query

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/index"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
	"github.com/qyiun666/memhop/memhop/internal/hash"
	"github.com/qyiun666/memhop/memhop/internal/timeutil"
)

// GetL2 loads a single L2 context by hex ID.
func GetL2(engine *storage.StorageEngine, id string) (*model.TopicSlot, error) {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse l2 id", err)
	}
	return loadTopic(engine, idHash)
}

// ListL2 lists L2 contexts with pagination and keyword filter.
func ListL2(engine *storage.StorageEngine, q TopicListQuery) (*TopicListResult, error) {
	all := collectAllTopics(engine)
	filterByKeyword(&all, q.Keyword)
	sortTopicsByUpdated(all)
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
) (*TopicDetail, error) {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse l2 id", err)
	}
	ctx, err := loadTopic(engine, idHash)
	if err != nil {
		return nil, err
	}
	indexChanged := applyL2Updates(ctx, fields)
	if indexChanged {
		reindexTopic(sparse, ctx)
	}
	ctx.UpdatedAt = timeutil.NowMs()
	ctx.Version++
	if err := writeTopic(engine, idHash, ctx); err != nil {
		return nil, err
	}
	detail := toTopicDetail(ctx)
	return &detail, nil
}

// DeleteL2 deletes an L2 context and all associated data.
func DeleteL2(
	engine *storage.StorageEngine,
	l1Reverse *L1ReverseIndex,
	sparse *index.SparseIndex,
	id string,
) error {
	idHash, err := hash.ParseID(id)
	if err != nil {
		return core.NewError(core.ErrInvalidQuery, "parse l2 id", err)
	}
	ctx, err := loadTopic(engine, idHash)
	if err != nil {
		return nil // not found = already deleted
	}
	deleteL1Nodes(engine, l1Reverse, idHash)
	deleteL4Refs(engine, ctx)
	sparse.RemoveDocument(idHash)
	if l1Reverse != nil {
		l1Reverse.RemoveContext(idHash)
	}
	engine.DeleteRecord(idHash)
	return nil
}

// MergeL2 merges multiple L2 contexts into a primary context.
func MergeL2(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	primaryID string,
	mergeIDs []string,
) (*MergeResult, error) {
	primaryHash, err := hash.ParseID(primaryID)
	if err != nil {
		return nil, core.NewError(core.ErrInvalidQuery, "parse primary id", err)
	}
	if !engine.Contains(primaryHash) {
		return nil, core.NewError(core.ErrNotFound, "primary not found")
	}
	mergeHashes, err := parseMergeIDs(mergeIDs)
	if err != nil {
		return nil, err
	}
	primaryCtx, err := loadTopic(engine, primaryHash)
	if err != nil {
		return nil, err
	}
	absorbSecondaries(engine, sparse, primaryCtx, mergeHashes, mergeIDs)
	primaryCtx.UpdatedAt = timeutil.NowMs()
	primaryCtx.Version++
	if err := writeTopic(engine, primaryHash, primaryCtx); err != nil {
		return nil, err
	}
	reindexTopic(sparse, primaryCtx)
	turnCount := uint32(len(primaryCtx.UserL4Refs) + len(primaryCtx.AgentL4Refs))
	return &MergeResult{
		PrimaryID:        hash.FormatHash(primaryHash),
		MergedCount:      uint32(len(mergeHashes)),
		NewTurnCount:     turnCount,
		AbsorbedTopicIDs: mergeIDs,
	}, nil
}

// GetSceneTree lists the full tree of nodes within a scene.
func GetSceneTree(
	engine *storage.StorageEngine,
	l2Meta *index.L2MetaIndex,
	sceneID uint64,
) (*SceneTreeResult, error) {
	nodeIDs := l2Meta.GetByScene(sceneID)
	if len(nodeIDs) == 0 {
		return emptySceneTree(sceneID), nil
	}
	nodes := loadTopicsByIDs(engine, nodeIDs)
	sortTopicsByCreated(nodes)
	return buildSceneTree(sceneID, nodes), nil
}

// --- internal helpers ---

func loadTopic(engine *storage.StorageEngine, idHash uint64) (*model.TopicSlot, error) {
	_, data, err := engine.ReadRecord(idHash)
	if err != nil {
		return nil, err
	}
	var ctx model.TopicSlot
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "unmarshal topic", err)
	}
	return &ctx, nil
}

func writeTopic(engine *storage.StorageEngine, idHash uint64, ctx *model.TopicSlot) error {
	data, err := json.Marshal(ctx)
	if err != nil {
		return core.NewError(core.ErrSerialization, "marshal topic", err)
	}
	_, err = engine.WriteRecord(storage.RecL2Topic, idHash, data)
	return err
}

func collectAllTopics(engine *storage.StorageEngine) []model.TopicSlot {
	var all []model.TopicSlot
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil || rt != storage.RecL2Topic {
			return true
		}
		var ctx model.TopicSlot
		if json.Unmarshal(data, &ctx) == nil {
			all = append(all, ctx)
		}
		return true
	})
	return all
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

func sortTopicsByUpdated(all []model.TopicSlot) {
	sort.Slice(all, func(i, j int) bool {
		return all[i].UpdatedAt > all[j].UpdatedAt
	})
}

func sortTopicsByCreated(all []model.TopicSlot) {
	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt < all[j].CreatedAt
	})
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
		FusedSummary:  ctx.FusedSummary,
		TurnCount:     turnCount,
		IsActive:      false,
		CreatedAt:     ctx.CreatedAt,
		L4Count:       turnCount,
		L3Count:       l3Count,
		UpdatedAt:     ctx.UpdatedAt,
	}
}

func toTopicDetail(ctx *model.TopicSlot) TopicDetail {
	return TopicDetail{
		ID:             hash.FormatHash(ctx.ID),
		ParentID:       formatOptUint64(ctx.ParentID),
		Depth:          ctx.Depth,
		SceneID:        hash.FormatHash(ctx.SceneID),
		UserKeywords:   ctx.UserKeywords,
		UserTimestamp:  ctx.UserTimestamp,
		AgentKeywords:  ctx.AgentKeywords,
		AgentTimestamp: ctx.AgentTimestamp,
		FusedKeywords:  ctx.FusedKeywords,
		FusedSummary:   ctx.FusedSummary,
		ChildrenIDs:    formatUint64Slice(ctx.ChildrenIDs),
		UserL4Refs:     formatUint64Slice(ctx.UserL4Refs),
		UserL3Refs:     formatUint64Slice(ctx.UserL3Refs),
		AgentL4Refs:    formatUint64Slice(ctx.AgentL4Refs),
		AgentL3Refs:    formatUint64Slice(ctx.AgentL3Refs),
		CreatedAt:      ctx.CreatedAt,
		UpdatedAt:      ctx.UpdatedAt,
	}
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
	if fields.FusedSummary != nil {
		ctx.FusedSummary = fields.FusedSummary
		changed = true
	}
	if fields.L3Refs != nil {
		refs := parseUint64Slice(fields.L3Refs)
		sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
		ctx.UserL3Refs = dedupUint64(refs)
		ctx.AgentL3Refs = nil
	}
	return changed
}

func reindexTopic(sparse *index.SparseIndex, ctx *model.TopicSlot) {
	sparse.RemoveDocument(ctx.ID)
	text := strings.Join(ctx.UserKeywords, " ")
	if ctx.FusedSummary != nil {
		text += " " + *ctx.FusedSummary
	}
	terms := index.Tokenize(text)
	sparse.AddDocument(ctx.ID, terms, uint32(len(terms)))
}

func deleteL1Nodes(engine *storage.StorageEngine, l1Reverse *L1ReverseIndex, ctxID uint64) {
	if l1Reverse == nil {
		return
	}
	ids := map[uint64]struct{}{ctxID: {}}
	nodeHashes := l1Reverse.FindAssociated(ids)
	for _, nodeHash := range nodeHashes {
		rt, _, err := engine.ReadRecord(nodeHash)
		if err == nil && rt == storage.RecL1SceneNode {
			engine.DeleteRecord(nodeHash)
			l1Reverse.RemoveNode(nodeHash)
		}
	}
}

func deleteL4Refs(engine *storage.StorageEngine, ctx *model.TopicSlot) {
	for _, ref := range ctx.UserL4Refs {
		engine.DeleteRecord(ref)
	}
	for _, ref := range ctx.AgentL4Refs {
		engine.DeleteRecord(ref)
	}
}

func parseMergeIDs(ids []string) ([]uint64, error) {
	hashes := make([]uint64, len(ids))
	for i, id := range ids {
		h, err := hash.ParseID(id)
		if err != nil {
			return nil, core.NewError(core.ErrInvalidQuery, "parse merge id", err)
		}
		if _, err2 := hash.ParseID(id); err2 != nil {
			return nil, err2
		}
		hashes[i] = h
	}
	return hashes, nil
}

func absorbSecondaries(
	engine *storage.StorageEngine,
	sparse *index.SparseIndex,
	primary *model.TopicSlot,
	mergeHashes []uint64,
	mergeIDs []string,
) {
	l4Set := toSet(primary.UserL4Refs)
	agL4Set := toSet(primary.AgentL4Refs)
	l3Set := toSet(primary.UserL3Refs)
	agL3Set := toSet(primary.AgentL3Refs)
	var summaries []string

	for i, secHash := range mergeHashes {
		sec, err := loadTopic(engine, secHash)
		if err != nil {
			continue
		}
		addAll(l4Set, sec.UserL4Refs)
		addAll(agL4Set, sec.AgentL4Refs)
		addAll(l3Set, sec.UserL3Refs)
		addAll(agL3Set, sec.AgentL3Refs)
		if sec.FusedSummary != nil {
			summaries = append(summaries, *sec.FusedSummary)
		}
		sparse.RemoveDocument(secHash)
		engine.DeleteRecord(secHash)
		_ = i
	}
	primary.UserL4Refs = sortedKeys(l4Set)
	primary.AgentL4Refs = sortedKeys(agL4Set)
	primary.UserL3Refs = sortedKeys(l3Set)
	primary.AgentL3Refs = sortedKeys(agL3Set)
	if len(summaries) > 0 {
		combined := ""
		if primary.FusedSummary != nil {
			combined = *primary.FusedSummary
		}
		for _, s := range summaries {
			if combined != "" {
				combined += " | "
			}
			combined += s
		}
		primary.FusedSummary = &combined
	}
}

func loadTopicsByIDs(engine *storage.StorageEngine, ids []uint64) []model.TopicSlot {
	var nodes []model.TopicSlot
	for _, id := range ids {
		rt, data, err := engine.ReadRecord(id)
		if err != nil || rt != storage.RecL2Topic {
			continue
		}
		var ctx model.TopicSlot
		if json.Unmarshal(data, &ctx) == nil {
			nodes = append(nodes, ctx)
		}
	}
	return nodes
}

func buildSceneTree(sceneID uint64, nodes []model.TopicSlot) *SceneTreeResult {
	totalTurns := uint32(len(nodes))
	var depthDist [4]uint32
	var edges [][2]string
	details := make([]TopicDetail, len(nodes))
	for i, ctx := range nodes {
		depthIdx := int(ctx.Depth) - 1
		if depthIdx < 0 {
			depthIdx = 0
		}
		if depthIdx > 3 {
			depthIdx = 3
		}
		depthDist[depthIdx]++
		if ctx.ParentID != nil {
			edges = append(edges, [2]string{hash.FormatHash(*ctx.ParentID), hash.FormatHash(ctx.ID)})
		}
		for _, childID := range ctx.ChildrenIDs {
			edges = append(edges, [2]string{hash.FormatHash(ctx.ID), hash.FormatHash(childID)})
		}
		details[i] = toTopicDetail(&nodes[i])
	}
	return &SceneTreeResult{
		SceneID:           hash.FormatHash(sceneID),
		TotalTurns:        totalTurns,
		DepthDistribution: depthDist,
		Nodes:             details,
		Edges:             edges,
	}
}

func emptySceneTree(sceneID uint64) *SceneTreeResult {
	return &SceneTreeResult{
		SceneID: hash.FormatHash(sceneID),
		Nodes:   []TopicDetail{},
		Edges:   [][2]string{},
	}
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

func formatOptUint64(v *uint64) *string {
	if v == nil {
		return nil
	}
	s := hash.FormatHash(*v)
	return &s
}

func formatUint64Slice(ids []uint64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = hash.FormatHash(id)
	}
	return out
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

func toSet(ids []uint64) map[uint64]struct{} {
	s := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		s[id] = struct{}{}
	}
	return s
}

func addAll(s map[uint64]struct{}, ids []uint64) {
	for _, id := range ids {
		s[id] = struct{}{}
	}
}

func sortedKeys(s map[uint64]struct{}) []uint64 {
	out := make([]uint64, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
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
