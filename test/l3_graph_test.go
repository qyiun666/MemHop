// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0
//
// L3 hypergraph E2E test using real Ollama + LLM environment.
// v0.60.0: rewritten to use the unified Knowledge(op) domain method plus
// the generic Get/Delete/List entry points.

package test

import (
	"testing"

	"github.com/qyiun666/MemHop/api"
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/query/crud"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// TestL3Graph exercises all L3 hypergraph APIs end-to-end.
func TestL3Graph(t *testing.T) {
	mh := testsupport.OpenMemHop(t)
	defer mh.Close()

	// === 1. Create a hypergraph slot ===
	res, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpCreateGraph, Name: "测试知识图谱"})
	if err != nil {
		t.Fatalf("Knowledge(KOpCreateGraph) failed: %v", err)
	}
	slot := res.Slot
	graphID := hash.FormatHash(slot.IDHash)
	graphHash := slot.IDHash
	t.Logf("Created L3 graph: id=%s name=%s", graphID, slot.Name)

	// === 2. Add 3 nodes ===
	node1 := &memhop.HypergraphNode{
		IDHash:     hash.HashID("Go语言"),
		GraphID:    graphHash,
		Title:      "Go语言",
		NodeType:   "concept",
		Content:    "Go是Google开发的静态类型编译语言，以并发和简洁著称",
		Keywords:   []string{"Go", "编程语言", "并发", "goroutine"},
		Importance: 0.8,
		Summary:    strPtr("Go语言概述"),
	}
	node2 := &memhop.HypergraphNode{
		IDHash:     hash.HashID("并发编程"),
		GraphID:    graphHash,
		Title:      "并发编程",
		NodeType:   "concept",
		Content:    "并发编程是一种同时执行多个计算任务的编程范式",
		Keywords:   []string{"并发", "并行", "goroutine", "channel"},
		Importance: 0.75,
		Summary:    strPtr("并发编程概述"),
	}
	node3 := &memhop.HypergraphNode{
		IDHash:     hash.HashID("微服务架构"),
		GraphID:    graphHash,
		Title:      "微服务架构",
		NodeType:   "concept",
		Content:    "微服务架构将应用划分为多个独立部署的小型服务",
		Keywords:   []string{"微服务", "分布式", "架构"},
		Importance: 0.7,
		Summary:    strPtr("微服务架构概述"),
	}

	for _, n := range []*memhop.HypergraphNode{node1, node2, node3} {
		if _, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpAddNode, GraphID: graphID, Node: n}); err != nil {
			t.Fatalf("Knowledge(KOpAddNode) failed: %v", err)
		}
	}
	t.Log("Added 3 nodes")

	// === 3. Add 2 edges ===
	edge1 := &memhop.HypergraphEdge{
		IDHash:  hash.HashID("go-concurrent"),
		GraphID: graphHash,
		Kind:    memhop.EdgeRelated,
		NodeIDs: []uint64{node1.IDHash, node2.IDHash},
		Weight:  0.9,
		Label:   strPtr("related"),
	}
	edge2 := &memhop.HypergraphEdge{
		IDHash:  hash.HashID("concurrent-microservice"),
		GraphID: graphHash,
		Kind:    memhop.EdgeRelated,
		NodeIDs: []uint64{node2.IDHash, node3.IDHash},
		Weight:  0.85,
		Label:   strPtr("related"),
	}

	for _, e := range []*memhop.HypergraphEdge{edge1, edge2} {
		if _, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpAddEdge, GraphID: graphID, Edge: e}); err != nil {
			t.Fatalf("Knowledge(KOpAddEdge) failed: %v", err)
		}
	}
	t.Log("Added 2 edges")

	// === 4. Sub-tests ===

	t.Run("GetKnowledge", func(t *testing.T) {
		r, err := mh.Get(memhop.LayerKnowledge, graphID)
		if err != nil {
			t.Fatalf("Get(LayerKnowledge) failed: %v", err)
		}
		detail := r.Knowledge
		if len(detail.Nodes) != 3 {
			t.Errorf("expected 3 nodes, got %d", len(detail.Nodes))
		}
		if len(detail.Edges) != 2 {
			t.Errorf("expected 2 edges, got %d", len(detail.Edges))
		}
		t.Logf("Get(LayerKnowledge) OK: nodes=%d edges=%d", len(detail.Nodes), len(detail.Edges))
	})

	t.Run("SearchKnowledge", func(t *testing.T) {
		r, err := mh.Knowledge(memhop.KnowledgeOp{
			Kind:        memhop.KOpSearch,
			SearchQuery: &memhop.L3SearchQuery{Keyword: "Go"},
		})
		if err != nil {
			t.Fatalf("Knowledge(KOpSearch) failed: %v", err)
		}
		if len(r.Search.Nodes) == 0 {
			t.Error("KOpSearch returned no results for keyword 'Go'")
		}
		t.Logf("KOpSearch OK: matched %d nodes", len(r.Search.Nodes))
	})

	t.Run("GraphQuery", func(t *testing.T) {
		startNode := hash.FormatHash(node1.IDHash)
		r, err := mh.Knowledge(memhop.KnowledgeOp{
			Kind:      memhop.KOpGraphQuery,
			GraphID:   graphID,
			StartNode: startNode,
			MaxDepth:  2,
		})
		if err != nil {
			t.Fatalf("Knowledge(KOpGraphQuery) failed: %v", err)
		}
		if len(r.Subgraph.Nodes) == 0 {
			t.Error("KOpGraphQuery returned no nodes")
		}
		if len(r.Subgraph.Edges) == 0 {
			t.Error("KOpGraphQuery returned no edges")
		}
		t.Logf("KOpGraphQuery OK: nodes=%d edges=%d", len(r.Subgraph.Nodes), len(r.Subgraph.Edges))
	})

	t.Run("DSLQueryMatch", func(t *testing.T) {
		r, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpDSL, DSLString: "MATCH (n)"})
		if err != nil {
			t.Fatalf("Knowledge(KOpDSL) MATCH failed: %v", err)
		}
		if r.DSL.Nodes == nil || r.DSL.Nodes.Total == 0 {
			t.Error("KOpDSL MATCH returned no nodes")
		}
		t.Logf("KOpDSL MATCH OK: %d nodes", r.DSL.Nodes.Total)
	})

	t.Run("DSLQueryPath", func(t *testing.T) {
		startHex := hash.FormatHash(node1.IDHash)
		dslStr := `PATH FROM "` + startHex + `" DEPTH 2`
		r, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpDSL, DSLString: dslStr})
		if err != nil {
			t.Fatalf("Knowledge(KOpDSL) PATH failed: %v", err)
		}
		if r.DSL.Hops == nil || r.DSL.Hops.Total == 0 {
			t.Error("KOpDSL PATH returned no hops")
		}
		t.Logf("KOpDSL PATH OK: %d hops", r.DSL.Hops.Total)
	})

	t.Run("DetectCommunities", func(t *testing.T) {
		r, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpDetectCommunities, GraphID: graphID})
		if err != nil {
			t.Fatalf("Knowledge(KOpDetectCommunities) failed: %v", err)
		}
		if r.Community.TotalNodes != 3 {
			t.Errorf("expected TotalNodes=3, got %d", r.Community.TotalNodes)
		}
		t.Logf("KOpDetectCommunities OK: %d nodes, %d communities",
			r.Community.TotalNodes, len(r.Community.Communities))
	})

	t.Run("ListKnowledge", func(t *testing.T) {
		r, err := mh.List(memhop.LayerKnowledge, memhop.ListRequest{
			Knowledge: &memhop.KnowledgeListQuery{Page: 1, PageSize: 10},
		})
		if err != nil {
			t.Fatalf("List(LayerKnowledge) failed: %v", err)
		}
		if r.Knowledge.Total < 1 {
			t.Errorf("expected at least 1 knowledge graph, got %d", r.Knowledge.Total)
		}
		t.Logf("List(LayerKnowledge) OK: total=%d items=%d", r.Knowledge.Total, len(r.Knowledge.Items))
	})

	t.Run("GetKnowledgeNodes", func(t *testing.T) {
		r, err := mh.Knowledge(memhop.KnowledgeOp{
			Kind: memhop.KOpGetNodes,
			NodesQuery: &memhop.KnowledgeNodeQuery{
				ByKeyword: &crud.ByKeywordQuery{
					GraphID: graphID,
					Keyword: "并发",
					Limit:   10,
				},
			},
		})
		if err != nil {
			t.Fatalf("Knowledge(KOpGetNodes) failed: %v", err)
		}
		if r.Nodes.Total == 0 {
			t.Error("KOpGetNodes returned no results for keyword '并发'")
		}
		t.Logf("KOpGetNodes OK: total=%d", r.Nodes.Total)
	})

	// === 5. Cleanup ===
	t.Run("Cleanup", func(t *testing.T) {
		// Delete edges first, then nodes, then the graph
		for _, eh := range []uint64{edge1.IDHash, edge2.IDHash} {
			if _, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpDeleteEdge, EdgeHash: eh}); err != nil {
				t.Errorf("KOpDeleteEdge %x failed: %v", eh, err)
			}
		}
		for _, nh := range []uint64{node1.IDHash, node2.IDHash, node3.IDHash} {
			if _, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpDeleteNode, NodeHash: nh}); err != nil {
				t.Errorf("KOpDeleteNode %x failed: %v", nh, err)
			}
		}
		if err := mh.Delete(memhop.LayerKnowledge, graphID); err != nil {
			t.Errorf("Delete(LayerKnowledge) failed: %v", err)
		}
		// Verify deletion
		if _, err := mh.Get(memhop.LayerKnowledge, graphID); err == nil {
			t.Error("expected error after Delete(LayerKnowledge), got nil")
		} else {
			t.Logf("Delete(LayerKnowledge) verified: %v", err)
		}
	})
}
