// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0
//
// L3 hypergraph E2E test using real Ollama + LLM environment.

package test

import (
	"testing"

	"memhop/api"
	"memhop/internal/common/hash"
	"memhop/internal/query/crud"
	"memhop/test/testsupport"
)

// TestL3Graph exercises all L3 hypergraph APIs end-to-end.
func TestL3Graph(t *testing.T) {
	mh := testsupport.OpenMemHop(t)
	defer mh.Close()

	// === 1. Create a hypergraph slot ===
	slot, err := mh.CreateL3Graph("测试知识图谱")
	if err != nil {
		t.Fatalf("CreateL3Graph failed: %v", err)
	}
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

	if err := mh.AddL3Node(graphID, node1); err != nil {
		t.Fatalf("AddL3Node node1 failed: %v", err)
	}
	if err := mh.AddL3Node(graphID, node2); err != nil {
		t.Fatalf("AddL3Node node2 failed: %v", err)
	}
	if err := mh.AddL3Node(graphID, node3); err != nil {
		t.Fatalf("AddL3Node node3 failed: %v", err)
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

	if err := mh.AddL3Edge(graphID, edge1); err != nil {
		t.Fatalf("AddL3Edge edge1 failed: %v", err)
	}
	if err := mh.AddL3Edge(graphID, edge2); err != nil {
		t.Fatalf("AddL3Edge edge2 failed: %v", err)
	}
	t.Log("Added 2 edges")

	// === 4. Sub-tests ===

	t.Run("GetL3", func(t *testing.T) {
		detail, err := mh.GetL3(graphID)
		if err != nil {
			t.Fatalf("GetL3 failed: %v", err)
		}
		if len(detail.Nodes) != 3 {
			t.Errorf("expected 3 nodes, got %d", len(detail.Nodes))
		}
		if len(detail.Edges) != 2 {
			t.Errorf("expected 2 edges, got %d", len(detail.Edges))
		}
		t.Logf("GetL3 OK: nodes=%d edges=%d", len(detail.Nodes), len(detail.Edges))
	})

	t.Run("SearchL3Nodes", func(t *testing.T) {
		result, err := mh.SearchL3Nodes(memhop.L3SearchQuery{Keyword: "Go"})
		if err != nil {
			t.Fatalf("SearchL3Nodes failed: %v", err)
		}
		if len(result.Nodes) == 0 {
			t.Error("SearchL3Nodes returned no results for keyword 'Go'")
		}
		t.Logf("SearchL3Nodes OK: matched %d nodes", len(result.Nodes))
	})

	t.Run("GraphQuery", func(t *testing.T) {
		startNode := hash.FormatHash(node1.IDHash)
		subgraph, err := mh.GraphQuery(graphID, startNode, 2, nil)
		if err != nil {
			t.Fatalf("GraphQuery failed: %v", err)
		}
		if len(subgraph.Nodes) == 0 {
			t.Error("GraphQuery returned no nodes")
		}
		if len(subgraph.Edges) == 0 {
			t.Error("GraphQuery returned no edges")
		}
		t.Logf("GraphQuery OK: nodes=%d edges=%d", len(subgraph.Nodes), len(subgraph.Edges))
	})

	t.Run("DSLQueryMatch", func(t *testing.T) {
		// MATCH all nodes in the graph
		result, err := mh.DSLQuery("MATCH (n)")
		if err != nil {
			t.Fatalf("DSLQuery MATCH failed: %v", err)
		}
		if result.Nodes == nil || result.Nodes.Total == 0 {
			t.Error("DSLQuery MATCH returned no nodes")
		}
		t.Logf("DSLQuery MATCH OK: %d nodes", result.Nodes.Total)
	})

	t.Run("DSLQueryPath", func(t *testing.T) {
		startHex := hash.FormatHash(node1.IDHash)
		dsl := `PATH FROM "` + startHex + `" DEPTH 2`
		result, err := mh.DSLQuery(dsl)
		if err != nil {
			t.Fatalf("DSLQuery PATH failed: %v", err)
		}
		if result.Hops == nil || result.Hops.Total == 0 {
			t.Error("DSLQuery PATH returned no hops")
		}
		t.Logf("DSLQuery PATH OK: %d hops", result.Hops.Total)
	})

	t.Run("DetectCommunities", func(t *testing.T) {
		result, err := mh.DetectCommunities(graphID, nil)
		if err != nil {
			t.Fatalf("DetectCommunities failed: %v", err)
		}
		if result.TotalNodes != 3 {
			t.Errorf("expected TotalNodes=3, got %d", result.TotalNodes)
		}
		t.Logf("DetectCommunities OK: %d nodes, %d communities",
			result.TotalNodes, len(result.Communities))
	})

	t.Run("ListKnowledge", func(t *testing.T) {
		result, err := mh.ListKnowledge(memhop.KnowledgeListQuery{Page: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("ListKnowledge failed: %v", err)
		}
		if result.Total < 1 {
			t.Errorf("expected at least 1 knowledge graph, got %d", result.Total)
		}
		t.Logf("ListKnowledge OK: total=%d items=%d", result.Total, len(result.Items))
	})

	t.Run("GetKnowledgeNodes", func(t *testing.T) {
		result, err := mh.GetKnowledgeNodes(memhop.KnowledgeNodeQuery{
			ByKeyword: &crud.ByKeywordQuery{
				GraphID: graphID,
				Keyword: "并发",
				Limit:   10,
			},
		})
		if err != nil {
			t.Fatalf("GetKnowledgeNodes failed: %v", err)
		}
		if result.Total == 0 {
			t.Error("GetKnowledgeNodes returned no results for keyword '并发'")
		}
		t.Logf("GetKnowledgeNodes OK: total=%d", result.Total)
	})

	// === 5. Cleanup ===
	t.Run("Cleanup", func(t *testing.T) {
		// Delete edges first, then nodes, then the graph
		if err := mh.DeleteL3Edge(edge1.IDHash); err != nil {
			t.Errorf("DeleteL3Edge edge1 failed: %v", err)
		}
		if err := mh.DeleteL3Edge(edge2.IDHash); err != nil {
			t.Errorf("DeleteL3Edge edge2 failed: %v", err)
		}
		if err := mh.DeleteL3Node(node1.IDHash); err != nil {
			t.Errorf("DeleteL3Node node1 failed: %v", err)
		}
		if err := mh.DeleteL3Node(node2.IDHash); err != nil {
			t.Errorf("DeleteL3Node node2 failed: %v", err)
		}
		if err := mh.DeleteL3Node(node3.IDHash); err != nil {
			t.Errorf("DeleteL3Node node3 failed: %v", err)
		}
		if err := mh.DeleteL3(graphID); err != nil {
			t.Errorf("DeleteL3 failed: %v", err)
		}
		// Verify deletion
		_, err := mh.GetL3(graphID)
		if err == nil {
			t.Error("expected error after DeleteL3, got nil")
		} else {
			t.Logf("DeleteL3 verified: %v", err)
		}
	})
}
