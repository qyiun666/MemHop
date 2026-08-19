// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/json"
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/common"
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

func strPtr(s string) *string { return &s }
func u64Ptr(v uint64) *uint64 { return &v }

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
	s := SceneSlot{SceneID: 12345, SceneName: "测试场景"}
	var got SceneSlot
	jsonRoundtrip(t, s, &got)
	if got.SceneID != s.SceneID || got.SceneName != s.SceneName {
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

func TestHypergraphSlotRoundtripPath(t *testing.T) {
	s := HypergraphSlot{
		IDHash: 1, Name: "memhop code graph",
		Source:    HypergraphSource{Kind: SourcePath, Value: "/src/lib.rs"},
		CreatedAt: 1000, UpdatedAt: 2000,
	}
	var got HypergraphSlot
	jsonRoundtrip(t, s, &got)
	if got.Source.Kind != SourcePath || got.Source.Value != "/src/lib.rs" {
		t.Fatalf("source mismatch: %+v", got.Source)
	}
}

func TestHypergraphSlotRoundtripContext(t *testing.T) {
	s := HypergraphSlot{
		IDHash: 2, Name: "ctx graph",
		Source: HypergraphSource{Kind: SourceContext, ContextID: 12345},
	}
	var got HypergraphSlot
	jsonRoundtrip(t, s, &got)
	if got.Source.Kind != SourceContext || got.Source.ContextID != 12345 {
		t.Fatalf("source mismatch: %+v", got.Source)
	}
}

func TestHypergraphSlotRoundtripURL(t *testing.T) {
	s := HypergraphSlot{
		IDHash: 3, Name: "ext",
		Source: HypergraphSource{Kind: SourceURL, Value: "https://example.com"},
	}
	var got HypergraphSlot
	jsonRoundtrip(t, s, &got)
	if got.Source.Kind != SourceURL || got.Source.Value != "https://example.com" {
		t.Fatalf("source mismatch: %+v", got.Source)
	}
}

func TestHypergraphSlotRoundtripManual(t *testing.T) {
	s := HypergraphSlot{
		IDHash: 4, Name: "manual",
		Source: HypergraphSource{Kind: SourceManual},
	}
	var got HypergraphSlot
	jsonRoundtrip(t, s, &got)
	if got.Source.Kind != SourceManual {
		t.Fatalf("source mismatch: %+v", got.Source)
	}
}

func TestHypergraphSourceJSONFormats(t *testing.T) {
	tests := []struct {
		src  HypergraphSource
		want string
	}{
		{HypergraphSource{Kind: SourcePath, Value: "/a/b"}, `{"kind":0,"value":"/a/b","context_id":0}`},
		{HypergraphSource{Kind: SourceContext, ContextID: 42}, `{"kind":1,"value":"","context_id":42}`},
		{HypergraphSource{Kind: SourceURL, Value: "http://x"}, `{"kind":2,"value":"http://x","context_id":0}`},
		{HypergraphSource{Kind: SourceManual}, `{"kind":3,"value":"","context_id":0}`},
	}
	for _, tt := range tests {
		data, err := json.Marshal(tt.src)
		if err != nil {
			t.Fatalf("marshal %v: %v", tt.src, err)
		}
		if string(data) != tt.want {
			t.Fatalf("want %s, got %s", tt.want, string(data))
		}
	}
}

func TestHypergraphNodeRoundtrip(t *testing.T) {
	n := HypergraphNode{
		IDHash: 1, GraphID: 100,
		Title: "MemHop::Open", NodeType: "function",
		Content:    "Opens or creates a MemHop database",
		Keywords:   []string{"open", "database"},
		SourceRef:  strPtr("/src/lib.rs:L114-L288"),
		Importance: 0.9,
		CreatedAt:  1000, UpdatedAt: 2000,
	}
	var got HypergraphNode
	jsonRoundtrip(t, n, &got)
	if got.IDHash != n.IDHash || got.GraphID != n.GraphID {
		t.Fatalf("hash mismatch: id=%d graph=%d", got.IDHash, got.GraphID)
	}
	if got.Title != n.Title || got.Importance != n.Importance {
		t.Fatalf("field mismatch")
	}
	if got.SourceRef == nil || *got.SourceRef != "/src/lib.rs:L114-L288" {
		t.Fatalf("source_ref mismatch")
	}
}

func TestHypergraphNodeNumericJSON(t *testing.T) {
	n := HypergraphNode{IDHash: 0xDEADBEEF, GraphID: 0x1234}
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Verify native numeric hash format in JSON
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	var idNum uint64
	if err := json.Unmarshal(raw["id_hash"], &idNum); err != nil {
		t.Fatalf("unmarshal id_hash: %v", err)
	}
	if idNum != 0xDEADBEEF {
		t.Fatalf("id mismatch: %d", idNum)
	}
}

func TestHypergraphEdgeRoundtrip(t *testing.T) {
	e := HypergraphEdge{
		IDHash: 1, GraphID: 100, Kind: EdgeDependency,
		NodeIDs: []uint64{10, 20, 30}, Weight: 0.8,
		Label: strPtr("depends_on"), CreatedAt: 1000,
	}
	var got HypergraphEdge
	jsonRoundtrip(t, e, &got)
	if got.IDHash != e.IDHash || got.GraphID != e.GraphID {
		t.Fatalf("hash mismatch")
	}
	if got.Kind != EdgeDependency || len(got.NodeIDs) != 3 {
		t.Fatalf("kind/nodes mismatch")
	}
	if got.Label == nil || *got.Label != "depends_on" {
		t.Fatalf("label mismatch")
	}
}

func TestHypergraphEdgeAllKinds(t *testing.T) {
	kinds := []GraphEdgeKind{
		EdgeRelated, EdgeCausal, EdgePartOf,
		EdgeSequence, EdgeDependency, EdgeCustom,
	}
	for _, k := range kinds {
		e := HypergraphEdge{
			IDHash: 99, GraphID: 1, Kind: k,
			NodeIDs: []uint64{1, 2}, Weight: 0.5,
		}
		var got HypergraphEdge
		jsonRoundtrip(t, e, &got)
		if got.Kind != k {
			t.Fatalf("kind mismatch: want %d got %d", k, got.Kind)
		}
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
		Metadata: strPtr(`{"lang":"rust"}`),
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
		Metadata: strPtr(`{"w":1920,"h":1080}`),
	}
	var got ArchiveSlot
	jsonRoundtrip(t, a, &got)
	if got.ContentType != ContentImage {
		t.Fatalf("content_type mismatch")
	}
}

func TestCapabilityRoundtrip(t *testing.T) {
	cfg := `{"endpoint":"http://localhost:9000"}`
	c := Capability{
		IDHash: 123456789, Name: "Deploy Service", Version: "1",
		Type: CapabilityComposite, Summary: "deploy service", Trigger: "deploy", Status: CapabilityActive,
		SuccessRate: 0.92, TriggerCount: 5,
		LastTriggered: 1000000,
		Resources: []ResourceRef{
			{Type: CapabilitySkill, Name: "deploy-checklist", Description: "pre-deploy checks"},
			{Type: CapabilityMCP, Name: "deploy-mcp", Ref: "localhost:9000", Config: &cfg},
			{Type: CapabilityMCP, Name: "run_test"},
		},
		Workflow: &Workflow{Steps: []WorkflowStep{
			{Ref: "deploy-checklist", Action: "run checks"},
			{Ref: "deploy-mcp", Action: "deploy"},
		}},
		CreatedAt: 900000, UpdatedAt: 950000,
	}
	var got Capability
	jsonRoundtrip(t, c, &got)
	if got.Status != CapabilityActive || got.SuccessRate != 0.92 {
		t.Fatalf("mismatch: %+v", got)
	}
	if len(got.Resources) != 3 || got.Resources[0].Type != CapabilitySkill ||
		got.Resources[1].Name != "deploy-mcp" || got.Resources[2].Name != "run_test" {
		t.Fatalf("resources mismatch: %+v", got.Resources)
	}
	if got.Workflow == nil || len(got.Workflow.Steps) != 2 || got.Workflow.Steps[1].Ref != "deploy-mcp" {
		t.Fatalf("workflow mismatch: %+v", got.Workflow)
	}
}

func TestCapabilityAllStatuses(t *testing.T) {
	statuses := []CapabilityStatus{CapabilityDraft, CapabilityActive, CapabilityDeprecated}
	for _, s := range statuses {
		c := Capability{IDHash: 1, Status: s}
		var got Capability
		jsonRoundtrip(t, c, &got)
		if got.Status != s {
			t.Fatalf("status mismatch: want %d got %d", s, got.Status)
		}
	}
}

func TestCapabilityResourcesOmitted(t *testing.T) {
	c := Capability{IDHash: 1, Resources: []ResourceRef{{Type: CapabilityMCP, Name: "s"}}}
	var got Capability
	jsonRoundtrip(t, c, &got)
	if len(got.Resources) != 1 || got.Workflow != nil {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestResourceRefConfigNil(t *testing.T) {
	p := ResourceRef{Type: CapabilityMCP, Name: "tool", Config: nil}
	var got ResourceRef
	jsonRoundtrip(t, p, &got)
	if got.Config != nil {
		t.Fatalf("expected nil config")
	}
}

func TestSceneUsageSlotRoundtrip(t *testing.T) {
	s := SceneUsageSlot{IDHash: 1, SceneID: 42, HitCount: 3, LastHitAt: 1000}
	var got SceneUsageSlot
	jsonRoundtrip(t, s, &got)
	if got != s {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestTrajectorySlotRoundtrip(t *testing.T) {
	ev := TrajectorySlot{
		IDHash: 1, SessionID: 42, Seq: 2, EventType: "tool_call",
		Payload: `{"tool":"read"}`, L4Ref: u64Ptr(7), Timestamp: 1000,
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

func TestCapabilityPathRoundtrip(t *testing.T) {
	c := Capability{
		IDHash: 1, Name: "t", Trigger: "tr", Status: CapabilityActive,
		Resources: []ResourceRef{{Type: CapabilityMCP, Name: "tool", Ref: "session:abc"}},
	}
	var got Capability
	jsonRoundtrip(t, c, &got)
	if len(got.Resources) != 1 || got.Resources[0].Ref != "session:abc" {
		t.Fatalf("path mismatch: %+v", got)
	}
}

func TestContentTypeValues(t *testing.T) {
	tests := []struct {
		ct   ContentType
		val  uint8
		name string
	}{
		{ContentText, 0, "text"}, {ContentImage, 1, "image"},
		{ContentVideo, 2, "video"}, {ContentDocument, 3, "document"},
		{ContentAudio, 4, "audio"}, {ContentCode, 5, "code"},
		{ContentOther, 0xFF, "other"},
	}
	for _, tt := range tests {
		if uint8(tt.ct) != tt.val {
			t.Fatalf("%s: want %d got %d", tt.name, tt.val, uint8(tt.ct))
		}
		if tt.ct.String() != tt.name {
			t.Fatalf("String(): want %s got %s", tt.name, tt.ct.String())
		}
		data, _ := json.Marshal(tt.ct)
		var back ContentType
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("roundtrip %s: %v", tt.name, err)
		}
		if back != tt.ct {
			t.Fatalf("roundtrip %s: got %d", tt.name, back)
		}
	}
}

func TestCapabilityStatusValues(t *testing.T) {
	tests := []struct {
		cs   CapabilityStatus
		val  uint8
		name string
	}{
		{CapabilityDraft, 0, "draft"}, {CapabilityActive, 1, "active"},
		{CapabilityDeprecated, 2, "deprecated"},
	}
	for _, tt := range tests {
		if uint8(tt.cs) != tt.val {
			t.Fatalf("%s: want %d got %d", tt.name, tt.val, uint8(tt.cs))
		}
		if tt.cs.String() != tt.name {
			t.Fatalf("String() mismatch")
		}
	}
}

func TestHyperedgeKindValues(t *testing.T) {
	kinds := []struct {
		k   HyperedgeKind
		val uint8
	}{
		{HyperCoOccurrence, 0}, {HyperCausal, 1}, {HyperSemantic, 2},
		{HyperTemporal, 3}, {HyperHierarchical, 4}, {HyperSequence, 5},
	}
	for _, tt := range kinds {
		if uint8(tt.k) != tt.val {
			t.Fatalf("want %d got %d", tt.val, uint8(tt.k))
		}
	}
}

func TestSourceKindValues(t *testing.T) {
	kinds := []struct {
		k   SourceKind
		val uint8
	}{
		{SourcePath, 0}, {SourceContext, 1}, {SourceURL, 2}, {SourceManual, 3},
	}
	for _, tt := range kinds {
		if uint8(tt.k) != tt.val {
			t.Fatalf("want %d got %d", tt.val, uint8(tt.k))
		}
	}
}

func TestGraphEdgeKindValues(t *testing.T) {
	kinds := []struct {
		k   GraphEdgeKind
		val uint8
	}{
		{EdgeRelated, 0}, {EdgeCausal, 1}, {EdgePartOf, 2},
		{EdgeSequence, 3}, {EdgeDependency, 4}, {EdgeCustom, 5},
	}
	for _, tt := range kinds {
		if uint8(tt.k) != tt.val {
			t.Fatalf("want %d got %d", tt.val, uint8(tt.k))
		}
	}
}

func TestLayerValues(t *testing.T) {
	layers := []struct {
		l    Layer
		val  uint8
		name string
	}{
		{LayerL0, 0, "L0"}, {LayerL1, 1, "L1"}, {LayerL2, 2, "L2"},
		{LayerL3, 3, "L3"}, {LayerL4, 4, "L4"}, {LayerL5, 5, "L5"},
	}
	for _, tt := range layers {
		if uint8(tt.l) != tt.val || tt.l.String() != tt.name {
			t.Fatalf("Layer %s: val=%d str=%s", tt.name, tt.val, tt.l.String())
		}
		data, _ := json.Marshal(tt.l)
		var back Layer
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("roundtrip %s: %v", tt.name, err)
		}
		if back != tt.l {
			t.Fatalf("roundtrip mismatch")
		}
	}
}

func makeTopic(id uint64, depth uint8) TopicSlot {
	var parentID *uint64
	if depth > 1 {
		v := uint64(1)
		parentID = &v
	}
	childrenIDs := []uint64{}
	if depth == 1 {
		childrenIDs = []uint64{2, 3}
	}
	fusedKW := []string{}
	if depth >= 2 {
		fusedKW = []string{"认证"}
	}
	return TopicSlot{
		ID: id, SceneID: 100, ParentID: parentID,
		ChildrenIDs: childrenIDs, Depth: depth,
		UserKeywords: []string{"登录", "JWT"}, UserTimestamp: 1000,
		L4Refs: []uint64{10}, L3Refs: []uint64{20, 21},
		AgentKeywords: []string{"token"}, AgentTimestamp: 1001,
		FusedKeywords: fusedKW, CentroidPageRef: 42,
	}
}
