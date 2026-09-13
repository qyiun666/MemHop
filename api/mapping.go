// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Mapping between internal/core uint64 models and the public api DTOs whose
// IDs fields are 16-char hex strings. All mapping is one-way explicit; no
// business logic lives here.

package api

import (
	"github.com/qyiun666/MemHop/internal"
)

func formatID(id uint64) string { return internal.FormatID(id) }

func formatIDs(ids []uint64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = formatID(id)
	}
	return out
}

// cloneStrings copies a list out of an internal record and keeps it non-nil: an
// imported node may carry no keywords, and every other list this package maps already
// answers as []. One field encoding as null while its neighbours encode as [] is two
// shapes for one answer.
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
		// One shape per "no preferences": a host that wrote its profile without the
		// map stored a JSON null, and every other empty collection this facade hands
		// back encodes as empty rather than as null.
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

// toCoreProfileSlot maps the host-writable half of the profile. Everything the
// library owns is inherited from the stored record by the write itself, so an
// input carries only what the host is allowed to state.
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
		IDHash:     formatID(n.IDHash),
		SceneID:    formatID(n.SceneID),
		TopicIDs:   formatIDs(n.TopicIDs),
		EdgeIDs:    formatIDs(n.EdgeIDs),
		Importance: n.Importance,
		Valence:    n.Valence,
		Arousal:    n.Arousal,
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
	topics := make([]TopicSlot, len(r.Topics))
	for i, t := range r.Topics {
		topics[i] = fromTopicSlot(t)
	}
	return &SearchResult{
		Profile:      fromProfileSlot(r.Profile),
		ProfileBrief: r.ProfileBrief,
		Scene:        fromSceneSlot(r.Scene),
		Topics:       topics,
		NewTopicID:   formatID(r.NewTopicID),
	}
}

func fromHypergraphSlot(s internal.HypergraphSlot) HypergraphSlot {
	return HypergraphSlot{
		IDHash:    formatID(s.IDHash),
		Name:      s.Name,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

func fromHypergraphNode(n internal.HypergraphNode) HypergraphNode {
	return HypergraphNode{
		IDHash:    formatID(n.IDHash),
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
		IDHash:    formatID(e.IDHash),
		GraphID:   formatID(e.GraphID),
		Kind:      e.Kind,
		NodeIDs:   formatIDs(e.NodeIDs),
		CreatedAt: e.CreatedAt,
	}
}

// mapL3Members renders a graph's node and edge sets into their public DTOs.
// A whole graph and a queried subgraph carry the same two member sets, so they
// share this and differ only in what else they return.
func mapL3Members(nodes []internal.HypergraphNode, edges []internal.HypergraphEdge) ([]HypergraphNode, []HypergraphEdge) {
	outNodes := make([]HypergraphNode, len(nodes))
	for i, n := range nodes {
		outNodes[i] = fromHypergraphNode(n)
	}
	outEdges := make([]HypergraphEdge, len(edges))
	for i, e := range edges {
		outEdges[i] = fromHypergraphEdge(e)
	}
	return outNodes, outEdges
}

func fromL3Graph(g *internal.L3Graph) *L3Graph {
	nodes, edges := mapL3Members(g.Nodes, g.Edges)
	return &L3Graph{
		Slot:  fromHypergraphSlot(g.Slot),
		Nodes: nodes,
		Edges: edges,
	}
}

func fromL3Subgraph(g *internal.L3Subgraph) *L3Subgraph {
	nodes, edges := mapL3Members(g.Nodes, g.Edges)
	return &L3Subgraph{Nodes: nodes, Edges: edges}
}

func fromArchiveSlot(s internal.ArchiveSlot) ArchiveSlot {
	return ArchiveSlot{
		IDHash:      formatID(s.IDHash),
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

// toCoreAppendSlot maps the fields a host owns onto a content slot for the append
// path. The owning topic comes from the argument the call is keyed by, and the
// record id follows from (topic, Seq), so neither is part of what a caller hands in
// — IDHash and TopicID are read from the slot and dropped.
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
	roots := make([]PlanNodeView, 0, len(t.Roots))
	for _, r := range t.Roots {
		roots = append(roots, fromPlanNodeView(r))
	}
	return PlanTree{Roots: roots, DoneCount: t.DoneCount, TotalCount: t.TotalCount}
}

func fromPlanNodeView(v internal.PlanNodeView) PlanNodeView {
	out := PlanNodeView{
		Seq: v.Seq, ParentSeq: v.ParentSeq, Title: v.Title, Status: string(v.Status),
		Summary: v.Summary, CreatedAt: v.CreatedAt,
		FinishedAt: v.FinishedAt, UpdatedAt: v.UpdatedAt,
		Children: make([]PlanNodeView, 0, len(v.Children)),
	}
	for _, c := range v.Children {
		out.Children = append(out.Children, fromPlanNodeView(c))
	}
	return out
}

func toInternalPlanStep(s PlanStep) internal.PlanStep {
	return internal.PlanStep{
		Seq: s.Seq, Status: s.Status,
		Title: s.Title, Summary: s.Summary,
	}
}
