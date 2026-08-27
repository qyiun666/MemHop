// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/json"
	"testing"
)

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
		SourceRef:  new("/src/lib.rs:L114-L288"),
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
		Label: new("depends_on"), CreatedAt: 1000,
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
