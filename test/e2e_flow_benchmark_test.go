// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// E2E 流程基准测试
//
// 与 e2e_flow_test.go 相同的流程，但使用 mock encoder（离线、可重复），
// 对每个阶段报告耗时和分配量。适合 CI 和非 Ollama 环境。
//
// 运行:
//
//	go test -bench=BenchmarkE2EFlow -benchmem -run=^$ ./test/...

package test

import (
	"context"
	"fmt"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
	"github.com/qyiun666/MemHop/internal/common/hash"
)

// ── 基准数据集 ──────────────────────────────────────────────────────────

var benchDialogues = []struct {
	user  string
	agent string
	label string
}{
	{"今天天气怎么样", "今天晴转多云，最高28度", "天气"},
	{"推荐科幻电影", "推荐《沙丘2》", "电影"},
	{"Go语言怎么入门", "从官方教程开始", "编程"},
	{"AI新进展", "多模态模型进展显著", "AI"},
	{"户外活动推荐", "推荐爬山骑行", "户外"},
}

// 模拟 L3 基准数据
var benchL3Nodes = []struct {
	title    string
	nodeType string
	content  string
	keywords []string
}{
	{"Go语言", "concept", "静态类型编译语言", []string{"Go", "编程语言"}},
	{"并发编程", "concept", "多任务执行范式", []string{"并发", "goroutine"}},
	{"微服务架构", "concept", "独立部署小型服务", []string{"微服务", "分布式"}},
}

// ── 基准测试套件 ────────────────────────────────────────────────────────

// BenchmarkE2EFlow 测量整个 E2E 流程各阶段的性能。
// 使用 b.Run() 子基准，每个子基准报告自己的 ns/op 和 allocs/op。
func BenchmarkE2EFlow(b *testing.B) {
	// ── 预热 ──
	b.StopTimer()
	mh := openMemHopMockTB(b)
	b.StartTimer()

	b.Run("OpenClose", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m := openMemHopMockTB(b)
			m.Checkpoint()
			m.Close()
		}
	})

	b.Run("L0_UpdateProfile", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			mm := openMemHopMockTB(b)
			b.StartTimer()

			_, err := mm.Topic(memhop.TopicOp{
				Kind: memhop.TOpSetProfile,
				ProfileDelta: &memhop.ProfileDelta{
					Name: strPtr("助手"),
					Role: strPtr("AI助手"),
				},
			})
			if err != nil {
				b.Fatalf("Topic(TOpSetProfile): %v", err)
			}
			_, err = mm.Get(memhop.LayerProfile, "")
			if err != nil {
				b.Fatalf("Get(LayerProfile): %v", err)
			}
			mm.Close()
		}
	})

	b.Run("Search_AutoCreate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			mm := openMemHopMockTB(b)
			b.StartTimer()

			result, err := mm.Search(memhop.SearchQuery{
				Text:       fmt.Sprintf("基准查询%d", i),
				AutoCreate: true,
			})
			if err != nil {
				b.Fatalf("Search: %v", err)
			}
			if len(result.Contexts) == 0 {
				b.Fatal("Search 返回 0 个 context")
			}
			_ = result.Contexts[0].ID

			b.StopTimer()
			mm.Close()
		}
	})

	b.Run("Search_ExistingTopic", func(b *testing.B) {
		b.StopTimer()
		mm := openMemHopMockTB(b)
		// 预先创建话题
		res, err := mm.Search(memhop.SearchQuery{
			Text:       "预写入话题",
			AutoCreate: true,
		})
		if err != nil || len(res.Contexts) == 0 {
			b.Fatalf("预热 Search: %v", err)
		}
		_ = mm.Checkpoint()
		b.StartTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, err := mm.Search(memhop.SearchQuery{Text: "预写入话题"})
			if err != nil {
				b.Fatalf("Search: %v", err)
			}
		}

		b.StopTimer()
		mm.Close()
	})

	mh.Close()
}

// BenchmarkE2EFlow_FullCycle 测量完整 Search+Update+L2+L4+Dream 循环性能。
func BenchmarkE2EFlow_FullCycle(b *testing.B) {
	for n := 0; n < b.N; n++ {
		b.StopTimer()
		mh := openMemHopMockTB(b)

		// L0
		_, err := mh.Topic(memhop.TopicOp{
			Kind: memhop.TOpSetProfile,
			ProfileDelta: &memhop.ProfileDelta{
				Name: strPtr("基准助手"),
				Role: strPtr("AI"),
			},
		})
		if err != nil {
			b.Fatalf("Topic: %v", err)
		}

		b.StartTimer()
		b.ReportAllocs()

		// 写入所有对话
		var topicIDs []string
		for _, d := range benchDialogues {
			result, err := mh.Search(memhop.SearchQuery{
				Text:       d.user,
				AutoCreate: true,
			})
			if err != nil || len(result.Contexts) == 0 {
				b.Fatalf("Search(%s): %v", d.label, err)
			}
			topicID := result.Contexts[0].ID
			topicIDs = append(topicIDs, topicID)

			err = mh.Update(topicID, d.agent, 0)
			if err != nil {
				b.Fatalf("Update(%s): %v", d.label, err)
			}
		}

		// L2 List + L4 List
		listRes, err := mh.List(memhop.LayerTopic, memhop.ListRequest{
			Topic: &memhop.TopicListQuery{Page: 1, PageSize: 100},
		})
		if err != nil {
			b.Fatalf("List(LayerTopic): %v", err)
		}
		if len(topicIDs) > 0 {
			for _, tid := range topicIDs {
				tidCopy := tid
				mh.Get(memhop.LayerTopic, tid)
				mh.List(memhop.LayerArchive, memhop.ListRequest{
					Archive: &memhop.ArchiveQuery{
						TopicID:  &tidCopy,
						Page:     1,
						PageSize: 10,
					},
				})
			}
		}
		_ = listRes

		// Dream
		mh.Dream(context.Background(), &memhop.DreamOptions{
			SkipDistill: true,
		})

		// L1 + L0 + L2 + L4 查看
		mh.Get(memhop.LayerScene, "")
		mh.Get(memhop.LayerProfile, "")
		mh.List(memhop.LayerTopic, memhop.ListRequest{
			Topic: &memhop.TopicListQuery{Page: 1, PageSize: 100},
		})
		for _, tid := range topicIDs {
			tidCopy := tid
			mh.List(memhop.LayerArchive, memhop.ListRequest{
				Archive: &memhop.ArchiveQuery{
					TopicID:  &tidCopy,
					Page:     1,
					PageSize: 10,
				},
			})
		}

		b.StopTimer()
		mh.Checkpoint()
		mh.Close()
	}
}

// BenchmarkE2EFlow_L3 测量 L3 超图操作的吞吐量。
func BenchmarkE2EFlow_L3(b *testing.B) {
	b.StopTimer()
	mh := openMemHopMockTB(b)
	defer mh.Close()

	res, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpCreateGraph, Name: "bench_l3"})
	if err != nil {
		b.Fatalf("KOpCreateGraph: %v", err)
	}
	graphHash := res.Slot.IDHash
	graphID := hash.FormatHash(graphHash)

	b.StartTimer()
	b.ReportAllocs()

	b.Run("L3_AddNode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			n := &memhop.HypergraphNode{
				IDHash:     hash.HashID(fmt.Sprintf("node_%d", i)),
				GraphID:    graphHash,
				Title:      fmt.Sprintf("Node%d", i),
				NodeType:   "concept",
				Content:    fmt.Sprintf("Content %d", i),
				Keywords:   []string{"bench", "test"},
				Importance: 0.5,
			}
			if _, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpAddNode, GraphID: graphID, Node: n}); err != nil {
				b.Fatalf("KOpAddNode: %v", err)
			}
		}
	})

	b.Run("L3_Search", func(b *testing.B) {
		b.ReportAllocs()
		// 先添加一个节点确保可搜索
		n := &memhop.HypergraphNode{
			IDHash:     hash.HashID("search_target"),
			GraphID:    graphHash,
			Title:      "search_target",
			NodeType:   "concept",
			Content:    "benchmark target",
			Keywords:   []string{"target", "bench"},
			Importance: 0.9,
		}
		mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpAddNode, GraphID: graphID, Node: n})

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := mh.Knowledge(memhop.KnowledgeOp{
				Kind:        memhop.KOpSearch,
				SearchQuery: &memhop.L3SearchQuery{Keyword: "target", Limit: 10},
			})
			if err != nil {
				b.Fatalf("KOpSearch: %v", err)
			}
		}
	})

	b.Run("L3_GraphQuery", func(b *testing.B) {
		b.ReportAllocs()
		startHex := hash.FormatHash(hash.HashID("search_target"))
		for i := 0; i < b.N; i++ {
			_, err := mh.Knowledge(memhop.KnowledgeOp{
				Kind:      memhop.KOpGraphQuery,
				GraphID:   graphID,
				StartNode: startHex,
				MaxDepth:  1,
			})
			if err != nil {
				b.Fatalf("KOpGraphQuery: %v", err)
			}
		}
	})
}

// BenchmarkE2EFlow_L5 测量 L5 动作链操作的吞吐量。
func BenchmarkE2EFlow_L5(b *testing.B) {
	b.StopTimer()
	mh := openMemHopMockTB(b)
	defer mh.Close()

	b.StartTimer()
	b.ReportAllocs()

	b.Run("L5_CreateChain", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, err := mh.Crystal(memhop.CrystalOp{
				Kind: memhop.COpCreateChain,
				ChainInput: &memhop.L5ChainInput{
					Title:   fmt.Sprintf("chain_%d", i),
					Trigger: "test trigger",
					Steps: []memhop.L5StepInput{
						{Action: "step1"},
					},
				},
			})
			if err != nil {
				b.Fatalf("COpCreateChain: %v", err)
			}
		}
	})

	b.Run("L5_AppendStep", func(b *testing.B) {
		b.ReportAllocs()
		// 先创建一条链
		r, err := mh.Crystal(memhop.CrystalOp{
			Kind: memhop.COpCreateChain,
			ChainInput: &memhop.L5ChainInput{
				Title:   "bench_chain",
				Trigger: "test",
			},
		})
		if err != nil {
			b.Fatalf("COpCreateChain: %v", err)
		}
		chainID := r.ChainID

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := mh.Crystal(memhop.CrystalOp{
				Kind:      memhop.COpAppendStep,
				ChainID:   chainID,
				StepInput: &memhop.L5StepInput{Action: fmt.Sprintf("step_%d", i)},
			})
			if err != nil {
				b.Fatalf("COpAppendStep: %v", err)
			}
		}
	})

	b.Run("L5_UpdateConfidence", func(b *testing.B) {
		b.ReportAllocs()
		r, _ := mh.Crystal(memhop.CrystalOp{
			Kind: memhop.COpCreateChain,
			ChainInput: &memhop.L5ChainInput{
				Title:   "conf_chain",
				Trigger: "test",
			},
		})
		chainID := r.ChainID

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			mh.Crystal(memhop.CrystalOp{
				Kind:    memhop.COpUpdateConfidence,
				ChainID: chainID,
				Success: i%2 == 0,
			})
		}
	})

	b.Run("L5_IncrTrigger", func(b *testing.B) {
		b.ReportAllocs()
		r, _ := mh.Crystal(memhop.CrystalOp{
			Kind: memhop.COpCreateChain,
			ChainInput: &memhop.L5ChainInput{
				Title:   "trigger_chain",
				Trigger: "test",
			},
		})
		chainID := r.ChainID

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			mh.Crystal(memhop.CrystalOp{Kind: memhop.COpIncrTrigger, ChainID: chainID})
		}
	})
}

// ── 辅助 ───────────────────────────────────────────────────────────────

// openMemHopMockTB 定义在 benchmark_search_test.go 中
