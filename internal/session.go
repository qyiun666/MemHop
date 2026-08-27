// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Per-agent session handle: binds every operation to one agent domain and
// renders the external hex-id surface (ActiveSceneIDs) so the api facade
// stays pure forwarding. The public method set of api.Session is exactly
// this type's method set; the domain lock is still taken per call by the
// underlying DB methods.

package internal

import (
	"context"

	"github.com/qyiun666/MemHop/internal/common"
)

// Session binds every call to one agent domain.
type Session struct {
	db      *DB
	agentID uint64
}

// NewSession creates a session for agentID; the ID must be the default
// domain or a registered tenant (CheckSession is the admission gate).
func (db *DB) NewSession(agentID uint64) (*Session, error) {
	if err := db.CheckSession(agentID); err != nil {
		return nil, err
	}
	return &Session{db: db, agentID: agentID}, nil
}

// ---- lifecycle ----

// AgentID returns the bound domain ID (render externally via FormatAgentID).
func (s *Session) AgentID() uint64 { return s.agentID }

// Checkpoint persists the per-agent index snapshots (DB-level operation).
func (s *Session) Checkpoint() error { return s.db.Checkpoint() }

// IsClosed reports whether the underlying database has been closed.
func (s *Session) IsClosed() bool { return s.db.IsClosed() }

// HasActiveScenes reports whether this domain has active scenes.
func (s *Session) HasActiveScenes() bool { return s.db.HasActiveScenesFor(s.agentID) }

// ---- retrieval / write core ----

func (s *Session) Search(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	return s.db.Search(ctx, s.agentID, q)
}

func (s *Session) Update(topicID string, text string, timestamp int64) error {
	return s.db.Update(s.agentID, topicID, text, timestamp)
}

func (s *Session) AppendL4Message(topicID string, text string, timestamp int64, role uint8) (uint64, error) {
	return s.db.AppendL4Message(s.agentID, topicID, text, timestamp, role)
}

func (s *Session) RefineTopicKeywords(ctx context.Context, topicID string) error {
	return s.db.RefineTopicKeywords(ctx, s.agentID, topicID)
}

// ---- Dream ----

// Dream runs the consolidation pipeline over the given scene (or all active
// scenes when sceneID is empty); RunDream takes the domain lock itself and
// reports success without work when the domain has no active scenes.
func (s *Session) Dream(ctx context.Context, sceneID string) (bool, error) {
	return s.db.RunDream(ctx, s.agentID, sceneID)
}

// ---- L0 profile ----

func (s *Session) GetL0() (*ProfileSlot, error) {
	return s.db.GetL0(s.agentID)
}

func (s *Session) UpdateL0(slot *ProfileSlot) error {
	return s.db.UpdateL0(s.agentID, slot)
}

// ---- L2 scenes/topics ----

func (s *Session) ListScenes() ([]SceneSlot, error) {
	return s.db.ListScenes(s.agentID)
}

// ActiveSceneIDs returns the active scene IDs as 16-char hex strings,
// consistent with the hex ID parameters of SceneContext / MergeScenes /
// Search.DirectedL2ID.
func (s *Session) ActiveSceneIDs() []string {
	return common.FormatIDs(s.db.ActiveSceneIDs(s.agentID))
}

func (s *Session) SceneContext(sceneID string) (*SceneContext, error) {
	return s.db.SceneContext(s.agentID, sceneID)
}

func (s *Session) MergeScenes(primaryID string, secondaryIDs []string) error {
	return s.db.MergeScenes(s.agentID, primaryID, secondaryIDs)
}

// DeleteTopic removes a topic and its whole subtree (children at any
// depth), the L4 archives they reference, and their L2Meta/sparse entries,
// so the deleted topic no longer surfaces in retrieval.
func (s *Session) DeleteTopic(topicID string) error {
	return s.db.DeleteTopic(s.agentID, topicID)
}

// DeleteScene removes a scene: its scene record, every topic (all depths),
// the referenced L4 archives, and the L2Meta/sparse entries, so the scene
// disappears from listings and retrieval.
func (s *Session) DeleteScene(sceneID string) error {
	return s.db.DeleteScene(s.agentID, sceneID)
}

// ---- L3 hypergraphs ----

func (s *Session) GetL3(id string) (*L3Graph, error) {
	return s.db.GetL3(s.agentID, id)
}

func (s *Session) ListL3() ([]HypergraphSlot, error) {
	return s.db.ListL3(s.agentID)
}

func (s *Session) ImportL3(items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	return s.db.ImportL3(s.agentID, items, mode)
}

func (s *Session) UpdateL3(id string, name *string) (*L3Graph, error) {
	return s.db.UpdateL3(s.agentID, id, name)
}

func (s *Session) DeleteL3(id string) error {
	return s.db.DeleteL3(s.agentID, id)
}

func (s *Session) QueryL3Nodes(q L3NodeQuery) ([]HypergraphNode, error) {
	return s.db.QueryL3Nodes(s.agentID, q)
}

func (s *Session) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []GraphEdgeKind) (*L3Subgraph, error) {
	return s.db.QueryL3Subgraph(s.agentID, graphID, startNodeID, maxDepth, edgeKinds)
}

// ---- L4 archive ----

func (s *Session) SearchL4(q L4Query) ([]ArchiveSlot, error) {
	return s.db.SearchL4(s.agentID, q)
}

func (s *Session) GetArchive(id string) (*ArchiveSlot, error) {
	return s.db.GetArchive(s.agentID, id)
}

// ---- L5 capabilities ----

func (s *Session) GetCapability(id string) (*Capability, error) {
	return s.db.GetCapability(s.agentID, id)
}

func (s *Session) ImportCapability(path string) (*Capability, error) {
	return s.db.ImportCapability(s.agentID, path)
}

func (s *Session) DeleteCapability(id string) error {
	return s.db.DeleteCapability(s.agentID, id)
}

func (s *Session) UpdateCapability(id string, patch CapabilityPatch) (*Capability, error) {
	return s.db.UpdateCapability(s.agentID, id, patch)
}

func (s *Session) ListCapabilities(q CapabilityListQuery) ([]Capability, error) {
	return s.db.ListCapabilities(s.agentID, q)
}

func (s *Session) ActivateCapability(id string) (*Capability, error) {
	return s.db.ActivateCapability(s.agentID, id)
}

func (s *Session) RecordCapabilityUsage(id string, success bool) (*Capability, error) {
	return s.db.RecordCapabilityUsage(s.agentID, id, success)
}

// ---- L6 trajectory ----

func (s *Session) AppendTrajectory(sessionID string, ev TrajectorySlot) error {
	return s.db.AppendTrajectory(s.agentID, sessionID, ev)
}

func (s *Session) ReadTrajectory(sessionID string) ([]TrajectorySlot, error) {
	return s.db.ReadTrajectory(s.agentID, sessionID)
}

func (s *Session) TrajectoryStats(sessionID string) (*TrajectoryStats, error) {
	return s.db.TrajectoryStats(s.agentID, sessionID)
}

func (s *Session) DeleteTrajectory(sessionID string) error {
	return s.db.DeleteTrajectory(s.agentID, sessionID)
}

func (s *Session) Crystallize(ctx context.Context, sessionID string) (*CrystallizeResult, error) {
	return s.db.Crystallize(ctx, s.agentID, sessionID)
}
