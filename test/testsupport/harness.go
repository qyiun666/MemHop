package testsupport

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/api"
)

// SearchUpdatePair 执行一次完整的用户-Agent 交互：
//  1. Search(Text=userText) → 若无匹配则带 AutoCreate=true 重试
//  2. 从结果中取首个 Context 的 ID 作为 topicID
//  3. UpdateMemory(ID=topicID, Layer=2, Fields={"dialogue_text": userText, "role": 0})
//  4. UpdateMemory(ID=topicID, Layer=2, Fields={"dialogue_text": agentText, "role": 1})
//
// 返回 topicID 和最终的搜索结果。
func SearchUpdatePair(t *testing.T, mh *memhop.MemHop, userText, agentText string) (string, *memhop.SearchResult) {
	t.Helper()

	// 第一步：不带 AutoCreate 搜索
	q := memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: userText}
	result, err := mh.Search(q)
	if err != nil {
		t.Fatalf("Search(Text=%q, AutoCreate=false): %v", userText, err)
	}

	// 无匹配时，带 AutoCreate=true 重试
	if len(result.Contexts) == 0 {
		q.AutoCreate = true
		result, err = mh.Search(q)
		if err != nil {
			t.Fatalf("Search(Text=%q, AutoCreate=true): %v", userText, err)
		}
		if len(result.Contexts) == 0 {
			t.Fatalf("搜索无结果: Text=%q, 即使带 AutoCreate=true", userText)
		}
	}

	topicID := result.Contexts[0].ID

	// 写入用户侧对话
	userTextJSON, err := json.Marshal(userText)
	if err != nil {
		t.Fatalf("json.Marshal(userText): %v", err)
	}
	_, err = mh.UpdateMemory(memhop.UpdateRequest{
		ID:        topicID,
		Layer:     2,
		Timestamp: time.Now().UnixMilli(),
		Fields: map[string]json.RawMessage{
			"dialogue_text": userTextJSON,
			"role":          json.RawMessage(`0`),
		},
	})
	if err != nil {
		t.Fatalf("UpdateMemory(ID=%q, Layer=2, role=0): %v", topicID, err)
	}

	// 写入 Agent 侧对话
	agentTextJSON, err := json.Marshal(agentText)
	if err != nil {
		t.Fatalf("json.Marshal(agentText): %v", err)
	}
	_, err = mh.UpdateMemory(memhop.UpdateRequest{
		ID:        topicID,
		Layer:     2,
		Timestamp: time.Now().UnixMilli(),
		Fields: map[string]json.RawMessage{
			"dialogue_text": agentTextJSON,
			"role":          json.RawMessage(`1`),
		},
	})
	if err != nil {
		t.Fatalf("UpdateMemory(ID=%q, Layer=2, role=1): %v", topicID, err)
	}

	// 最终搜索，返回最新结果
	finalResult, err := mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: userText})
	if err != nil {
		t.Fatalf("最终 Search(Text=%q): %v", userText, err)
	}
	return topicID, finalResult
}

// LogSearchQuality 打印搜索质量指标（不 Fatal，仅 t.Logf）。
// 指标包括：
//   - Contexts 数量
//   - 首个 Context 的 RetrievalScore
//   - UserKeywords / AgentKeywords 数量
//   - FusedSummary 是否存在
//   - L4Refs 数量
//   - AssociatedContexts 数量
func LogSearchQuality(t *testing.T, result *memhop.SearchResult, query, expectedDesc string) {
	t.Helper()

	t.Logf("--- 搜索质量: %s ---", expectedDesc)
	t.Logf("  QueryText:        %q", query)
	t.Logf("  Contexts:         %d", len(result.Contexts))
	if len(result.Contexts) > 0 {
		c := result.Contexts[0]
		t.Logf("  TopContext ID:    %s", c.ID)
		t.Logf("  RetrievalScore:   %.4f", c.RetrievalScore)
		t.Logf("  UserKeywords:     %d", len(c.UserKeywords))
		t.Logf("  AgentKeywords:    %d", len(c.AgentKeywords))
		if c.FusedSummary != nil {
			t.Logf("  FusedSummary:     %q", *c.FusedSummary)
		} else {
			t.Logf("  FusedSummary:     (无)")
		}
		t.Logf("  L4Refs:           %d", len(c.L4Refs))
	}
	t.Logf("  AssociatedContexts: %d", len(result.AssociatedContexts))
}

// RunDream 执行 Dream 并打印各阶段耗时和结果。
// Dream 失败不 Fatal，仅 t.Logf 记录。
func RunDream(t *testing.T, mh *memhop.MemHop) *memhop.DreamReport {
	t.Helper()

	report, err := mh.Dream(context.Background(), nil)
	if err != nil {
		t.Logf("Dream 执行失败: %v", err)
		return nil
	}

	t.Logf("--- Dream 结果 ---")
	t.Logf("  ConsolidatedCount: %d", report.ConsolidatedCount)
	t.Logf("  L1DecayedNodes:    %d", report.L1DecayedNodes)
	t.Logf("  L1PrunedEdges:     %d", report.L1PrunedEdges)
	t.Logf("  L1RemovedNodes:    %d", report.L1RemovedNodes)
	t.Logf("  L1RemovedEdges:    %d", report.L1RemovedEdges)
	t.Logf("  Stages:            %d", len(report.Stages))
	for i, st := range report.Stages {
		t.Logf("    [%d] %s: %s (%d ms)", i, st.Name, st.Status, st.DurationMs)
		if st.Error != "" {
			t.Logf("         Error: %s", st.Error)
		}
	}
	return report
}

// AssertL2Topic 验证 L2 话题存在且关键字段非空。
// 使用 Get(LayerTopic, id) 获取 TopicDetail，断言 ID 非空。
func AssertL2Topic(t *testing.T, mh *memhop.MemHop, topicID string) {
	t.Helper()

	res, err := mh.Get(memhop.LayerTopic, topicID)
	if err != nil {
		t.Fatalf("Get(LayerTopic, %q): %v", topicID, err)
	}
	if res.Topic == nil || res.Topic.ID == "" {
		t.Fatal("Get 返回的 TopicDetail 为空")
	}
}

// AssertL4Archive 验证指定话题的 L4 archive 数量 >= minCount。
// 使用 List(LayerArchive, ArchiveQuery{TopicID: topicID}) 查询。
func AssertL4Archive(t *testing.T, mh *memhop.MemHop, topicID string, minCount int) {
	t.Helper()

	topicIDCopy := topicID
	res, err := mh.List(memhop.LayerArchive, memhop.ListRequest{
		Archive: &memhop.ArchiveQuery{
			TopicID:  &topicIDCopy,
			Page:     1,
			PageSize: 100,
		},
	})
	if err != nil {
		t.Fatalf("List(LayerArchive, TopicID=%q): %v", topicID, err)
	}
	if res.Archives == nil || res.Archives.Total < minCount {
		total := 0
		if res.Archives != nil {
			total = res.Archives.Total
		}
		t.Fatalf("L4 archive 数量 %d < 最小要求 %d", total, minCount)
	}
}

// SnapshotHealth 打印 HealthCheck 各层计数并返回 HealthStatus。
func SnapshotHealth(t *testing.T, mh *memhop.MemHop, label string) *memhop.HealthStatus {
	t.Helper()

	hs, err := mh.HealthCheck()
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}

	t.Logf("--- HealthCheck: %s ---", label)
	t.Logf("  OK:                %v", hs.OK)
	t.Logf("  DBSizeBytes:       %d", hs.DBSizeBytes)
	t.Logf("  EncoderConfigured: %v", hs.EncoderConfigured)
	if hs.LastDreamAt != nil {
		t.Logf("  LastDreamAt:       %s", *hs.LastDreamAt)
	}
	// 按固定顺序打印各层计数
	for _, layer := range []string{"l0_profile", "l1_engram", "l2_topic", "l3_knowledge", "l4_archive", "l5_crystal"} {
		count := hs.LayerCounts[layer]
		if count > 0 {
			t.Logf("  %-18s %d", layer+":", count)
		} else {
			t.Logf("  %-18s 0", layer+":")
		}
	}
	if len(hs.Issues) > 0 {
		for _, iss := range hs.Issues {
			t.Logf("  Issue: %s", iss)
		}
	}

	return hs
}

// EnsureSearchResult 是一个辅助函数，用于确保存在搜索结果并返回首个结果。
// areaID 是可选参数，用于在测试失败时提供更多上下文。
func EnsureSearchResult(t *testing.T, mh *memhop.MemHop, query string, areaID string) *memhop.SearchResult {
	t.Helper()

	result, err := mh.Search(memhop.SearchQuery{Timestamp: time.Now().UnixMilli(), Text: query})
	if err != nil {
		msg := fmt.Sprintf("Search(Text=%q) 失败: %v", query, err)
		if areaID != "" {
			msg = fmt.Sprintf("%s (area=%s)", msg, areaID)
		}
		t.Fatalf("%s", msg)
	}
	if len(result.Contexts) == 0 {
		msg := fmt.Sprintf("Search(Text=%q) 无结果", query)
		if areaID != "" {
			msg = fmt.Sprintf("%s (area=%s)", msg, areaID)
		}
		t.Fatalf("%s", msg)
	}
	return result
}
