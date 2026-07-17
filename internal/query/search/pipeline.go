// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Pipeline components: candidate set builder.

package search

import (
	"encoding/json"

	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
	"memhop/internal/common/hash"
)

// BuildCandidateSet returns L2 IDs for scoped search.
//
// If l3ID is specified, restricts to L2 contexts referencing that L3 graph.
// Otherwise returns all depth≤2 L2 context IDs.
func BuildCandidateSet(
	l2Meta *index.L2MetaIndex,
	sparse *index.SparseIndex,
	l3ID *string,
) map[uint64]struct{} {
	if l3ID != nil {
		l3Hash, err := hash.ParseID(*l3ID)
		if err != nil {
			return nil
		}
		ids := l2Meta.GetL2IDsByL3(l3Hash)
		if len(ids) == 0 {
			return nil
		}
		set := make(map[uint64]struct{}, len(ids))
		for _, id := range ids {
			set[id] = struct{}{}
		}
		return set
	}
	// Full scan: all L2 IDs with depth ≤ 2.
	set := make(map[uint64]struct{})
	l2Meta.Iter(func(idHash uint64, meta *index.L2Meta) bool {
		if meta.Depth <= 2 {
			set[idHash] = struct{}{}
		}
		return true
	})
	if len(set) == 0 {
		return nil
	}
	return set
}



// ============================================================================
// L1 Associated Contexts & Previews (public API)
// ============================================================================

// MatchedContext pairs a TopicSlot with its retrieval score.
type MatchedContext struct {
	Topic *model.TopicSlot
	Score float32
}

// GetL1AssociatedContexts finds L2 contexts related to matched contexts
// via the L1 hypergraph (ContextNode + Hyperedge traversal).
func GetL1AssociatedContexts(
	engine *storage.StorageEngine,
	matched []MatchedContext,
	l1Reverse *index.L1ReverseIndex,
) []ContextResult {
	if l1Reverse == nil || len(matched) == 0 {
		return []ContextResult{}
	}
	primaryIDs := matchedContextIDs(matched)
	nodeIDs := l1Reverse.FindAssociated(primaryIDs)
	seen := copyMap(primaryIDs)
	var associated []ContextResult
	for _, nodeHash := range nodeIDs {
		cr := loadAssociatedContext(engine, nodeHash, seen)
		if cr != nil {
			associated = append(associated, *cr)
		}
	}
	addParentContexts(engine, matched, seen, &associated)
	if associated == nil {
		return []ContextResult{}
	}
	return associated
}

// GetL1Previews builds lightweight L1 previews for matched contexts.
func GetL1Previews(
	engine *storage.StorageEngine,
	matched []MatchedContext,
	l1Reverse *index.L1ReverseIndex,
	keywords []string,
) []L1Preview {
	if l1Reverse == nil || len(matched) == 0 {
		return []L1Preview{}
	}
	primaryIDs := matchedContextIDs(matched)
	nodeIDs := l1Reverse.FindAssociated(primaryIDs)
	return buildPreviewsFromNodes(engine, nodeIDs, primaryIDs, matched, keywords)
}

func matchedContextIDs(matched []MatchedContext) map[uint64]struct{} {
	ids := make(map[uint64]struct{}, len(matched))
	for _, mc := range matched {
		ids[mc.Topic.ID] = struct{}{}
	}
	return ids
}

func copyMap(m map[uint64]struct{}) map[uint64]struct{} {
	out := make(map[uint64]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

func loadAssociatedContext(
	engine *storage.StorageEngine,
	nodeHash uint64,
	seen map[uint64]struct{},
) *ContextResult {
	rt, data, err := engine.ReadRecord(nodeHash)
	if err != nil || rt != storage.RecL1SceneNode {
		return nil
	}
	var node model.SceneNode
	if json.Unmarshal(data, &node) != nil || node.SceneID == 0 {
		return nil
	}
	if _, ok := seen[node.SceneID]; ok {
		return nil
	}
	seen[node.SceneID] = struct{}{}
	_, ctxData, err := engine.ReadRecord(node.SceneID)
	if err != nil {
		return nil
	}
	var ctx model.TopicSlot
	if json.Unmarshal(ctxData, &ctx) != nil {
		return nil
	}
	cr := topicSlotToResult(&ctx, float32(node.Importance))
	return &cr
}

func addParentContexts(
	engine *storage.StorageEngine,
	matched []MatchedContext,
	seen map[uint64]struct{},
	associated *[]ContextResult,
) {
	for _, mc := range matched {
		if mc.Topic.ParentID == nil {
			continue
		}
		parentID := *mc.Topic.ParentID
		if _, ok := seen[parentID]; ok {
			continue
		}
		_, data, err := engine.ReadRecord(parentID)
		if err != nil {
			continue
		}
		var parent model.TopicSlot
		if json.Unmarshal(data, &parent) != nil {
			continue
		}
		seen[parentID] = struct{}{}
		cr := topicSlotToResult(&parent, 0.5)
		*associated = append(*associated, cr)
	}
}

func buildPreviewsFromNodes(
	engine *storage.StorageEngine,
	nodeIDs []uint64,
	primaryIDs map[uint64]struct{},
	matched []MatchedContext,
	keywords []string,
) []L1Preview {
	scores := buildScoreMap(matched)
	seen := make(map[uint64]struct{})
	var previews []L1Preview
	for _, nodeHash := range nodeIDs {
		if _, ok := seen[nodeHash]; ok {
			continue
		}
		seen[nodeHash] = struct{}{}
		p := buildOnePreview(engine, nodeHash, scores, keywords)
		if p != nil {
			previews = append(previews, *p)
		}
	}
	if previews == nil {
		return []L1Preview{}
	}
	return previews
}

func buildScoreMap(matched []MatchedContext) map[uint64]float32 {
	m := make(map[uint64]float32, len(matched))
	for _, mc := range matched {
		m[mc.Topic.ID] = mc.Score
	}
	return m
}

func buildOnePreview(
	engine *storage.StorageEngine,
	nodeHash uint64,
	scores map[uint64]float32,
	keywords []string,
) *L1Preview {
	rt, data, err := engine.ReadRecord(nodeHash)
	if err != nil || rt != storage.RecL1SceneNode {
		return nil
	}
	var node model.SceneNode
	if json.Unmarshal(data, &node) != nil {
		return nil
	}
	p := &L1Preview{
		ID:              hash.FormatHash(nodeHash),
		Importance:      float64Ptr(float64(node.Importance)),
		DominantEmotion: deriveEmotion(node.Valence, node.Arousal),
		MatchedKeywords: keywords,
	}
	if s, ok := scores[node.SceneID]; ok {
		v := float64(s)
		p.RecallScore = &v
	}
	return p
}

func deriveEmotion(valence, arousal float64) *string {
	var label string
	switch {
	case valence > 0.3 && arousal > 0.5:
		label = "excited"
	case valence > 0.3:
		label = "content"
	case valence < -0.3 && arousal > 0.5:
		label = "distressed"
	case valence < -0.3:
		label = "melancholic"
	default:
		label = "neutral"
	}
	return &label
}

func topicSlotToResult(ctx *model.TopicSlot, score float32) ContextResult {
	var parentID *string
	if ctx.ParentID != nil {
		s := hash.FormatHash(*ctx.ParentID)
		parentID = &s
	}
	l4Refs := formatHashSlice(ctx.UserL4Refs, ctx.AgentL4Refs)
	l3Refs := formatHashSlice(ctx.UserL3Refs, ctx.AgentL3Refs)
	childIDs := make([]string, len(ctx.ChildrenIDs))
	for i, c := range ctx.ChildrenIDs {
		childIDs[i] = hash.FormatHash(c)
	}
	return ContextResult{
		ID: hash.FormatHash(ctx.ID), ParentID: parentID,
		Depth: ctx.Depth, SceneID: hash.FormatHash(ctx.SceneID),
		UserKeywords: ctx.UserKeywords, UserTimestamp: ctx.UserTimestamp,
		AgentKeywords: ctx.AgentKeywords, AgentTimestamp: ctx.AgentTimestamp,
		FusedKeywords: ctx.FusedKeywords, FusedSummary: ctx.FusedSummary,
		ChildrenIDs: childIDs, L4Refs: l4Refs, L3Refs: l3Refs,
		RetrievalScore: score,
	}
}

func formatHashSlice(a, b []uint64) []string {
	out := make([]string, 0, len(a)+len(b))
	for _, r := range a {
		out = append(out, hash.FormatHash(r))
	}
	for _, r := range b {
		out = append(out, hash.FormatHash(r))
	}
	return out
}
