// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Per-agent session handle: binds every operation to one agent domain. The
// public method set of api.Session is exactly this type's method set; the
// domain lock is taken per call by the underlying DB methods. File-level
// lifecycle (Checkpoint/Close/IsClosed) belongs to the DB handle the host
// opened, so it is not repeated here. Method contracts are documented on the
// DB big methods this type forwards to.

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

// AgentID reports the domain this handle is bound to, as the library numbers it. DB.Agent
// takes that number back, so a host that keeps one identifier for a memory has a usable
// one — no id is ever invented by the host, and none is derived from anything it holds.
func (s *Session) AgentID() uint64 { return s.agentID }

// ---- scene read / turn write ----

func (s *Session) Search(q SearchQuery) (*SearchResult, error) {
	return s.db.Search(s.agentID, q)
}

func (s *Session) Update(end TurnEnd) (*TopicSlot, error) {
	return s.db.Update(s.agentID, end)
}

// ---- Dream ----

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

// ---- L1 scene hypergraph ----

func (s *Session) ListL1() ([]SceneNode, error) {
	return s.db.ListL1(s.agentID)
}

// ---- L2 scenes/topics ----

func (s *Session) ListScenes(l3ID string) ([]SceneSlot, error) {
	return s.db.ListScenes(s.agentID, l3ID)
}

func (s *Session) UpdateScene(sceneID string, patch ScenePatch) (SceneSlot, error) {
	return s.db.UpdateScene(s.agentID, sceneID, patch)
}

func (s *Session) RenameTopic(topicID, name string) (TopicSlot, error) {
	return s.db.RenameTopic(s.agentID, topicID, name)
}

func (s *Session) SceneContext(sceneID string) (*SceneContext, error) {
	return s.db.SceneContext(s.agentID, sceneID)
}

func (s *Session) MergeScenes(primaryID string, secondaryIDs []string) error {
	return s.db.MergeScenes(s.agentID, primaryID, secondaryIDs)
}

func (s *Session) DeleteTopic(topicID string) error {
	return s.db.DeleteTopic(s.agentID, topicID)
}

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

func (s *Session) UpdateL3(id, name string) (*L3Graph, error) {
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

func (s *Session) AppendArchive(slot ArchiveSlot) (uint64, error) {
	return s.db.AppendArchive(s.agentID, slot)
}

// ---- L5 plan tree ----

func (s *Session) PlanNodeAdd(parentSeq uint32, title string) (uint32, error) {
	return s.db.PlanNodeAdd(s.agentID, parentSeq, title)
}

func (s *Session) PlanNodeUpdate(step PlanStep) error {
	return s.db.PlanNodeUpdate(s.agentID, step)
}

func (s *Session) PlanState() (*PlanTree, error) {
	return s.db.PlanState(s.agentID)
}
