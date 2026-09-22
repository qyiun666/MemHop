// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Mapping from the internal/core uint64 models to the public DTOs. One-way, no
// business logic; every id a host can see is rendered here.

package api

import (
	"github.com/qyiun666/MemHop/internal"
)

func formatID(id uint64) string { return internal.FormatID(id) }

// mapSlice renders a list of internal records into the element DTO of each. The
// answer is always non-nil: a host decoding a collection never sees null.
func mapSlice[T, U any](in []T, f func(T) U) []U {
	out := make([]U, len(in))
	for i := range in {
		out[i] = f(in[i])
	}
	return out
}

func formatIDs(ids []uint64) []string { return mapSlice(ids, formatID) }

// cloneStrings copies a keyword list out of an internal record. An imported node may
// carry none, and that must encode as [] like every other list here, not as null.
func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func formatPtr(id *uint64) *string {
	if id == nil {
		return nil
	}
	s := formatID(*id)
	return &s
}

func fromProfileSlot(s internal.ProfileSlot) ProfileSlot {
	prefs := s.Preferences
	if prefs == nil {
		// A host that wrote its profile without the map stored a JSON null; the
		// answer it reads back has to be the same shape as every other empty list here.
		prefs = map[string]string{}
	}
	return ProfileSlot{
		Name:         s.Name,
		Role:         s.Role,
		Personality:  s.Personality,
		EmotionState: s.EmotionState,
		MBTI:         s.MBTI,
		Preferences:  prefs,
		AgentType:    s.AgentType,
		UpdatedAtMs:  s.UpdatedAtMs,
	}
}

// toCoreProfileSlot maps the host-writable half of the profile; the library-owned
// half is inherited by the write itself.
func toCoreProfileSlot(s *ProfileInput) internal.ProfileSlot {
	if s == nil {
		return internal.ProfileSlot{}
	}
	return internal.ProfileSlot{
		Name:        s.Name,
		Role:        s.Role,
		Personality: s.Personality,
		Preferences: s.Preferences,
	}
}

func fromSceneNode(n internal.SceneNode) SceneNodeView {
	return SceneNodeView{
		ID:         formatID(n.IDHash),
		SceneID:    formatID(n.SceneID),
		TopicIDs:   formatIDs(n.TopicIDs),
		EdgeIDs:    formatIDs(n.EdgeIDs),
		Importance: n.Importance,
		Valence:    n.Valence,
		Arousal:    n.Arousal,
		EmotionSet: n.EmotionSet,
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
	}
}

func fromSceneSlot(s internal.SceneSlot) SceneSlot {
	return SceneSlot{
		SceneID:   formatID(s.SceneID),
		SceneName: s.SceneName,
		L3ID:      formatOptionalID(s.L3ID),
	}
}

func fromTopicSlot(t internal.TopicSlot) TopicSlot {
	return TopicSlot{
		ID:             formatID(t.ID),
		SceneID:        formatID(t.SceneID),
		ParentID:       formatPtr(t.ParentID),
		Depth:          t.Depth,
		Name:           t.Name,
		FusedKeywords:  cloneStrings(t.FusedKeywords),
		UserTimestamp:  t.UserTimestamp,
		AgentTimestamp: t.AgentTimestamp,
	}
}

func fromSearchResult(r *internal.SearchResult) *SearchResult {
	return &SearchResult{
		Profile:      fromProfileSlot(r.Profile),
		ProfileBrief: r.ProfileBrief,
		Scene:        fromSceneSlot(r.Scene),
		Topics:       mapSlice(r.Topics, fromTopicSlot),
		NewTopicID:   formatID(r.NewTopicID),
	}
}

func fromHypergraphSlot(s internal.HypergraphSlot) HypergraphSlot {
	return HypergraphSlot{
		ID:        formatID(s.IDHash),
		Name:      s.Name,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

func fromHypergraphNode(n internal.HypergraphNode) HypergraphNode {
	return HypergraphNode{
		ID:        formatID(n.IDHash),
		GraphID:   formatID(n.GraphID),
		Title:     n.Title,
		NodeType:  n.NodeType,
		Content:   n.Content,
		Keywords:  cloneStrings(n.Keywords),
		SourceRef: n.SourceRef,
		CreatedAt: n.CreatedAt,
		UpdatedAt: n.UpdatedAt,
	}
}

func fromHypergraphEdge(e internal.HypergraphEdge) HypergraphEdge {
	return HypergraphEdge{
		ID:        formatID(e.IDHash),
		GraphID:   formatID(e.GraphID),
		Kind:      e.Kind,
		NodeIDs:   formatIDs(e.NodeIDs),
		CreatedAt: e.CreatedAt,
	}
}

func fromL3Graph(g *internal.L3Graph) *L3Graph {
	return &L3Graph{
		Slot:  fromHypergraphSlot(g.Slot),
		Nodes: mapSlice(g.Nodes, fromHypergraphNode),
		Edges: mapSlice(g.Edges, fromHypergraphEdge),
	}
}

func fromL3Subgraph(g *internal.L3Subgraph) *L3Subgraph {
	return &L3Subgraph{
		Nodes: mapSlice(g.Nodes, fromHypergraphNode),
		Edges: mapSlice(g.Edges, fromHypergraphEdge),
	}
}

func fromArchiveSlot(s internal.ArchiveSlot) ArchiveSlot {
	return ArchiveSlot{
		ID:          formatID(s.IDHash),
		Kind:        s.Kind,
		Seq:         s.Seq,
		ContentType: s.ContentType,
		Role:        s.Role,
		TopicID:     formatOptionalID(s.TopicID),
		EventType:   s.EventType,
		NodeSeq:     s.NodeSeq,
		CreatedAt:   s.CreatedAt,
		Content:     s.Content,
	}
}

func formatOptionalID(id uint64) string {
	if id == 0 {
		return ""
	}
	return formatID(id)
}

// toCoreAppendSlot drops ID and TopicID: the owning topic comes from the argument
// the call is keyed by, and the record id follows from (topic, Seq).
func toCoreAppendSlot(s ArchiveSlot) internal.ArchiveSlot {
	return internal.ArchiveSlot{
		Kind:        s.Kind,
		Seq:         s.Seq,
		ContentType: s.ContentType,
		Role:        s.Role,
		EventType:   s.EventType,
		NodeSeq:     s.NodeSeq,
		CreatedAt:   s.CreatedAt,
		Content:     s.Content,
	}
}

func fromPlanTree(t *internal.PlanTree) PlanTree {
	return PlanTree{
		Roots:      mapSlice(t.Roots, fromPlanNodeView),
		DoneCount:  t.DoneCount,
		TotalCount: t.TotalCount,
	}
}

func fromPlanNodeView(v internal.PlanNodeView) PlanNodeView {
	return PlanNodeView{
		Seq: v.Seq, ParentSeq: v.ParentSeq, Title: v.Title, Status: string(v.Status),
		Summary: v.Summary, CreatedAt: v.CreatedAt,
		FinishedAt: v.FinishedAt, UpdatedAt: v.UpdatedAt,
		Children: mapSlice(v.Children, fromPlanNodeView),
	}
}

func toInternalPlanStep(s PlanStep) internal.PlanStep {
	return internal.PlanStep{
		Seq: s.Seq, Status: s.Status,
		Title: s.Title, Summary: s.Summary,
	}
}
