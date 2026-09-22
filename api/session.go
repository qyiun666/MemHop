// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Session is the only business handle of the public facade: it holds the internal
// domain-bound session in an unexported field and declares every externally callable
// method itself, so the surface is exactly what this file lists. Every call is
// serialized per agent domain by the internal domain lock.
//
// The methods split by audience. The runtime/task face (18) is what the host
// drives every turn and what LLM tools bind to: Search, Settle, Dream,
// AppendArchive (the host-driven loop), SceneContext, ListScenes, GetL0,
// UpdateL0, ListL1, SearchL4, GetL3, ListL3, ImportL3, QueryL3Nodes,
// QueryL3Subgraph, PlanNodeAdd, PlanNodeUpdate, PlanState.
// The assembly/admin face (7) is host code at session boundaries and management
// channels rather than the per-turn loop: UpdateScene, RenameTopic, MergeScenes,
// DeleteTopic, DeleteScene, UpdateL3, DeleteL3. Five of them are also MCP tools;
// the two deletes stay on the Go side, because correcting a memory takes a host
// that holds the ids it is about to erase.

package api

import (
	"context"

	"github.com/qyiun666/MemHop/internal"
)

// Session binds every call to one agent domain. The internal session it holds is an
// unexported field, not an embedded one: every method below is declared here, so
// embedding would promote nothing and would only hand a host the internal shapes —
// numeric ids among them — that this facade exists to keep inside.
type Session struct {
	session *internal.Session
}

// Search reads one scene — the host's session: its record plus its depth-1
// topics in turn order, and the topic id this read opened for the turn the
// host is about to run. It is the loop's one write-shaped read: opening a turn
// advances the scene's turn counter, so a host calls it once per turn and not
// as a poll — the pure read is SceneContext. An empty SearchQuery.SceneID
// allocates a fresh scene, which SearchQuery.L3ID may anchor to an L3 project
// domain; naming a scene that already exists together with an L3ID is refused
// (ErrInvalidQuery) instead of leaving that anchor where the host cannot see it
// did nothing; UpdateScene moves the anchor of a scene that exists.
func (s *Session) Search(q SearchQuery) (*SearchResult, error) {
	res, err := s.session.Search(q)
	if err != nil {
		return nil, err
	}
	return fromSearchResult(res), nil
}

// Settle closes one turn: it distills the utterances appended under topicID —
// the id Search opened for it — into that topic's keyword track and returns the
// topic as stored, the distilled track among its fields, so closing a turn needs
// no second read to see what the turn was distilled into. It writes no content of
// its own. One LLM call, inside the domain lock; a failed call leaves the turn
// unsettled and the scene exactly as it was, and re-running Settle on the same
// topic re-distills from what the topic holds. A turn whose content the retention
// window already reclaimed is refused with ErrInvalidQuery instead of getting an
// empty track.
func (s *Session) Settle(sceneID, topicID string) (*TopicSlot, error) {
	topic, err := s.session.Settle(sceneID, topicID)
	if err != nil {
		return nil, err
	}
	out := fromTopicSlot(*topic)
	return &out, nil
}

// GetL0 returns the profile without the internal id_hash. A domain whose profile
// was never written reads back as an empty, non-nil ProfileSlot — the answer to
// "nothing stored yet" is not an error. A profile that exists but cannot be read
// is reported as one (ErrIO / ErrDeserialization), never as an absent profile.
func (s *Session) GetL0() (*ProfileSlot, error) {
	slot, err := s.session.GetL0()
	if err != nil {
		return nil, err
	}
	out := fromProfileSlot(*slot)
	return &out, nil
}

// UpdateL0 writes the host-owned profile fields (Name / Role / Personality /
// Preferences) — the whole of ProfileInput. Name is required here as it is at
// Open and SubAgent: a blank name is refused, not stored. The library-owned half
// of the stored profile is inherited by the write itself: EmotionState and MBTI,
// which Dream evolves, and AgentType, stamped once when the domain was created.
// UpdatedAtMs is stamped here. So a profile edit never moves a domain between
// primary and sub, and never wipes the two distilled signals.
//
// Personality is the one field Dream evolves that this write does not inherit:
// it is host-seeded and Dream-refined, so an edit that leaves it empty clears the
// summary the last pass distilled, and the next pass evolves it again.
func (s *Session) UpdateL0(profile *ProfileInput) error {
	if profile == nil {
		return NewError(ErrInvalidQuery, "UpdateL0: profile is required")
	}
	coreSlot := toCoreProfileSlot(profile)
	return s.session.UpdateL0(&coreSlot)
}

// ListL1 returns the domain's L1 scene nodes, every id rendered as hex and the
// order stable across calls. Read-only by design: Dream builds the nodes and the
// co-occurrence edges between them, decays both, and is the only writer — a host
// reads what consolidation decided and cannot set it. EdgeIDs have no read of
// their own; two nodes sharing one are a pair Dream judged related.
func (s *Session) ListL1() ([]SceneNodeView, error) {
	nodes, err := s.session.ListL1()
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
	scenes, err := s.session.ListScenes(l3ID)
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
	slot, err := s.session.UpdateScene(sceneID, patch)
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
	slot, err := s.session.RenameTopic(topicID, name)
	if err != nil {
		return TopicSlot{}, err
	}
	return fromTopicSlot(slot), nil
}

// GetL3 returns an L3 graph with hex IDs. Its nodes and edges are sorted by id,
// and that order is the same on every call: the scan under the shared pool is a
// hash map, so without the sort one host would see one graph in two orders.
func (s *Session) GetL3(id string) (*L3Graph, error) {
	g, err := s.session.GetL3(id)
	if err != nil {
		return nil, err
	}
	return fromL3Graph(g), nil
}

// ListL3 returns all hypergraph slots with hex IDs, sorted by graph id.
func (s *Session) ListL3() ([]HypergraphSlot, error) {
	graphs, err := s.session.ListL3()
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
// leaving that domain ambiguous. Renaming onto the label the graph already has
// changes nothing but succeeds, and it still moves the graph's UpdatedAt: this
// call stamps that clock whether or not the label moved, so a nil name is the
// spelling of "stamp it, change nothing" and an empty one is refused rather than
// erasing the label that addresses the graph.
func (s *Session) UpdateL3(id string, name *string) (*L3Graph, error) {
	g, err := s.session.UpdateL3(id, name)
	if err != nil {
		return nil, err
	}
	return fromL3Graph(g), nil
}

// QueryL3Nodes returns nodes with hex IDs, sorted by node id; Limit keeps the
// first N of that order, so a capped query is the same subset every time.
func (s *Session) QueryL3Nodes(q L3NodeQuery) ([]HypergraphNode, error) {
	nodes, err := s.session.QueryL3Nodes(q)
	if err != nil {
		return nil, err
	}
	out := make([]HypergraphNode, len(nodes))
	for i, n := range nodes {
		out[i] = fromHypergraphNode(n)
	}
	return out, nil
}

// QueryL3Subgraph returns a subgraph with hex IDs, its nodes and edges sorted
// by id. A node the walk reaches but cannot be read is an error rather than a
// smaller answer: that node is one an edge named.
func (s *Session) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []GraphEdgeKind) (*L3Subgraph, error) {
	sub, err := s.session.QueryL3Subgraph(graphID, startNodeID, maxDepth, edgeKinds)
	if err != nil {
		return nil, err
	}
	return fromL3Subgraph(sub), nil
}

// SearchL4 returns content records with hex IDs. A topic-scoped read comes back in
// Seq order — the slot the turn's own writing chose; a read that spans topics has no
// Seq in common (it numbers slots inside one turn), so it comes back by each record's
// CreatedAt, with the record id breaking ties. L4 holds a
// turn's dialogue originals and its operation events alike, so Kind is a condition
// like any other: leaving it unset selects both. TopicID is the key Search issued
// for one turn, parsed as it is everywhere else — the reserved all-zero key is
// refused, not answered with an empty list.
func (s *Session) SearchL4(q L4Query) ([]ArchiveSlot, error) {
	archives, err := s.session.SearchL4(q)
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
// opened for it, keyed to the scene that turn belongs to — and is the only way
// content enters a topic. sceneID names the scene Search read, and the pair is
// checked before anything is stored: topicID has to be a turn key that scene
// opened, so a mistyped or invented id is refused (an unknown scene with
// ErrNotFound, a key outside the scene's turns with ErrInvalidQuery) instead of
// landing content under a key no read ever lists. KindUtterance is
// something somebody said; KindEvent is something that happened while they said it.
// Settle distills the utterances of that key, its events come back from SearchL4
// under a Kind condition, and the plan tree sharing the key comes back from
// PlanState.
//
// The slot the record took is what this call returns: a Seq of 0 asks the library
// to allocate one above everything the topic holds, and the address it chose comes
// back — the address a replay rewrites. Read the same shape back from SearchL4.
//
// What is stored of what you hand in is Kind, Seq, Role, ContentType, EventType,
// NodeSeq, Content and CreatedAt; ID and TopicID are ignored, which is what makes
// the round trip work — read a record back, change one field, write it to the slot it
// came from. CreatedAt is yours to supply and the library never stamps it: a turn
// records when things were said, not when the write happened. It is milliseconds since
// the epoch, the unit the retention window and every time filter measure, so a
// non-positive one is refused and so is a stamp in the seconds or microsecond band —
// the first would be swept as already expired, the second would never expire.
// An event owns no speaker and no medium, so its Role and ContentType are the
// library's (0 and text) whatever you set the second one to; an utterance owns
// both. A ContentType outside the constants above is refused on either kind.
//
// Seq 0 allocates: the record lands one slot above everything the topic holds, and
// above Seq 1 and 2, which belong to dialogue — so the first event of a turn is
// Seq 3. Allocation is this library choosing the slot, so it confirms the slot is
// empty before taking one: a slot whose record will not read back is refused with
// that read's own code rather than overwritten. A Seq you name is written as named,
// and taking a held slot overwrites it instead of erroring, across kinds: that is
// what lets a replayed turn converge rather than accumulate versions — you pointed
// at the slot, so it is yours to replace. Nothing reclaims a slot a replay stopped
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
// has to exist already: PlanNodeAdd is the only write that ever creates a step, so
// an event naming an ordinal nobody created is refused rather than answered with a
// fresh branch, and a mistyped ordinal cannot open a second tree. 0 attributes the
// event to no step at all.
//
// EventType is the host's own word for the step: the engine never branches on it,
// it comes back verbatim through SearchL4.
// These conventions are a shared vocabulary for the reader, not an accepted set:
// plan_step, llm_request, llm_output, tool_call, tool_result, subagent_spawn,
// subagent_done, context_inject, ask_user, user_reply.
func (s *Session) AppendArchive(sceneID, topicID string, slot ArchiveSlot) (uint64, error) {
	return s.session.AppendArchive(sceneID, topicID, toCoreAppendSlot(slot))
}

// PlanNodeAdd adds one step to a turn's plan tree and returns its ordinal.
// parentSeq 0 hangs the step at the top level and opens the turn's tree — a tree
// starts with no steps, so the first root step is how a plan first appears under a
// turn, and there is no separate create call. topicID names the turn the tree
// belongs to — the id Search handed out for it, which the host only ever passes
// back. Any other parentSeq must name a step this tree already holds —
// PlanNodeAdd under an unknown parent is refused rather than answered by growing
// one.
//
// The returned ordinal is the library's to hand out and the host's to keep: it is
// never derived from a title, and no two steps of one turn are ever live at the
// same ordinal. It is not permanently unique — a retention sweep frees the
// ordinals it removed, and an event that outlived the step it names keeps that
// number — so a host returning to a turn older than the retention window treats
// an ordinal it held before as a new step's address, not as the same step.
//
// One refusal is durable rather than one-shot: if the address the next ordinal
// names holds a record the engine cannot read back, the create reports that read's
// own code, and the next call reports it again — the library will not step around a
// record it cannot read in order to hand out a neighbouring number.
//
// This is the only way a step comes into existence. Nothing is written when the
// call returns an error, and a step's title may be filled in later by
// PlanNodeUpdate, which is why an empty title here is allowed: the view falls back
// to the ordinal until the host names the step.
func (s *Session) PlanNodeAdd(topicID string, parentSeq uint32, title string) (uint32, error) {
	return s.session.PlanNodeAdd(topicID, parentSeq, title)
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
	return s.session.PlanNodeUpdate(topicID, toInternalPlanStep(step))
}

// PlanState returns the plan tree of one turn — keyed by the topic id that
// opened it — with hex-free string statuses.
func (s *Session) PlanState(topicID string) (*PlanTree, error) {
	t, err := s.session.PlanState(topicID)
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
	return s.session.Dream(ctx, sceneID)
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
// apart from the turns it grouped.
func (s *Session) SceneContext(sceneID string) (*SceneContext, error) {
	return s.session.SceneContext(sceneID)
}

// MergeScenes folds scenes together: every topic of each secondary scene is
// retargeted to the primary scene, then the secondary scene records are
// deleted. Use it when a host resumed one conversation under a new session id.
// The primary's name and anchor win, and nothing comes back — re-read the
// primary to see the merged history.
//
// Settle before merging. A turn Search opened on a secondary scene and never
// settled has no topic record yet — settling is what writes one — so it is
// not among the topics this retargets. Its id names a scene that is now gone, the
// turn can never be settled, and whatever the host appended under it is reclaimed
// by the retention window like any other transcript.
//
// A secondary's L1 node goes with it, and the primary's node picks the retargeted
// topics up at the next Dream. A hyperedge that pointed at a merged scene loses
// the member to that same pass's edge decay.
func (s *Session) MergeScenes(primaryID string, secondaryIDs []string) error {
	return s.session.MergeScenes(primaryID, secondaryIDs)
}

// DeleteScene removes a scene for good: its record, every topic at any depth,
// all of their L4 content (originals and events alike), the plan trees those
// turns opened and the L1 scene node, so it disappears from listings and reads.
// A hyperedge that pointed at that scene loses the member to the next Dream's
// edge decay, and one left with fewer than two members is deleted with it.
func (s *Session) DeleteScene(sceneID string) error {
	return s.session.DeleteScene(sceneID)
}

// DeleteTopic removes one topic and its whole subtree (children at any depth)
// with all of their L4 content and the plan trees those turns opened — the
// memory-correction counterpart of Settle. Nothing is left hanging: the subtree
// goes with the topic, so a surviving topic never keeps a parent that is gone.
// Deleting a topic that does not exist is an error, not a no-op.
//
// One listing does lag. The scene survives with its L1 node, and that node's
// TopicIDs are what the last Dream found under the scene — so a topic deleted
// since is still named there until the next Dream rebuilds the list. Reading such
// an id back answers ErrNotFound, which is the correct answer: it is gone.
func (s *Session) DeleteTopic(topicID string) error {
	return s.session.DeleteTopic(topicID)
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
// was skipped, because edges are deduped by their members plus kind. The result
// reports the node ids created and the ones updated, how many it left alone, how
// many edges were created, and GraphIDs: every graph this batch resolved a domain
// into, including one it added nothing new to. A host needs those ids to anchor a
// scene on them (UpdateScene / SearchQuery.L3ID), since a graph id derives from the
// domain name and no other public call renders that derivation.
func (s *Session) ImportL3(items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	return s.session.ImportL3(items, mode)
}

// DeleteL3 removes a whole graph: the slot plus all of its nodes and hyperedges.
// It also drops the L2 anchors that named the graph — a scene's L3ID is the only
// inbound reference a graph has, and both anchor write paths require the graph to
// exist, so no scene is left listing under a project domain nothing resolves to.
// The blast radius is the whole graph: every edge bound to any node in it goes
// too, including the ones pointing at nodes a host would have kept. There is no
// narrower delete — a graph whose contents are wrong is re-imported.
func (s *Session) DeleteL3(id string) error {
	return s.session.DeleteL3(id)
}
