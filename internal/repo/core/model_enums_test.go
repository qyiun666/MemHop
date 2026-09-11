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

func makeTopic(id uint64, depth uint8) TopicSlot {
	var parentID *uint64
	if depth > 1 {
		v := uint64(1)
		parentID = &v
	}
	keywords := []string{"登录", "JWT"}
	if depth >= 2 {
		keywords = []string{"认证"}
	}
	return TopicSlot{
		ID: id, SceneID: 100, ParentID: parentID, Depth: depth,
		FusedKeywords: keywords,
		UserTimestamp: 1000, AgentTimestamp: 1001,
	}
}
