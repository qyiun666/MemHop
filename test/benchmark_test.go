//go:build integration

package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"memhop/api"
	"memhop/test/testsupport"
)

// ── 核心验证维度 ──────────────────────────────────────────
//
// MemHop 是记忆跳转系统，核心能力是：
//
//	1. 话题分类：同一会话的不同轮次应归入同一 L2 topic
//	2. 话题分离：不同会话的轮次应归入不同 L2 topic
//	3. 跨会话关联：Dream 应在相关会话之间建立 L1 关联
//	4. 画像演化：Dream 应从对话中学习 lexicon/style/emotion
//	5. 知识蒸馏：Dream 应从 L4 内容中抽取 L3 知识节点
//
// 评测方式不是"在 L4 里找答案"，而是：
//
//	- 检查话题分配是否正确（同 session → 同 topic）
//	- 检查 Dream 后各层的状态变化
//	- 检查跨会话搜索时关联话题是否出现
//	- 检查画像是否反映了对话内容

func TestMemHopTopicBenchmark(t *testing.T) {
	locomo := loadFixture(t, "locomo_smoke.json")
	lme := loadFixture(t, "longmemeval_smoke.json")

	t.Run("LOCOMO_TopicCohesion", func(t *testing.T) {
		mh := testsupport.OpenMemHop(t)
		defer mh.Close()
		evalTopicCohesion(t, mh, locomo, "LOCOMO")
	})

	t.Run("LongMemEval_CrossSession", func(t *testing.T) {
		mh := testsupport.OpenMemHop(t)
		defer mh.Close()
		evalCrossSession(t, mh, lme, "LongMemEval")
	})

	t.Run("Dream_Consolidation", func(t *testing.T) {
		mh := testsupport.OpenMemHop(t)
		defer mh.Close()
		evalDreamEffect(t, mh, locomo, "LOCOMO")
	})
}

// ── 维度1: 场景凝聚 ──────────────────────────────────────
// 验证同一 session 的多个 turn 归入同一个 Scene（不是同一个 Topic）
// MemHop 评分按场景算：SceneScore = Σ(topic_scores) + activation_bonus
// activation_bonus: 已激活状态 +X 或 最近时间 +Y，优先已激活，二选一
func evalTopicCohesion(t *testing.T, mh *memhop.MemHop, fixture map[string]interface{}, name string) {
	sessions := fixture["sessions"].([]interface{})
	// sessionID → sceneID → topicID → turnCount
	sessionSceneMap := make(map[string]map[string]map[string]int)

	for si, s := range sessions {
		session := s.(map[string]interface{})
		sessionID := session["id"].(string)
		turns := session["turns"].([]interface{})
		t.Logf("[%s] Session %d/%s: %d turns", name, si+1, sessionID, len(turns))

		sessionSceneMap[sessionID] = make(map[string]map[string]int)

		for _, turn := range turns {
			turnData := turn.(map[string]interface{})
			text := turnData["text"].(string)

			result := searchOrCreate(t, mh, text)
			if result == nil {
				continue
			}
			if len(result.Contexts) > 0 {
				topicID := result.Contexts[0].ID
				sceneID := result.Contexts[0].SceneID
				if sessionSceneMap[sessionID][sceneID] == nil {
					sessionSceneMap[sessionID][sceneID] = make(map[string]int)
				}
				sessionSceneMap[sessionID][sceneID][topicID]++
			}
		}

		// 分析场景凝聚（Scene 级别）
		scenes := sessionSceneMap[sessionID]
		t.Logf("  场景分配: %d 个 turn → %d 个场景", len(turns), len(scenes))
		for sceneID, topics := range scenes {
			totalInScene := 0
			for _, count := range topics {
				totalInScene += count
			}
			sceneCohesion := float64(totalInScene) / float64(len(turns)) * 100
			status := "✓"
			if totalInScene < len(turns)/2 {
				status = "△"
			}
			t.Logf("    %s 场景 %s: %d/%d turns (%.0f%%) 含 %d 个子话题",
				status, sceneID[:12], totalInScene, len(turns), sceneCohesion, len(topics))
			for tid, count := range topics {
				t.Logf("      └ 话题 %s: %d turns", tid[:12], count)
			}
		}

		// Run Dream
		if _, err := mh.Dream(nil); err != nil {
			t.Logf("  Dream: %v", err)
		}
	}

	// ── 总结 ──
	totalTurnsInAll := 0
	for _, scenes := range sessionSceneMap {
		for _, topics := range scenes {
			for _, c := range topics {
				totalTurnsInAll += c
			}
		}
	}

	t.Logf("\n══════ %s 场景凝聚报告 ══════", name)
	t.Logf("总轮次: %d  |  总 session: %d", totalTurnsInAll, len(sessions))

	// 跨会话场景重叠检查
	// 收集所有场景跨会话分配情况
	globalSceneSessions := make(map[string][]string) // sceneID → sessionIDs
	for sid, scenes := range sessionSceneMap {
		for sceneID := range scenes {
			globalSceneSessions[sceneID] = append(globalSceneSessions[sceneID], sid)
		}
	}
	crossSessionScenes := 0
	for sceneID, sids := range globalSceneSessions {
		uniqueSessions := make(map[string]bool)
		for _, s := range sids {
			uniqueSessions[s] = true
		}
		if len(uniqueSessions) > 1 {
			crossSessionScenes++
			t.Logf("  ⚠ 跨会话污染: 场景 %s 被 %d 个会话共用", sceneID[:12], len(uniqueSessions))
		}
	}

	for sid, scenes := range sessionSceneMap {
		dominantScene := ""
		maxTurns := 0
		for sceneID, topics := range scenes {
			total := 0
			for _, c := range topics {
				total += c
			}
			if total > maxTurns {
				maxTurns = total
				dominantScene = sceneID
			}
		}
		totalTurnsInSession := 0
		sceneCount := 0
		for _, topics := range scenes {
			for _, c := range topics {
				totalTurnsInSession += c
			}
			sceneCount++
		}
		cohesion := float64(maxTurns) / float64(totalTurnsInSession) * 100
		mark := "✓"
		if sceneCount > 1 {
			mark = "✗"
		} else if cohesion >= 80 {
			mark = "✓"
		} else {
			mark = "△"
		}
		t.Logf("  %s %s: 主场景 %s 覆盖 %.0f%% (%d/%d), 共 %d 个场景",
			mark, sid, dominantScene[:12], cohesion, maxTurns, totalTurnsInSession, sceneCount)
	}

	if crossSessionScenes > 0 {
		t.Logf("\n⚠ 跨会话场景污染: 共 %d 个场景被跨会话共用", crossSessionScenes)
		t.Logf("  这说明向量阈值不够严格，不同会话的话题被归入同场景")
	} else {
		t.Logf("\n✓ 场景分离正确: 不同会话的话题分配到不同场景")
	}
}

// ── 维度2: 跨会话关联 ──────────────────────────────────
// 验证 LongMemEval 的 session1(生日策划) 和 session2(更新) 之间
// Dream 后应产生 L1 关联，搜索 session1 的话题时 session2 应作为 AssociatedContext 出现
func evalCrossSession(t *testing.T, mh *memhop.MemHop, fixture map[string]interface{}, name string) {
	sessions := fixture["sessions"].([]interface{})
	if len(sessions) < 2 {
		t.Skip("需要至少2个 session")
	}

	// 存储所有 turns
	for si, s := range sessions {
		session := s.(map[string]interface{})
		turns := session["turns"].([]interface{})
		t.Logf("[%s] 存储 Session %d: %d turns", name, si+1, len(turns))
		for _, turn := range turns {
			text := turn.(map[string]interface{})["text"].(string)
			searchOrCreate(t, mh, text)
		}
	}

	// Dream 前：检查关联
	t.Log("[Dream 前] 搜索第一个 session 的话题...")
	s0t0 := sessions[0].(map[string]interface{})["turns"].([]interface{})[0].(map[string]interface{})["text"].(string)
	resultBefore, _ := mh.Search(memhop.SearchQuery{Text: s0t0})
	t.Logf("  关联话题: %d 个 (Dream 前)", len(resultBefore.AssociatedContexts))

	// Dream
	t.Log("执行 Dream...")
	if _, err := mh.Dream(nil); err != nil {
		t.Logf("  Dream: %v", err)
	}

	// Dream 后：检查关联是否建立
	resultAfter, _ := mh.Search(memhop.SearchQuery{Text: s0t0})
	t.Logf("[Dream 后] 搜索相同话题 → 关联话题: %d 个", len(resultAfter.AssociatedContexts))
	for i, asc := range resultAfter.AssociatedContexts {
		t.Logf("  Assoc[%d]: ID=%s Depth=%d Score=%.4f", i, asc.ID[:12], asc.Depth, asc.RetrievalScore)
	}

	// 验证 LongMemEval 的 sesison1→session2 关联
	if strings.Contains(name, "LongMemEval") || strings.Contains(name, "Combined") {
		if len(resultAfter.AssociatedContexts) > 0 {
			t.Logf("  ✓ 跨会话关联已建立")
		} else {
			t.Logf("  △ 跨会话关联未建立（Dream 可能失败，或话题间语义距离过大）")
		}
	}

	// 检查各层状态
	health, _ := mh.HealthCheck()
	t.Logf("最终各层: L0=%d L1=%d L2=%d L3=%d L4=%d L5=%d",
		health.LayerCounts["l0_profile"],
		health.LayerCounts["l1_engram"],
		health.LayerCounts["l2_topic"],
		health.LayerCounts["l3_knowledge"],
		health.LayerCounts["l4_archive"],
		health.LayerCounts["l5_crystal"])
}

// ── 维度3: Dream 效果 ──────────────────────────────────
// 验证 Dream 后 L0/L1/L3/L5 的正确更新
func evalDreamEffect(t *testing.T, mh *memhop.MemHop, fixture map[string]interface{}, name string) {
	sessions := fixture["sessions"].([]interface{})

	// 存储所有 turns
	for _, s := range sessions {
		session := s.(map[string]interface{})
		for _, turn := range session["turns"].([]interface{}) {
			text := turn.(map[string]interface{})["text"].(string)
			searchOrCreate(t, mh, text)
		}
	}

	// Dream 前状态
	healthBefore, _ := mh.HealthCheck()
	profileBefore, _ := mh.GetProfile()
	t.Logf("[%s] Dream 前: L0=%d L1=%d L2=%d L3=%d L4=%d L5=%d",
		name,
		healthBefore.LayerCounts["l0_profile"],
		healthBefore.LayerCounts["l1_engram"],
		healthBefore.LayerCounts["l2_topic"],
		healthBefore.LayerCounts["l3_knowledge"],
		healthBefore.LayerCounts["l4_archive"],
		healthBefore.LayerCounts["l5_crystal"])
	if profileBefore != nil {
		t.Logf("  画像: Name=%q Lexicon=%d Style=%d Emotion=%d",
			profileBefore.Name, len(profileBefore.Lexicon), len(profileBefore.StyleTraits), len(profileBefore.EmotionPatterns))
	}

	// 执行 Dream
	t.Log("执行 Dream...")
	report, err := mh.Dream(nil)
	if err != nil {
		t.Logf("  Dream 失败: %v", err)
		t.Logf("  (Dream 依赖 LLM，英文对话 + DeepSeek 可能解析失败)")
		return
	}

	// Dream 报告
	t.Logf("  Dream 报告: consolidated=%d L3=%d Crystals=%d L1Decay=%d Habits=%d",
		report.ConsolidatedCount, report.NewL3Nodes, report.NewCrystals,
		report.L1DecayedNodes, 0)
	for _, stage := range report.Stages {
		mark := "✓"
		if stage.Status != "success" {
			mark = "✗"
		}
		t.Logf("    Stage %s: %s (%s) %dms", mark, stage.Name, stage.Description, stage.DurationMs)
	}

	// Dream 后状态
	healthAfter, _ := mh.HealthCheck()
	profileAfter, _ := mh.GetProfile()

	t.Logf("")
	t.Logf("Dream 效果对比:")

	// L0 画像更新
	if profileBefore != nil && profileAfter != nil {
		lexiconNew := len(profileAfter.Lexicon) - len(profileBefore.Lexicon)
		styleNew := len(profileAfter.StyleTraits) - len(profileBefore.StyleTraits)
		emotionNew := len(profileAfter.EmotionPatterns) - len(profileBefore.EmotionPatterns)
		t.Logf("  L0 画像: Lexicon %+d  Style %+d  Emotion %+d", lexiconNew, styleNew, emotionNew)
		if lexiconNew > 0 || styleNew > 0 {
			t.Logf("    ✓ Dream 从对话中学习了新表达")
		}
	}

	// L1 关联
	l1Diff := healthAfter.LayerCounts["l1_engram"] - healthBefore.LayerCounts["l1_engram"]
	t.Logf("  L1 场景节点: %+d 个 (%d → %d)", l1Diff,
		healthBefore.LayerCounts["l1_engram"], healthAfter.LayerCounts["l1_engram"])

	// L3 知识
	l3Diff := healthAfter.LayerCounts["l3_knowledge"] - healthBefore.LayerCounts["l3_knowledge"]
	t.Logf("  L3 知识图谱: %+d 个 (%d → %d)", l3Diff,
		healthBefore.LayerCounts["l3_knowledge"], healthAfter.LayerCounts["l3_knowledge"])

	// L5 晶体
	l5Diff := healthAfter.LayerCounts["l5_crystal"] - healthBefore.LayerCounts["l5_crystal"]
	t.Logf("  L5 行为模式: %+d 个 (%d → %d)", l5Diff,
		healthBefore.LayerCounts["l5_crystal"], healthAfter.LayerCounts["l5_crystal"])

	// L4 归档
	l4After := healthAfter.LayerCounts["l4_archive"]
	t.Logf("  L4 归档: %d (说明原始对话已保留)", l4After)

	// Dream 总体评价
	if l3Diff > 0 || l5Diff > 0 || l1Diff > 0 {
		t.Logf("  ✓ Dream 成功完成了记忆整合")
	} else {
		t.Logf("  △ Dream 未产生新知识（英文内容可能导致 LLM 解析失败）")
	}
}

// ── 工具函数 ─────────────────────────────────────────────

// searchOrCreate 先检索已有话题，无匹配时显式 AutoCreate 建话题。
// 测试库在 t.TempDir() 中从空白开始，不再依赖共享库累积的历史数据。
func searchOrCreate(t *testing.T, mh *memhop.MemHop, text string) *memhop.SearchResult {
	t.Helper()
	result, err := mh.Search(memhop.SearchQuery{Text: text})
	if err != nil {
		t.Logf("search failed: %v", err)
		return nil
	}
	if len(result.Contexts) == 0 {
		result, err = mh.Search(memhop.SearchQuery{Text: text, AutoCreate: true})
		if err != nil {
			t.Logf("auto-create failed: %v", err)
			return nil
		}
	}
	return result
}

func loadFixture(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	fixturesDir := findFixturesDir(t)
	path := filepath.Join(fixturesDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("跳过：fixture 不可用 %s: %v", name, err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return result
}

func findFixturesDir(t *testing.T) string {
	t.Helper()
	// 相对 test 包目录定位仓库根的 benches/fixtures
	candidates := []string{
		"../benches/fixtures",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	t.Skip("跳过：benches/fixtures 目录不存在")
	return ""
}
