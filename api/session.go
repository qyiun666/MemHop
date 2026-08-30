// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Session is the only business handle of the public facade: it embeds the
// internal domain-bound session (internal.Session), so the promoted method
// set is exactly the externally callable surface. Every call is serialized
// per agent domain by the internal domain lock.

package api

import (
	"context"

	"github.com/qyiun666/MemHop/internal"
)

// Session binds every call to one agent domain.
type Session struct {
	*internal.Session
}

// AgentID returns the bound domain ID as a 16-char hex string.
func (s *Session) AgentID() string { return internal.FormatAgentID(s.Session.AgentID()) }

// Search runs three-route retrieval and returns IDs as hex strings.
func (s *Session) Search(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	res, err := s.Session.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	return fromSearchResult(res), nil
}

// AppendL4Message returns the new L4 archive ID as a hex string.
func (s *Session) AppendL4Message(topicID string, text string, timestamp int64, role uint8, contentType ContentType) (string, error) {
	id, err := s.Session.AppendL4Message(topicID, text, timestamp, role, contentType)
	if err != nil {
		return "", err
	}
	return internal.FormatID(id), nil
}

// GetL0 returns the profile without the internal id_hash.
func (s *Session) GetL0() (*ProfileSlot, error) {
	slot, err := s.Session.GetL0()
	if err != nil {
		return nil, err
	}
	out := fromProfileSlot(*slot)
	return &out, nil
}

// UpdateL0 writes a public profile to the internal store.
func (s *Session) UpdateL0(slot *ProfileSlot) error {
	if slot == nil {
		return internal.NewError(internal.ErrInvalidQuery, "UpdateL0: slot is required")
	}
	coreSlot := toCoreProfileSlot(slot)
	return s.Session.UpdateL0(&coreSlot)
}

// ListScenes returns all scenes with hex IDs.
func (s *Session) ListScenes() ([]SceneSlot, error) {
	scenes, err := s.Session.ListScenes()
	if err != nil {
		return nil, err
	}
	out := make([]SceneSlot, len(scenes))
	for i, sc := range scenes {
		out[i] = fromSceneSlot(sc)
	}
	return out, nil
}

// ListScenesByL3 returns scenes anchored to an L3 domain with hex IDs.
func (s *Session) ListScenesByL3(l3ID string) ([]SceneSlot, error) {
	scenes, err := s.Session.ListScenesByL3(l3ID)
	if err != nil {
		return nil, err
	}
	out := make([]SceneSlot, len(scenes))
	for i, sc := range scenes {
		out[i] = fromSceneSlot(sc)
	}
	return out, nil
}

// GetL3 returns an L3 graph with hex IDs.
func (s *Session) GetL3(id string) (*L3Graph, error) {
	g, err := s.Session.GetL3(id)
	if err != nil {
		return nil, err
	}
	return fromL3Graph(g), nil
}

// ListL3 returns all hypergraph slots with hex IDs.
func (s *Session) ListL3() ([]HypergraphSlot, error) {
	graphs, err := s.Session.ListL3()
	if err != nil {
		return nil, err
	}
	out := make([]HypergraphSlot, len(graphs))
	for i, g := range graphs {
		out[i] = fromHypergraphSlot(g)
	}
	return out, nil
}

// UpdateL3 renames a graph and returns it with hex IDs.
func (s *Session) UpdateL3(id string, name *string) (*L3Graph, error) {
	g, err := s.Session.UpdateL3(id, name)
	if err != nil {
		return nil, err
	}
	return fromL3Graph(g), nil
}

// QueryL3Nodes returns nodes with hex IDs.
func (s *Session) QueryL3Nodes(q L3NodeQuery) ([]HypergraphNode, error) {
	nodes, err := s.Session.QueryL3Nodes(q)
	if err != nil {
		return nil, err
	}
	out := make([]HypergraphNode, len(nodes))
	for i, n := range nodes {
		out[i] = fromHypergraphNode(n)
	}
	return out, nil
}

// QueryL3Subgraph returns a subgraph with hex IDs.
func (s *Session) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []GraphEdgeKind) (*L3Subgraph, error) {
	sub, err := s.Session.QueryL3Subgraph(graphID, startNodeID, maxDepth, edgeKinds)
	if err != nil {
		return nil, err
	}
	return fromL3Subgraph(sub), nil
}

// SearchL4 returns archives with hex IDs.
func (s *Session) SearchL4(q L4Query) ([]ArchiveSlot, error) {
	archives, err := s.Session.SearchL4(q)
	if err != nil {
		return nil, err
	}
	out := make([]ArchiveSlot, len(archives))
	for i, a := range archives {
		out[i] = fromArchiveSlot(a)
	}
	return out, nil
}

// GetArchive returns one archive with hex IDs.
func (s *Session) GetArchive(id string) (*ArchiveSlot, error) {
	a, err := s.Session.GetArchive(id)
	if err != nil {
		return nil, err
	}
	out := fromArchiveSlot(*a)
	return &out, nil
}

// GetCapability returns a capability with a hex ID.
func (s *Session) GetCapability(id string) (*Capability, error) {
	c, err := s.Session.GetCapability(id)
	if err != nil {
		return nil, err
	}
	out := fromCapability(*c)
	return &out, nil
}

// ImportCapability imports a capability and returns it with a hex ID.
func (s *Session) ImportCapability(path string) (*Capability, error) {
	c, err := s.Session.ImportCapability(path)
	if err != nil {
		return nil, err
	}
	out := fromCapability(*c)
	return &out, nil
}

// UpdateCapability updates a capability and returns it with a hex ID.
func (s *Session) UpdateCapability(id string, patch CapabilityPatch) (*Capability, error) {
	c, err := s.Session.UpdateCapability(id, patch)
	if err != nil {
		return nil, err
	}
	out := fromCapability(*c)
	return &out, nil
}

// ActivateCapability activates a capability and returns it with a hex ID.
func (s *Session) ActivateCapability(id string) (*Capability, error) {
	c, err := s.Session.ActivateCapability(id)
	if err != nil {
		return nil, err
	}
	out := fromCapability(*c)
	return &out, nil
}

// RecordCapabilityUsage records usage and returns the capability with a hex ID.
func (s *Session) RecordCapabilityUsage(id string, success bool) (*Capability, error) {
	c, err := s.Session.RecordCapabilityUsage(id, success)
	if err != nil {
		return nil, err
	}
	out := fromCapability(*c)
	return &out, nil
}

// ListCapabilities returns capabilities with hex IDs.
func (s *Session) ListCapabilities(q CapabilityListQuery) ([]Capability, error) {
	caps, err := s.Session.ListCapabilities(q)
	if err != nil {
		return nil, err
	}
	out := make([]Capability, len(caps))
	for i, c := range caps {
		out[i] = fromCapability(c)
	}
	return out, nil
}

// ReadTrajectory returns trajectory events with hex IDs.
func (s *Session) ReadTrajectory(sessionID string) ([]TrajectorySlot, error) {
	events, err := s.Session.ReadTrajectory(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]TrajectorySlot, len(events))
	for i, e := range events {
		out[i] = fromTrajectorySlot(e)
	}
	return out, nil
}

// AppendTrajectory writes a public trajectory event, parsing hex refs.
func (s *Session) AppendTrajectory(sessionID string, ev TrajectorySlot) error {
	coreEv, err := toCoreTrajectorySlot(ev)
	if err != nil {
		return err
	}
	return s.Session.AppendTrajectory(sessionID, coreEv)
}

// PlanAppend appends one event to a plan node (hex planID, nodePath).
func (s *Session) PlanAppend(planID, nodePath string, ev TrajectorySlot) error {
	coreEv, err := toCoreTrajectorySlot(ev)
	if err != nil {
		return err
	}
	return s.Session.PlanAppend(planID, nodePath, coreEv)
}

// PlanCommit advances a plan node to a status and appends the step event.
func (s *Session) PlanCommit(planID, nodePath string, ev TrajectorySlot, status string, summary string) error {
	coreEv, err := toCoreTrajectorySlot(ev)
	if err != nil {
		return err
	}
	return s.Session.PlanCommit(planID, nodePath, coreEv, internal.PlanStatus(status), summary)
}

// PlanState returns the plan tree with hex-free string statuses.
func (s *Session) PlanState(planID string) (*PlanTree, error) {
	t, err := s.Session.PlanState(planID)
	if err != nil {
		return nil, err
	}
	out := fromPlanTree(t)
	return &out, nil
}
