//go:build integration
//
// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/api"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// TestLOCOMOFull runs the full LOCOMO benchmark: 272 sessions, 1986 questions.
func TestLOCOMOFull(t *testing.T) {
	fixture := loadFixture(t, "locomo_full.json")

	mh := testsupport.OpenMemHop(t)
	defer mh.Close()

	sessions := fixture["sessions"].([]interface{})
	t.Logf("[LOCOMO Full] %d sessions to process", len(sessions))

	// Phase 1: Store all sessions with periodic Dream every 20 turns
	sessionCount := 0
	totalTurns := 0
	lastDreamTurn := 0
	for _, s := range sessions {
		session := s.(map[string]interface{})
		turns := session["turns"].([]interface{})
		for _, turn := range turns {
			text := turn.(map[string]interface{})["text"].(string)
			mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: text, AutoCreate: true})
			totalTurns++
		}
		sessionCount++
		if sessionCount%50 == 0 {
			t.Logf("  Session %d/%d done (%d turns)", sessionCount, len(sessions), totalTurns)
		}

		// Dream every ~20 turns (after each session batch if close enough)
		if totalTurns-lastDreamTurn >= 20 {
			if _, err := mh.Dream(context.Background(), nil); err != nil {
				t.Logf("  Dream at turn %d: %v", totalTurns, err)
			} else {
				t.Logf("  Dream at turn %d: OK (L1=%d L2=%d L3=%d)",
					totalTurns, -1, -1, -1)
			}
			lastDreamTurn = totalTurns
		}
	}
	// Final Dream after all sessions
	t.Log("Final Dream...")
	report, err := mh.Dream(context.Background(), nil)
	if err != nil {
		t.Logf("  Final Dream: %v", err)
	} else {
		t.Logf("  Final Dream: consolidated=%d", report.ConsolidatedCount)
	}
	t.Logf("All %d sessions stored (%d turns total), Dream %d times", sessionCount, totalTurns, totalTurns/20)

	// Phase 2: Evaluate questions
	questions := fixture["questions"].([]interface{})
	t.Logf("Evaluating %d questions...", len(questions))

	correct := 0
	total := 0
	byCategory := make(map[string]int)
	categoryCorrect := make(map[string]int)

	for qi, q := range questions {
		qData := q.(map[string]interface{})
		qText := qData["question"].(string)
		qAnswer := qData["answer"].(string)
		qCategory := "unknown"
		if cat, ok := qData["category"]; ok {
			qCategory = cat.(string)
		}

		total++
		byCategory[qCategory]++

		result, err := mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: qText, AutoCreate: true})
		if err != nil {
			continue
		}

		// Collect unique scene IDs from search results
		sceneIDs := make(map[string]bool)
		for _, ctx := range result.Contexts {
			if ctx.SceneID != "" {
				sceneIDs[ctx.SceneID] = true
			}
		}

		answerLower := strings.ToLower(qAnswer)

		// For each scene, get ALL depth-1 topics and check their keywords
		found := false
		for sceneID := range sceneIDs {
			treeRes, err := mh.Topic(memhop.TopicOp{Kind: memhop.TOpSceneTree, SceneID: sceneID})
			if err != nil || treeRes == nil || treeRes.SceneTree == nil {
				continue
			}
			tree := treeRes.SceneTree
			for _, node := range tree.Nodes {
				for _, kw := range node.UserKeywords {
					if strings.Contains(strings.ToLower(kw), answerLower) ||
						strings.Contains(answerLower, strings.ToLower(kw)) {
						found = true
						break
					}
				}
				if found {
					break
				}
				for _, kw := range node.AgentKeywords {
					if strings.Contains(strings.ToLower(kw), answerLower) ||
						strings.Contains(answerLower, strings.ToLower(kw)) {
						found = true
						break
					}
				}
				if found {
					break
				}
				for _, kw := range node.FusedKeywords {
					if strings.Contains(strings.ToLower(kw), answerLower) ||
						strings.Contains(answerLower, strings.ToLower(kw)) {
						found = true
						break
					}
				}
				if found {
					break
				}
				if node.FusedSummary != nil &&
					strings.Contains(strings.ToLower(*node.FusedSummary), answerLower) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}

		if found {
			correct++
			categoryCorrect[qCategory]++
		}

		if qi%200 == 0 {
			t.Logf("  Question %d/%d, accuracy so far: %d/%d (%.1f%%)",
				qi, len(questions), correct, total,
				float64(correct)/float64(total)*100)
		}
	}

	// Phase 3: Report
	acc := float64(correct) / float64(total) * 100
	t.Logf("")
	t.Logf("══════ LOCOMO Full 端到端准确率 ══════")
	t.Logf("总问题: %d  |  正确: %d  |  准确率: %.1f%%", total, correct, acc)
	t.Logf("")
	t.Logf("按类别:")
	for cat, cnt := range byCategory {
		catAcc := float64(categoryCorrect[cat]) / float64(cnt) * 100
		t.Logf("  %-25s: %d/%d = %.1f%%", cat, categoryCorrect[cat], cnt, catAcc)
	}

	health, _ := mh.HealthCheck()
	t.Logf("")
	t.Logf("最终状态: L0=%d L1=%d L2=%d L3=%d L4=%d L5=%d",
		health.LayerCounts["l0_profile"],
		health.LayerCounts["l1_engram"],
		health.LayerCounts["l2_topic"],
		health.LayerCounts["l3_knowledge"],
		health.LayerCounts["l4_archive"],
		health.LayerCounts["l5_crystal"])
}
