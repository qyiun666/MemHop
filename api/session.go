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

// ListScenes returns scenes with hex IDs; a non-empty l3ID keeps only the
// scenes anchored to that L3 project domain.
func (s *Session) ListScenes(l3ID string) ([]SceneSlot, error) {
	scenes, err := s.Session.ListScenes(l3ID)
	if err != nil {
		return nil, err
	}
	out := make([]SceneSlot, len(scenes))
	for i, sc := range scenes {
		out[i] = fromSceneSlot(sc)
	}
	return out, nil
}

// UpdateScene patches one scene's host-facing metadata (title, L3 anchor) and
// returns the scene as stored afterwards, so a host confirms an anchor without
// listing the domain. Nil patch fields keep their stored value.
func (s *Session) UpdateScene(sceneID string, patch ScenePatch) (SceneSlot, error) {
	slot, err := s.Session.UpdateScene(sceneID, patch)
	if err != nil {
		return SceneSlot{}, err
	}
	return fromSceneSlot(slot), nil
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

// UpdateL3 renames a graph and returns it with hex IDs. The new label has to be
// free: a domain label is how ImportL3 addresses a graph, so renaming onto a
// label another graph already carries is refused with ErrInvalidQuery instead of
// leaving that domain ambiguous. Renaming onto the name the graph already has is
// a no-op that succeeds.
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

// AppendTrajectory writes one event under key: a turn's topic id (Search
// mints it) with an empty nodePath, or a plan id with the dotted nodePath of
// the plan node the event binds to ("1", "1.2"; a missing node is created
// pending).
//
// The log is append-only and per-key: nothing returns or takes an event id,
// because no public call consumes one — ReadTrajectory(key) gives the events
// back in Seq order, and Dream drops ones past the retention window. Of the
// event you pass, EventType, Payload, Timestamp, FinishedAt and — for a
// plan-bound event — TopicID are stored. Seq, PlanID and PlanNodeRef are
// assigned by the library, and so is the record's NodePath: a plan-bound event
// is stamped with the step it landed on, which is how a host attributes an
// event to a step afterwards. On a bare turn event TopicID comes from the key
// — it cannot disagree with it — while a plan-bound event keeps the TopicID
// named for the turn it happened in.
//
// A Payload over the 4 KiB budget is refused, not truncated: a shortened event
// would read back exactly like a complete one. Nothing is written when this
// call returns an error.
//
// A plan-bound event (non-empty nodePath) is validated against the plan step
// vocabulary: plan_step, llm_request, llm_output, tool_call, tool_result,
// subagent_spawn, subagent_done, context_inject, ask_user, user_reply. Anything
// else is ErrInvalidQuery; a bare turn event takes any EventType the host
// names.
func (s *Session) AppendTrajectory(key, nodePath string, ev TrajectorySlot) error {
	coreEv, err := toCoreTrajectorySlot(ev)
	if err != nil {
		return err
	}
	return s.Session.AppendTrajectory(key, nodePath, coreEv)
}

// PlanCommit advances a plan node to a status, appends the step event and rolls
// Done children's summaries up into their parent. status takes the PlanStatus*
// string constants ("pending" / "in_progress" / "running" / "done" / "failed");
// an unknown value is rejected. nodePath is the dotted path the host assigned
// with SyncPlanTree. Like the node-bound AppendTrajectory, the event is forced
// to bare-event semantics and its EventType must come from the plan vocabulary
// above; summary is the node's own conclusion, kept when a later sync leaves it
// blank.
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

// SyncPlanTree replaces one plan's whole tree from the host's authoritative
// snapshot: adds/updates nodes by path, deletes vanished nodes (with their
// bound events) and never appends a plan_step event. planID is preserved.
func (s *Session) SyncPlanTree(planID string, root *PlanNode) error {
	in := toInternalPlanNode(root)
	return s.Session.SyncPlanTree(planID, &in)
}

// ---- Promoted surface, documented ----
//
// The methods below need no DTO mapping, so embedding alone already makes them
// callable. They are declared here because internal is not a published package:
// a host reading `go doc github.com/qyiun666/MemHop/api.Session` would
// otherwise not see them at all, and these are the calls with the contracts a
// host has to get right (locking, LLM cost, cascade scope). Each body is a
// forwarding declaration that exists for its doc comment;
// api/surface_public_test.go pins the resulting method set.

// Dream runs the consolidation pass — the sleep analogue: it fuses and compresses
// topics (L2), rebuilds and decays the L1 graph, distills the L0 profile, and
// prunes trajectory events past their retention window. Pass a scene id to
// consolidate one scene, or "" for every scene of the domain. It is the only
// path that prunes L6 events and rebuilds L1, so a domain that stops being
// written to still needs one Dream to shrink.
//
// It contacts the LLM and runs inside the domain lock: while it works, every
// other call on this agent domain waits. The report counts what this pass did;
// on a mid-pipeline failure the partially filled report comes back with the
// error.
func (s *Session) Dream(ctx context.Context, sceneID string) (*DreamReport, error) {
	return s.Session.Dream(ctx, sceneID)
}

// SceneContext reads a scene's whole transcript without opening a turn: unlike
// Search it writes nothing — no turn id is minted, HitCount and LastHitAt stay
// alone — so it is the read for showing or exporting a conversation.
//
// It returns more than Search does, on purpose: a Dream-fused group keeps its
// originals on the sunk child topics, and SceneContext is the only read that
// flattens them back in (entries with Depth 2, reachable up to two levels).
// Entries come in speaking order, each carrying its own L4 messages and
// ChildCount, so a fused parent (whose message is Dream's summary) can be told
// apart from the turns it grouped. TopicCount counts every entry returned —
// SceneSlot.TopicCount from Search/ListScenes counts only the depth-1 roots.
func (s *Session) SceneContext(sceneID string) (*SceneContext, error) {
	return s.Session.SceneContext(sceneID)
}

// MergeScenes folds scenes together: every topic of each secondary scene is
// retargeted to the primary scene, then the secondary scene records are
// deleted. Use it when a host resumed one conversation under a new session id.
// The primary's name and anchor win, and nothing comes back — re-read the
// primary to see the merged history.
func (s *Session) MergeScenes(primaryID string, secondaryIDs []string) error {
	return s.Session.MergeScenes(primaryID, secondaryIDs)
}

// DeleteScene removes a scene for good: its record, every topic at any depth,
// the L4 originals they reference and the L1 scene node, so it disappears from
// listings and reads. Hyperedges that incidentally pointed at it are cleaned by
// the next Dream's L1 rebuild.
func (s *Session) DeleteScene(sceneID string) error {
	return s.Session.DeleteScene(sceneID)
}

// DeleteTopic removes one topic and its whole subtree (children at any depth)
// with the L4 originals they reference, and prunes it from its surviving
// parent's child list — the memory-correction counterpart of Update. Deleting a
// topic that does not exist is an error, not a no-op.
func (s *Session) DeleteTopic(topicID string) error {
	return s.Session.DeleteTopic(topicID)
}

// ImportL3 batch-imports knowledge nodes into one graph per Domain: a domain
// name the graph already has extends it, a new one creates it. The mode is
// required — Skip leaves existing nodes alone, Merge appends, Overwrite
// replaces. Every item must name a Title and a Domain: a malformed batch is
// refused and writes nothing at all, so result.Errors is reserved for a
// per-item storage failure while the rest of the batch proceeds and the error
// return means the call did nothing.
//
// A relation may target an item later in the same batch (edges resolve in a
// second pass), and every item declares its edges — including one whose node
// was skipped, because edges are deduped by their members plus kind. The
// result reports the node ids created/updated, how many edges were created,
// and GraphIDs: the graphs this batch wrote into, which a host needs to anchor
// a scene on them (UpdateScene / SearchQuery.L3ID), since a graph id derives
// from the domain name and no other public call renders that derivation.
func (s *Session) ImportL3(items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	return s.Session.ImportL3(items, mode)
}

// DeleteL3 removes a whole graph: the slot plus all of its nodes and hyperedges.
// It also drops the L2 anchors that named the graph — a scene's L3ID is the only
// inbound reference a graph has, and both anchor write paths require the graph to
// exist, so no scene is left listing under a project domain nothing resolves to.
// Reach for DeleteL3Nodes when only part of the graph is wrong — this call takes
// the edges bound to every node in it, including the correct ones.
func (s *Session) DeleteL3(id string) error {
	return s.Session.DeleteL3(id)
}

// DeleteL3Nodes removes specific nodes from one graph, cascading every hyperedge
// that touches them, so correcting a knowledge node does not mean rebuilding the
// graph. Every id must name a node of that graph; an unknown or foreign id is
// refused and nothing is deleted.
func (s *Session) DeleteL3Nodes(graphID string, nodeIDs []string) error {
	return s.Session.DeleteL3Nodes(graphID, nodeIDs)
}

// DeleteCapability removes an L5 card. Cards the engine ships (Origin builtin)
// are the manual the host reads, not host data, so they are read-only and
// deleting one is refused. Deleting a card that is not there is an error, not
// a no-op.
func (s *Session) DeleteCapability(id string) error {
	return s.Session.DeleteCapability(id)
}

// ListTrajectorySessions summarizes every L6 key of the domain — turn topic ids
// and plan ids — with its step count and last-append time. The returned ids feed
// ReadTrajectory and Crystallize directly; events past the retention window drop
// out at the next Dream.
func (s *Session) ListTrajectorySessions() ([]TrajectorySessionSummary, error) {
	return s.Session.ListTrajectorySessions()
}

// Crystallize turns one key's trajectory events into L5 capability cards: pass a
// turn's topic id to work off a single turn, or a plan id to aggregate the whole
// plan. It contacts the LLM inside the domain lock. New cards land in the draft
// status — they are listed, but a host that only wires up active cards needs
// ActivateCapability before one becomes usable.
func (s *Session) Crystallize(ctx context.Context, turnID string) (*CrystallizeResult, error) {
	return s.Session.Crystallize(ctx, turnID)
}

// PlanReplace wipes a plan — its nodes and the events bound to them — and keeps
// the planID, so a host starts an unrelated task on the id it already holds
// without the new nodes landing on the old tree by path. Pass a rootTitle to
// seed one titled pending root, or "" for an empty plan.
func (s *Session) PlanReplace(planID, rootTitle string) error {
	return s.Session.PlanReplace(planID, rootTitle)
}
