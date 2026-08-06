// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package l3

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

func newTestEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	engine, err := storage.Create(filepath.Join(t.TempDir(), "l3.meh"), 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close(&storage.IndexSnapshotData{}) })
	return engine
}

func TestCreateAndListGraph(t *testing.T) {
	engine := newTestEngine(t)
	graphID, err := CreateGraph(engine, "codebase", model.HypergraphSource{Kind: model.SourcePath, Value: "/src"})
	if err != nil {
		t.Fatal(err)
	}
	if graphID != hash.HashID("codebase") {
		t.Errorf("graph id mismatch: %d", graphID)
	}
	graphs := ListGraphs(engine)
	if len(graphs) != 1 || graphs[0].Name != "codebase" || graphs[0].Source.Kind != model.SourcePath {
		t.Errorf("graphs mismatch: %+v", graphs)
	}
}

func TestCreateNodeAndEdge(t *testing.T) {
	engine := newTestEngine(t)
	graphID, err := CreateGraph(engine, "g1", model.HypergraphSource{Kind: model.SourceManual})
	if err != nil {
		t.Fatal(err)
	}
	graphIDStr := hash.FormatHash(graphID)
	n1, err := CreateNode(engine, graphIDStr, "title-a", "concept", "content-a", []string{"kw1"})
	if err != nil {
		t.Fatal(err)
	}
	n2, err := CreateNode(engine, graphIDStr, "title-b", "concept", "content-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	e1, err := CreateEdge(engine, graphIDStr, model.EdgeRelated, []uint64{n1, n2}, 0.8)
	if err != nil {
		t.Fatal(err)
	}

	nodes := ListNode(engine, graphIDStr)
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
	edges := ListEdge(engine, graphIDStr)
	if len(edges) != 1 || len(edges[0].NodeIDs) != 2 || edges[0].Weight != 0.8 {
		t.Errorf("edges mismatch: %+v", edges)
	}
	if e1 == 0 {
		t.Error("edge id should not be zero")
	}
}

func TestDeleteGraphCascades(t *testing.T) {
	engine := newTestEngine(t)
	g1, _ := CreateGraph(engine, "g1", model.HypergraphSource{Kind: model.SourceManual})
	g2, _ := CreateGraph(engine, "g2", model.HypergraphSource{Kind: model.SourceManual})
	g1Str := hash.FormatHash(g1)
	g2Str := hash.FormatHash(g2)
	n1, _ := CreateNode(engine, g1Str, "t1", "concept", "c", nil)
	n2, _ := CreateNode(engine, g2Str, "t2", "concept", "c", nil)
	CreateEdge(engine, g1Str, model.EdgeRelated, []uint64{n1, n2}, 0.5)

	if !DeleteGraph(engine, g1Str) {
		t.Fatal("DeleteGraph returned false")
	}
	// 图1 及其节点/边删除；图2 及其节点保留
	if engine.Contains(g1) || engine.Contains(n1) {
		t.Error("g1 records should be deleted")
	}
	if !engine.Contains(g2) || !engine.Contains(n2) {
		t.Error("g2 records should survive")
	}
	if len(ListEdge(engine, g1Str)) != 0 {
		t.Error("g1 edges should be gone")
	}
	if len(ListGraphs(engine)) != 1 {
		t.Errorf("expected 1 graph left, got %d", len(ListGraphs(engine)))
	}
}
