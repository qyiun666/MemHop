// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Per-agent session handle of the multi-agent facade. The method set
// mirrors the single-agent DB surface, with every operation bound to the
// session's agentID; the internal layer takes the domain lock per call.

package api

import (
	"context"

	"github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
)

// AgentSession binds every call to one agent domain.
type AgentSession struct {
	db      *internal.DB
	agentID uint64
}

// AgentID returns the bound domain ID (render externally via FormatAgentID).
func (s *AgentSession) AgentID() uint64 { return s.agentID }

// Checkpoint persists the per-agent index snapshots (DB-level operation).
func (s *AgentSession) Checkpoint() error { return s.db.Checkpoint() }

// IsClosed reports whether the underlying database has been closed.
func (s *AgentSession) IsClosed() bool { return s.db.IsClosed() }

// HasActiveScenes reports whether this domain has active scenes.
func (s *AgentSession) HasActiveScenes() bool { return s.db.HasActiveScenesFor(s.agentID) }

// ---- retrieval / write core ----

func (s *AgentSession) Search(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	return s.db.Search(ctx, s.agentID, q)
}

func (s *AgentSession) Update(topicID string, text string, timestamp int64) error {
	return s.db.Update(s.agentID, topicID, text, timestamp)
}

func (s *AgentSession) RefineTopicKeywords(ctx context.Context, topicID string) error {
	return s.db.RefineTopicKeywords(ctx, s.agentID, topicID)
}

// ---- Dream ----

// Dream runs the consolidation pipeline over the given scene (or all active
// scenes when sceneID is empty); RunDream takes the domain lock itself.
func (s *AgentSession) Dream(ctx context.Context, sceneID string) (bool, error) {
	if s.db.IsClosed() {
		return false, common.NewError(common.ErrClosed, "database is closed")
	}
	if sceneID == "" && !s.db.HasActiveScenesFor(s.agentID) {
		return true, nil // no active scenes: nothing to do, succeed
	}
	return s.db.RunDream(ctx, s.agentID, sceneID)
}

// ---- L0 profile ----

func (s *AgentSession) GetL0() (*ProfileSlot, error) {
	return s.db.GetL0(s.agentID)
}

func (s *AgentSession) UpdateL0(slot *ProfileSlot) error {
	return s.db.UpdateL0(s.agentID, slot)
}

// ---- L2 scenes/topics ----

func (s *AgentSession) ListScenes() ([]SceneSlot, error) {
	return s.db.ListScenes(s.agentID)
}

func (s *AgentSession) ActiveSceneIDs() []string {
	ids := s.db.ActiveSceneIDs(s.agentID)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, common.FormatHash(id))
	}
	return out
}

func (s *AgentSession) SceneContext(sceneID string) (*SceneContext, error) {
	return s.db.SceneContext(s.agentID, sceneID)
}

func (s *AgentSession) MergeScenes(primaryID string, secondaryIDs []string) error {
	return s.db.MergeScenes(s.agentID, primaryID, secondaryIDs)
}

func (s *AgentSession) DeleteTopic(topicID string) error {
	return s.db.DeleteTopic(s.agentID, topicID)
}

func (s *AgentSession) DeleteScene(sceneID string) error {
	return s.db.DeleteScene(s.agentID, sceneID)
}

// ---- L3 hypergraphs ----

func (s *AgentSession) GetL3(id string) (*L3Graph, error) {
	return s.db.GetL3(s.agentID, id)
}

func (s *AgentSession) ListL3() ([]HypergraphSlot, error) {
	return s.db.ListL3(s.agentID)
}

func (s *AgentSession) ImportL3(items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	return s.db.ImportL3(s.agentID, items, mode)
}

func (s *AgentSession) UpdateL3(id string, name *string) (*L3Graph, error) {
	return s.db.UpdateL3(s.agentID, id, name)
}

func (s *AgentSession) DeleteL3(id string) error {
	return s.db.DeleteL3(s.agentID, id)
}

func (s *AgentSession) QueryL3Nodes(q L3NodeQuery) ([]HypergraphNode, error) {
	return s.db.QueryL3Nodes(s.agentID, q)
}

func (s *AgentSession) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []GraphEdgeKind) (*L3Subgraph, error) {
	return s.db.QueryL3Subgraph(s.agentID, graphID, startNodeID, maxDepth, edgeKinds)
}

// ---- L4 archive ----

func (s *AgentSession) AppendL4Message(topicID string, text string, timestamp int64, role uint8) (uint64, error) {
	return s.db.AppendL4Message(s.agentID, topicID, text, timestamp, role)
}

func (s *AgentSession) SearchL4(q L4Query) ([]ArchiveSlot, error) {
	return s.db.SearchL4(s.agentID, q)
}

func (s *AgentSession) GetArchive(id string) (*ArchiveSlot, error) {
	return s.db.GetArchive(s.agentID, id)
}

// ---- L5 capabilities ----

func (s *AgentSession) GetCapability(id string) (*Capability, error) {
	return s.db.GetCapability(s.agentID, id)
}

func (s *AgentSession) ImportCapability(path string) (*Capability, error) {
	return s.db.ImportCapability(s.agentID, path)
}

func (s *AgentSession) DeleteCapability(id string) error {
	return s.db.DeleteCapability(s.agentID, id)
}

func (s *AgentSession) UpdateCapability(id string, patch CapabilityPatch) (*Capability, error) {
	return s.db.UpdateCapability(s.agentID, id, patch)
}

func (s *AgentSession) ListCapabilities(q CapabilityListQuery) ([]Capability, error) {
	return s.db.ListCapabilities(s.agentID, q)
}

func (s *AgentSession) ActivateCapability(id string) (*Capability, error) {
	return s.db.ActivateCapability(s.agentID, id)
}

func (s *AgentSession) RecordCapabilityUsage(id string, success bool) (*Capability, error) {
	return s.db.RecordCapabilityUsage(s.agentID, id, success)
}

// ---- L6 trajectory ----

func (s *AgentSession) AppendTrajectory(sessionID string, ev TrajectorySlot) error {
	return s.db.AppendTrajectory(s.agentID, sessionID, ev)
}

func (s *AgentSession) ReadTrajectory(sessionID string) ([]TrajectorySlot, error) {
	return s.db.ReadTrajectory(s.agentID, sessionID)
}

func (s *AgentSession) TrajectoryStats(sessionID string) (*TrajectoryStats, error) {
	return s.db.TrajectoryStats(s.agentID, sessionID)
}

func (s *AgentSession) DeleteTrajectory(sessionID string) error {
	return s.db.DeleteTrajectory(s.agentID, sessionID)
}

func (s *AgentSession) Crystallize(ctx context.Context, sessionID string) (*CrystallizeResult, error) {
	return s.db.Crystallize(ctx, s.agentID, sessionID)
}
