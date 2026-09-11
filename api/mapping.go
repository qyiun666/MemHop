// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Mapping between internal/core uint64 models and the public api DTOs whose
// IDs fields are 16-char hex strings. All mapping is one-way explicit; no
// business logic lives here.

package api

import (
	"slices"

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

func formatPtr(id *uint64) *string {
	if id == nil {
		return nil
	}
	s := formatID(*id)
	return &s
}

func fromProfileSlot(s internal.ProfileSlot) ProfileSlot {
	return ProfileSlot{
		Name:         s.Name,
		Role:         s.Role,
		Personality:  s.Personality,
		EmotionState: s.EmotionState,
		MBTI:         s.MBTI,
		Preferences:  s.Preferences,
		AgentType:    s.AgentType,
		UpdatedAtMs:  s.UpdatedAtMs,
	}
}

// toCoreProfileSlot maps the host-writable half of the profile. EmotionState,
// MBTI and UpdatedAtMs are read-only echoes (Dream evolves the first two, the
// library stamps the last), and AgentType is stamped once when the domain is
// created, so none of the four is carried inbound.
func toCoreProfileSlot(s *ProfileSlot) internal.ProfileSlot {
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
		ChildrenIDs:    formatIDs(t.ChildrenIDs),
		Depth:          t.Depth,
		Name:           t.Name,
		FusedKeywords:  slices.Clone(t.FusedKeywords),
		UserTimestamp:  t.UserTimestamp,
		AgentTimestamp: t.AgentTimestamp,
	}
}

func fromSearchResult(r *internal.SearchResult) *SearchResult {
	if r == nil {
		return nil
	}
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

func fromHypergraphSource(s internal.HypergraphSource) HypergraphSource {
	return HypergraphSource{
		Kind:      s.Kind.String(),
		Value:     s.Value,
		ContextID: formatOptionalID(s.ContextID),
	}
}

func fromHypergraphSlot(s internal.HypergraphSlot) HypergraphSlot {
	return HypergraphSlot{
		IDHash:    formatID(s.IDHash),
		Name:      s.Name,
		Source:    fromHypergraphSource(s.Source),
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
		Keywords:  slices.Clone(n.Keywords),
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
	if g == nil {
		return nil
	}
	nodes, edges := mapL3Members(g.Nodes, g.Edges)
	return &L3Graph{
		Slot:  fromHypergraphSlot(g.Slot),
		Nodes: nodes,
		Edges: edges,
	}
}

func fromL3Subgraph(g *internal.L3Subgraph) *L3Subgraph {
	if g == nil {
		return nil
	}
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
	if t == nil {
		return PlanTree{}
	}
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
		ChildCount: v.ChildCount,
		Children:   make([]PlanNodeView, 0, len(v.Children)),
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
