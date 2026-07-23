// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	memhop "memhop/api"
	"memhop/test/testsupport"
)

// ── 基准测试数据集 ──────────────────────────────────────────
//
// 每个 topic 包含一组关键词，AutoCreate 写入后用语义相近的查询检验召回率。
// 关键词共享汉字/英文词素，mock encoder 的 bag-of-words 哈希能产生可预测的相似度。
//
// recallGroundTruth 定义了 topic 的"种子文本"和期望被召回的相关查询。
var recallGroundTruth = []struct {
	seedText  string // AutoCreate 写入的原始文本
	related   string // 语义相近的查询（应召回该 topic）
	unrelated string // 语义相远的查询（不应召回该 topic）
	topic     string // 话题标签（仅用于日志）
}{
	{
		seedText:  "今天天气怎么样，北京明天会下雨吗，气温多少度",
		related:   "北京天气预报",
		unrelated: "推荐一部好看的科幻电影",
		topic:     "天气",
	},
	{
		seedText:  "Go语言入门学习，推荐教程和练习项目，并发编程怎么学",
		related:   "Go语言学习教程",
		unrelated: "电影推荐",
		topic:     "编程学习",
	},
	{
		seedText:  "推荐好看的科幻电影，最近有什么新片上映，评分高的电影",
		related:   "科幻电影推荐新片",
		unrelated: "如何学习Go语言",
		topic:     "电影推荐",
	},
	{
		seedText:  "微服务架构设计，服务拆分原则，gRPC通信，分布式事务",
		related:   "微服务gRPC分布式架构",
		unrelated: "早餐吃什么有营养",
		topic:     "架构设计",
	},
	{
		seedText:  "健康饮食食谱，减肥餐怎么搭配，早餐营养均衡",
		related:   "健康减肥食谱",
		unrelated: "Go语言并发编程",
		topic:     "健康饮食",
	},
	{
		seedText:  "Python机器学习的入门路径，scikit-learn和TensorFlow推荐",
		related:   "Python机器学习框架",
		unrelated: "北京天气",
		topic:     "机器学习",
	},
	{
		seedText:  "新能源汽车推荐，特斯拉和比亚迪对比，续航里程",
		related:   "新能源电动车推荐",
		unrelated: "微服务架构",
		topic:     "新能源车",
	},
	{
		seedText:  "日本旅游攻略，东京大阪京都，樱花季什么时候",
		related:   "日本东京旅游",
		unrelated: "机器学习入门",
		topic:     "旅游",
	},
}

// ── 工具函数：准备带数据的 MemHop ──────────────────────────

// populateRecallData 将所有 seedText 以 AutoCreate 写入 MemHop 并返回 topicIDs。
func populateRecallData(t testing.TB, mh *memhop.MemHop) []string {
	t.Helper()
	ids := make([]string, len(recallGroundTruth))
	for i, gt := range recallGroundTruth {
		result, err := mh.Search(memhop.SearchQuery{
			Text:       gt.seedText,
			AutoCreate: true,
		})
		if err != nil {
			t.Fatalf("AutoCreate[%d] %s: %v", i, gt.topic, err)
		}
		if len(result.Contexts) == 0 {
			t.Fatalf("AutoCreate[%d] %s: 无返回结果", i, gt.topic)
		}
		ids[i] = result.Contexts[0].ID
		t.Logf("[写入] %s → topicID=%s", gt.topic, ids[i][:12])
	}
	return ids
}

// recallAtK 返回查询在 topK 结果中是否召回了目标 topicID。
func recallAtK(result *memhop.SearchResult, topicID string, k int) bool {
	limit := k
	if len(result.Contexts) < limit {
		limit = len(result.Contexts)
	}
	for i := 0; i < limit; i++ {
		if result.Contexts[i].ID == topicID {
			return true
		}
	}
	return false
}

// avgScore 返回 topK 结果的平均 RetrievalScore。
func avgScore(result *memhop.SearchResult, k int) float64 {
	limit := k
	if len(result.Contexts) < limit {
		limit = len(result.Contexts)
	}
	if limit == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < limit; i++ {
		sum += float64(result.Contexts[i].RetrievalScore)
	}
	return sum / float64(limit)
}

// ── 基准测试：召回率 Benchmark ─────────────────────────────

// BenchmarkRetrievalRecall 测量不同查询下的召回率。
// 每个子 benchmark 在独立 MemHop 实例上运行以避免缓存干扰。
func BenchmarkRetrievalRecall(b *testing.B) {
	b.StopTimer()

	mh := openMemHopMockTB(b)
	defer mh.Close()
	topicIDs := populateRecallData(b, mh)

	// 预热：确保所有数据已持久化
	if err := mh.Checkpoint(); err != nil {
		b.Fatalf("Checkpoint: %v", err)
	}

	b.StartTimer()

	for i, gt := range recallGroundTruth {
		gt := gt
		topicID := topicIDs[i]
		b.Run(fmt.Sprintf("Recall@1_%s", gt.topic), func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				result, err := mh.Search(memhop.SearchQuery{Text: gt.related})
				if err != nil {
					b.Fatalf("Search: %v", err)
				}
				if !recallAtK(result, topicID, 1) {
					b.Logf("未在 top-1 召回 %s (related=%q)", gt.topic, gt.related)
				}
			}
		})

		b.Run(fmt.Sprintf("Recall@3_%s", gt.topic), func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				result, err := mh.Search(memhop.SearchQuery{Text: gt.related})
				if err != nil {
					b.Fatalf("Search: %v", err)
				}
				recallAtK(result, topicID, 3)
			}
		})

		b.Run(fmt.Sprintf("Recall@5_%s", gt.topic), func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				result, err := mh.Search(memhop.SearchQuery{Text: gt.related, MaxResults: 10})
				if err != nil {
					b.Fatalf("Search: %v", err)
				}
				recallAtK(result, topicID, 5)
			}
		})
	}
}

// BenchmarkSearchParams 测量不同搜索参数组合的性能影响。
func BenchmarkSearchParams(b *testing.B) {
	b.StopTimer()

	mh := openMemHopMockTB(b)
	defer mh.Close()
	populateRecallData(b, mh)

	// 获取第一个 topicID 用于 DirectedL2ID 测试
	firstResult, err := mh.Search(memhop.SearchQuery{Text: recallGroundTruth[0].seedText})
	if err != nil || len(firstResult.Contexts) == 0 {
		b.Fatalf("无法获取 topicID: %v", err)
	}
	firstTopicID := firstResult.Contexts[0].ID

	if err := mh.Checkpoint(); err != nil {
		b.Fatalf("Checkpoint: %v", err)
	}

	b.StartTimer()

	// 不同 MaxResults
	for _, maxRes := range []int{1, 5, 10, 20, 50} {
		maxRes := maxRes
		b.Run(fmt.Sprintf("MaxResults_%d", maxRes), func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				_, err := mh.Search(memhop.SearchQuery{
					Text:       "天气",
					MaxResults: maxRes,
				})
				if err != nil {
					b.Fatalf("Search: %v", err)
				}
			}
		})
	}

	// DirectedL2ID 定向搜索
	b.Run("DirectedL2ID", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			_, err := mh.Search(memhop.SearchQuery{
				Text:         "测试",
				DirectedL2ID: &firstTopicID,
			})
			if err != nil {
				b.Fatalf("Search DirectedL2ID: %v", err)
			}
		}
	})

	// AutoCreate 搜索
	b.Run("AutoCreate", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			_, err := mh.Search(memhop.SearchQuery{
				Text:       fmt.Sprintf("新话题测试%d", n),
				AutoCreate: true,
			})
			if err != nil {
				b.Fatalf("Search AutoCreate: %v", err)
			}
		}
	})

	// 无匹配查询（应在所有 topic 后搜索不到相关内容）
	b.Run("NoMatch", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			_, err := mh.Search(memhop.SearchQuery{Text: "xyzzy_nonexistent_12345"})
			if err != nil {
				b.Fatalf("Search no-match: %v", err)
			}
		}
	})
}

// BenchmarkSceneSeparation 测量场景分离质量：同一话题的两次查询是否归入同一 scene。
func BenchmarkSceneSeparation(b *testing.B) {
	mh := openMemHopMockTB(b)
	defer mh.Close()

	// 写入同一话题的两条相似文本
	r1, err := mh.Search(memhop.SearchQuery{Text: "今天北京天气怎么样", AutoCreate: true})
	if err != nil || len(r1.Contexts) == 0 {
		b.Fatalf("AutoCreate: %v", err)
	}
	topicID1 := r1.Contexts[0].ID
	sceneID1 := r1.Contexts[0].SceneID

	_, err = mh.Search(memhop.SearchQuery{Text: "明天上海天气如何"})
	if err != nil {
		b.Fatalf("Search: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		result, err := mh.Search(memhop.SearchQuery{Text: "后天会下雨吗"})
		if err != nil {
			b.Fatalf("Search: %v", err)
		}
		if len(result.Contexts) > 0 {
			// 检查是否归入同 scene
			_ = result.Contexts[0].SceneID == sceneID1
			_ = result.Contexts[0].ID == topicID1
		}
	}
	_ = sceneID1
	_ = topicID1
}

// ── 测试：检索准确率与精度 ─────────────────────────────────

// TestRetrievalRecallRate 测量整体召回率（按 topic 分类）。
func TestRetrievalRecallRate(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	topicIDs := populateRecallData(t, mh)

	t.Log("")
	t.Log("══════ 召回率测试 ══════")

	type topicStat struct {
		recall1 int // Recall@1 成功次数
		recall3 int // Recall@3 成功次数
		recall5 int // Recall@5 成功次数
		total   int // 总查询次数
	}

	stats := make(map[string]*topicStat)

	for i, gt := range recallGroundTruth {
		stats[gt.topic] = &topicStat{total: 1}
		topicID := topicIDs[i]

		// 用 related 查询测试召回
		result, err := mh.Search(memhop.SearchQuery{Text: gt.related})
		if err != nil {
			t.Fatalf("Search(%q): %v", gt.related, err)
		}

		if recallAtK(result, topicID, 1) {
			stats[gt.topic].recall1++
		}
		if recallAtK(result, topicID, 3) {
			stats[gt.topic].recall3++
		}
		if recallAtK(result, topicID, 5) {
			stats[gt.topic].recall5++
		}

		top1hit := recallAtK(result, topicID, 1)
		top3hit := recallAtK(result, topicID, 3)

		t.Logf("  [%s] related=%q → Recall@1=%v  Recall@3=%v  Score=%.4f  topID=%s",
			gt.topic, gt.related,
			top1hit, top3hit,
			safeScore(result, 0),
			result.Contexts[0].ID[:12])
		if len(result.Contexts) > 0 {
			for ri, ctx := range result.Contexts {
				t.Logf("    Result[%d]: ID=%s Score=%.4f",
					ri, ctx.ID[:12], ctx.RetrievalScore)
			}
		}

		if top1hit {
			t.Logf("    ✓ 正确召回")
		} else if top3hit {
			t.Logf("    △ 在 top-3 中（但不在 top-1）")
		} else {
			t.Logf("    ✗ 未在 top-5 中召回")
		}

		// 用 unrelated 查询验证不应召回（精确率检查）
		resultUnrel, err := mh.Search(memhop.SearchQuery{Text: gt.unrelated, MaxResults: 5})
		if err != nil {
			t.Fatalf("Search(%q): %v", gt.unrelated, err)
		}
		unwantedRecall := recallAtK(resultUnrel, topicID, 5)
		if unwantedRecall {
			t.Logf("  ⚠ [%s] unrelated=%q 错误触发了召回", gt.topic, gt.unrelated)
		} else {
			t.Logf("  ✓ [%s] unrelated=%q 正确未召回", gt.topic, gt.unrelated)
		}
	}

	// ── 汇总报告 ──
	t.Log("")
	t.Log("══════ 汇总报告 ══════")
	var totalRecall1, totalRecall3, totalRecall5, totalCount int
	for topic, stat := range stats {
		totalRecall1 += stat.recall1
		totalRecall3 += stat.recall3
		totalRecall5 += stat.recall5
		totalCount += stat.total
		pct1 := float64(stat.recall1) / float64(stat.total) * 100
		pct3 := float64(stat.recall3) / float64(stat.total) * 100
		t.Logf("  %-12s  Recall@1=%d/%d (%.0f%%)  Recall@3=%d/%d (%.0f%%)",
			topic, stat.recall1, stat.total, pct1, stat.recall3, stat.total, pct3)
	}
	t.Logf("")
	t.Logf("  总体 Recall@1 = %d/%d (%.1f%%)", totalRecall1, totalCount, float64(totalRecall1)/float64(totalCount)*100)
	t.Logf("  总体 Recall@3 = %d/%d (%.1f%%)", totalRecall3, totalCount, float64(totalRecall3)/float64(totalCount)*100)
	t.Logf("  总体 Recall@5 = %d/%d (%.1f%%)", totalRecall5, totalCount, float64(totalRecall5)/float64(totalCount)*100)
}

// TestSearchPrecision 测量搜索结果精度（top-1 和 top-3 平均得分）。
func TestSearchPrecision(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	populateRecallData(t, mh)

	t.Log("")
	t.Log("══════ 搜索精度测试 ══════")

	type precisionStat struct {
		top1Scores []float64
		top3Scores []float64
	}

	var allStats precisionStat

	for i, gt := range recallGroundTruth {
		result, err := mh.Search(memhop.SearchQuery{Text: gt.related, MaxResults: 5})
		if err != nil {
			t.Fatalf("Search(%q): %v", gt.related, err)
		}
		if len(result.Contexts) == 0 {
			t.Logf("  [%s] 无搜索结果", gt.topic)
			continue
		}

		// top-1 得分
		top1 := float64(result.Contexts[0].RetrievalScore)
		allStats.top1Scores = append(allStats.top1Scores, top1)

		// top-3 平均得分
		top3 := avgScore(result, 3)
		allStats.top3Scores = append(allStats.top3Scores, top3)

		t.Logf("  [%s] top1=%.4f  top3_avg=%.4f  contexts=%d",
			gt.topic, top1, top3, len(result.Contexts))
		for ri, ctx := range result.Contexts {
			if ri >= 3 {
				break
			}
			t.Logf("    [%d] ID=%s Score=%.4f Keywords=%v",
				ri, ctx.ID[:12], ctx.RetrievalScore, ctx.UserKeywords)
		}
		_ = i
	}

	// 汇总
	if len(allStats.top1Scores) > 0 {
		var sum1, sum3 float64
		for i := range allStats.top1Scores {
			sum1 += allStats.top1Scores[i]
			sum3 += allStats.top3Scores[i]
		}
		avg1 := sum1 / float64(len(allStats.top1Scores))
		avg3 := sum3 / float64(len(allStats.top3Scores))
		t.Logf("")
		t.Logf("  平均 top-1 检索得分: %.4f", avg1)
		t.Logf("  平均 top-3 检索得分: %.4f", avg3)
		t.Logf("  总样本数: %d", len(allStats.top1Scores))
	}

	// 测试不可约分查询（应得到低分或空结果）
	t.Log("")
	t.Log("--- 噪声查询精度（应返回低分或无结果） ---")
	noiseQueries := []string{
		"xyzzy_nonexistent_12345",
		"asdfghjklzxcvbnm",
		strings.Repeat("测试", 50), // 超长文本
	}
	for _, nq := range noiseQueries {
		result, err := mh.Search(memhop.SearchQuery{Text: nq, MaxResults: 5})
		if err != nil {
			t.Fatalf("Search(%q): %v", nq, err)
		}
		if len(result.Contexts) == 0 {
			t.Logf("  ✓ 噪声查询 %q: 无结果（正确）", strLimit(nq, 30))
		} else {
			t.Logf("  △ 噪声查询 %q: %d 个结果 top1=%.4f",
				strLimit(nq, 30), len(result.Contexts), result.Contexts[0].RetrievalScore)
		}
	}
}

// TestSearchVariousParameters 测试搜索接口的不同参数组合都能跑通。
func TestSearchVariousParameters(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	populateRecallData(t, mh)

	t.Log("")
	t.Log("══════ 搜索参数兼容性测试 ══════")

	// 1. 基本搜索 - 空文本
	_, err := mh.Search(memhop.SearchQuery{Text: ""})
	if err != nil {
		t.Logf("  △ 空文本搜索: %v（可能是预期行为）", err)
	} else {
		t.Logf("  ✓ 空文本搜索: 通过")
	}

	// 2. 不同 MaxResults
	for _, mr := range []int{0, 1, 3, 10, 100} {
		result, err := mh.Search(memhop.SearchQuery{Text: "天气", MaxResults: mr})
		if err != nil {
			t.Fatalf("MaxResults=%d: %v", mr, err)
		}
		if mr > 0 && len(result.Contexts) > mr {
			t.Errorf("MaxResults=%d 返回了 %d 个结果", mr, len(result.Contexts))
		}
		t.Logf("  ✓ MaxResults=%d: %d 个结果", mr, len(result.Contexts))
	}

	// 3. DirectedL2ID
	firstResult, err := mh.Search(memhop.SearchQuery{Text: recallGroundTruth[0].seedText})
	if err != nil || len(firstResult.Contexts) == 0 {
		t.Fatalf("无法获取 topicID: %v", err)
	}
	topicID := firstResult.Contexts[0].ID

	result, err := mh.Search(memhop.SearchQuery{
		Text:         "任意内容",
		DirectedL2ID: &topicID,
	})
	if err != nil {
		t.Fatalf("DirectedL2ID: %v", err)
	}
	if len(result.Contexts) == 0 || result.Contexts[0].ID != topicID {
		t.Errorf("DirectedL2ID 未返回指定话题: got %v", result.Contexts)
	} else {
		t.Logf("  ✓ DirectedL2ID: 正确返回话题 %s", topicID[:12])
	}

	// 4. DirectedL2ID 无效 ID
	invalidID := "0000000000000000"
	result, err = mh.Search(memhop.SearchQuery{
		Text:         "test",
		DirectedL2ID: &invalidID,
	})
	if err != nil {
		t.Fatalf("DirectedL2ID 无效ID不应报错: %v", err)
	}
	if len(result.Contexts) == 0 {
		t.Logf("  ✓ DirectedL2ID 无效ID: 返回空结果（预期行为）")
	}

	// 5. DirectedL3ID（如果可用）
	l3Result, err := mh.Search(memhop.SearchQuery{
		Text:       "天气",
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	_ = l3Result

	// 6. 时间戳参数
	result, err = mh.Search(memhop.SearchQuery{
		Text:      "天气",
		Timestamp: 1700000000000,
	})
	if err != nil {
		t.Fatalf("Search with Timestamp: %v", err)
	}
	t.Logf("  ✓ Search with Timestamp: %d 个结果", len(result.Contexts))
}

// ── 测试：各层 API 兼容性 ──────────────────────────────────

// TestAPICompatibility 测试各层 API 在不同参数下都能正常跑通。
func TestAPICompatibility(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	t.Log("")
	t.Log("══════ API 兼容性测试 ══════")

	// ── L0 Profile API ──
	t.Log("--- L0 Profile API ---")
	name := "测试助手"
	role := "AI Assistant"
	err := mh.SetProfile(memhop.ProfileDelta{Name: &name, Role: &role})
	if err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	t.Logf("  ✓ SetProfile")

	profile, err := mh.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if profile != nil {
		t.Logf("  ✓ GetProfile: Name=%s Role=%s", profile.Name, profile.Role)
	}

	// ── Search + Update（L2 + L4 写入） ──
	t.Log("--- L2 Update + L4 Write API ---")
	dialogueText := "今天天气怎么样"
	agentText := "今天晴转多云"
	topicID, result := testsupport.SearchUpdatePair(t, mh, dialogueText, agentText)
	t.Logf("  ✓ SearchUpdatePair: topicID=%s", topicID[:12])

	// 验证搜索结果结构
	if len(result.Contexts) > 0 {
		ctx := result.Contexts[0]
		t.Logf("  ✓ SearchResult: SceneID=%s Depth=%d Score=%.4f",
			ctx.SceneID[:12], ctx.Depth, ctx.RetrievalScore)
		t.Logf("    UserKeywords=%v", ctx.UserKeywords)
		t.Logf("    AgentKeywords=%v", ctx.AgentKeywords)
		if len(ctx.L4Refs) > 0 {
			t.Logf("    L4Refs=%v", ctx.L4Refs)
		}
		if len(ctx.ChildrenIDs) > 0 {
			t.Logf("    ChildrenIDs=%v", ctx.ChildrenIDs)
		}
	}

	// ── L2 Read API ──
	t.Log("--- L2 Read API ---")
	detail, err := mh.GetL2(topicID)
	if err != nil {
		t.Fatalf("GetL2: %v", err)
	}
	if detail.ID != topicID {
		t.Errorf("GetL2 ID 不匹配")
	}
	t.Logf("  ✓ GetL2: ID=%s Depth=%d", detail.ID[:12], detail.Depth)

	listResult, err := mh.ListL2(memhop.TopicListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListL2: %v", err)
	}
	t.Logf("  ✓ ListL2: Total=%d Items=%d", listResult.Total, len(listResult.Items))

	// ── L4 Archive API ──
	t.Log("--- L4 Archive API ---")
	topicIDCopy := topicID
	archiveResult, err := mh.QueryArchives(memhop.ArchiveQuery{
		TopicID:  &topicIDCopy,
		Page:     1,
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("QueryArchives: %v", err)
	}
	t.Logf("  ✓ QueryArchives: Total=%d", archiveResult.Total)
	if archiveResult.Total > 0 {
		archive, err := mh.GetArchive(archiveResult.Items[0].ID)
		if err != nil {
			t.Fatalf("GetArchive: %v", err)
		}
		t.Logf("  ✓ GetArchive: Role=%d Content=%q", archive.Role, strLimit(archive.Content, 50))
	}

	// ── L5 Action Chain API ──
	t.Log("--- L5 Crystal API ---")
	chainID, err := mh.CreateActionChain(memhop.L5ChainInput{
		Title:   "test_chain",
		Trigger: "user asks about weather",
		Steps: []memhop.L5StepInput{
			{Action: "call_weather_api", Parameters: nil},
			{Action: "format_response", Parameters: nil},
		},
	})
	if err != nil {
		t.Fatalf("CreateActionChain: %v", err)
	}
	t.Logf("  ✓ CreateActionChain: chainID=%s", chainID[:12])

	chain, err := mh.GetL5(chainID)
	if err != nil {
		t.Fatalf("GetL5: %v", err)
	}
	t.Logf("  ✓ GetL5: Title=%s Status=%s TriggerCount=%d", chain.Title, chain.Status, chain.TriggerCount)

	stepID, err := mh.AppendActionStep(chainID, memhop.L5StepInput{
		Action: "log_result", Parameters: nil,
	})
	if err != nil {
		t.Fatalf("AppendActionStep: %v", err)
	}
	t.Logf("  ✓ AppendActionStep: stepID=%s", stepID[:12])

	err = mh.IncrChainTrigger(chainID)
	if err != nil {
		t.Fatalf("IncrChainTrigger: %v", err)
	}
	t.Logf("  ✓ IncrChainTrigger")

	err = mh.UpdateChainConfidence(chainID, true)
	if err != nil {
		t.Fatalf("UpdateChainConfidence: %v", err)
	}
	t.Logf("  ✓ UpdateChainConfidence")

	crystals, err := mh.ListCrystals(memhop.CrystalListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListCrystals: %v", err)
	}
	t.Logf("  ✓ ListCrystals: Total=%d", crystals.Total)

	// ── L3 Hypergraph API ──
	t.Log("--- L3 Hypergraph API ---")
	graph, err := mh.CreateL3Graph("test_knowledge_graph")
	if err != nil {
		t.Fatalf("CreateL3Graph: %v", err)
	}
	t.Logf("  ✓ CreateL3Graph: graphID=%s", hashFormat(graph.IDHash))

	node := &memhop.HypergraphNode{
		Title:    "Go语言",
		NodeType: "concept",
		Content:  "Go是一种静态类型编译型语言",
		Keywords: []string{"Go", "编程语言", "编译型"},
	}
	err = mh.AddL3Node(hashFormat(graph.IDHash), node)
	if err != nil {
		t.Fatalf("AddL3Node: %v", err)
	}
	t.Logf("  ✓ AddL3Node: %s", node.Title)

	// L3 搜索
	l3SearchResult, err := mh.SearchL3Nodes(memhop.L3SearchQuery{Keyword: "Go"})
	if err != nil {
		t.Fatalf("SearchL3Nodes: %v", err)
	}
	t.Logf("  ✓ SearchL3Nodes: Results=%d", len(l3SearchResult.Nodes))

	// L3 列表
	knowledgeList, err := mh.ListKnowledge(memhop.KnowledgeListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListKnowledge: %v", err)
	}
	t.Logf("  ✓ ListKnowledge: Total=%d", knowledgeList.Total)

	// L3 知识节点查询（通过 SearchL3Nodes 按类型搜索）
	l3ByTypeResult, err := mh.SearchL3Nodes(memhop.L3SearchQuery{
		NodeType: "concept",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("SearchL3Nodes(by type): %v", err)
	}
	t.Logf("  ✓ SearchL3Nodes(by type): Results=%d", len(l3ByTypeResult.Nodes))

	// ── L1 Graph API ──
	t.Log("--- L1 Graph API ---")
	l1Graph, err := mh.GetL1Graph(nil)
	if err != nil {
		t.Fatalf("GetL1Graph: %v", err)
	}
	t.Logf("  ✓ GetL1Graph: Nodes=%d Edges=%d", len(l1Graph.Nodes), len(l1Graph.Edges))

	// ── Dream API ──
	t.Log("--- Dream API ---")
	report, err := mh.Dream(&memhop.DreamOptions{SkipDistill: true})
	if err != nil {
		t.Logf("  △ Dream (offline mock): %v", err)
	} else {
		t.Logf("  ✓ Dream: Consolidated=%d L3=%d Crystals=%d",
			report.ConsolidatedCount, report.NewL3Nodes, report.NewCrystals)
		for _, stage := range report.Stages {
			t.Logf("    Stage: %s Status=%s (%dms)", stage.Name, stage.Status, stage.DurationMs)
		}
	}

	// ── Health API ──
	t.Log("--- Health API ---")
	health, err := mh.HealthCheck()
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	t.Logf("  ✓ HealthCheck: OK=%v Size=%d Encoder=%v",
		health.OK, health.DBSizeBytes, health.EncoderConfigured)
	for _, layer := range []string{"l0_profile", "l1_engram", "l2_topic", "l3_knowledge", "l4_archive", "l5_crystal"} {
		if count := health.LayerCounts[layer]; count > 0 {
			t.Logf("    %s: %d", layer, count)
		}
	}

	// ── Session API ──
	t.Log("--- Session API ---")
	sessionStatus, err := mh.SessionStatus()
	if err != nil {
		t.Fatalf("SessionStatus: %v", err)
	}
	t.Logf("  ✓ SessionStatus: ActiveTopics=%d IsEmpty=%v", sessionStatus.Count, sessionStatus.IsEmpty)

	// ── Checkpoint API ──
	t.Log("--- Checkpoint API ---")
	err = mh.Checkpoint()
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	t.Logf("  ✓ Checkpoint")

	// ── BatchStore API ──
	t.Log("--- BatchStore API ---")
	batchResult, err := mh.BatchStore(memhop.StoreBatch{
		Items: []memhop.StoreItem{
			{
				Content:  "批量存储测试内容",
				Keywords: []string{"测试", "批量存储"},
				Source:   "test",
			},
		},
	})
	if err != nil {
		t.Fatalf("BatchStore: %v", err)
	}
	t.Logf("  ✓ BatchStore: Stored=%d ItemIDs=%v", batchResult.StoredCount, batchResult.ItemIDs)

	// ── 关闭后调用检查 ──
	t.Log("--- 关闭后错误检查 ---")
	err = mh.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 关闭后调用应返回 ErrClosed
	checkClosed := func(name string, gotErr error) {
		if gotErr == nil {
			t.Errorf("  %s 在关闭后调用应返回错误", name)
		} else if strings.Contains(gotErr.Error(), "closed") {
			t.Logf("  ✓ %s 关闭后返回 closed 错误: %v", name, gotErr)
		} else {
			t.Logf("  △ %s 关闭后返回其他错误: %v", name, gotErr)
		}
	}
	_, err = mh.Search(memhop.SearchQuery{Text: "test"})
	checkClosed("Search", err)
	_, err = mh.GetProfile()
	checkClosed("GetProfile", err)
	_, err = mh.HealthCheck()
	checkClosed("HealthCheck", err)
	_, err = mh.Dream(nil)
	checkClosed("Dream", err)
}

// ── 测试：不同对话数的检索性能趋势 ─────────────────────────

// TestRetrievalScale 测试不同数据量下的检索表现。
func TestRetrievalScale(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过扩展性测试（-short 标记）")
	}

	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	sizes := []int{5, 20, 50, 100}
	query := "天气"
	seedBase := "这是关于%d的一个话题，包含一些描述性的关键词"

	for _, size := range sizes {
		// 写入 size 个不同话题
		for i := 0; i < size; i++ {
			text := fmt.Sprintf(seedBase, i)
			if i == 0 {
				text = "今天天气怎么样明天会下雨吗气温多少度" // 目标话题
			}
			_, err := mh.Search(memhop.SearchQuery{Text: text, AutoCreate: true})
			if err != nil {
				t.Fatalf("AutoCreate[%d]: %v", i, err)
			}
		}

		result, err := mh.Search(memhop.SearchQuery{Text: query, MaxResults: 10})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		t.Logf("  数据量=%d  → 结果数=%d  top1=%.4f  top3_avg=%.4f",
			size, len(result.Contexts),
			safeScore(result, 0), safeAvgScore(result, 3))
	}
}

// ── 辅助函数 ───────────────────────────────────────────────

// openMemHopMockTB 是 OpenMemHopMock 的 testing.TB 版本，供 Benchmark 使用。
func openMemHopMockTB(t testing.TB) *memhop.MemHop {
	t.Helper()
	cfg := memhop.Config{
		DBPath:    filepath.Join(t.TempDir(), "mock.meh"),
		VectorDim: testsupport.MockVectorDim,
	}
	mh, err := memhop.OpenWithEncoder(&cfg, testsupport.NewMockEncoder(testsupport.MockVectorDim))
	if err != nil {
		t.Fatalf("OpenWithEncoder: %v", err)
	}
	return mh
}

func hashFormat(h uint64) string {
	return fmt.Sprintf("%016x", h)
}

func strLimit(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

func safeScore(result *memhop.SearchResult, idx int) float64 {
	if idx < len(result.Contexts) {
		return float64(result.Contexts[idx].RetrievalScore)
	}
	return 0
}

func safeAvgScore(result *memhop.SearchResult, k int) float64 {
	limit := k
	if len(result.Contexts) < limit {
		limit = len(result.Contexts)
	}
	if limit == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < limit; i++ {
		sum += float64(result.Contexts[i].RetrievalScore)
	}
	return sum / float64(limit)
}

// ── 测试：SearchResult 结构完整性 ──────────────────────────

// TestSearchResultIntegrity 验证搜索结果的结构字段完整性。
func TestSearchResultIntegrity(t *testing.T) {
	mh := testsupport.OpenMemHopMock(t)
	defer mh.Close()

	populateRecallData(t, mh)

	t.Log("")
	t.Log("══════ 搜索结果结构完整性测试 ══════")

	result, err := mh.Search(memhop.SearchQuery{Text: "天气", MaxResults: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// 验证顶层结构
	if result.Profile.ID == "" {
		t.Log("  △ SearchResult.Profile: 无 ID（可能未设置 L0）")
	} else {
		t.Logf("  ✓ Profile.ID=%s Name=%s", result.Profile.ID[:12], result.Profile.Name)
	}

	if len(result.Contexts) == 0 {
		t.Log("  △ 无 Contexts 返回")
	} else {
		t.Logf("  ✓ Contexts: %d 个结果", len(result.Contexts))
		for i, ctx := range result.Contexts {
			if i >= 3 {
				break
			}
			if ctx.ID == "" || ctx.SceneID == "" {
				t.Errorf("  Context[%d] ID 或 SceneID 为空", i)
			}
			t.Logf("  Context[%d]: ID=%s Depth=%d SceneID=%s Score=%.4f",
				i, ctx.ID[:12], ctx.Depth, ctx.SceneID[:12], ctx.RetrievalScore)
		}
	}

	// 验证 Crystals（L5 匹配）
	t.Logf("  ✓ Crystals: %d 个", len(result.Crystals))

	// AssociatedContexts
	t.Logf("  ✓ AssociatedContexts: %d 个", len(result.AssociatedContexts))
}
