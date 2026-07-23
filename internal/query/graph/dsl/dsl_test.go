// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dsl

import (
	"encoding/json"
	"os"
	"testing"

	"memhop/internal/common/hash"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
)

func TestTokenize(t *testing.T) {
	input := `MATCH (n:concept) WHERE n.importance > 0.5 LIMIT 10`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize error: %v", err)
	}
	expected := []struct {
		kind  TokenKind
		value string
	}{
		{TokKeyword, "MATCH"}, {TokLParen, "("}, {TokIdent, "n"},
		{TokColon, ":"}, {TokIdent, "concept"}, {TokRParen, ")"},
		{TokKeyword, "WHERE"}, {TokIdent, "n"}, {TokDot, "."},
		{TokIdent, "importance"}, {TokOp, ">"}, {TokNumber, "0.5"},
		{TokKeyword, "LIMIT"}, {TokNumber, "10"}, {TokEOF, ""},
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, exp := range expected {
		if tokens[i].Kind != exp.kind || tokens[i].Value != exp.value {
			t.Errorf("token[%d]: expected (%v, %q), got (%v, %q)",
				i, exp.kind, exp.value, tokens[i].Kind, tokens[i].Value)
		}
	}
}

func TestTokenizeString(t *testing.T) {
	input := `PATH FROM "abc123" DEPTH 3`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize error: %v", err)
	}
	if tokens[2].Kind != TokString || tokens[2].Value != "abc123" {
		t.Errorf("expected string token 'abc123', got %v %q", tokens[2].Kind, tokens[2].Value)
	}
}

func TestParseMatch(t *testing.T) {
	tests := []struct {
		input    string
		varName  string
		nodeType string
		hasWhere bool
		limit    int
	}{
		{`MATCH (n:concept)`, "n", "concept", false, 0},
		{`MATCH (n)`, "n", "", false, 0},
		{`MATCH (n:concept) LIMIT 10`, "n", "concept", false, 10},
		{`MATCH (n) WHERE n.importance > 0.5 LIMIT 5`, "n", "", true, 5},
	}
	for _, tc := range tests {
		q, err := Parse(tc.input)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tc.input, err)
			continue
		}
		if q.Match == nil {
			t.Errorf("Parse(%q): expected Match query", tc.input)
			continue
		}
		m := q.Match
		if m.Variable != tc.varName {
			t.Errorf("Parse(%q): variable = %q, want %q", tc.input, m.Variable, tc.varName)
		}
		if m.NodeType != tc.nodeType {
			t.Errorf("Parse(%q): node_type = %q, want %q", tc.input, m.NodeType, tc.nodeType)
		}
		if (m.WhereClause != nil) != tc.hasWhere {
			t.Errorf("Parse(%q): has_where = %v, want %v", tc.input, m.WhereClause != nil, tc.hasWhere)
		}
		if m.Limit != tc.limit {
			t.Errorf("Parse(%q): limit = %d, want %d", tc.input, m.Limit, tc.limit)
		}
	}
}

func TestParseWhere(t *testing.T) {
	q, err := Parse(`MATCH (n) WHERE n.importance > 0.5 AND n.type = "concept"`)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if q.Match == nil || q.Match.WhereClause == nil {
		t.Fatal("expected WHERE clause")
	}
	wc := q.Match.WhereClause
	if wc.And == nil {
		t.Fatal("expected AND condition")
	}
	if wc.And.Left.PropertyCompare == nil {
		t.Error("left should be PropertyCompare")
	}
	if wc.And.Right.TypeEquals == nil {
		t.Error("right should be TypeEquals")
	}
}

func TestParseWhereOr(t *testing.T) {
	q, err := Parse(`MATCH (n) WHERE n.importance > 0.5 OR n.keywords CONTAINS "rust"`)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	wc := q.Match.WhereClause
	if wc.Or == nil {
		t.Fatal("expected OR condition")
	}
	if wc.Or.Left.PropertyCompare == nil {
		t.Error("left should be PropertyCompare")
	}
	if wc.Or.Right.KeywordContains == nil {
		t.Error("right should be KeywordContains")
	}
}

func TestParseHyperedge(t *testing.T) {
	q, err := Parse(`MATCH HYPEREDGE e-[n1, n2, n3]-`)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if q.Hyperedge == nil {
		t.Fatal("expected Hyperedge query")
	}
	h := q.Hyperedge
	if h.EdgeVar != "e" {
		t.Errorf("edge_var = %q, want 'e'", h.EdgeVar)
	}
}

func TestParsePath(t *testing.T) {
	q, err := Parse(`PATH FROM "abc123" DEPTH 3`)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if q.Path == nil {
		t.Fatal("expected Path query")
	}
	p := q.Path
	if p.StartNode != "abc123" {
		t.Errorf("start_node = %q, want 'abc123'", p.StartNode)
	}
	if p.MaxDepth != 3 {
		t.Errorf("max_depth = %d, want 3", p.MaxDepth)
	}
}

func TestParsePathWithEdgeKinds(t *testing.T) {
	q, err := Parse(`PATH FROM "abc123" DEPTH 3 EDGE_KINDS ["Related", "Causal"]`)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	p := q.Path
	if len(p.EdgeKinds) != 2 {
		t.Errorf("edge_kinds length = %d, want 2", len(p.EdgeKinds))
	}
}

func TestParseSubgraph(t *testing.T) {
	q, err := Parse(`SUBGRAPH FROM "abc123" DEPTH 2`)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if q.Subgraph == nil {
		t.Fatal("expected Subgraph query")
	}
	s := q.Subgraph
	if s.StartNode != "abc123" {
		t.Errorf("start_node = %q, want 'abc123'", s.StartNode)
	}
	if s.MaxDepth != 2 {
		t.Errorf("max_depth = %d, want 2", s.MaxDepth)
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		`INVALID QUERY`,
		`MATCH`,
		`MATCH (`,
		`MATCH (n:`,
		`PATH FROM`,
		`PATH FROM "abc"`,
		`SUBGRAPH FROM`,
		`MATCH (n) WHERE`,
	}
	for _, input := range bad {
		_, err := Parse(input)
		if err == nil {
			t.Errorf("Parse(%q) should fail", input)
		}
	}
}

func TestParseFull(t *testing.T) {
	input := `MATCH (n:concept) WHERE n.importance > 0.5 AND n.keywords CONTAINS "rust" LIMIT 10`
	q, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if q.Match == nil {
		t.Fatal("expected Match")
	}
	m := q.Match
	if m.NodeType != "concept" || m.Limit != 10 {
		t.Error("node_type or limit mismatch")
	}
	if m.WhereClause == nil || m.WhereClause.And == nil {
		t.Fatal("expected AND WHERE clause")
	}
}

// Helper to create test storage with nodes and edges.
func makeTestEngine(t *testing.T) (*storage.StorageEngine, string) {
	t.Helper()
	f, err := os.CreateTemp("", "dsl_test_*.meh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()); f.Close() })
	engine, err := storage.Create(f.Name(), 768)
	if err != nil {
		t.Fatal(err)
	}
	return engine, f.Name()
}

func buildTestGraph(t *testing.T, engine *storage.StorageEngine) uint64 {
	t.Helper()
	graphID := hash.HashID("test_graph")
	nodes := []model.HypergraphNode{
		{IDHash: 101, GraphID: graphID, Title: "Rust", NodeType: "concept", Importance: 0.9, Keywords: []string{"programming", "systems"}},
		{IDHash: 102, GraphID: graphID, Title: "Go", NodeType: "concept", Importance: 0.8, Keywords: []string{"programming", "concurrency"}},
		{IDHash: 103, GraphID: graphID, Title: "Memory", NodeType: "topic", Importance: 0.7, Keywords: []string{"cognitive", "systems"}},
		{IDHash: 104, GraphID: graphID, Title: "Graph", NodeType: "concept", Importance: 0.6, Keywords: []string{"data", "structure"}},
		{IDHash: 105, GraphID: graphID, Title: "DSL", NodeType: "tool", Importance: 0.5, Keywords: []string{"query", "language"}},
	}
	for _, n := range nodes {
		data, _ := json.Marshal(n)
		_, err := engine.WriteRecord(storage.RecL3GraphNode, n.IDHash, data)
		if err != nil {
			t.Fatal(err)
		}
	}
	edges := []model.HypergraphEdge{
		{IDHash: 201, GraphID: graphID, Kind: model.EdgeRelated, NodeIDs: []uint64{101, 102}, Weight: 0.8},
		{IDHash: 202, GraphID: graphID, Kind: model.EdgeCausal, NodeIDs: []uint64{101, 103}, Weight: 0.7},
		{IDHash: 203, GraphID: graphID, Kind: model.EdgeRelated, NodeIDs: []uint64{102, 104}, Weight: 0.6},
		{IDHash: 204, GraphID: graphID, Kind: model.EdgePartOf, NodeIDs: []uint64{103, 105}, Weight: 0.5},
	}
	for _, e := range edges {
		data, _ := json.Marshal(e)
		_, err := engine.WriteRecord(storage.RecL3GraphEdge, e.IDHash, data)
		if err != nil {
			t.Fatal(err)
		}
	}
	return graphID
}

func TestExecuteMatch(t *testing.T) {
	engine, _ := makeTestEngine(t)
	buildTestGraph(t, engine)
	executor := NewExecutor(engine)

	q, err := Parse(`MATCH (n)`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Nodes == nil || result.Nodes.Total != 5 {
		t.Errorf("expected 5 nodes, got %v", result.Nodes)
	}
}

func TestExecuteMatchWithType(t *testing.T) {
	engine, _ := makeTestEngine(t)
	buildTestGraph(t, engine)
	executor := NewExecutor(engine)

	q, err := Parse(`MATCH (n:concept)`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Nodes == nil || result.Nodes.Total != 3 {
		t.Errorf("expected 3 concept nodes, got %v", result.Nodes)
	}
}

func TestExecuteMatchWithWhere(t *testing.T) {
	engine, _ := makeTestEngine(t)
	buildTestGraph(t, engine)
	executor := NewExecutor(engine)

	q, err := Parse(`MATCH (n) WHERE n.importance > 0.7`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Nodes == nil || result.Nodes.Total != 2 {
		t.Errorf("expected 2 nodes with importance > 0.7, got %v", result.Nodes)
	}
}

func TestExecuteMatchWithLimit(t *testing.T) {
	engine, _ := makeTestEngine(t)
	buildTestGraph(t, engine)
	executor := NewExecutor(engine)

	q, err := Parse(`MATCH (n) LIMIT 2`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Nodes == nil || result.Nodes.Total != 2 {
		t.Errorf("expected 2 nodes with limit, got %v", result.Nodes)
	}
}

func TestExecuteHyperedge(t *testing.T) {
	engine, _ := makeTestEngine(t)
	buildTestGraph(t, engine)
	executor := NewExecutor(engine)

	q, err := Parse(`MATCH HYPEREDGE e-[n1, n2]-`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Edges == nil || result.Edges.Total != 4 {
		t.Errorf("expected 4 edges, got %v", result.Edges)
	}
}

func TestExecutePath(t *testing.T) {
	engine, _ := makeTestEngine(t)
	buildTestGraph(t, engine)
	executor := NewExecutor(engine)

	startID := hash.FormatHash(101)
	q, err := Parse(`PATH FROM "` + startID + `" DEPTH 2`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Hops == nil || result.Hops.Total == 0 {
		t.Error("expected traversal hops")
	}
}

func TestExecuteSubgraph(t *testing.T) {
	engine, _ := makeTestEngine(t)
	buildTestGraph(t, engine)
	executor := NewExecutor(engine)

	startID := hash.FormatHash(101)
	q, err := Parse(`SUBGRAPH FROM "` + startID + `" DEPTH 1`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Subgraph == nil || len(result.Subgraph.Nodes) == 0 {
		t.Error("expected subgraph nodes")
	}
}

func TestExecuteWhereAnd(t *testing.T) {
	engine, _ := makeTestEngine(t)
	buildTestGraph(t, engine)
	executor := NewExecutor(engine)

	q, err := Parse(`MATCH (n) WHERE n.importance > 0.6 AND n.type = "concept"`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(q)
	if err != nil {
		t.Fatal(err)
	}
	// Should match: Rust (0.9), Go (0.8), Graph (0.6 excluded by > 0.6)
	if result.Nodes == nil || result.Nodes.Total != 2 {
		t.Errorf("expected 2 nodes, got %v", result.Nodes)
	}
}

func TestExecuteWhereKeyword(t *testing.T) {
	engine, _ := makeTestEngine(t)
	buildTestGraph(t, engine)
	executor := NewExecutor(engine)

	q, err := Parse(`MATCH (n) WHERE n.keywords CONTAINS "programming"`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Nodes == nil || result.Nodes.Total != 2 {
		t.Errorf("expected 2 nodes with 'programming' keyword, got %v", result.Nodes)
	}
}
