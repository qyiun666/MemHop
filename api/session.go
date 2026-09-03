// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Session is the only business handle of the public facade: it embeds the
// internal domain-bound session (internal.Session), so the promoted method
// set is exactly the externally callable surface. Every call is serialized
// per agent domain by the internal domain lock.

package api

import (
	"github.com/qyiun666/MemHop/internal"
)

// Session binds every call to one agent domain.
type Session struct {
	*internal.Session
}

// AgentID returns the bound domain ID as a 16-char hex string.
func (s *Session) AgentID() string { return internal.FormatAgentID(s.Session.AgentID()) }

// Search reads one scene — the host's session: its record plus its depth-1
// topics in turn order, and the topic id this read opened for the turn the
// host is about to run. An empty SearchQuery.SceneID allocates a fresh scene.
func (s *Session) Search(q SearchQuery) (*SearchResult, error) {
	res, err := s.Session.Search(q)
	if err != nil {
		return nil, err
	}
	return fromSearchResult(res), nil
}

// Update settles one finished turn (both originals plus their timestamps)
// into the topic id Search issued for it, and returns that id.
func (s *Session) Update(in TurnUpdate) (string, error) {
	id, err := s.Session.Update(in)
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

// UpdateL0 writes the host-owned profile fields (Name / Role / Personality /
// Preferences). The two fields Dream evolves — EmotionState and MBTI — are kept
// from the stored profile, and UpdatedAtMs is stamped by the library, so a
// profile edit never wipes the distilled half.
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

// ReadTrajectory returns one turn's trajectory events with hex IDs. turnID is
// the topic id Search minted for that turn; plan-bound events are keyed by
// their plan id, so a planID works here too.
func (s *Session) ReadTrajectory(turnID string) ([]TrajectorySlot, error) {
	events, err := s.Session.ReadTrajectory(turnID)
	if err != nil {
		return nil, err
	}
	out := make([]TrajectorySlot, len(events))
	for i, e := range events {
		out[i] = fromTrajectorySlot(e)
	}
	return out, nil
}

// AppendTrajectory writes one event of the turn Search opened, keyed by that
// turn's topic id, and stamps the record's topic_id with the same value.
func (s *Session) AppendTrajectory(turnID string, ev TrajectorySlot) error {
	coreEv, err := toCoreTrajectorySlot(ev)
	if err != nil {
		return err
	}
	return s.Session.AppendTrajectory(turnID, coreEv)
}

// PlanAppend appends one event to a plan node (hex planID, nodePath). The
// record is forced to bare-event semantics: NodeType/ParentID/NodePath/
// Status/Summary are cleared and PlanID/PlanNodeRef/Seq come from this call,
// so caller-supplied values in those fields are ignored.
func (s *Session) PlanAppend(planID, nodePath string, ev TrajectorySlot) error {
	coreEv, err := toCoreTrajectorySlot(ev)
	if err != nil {
		return err
	}
	return s.Session.PlanAppend(planID, nodePath, coreEv)
}

// PlanCommit advances a plan node to a status and appends the step event.
// status takes the PlanStatus* string constants ("pending" / "in_progress" /
// "running" / "done" / "failed"); an unknown value is rejected. The appended
// event is forced to bare-event semantics as in PlanAppend.
func (s *Session) PlanCommit(planID, nodePath string, ev TrajectorySlot, status string, summary string) error {
	coreEv, err := toCoreTrajectorySlot(ev)
	if err != nil {
		return err
	}
	return s.Session.PlanCommit(planID, nodePath, coreEv, internal.PlanStatus(status), summary)
}

// PlanState returns the plan forest with hex-free string statuses.
func (s *Session) PlanState(planID string) (*PlanTree, error) {
	t, err := s.Session.PlanState(planID)
	if err != nil {
		return nil, err
	}
	out := fromPlanTree(t)
	return &out, nil
}

// ListPlans summarizes every plan of the agent domain (restart recovery).
func (s *Session) ListPlans() ([]PlanSummary, error) {
	plans, err := s.Session.ListPlans()
	if err != nil {
		return nil, err
	}
	out := make([]PlanSummary, 0, len(plans))
	for _, p := range plans {
		out = append(out, fromPlanSummary(p))
	}
	return out, nil
}

// SyncPlanTree replaces one plan's whole tree from the host's authoritative
// snapshot: adds/updates nodes by path, deletes vanished nodes (with their
// bound events) and never appends a plan_step event. planID is preserved.
func (s *Session) SyncPlanTree(planID string, root *PlanNode) error {
	in := toInternalPlanNode(root)
	return s.Session.SyncPlanTree(planID, &in)
}
