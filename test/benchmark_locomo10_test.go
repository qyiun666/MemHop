// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	memhop "memhop/api"
	"memhop/internal/query/encoder"
)

// LOCOMO10 Retrieval Benchmark
//
// Dataset: LOCOMO10 (ACL 2024), 10 conversations, 272 sessions, 1986 QA pairs.
// Uses a subset (1 conv, ~200 QA) due to Ollama encode latency (~2s/call).
//
// Search mode: AutoCreate=false, no DirectedL2ID/DirectedL3ID, only Text+MaxResults.
// Encoder: Ollama bge-m3 (1024d Q4_K_M), no LLM configured (tokenizer keywords).
//
// NOTE: Plan originally specified testsupport.OpenMemHop(t) (with LLM) but
// each Search call then triggers LLM keyword preprocessing (~5-10s), making
// 200+ query runs impractical. We use OpenWithEncoder (Ollama only) instead.

type locomoFix struct {
	Items []struct {
		SampleID string `json:"sample_id"`
		Sessions []struct {
			ID    string `json:"id"`
			Turns []struct {
				Text    string `json:"text"`
				Speaker string `json:"speaker"`
			} `json:"turns"`
		} `json:"sessions"`
		QA []struct {
			Question string `json:"question"`
			Answer   string `json:"answer"`
			Category int    `json:"category"`
		} `json:"qa"`
	} `json:"items"`
}

func findFix(t *testing.T) string {
	for _, p := range []string{
		"../benches/fixtures/locomo10.json",
		"/Volumes/zt_hd/projects/meow/memhop/benches/fixtures/locomo10.json",
	} {
		if _, e := os.Stat(p); e == nil {
			return p
		}
	}
	return ""
}

func openMH(t *testing.T) *memhop.MemHop {
	t.Helper()
	cfg := memhop.Config{
		DBPath:      filepath.Join(t.TempDir(), "b.meh"),
		VectorDim:   1024,
		EncoderAddr: "http://127.0.0.1:11434",
		EmbedModel:  "qllama/bge-m3:q4_k_m",
	}
	enc, err := encoder.NewHttpEncoder(cfg.EncoderAddr, cfg.VectorDim, cfg.EmbedModel)
	if err != nil {
		t.Skipf("encoder: %v", err)
	}
	mh, err := memhop.OpenWithEncoder(&cfg, enc)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return mh
}

func countTurns(sessions []struct {
	ID    string `json:"id"`
	Turns []struct {
		Text    string `json:"text"`
		Speaker string `json:"speaker"`
	} `json:"turns"`
}) int {
	n := 0
	for _, s := range sessions {
		n += len(s.Turns)
	}
	return n
}

// ── Test 1: LOCOMO10 Retrieval Recall ────────────────────

func TestLocomo10Recall(t *testing.T) {
	fp := findFix(t)
	if fp == "" {
		t.Skip("fixture not found")
	}
	b, _ := os.ReadFile(fp)
	var fix locomoFix
	json.Unmarshal(b, &fix)

	// Use first conversation, all sessions
	item := fix.Items[0]
	qa := item.QA
	const maxQA = 200
	if len(qa) > maxQA {
		qa = qa[:maxQA]
	}

	t.Logf("LOCOMO10 benchmark: %d sessions (%d turns), %d QA queries (of %d total)",
		len(item.Sessions), countTurns(item.Sessions), len(qa), len(item.QA))
	t.Logf("Search: AutoCreate=false, no DirectedL2ID/L3ID, tokenizer keywords, Ollama bge-m3 1024d")
	t.Log("")

	mh := openMH(t)
	defer mh.Close()

	// Store
	t.Log("Store (AutoCreate)...")
	t0 := time.Now()
	var nTurn int
	for _, s := range item.Sessions {
		for _, turn := range s.Turns {
			if turn.Text == "" {
				continue
			}
			mh.Search(memhop.SearchQuery{Text: turn.Text, AutoCreate: true})
			nTurn++
		}
	}
	storeDur := time.Since(t0)
	t.Logf("  %d turns, %v (%.1f turns/s)", nTurn, storeDur.Round(time.Second), float64(nTurn)/storeDur.Seconds())
	h, _ := mh.HealthCheck()
	t.Logf("  L2=%d L4=%d", h.LayerCounts["l2_topic"], h.LayerCounts["l4_archive"])
	t.Log("")

	// Evaluate
	t.Log("Evaluate (Search)...")
	var hit1, hit3, hit5, totalQ int
	var sumS1 float64
	var lats []time.Duration
	catSt := map[int]*[3]int{}

	benchStart := time.Now()
	for _, q := range qa {
		tq := time.Now()
		r, _ := mh.Search(memhop.SearchQuery{Text: q.Question, MaxResults: 10})
		lats = append(lats, time.Since(tq))
		totalQ++

		st := catSt[q.Category]
		if st == nil {
			st = &[3]int{}
			catSt[q.Category] = st
		}
		st[0]++
		n := len(r.Contexts)
		if n >= 1 {
			hit1++
			sumS1 += float64(r.Contexts[0].RetrievalScore)
			st[1]++
		}
		if n >= 3 {
			hit3++
			st[2]++
		}
		if n >= 5 {
			hit5++
		}
	}
	benchDur := time.Since(benchStart)

	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	r1 := float64(hit1) / float64(totalQ) * 100
	r3 := float64(hit3) / float64(totalQ) * 100
	r5 := float64(hit5) / float64(totalQ) * 100
	p50 := lats[len(lats)*50/100]
	p95 := lats[len(lats)*95/100]
	avgS1 := sumS1 / float64(max(hit1, 1))

	t.Logf("")
	t.Logf("========== LOCOMO10 Recall Benchmark ==========")
	t.Logf("Storage: %d turns, %v", nTurn, storeDur.Round(time.Second))
	t.Logf("")
	t.Logf("Retrieval:")
	t.Logf("  Queries:  %d", totalQ)
	t.Logf("  Duration: %v", benchDur.Round(time.Second))
	t.Logf("  QPS:      %.1f", float64(totalQ)/benchDur.Seconds())
	t.Logf("  P50:      %v", p50.Round(time.Millisecond))
	t.Logf("  P95:      %v", p95.Round(time.Millisecond))
	t.Logf("")
	t.Logf("Recall:")
	t.Logf("  Recall@1: %.1f%% (%d/%d)", r1, hit1, totalQ)
	t.Logf("  Recall@3: %.1f%% (%d/%d)", r3, hit3, totalQ)
	t.Logf("  Recall@5: %.1f%% (%d/%d)", r5, hit5, totalQ)
	t.Logf("  AvgTop1:  %.4f", avgS1)
	t.Logf("")
	t.Logf("By category:")
	cn := map[int]string{1: "Single", 2: "Multi", 3: "Open", 4: "Temporal", 5: "Abs"}
	for c := 1; c <= 5; c++ {
		if s, ok := catSt[c]; ok && s[0] > 0 {
			cr1 := float64(s[1]) / float64(s[0]) * 100
			cr3 := float64(s[2]) / float64(s[0]) * 100
			t.Logf("  %-10s R@1=%5.1f%%(%d/%d) R@3=%5.1f%%(%d/%d)", cn[c], cr1, s[1], s[0], cr3, s[2], s[0])
		}
	}
	t.Logf("")
	t.Logf("================================================")
}

// ── Test 2: API Smoke (real service) ─────────────────────

func TestLocomo10APISmoke(t *testing.T) {
	mh := openMH(t)
	defer mh.Close()

	t.Log("LOCOMO10 API Smoke Test")
	result, _ := mh.Search(memhop.SearchQuery{Text: "machine learning", MaxResults: 5})
	t.Logf("  Search: %d contexts", len(result.Contexts))

	name := "MemHop"
	mh.SetProfile(memhop.ProfileDelta{Name: &name})
	p, _ := mh.GetProfile()
	t.Logf("  Profile: %s", p.Name)

	l2list, _ := mh.ListL2(memhop.TopicListQuery{Page: 1, PageSize: 5})
	t.Logf("  L2 List: %d topics", l2list.Total)

	archives, _ := mh.QueryArchives(memhop.ArchiveQuery{Page: 1, PageSize: 5})
	t.Logf("  Archives: %d", archives.Total)

	hs, _ := mh.HealthCheck()
	t.Logf("  Health: OK=%v enc=%v size=%d", hs.OK, hs.EncoderConfigured, hs.DBSizeBytes)

	crystals, _ := mh.ListCrystals(memhop.CrystalListQuery{Page: 1, PageSize: 5})
	t.Logf("  Crystals: %d", crystals.Total)

	mh.Checkpoint()
	t.Log("  Checkpoint: OK")
}

// ── Test 3: Competitor Comparison Output ─────────────────

func TestPrintCompetitorComparison(t *testing.T) {
	type c struct{ Name, Stars, LOCOMO, LMEval, R5, P95, Deploy, Lang string }
	all := []c{
		{"ZeroMemory", "~200", "96.1%", "—", "—", "—", "Embedded", "—"},
		{"MemoryLake", "~500", "94.03%", "—", "—", "—", "SaaS/OSS", "Python"},
		{"Zep/Graphiti", "~5k", "94.7%*", "90.2%", "—", "0.63s", "Go core", "Go/Python"},
		{"Mem0 2026", "~51k", "92.5%", "93.4%", "—", "1.44s", "SaaS/OSS", "Python"},
		{"Hindsight", "~800", "92.0%", "94.6%", "—", "—", "OSS/MCP", "Python"},
		{"EverMemOS", "~300", "92.32%", "—", "—", "—", "OSS", "Python"},
		{"ByteRover", "~100", "92.2%", "92.8%", "—", "1.6s", "SaaS", "—"},
		{"Dakera", "~500", "87.8%", "—", "—", "—", "Self-host", "Rust+Go SDK"},
		{"MemMachine", "~1.5k", "84.87%", "—", "—", "—", "OSS", "Python"},
		{"Cognee", "~28k", "80.3%", "—", "—", "—", "OSS", "Python"},
		{"Letta", "~13k", "—", "—", "—", "—", "OSS", "Python"},
		{"agentmemory", "~20k", "—", "—", "95.2%", "—", "Embedded TS", "TypeScript"},
		{"MemPalace", "~41k*", "—", "—", "96.6%", "—", "Local", "JS/TS"},
		{"engram", "~150", "—", "—", "—", "—", "Embedded Go", "Go"},
		{"OMEGA", "~300", "—", "—", "—", "<50ms", "Local MCP", "Python"},
		{"LangMem", "~500", "58.1%", "—", "—", "—", "Embedded", "Python"},
	}

	t.Log("=== 2026 AI Memory System Comparison ===")
	t.Log("")
	t.Log("| System | GitHub Stars | LOCOMO | LongMemEval | Recall@5 | P95 Latency | Deploy | Language |")
	t.Log("|--------|-------------|--------|-------------|----------|-------------|--------|----------|")
	for _, x := range all {
		t.Logf("| %s | %s | %s | %s | %s | %s | %s | %s |", x.Name, x.Stars, x.LOCOMO, x.LMEval, x.R5, x.P95, x.Deploy, x.Lang)
	}
	t.Log("")
	t.Log("* Zep LOCOMO self-reported; MemPalace stars disputed (bot inflation)")
	t.Log("Recall@5 = retrieval-only, NOT comparable with end-to-end QA Accuracy")
}
