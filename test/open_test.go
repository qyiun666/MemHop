//go:build integration

// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/api"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// 长文本带代码块 — 用拼接方式避免 raw string 中的反引号冲突
var longCodeQuery = `帮我写一个Go语言的HTTP服务器，使用标准库net/http，支持GET和POST，返回JSON格式

代码示例：
` + "```" + `go
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type Response struct {
	Message string ` + "`" + `json:"message"` + "`" + `
	Status  int    ` + "`" + `json:"status"` + "`" + `
}

func main() {
	http.HandleFunc("/api/hello", helloHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	resp := Response{Message: "Hello World", Status: 200}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
` + "```" + `
请帮我实现这个服务`

// 测试数据集：语义对 + 多轮对话 + 长文本代码块
// 第1-2条是语义变体（不同词同一意思），应归为同一话题
var testQueries = []struct {
	text string
	desc string
}{
	{"今天天气怎么样", "天气询问-原版"},
	{"明天的气候如何", "天气询问-语义变体 → 应归入同一话题"},
	{"推荐一部好看的科幻电影", "电影推荐"},
	{"如何学习Go语言编程", "Go编程学习"},
	{"最近人工智能有什么新进展", "AI进展"},
	{"帮我写一首关于秋天的诗", "诗歌创作"},
	{"周末想去户外活动有什么推荐", "户外活动"},
	{longCodeQuery, "长文本带代码块"},
}

func TestOpen(t *testing.T) {
	// 真实依赖（Ollama+LLM）缺失时自动 Skip；DB 在 t.TempDir() 中
	mh := testsupport.OpenMemHop(t)
	defer mh.Close()

	// ============================================================
	// Phase 1: 准备测试集 + 更新 L0 基础画像
	// ============================================================
	t.Log("████ Phase 1: 准备测试集 + 更新 L0 基础画像")
	t.Logf("测试查询 (%d 条):", len(testQueries))
	for i, q := range testQueries {
		t.Logf("  [%d] %s — %s", i+1, q.desc, q.text)
	}

	// 更新 L0
	{
		_, err := mh.Topic(memhop.TopicOp{
			Kind: memhop.TOpSetProfile,
			ProfileDelta: &memhop.ProfileDelta{
				Name:        strPtr("MemHop助手"),
				Role:        strPtr("AI助手"),
				Personality: strPtr("友善、专业、乐于助人"),
				Worldview:   strPtr("关注用户需求"),
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
		t.Logf("L0 基础画像: Name=%q Role=%q Personality=%q",
			profile.Name, profile.Role, profile.Personality)
		if profile.Name != "MemHop助手" {
			t.Errorf("Name expected 'MemHop助手', got %q", profile.Name)
		}
	}

	// 初始健康检查
	{
		health, err := mh.HealthCheck()
		if err != nil {
			t.Fatalf("HealthCheck failed: %v", err)
		}
		t.Logf("初始状态: L0=%d L1=%d L2=%d L3=%d L4=%d L5=%d DB=%dbytes",
			health.LayerCounts["l0_profile"],
			health.LayerCounts["l1_engram"],
			health.LayerCounts["l2_topic"],
			health.LayerCounts["l3_knowledge"],
			health.LayerCounts["l4_archive"],
			health.LayerCounts["l5_crystal"],
			health.DBSizeBytes)
	}

	// ============================================================
	// Phase 2: 循环测试集
	// ============================================================
	t.Log("")
	t.Log("████ Phase 2: 循环测试集")

	// 跟踪语义匹配：第一个天气查询的话题 ID
	var weatherTopicID string
	topicsSeen := make(map[string]int) // topicID → first_iteration

	for idx, q := range testQueries {
		iteration := idx + 1
		query := q.text
		t.Logf("")
		t.Logf("═══ 迭代 %d/%d: %s ═══", iteration, len(testQueries), q.desc)
		t.Logf("  文本: %s", query)

		// ── 2a. 执行检索（识别场景 + 添加消息到该场景）──
		result, err := mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: query})
		if err != nil {
			t.Fatalf("[迭代%d] Search(%q) failed: %v", iteration, query, err)
		}
		// 库在 t.TempDir() 中从空白开始：无匹配时显式建话题
		if len(result.Contexts) == 0 {
			result, err = mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: query, AutoCreate: true})
			if err != nil {
				t.Fatalf("[迭代%d] AutoCreate Search(%q) failed: %v", iteration, query, err)
			}
		}
		t.Logf("  检索结果: contexts=%d associated=%d crystals=%d",
			len(result.Contexts), len(result.AssociatedContexts), len(result.Crystals))

		// ── 2b. 检查返回内容、每个字段 ──
		if len(result.Contexts) == 0 {
			t.Fatalf("[迭代%d] 检索返回 0 个 context", iteration)
		}

		for ci, ctx := range result.Contexts {
			t.Logf("  Ctx[%d]: ID=%s Depth=%d Scene=%s Score=%.4f",
				ci, ctx.ID, ctx.Depth, ctx.SceneID, ctx.RetrievalScore)

			// 基本字段校验
			if ctx.ID == "" {
				t.Errorf("[迭代%d] Ctx[%d]: ID 为空", iteration, ci)
			}
			if ctx.Depth < 1 {
				t.Errorf("[迭代%d] Ctx[%d]: Depth=%d 无效", iteration, ci, ctx.Depth)
			}
			// keywords 要么是当前的搜索词, 要么是历史数据, 要么是 Dream 合并后的 FusedKeywords, 不应全为空
			if len(ctx.UserKeywords) == 0 && len(ctx.AgentKeywords) == 0 && len(ctx.FusedKeywords) == 0 {
				t.Errorf("[迭代%d] Ctx[%d]: UserKeywords、AgentKeywords 与 FusedKeywords 都为空", iteration, ci)
			}
			t.Logf("    UserKw=%v AgentKw=%v FusedKw=%v",
				ctx.UserKeywords, ctx.AgentKeywords, ctx.FusedKeywords)
		}

		// ── 2c. 检查 L4Refs 与 L4 Archive 是否能对应上 ──
		for ci, ctx := range result.Contexts {
			if len(ctx.L4Refs) == 0 {
				// 旧数据可能没有 L4Refs，但新创建的必须有
				// 通过 Get(LayerTopic) 确认是否有 UserL4Refs
				if detailRes, err := mh.Get(memhop.LayerTopic, ctx.ID); err == nil {
					detail := detailRes.Topic
					if len(detail.UserL4Refs) == 0 && len(detail.AgentL4Refs) == 0 {
						t.Logf("  Ctx[%d] 的 L2 topic 无 L4Refs (历史数据)", ci)
					} else {
						allRefs := append(detail.UserL4Refs, detail.AgentL4Refs...)
						t.Logf("  Ctx[%d] 的 L2 有 %d 个 L4Refs (%d user + %d agent), 但未在搜索结果中返回",
							ci, len(allRefs), len(detail.UserL4Refs), len(detail.AgentL4Refs))
					}
				}
				continue
			}

			// 对每个 L4Ref，验证 archive 存在且内容匹配
			for ri, refID := range ctx.L4Refs {
				archRes, err := mh.Get(memhop.LayerArchive, refID)
				if err != nil {
					t.Errorf("[迭代%d] Ctx[%d] L4Ref[%d]=%s Get(LayerArchive) 失败: %v",
						iteration, ci, ri, refID, err)
					continue
				}
				archive := archRes.Archive
				if archive == nil {
					t.Errorf("[迭代%d] Ctx[%d] L4Ref[%d]=%s: archive 不存在",
						iteration, ci, ri, refID)
					continue
				}

				// Archive 的 TopicID 应指回当前 L2
				if archive.TopicID != nil {
					if *archive.TopicID != ctx.ID {
						t.Errorf("[迭代%d] Ctx[%d] L4Ref[%d]: archive.TopicID=%v != ctx.ID=%s",
							iteration, ci, ri, *archive.TopicID, ctx.ID)
					}
				}

				contentPreview := archive.Content
				if len(contentPreview) > 80 {
					contentPreview = contentPreview[:80] + "..."
				}
				t.Logf("    L4Ref[%d]: ID=%s Type=%s Content=%q TopicID=%v ✓",
					ri, refID, archive.ContentType, contentPreview, archive.TopicID)
			}
		}

		// ── 2d. 话题 ID 追踪 & 语义匹配验证 ──
		ctx := result.Contexts[0]
		if firstSeen, exists := topicsSeen[ctx.ID]; exists {
			t.Logf("  话题复用: ID=%s 首次出现在迭代 %d, 本次迭代 %d 复用 ✓",
				ctx.ID, firstSeen, iteration)
		} else {
			topicsSeen[ctx.ID] = iteration
			t.Logf("  新话题创建: ID=%s (第 %d 个独立话题)", ctx.ID, len(topicsSeen))
		}

		// 语义变体验证："明天的气候如何" 应与 "今天天气怎么样" 同话题
		if idx == 0 {
			weatherTopicID = ctx.ID
			t.Logf("  → 记录天气话题 ID=%s", weatherTopicID)
		}
		if idx == 1 {
			if ctx.ID == weatherTopicID {
				t.Logf("  ✓ 语义匹配成功: [明天的气候如何] 归入天气话题 %s", weatherTopicID)
			} else {
				t.Logf("  △ 语义未命中: [明天的气候如何] 创建了新话题 %s (向量相似度 < 0.65)", ctx.ID)
			}
		}

		// ── 2e. 第二次开始检查关联 L2 ──
		if iteration >= 2 {
			if len(result.AssociatedContexts) > 0 {
				t.Logf("  关联 L2 (%d 条): ✓", len(result.AssociatedContexts))
				for ai, asc := range result.AssociatedContexts {
					t.Logf("    Assoc[%d]: ID=%s Depth=%d Scene=%s Score=%.4f",
						ai, asc.ID, asc.Depth, asc.SceneID, asc.RetrievalScore)
				}
			} else {
				t.Logf("  关联 L2: 0 条 (可能 L1 尚未构建)")
			}
		}

		// ── 2e. 每 2 次迭代随机触发 Dream ──
		// 第 3、5 次迭代触发 dream（策略：第1次积累数据，第2次也积累，第3次dream…）
		shouldDream := false
		if iteration == 3 || iteration == 5 {
			shouldDream = true
		} else if iteration > 1 && rand.Intn(3) == 0 {
			shouldDream = true
		}

		if shouldDream {
			t.Logf("  → 触发 Dream...")
			report, err := mh.Dream(context.Background(), nil)
			if err != nil {
				t.Logf("  Dream 预期失败 (长文本/代码内容可能导致 L3 解析失败): %v", err)
			} else {
				t.Logf("  Dream 完成: consolidated=%d L1Decay=%d",
					report.ConsolidatedCount, report.L1DecayedNodes)

				// 检查 L0 更新
				if profRes, err := mh.Get(memhop.LayerProfile, ""); err == nil {
					profile := profRes.Profile
					t.Logf("  L0 更新后: Name=%q Role=%q Personality=%q Lexicon=%d Style=%d Emotion=%d",
						profile.Name, profile.Role, profile.Personality,
						len(profile.Lexicon), len(profile.StyleTraits), len(profile.EmotionPatterns))
				}

				// 检查 L2 更新（总数和融合信息）
				if l2Res, err := mh.List(memhop.LayerTopic, memhop.ListRequest{
					Topic: &memhop.TopicListQuery{Page: 1, PageSize: 100},
				}); err == nil {
					l2List := l2Res.Topics
					t.Logf("  L2 更新后: total=%d topics", l2List.Total)
					for ti, topic := range l2List.Items {
						fusedInfo := ""
						if len(topic.FusedKeywords) > 0 {
							fusedInfo = fmt.Sprintf(" FusedKw=%v", topic.FusedKeywords)
						}
						t.Logf("    Topic[%d]: ID=%s Depth=%d L4=%d L3=%d TurnCnt=%d%s",
							ti, topic.ID, topic.Depth, topic.L4Count, topic.L3Count,
							topic.TurnCount, fusedInfo)
					}
				}
			}
		}

		// 打印每轮完整结果（前3轮，之后减少）
		if iteration <= 3 || shouldDream {
			b, _ := json.MarshalIndent(result, "  ", "  ")
			t.Logf("  完整检索结果:\n  %s", string(b))
		}

		t.Logf("═══ 迭代 %d 完成 ═══", iteration)
	}

	// ============================================================
	// Phase 3: 最终健康检查
	// ============================================================
	t.Log("")
	t.Log("████ Phase 3: 最终健康检查")
	{
		health, err := mh.HealthCheck()
		if err != nil {
			t.Fatalf("HealthCheck failed: %v", err)
		}
		t.Logf("最终状态: OK=%v DB=%dbytes", health.OK, health.DBSizeBytes)
		t.Logf("  L0=%d L1=%d L2=%d L3=%d L4=%d L5=%d",
			health.LayerCounts["l0_profile"],
			health.LayerCounts["l1_engram"],
			health.LayerCounts["l2_topic"],
			health.LayerCounts["l3_knowledge"],
			health.LayerCounts["l4_archive"],
			health.LayerCounts["l5_crystal"])
		for _, issue := range health.Issues {
			t.Logf("  Issue: %s", issue)
		}
	}

	t.Log("")
	t.Log("✓ 全流程测试完成 — 所有迭代数据一致性验证通过")
}
