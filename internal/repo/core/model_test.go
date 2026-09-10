// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

func jsonRoundtrip(t *testing.T, v any, out any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal: %v\njson: %s", err, string(data))
	}
}

func TestProfileSlotRoundtrip(t *testing.T) {
	p := ProfileSlot{
		IDHash:       1,
		Name:         "Meow",
		Role:         "assistant",
		Personality:  "friendly, helpful, curious",
		EmotionState: EmotionScore{Valence: 0.8, Arousal: 0.4, Dominance: 0.6},
		MBTI:         MBTIScore{IE: -0.5, NS: 0.2, TF: -0.3, JP: 0.1, Type: "INTJ"},
		Preferences:  map[string]string{"language": "Rust", "style": "concise"},
		UpdatedAtMs:  1700000000000,
	}
	var got ProfileSlot
	jsonRoundtrip(t, p, &got)
	if got.IDHash != p.IDHash || got.Name != p.Name || got.Personality != p.Personality {
		t.Fatalf("mismatch: %+v", got)
	}
	if got.EmotionState != p.EmotionState || got.MBTI != p.MBTI {
		t.Fatalf("distilled signals mismatch: %+v %+v", got.EmotionState, got.MBTI)
	}
	if got.Preferences["language"] != "Rust" {
		t.Fatalf("preferences mismatch")
	}
	if got.UpdatedAtMs != p.UpdatedAtMs {
		t.Fatalf("updated_at_ms mismatch")
	}
}

func TestSceneNodeEmptyEdges(t *testing.T) {
	n := SceneNode{EdgeIDs: []uint64{}}
	var got SceneNode
	jsonRoundtrip(t, n, &got)
	if len(got.EdgeIDs) != 0 {
		t.Fatalf("expected empty edge_ids")
	}
}

func TestSceneNodeRoundtrip(t *testing.T) {
	n := SceneNode{
		IDHash: 100, SceneID: 200, TopicIDs: []uint64{1, 2, 3},
		Importance: 0.9,
		Valence:    -0.3, Arousal: 0.7,
		CreatedAt: 1000, UpdatedAt: 2000,
		EdgeIDs: []uint64{10, 20},
	}
	var got SceneNode
	jsonRoundtrip(t, n, &got)
	if got.SceneID != n.SceneID {
		t.Fatalf("mismatch: %+v", got)
	}
	if len(got.TopicIDs) != 3 || len(got.EdgeIDs) != 2 {
		t.Fatalf("slice length mismatch")
	}
}

func TestSceneEdgeRoundtrip(t *testing.T) {
	e := SceneEdge{
		IDHash: 999, Kind: HyperCausal,
		NodeIDs: []uint64{10, 20}, Weight: 0.5, CreatedAt: 5000,
	}
	var got SceneEdge
	jsonRoundtrip(t, e, &got)
	if got.Kind != HyperCausal || len(got.NodeIDs) != 2 {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestSceneSlotRoundtrip(t *testing.T) {
	s := SceneSlot{SceneID: 12345, SceneName: "测试场景", TurnSeq: 3}
	var got SceneSlot
	jsonRoundtrip(t, s, &got)
	if got.SceneID != s.SceneID || got.SceneName != s.SceneName || got.TurnSeq != s.TurnSeq {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestNewSceneSlotKeepsHostSceneID(t *testing.T) {
	s := NewSceneSlot(4242, "购物助手")
	if s.SceneID != 4242 {
		t.Fatalf("the host owns the scene id; got %d", s.SceneID)
	}
	if s.SceneName != "购物助手" {
		t.Fatalf("scene_name mismatch")
	}
}

func TestTopicSlotRoundtripDepth1(t *testing.T) {
	topic := makeTopic(111, 1)
	var got TopicSlot
	jsonRoundtrip(t, topic, &got)
	if got.ID != topic.ID || got.SceneID != topic.SceneID {
		t.Fatalf("id/scene mismatch")
	}
	if got.ParentID != nil {
		t.Fatalf("depth-1 should have nil parent_id")
	}
}

func TestTopicSlotRoundtripDepth2(t *testing.T) {
	topic := makeTopic(222, 2)
	var got TopicSlot
	jsonRoundtrip(t, topic, &got)
	if got.ParentID == nil || *got.ParentID != 1 {
		t.Fatalf("depth-2 parent_id mismatch")
	}
}

func TestTopicSlotRoundtripKeywords(t *testing.T) {
	topic := makeTopic(333, 1)
	topic.FusedKeywords = []string{"场景 🚀", "回复内容", "压缩 🔥"}
	var got TopicSlot
	jsonRoundtrip(t, topic, &got)
	if !slices.Equal(got.FusedKeywords, topic.FusedKeywords) {
		t.Fatalf("keyword roundtrip mismatch: %v", got.FusedKeywords)
	}
}

func TestComputeTopicIDDeterministic(t *testing.T) {
	id1 := ComputeTopicID(100, 1000, 1001)
	id2 := ComputeTopicID(100, 1000, 1001)
	if id1 != id2 {
		t.Fatalf("not deterministic: %d != %d", id1, id2)
	}
}

func TestComputeTopicIDDifferent(t *testing.T) {
	id1 := ComputeTopicID(100, 1000, 1001)
	id2 := ComputeTopicID(100, 1000, 1002)
	if id1 == id2 {
		t.Fatalf("should differ: both are %d", id1)
	}
}

func TestComputeTopicIDConsistency(t *testing.T) {
	expected := common.HashID("100:1000:1001")
	got := ComputeTopicID(100, 1000, 1001)
	if got != expected {
		t.Fatalf("ComputeTopicID mismatch: got %d, want %d", got, expected)
	}
}

// A turn topic and a Dream-fused group can share a scene; the "turn:"
// namespace is what keeps their IDs apart. The turn counter, not the message
// timestamps, is what makes consecutive turns distinct.
func TestComputeTurnTopicIDNamespaced(t *testing.T) {
	turn := ComputeTurnTopicID(100, 1)
	if turn == ComputeTopicID(100, 1000, 1001) {
		t.Fatalf("turn topic id collides with the fused-topic id space")
	}
	if turn != ComputeTurnTopicID(100, 1) {
		t.Fatal("turn topic id must stay deterministic")
	}
	if turn == ComputeTurnTopicID(100, 2) {
		t.Fatal("the next turn of the same scene must get a different id")
	}
	if ComputeTurnTopicID(101, 1) == turn {
		t.Fatal("the same turn seq in another scene must get a different id")
	}
}

func TestArchiveSlotRoundtrip(t *testing.T) {
	a := ArchiveSlot{
		IDHash: 1, ContentType: ContentText, Role: 0,
		ContextID: 20, CreatedAt: 1000,
		Content: "hello",
	}
	var got ArchiveSlot
	jsonRoundtrip(t, a, &got)
	if got.ContentType != ContentText || got.Content != "hello" {
		t.Fatalf("mismatch: %+v", got)
	}
}

// The only archive case carrying a non-zero ContentType: value 0 would
// round-trip even if the tag were wrong.
func TestArchiveSlotImagePath(t *testing.T) {
	a := ArchiveSlot{
		IDHash: 3, ContentType: ContentImage, Role: 0,
		ContextID: 20, CreatedAt: 1000,
		Content: "/img/screenshot.png",
	}
	var got ArchiveSlot
	jsonRoundtrip(t, a, &got)
	if got.ContentType != ContentImage {
		t.Fatalf("content_type mismatch")
	}
}

// An operation event is an L4 record now, so the fields only events carry
// survive the same round trip as the utterances beside them.
func TestArchiveEventRoundtrip(t *testing.T) {
	ev := ArchiveSlot{
		IDHash: HashContent(42, 3), Kind: KindEvent, Seq: 3, ContentType: ContentText,
		ContextID: 42, EventType: "tool_call", NodePath: "1.1",
		CreatedAt: 1000, Content: `{"tool":"read"}`,
	}
	var got ArchiveSlot
	jsonRoundtrip(t, ev, &got)
	if got != ev {
		t.Fatalf("event archive mismatch: %+v", got)
	}
}

// Kind is the axis separating a turn's originals from its events, and the
// undefined end of it must be catchable at the boundary rather than stored.
func TestArchiveKindValid(t *testing.T) {
	if !KindUtterance.Valid() || !KindEvent.Valid() {
		t.Fatal("defined kinds must validate")
	}
	if KindUtterance.String() != "utterance" || KindEvent.String() != "event" {
		t.Fatalf("kind names: %q / %q", KindUtterance, KindEvent)
	}
	if ArchiveKind(9).Valid() {
		t.Fatal("an undefined kind must not validate")
	}
}

func TestSceneSlotL3ID(t *testing.T) {
	s := NewSceneSlot(1, "proj-a")
	if s.L3ID != 0 {
		t.Fatalf("fresh scene must have L3ID 0, got %d", s.L3ID)
	}
	s.L3ID = 42
	if got := s.L3ID; got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
}

// A plan node is its own record type now: no node/event discriminator, no event
// fields, and an identity derived from the topic that owns the tree.
func TestPlanNodeIdentity(t *testing.T) {
	node := PlanNode{
		IDHash: HashPlanNode(9, "1.2.1"), TopicID: 9, NodePath: "1.2.1",
		Status: StatusInProgress, Summary: "sum", UpdatedAt: 1000,
	}
	// Content and nodes share one topic key now, so the node id must fold that
	// key in: the same path under two turns is two distinct nodes.
	if HashPlanNode(9, "1") == HashPlanNode(10, "1") {
		t.Fatal("the same nodePath under two topics must not share a node id")
	}
	var got PlanNode
	jsonRoundtrip(t, node, &got)
	if got != node {
		t.Fatalf("plan node mismatch: %+v", got)
	}
}
