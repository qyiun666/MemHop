// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

// E2E 端到端流程测试
//
// 严格遵循用户的测试脚流程：
//
//	┌─ 1. 打开数据库
//	├─ 2. 更新 L0 画像
//	├─ 3. Search 并检查返回内容
//	├─ 4. 查看 L2 列表 + 指定话题 + L4
//	├─ 5. 循环 3-4, 触发 Dream
//	├─ 6. 查看 L2/L1/L0/L4
//	├─ 7. 循环 3-6
//	├─ 8. 关闭数据库
//	└─ 9. L3 / L5 单独测试
//
// 依赖: Ollama(bge-m3) + LLM(API key)

package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// ── 测试数据 ────────────────────────────────────────────────────────────

type dialogueRound struct {
	user  string
	agent string
	desc  string
}

var flowDialogues = []dialogueRound{
	{"今天天气怎么样，北京明天会下雨吗", "今天北京晴转多云，最高28度，明天可能有小雨", "天气"},
	{"推荐一部好看的科幻电影", "推荐《沙丘2》，画面震撼，剧情宏大，IMAX效果极佳", "电影"},
	{"Go语言怎么入门学习", "建议从官方A Tour of Go开始，然后做小项目练习", "编程学习"},
	{"最近AI领域有什么新进展", "2026年多模态模型进展显著，Claude和GPT持续迭代", "AI技术"},
	{"周末想去户外活动有什么推荐", "推荐去爬山或骑行，最近天气适合户外运动", "户外活动"},
}

// ── 主流程 ──────────────────────────────────────────────────────────────

func TestE2EFlow(t *testing.T) {
	// ==================================================================
	// 步骤 1: 打开数据库
	// ==================================================================
	mh := testsupport.OpenMemHop(t)
	defer mh.Close()
	t.Log("✓ [1] 数据库打开成功")

	// ==================================================================
	// 步骤 2: 更新 L0 画像
	// ==================================================================
	t.Log("")
	t.Log("████ 步骤 2: 更新 L0 画像")

	_, err := mh.Topic(memhop.TopicOp{
		Kind: memhop.TOpSetProfile,
		ProfileDelta: &memhop.ProfileDelta{
			Name:        strPtr("MemHop助手"),
			Role:        strPtr("AI 助手"),
			Personality: strPtr("友善、专业、乐于助人"),
		},
	})
	if err != nil {
		t.Fatalf("[2] Topic(TOpSetProfile) 失败: %v", err)
	}

	profRes, err := mh.Get(memhop.LayerProfile, "")
	if err != nil {
		t.Fatalf("[2] Get(LayerProfile) 失败: %v", err)
	}
	profile := profRes.Profile
	if profile.Name != "MemHop助手" {
		t.Errorf("[2] Name 期望 %q, 实际 %q", "MemHop助手", profile.Name)
	}
	t.Logf("✓ [2] L0 画像: Name=%q Role=%q Personality=%q", profile.Name, profile.Role, profile.Personality)

	// ==================================================================
	// 主循环：步骤 3-7
	// ==================================================================
	t.Log("")
	t.Log("████ 主循环: 步骤 3 → 4 → 5 → 6 (循环 2 轮)")

	type topicInfo struct {
		id    string
		scene string
	}
	var allTopics []topicInfo
	dreamCount := 0

	for loop := 1; loop <= 2; loop++ {
		t.Logf("")
		t.Logf("══════ 外层循环 %d/2 ══════", loop)

		// ── 内层: 积累对话轮次 ──
		for i, round := range flowDialogues {
			t.Logf("")
			t.Logf("═══ 内层 %d/%d: %s ═══", i+1, len(flowDialogues), round.desc)

			// ==========================================================
			// 步骤 3: Search + Agent Update + 检查返回内容
			// ==========================================================
			t.Log("--- [3] Search + 检查返回内容 ---")

			result, err := mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(),
				Text:       round.user,
				AutoCreate: true,
			})
			if err != nil {
				t.Fatalf("[3] Search(%q) 失败: %v", round.user, err)
			}
			if len(result.Contexts) == 0 {
				t.Fatalf("[3] Search(%q) 返回 0 个 context", round.user)
			}

			ctx0 := result.Contexts[0]
			t.Logf("  话题 ID: %s", ctx0.ID)
			t.Logf("  场景 ID: %s", ctx0.SceneID)
			t.Logf("  Depth:   %d", ctx0.Depth)
			t.Logf("  Score:   %.4f", ctx0.RetrievalScore)
			t.Logf("  UserKw:  %v", ctx0.UserKeywords)
			t.Logf("  AgentKw: %v", ctx0.AgentKeywords)
			t.Logf("  L4Refs:  %d 条", len(ctx0.L4Refs))
			t.Logf("  Assoc:   %d 条", len(result.AssociatedContexts))

			// 基本字段非空检测
			if ctx0.ID == "" {
				t.Errorf("[3] Context ID 为空")
			}
			if len(ctx0.UserKeywords) == 0 && len(ctx0.AgentKeywords) == 0 {
				t.Logf("[3] △ User/AgentKeywords 均为空（新话题未积累数据）")
			}

			// Agent 侧写入（Update）
			err = mh.Update(ctx0.ID, round.agent, time.Now().UnixMilli())
			if err != nil {
				t.Fatalf("[3] Update 失败: %v", err)
			}
			t.Logf("✓ [3] Update Agent 回复成功")

			// 记录话题
			allTopics = append(allTopics, topicInfo{id: ctx0.ID, scene: ctx0.SceneID})

			// ==========================================================
			// 步骤 4: 查看 L2 列表 + L2 指定话题 + L4
			// ==========================================================
			t.Log("--- [4] 查看 L2 列表 + 指定话题 + L4 ---")

			// L2 列表
			listRes, err := mh.List(memhop.LayerTopic, memhop.ListRequest{
				Topic: &memhop.TopicListQuery{Page: 1, PageSize: 100},
			})
			if err != nil {
				t.Fatalf("[4] List(LayerTopic) 失败: %v", err)
			}
			t.Logf("  L2 列表: Total=%d", listRes.Topics.Total)
			for ti, topic := range listRes.Topics.Items {
				t.Logf("    Topic[%d]: ID=%s Depth=%d TurnCnt=%d L4=%d",
					ti, topic.ID[:12], topic.Depth, topic.TurnCount, topic.L4Count)
			}

			// L2 指定话题详情
			detailRes, err := mh.Get(memhop.LayerTopic, ctx0.ID)
			if err != nil {
				t.Fatalf("[4] Get(LayerTopic, %s) 失败: %v", ctx0.ID, err)
			}
			detail := detailRes.Topic
			if detail == nil {
				t.Fatal("[4] TopicDetail 为 nil")
			}
			l4Count := len(detail.UserL4Refs) + len(detail.AgentL4Refs)
			l3Count := len(detail.UserL3Refs) + len(detail.AgentL3Refs)
			t.Logf("  指定话题 %s: Depth=%d L4=%d L3=%d FusedKw=%v",
				detail.ID[:12], detail.Depth, l4Count, l3Count, detail.FusedKeywords)

			// L4 Archive 查看
			topicIDCopy := ctx0.ID
			archRes, err := mh.List(memhop.LayerArchive, memhop.ListRequest{
				Archive: &memhop.ArchiveQuery{
					TopicID:  &topicIDCopy,
					Page:     1,
					PageSize: 50,
				},
			})
			if err != nil {
				t.Fatalf("[4] List(LayerArchive, TopicID=%s) 失败: %v", ctx0.ID, err)
			}
			archives := archRes.Archives
			t.Logf("  L4 Archive: Total=%d", archives.Total)
			for ai, a := range archives.Items {
				content := a.Content
				if len(content) > 60 {
					content = content[:60] + "..."
				}
				roleLabel := "user"
				if a.Role == 1 {
					roleLabel = "agent"
				}
				t.Logf("    Arch[%d]: ID=%s Role=%s Content=%q", ai, a.ID[:12], roleLabel, content)
			}

			t.Logf("✓ [4] L2/L4 查看完成")
		}

		// ==========================================================
		// 步骤 5: 触发 Dream
		// ==========================================================
		t.Log("")
		t.Log("--- [5] 触发 Dream ---")

		report, err := mh.Dream(context.Background(), nil)
		if err != nil {
			t.Logf("  Dream 结果: %v (可能是 LLM 解析失败，非致命)", err)
		} else {
			dreamCount++
			t.Logf("  ✓ Dream[%d] 完成:", dreamCount)
			t.Logf("    ConsolidatedCount=%d", report.ConsolidatedCount)
			t.Logf("    L1DecayedNodes=%d", report.L1DecayedNodes)
			for si, stage := range report.Stages {
				t.Logf("    Stage[%d] %s: %s (%dms)", si, stage.Name, stage.Status, stage.DurationMs)
			}
		}

		// ==========================================================
		// 步骤 6: 查看 L2 / L1 / L0 / L4
		// ==========================================================
		t.Log("")
		t.Log("--- [6] 查看 L2 / L1 / L0 / L4 ---")

		// L2
		listRes2, err := mh.List(memhop.LayerTopic, memhop.ListRequest{
			Topic: &memhop.TopicListQuery{Page: 1, PageSize: 100},
		})
		if err != nil {
			t.Fatalf("[6] List(LayerTopic) 失败: %v", err)
		}
		t.Logf("  L2: Total=%d", listRes2.Topics.Total)
		for ti, topic := range listRes2.Topics.Items {
			fused := ""
			if len(topic.FusedKeywords) > 0 {
				fused = fmt.Sprintf(" FusedKw=%v", topic.FusedKeywords)
			}
			t.Logf("    Topic[%d]: ID=%s Depth=%d TurnCnt=%d L4=%d%s",
				ti, topic.ID[:12], topic.Depth, topic.TurnCount, topic.L4Count, fused)
		}

		// L1 Scene Graph
		l1Res, err := mh.Get(memhop.LayerScene, "")
		if err != nil {
			t.Fatalf("[6] Get(LayerScene) 失败: %v", err)
		}
		l1Graph := l1Res.SceneGraph
		if l1Graph != nil {
			t.Logf("  L1: Nodes=%d Edges=%d", len(l1Graph.Nodes), len(l1Graph.Edges))
			for ni, node := range l1Graph.Nodes {
				t.Logf("    Node[%d]: ID=%s Scene=%s Depth=%d Topics=%v",
					ni, node.ID[:12], node.SceneID[:12], node.Depth, node.TopicIDs)
			}
			for ei, edge := range l1Graph.Edges {
				t.Logf("    Edge[%d]: ID=%s Kind=%s Weight=%.2f",
					ei, edge.ID[:12], edge.Kind, edge.Weight)
			}
		}

		// L0 Profile
		profRes2, err := mh.Get(memhop.LayerProfile, "")
		if err != nil {
			t.Fatalf("[6] Get(LayerProfile) 失败: %v", err)
		}
		p2 := profRes2.Profile
		if p2 != nil {
			t.Logf("  L0: Name=%q Role=%q", p2.Name, p2.Role)
			if len(p2.Lexicon) > 0 {
				t.Logf("      Lexicon=%v", p2.Lexicon)
			}
			if len(p2.StyleTraits) > 0 {
				t.Logf("      StyleTraits=%v", p2.StyleTraits)
			}
			if len(p2.EmotionPatterns) > 0 {
				t.Logf("      EmotionPatterns=%v", p2.EmotionPatterns)
			}
		}

		// L4 按场景汇总
		t.Logf("  L4 (按话题):")
		for _, ti := range allTopics {
			tid := ti.id
			tidCopy := tid
			archRes2, err := mh.List(memhop.LayerArchive, memhop.ListRequest{
				Archive: &memhop.ArchiveQuery{
					TopicID:  &tidCopy,
					Page:     1,
					PageSize: 10,
				},
			})
			if err != nil {
				continue
			}
			if archRes2.Archives.Total > 0 {
				t.Logf("    话题 %s: %d 条归档", tid[:12], archRes2.Archives.Total)
			}
		}

		t.Logf("✓ [6] L2/L1/L0/L4 查看完成")
	}

	// ==================================================================
	// 步骤 7: 循环完成 — 统计
	// ==================================================================
	t.Log("")
	t.Log("████ 步骤 7: 循环完成 (2 轮 × 5 对话 = 10 次搜索)")

	health, err := mh.HealthCheck()
	if err != nil {
		t.Fatalf("HealthCheck 失败: %v", err)
	}
	t.Logf("  总 Dream 次数: %d", dreamCount)
	t.Logf("  各层计数:")
	t.Logf("    L0 Profile:  %d", health.LayerCounts["l0_profile"])
	t.Logf("    L1 Engram:   %d", health.LayerCounts["l1_engram"])
	t.Logf("    L2 Topic:    %d", health.LayerCounts["l2_topic"])
	t.Logf("    L3 Knowledge:%d", health.LayerCounts["l3_knowledge"])
	t.Logf("    L4 Archive:  %d", health.LayerCounts["l4_archive"])
	t.Logf("    L5 Crystal:  %d", health.LayerCounts["l5_crystal"])
	t.Logf("  DB Size: %d bytes", health.DBSizeBytes)

	// ==================================================================
	// 步骤 8: 关闭数据库
	// ==================================================================
	t.Log("")
	t.Log("████ 步骤 8: 关闭数据库")

	err = mh.Checkpoint()
	if err != nil {
		t.Fatalf("[8] Checkpoint 失败: %v", err)
	}
	t.Log("  ✓ Checkpoint 完成")

	err = mh.Close()
	if err != nil {
		t.Fatalf("[8] Close 失败: %v", err)
	}
	t.Log("✓ [8] 数据库关闭成功")

	t.Log("")
	t.Log("✓ 主 E2E 流程测试完成")
}

// ── 步骤 9: L3 单独测试 ────────────────────────────────────────────────

func TestE2EFlow_L3(t *testing.T) {
	mh := testsupport.OpenMemHop(t)
	defer mh.Close()
	t.Log("████ 步骤 9a: L3 超图单独测试")

	// 9a-1: 创建超图
	res, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpCreateGraph, Name: "e2e_test_knowledge"})
	if err != nil {
		t.Fatalf("[L3-1] KOpCreateGraph 失败: %v", err)
	}
	graphHash := res.Slot.IDHash
	graphID := hash.FormatHash(graphHash)
	t.Logf("  [L3-1] 创建超图: ID=%s Name=%s", graphID, res.Slot.Name)

	// 9a-2: 添加节点
	node1 := &memhop.HypergraphNode{
		IDHash:     hash.HashID("Go语言"),
		GraphID:    graphHash,
		Title:      "Go语言",
		NodeType:   "concept",
		Content:    "Go是Google开发的静态类型编译语言",
		Keywords:   []string{"Go", "编程语言"},
		Importance: 0.8,
	}
	node2 := &memhop.HypergraphNode{
		IDHash:     hash.HashID("并发编程"),
		GraphID:    graphHash,
		Title:      "并发编程",
		NodeType:   "concept",
		Content:    "并发编程是一种同时执行多个计算任务的编程范式",
		Keywords:   []string{"并发", "goroutine"},
		Importance: 0.75,
	}

	for _, n := range []*memhop.HypergraphNode{node1, node2} {
		if _, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpAddNode, GraphID: graphID, Node: n}); err != nil {
			t.Fatalf("[L3-2] KOpAddNode(%s) 失败: %v", n.Title, err)
		}
	}
	t.Logf("  [L3-2] 添加 2 个节点: %s, %s", node1.Title, node2.Title)

	// 9a-3: 添加边
	edge := &memhop.HypergraphEdge{
		IDHash:  hash.HashID("go-concurrent"),
		GraphID: graphHash,
		Kind:    memhop.EdgeRelated,
		NodeIDs: []uint64{node1.IDHash, node2.IDHash},
		Weight:  0.9,
	}
	if _, err := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpAddEdge, GraphID: graphID, Edge: edge}); err != nil {
		t.Fatalf("[L3-3] KOpAddEdge 失败: %v", err)
	}
	t.Logf("  [L3-3] 添加边: %s <-> %s", node1.Title, node2.Title)

	// 9a-4: 搜索节点
	searchRes, err := mh.Knowledge(memhop.KnowledgeOp{
		Kind:        memhop.KOpSearch,
		SearchQuery: &memhop.L3SearchQuery{Keyword: "Go", Limit: 10},
	})
	if err != nil {
		t.Fatalf("[L3-4] KOpSearch 失败: %v", err)
	}
	if len(searchRes.Search.Nodes) == 0 {
		t.Error("[L3-4] KOpSearch 未返回结果")
	} else {
		t.Logf("  [L3-4] 搜索 'Go': %d 个结果 (hash IDs)", len(searchRes.Search.Nodes))
		for ni, nh := range searchRes.Search.Nodes {
			t.Logf("    Node[%d]: hash=%s", ni, hash.FormatHash(nh))
		}
	}

	// 9a-5: 图查询 (BFS)
	startNodeHex := hash.FormatHash(node1.IDHash)
	subRes, err := mh.Knowledge(memhop.KnowledgeOp{
		Kind:      memhop.KOpGraphQuery,
		GraphID:   graphID,
		StartNode: startNodeHex,
		MaxDepth:  2,
	})
	if err != nil {
		t.Fatalf("[L3-5] KOpGraphQuery 失败: %v", err)
	}
	if subRes.Subgraph != nil {
		t.Logf("  [L3-5] 图查询: Nodes=%d Edges=%d", len(subRes.Subgraph.Nodes), len(subRes.Subgraph.Edges))
	}

	// 9a-6: DSL 查询
	dslRes, err := mh.Knowledge(memhop.KnowledgeOp{
		Kind:      memhop.KOpDSL,
		DSLString: fmt.Sprintf(`PATH FROM "%s" DEPTH 2`, startNodeHex),
	})
	if err != nil {
		t.Fatalf("[L3-6] KOpDSL 失败: %v", err)
	}
	if dslRes.DSL != nil && dslRes.DSL.Hops != nil {
		t.Logf("  [L3-6] DSL PATH: %d hops", dslRes.DSL.Hops.Total)
	}

	// 9a-7: 社区发现
	commRes, err := mh.Knowledge(memhop.KnowledgeOp{
		Kind:    memhop.KOpDetectCommunities,
		GraphID: graphID,
	})
	if err != nil {
		t.Fatalf("[L3-7] KOpDetectCommunities 失败: %v", err)
	}
	t.Logf("  [L3-7] 社区发现: TotalNodes=%d Communities=%d",
		commRes.Community.TotalNodes, len(commRes.Community.Communities))

	// 9a-8: List L3
	listRes, err := mh.List(memhop.LayerKnowledge, memhop.ListRequest{
		Knowledge: &memhop.KnowledgeListQuery{Page: 1, PageSize: 10},
	})
	if err != nil {
		t.Fatalf("[L3-8] List(LayerKnowledge) 失败: %v", err)
	}
	t.Logf("  [L3-8] L3 列表: Total=%d", listRes.Knowledge.Total)

	// 9a-9: 通过 Get 获取 L3 详情
	getRes, err := mh.Get(memhop.LayerKnowledge, graphID)
	if err != nil {
		t.Fatalf("[L3-9] Get(LayerKnowledge) 失败: %v", err)
	}
	if getRes.Knowledge != nil {
		t.Logf("  [L3-9] L3 详情: Nodes=%d Edges=%d", len(getRes.Knowledge.Nodes), len(getRes.Knowledge.Edges))
	}

	t.Log("✓ L3 超图测试完成")

	// 清理
	t.Log("  清理 L3 数据...")
	for _, nh := range []uint64{edge.IDHash} {
		mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpDeleteEdge, EdgeID: hash.FormatHash(nh)})
	}
	for _, nh := range []uint64{node1.IDHash, node2.IDHash} {
		mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpDeleteNode, NodeID: hash.FormatHash(nh)})
	}
	mh.Delete(memhop.LayerKnowledge, graphID)
}

// ── 步骤 9: L5 单独测试 ────────────────────────────────────────────────

func TestE2EFlow_L5(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()
	t.Log("████ 步骤 9b: L5 动作链单独测试")

	// 9b-1: 创建动作链
	r, err := mh.Crystal(memhop.CrystalOp{
		Kind: memhop.COpCreateChain,
		ChainInput: &memhop.L5ChainInput{
			Title:   "wechat_moments_reply",
			Trigger: "friend posts new moments",
			Steps: []memhop.L5StepInput{
				{Action: "fetch_moments", Parameters: nil},
			},
		},
	})
	if err != nil {
		t.Fatalf("[L5-1] COpCreateChain 失败: %v", err)
	}
	chainID := r.ChainID
	t.Logf("  [L5-1] 创建链: ID=%s Title=wechat_moments_reply", chainID[:12])

	// 9b-2: Get 验证
	getRes, err := mh.Get(memhop.LayerCrystal, chainID)
	if err != nil {
		t.Fatalf("[L5-2] Get(LayerCrystal) 失败: %v", err)
	}
	chain := getRes.Crystal
	if chain.Title != "wechat_moments_reply" {
		t.Errorf("[L5-2] Title=%q, 期望 %q", chain.Title, "wechat_moments_reply")
	}
	t.Logf("  [L5-2] Get: Title=%s Status=%s TriggerCount=%d", chain.Title, chain.Status, chain.TriggerCount)

	// 9b-3: 追加步骤
	r2, err := mh.Crystal(memhop.CrystalOp{
		Kind:      memhop.COpAppendStep,
		ChainID:   chainID,
		StepInput: &memhop.L5StepInput{Action: "analyze_content", Parameters: nil},
	})
	if err != nil {
		t.Fatalf("[L5-3] COpAppendStep 失败: %v", err)
	}
	t.Logf("  [L5-3] 追加步骤: StepID=%s", r2.StepID[:12])

	// 9b-4: 触发计数
	if _, err := mh.Crystal(memhop.CrystalOp{Kind: memhop.COpIncrTrigger, ChainID: chainID}); err != nil {
		t.Fatalf("[L5-4] COpIncrTrigger 失败: %v", err)
	}
	getRes2, _ := mh.Get(memhop.LayerCrystal, chainID)
	t.Logf("  [L5-4] 触发后: TriggerCount=%d", getRes2.Crystal.TriggerCount)

	// 9b-5: 成功置信度
	if _, err := mh.Crystal(memhop.CrystalOp{Kind: memhop.COpUpdateConfidence, ChainID: chainID, Success: true}); err != nil {
		t.Fatalf("[L5-5] COpUpdateConfidence 失败: %v", err)
	}
	getRes3, _ := mh.Get(memhop.LayerCrystal, chainID)
	t.Logf("  [L5-5] 置信度更新后: SuccessRate=%.2f", getRes3.Crystal.SuccessRate)

	// 9b-6: 失败置信度
	if _, err := mh.Crystal(memhop.CrystalOp{Kind: memhop.COpUpdateConfidence, ChainID: chainID, Success: false}); err != nil {
		t.Fatalf("[L5-6] COpUpdateConfidence(false) 失败: %v", err)
	}

	// 9b-7: List L5
	listRes, err := mh.List(memhop.LayerCrystal, memhop.ListRequest{
		Crystal: &memhop.CrystalListQuery{Page: 1, PageSize: 10},
	})
	if err != nil {
		t.Fatalf("[L5-7] List(LayerCrystal) 失败: %v", err)
	}
	t.Logf("  [L5-7] L5 列表: Total=%d", listRes.Crystals.Total)

	// 9b-8: 批量删除
	r3, _ := mh.Crystal(memhop.CrystalOp{
		Kind:       memhop.COpCreateChain,
		ChainInput: &memhop.L5ChainInput{Title: "temp_chain", Trigger: "test"},
	})
	if _, err := mh.Crystal(memhop.CrystalOp{
		Kind: memhop.COpBatchDelete,
		IDs:  []string{chainID, r3.ChainID},
	}); err != nil {
		t.Fatalf("[L5-8] COpBatchDelete 失败: %v", err)
	}
	t.Logf("  [L5-8] 批量删除 2 条链")

	// 验证删除
	if _, err := mh.Get(memhop.LayerCrystal, chainID); err == nil {
		t.Error("[L5-8] 删除后 Get 应返回错误")
	} else {
		t.Logf("  [L5-8] 删除验证: %v", err)
	}

	t.Log("✓ L5 动作链测试完成")
}

// ── 辅助函数 ────────────────────────────────────────────────────────────

// strPtr 定义在 core_flow_test.go 中
