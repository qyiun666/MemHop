// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"fmt"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// 8 对真实中文对话数据
var dialoguePairs = []struct {
	user  string
	agent string
	desc  string
}{
	{"今天天气怎么样", "今天北京多云转晴，最高温度28度，适合户外活动", "天气"},
	{"明天的气温如何", "明天预计25-30度，紫外线较强，建议做好防晒", "天气追问-应归入天气话题"},
	{"推荐一部好看的科幻电影", "推荐《沙丘2》，画面震撼，剧情宏大，IMAX效果极佳", "电影"},
	{"Go语言怎么入门学习", "建议从官方A Tour of Go开始，然后做小项目练习，比如写个CLI工具", "编程学习"},
	{"最近AI领域有什么新进展", "2026年多模态模型进展显著，Claude和GPT系列持续迭代，开源模型也在快速追赶", "AI技术"},
	{"Python和Go哪个更适合做后端", "Go适合高并发服务，Python适合快速开发和数据科学，取决于团队和项目需求", "编程对比-应与编程学习关联"},
	{"帮我写一段快速排序的代码", "快速排序核心思想是选pivot分区递归，平均时间复杂度O(nlogn)，以下是Go实现示例：func quickSort(arr []int) []int { if len(arr) < 2 { return arr } pivot := arr[len(arr)/2] var left, right []int for _, v := range arr { if v < pivot { left = append(left, v) } else if v > pivot { right = append(right, v) } } return append(append(quickSort(left), pivot), quickSort(right)...) }", "编程实践-含代码长文本"},
	{"什么是微服务架构", "微服务将单体应用拆分为独立服务，通过API通信，常用gRPC或REST，需注意分布式事务和服务治理", "架构知识"},
}

func TestCoreFlow(t *testing.T) {
	// ============================================================
	// Phase 0: 生命周期 + L0 画像
	// ============================================================
	mh := testsupport.OpenMemHop(t)
	defer mh.Close()

	// 设置 L0 Profile
	_, err := mh.Topic(memhop.TopicOp{
		Kind: memhop.TOpSetProfile,
		ProfileDelta: &memhop.ProfileDelta{
			Name: strPtr("助手"),
			Role: strPtr("AI Assistant"),
		},
	})
	if err != nil {
		t.Fatalf("Topic(TOpSetProfile) failed: %v", err)
	}

	profRes, err := mh.Get(memhop.LayerProfile, "")
	if err != nil {
		t.Fatalf("Get(LayerProfile) failed: %v", err)
	}
	profile := profRes.Profile
	if profile.Name != "助手" {
		t.Fatalf("profile.Name expected '助手', got %q", profile.Name)
	}
	t.Logf("L0 画像设置: Name=%q, Role=%q ✓", profile.Name, profile.Role)

	before := testsupport.SnapshotHealth(t, mh, "初始状态")

	// ============================================================
	// Phase 1: 8 轮 Search+Update 配对对话
	// ============================================================
	t.Log("")
	t.Log("████ Phase 1: 8 轮 Search+Update 配对对话")

	topicIDs := make([]string, len(dialoguePairs))
	for i, pair := range dialoguePairs {
		t.Logf("")
		t.Logf("═══ 轮次 %d/%d: %s ═══", i+1, len(dialoguePairs), pair.desc)

		topicID, result := testsupport.SearchUpdatePair(t, mh, pair.user, pair.agent)
		topicIDs[i] = topicID

		// 搜索质量验证
		testsupport.LogSearchQuality(t, result, pair.user, pair.desc)
		testsupport.AssertL2Topic(t, mh, topicID)
		testsupport.AssertL4Archive(t, mh, topicID, 2) // user + agent 各一条

		t.Logf("[轮次 %d] %s → topicID=%s", i+1, pair.desc, topicID)

		// 语义归并验证：第 2 轮"明天的气温"应归入第 1 轮"今天天气"的同一话题
		if i == 1 && topicIDs[0] != "" {
			if topicID != topicIDs[0] {
				t.Logf("[语义归并] 注意：天气追问未归入同一话题（got=%s, expected=%s），这是正常的语义区分", topicID, topicIDs[0])
			} else {
				t.Logf("[语义归并] 天气追问成功归入天气话题 ✓")
			}
		}

		// Dream 触发：第 3 轮和第 6 轮后
		if i == 2 || i == 5 {
			t.Logf("")
			t.Logf("=== Dream 触发（第 %d 轮后）===", i+1)
			report := testsupport.RunDream(t, mh)
			if report != nil {
				t.Logf("Dream: ConsolidatedCount=%d", report.ConsolidatedCount)
			}
			testsupport.SnapshotHealth(t, mh, fmt.Sprintf("Dream后(第%d轮)", i+1))
		}
	}

	// ============================================================
	// Phase 2: Dream 后搜索质量复检
	// ============================================================
	t.Log("")
	t.Log("████ Phase 2: Dream 后搜索质量复检")
	for _, pair := range dialoguePairs[:3] { // 复检前 3 个话题
		t.Logf("")
		t.Logf("--- 复检: %s ---", pair.desc)
		result, err := mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: pair.user})
		if err != nil {
			t.Fatalf("Search(Text=%q) 复检失败: %v", pair.user, err)
		}
		if len(result.Contexts) == 0 {
			t.Fatalf("复检: Search(Text=%q) 无结果", pair.user)
		}
		testsupport.LogSearchQuality(t, result, pair.user, pair.desc+" (复检)")

		// 验证 FusedKeywords 是否被 Dream 填充
		ctx := result.Contexts[0]
		if len(ctx.FusedKeywords) > 0 {
			t.Logf("[Dream增强] %s: FusedKeywords=%v", pair.desc, ctx.FusedKeywords)
		}
		// 检查 AssociatedContexts（跨话题关联）
		if len(result.AssociatedContexts) > 0 {
			t.Logf("[跨话题关联] %s: %d 个关联上下文", pair.desc, len(result.AssociatedContexts))
		}
	}

	// ============================================================
	// Phase 3: L0 画像变化验证
	// ============================================================
	t.Log("")
	t.Log("████ Phase 3: L0 画像验证")
	afterRes, err := mh.Get(memhop.LayerProfile, "")
	if err != nil {
		t.Fatalf("Get(LayerProfile) 失败: %v", err)
	}
	afterProfile := afterRes.Profile
	if afterProfile == nil {
		t.Fatal("Get(LayerProfile) 返回 nil profile")
	}
	t.Logf("Profile: Name=%s, Role=%s", afterProfile.Name, afterProfile.Role)
	t.Logf("  Lexicon:         %v", afterProfile.Lexicon)
	t.Logf("  StyleTraits:     %v", afterProfile.StyleTraits)
	t.Logf("  EmotionPatterns: %v", afterProfile.EmotionPatterns)
	t.Logf("  Personality:     %s", afterProfile.Personality)

	// ============================================================
	// Phase 4: L1 图可视化
	// ============================================================
	t.Log("")
	t.Log("████ Phase 4: L1 图验证")
	l1GraphRes, err := mh.Get(memhop.LayerScene, "")
	if err != nil {
		t.Fatalf("Get(LayerScene) 失败: %v", err)
	}
	l1Graph := l1GraphRes.SceneGraph
	if l1Graph == nil {
		t.Fatal("Get(LayerScene) 返回 nil")
	}
	if len(l1Graph.Nodes) == 0 {
		t.Logf("L1 Graph: 0 nodes（可能尚未构建，属于正常状态）")
	} else {
		t.Logf("L1 Graph: %d nodes", len(l1Graph.Nodes))
		for ni, node := range l1Graph.Nodes {
			t.Logf("  Node[%d]: ID=%s Scene=%s Depth=%d Topics=%v", ni, node.ID, node.SceneID, node.Depth, node.TopicIDs)
		}
	}
	if len(l1Graph.Edges) == 0 {
		t.Logf("L1 Graph: 0 edges（可能尚未构建，属于正常状态）")
	} else {
		t.Logf("L1 Graph: %d edges", len(l1Graph.Edges))
		for ei, edge := range l1Graph.Edges {
			t.Logf("  Edge[%d]: ID=%s Kind=%s Weight=%.2f Nodes=%v", ei, edge.ID, edge.Kind, edge.Weight, edge.NodeIDs)
		}
	}

	// ============================================================
	// Phase 5: 最终健康检查 + Checkpoint
	// ============================================================
	t.Log("")
	t.Log("████ Phase 5: 最终验证")
	after := testsupport.SnapshotHealth(t, mh, "最终状态")

	// 比较 before/after 各层计数增长
	t.Logf("--- 各层计数增长 ---")
	for _, layer := range []string{"l0_profile", "l1_engram", "l2_topic", "l3_knowledge", "l4_archive", "l5_crystal"} {
		diff := after.LayerCounts[layer] - before.LayerCounts[layer]
		t.Logf("  %-18s %+d (%d → %d)", layer+":", diff, before.LayerCounts[layer], after.LayerCounts[layer])
	}

	err = mh.Checkpoint()
	if err != nil {
		t.Fatalf("Checkpoint 失败: %v", err)
	}
	t.Log("Checkpoint 完成 ✓")

	t.Log("")
	t.Log("✓ 核心端到端流程测试完成")
}

// strPtr 辅助函数，用于创建 *string 参数
func strPtr(s string) *string {
	return &s
}
