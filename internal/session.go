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
// topics. An empty SearchQuery.SceneID allocates a fresh scene. The result also
// carries the topic id this read opened for the turn the host is about to run.
func (s *Session) Search(q SearchQuery) (*SearchResult, error) {
	return s.db.Search(s.agentID, q)
}

// Update distills the content this turn appended under topicID into that
// topic's keyword track; sceneID names the scene Search read.
func (s *Session) Update(sceneID, topicID string) error {
	return s.db.Update(s.agentID, sceneID, topicID)
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

// DeleteTopic removes a topic and its whole subtree (children at any depth),
// the L4 content they own, the plan trees they opened and their cache entries,
// so the deleted topic no longer surfaces in any scene read.
func (s *Session) DeleteTopic(topicID string) error {
	return s.db.DeleteTopic(s.agentID, topicID)
}

// DeleteScene removes a scene: its scene record, every topic (all depths),
// the L4 content and plan trees those topics own, and the cache entries, so the
// scene disappears from listings and reads.
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

// SearchL4 reads content records by any combination of filters; they AND
// together, so L4Query{TopicID: &turnID} returns exactly that turn's originals or
// its events depending on Kind — unset Kind selects both kinds.
func (s *Session) SearchL4(q L4Query) ([]ArchiveSlot, error) {
	return s.db.SearchL4(s.agentID, q)
}

// AppendArchive writes one piece of content — a dialogue original or an
// operation event — under the topic id Search issued for this turn. Seq 0 lets the
// library allocate the slot; a non-zero NodeSeq on an event hangs it on that plan
// step, which has to exist already. Nothing is written when this call returns an
// error, and no step is ever created here.
func (s *Session) AppendArchive(topicID string, slot ArchiveSlot) error {
	return s.db.AppendArchive(s.agentID, topicID, slot)
}

// ---- L5 plan tree ----

// PlanCreate opens a turn's plan tree by creating its first root step, and
// returns the ordinal that step is addressed by from now on. topicID names the
// turn that owns the plan.
func (s *Session) PlanCreate(topicID, title string) (uint32, error) {
	return s.db.PlanCreate(s.agentID, topicID, title)
}

// PlanNodeAdd adds one step to a turn's plan tree and returns its ordinal.
// parentSeq 0 puts it at the top level; any other value must name a step the tree
// already holds. A step has no other way into the tree.
func (s *Session) PlanNodeAdd(topicID string, parentSeq uint32, title string) (uint32, error) {
	return s.db.PlanNodeAdd(s.agentID, topicID, parentSeq, title)
}

// PlanNodeUpdate restates one step of a turn's plan tree. The step's ordinal is
// the library's, never the host's; a blank Title/Summary keeps what is stored.
func (s *Session) PlanNodeUpdate(topicID string, step PlanStep) error {
	return s.db.PlanNodeUpdate(s.agentID, topicID, step)
}

// PlanState returns the plan tree of one turn, keyed by the topic id that
// opened it.
func (s *Session) PlanState(topicID string) (*PlanTree, error) {
	return s.db.PlanState(s.agentID, topicID)
}
