// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package engram

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// An edge the decay pass cannot read is not an edge that went away: reporting it
// as pruned lets the pass carry on and leave nodes holding a reference to an edge
// nobody trimmed. The sibling that prunes the other half of the same link already
// draws this line, so the two directions of one cascade now agree.
func TestRemoveNodeFromEdgeReportsUnreadableEdge(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "decay.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const (
		edgeID   = uint64(0xE1)
		victimID = uint64(1)
		otherID  = uint64(2)
	)
	edge := &core.SceneEdge{
		IDHash: edgeID, NodeIDs: []uint64{victimID, otherID},
		Weight: 0.9, CreatedAt: 1,
	}
	if err := core.WriteSceneEdge(engine, core.DefaultAgentID, edgeID, edge); err != nil {
		t.Fatalf("write edge: %v", err)
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL1Hyperedge, edgeID,
		[]byte(`{"node_ids":`)); err != nil {
		t.Fatalf("replace the edge payload with an undecodable one: %v", err)
	}

	deleted, err := removeNodeFromEdge(engine, core.DefaultAgentID, edgeID, victimID, &DecayParams{MinEdgeNodes: 2})
	if err == nil {
		t.Fatal("an unreadable edge must stop the cascade, not be reported as pruned")
	}
	if common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("want the read's own classification, got %v", err)
	}
	if deleted {
		t.Fatal("nothing was deleted, so the report must not say otherwise")
	}
}

// A depth-3 node survives the pass only if its topic can be read and its parent
// is shallow enough. Answering「don't keep」for a read that merely failed deleted
// an L1 record over one unreadable payload, so the failure now stops the pass.
func TestRebuildFromL2StopsOnUnreadableDeepTopic(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "decay.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	var (
		nodeID   = uint64(0xA1)
		topicID  = uint64(0xB1)
		parentID = uint64(0xB2)
	)
	parent := &core.TopicSlot{ID: parentID, SceneID: 7, Depth: 2,
		FusedKeywords: []string{"p"}, UserTimestamp: 5, AgentTimestamp: 6}
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, parentID, parent); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	topic := &core.TopicSlot{ID: topicID, SceneID: 7, Depth: 3, ParentID: &parentID,
		FusedKeywords: []string{"k"}, UserTimestamp: 1, AgentTimestamp: 2}
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topicID, topic); err != nil {
		t.Fatalf("write topic: %v", err)
	}
	node := &core.SceneNode{IDHash: nodeID, SceneID: 7, TopicIDs: []uint64{topicID},
		Importance: 1, CreatedAt: 1, UpdatedAt: 2}
	if err := core.WriteSceneNode(engine, core.DefaultAgentID, nodeID, node); err != nil {
		t.Fatalf("write node: %v", err)
	}

	// The cache is built first: the rule reads the cached depth, and the payload
	// is made unreadable afterwards.
	l2Meta := index.BuildL2MetaFromEngine(engine, core.DefaultAgentID)
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL2Topic, topicID,
		[]byte(`{"id":`)); err != nil {
		t.Fatalf("replace the topic payload with an undecodable one: %v", err)
	}

	removed, err := RebuildFromL2(engine, core.DefaultAgentID, l2Meta, &DecayParams{MinEdgeNodes: 2})
	if err == nil {
		t.Fatal("an unreadable topic must stop the pass, not delete the node standing on it")
	}
	if common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("want the read's own classification, got %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("a stopped pass removes nothing, got %v", removed)
	}
	if _, err := core.ReadSceneNode(engine, core.DefaultAgentID, nodeID); err != nil {
		t.Fatalf("the node must still be there: %v", err)
	}
}

// An edge that is genuinely gone has no member list left to prune, and that is a
// clean answer rather than a failure — the node's stale reference is trimmed by
// whoever reads it next.
func TestRemoveNodeFromEdgeToleratesGoneEdge(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "decay.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	deleted, err := removeNodeFromEdge(engine, core.DefaultAgentID, 0xE2, 1, &DecayParams{MinEdgeNodes: 2})
	if err != nil {
		t.Fatalf("a missing edge is not a failure: %v", err)
	}
	if deleted {
		t.Fatal("an edge that was never there cannot be deleted")
	}
}

// A node a scene delete took away between two Dreams leaves the co-occurrence edge
// naming it: the rebuild walks the nodes that exist, so nothing else ever trims
// that member, and the edge goes on reporting a pairing with a record the domain
// does not hold.
func TestDecayOneEdgeDropsAMemberThatIsGone(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "decay.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const (
		edgeID = uint64(0xE3)
		goneID = uint64(11)
		liveID = uint64(12)
	)
	if err := core.WriteSceneNode(engine, core.DefaultAgentID, liveID, &core.SceneNode{
		IDHash: liveID, SceneID: 2, TopicIDs: []uint64{1}, CreatedAt: 1000, Importance: 1,
	}); err != nil {
		t.Fatalf("write the surviving node: %v", err)
	}
	edge := &core.SceneEdge{IDHash: edgeID,
		NodeIDs: []uint64{goneID, liveID}, Weight: 0.9, CreatedAt: 1000}
	if err := core.WriteSceneEdge(engine, core.DefaultAgentID, edgeID, edge); err != nil {
		t.Fatalf("write the edge: %v", err)
	}

	rep := &DecayReport{}
	if err := decayOneEdge(engine, core.DefaultAgentID,
		&DecayParams{LambdaEdge: 0.02, EdgeRemoveThreshold: 0.05, MinEdgeNodes: 1},
		edge, edgeID, map[uint64]bool{}, 1000, rep); err != nil {
		t.Fatalf("decay one edge: %v", err)
	}
	got, err := core.ReadSceneEdge(engine, core.DefaultAgentID, edgeID)
	if err != nil {
		t.Fatalf("read the edge back: %v", err)
	}
	if len(got.NodeIDs) != 1 || got.NodeIDs[0] != liveID {
		t.Fatalf("the edge still names a node the domain does not hold: %v", got.NodeIDs)
	}
	if rep.RemovedEdges != 0 {
		t.Fatalf("an edge that kept a member is not a removed edge: %+v", rep)
	}
}
