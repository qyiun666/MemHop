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

	removed, edgesRemoved, err := RebuildFromL2(engine, core.DefaultAgentID, l2Meta, &DecayParams{MinEdgeNodes: 2})
	if err == nil {
		t.Fatal("an unreadable topic must stop the pass, not delete the node standing on it")
	}
	if common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("want the read's own classification, got %v", err)
	}
	if len(removed) != 0 || edgesRemoved != 0 {
		t.Fatalf("a stopped pass removes nothing, got nodes %v edges %d", removed, edgesRemoved)
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

// Valence arrives on [0,1] with 0 = very negative and 1 = very positive, so the
// emotion a memory carries is how far it sits from neutral. Measuring it from zero
// instead says how positive it is — which would freeze a mildly positive memory's
// decay (a lambda of 0 makes a node uncollectable for good, since deletion only
// happens through decay) while letting the most painful ones fade at full speed.
func TestEmotionalBoostMeasuresDistanceFromNeutral(t *testing.T) {
	const base = 0.01
	neutral := applyEmotionalBoost(base, neutralValence, 1.0)
	if neutral != base {
		t.Fatalf("a neutral memory gets no protection however aroused: want %v, got %v", base, neutral)
	}
	dark := applyEmotionalBoost(base, 0.0, 1.0)
	bright := applyEmotionalBoost(base, 1.0, 1.0)
	if dark != bright {
		t.Fatalf("the two ends of the scale are equally emotional, so they must fade alike: %v vs %v", dark, bright)
	}
	if dark >= base {
		t.Fatalf("the most intense memory must fade slower than a neutral one: %v vs %v", dark, base)
	}
	const floor = base * (1 - maxEmotionalSlowdown)
	if dark <= 0 || dark < floor*(1-1e-9) {
		t.Fatalf("emotion may slow decay to a ceiling, never freeze it: %v below the %v floor", dark, floor)
	}
	// Arousal scales it: the same valence answered calmly keeps the base rate.
	if got := applyEmotionalBoost(base, 0.0, 0.0); got != base {
		t.Fatalf("an unaroused memory carries no emotional weight: want %v, got %v", base, got)
	}
	// A record is whatever the file says: outside the band the factor must stay
	// inside [0,1], or a negative lambda would grow importance every pass.
	for _, v := range []float64{-3, 4, 50} {
		for _, a := range []float64{-1, 9} {
			if got := applyEmotionalBoost(base, v, a); got <= 0 || got > base {
				t.Fatalf("valence %v arousal %v escaped the band: lambda %v not in (0, %v]", v, a, got, base)
			}
		}
	}
}

// All three L1 passes act on the whole scene-node set, and each of them is the
// only path that does what it does: decay is the only thing that ever removes a
// node, the rebuild is the only thing that drops a stale one, and an edge nobody
// builds never reports itself missing. So a node the enumeration steps over is not
// one less row — it is a node no pass ever acts on again.
func TestL1PassesRefuseANodeTheyCannotRead(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "l1strict.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const (
		liveID    = uint64(0xA1)
		damagedID = uint64(0xA2)
		topicID   = uint64(0xB1)
	)
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topicID,
		&core.TopicSlot{ID: topicID, SceneID: 7, Depth: 1}); err != nil {
		t.Fatalf("write topic: %v", err)
	}
	if err := core.WriteSceneNode(engine, core.DefaultAgentID, liveID, &core.SceneNode{
		IDHash: liveID, SceneID: 7, TopicIDs: []uint64{topicID},
		Importance: 1, CreatedAt: 1000, UpdatedAt: 1000,
	}); err != nil {
		t.Fatalf("write node: %v", err)
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL1SceneNode, damagedID,
		[]byte(`{"id":`)); err != nil {
		t.Fatalf("write the undecodable node: %v", err)
	}

	l2Meta := index.BuildL2MetaFromEngine(engine, core.DefaultAgentID)
	cfg := &DecayParams{LambdaNode: 0.01, LambdaEdge: 0.02, MinEdgeNodes: 2}
	if _, err := BuildHyperedges(engine, core.DefaultAgentID, 0.15, nil); common.CodeOf(err) != common.ErrDeserialization {
		t.Errorf("BuildHyperedges over a damaged node = %v, want the read's own code", err)
	}
	if _, _, err := RebuildFromL2(engine, core.DefaultAgentID, l2Meta, cfg); common.CodeOf(err) != common.ErrDeserialization {
		t.Errorf("RebuildFromL2 over a damaged node = %v, want the read's own code", err)
	}
	if _, err := DecayNetwork(engine, core.DefaultAgentID, l2Meta, cfg); common.CodeOf(err) != common.ErrDeserialization {
		t.Errorf("DecayNetwork over a damaged node = %v, want the read's own code", err)
	}
	// A refused pass changes nothing: the readable node keeps its importance and the
	// clock decay would have re-based.
	got, err := core.ReadSceneNode(engine, core.DefaultAgentID, liveID)
	if err != nil {
		t.Fatalf("read the live node: %v", err)
	}
	if got.Importance != 1 || got.UpdatedAt != 1000 {
		t.Fatalf("a refused pass moved the live node: %+v", got)
	}
}

// Dropping a stale node takes its two-member co-occurrence edge below
// MinEdgeNodes, so the edge goes with it — and a pass that counted only the decay
// stage's removals reported fewer edges than it deleted.
func TestRebuildFromL2CountsTheEdgeItTakesWithIt(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "rebuild.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const (
		staleID = uint64(0xA1)
		liveID  = uint64(0xA2)
		edgeID  = uint64(0xE1)
		topicID = uint64(0xB1)
	)
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topicID,
		&core.TopicSlot{ID: topicID, SceneID: 8, Depth: 1}); err != nil {
		t.Fatalf("write topic: %v", err)
	}
	// The stale one is stale on its own terms: no topics at all.
	if err := core.WriteSceneNode(engine, core.DefaultAgentID, staleID, &core.SceneNode{
		IDHash: staleID, SceneID: 7, Importance: 1, EdgeIDs: []uint64{edgeID}, CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("write the stale node: %v", err)
	}
	if err := core.WriteSceneNode(engine, core.DefaultAgentID, liveID, &core.SceneNode{
		IDHash: liveID, SceneID: 8, TopicIDs: []uint64{topicID},
		Importance: 1, EdgeIDs: []uint64{edgeID}, CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("write the live node: %v", err)
	}
	if err := core.WriteSceneEdge(engine, core.DefaultAgentID, edgeID,
		&core.SceneEdge{IDHash: edgeID, NodeIDs: []uint64{staleID, liveID}, Weight: 0.5}); err != nil {
		t.Fatalf("write the edge: %v", err)
	}

	l2Meta := index.BuildL2MetaFromEngine(engine, core.DefaultAgentID)
	removed, edgesRemoved, err := RebuildFromL2(engine, core.DefaultAgentID, l2Meta, &DecayParams{MinEdgeNodes: 2})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if len(removed) != 1 || edgesRemoved != 1 {
		t.Fatalf("rebuild took %d node(s) and reported %d edge(s), want 1 and 1: %v", len(removed), edgesRemoved, removed)
	}
	if _, err := core.ReadSceneEdge(engine, core.DefaultAgentID, edgeID); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("the edge must have gone with the node, read gives %v", err)
	}
}
