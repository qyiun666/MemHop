// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Session is the only business handle of the public facade: it embeds the
// internal domain-bound session (internal.Session), so the promoted method
// set is exactly the externally callable surface. Every call is serialized
// per agent domain by the internal domain lock.
//
// The methods split by audience. The runtime/task face (19) is what the host
// drives every turn and what LLM tools bind to: Search, Update, Dream,
// AppendArchive (the host-driven loop), SceneContext, ListScenes, GetL0,
// UpdateL0, ListL1, SearchL4, GetL3, ListL3, ImportL3, QueryL3Nodes,
// QueryL3Subgraph, PlanCreate, PlanNodeAdd, PlanNodeUpdate, PlanState.
// The assembly/admin face (7, plus all of
// MultiAgentDB) is host code at session boundaries and management channels
// only — never an LLM tool: UpdateScene, RenameTopic, MergeScenes, DeleteTopic,
// DeleteScene, UpdateL3, DeleteL3.

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

// Update distills one finished turn into the keyword track of topicID, the id
// Search opened for it, and reads that topic's appended utterances as the turn's
// content: it writes no content of its own. One LLM call, inside the domain lock.
// A turn whose content the retention window already reclaimed is refused with
// ErrInvalidQuery instead of getting an empty track.
func (s *Session) Update(sceneID, topicID string) error {
	return s.Session.Update(sceneID, topicID)
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
// Preferences). The library-owned three are kept from the stored profile:
// EmotionState and MBTI, which Dream evolves, and AgentType, stamped once when
// the domain was created. UpdatedAtMs is stamped here. So a profile edit never
// wipes the distilled half, and never moves a domain between primary and sub.
func (s *Session) UpdateL0(slot *ProfileSlot) error {
	if slot == nil {
		return internal.NewError(internal.ErrInvalidQuery, "UpdateL0: slot is required")
	}
	coreSlot := toCoreProfileSlot(slot)
	return s.Session.UpdateL0(&coreSlot)
}

// ListL1 returns the domain's L1 scene nodes, every id rendered as hex and the
// order stable across calls. Read-only by design: Dream builds the nodes and the
// co-occurrence edges between them, decays both, and is the only writer — a host
// reads what consolidation decided and cannot set it. EdgeIDs have no read of
// their own; two nodes sharing one are a pair Dream judged related.
func (s *Session) ListL1() ([]SceneNodeView, error) {
	nodes, err := s.Session.ListL1()
	if err != nil {
		return nil, err
	}
	out := make([]SceneNodeView, len(nodes))
	for i, n := range nodes {
		out[i] = fromSceneNode(n)
	}
	return out, nil
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

// RenameTopic gives one topic the name the host chose and returns the topic as
// stored afterwards. The name is the host's alone — the engine derives nothing
// into it, so consolidating or merging a scene rewrites the record around the
// name and never over it. An empty name is refused: topics are created unnamed,
// so "" is the absence of a name rather than one. A topic that is not there is
// ErrNotFound, and nothing is created for it. The new name is visible to
// Search and SceneContext immediately, not at the next consolidation.
func (s *Session) RenameTopic(topicID, name string) (TopicSlot, error) {
	slot, err := s.Session.RenameTopic(topicID, name)
	if err != nil {
		return TopicSlot{}, err
	}
	return fromTopicSlot(slot), nil
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

// SearchL4 returns content records with hex IDs, ordered by Seq. L4 holds a
// turn's dialogue originals and its operation events alike, so Kind is a condition
// like any other: leaving it unset selects both.
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

// AppendArchive writes one piece of a turn's content under topicID — the id Search
// opened for it — and is the only way content enters a topic. KindUtterance is
// something somebody said; KindEvent is something that happened while they said it.
// Update distills the utterances of that key, its events come back from SearchL4
// under a Kind condition, and the plan tree sharing the key comes back from
// PlanState.
//
// What is stored is Kind, Seq, Role, ContentType, EventType, NodeSeq, Content and
// CreatedAt; IDHash and TopicID are ignored, which is what makes the round trip
// work — read a record back, change one field, write it to the slot it came from.
//
// Seq 0 allocates: the record lands one slot above everything the topic holds, and
// above Seq 1 and 2, which belong to dialogue — so the first event of a turn is
// Seq 3. A Seq you name is written as named, and taking a held slot overwrites it
// instead of erroring, across kinds: that is what lets a replayed turn converge
// rather than accumulate versions. Nothing reclaims a slot a replay stopped
// filling, so a withdrawn line stays until DeleteTopic or the retention window.
//
// Every rule below is refused before any record or plan node is touched. An event
// names itself with a non-empty EventType and has no speaker; an utterance declares
// Role (RoleUser / RoleAgent / RoleSystem) and ContentType and carries neither
// EventType nor NodeSeq. RoleDream is refused: it is the library's own mark on a
// consolidated summary, and a host that could write one makes that mark meaningless.
// Content over budget is refused, not truncated — 4 KiB per event, 64 KiB per
// utterance — because a shortened record reads back exactly like a complete one, and
// an unbounded utterance turns one distillation into an unbounded number of LLM
// calls holding the domain lock.
//
// NodeSeq attributes an event to one step of this turn's plan tree, and that step
// has to exist already: PlanCreate and PlanNodeAdd are the only writes that ever
// create a step, so an event naming an ordinal nobody created is refused rather
// than answered with a fresh branch, and a mistyped ordinal cannot open a second
// tree. 0 attributes the event to no step at all.
//
// EventType is the host's own word for the step: the engine never branches on it,
// it comes back verbatim through SearchL4.
// These conventions are a shared vocabulary for the reader, not an accepted set:
// plan_step, llm_request, llm_output, tool_call, tool_result, subagent_spawn,
// subagent_done, context_inject, ask_user, user_reply.
func (s *Session) AppendArchive(topicID string, slot ArchiveSlot) error {
	return s.Session.AppendArchive(topicID, toCoreAppendSlot(slot))
}

// PlanCreate opens a turn's plan tree by creating its first step, and returns the
// ordinal that step is addressed by from here on. topicID names the turn that
// opened the tree — the id Search handed out for it, which the host only ever
// passes back. A tree starts with no steps at all, so this is also how a plan
// first appears under a turn.
//
// The returned ordinal is the library's to hand out and the host's to keep: it is
// never derived from a title, and two steps of one turn never share one.
func (s *Session) PlanCreate(topicID string, title string) (uint32, error) {
	return s.Session.PlanCreate(topicID, title)
}

// PlanNodeAdd adds one step to a turn's plan tree and returns its ordinal.
// parentSeq 0 hangs the step at the top level, so this is also how a second root
// joins the forest; any other value must name a step this tree already holds —
// PlanNodeAdd under an unknown parent is refused rather than answered by growing
// one.
//
// This is the only way a step comes into existence. Nothing is written when either
// create call returns an error, and a step's title may be filled in later by
// PlanNodeUpdate, which is why an empty title here is allowed: the view falls back
// to the ordinal until the host names the step.
func (s *Session) PlanNodeAdd(topicID string, parentSeq uint32, title string) (uint32, error) {
	return s.Session.PlanNodeAdd(topicID, parentSeq, title)
}

// PlanNodeUpdate restates one step of a turn's plan tree: its status, and its own
// Title/Summary. Status always states where that step got to (there is no "leave
// it as it was" spelling), while a blank Title or Summary keeps what the node
// already holds — so updating a step never rewinds its title or erases a folded
// summary. Restating a settled step as in_progress re-opens it, and that drops its
// FinishedAt.
//
// An update the engine cannot honour is refused before the node is touched: a
// status outside in_progress / done / failed, or a step this turn never created,
// leaves the tree exactly as it was. A step that reaches a terminal status can
// settle its parent: once every direct child of a Done parent is itself terminal,
// the parent's Summary folds up from its children's. This call writes no content:
// the events a step produced are L4 records, appended with AppendArchive under the
// same topic id.
func (s *Session) PlanNodeUpdate(topicID string, step PlanStep) error {
	return s.Session.PlanNodeUpdate(topicID, toInternalPlanStep(step))
}

// PlanState returns the plan tree of one turn — keyed by the topic id that
// opened it — with hex-free string statuses.
func (s *Session) PlanState(topicID string) (*PlanTree, error) {
	t, err := s.Session.PlanState(topicID)
	if err != nil {
		return nil, err
	}
	out := fromPlanTree(t)
	return &out, nil
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
// prunes content (L4) and plan nodes (L5) past their retention window. Pass a
// scene id to consolidate one scene, or "" for every scene of the domain. It is
// the only path that prunes either layer or rebuilds L1, so a domain that stops
// being written to still needs one Dream to shrink.
//
// It contacts the LLM and runs inside the domain lock: while it works, every
// other call on this agent domain waits. The report counts what this pass did;
// on a mid-pipeline failure the partially filled report comes back with the
// error.
func (s *Session) Dream(ctx context.Context, sceneID string) (*DreamReport, error) {
	return s.Session.Dream(ctx, sceneID)
}

// SceneContext reads a scene's whole transcript without opening a turn: unlike
// Search it writes nothing — no turn id is minted and the scene's turn counter
// stays where it is — so it is the read for showing or exporting a conversation.
//
// It returns more than Search does, on purpose: a Dream-fused group keeps its
// originals on the sunk child topics, and SceneContext is the only read that
// flattens them back in (entries with Depth 2, reachable up to two levels).
// Entries come in speaking order, each carrying its own L4 messages and
// ChildCount, so a fused parent (whose message is Dream's summary) can be told
// apart from the turns it grouped. TopicCount counts every entry returned —
// roots and the sunk children this read alone brings back, alike.
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
// The blast radius is the whole graph: every edge bound to any node in it goes
// too, including the ones pointing at nodes a host would have kept. There is no
// narrower delete — a graph whose contents are wrong is re-imported.
func (s *Session) DeleteL3(id string) error {
	return s.Session.DeleteL3(id)
}
