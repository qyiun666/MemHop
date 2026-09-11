// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/json"
	"testing"
)

func TestHypergraphNodeRoundtrip(t *testing.T) {
	n := HypergraphNode{
		IDHash: 1, GraphID: 100,
		Title: "MemHop::Open", NodeType: "function",
		Content:   "Opens or creates a MemHop database",
		Keywords:  []string{"open", "database"},
		SourceRef: new("/src/lib.rs:L114-L288"),
		CreatedAt: 1000, UpdatedAt: 2000,
	}
	var got HypergraphNode
	jsonRoundtrip(t, n, &got)
	if got.IDHash != n.IDHash || got.GraphID != n.GraphID {
		t.Fatalf("hash mismatch: id=%d graph=%d", got.IDHash, got.GraphID)
	}
	if got.Title != n.Title {
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
		NodeIDs: []uint64{10, 20, 30}, CreatedAt: 1000,
	}
	var got HypergraphEdge
	jsonRoundtrip(t, e, &got)
	if got.IDHash != e.IDHash || got.GraphID != e.GraphID {
		t.Fatalf("hash mismatch")
	}
	if got.Kind != EdgeDependency || len(got.NodeIDs) != 3 {
		t.Fatalf("kind/nodes mismatch")
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
			NodeIDs: []uint64{1, 2},
		}
		var got HypergraphEdge
		jsonRoundtrip(t, e, &got)
		if got.Kind != k {
			t.Fatalf("kind mismatch: want %d got %d", k, got.Kind)
		}
	}
}
