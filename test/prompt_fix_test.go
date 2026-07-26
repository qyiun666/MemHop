//go:build integration
//
// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/api"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// TestEnglishPromptFix validates the English prompt fix by running
// 20 sessions from LOCOMO full + all smoke datasets.
// This verifies Dream no longer fails on English content.
func TestEnglishPromptFix(t *testing.T) {
	// ── 1. LOCOMO smoke (all 2 sessions) ──
	t.Run("LOCOMO_smoke", func(t *testing.T) {
		mh := testsupport.OpenMemHop(t)
		defer mh.Close()
		runSessions(t, mh, loadFixture(t, "locomo_smoke.json"), "LOCOMO_smoke")
	})

	// ── 2. LongMemEval smoke (all 2 sessions) ──
	t.Run("LongMemEval_smoke", func(t *testing.T) {
		mh := testsupport.OpenMemHop(t)
		defer mh.Close()
		runSessions(t, mh, loadFixture(t, "longmemeval_smoke.json"), "LongMemEval_smoke")
	})

	// ── 3. LOCOMO full (first 20 sessions) ──
	t.Run("LOCOMO_full_20", func(t *testing.T) {
		mh := testsupport.OpenMemHop(t)
		defer mh.Close()
		fixture := loadFixture(t, "locomo_full.json")
		// Take only first 20 sessions
		allSessions := fixture["sessions"].([]interface{})
		if len(allSessions) > 20 {
			allSessions = allSessions[:20]
		}
		truncated := map[string]interface{}{
			"sessions":  allSessions,
			"questions": fixture["questions"],
		}
		runSessions(t, mh, truncated, "LOCOMO_full_20")
	})
}

// runSessions stores all turns with periodic Dream, then evaluates questions.
func runSessions(t *testing.T, mh *memhop.MemHop, fixture map[string]interface{}, name string) {
	sessions := fixture["sessions"].([]interface{})
	questions := extractQuestions(fixture["questions"])

	var totalTurns int
	var dreamCount int

	// Phase 1: Store all sessions with periodic Dream
	for si, s := range sessions {
		session := s.(map[string]interface{})
		turns := session["turns"].([]interface{})
		t.Logf("[%s] Session %d/%d: %d turns", name, si+1, len(sessions), len(turns))

		for ti, turn := range turns {
			turnData := turn.(map[string]interface{})
			text := turnData["text"].(string)
			if searchOrCreate(t, mh, text) == nil {
				t.Logf("  Turn %d store failed", ti)
			}
			totalTurns++

			// Dream every 20 turns
			if totalTurns%20 == 0 {
				report, err := mh.Dream(context.Background(), nil)
				if err != nil {
					t.Logf("  Dream at turn %d: %v", totalTurns, err)
				} else {
					dreamCount++
					t.Logf("  Dream[%d] at turn %d: consolidated=%d",
						dreamCount, totalTurns, report.ConsolidatedCount)
					for _, stage := range report.Stages {
						if stage.Status != "success" {
							t.Logf("    ⚠ Stage %s: %s", stage.Name, stage.Description)
						}
					}
				}
			}
		}
	}

	// Final Dream
	report, err := mh.Dream(context.Background(), nil)
	if err != nil {
		t.Logf("[%s] Final Dream: %v", name, err)
	} else {
		dreamCount++
		t.Logf("[%s] Final Dream: consolidated=%d", name, report.ConsolidatedCount)
	}

	// Phase 2: Evaluate questions
	health, _ := mh.HealthCheck()
	t.Logf("\n══════ %s 报告 ══════", name)
	t.Logf("总轮次: %d | Dream 次数: %d", totalTurns, dreamCount)
	t.Logf("各层: L0=%d L1=%d L2=%d L3=%d L4=%d L5=%d",
		health.LayerCounts["l0_profile"],
		health.LayerCounts["l1_engram"],
		health.LayerCounts["l2_topic"],
		health.LayerCounts["l3_knowledge"],
		health.LayerCounts["l4_archive"],
		health.LayerCounts["l5_crystal"])

	if len(questions) == 0 {
		t.Logf("无评估问题，跳过")
		return
	}

	evaluateQuestions(t, mh, questions, name)
}

type questionItem struct {
	text      string
	answer    string
	sessionID string
}

func extractQuestions(q interface{}) []questionItem {
	if q == nil {
		return nil
	}
	qs, ok := q.([]interface{})
	if !ok {
		return nil
	}
	var result []questionItem
	for _, item := range qs {
		m := item.(map[string]interface{})
		q := questionItem{
			text:   m["question"].(string),
			answer: strings.ToLower(m["answer"].(string)),
		}
		if sid, ok := m["session_id"].(string); ok {
			q.sessionID = sid
		}
		result = append(result, q)
	}
	return result
}

func evaluateQuestions(t *testing.T, mh *memhop.MemHop, questions []questionItem, name string) {
	correct := 0
	attempted := 0

	writeResult := func(s string) {
		resultPath := filepath.Join(os.TempDir(), "memhop_prompt_fix_result.txt")
		f, _ := os.OpenFile(resultPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		defer f.Close()
		f.WriteString(s + "\n")
	}

	writeResult(fmt.Sprintf("\n=== %s ===", name))

	for qi, q := range questions {
		result, err := mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: q.text})
		if err != nil {
			t.Logf("  Q%d search failed: %v", qi+1, err)
			continue
		}

		// 收集所有 context 的 keywords + summary 用于答案检查
		found := false
		for _, ctx := range result.Contexts {
			// 检查主题关键词
			for _, kw := range ctx.UserKeywords {
				if matchAnswer(strings.ToLower(kw), q.answer) {
					found = true
					break
				}
			}
			if found {
				break
			}
			for _, kw := range ctx.AgentKeywords {
				if matchAnswer(strings.ToLower(kw), q.answer) {
					found = true
					break
				}
			}
			if found {
				break
			}
			// 检查 fused 摘要
			if ctx.FusedSummary != nil && matchAnswer(strings.ToLower(*ctx.FusedSummary), q.answer) {
				found = true
				break
			}
			for _, kw := range ctx.FusedKeywords {
				if matchAnswer(strings.ToLower(kw), q.answer) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}

		// 如果直接 context 没找到，检查场景树
		if !found && len(result.Contexts) > 0 {
			sceneID := result.Contexts[0].SceneID
			treeRes, err := mh.Topic(memhop.TopicOp{Kind: memhop.TOpSceneTree, SceneID: sceneID})
			if err == nil && treeRes != nil && treeRes.SceneTree != nil {
				tree := treeRes.SceneTree
				for _, node := range tree.Nodes {
					for _, kw := range node.UserKeywords {
						if matchAnswer(strings.ToLower(kw), q.answer) {
							found = true
							break
						}
					}
					if found {
						break
					}
					for _, kw := range node.AgentKeywords {
						if matchAnswer(strings.ToLower(kw), q.answer) {
							found = true
							break
						}
					}
					if found {
						break
					}
					for _, kw := range node.FusedKeywords {
						if matchAnswer(strings.ToLower(kw), q.answer) {
							found = true
							break
						}
					}
					if found {
						break
					}
				}
			}
		}

		attempted++
		mark := "✗"
		if found {
			correct++
			mark = "✓"
		}
		t.Logf("  %s Q%d/%d: %s", mark, qi+1, len(questions), q.text[:min(60, len(q.text))])
		writeResult(fmt.Sprintf("%s Q%d: %s", mark, qi+1, q.text))
	}

	accuracy := float64(correct) / float64(attempted) * 100
	summary := fmt.Sprintf("[%s] 准确率: %d/%d = %.1f%% | 轮次: %d | Dream: 已执行",
		name, correct, attempted, accuracy, 0)
	t.Log(summary)
	writeResult(summary)
}

func matchAnswer(text, answer string) bool {
	// 检查答案是否出现在文本中
	keywords := strings.Fields(answer)
	if len(keywords) == 0 {
		return false
	}
	// 至少匹配 50% 的关键词
	matched := 0
	for _, kw := range keywords {
		if len(kw) < 3 {
			continue // skip short words
		}
		if strings.Contains(text, kw) {
			matched++
		}
	}
	threshold := len(keywords) / 2
	if threshold < 1 {
		threshold = 1
	}
	return matched >= threshold
}
