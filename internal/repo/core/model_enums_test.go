// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/json"
	"testing"
)

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
