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

func formatPtr(id *uint64) *string {
	if id == nil {
		return nil
	}
	s := formatID(*id)
	return &s
}

func parsePtr(s *string) (*uint64, error) {
	if s == nil {
		return nil, nil
	}
	id, err := internal.ParseID(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func fromProfileSlot(s internal.ProfileSlot) ProfileSlot {
	return ProfileSlot{
		Name:         s.Name,
		Role:         s.Role,
		Personality:  s.Personality,
		EmotionState: s.EmotionState,
		MBTI:         s.MBTI,
		Preferences:  s.Preferences,
		UpdatedAtMs:  s.UpdatedAtMs,
	}
}

func toCoreProfileSlot(s *ProfileSlot) internal.ProfileSlot {
	if s == nil {
		return internal.ProfileSlot{}
	}
	return internal.ProfileSlot{
		Name:         s.Name,
		Role:         s.Role,
		Personality:  s.Personality,
		EmotionState: s.EmotionState,
		MBTI:         s.MBTI,
		Preferences:  s.Preferences,
		UpdatedAtMs:  s.UpdatedAtMs,
	}
}

func fromSceneSlot(s internal.SceneSlot) SceneSlot {
	return SceneSlot{
		SceneID:    formatID(s.SceneID),
		SceneName:  s.SceneName,
		TopicCount: s.TopicCount,
		HitCount:   s.HitCount,
		LastHitAt:  s.LastHitAt,
		L3ID:       formatOptionalID(s.L3ID),
	}
}

func fromTopicSlot(t internal.TopicSlot) TopicSlot {
	return TopicSlot{
		ID:              formatID(t.ID),
		SceneID:         formatID(t.SceneID),
		ParentID:        formatPtr(t.ParentID),
		ChildrenIDs:     formatIDs(t.ChildrenIDs),
		Depth:           t.Depth,
		UserKeywords:    cloneStrings(t.UserKeywords),
		UserTimestamp:   t.UserTimestamp,
		L4Refs:          formatIDs(t.L4Refs),
		L3Refs:          formatIDs(t.L3Refs),
		AgentKeywords:   cloneStrings(t.AgentKeywords),
		AgentTimestamp:  t.AgentTimestamp,
		FusedKeywords:   cloneStrings(t.FusedKeywords),
		CentroidPageRef: formatOptionalID(t.CentroidPageRef),
	}
}

func fromSearchResult(r *internal.SearchResult) *SearchResult {
	if r == nil {
		return nil
	}
	contexts := make([]TopicSlot, len(r.Contexts))
	for i, t := range r.Contexts {
		contexts[i] = fromTopicSlot(t)
	}
	associated := make([]TopicSlot, len(r.AssociatedContexts))
	for i, t := range r.AssociatedContexts {
		associated[i] = fromTopicSlot(t)
	}
	return &SearchResult{
		Profile:            fromProfileSlot(r.Profile),
		ProfileBrief:       r.ProfileBrief,
		Contexts:           contexts,
		AssociatedContexts: associated,
		NewTopicID:         formatID(r.NewTopicID),
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
		IDHash:     formatID(n.IDHash),
		GraphID:    formatID(n.GraphID),
		Title:      n.Title,
		NodeType:   n.NodeType,
		Content:    n.Content,
		Keywords:   cloneStrings(n.Keywords),
		SourceRef:  n.SourceRef,
		Importance: n.Importance,
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
	}
}

func fromHypergraphEdge(e internal.HypergraphEdge) HypergraphEdge {
	return HypergraphEdge{
		IDHash:    formatID(e.IDHash),
		GraphID:   formatID(e.GraphID),
		Kind:      e.Kind,
		NodeIDs:   formatIDs(e.NodeIDs),
		Weight:    e.Weight,
		Label:     e.Label,
		CreatedAt: e.CreatedAt,
	}
}

func fromL3Graph(g *internal.L3Graph) *L3Graph {
	if g == nil {
		return nil
	}
	nodes := make([]HypergraphNode, len(g.Nodes))
	for i, n := range g.Nodes {
		nodes[i] = fromHypergraphNode(n)
	}
	edges := make([]HypergraphEdge, len(g.Edges))
	for i, e := range g.Edges {
		edges[i] = fromHypergraphEdge(e)
	}
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
	nodes := make([]HypergraphNode, len(g.Nodes))
	for i, n := range g.Nodes {
		nodes[i] = fromHypergraphNode(n)
	}
	edges := make([]HypergraphEdge, len(g.Edges))
	for i, e := range g.Edges {
		edges[i] = fromHypergraphEdge(e)
	}
	return &L3Subgraph{Nodes: nodes, Edges: edges}
}

func fromArchiveSlot(s internal.ArchiveSlot) ArchiveSlot {
	return ArchiveSlot{
		IDHash:      formatID(s.IDHash),
		ContentType: s.ContentType,
		Role:        s.Role,
		ContextID:   formatOptionalID(s.ContextID),
		CreatedAt:   s.CreatedAt,
		Content:     s.Content,
		Metadata:    s.Metadata,
	}
}

func fromCapability(c internal.Capability) Capability {
	resources := make([]ResourceRef, len(c.Resources))
	copy(resources, c.Resources)
	return Capability{
		IDHash:        formatID(c.IDHash),
		Name:          c.Name,
		Version:       c.Version,
		Type:          c.Type,
		Summary:       c.Summary,
		Trigger:       c.Trigger,
		Resources:     resources,
		Workflow:      c.Workflow,
		Status:        c.Status,
		Origin:        c.Origin,
		FileHash:      c.FileHash,
		SuccessRate:   c.SuccessRate,
		TriggerCount:  c.TriggerCount,
		LastTriggered: c.LastTriggered,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	}
}

func formatOptionalID(id uint64) string {
	if id == 0 {
		return ""
	}
	return formatID(id)
}

func parseOptionalID(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}
	return internal.ParseID(s)
}

func fromTrajectorySlot(s internal.TrajectorySlot) TrajectorySlot {
	return TrajectorySlot{
		IDHash:      formatID(s.IDHash),
		SessionID:   formatID(s.SessionID),
		Seq:         s.Seq,
		EventType:   s.EventType,
		Payload:     s.Payload,
		TopicID:     formatOptionalID(s.TopicID),
		Timestamp:   s.Timestamp,
		NodeType:    s.NodeType,
		PlanID:      formatOptionalID(s.PlanID),
		ParentID:    formatOptionalID(s.ParentID),
		NodePath:    s.NodePath,
		Status:      s.Status,
		Summary:     s.Summary,
		PlanType:    s.PlanType,
		PlanNodeRef: formatOptionalID(s.PlanNodeRef),
		FinishedAt:  s.FinishedAt,
	}
}

func toCoreTrajectorySlot(s TrajectorySlot) (internal.TrajectorySlot, error) {
	topicID, err := parseOptionalID(s.TopicID)
	if err != nil {
		return internal.TrajectorySlot{}, err
	}
	planID, err := parseOptionalID(s.PlanID)
	if err != nil {
		return internal.TrajectorySlot{}, err
	}
	parentID, err := parseOptionalID(s.ParentID)
	if err != nil {
		return internal.TrajectorySlot{}, err
	}
	planNodeRef, err := parseOptionalID(s.PlanNodeRef)
	if err != nil {
		return internal.TrajectorySlot{}, err
	}
	return internal.TrajectorySlot{
		EventType:   s.EventType,
		Payload:     s.Payload,
		TopicID:     topicID,
		Timestamp:   s.Timestamp,
		NodeType:    s.NodeType,
		PlanID:      planID,
		ParentID:    parentID,
		NodePath:    s.NodePath,
		Status:      s.Status,
		Summary:     s.Summary,
		PlanType:    s.PlanType,
		PlanNodeRef: planNodeRef,
		FinishedAt:  s.FinishedAt,
	}, nil
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
		NodePath: v.NodePath, Title: v.Title, Status: string(v.Status),
		Type: v.Type, Summary: v.Summary, FinishedAt: v.FinishedAt,
		ChildCount: v.ChildCount, TrajCount: v.TrajCount,
		Children: make([]PlanNodeView, 0, len(v.Children)),
	}
	for _, c := range v.Children {
		out.Children = append(out.Children, fromPlanNodeView(c))
	}
	return out
}

func toInternalPlanNode(root *PlanNode) internal.PlanNode {
	if root == nil {
		return internal.PlanNode{}
	}
	children := make([]internal.PlanNode, 0, len(root.Children))
	for i := range root.Children {
		children = append(children, toInternalPlanNode(&root.Children[i]))
	}
	return internal.PlanNode{
		NodePath: root.NodePath, Title: root.Title, PlanType: root.Type,
		Status: internal.PlanStatus(root.Status), Summary: root.Summary,
		Children: children,
	}
}

func fromPlanSummary(s internal.PlanSummary) PlanSummary {
	return PlanSummary{
		PlanID: s.PlanID, CreatedAt: s.CreatedAt, LastActiveAt: s.LastActiveAt,
		NodeCount: s.NodeCount, DoneCount: s.DoneCount, TotalCount: s.TotalCount,
		Active: s.Active,
	}
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
