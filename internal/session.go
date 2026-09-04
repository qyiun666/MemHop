// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Per-agent session handle: binds every operation to one agent domain and
// renders the external hex-id surface so the api facade stays pure
// forwarding. The public method set of api.Session is exactly this type's
// method set; the domain lock is still taken per call by the underlying DB
// methods. File-level lifecycle (Checkpoint/Close/IsClosed) is not repeated
// here — it belongs to the DB handle the host opened.

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

// ---- scene read / turn write ----

// Search reads one scene (the host's session): its record and its depth-1
// topics, whose count the result reports as TopicCount. An empty
// SearchQuery.SceneID allocates a fresh scene. The result also carries the
// topic id this read opened for the turn the host is about to run.
func (s *Session) Search(q SearchQuery) (*SearchResult, error) {
	return s.db.Search(s.agentID, q)
}

// Update settles one finished turn into the topic id Search issued for it and
// returns that id.
func (s *Session) Update(in TurnUpdate) (uint64, error) {
	return s.db.Update(s.agentID, in)
}

// ---- Dream ----

// Dream runs the consolidation pipeline over the given scene (or every scene
// of the domain when sceneID is empty); RunDream takes the domain lock itself
// and errors when the named scene does not exist.
func (s *Session) Dream(ctx context.Context, sceneID string) (*DreamReport, error) {
	var hash uint64
	if sceneID != "" {
		var err error
		hash, err = common.ParseID(sceneID)
		if err != nil {
			return nil, common.NewError(common.ErrInvalidQuery, "parse scene id", err)
		}
	}
	return s.db.RunDream(ctx, s.agentID, hash)
}

// ---- L0 profile ----

func (s *Session) GetL0() (*ProfileSlot, error) {
	return s.db.GetL0(s.agentID)
}

func (s *Session) UpdateL0(slot *ProfileSlot) error {
	return s.db.UpdateL0(s.agentID, slot)
}

// ---- L2 scenes/topics ----

// ListScenes lists the domain's scenes; a non-empty l3ID keeps only the
// scenes anchored to that L3 project domain.
func (s *Session) ListScenes(l3ID string) ([]SceneSlot, error) {
	return s.db.ListScenes(s.agentID, l3ID)
}

// UpdateScene patches a scene's host-facing metadata (title, L3 anchor);
// nil fields stay unchanged. The written scene comes back, so a host confirms
// an anchor without listing the domain.
func (s *Session) UpdateScene(sceneID string, patch ScenePatch) (SceneSlot, error) {
	return s.db.UpdateScene(s.agentID, sceneID, patch)
}

func (s *Session) SceneContext(sceneID string) (*SceneContext, error) {
	return s.db.SceneContext(s.agentID, sceneID)
}

func (s *Session) MergeScenes(primaryID string, secondaryIDs []string) error {
	return s.db.MergeScenes(s.agentID, primaryID, secondaryIDs)
}

// DeleteTopic removes a topic and its whole subtree (children at any
// depth), the L4 archives they reference, and their L2Meta cache entries,
// so the deleted topic no longer surfaces in any scene read.
func (s *Session) DeleteTopic(topicID string) error {
	return s.db.DeleteTopic(s.agentID, topicID)
}

// DeleteScene removes a scene: its scene record, every topic (all depths),
// the referenced L4 archives, and the L2Meta cache entries, so the scene
// disappears from listings and reads.
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

// DeleteL3Nodes removes nodes from one graph and cascades the hyperedges that
// touch them, so a wrong node can be corrected without rebuilding the graph.
// Every id must name a node of this graph; an unknown or foreign id is refused
// and nothing is deleted.
func (s *Session) DeleteL3Nodes(graphID string, nodeIDs []string) error {
	return s.db.DeleteL3Nodes(s.agentID, graphID, nodeIDs)
}

func (s *Session) QueryL3Nodes(q L3NodeQuery) ([]HypergraphNode, error) {
	return s.db.QueryL3Nodes(s.agentID, q)
}

func (s *Session) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []GraphEdgeKind) (*L3Subgraph, error) {
	return s.db.QueryL3Subgraph(s.agentID, graphID, startNodeID, maxDepth, edgeKinds)
}

// ---- L4 archive ----

// SearchL4 reads archives by any combination of filters; they AND together,
// so L4Query{TopicID: &turnID} returns exactly that turn's originals.
func (s *Session) SearchL4(q L4Query) ([]ArchiveSlot, error) {
	return s.db.SearchL4(s.agentID, q)
}

// ---- L5 capabilities ----

func (s *Session) ImportCapability(path string) (*Capability, error) {
	return s.db.ImportCapability(s.agentID, path)
}

func (s *Session) DeleteCapability(id string) error {
	return s.db.DeleteCapability(s.agentID, id)
}

func (s *Session) UpdateCapability(id string, patch CapabilityPatch) (*Capability, error) {
	return s.db.UpdateCapability(s.agentID, id, patch)
}

// ListCapabilities filters the L5 catalog; CapabilityListQuery.IDs selects a
// single card.
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

// AppendTrajectory appends one event under `key`: a turn's topic id for a
// bare turn event, or a plan id together with the nodePath to bind the event
// to that plan node.
func (s *Session) AppendTrajectory(key, nodePath string, ev TrajectorySlot) error {
	return s.db.AppendTrajectory(s.agentID, key, nodePath, ev)
}

// ReadTrajectory returns one turn's events; turnID is the topic id Search
// minted for it. Plan-bound events are keyed by their plan id, so a planID
// works here too.
func (s *Session) ReadTrajectory(turnID string) ([]TrajectorySlot, error) {
	return s.db.ReadTrajectory(s.agentID, turnID)
}

// ListTrajectorySessions summarizes every key of the domain's L6 log (turn
// topic ids and plan ids) with its step count and last-append time; the
// returned hex ids feed ReadTrajectory / Crystallize directly. Events older
// than trajectoryRetention are dropped by Dream automatically.
func (s *Session) ListTrajectorySessions() ([]TrajectorySessionSummary, error) {
	return s.db.ListTrajectorySessions(s.agentID)
}

// Crystallize turns one key's events into L5 capability drafts; pass a turn's
// topic id for a single turn, or a plan id to aggregate the whole plan.
func (s *Session) Crystallize(ctx context.Context, turnID string) (*CrystallizeResult, error) {
	return s.db.Crystallize(ctx, s.agentID, turnID)
}

// ---- L6 plan (tri-form) ----

// PlanCommit advances a plan node to a status and appends the step event.
func (s *Session) PlanCommit(planID, nodePath string, ev TrajectorySlot, status PlanStatus, summary string) error {
	return s.db.PlanCommit(s.agentID, planID, nodePath, ev, status, summary)
}

// PlanState returns the plan tree view.
func (s *Session) PlanState(planID string) (*PlanTree, error) {
	return s.db.PlanState(s.agentID, planID)
}

// PlanReplace wipes a plan's nodes and bound events for re-planning,
// keeping the planID; a non-empty rootTitle seeds a titled pending root.
func (s *Session) PlanReplace(planID, rootTitle string) error {
	return s.db.PlanReplace(s.agentID, planID, rootTitle)
}

// SyncPlanTree replaces one plan's whole tree from the host's authoritative
// snapshot: adds/updates nodes by path, deletes vanished nodes (with their
// bound events) and never appends a plan_step event.
func (s *Session) SyncPlanTree(planID string, root *PlanNode) error {
	return s.db.SyncPlanTree(s.agentID, planID, root)
}
