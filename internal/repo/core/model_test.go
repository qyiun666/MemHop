// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/json"
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

//go:fix inline
func strPtr(s string) *string { return new(s) }

//go:fix inline
func u64Ptr(v uint64) *uint64 { return new(v) }

func TestProfileSlotRoundtrip(t *testing.T) {
	p := ProfileSlot{
		IDHash:          1,
		Name:            "Meow",
		Role:            "assistant",
		Personality:     "friendly, helpful, curious",
		Preferences:     map[string]string{"language": "Rust", "style": "concise"},
		Lexicon:         map[string]string{"6": "厉害/牛"},
		StyleTraits:     []string{"prefers_brevity"},
		EmotionPatterns: map[string]string{"呵呵": "不满或敷衍"},
	}
	var got ProfileSlot
	jsonRoundtrip(t, p, &got)
	if got.IDHash != p.IDHash || got.Name != p.Name {
		t.Fatalf("mismatch: %+v", got)
	}
	if got.Preferences["language"] != "Rust" {
		t.Fatalf("preferences mismatch")
	}
	if got.Lexicon["6"] != "厉害/牛" {
		t.Fatalf("lexicon mismatch")
	}
	if len(got.StyleTraits) != 1 || got.StyleTraits[0] != "prefers_brevity" {
		t.Fatalf("style_traits mismatch")
	}
	if got.EmotionPatterns["呵呵"] != "不满或敷衍" {
		t.Fatalf("emotion_patterns mismatch")
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
		VectorPageRef: 50, Importance: 0.9,
		Valence: -0.3, Arousal: 0.7,
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
	s := SceneSlot{SceneID: 12345, SceneName: "测试场景", HitCount: 3, LastHitAt: 1000}
	var got SceneSlot
	jsonRoundtrip(t, s, &got)
	if got.SceneID != s.SceneID || got.SceneName != s.SceneName || got.HitCount != s.HitCount || got.LastHitAt != s.LastHitAt {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestNewSceneSlot(t *testing.T) {
	s := NewSceneSlot("购物助手")
	if s.SceneID != common.HashID("购物助手") {
		t.Fatalf("scene_id mismatch")
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

func TestTopicSlotEmptyKeywords(t *testing.T) {
	topic := makeTopic(333, 1)
	topic.UserKeywords = []string{}
	topic.AgentKeywords = []string{}
	topic.FusedKeywords = []string{}
	var got TopicSlot
	jsonRoundtrip(t, topic, &got)
	if len(got.UserKeywords) != 0 || len(got.AgentKeywords) != 0 {
		t.Fatalf("expected empty keywords")
	}
}

func TestTopicSlotUnicode(t *testing.T) {
	topic := makeTopic(555, 2)
	topic.UserKeywords = []string{"场景 🚀"}
	topic.AgentKeywords = []string{"回复内容"}
	topic.FusedKeywords = []string{"压缩 🔥"}
	var got TopicSlot
	jsonRoundtrip(t, topic, &got)
	if got.UserKeywords[0] != "场景 🚀" {
		t.Fatalf("unicode keyword mismatch")
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
	// Verify ComputeTopicID matches Rust: hash_id(format!("{}:{}:{}", ...))
	combined := "100:1000:1001"
	expected := common.HashID(combined)
	got := ComputeTopicID(100, 1000, 1001)
	if got != expected {
		t.Fatalf("ComputeTopicID mismatch: got %d, want %d", got, expected)
	}
}

func TestComputeTopicIDForTextDifferentiatesContent(t *testing.T) {
	id1 := ComputeTopicIDForText(100, 1000, "hello")
	id2 := ComputeTopicIDForText(100, 1000, "world")
	if id1 == id2 {
		t.Fatalf("same timestamp but different text should not collide: %d", id1)
	}
	if id1 != ComputeTopicIDForText(100, 1000, "hello") {
		t.Fatal("text-based topic ID must stay deterministic")
	}
}

func TestArchiveSlotRoundtrip(t *testing.T) {
	a := ArchiveSlot{
		IDHash: 1, ContentType: ContentText, Role: 0,
		ContextID: 20, CreatedAt: 1000,
		Content: "hello", Metadata: nil,
	}
	var got ArchiveSlot
	jsonRoundtrip(t, a, &got)
	if got.ContentType != ContentText || got.Content != "hello" {
		t.Fatalf("mismatch: %+v", got)
	}
	if got.Metadata != nil {
		t.Fatalf("expected nil metadata")
	}
}

func TestArchiveSlotWithMetadata(t *testing.T) {
	a := ArchiveSlot{
		IDHash: 2, ContentType: ContentCode, Role: 1,
		ContextID: 30, CreatedAt: 2000,
		Content:  "fn main() {}",
		Metadata: new(`{"lang":"rust"}`),
	}
	var got ArchiveSlot
	jsonRoundtrip(t, a, &got)
	if got.Metadata == nil || *got.Metadata != `{"lang":"rust"}` {
		t.Fatalf("metadata mismatch")
	}
}

func TestArchiveSlotImagePath(t *testing.T) {
	a := ArchiveSlot{
		IDHash: 3, ContentType: ContentImage, Role: 0,
		ContextID: 20, CreatedAt: 1000,
		Content:  "/img/screenshot.png",
		Metadata: new(`{"w":1920,"h":1080}`),
	}
	var got ArchiveSlot
	jsonRoundtrip(t, a, &got)
	if got.ContentType != ContentImage {
		t.Fatalf("content_type mismatch")
	}
}

func TestTrajectorySlotRoundtrip(t *testing.T) {
	ev := TrajectorySlot{
		IDHash: 1, SessionID: 42, Seq: 2, EventType: "tool_call",
		Payload: `{"tool":"read"}`, L4Ref: new(uint64(7)), Timestamp: 1000,
	}
	var got TrajectorySlot
	jsonRoundtrip(t, ev, &got)
	if got.IDHash != ev.IDHash || got.SessionID != ev.SessionID || got.Seq != ev.Seq ||
		got.EventType != ev.EventType || got.Payload != ev.Payload || got.Timestamp != ev.Timestamp {
		t.Fatalf("mismatch: %+v", got)
	}
	if got.L4Ref == nil || *got.L4Ref != 7 {
		t.Fatalf("l4_ref mismatch")
	}
}
