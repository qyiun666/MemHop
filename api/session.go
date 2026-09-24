// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The Session methods split by audience. The runtime/task face (18) is what the host
// drives every turn and what LLM tools bind to: Search, Update, Dream, AppendArchive,
// SceneContext, ListScenes, GetL0, UpdateL0, ListL1, SearchL4, GetL3, ListL3,
// ImportL3, QueryL3Nodes, QueryL3Subgraph, PlanNodeAdd, PlanNodeUpdate, PlanState. The
// assembly/admin face (8) is host code at session boundaries and management channels
// rather than the per-turn loop: UpdateScene, RenameTopic, MergeScenes, DeleteTopic,
// DeleteScene, UpdateL3, DeleteL3, AgentID.

package api

import (
	"context"

	"github.com/qyiun666/MemHop/internal"
)

// Session binds every call to one agent domain: calls on one domain are serialized
// by the library's domain lock, and the internal session is an unexported field —
// embedding it would promote nothing and would hand a host the internal shapes this
// facade keeps inside. Every id a host receives is a 16-character hex string issued
// by the library: round-trip it, never construct or parse one.
type Session struct {
	session *internal.Session
}

// Search reads the scene this domain is working — the host's session: its record plus
// its depth-1 topics in turn order, and the topic id this read minted for the turn the
// host is about to run. It is the loop's one write-shaped read: opening a turn
// advances the scene's turn counter, so call it once per turn and not as a poll — the
// pure read is SceneContext. A domain holds exactly one open turn, so a Session is one
// conversation in progress: run a worker per domain (SubAgent hands each its own session),
// or serialize Search-to-Update on one. Sharing a handle across goroutines is safe on the
// file — the domain lock serializes it — but a second Search takes the turn the first was
// about to close, and the first caller's turn is then left unsettled.
// An empty SceneID continues the domain's current scene,
// and after the file is reopened that scene restores from the records (the one a turn
// was opened in most recently; the turn counter only breaks ties among records written
// before that stamp existed); NewScene starts a fresh conversation instead. L3ID
// anchors a scene to an L3 project domain and is taken only by a read that creates
// one — NewScene, or the domain's first scene. Handed in along with a scene this read
// continues, named or not, it is refused (ErrInvalidQuery) rather than dropped: an
// anchor that did nothing must not look adopted. UpdateScene moves the anchor of a
// scene that exists.
func (s *Session) Search(q SearchQuery) (*SearchResult, error) {
	res, err := s.session.Search(q)
	if err != nil {
		return nil, err
	}
	return fromSearchResult(res), nil
}

// Update closes the turn Search opened for this domain. It records what the turn
// opened with and what it ended with — those two land on the dialogue slots a reader
// looks for them on, so closing the same turn again rewrites them instead of
// accumulating versions — and the host's own word for how it ended goes in as one
// event per call, since a suspension and the resume that followed it are two facts,
// not one line written twice. Closing one turn twice is therefore a replay, not a
// continuation: both endings are kept and only the last pair of words is stated, so a
// round that suspends and resumes is two turns (each its own read and its own close),
// never one turn closed twice. The turn's utterances are then distilled into its
// topic's keyword track, and the topic comes back as stored, that track among its
// fields.
//
// One LLM call, inside the domain lock; the only other call on this surface that
// talks to the model is Dream. A failed call leaves the turn open, so the host may
// close it again. This is the whole of what a turn's ending needs: which scene and
// which turn are the library's to remember, so a host running one agent over one
// library names no ids and carries no keys between calls. What the turn recorded
// while it ran is AppendArchive's, and a turn whose originals the retention window
// already reclaimed is refused rather than settled into an empty track.
func (s *Session) Update(end TurnEnd) (*TopicSlot, error) {
	topic, err := s.session.Update(end)
	if err != nil {
		return nil, err
	}
	out := fromTopicSlot(*topic)
	return &out, nil
}

// GetL0 returns the domain's profile. One that was never written reads back as an
// empty, non-nil ProfileSlot — "nothing stored yet" is not an error. A profile that
// exists but cannot be read is reported as one (ErrIO / ErrDeserialization), never
// as an absent profile.
func (s *Session) GetL0() (*ProfileSlot, error) {
	slot, err := s.session.GetL0()
	if err != nil {
		return nil, err
	}
	out := fromProfileSlot(*slot)
	return &out, nil
}

// UpdateL0 writes the host-owned profile fields (Name / Role / Personality /
// Preferences) — the whole of ProfileInput, taken by value the way SubAgent takes it:
// there is no "no profile" this call could mean, so the shape carries no pointer to
// leave nil. Name is required here as it is at Open and SubAgent: a blank name is
// refused, not stored. The library-owned half of the
// stored profile is inherited by the write itself (EmotionState, MBTI, AgentType,
// UpdatedAtMs), so a profile edit never moves a domain between primary and sub and
// never wipes the two distilled signals. Personality is the one exception: this
// write does not inherit it, so omitting it clears the summary the last Dream pass
// distilled, and the next pass evolves it again.
func (s *Session) UpdateL0(profile ProfileInput) error {
	coreSlot := toCoreProfileSlot(profile)
	return s.session.UpdateL0(&coreSlot)
}

// ListL1 returns the domain's L1 scene nodes in an order stable across calls.
// Read-only: Dream builds the nodes and the co-occurrence edges between them,
// decays both, and is the only writer.
func (s *Session) ListL1() ([]SceneNodeView, error) {
	nodes, err := s.session.ListL1()
	if err != nil {
		return nil, err
	}
	return mapSlice(nodes, fromSceneNode), nil
}

// ListScenes returns the domain's scenes; a non-empty l3ID keeps only the scenes
// anchored to that L3 project domain.
func (s *Session) ListScenes(l3ID string) ([]SceneSlot, error) {
	scenes, err := s.session.ListScenes(l3ID)
	if err != nil {
		return nil, err
	}
	return mapSlice(scenes, fromSceneSlot), nil
}

// UpdateScene patches one scene's host-facing metadata (title, L3 anchor) and
// returns the scene as stored afterwards, so a host confirms an anchor without
// listing the domain. Nil patch fields keep their stored value. A patch that changes
// nothing — an empty one, or values the scene already holds — writes nothing: the
// file is append-only, so the confirm read that documented this call would otherwise
// charge the host by the byte per look, and a retried patch per retry
// (TestNoOpSceneAndTopicWritesAppendNothing).
func (s *Session) UpdateScene(sceneID string, patch ScenePatch) (SceneSlot, error) {
	slot, err := s.session.UpdateScene(sceneID, patch)
	if err != nil {
		return SceneSlot{}, err
	}
	return fromSceneSlot(slot), nil
}

// RenameTopic gives one topic the name the host chose and returns the topic as stored
// afterwards. The name is the host's alone — the engine derives nothing into it, so
// consolidating or merging a scene rewrites the record around the name and never over
// it, and the new name is visible to Search and SceneContext immediately, not at the
// next consolidation. An empty name is refused: topics are created unnamed, so "" is
// the absence of a name rather than one. A topic that is not there is ErrNotFound,
// and nothing is created for it. Renaming to the name the topic already carries writes
// nothing for the same reason: a retried rename costs no space.
func (s *Session) RenameTopic(topicID, name string) (TopicSlot, error) {
	slot, err := s.session.RenameTopic(topicID, name)
	if err != nil {
		return TopicSlot{}, err
	}
	return fromTopicSlot(slot), nil
}

// GetL3 returns one L3 graph. Its nodes and edges are sorted by id, and that order is
// the same on every call: the scan under the shared pool is a hash map, so without
// the sort one host would see one graph in two orders.
func (s *Session) GetL3(id string) (*L3Graph, error) {
	g, err := s.session.GetL3(id)
	if err != nil {
		return nil, err
	}
	return fromL3Graph(g), nil
}

// ListL3 returns all hypergraph slots, sorted by graph id.
func (s *Session) ListL3() ([]HypergraphSlot, error) {
	graphs, err := s.session.ListL3()
	if err != nil {
		return nil, err
	}
	return mapSlice(graphs, fromHypergraphSlot), nil
}

// UpdateL3 renames a graph and returns it. The new label has to be free: a domain
// label is how ImportL3 addresses a graph, so renaming onto a label another graph
// already carries is refused with ErrInvalidQuery instead of leaving that domain
// ambiguous, and an empty one is refused rather than erasing the label that
// addresses the graph. Renaming onto the label the graph already carries is not an
// error and not a write: the slot's UpdatedAt is a content-change clock, so a call
// that changes nothing leaves it exactly where it was, which is what makes a
// replayed rename converge instead of making an untouched graph look freshly edited.
//
// What a rename never touches is the id. It stays the one ImportL3 or ListL3 handed
// over, and from then on it is no longer the hash of the label the graph carries, so
// every address a host holds for this graph — a scene's L3 anchor, a GetL3 or
// QueryL3Subgraph argument — keeps the id it was given rather than re-deriving one from
// the new label. Both labels still route to it while the rename stands: the new one by
// the label on the record, the label it was created under by the id that derives from
// it, which is why re-importing under either extends this graph instead of starting a
// second one (TestUpdateL3RenameSurvivesReimport).
func (s *Session) UpdateL3(id string, name string) (*L3Graph, error) {
	g, err := s.session.UpdateL3(id, name)
	if err != nil {
		return nil, err
	}
	return fromL3Graph(g), nil
}

// QueryL3Nodes returns the nodes one L3NodeQuery keeps, sorted by node id; Limit
// keeps the first N of that order, so a capped query is the same subset every time.
func (s *Session) QueryL3Nodes(q L3NodeQuery) ([]HypergraphNode, error) {
	nodes, err := s.session.QueryL3Nodes(q)
	if err != nil {
		return nil, err
	}
	return mapSlice(nodes, fromHypergraphNode), nil
}

// QueryL3Subgraph returns a BFS subgraph, its nodes and edges sorted by id. maxDepth
// counts hops from the start node, and a non-positive one sets no bound — the walk runs to
// the reachable component, which is how every limit on this surface is read too, so one
// zero never means "one hop" here and "everything" there. A node
// the walk reaches but cannot be read is an error rather than a smaller answer: that
// node is one an edge named.
func (s *Session) QueryL3Subgraph(graphID, startNodeID string, maxDepth int, edgeKinds []GraphEdgeKind) (*L3Subgraph, error) {
	sub, err := s.session.QueryL3Subgraph(graphID, startNodeID, maxDepth, edgeKinds)
	if err != nil {
		return nil, err
	}
	return fromL3Subgraph(sub), nil
}

// SearchL4 returns the content records an L4Query keeps. A topic-scoped read comes
// back in Seq order — the slot the turn's own writing chose; a read that spans topics
// has no Seq in common (it numbers slots inside one turn), so it comes back by each
// record's CreatedAt, with the record id breaking ties. TopicID is the key Search
// issued for one turn, parsed as it is everywhere else — the reserved all-zero key is
// refused, not answered with an empty list.
func (s *Session) SearchL4(q L4Query) ([]ArchiveSlot, error) {
	archives, err := s.session.SearchL4(q)
	if err != nil {
		return nil, err
	}
	return mapSlice(archives, fromArchiveSlot), nil
}

// AppendArchive writes one piece of the open turn's content and is the only way
// content enters a topic. Which turn that is belongs to the library — Search minted
// it — so this call names no ids and what it writes cannot land on a turn the host
// is not working, nor under a key no read ever lists.
//
// The slot the record took is what this returns. A Seq of 0 asks the library to
// allocate one above everything the topic holds — and above Seq 1 and 2, which belong
// to dialogue, so a turn's first event is Seq 3 — and the address it chose comes back:
// that address is what a replay rewrites. Allocation is the library choosing the slot,
// so it confirms the slot is empty before taking one; a slot whose record will not
// read back is refused with that read's own code rather than overwritten. A Seq you
// name is written as named, and taking a held slot overwrites it instead of erroring,
// across kinds: that is what lets a replayed turn converge rather than accumulate
// versions. Nothing reclaims a slot a replay stopped filling, so a withdrawn line
// stays until DeleteTopic or the retention window.
//
// What is stored of what you hand in is Kind, Seq, Role, ContentType, EventType,
// NodeSeq, Content and CreatedAt — the whole of ArchiveInput, which is the write shape
// and carries nothing else. CreatedAt is yours to supply and the
// library never stamps it — a turn records when things were said, not when the write
// happened — and a non-positive one, or one in the seconds or microsecond band, is
// refused, because the retention window measures milliseconds. Bound this call as a tool and
// the host still fills that field: a schema that asks a model for an epoch in milliseconds
// gets seconds or nothing back, and a refusal at the write boundary is the cheaper failure.
//
// An event names itself with a non-empty EventType and has no speaker, so its Role and
// ContentType are the library's (0 and text) whatever you set the second one to. An
// utterance declares Role (RoleUser / RoleAgent / RoleSystem) and ContentType and
// carries neither EventType nor NodeSeq; RoleDream is refused. Content over budget is
// refused, not truncated — 4 KiB per event, 64 KiB per utterance — because a shortened
// record reads back exactly like a complete one, and an unbounded utterance turns one
// distillation into an unbounded number of LLM calls holding the domain lock. Every
// rule is refused before any record or plan node is touched.
//
// NodeSeq attributes an event to one step of this turn's plan tree, and that step has
// to exist already: PlanNodeAdd is the only write that creates a step, so an ordinal
// nobody created is refused rather than answered with a fresh branch, and a mistyped
// one cannot open a second tree. 0 attributes the event to no step at all.
//
// EventType is the host's own word for the step: the engine never branches on it, and
// these conventions are a shared vocabulary for the reader, not an accepted set —
// plan_step, llm_request, llm_output, tool_call, tool_result, subagent_spawn,
// subagent_done, context_inject, ask_user, user_reply.
func (s *Session) AppendArchive(in ArchiveInput) (uint64, error) {
	return s.session.AppendArchive(toCoreAppendSlot(in))
}

// PlanNodeAdd adds one step to the open turn's plan tree and returns its ordinal.
// parentSeq 0 hangs the step at the top level and opens the tree — a tree starts with
// no steps, so the first root step is how a plan first appears under a turn and there
// is no separate create call. Which turn the tree belongs to is the library's to
// remember: Search minted it, so this call names no id. Any other parentSeq must name
// a step this tree already holds: an unknown parent is refused rather than answered by
// growing one.
//
// The returned ordinal is the library's to hand out and the host's to keep: never
// derived from a title, and no two steps of one turn are ever live at the same
// ordinal. It is issued above both this turn's live steps and the highest ordinal its
// surviving events still name — a step and the events bound to it share one address,
// while the two age separately — so an ordinal is free again only once nothing speaks
// of it. A turn's numbering can therefore carry a gap, and density is not something a
// host may read back out of it. It is still not unique across turns: a retention sweep
// frees a swept step's ordinal once the events naming it go too, so a host returning
// to a turn older than the retention window treats an ordinal it held before as a new
// step's address, not as the same step.
//
// One refusal is durable rather than one-shot: if the address the next ordinal names
// holds a record the engine cannot read back, this call reports that read's own code,
// and the next call reports it again — the library will not step around a record it
// cannot read in order to hand out a neighbouring number.
//
// Nothing is written when this call returns an error. A step's title may be filled in
// later by PlanNodeUpdate, which is why an empty title here is allowed: the view falls
// back to the ordinal until the host names the step.
func (s *Session) PlanNodeAdd(parentSeq uint32, title string) (uint32, error) {
	return s.session.PlanNodeAdd(parentSeq, title)
}

// PlanNodeUpdate restates one step of a turn's plan tree. Status always states where
// that step got to (there is no "leave it as it was" spelling); a blank Title or
// Summary keeps what the node holds. Restating a settled step as in_progress re-opens
// it, and that drops its FinishedAt.
//
// An update the engine cannot honour is refused before the node is touched: a status
// outside in_progress / done / failed, or a step this turn never created, leaves the
// tree exactly as it was. A step that reaches a terminal status can settle its
// parent: once every direct child of a Done parent is itself terminal, the parent's
// Summary folds up from its children's. This call writes no content: the events a step
// produced are L4 records, appended with AppendArchive on the same turn.
func (s *Session) PlanNodeUpdate(step PlanStep) error {
	return s.session.PlanNodeUpdate(toInternalPlanStep(step))
}

// PlanState returns the plan tree of the turn Search opened for this domain.
func (s *Session) PlanState() (*PlanTree, error) {
	t, err := s.session.PlanState()
	if err != nil {
		return nil, err
	}
	out := fromPlanTree(t)
	return &out, nil
}

// AgentID is the id of the domain this handle is bound to, as 16 hex characters, and inside
// this file it names exactly one domain. The library issues it — a sub-agent's when that
// domain is created, and the primary's is the implicit zero domain the file was opened on —
// and DB.Agent takes this string back to reach the same domain again. A host that wants one
// identifier per memory keeps this one: it survives reopening, and it is the only id here
// that addresses a domain rather than a record inside one.
//
// Scope, since the deployed shape is one file per agent: every file's primary is the zero
// domain, so the same 16 zeros mean different memories in different files. Across files the
// key is the path (or path plus id); a global map keyed on id alone would fold two agents
// together (TestAnAgentIDAddressesADomainInsideOneFile). Round-trip it; never construct or
// parse it.
func (s *Session) AgentID() string { return formatID(s.session.AgentID()) }

// ---- Promoted surface, documented ----
//
// These methods need no DTO mapping. They are declared rather than promoted because
// internal is not a published package: a forwarding declaration is the only way their
// contracts — lock scope, LLM cost, cascade scope, replay semantics — reach
// `go doc api.Session`. api/surface_public_test.go pins the resulting method set.

// Dream runs the consolidation pass — the sleep analogue: it fuses and compresses
// topics (L2), rebuilds and decays the L1 graph, distills the L0 profile, and prunes
// content (L4) and plan nodes (L5) past their retention window. Pass a scene id to
// consolidate one scene, or "" for every scene of the domain — and note the scope reaches
// consolidation only: both retention prunes run over the whole domain either way, since
// records age on their own clocks and a host that had to sweep one scene per call would
// never finish the domain. The report's numbers describe the pass it came from, so the
// prune counts on a scoped Dream include scenes the call never named. Naming a scene this
// domain does not hold — an id a model echoed from stale context, say — fails the call with
// ErrNotFound rather than reporting a clean no-op, because the second reads as consolidated.
// It is the only path that
// prunes either layer or rebuilds L1, so a domain that stops being written to still
// needs one Dream to shrink.
//
// It contacts the LLM and runs inside the domain lock: while it works, every other
// call on this agent domain waits.
func (s *Session) Dream(ctx context.Context, sceneID string) (*DreamReport, error) {
	return s.session.Dream(ctx, sceneID)
}

// SceneContext reads a scene's whole transcript without opening a turn: unlike Search
// it writes nothing — no topic id is minted and the scene's turn counter stays where
// it is — so it is the read for showing or exporting a conversation, and the one a
// loop can call again mid-round without spending a turn. An empty sceneID reads the
// scene this domain is working, so a host running one agent over one library names
// nothing to read it; a domain that has never spoken answers an empty transcript with
// no scene named — "nothing has been said here yet" is a fact, not a failure, and this
// call still writes nothing. A scene id the host names that is not there is ErrNotFound.
// Rows arrive ordered: user timestamp, then shallower-first at a tie (a fused group carries
// the timestamp of the first turn it swallowed, so ties are the norm), then topic id - a
// host reads the listing linearly and never re-sorts it. Each row carries its own
// L4 messages; depth-2 originals are included here (see SceneContext).
func (s *Session) SceneContext(sceneID string) (*SceneContext, error) {
	return s.session.SceneContext(sceneID)
}

// MergeScenes folds scenes together: every topic of each secondary scene is retargeted
// to the primary scene, then the secondary scene records are deleted. Use it when a
// host resumed one conversation under a new session id. The primary's name and anchor
// win, and nothing comes back — re-read the primary to see the merged history.
//
// Update before merging. A turn Search opened on a secondary scene and never closed
// has no topic record yet — closing is what writes one — so it is not among the
// topics this retargets: its id names a scene that is now gone, the turn can never be
// closed, and whatever the host appended under it is reclaimed by the retention
// window like any other transcript. The domain's own memory of the turn moves to the
// primary with the merge, emptied: the turn id derives from the scene, so the host
// reads again to work on the merged one.
//
// A secondary's L1 node goes with it, and the primary's node picks the retargeted
// topics up at the next Dream. A hyperedge that pointed at a merged scene loses the
// member to that same pass's edge decay.
func (s *Session) MergeScenes(primaryID string, secondaryIDs []string) error {
	return s.session.MergeScenes(primaryID, secondaryIDs)
}

// DeleteScene removes a scene for good: its record, every topic at any depth, all of
// their L4 content (originals and events alike), the plan trees those turns opened and
// the L1 scene node. A hyperedge that pointed at that scene loses the member to the
// next Dream's edge decay, and one left with fewer than two members is deleted with it.
func (s *Session) DeleteScene(sceneID string) error {
	return s.session.DeleteScene(sceneID)
}

// DeleteTopic removes one topic and its whole subtree (children at any depth) with all
// of their L4 content and the plan trees those turns opened — the memory-correction
// counterpart of Update. The subtree goes with the topic, so a surviving topic never
// keeps a parent that is gone. Deleting a topic that does not exist is an error, not a
// no-op.
//
// One listing does lag: the surviving scene's L1 node keeps the TopicIDs the last
// Dream found, so a topic deleted since is still named there until the next Dream
// rebuilds the list. Reading such an id back answers ErrNotFound, which is correct.
func (s *Session) DeleteTopic(topicID string) error {
	return s.session.DeleteTopic(topicID)
}

// ImportL3 batch-imports knowledge nodes into one graph per Domain: a domain name the
// graph already has extends it, a new one creates it. The mode is required (see
// L3ImportMode). Every item must name a Title and a Domain: a malformed batch is
// refused and writes nothing at all, so result.Errors is reserved for a per-item
// storage failure while the rest of the batch proceeds and the error return means the
// call did nothing.
//
// A relation may target an item later in the same batch (edges resolve in a second
// pass), and every item declares its edges — including one whose node was skipped,
// because edges are deduped by their members plus kind.
//
// GraphIDs names every graph this batch resolved a domain into, including one it added
// nothing new to. A host needs those ids to anchor a scene on them (UpdateScene /
// SearchQuery.L3ID), since a graph id derives from the domain name and no other public
// call renders that derivation.
func (s *Session) ImportL3(items []L3ImportItem, mode L3ImportMode) (*L3ImportResult, error) {
	return s.session.ImportL3(items, mode)
}

// DeleteL3 removes a whole graph: the slot plus all of its nodes and hyperedges. It
// also drops the L2 anchors that named the graph — a scene's L3ID is the only inbound
// reference a graph has, and both anchor write paths require the graph to exist, so no
// scene is left listing under a project domain nothing resolves to. The blast radius is
// the whole graph: every edge bound to any node in it goes too, including the ones
// pointing at nodes a host would have kept. There is no narrower delete — a graph whose
// contents are wrong is re-imported.
func (s *Session) DeleteL3(id string) error {
	return s.session.DeleteL3(id)
}
