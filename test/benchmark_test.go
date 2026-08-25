// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// locomoFixture mirrors benches/fixtures/locomo_smoke.json.
type locomoFixture struct {
	Sessions []struct {
		ID    string `json:"id"`
		Turns []struct {
			Text    string `json:"text"`
			Speaker string `json:"speaker"`
		} `json:"turns"`
	} `json:"sessions"`
}

func loadLocomoSmoke(tb testing.TB) *locomoFixture {
	tb.Helper()
	path := filepath.Join("..", "benches", "fixtures", "locomo_smoke.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read fixture: %v", err)
	}
	var fx locomoFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		tb.Fatalf("parse fixture: %v", err)
	}
	if len(fx.Sessions) == 0 {
		tb.Fatal("fixture has no sessions")
	}
	return &fx
}

// BenchmarkSearchAutoCreate measures the cost of the auto-create search route
// (LLM keyword extraction + scene/topic creation + L4 archive write) over the
// locomo smoke fixture. Each iteration ingests one user turn.
func BenchmarkSearchAutoCreate(b *testing.B) {
	fx := loadLocomoSmoke(b)
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	// Flatten all turns into a single ingestion stream.
	var turns []string
	for _, s := range fx.Sessions {
		for _, tn := range s.Turns {
			turns = append(turns, tn.Text)
		}
	}
	if len(turns) == 0 {
		b.Fatal("no turns in fixture")
	}

	base := time.Now().UnixMilli()
	b.ResetTimer()
	i := 0
	for b.Loop() {
		ts := base + int64(i)*1000
		res, err := db.Search(context.Background(), internal.SearchQuery{
			Text:       turns[i%len(turns)],
			AutoCreate: true,
			Timestamp:  ts,
		})
		if err != nil {
			b.Fatalf("Search: %v", err)
		}
		if res.NewTopicID == 0 {
			b.Fatal("expected NewTopicID")
		}
		i++
	}
}

// BenchmarkSearchRetrieve measures the normal retrieval route (LLM keyword
// extraction + three-channel search + topic creation in the hit scene) after
// the fixture has been ingested.
func BenchmarkSearchRetrieve(b *testing.B) {
	fx := loadLocomoSmoke(b)
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	// Seed: ingest every turn once via auto-create.
	base := time.Now().UnixMilli()
	var lastTopicID uint64
	i := 0
	for _, s := range fx.Sessions {
		for _, tn := range s.Turns {
			ts := base + int64(i)*1000
			res, err := db.Search(context.Background(), internal.SearchQuery{
				Text:       tn.Text,
				AutoCreate: true,
				Timestamp:  ts,
			})
			if err != nil {
				b.Fatalf("seed Search: %v", err)
			}
			lastTopicID = res.NewTopicID
			i++
		}
	}
	if lastTopicID == 0 {
		b.Fatal("seed produced no topic")
	}

	query := fx.Sessions[0].Turns[0].Text
	b.ResetTimer()
	i = 0 // reuse seed counter; iteration timestamps restart from 1_000_000
	for b.Loop() {
		ts := base + int64(1_000_000+i)*1000
		if _, err := db.Search(context.Background(), internal.SearchQuery{
			Text:      query,
			Timestamp: ts,
		}); err != nil {
			b.Fatalf("Search: %v", err)
		}
		i++
	}
}

// BenchmarkUpdate measures appending an agent reply to an existing topic.
func BenchmarkUpdate(b *testing.B) {
	fx := loadLocomoSmoke(b)
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	base := time.Now().UnixMilli()
	res, err := db.Search(context.Background(), internal.SearchQuery{
		Text:       fx.Sessions[0].Turns[0].Text,
		AutoCreate: true,
		Timestamp:  base,
	})
	if err != nil {
		b.Fatalf("seed Search: %v", err)
	}
	topicID := common.FormatHash(res.Contexts[0].ID)

	b.ResetTimer()
	i := 0
	for b.Loop() {
		ts := base + int64(i+1)*1000
		if ok, err := db.Update(topicID, "agent reply for benchmark", ts); err != nil || !ok {
			b.Fatalf("Update failed: ok=%v err=%v", ok, err)
		}
		i++
	}
}

// BenchmarkDreamConsolidation measures the Dream pipeline after seeding >20
// related topics in one scene: real compression path with LLM consolidate,
// summary archive, centroid encode, fused topic, child sink and index rebuild.
func BenchmarkDreamConsolidation(b *testing.B) {
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	// 25 same-topic turns trigger real compression.
	related := []string{
		"我喜欢早上六点去公园慢跑", "跑步的时候我习惯听播客", "我每周跑步大概三次，每次五公里",
		"跑完步我会喝一杯蛋白粉", "我早上六点出门跑步", "慢跑时我听健身播客",
		"我一周跑三次步", "每次跑步五公里左右", "运动后我喝蛋白粉补充",
		"清晨六点我在公园跑步", "跑步路上我放播客听", "每周三次是我的跑步频率",
		"五公里是我每次的跑步距离", "跑完我习惯喝杯蛋白粉", "我早上喜欢去公园慢跑",
		"边跑步边听播客是我的习惯", "我每周坚持跑三次", "每次我都跑五公里",
		"跑步后我必喝蛋白粉", "六点起床去公园跑步", "慢跑配播客最舒服",
		"一周三次跑步不间断", "五公里跑完很爽", "蛋白粉是跑后必备", "早上公园跑步我很享受",
	}
	base := time.Now().UnixMilli()
	var sceneID uint64
	for i, text := range related {
		ts := base + int64(i)*1000
		q := internal.SearchQuery{Text: text, Timestamp: ts}
		if i == 0 {
			q.AutoCreate = true
		} else {
			sid := common.FormatHash(sceneID)
			q.DirectedL2ID = &sid
		}
		res, err := db.Search(context.Background(), q)
		if err != nil {
			b.Fatalf("seed Search[%d]: %v", i, err)
		}
		if i == 0 {
			sceneID = res.Contexts[0].SceneID
		}
		if _, err := db.Update(common.FormatHash(res.NewTopicID), "好的", ts+500); err != nil {
			b.Fatalf("seed Update: %v", err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := db.Dream(context.Background(), ""); err != nil {
			b.Fatalf("Dream: %v", err)
		}
	}
}

// BenchmarkRetrievalRecall measures retrieval quality: seed facts on distinct
// topics, then issue cross-phrased queries and measure how often the stored
// fact's distinctive keyword is recalled in the returned context.
func BenchmarkRetrievalRecall(b *testing.B) {
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	// Each anchor: fact text + cross-phrased query + distinctive keyword for the hit check.
	type anchor struct {
		fact, query, marker string
	}
	anchors := []anchor{
		{"我的狗叫旺财，是一只金毛，今年五岁了", "我的狗叫什么名字", "旺财"},
		{"我的猫叫咪咪，最喜欢吃三文鱼罐头", "我的猫爱吃什么", "咪咪"},
		{"我住在北京市朝阳区，住了十年了", "我住在哪个城市", "北京"},
		{"我上个月刚换了工作，现在做后端开发", "我现在做什么工作", "后端"},
	}

	base := time.Now().UnixMilli()
	for i, a := range anchors {
		ts := base + int64(i)*2000
		res, err := db.Search(context.Background(), internal.SearchQuery{Text: a.fact, AutoCreate: true, Timestamp: ts})
		if err != nil {
			b.Fatalf("seed Search[%d]: %v", i, err)
		}
		if _, err := db.Update(common.FormatHash(res.NewTopicID), "好的，记下了。", ts+500); err != nil {
			b.Fatalf("seed Update: %v", err)
		}
	}

	b.ResetTimer()
	hits, total := 0, 0
	i := 0
	for b.Loop() {
		a := anchors[i%len(anchors)]
		ts := base + int64(1_000_000+i)*1000
		res, err := db.Search(context.Background(), internal.SearchQuery{Text: a.query, Timestamp: ts})
		if err != nil {
			b.Fatalf("Search: %v", err)
		}
		total++
		var kws []string
		for j := range res.Contexts {
			kws = append(kws, res.Contexts[j].UserKeywords...)
			kws = append(kws, res.Contexts[j].FusedKeywords...)
		}
		if strings.Contains(strings.ToLower(strings.Join(kws, " ")), strings.ToLower(a.marker)) {
			hits++
		}
		i++
	}
	b.StopTimer()
	if total > 0 {
		b.ReportMetric(float64(hits)/float64(total), "recall")
	}
}

// BenchmarkSearchLatency runs repeated retrieve-route searches and reports the
// per-iteration latency distribution (min/p50/p95/max in ms) so stability is
// visible, not just the mean that testing.B reports.
func BenchmarkSearchLatency(b *testing.B) {
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	// Seed a handful of topics so retrieve has something to hit.
	base := time.Now().UnixMilli()
	seeds := []string{
		"我喜欢早上六点去公园慢跑",
		"我的狗叫旺财，是一只金毛",
		"我住在北京市朝阳区",
		"我现在做后端开发工作",
	}
	for i, s := range seeds {
		ts := base + int64(i)*2000
		res, err := db.Search(context.Background(), internal.SearchQuery{Text: s, AutoCreate: true, Timestamp: ts})
		if err != nil {
			b.Fatalf("seed: %v", err)
		}
		if _, err := db.Update(common.FormatHash(res.NewTopicID), "好的", ts+500); err != nil {
			b.Fatalf("seed Update: %v", err)
		}
	}

	queries := []string{"我的跑步习惯", "我的狗叫什么", "我住在哪", "我做什么工作"}
	lat := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	i := 0
	for b.Loop() {
		q := queries[i%len(queries)]
		ts := base + int64(1_000_000+i)*1000
		start := time.Now()
		if _, err := db.Search(context.Background(), internal.SearchQuery{Text: q, Timestamp: ts}); err != nil {
			b.Fatalf("Search: %v", err)
		}
		lat = append(lat, time.Since(start))
		i++
	}
	b.StopTimer()
	reportLatency(b, lat)
}

// reportLatency sorts observed durations and reports min/p50/p95/max in ms.
func reportLatency(b *testing.B, lat []time.Duration) {
	b.Helper()
	if len(lat) == 0 {
		return
	}
	sorted := make([]time.Duration, len(lat))
	copy(sorted, lat)
	slices.Sort(sorted)
	pct := func(p float64) time.Duration {
		idx := int(p * float64(len(sorted)-1))
		return sorted[idx]
	}
	b.ReportMetric(float64(sorted[0].Milliseconds()), "min_ms")
	b.ReportMetric(float64(pct(0.50).Milliseconds()), "p50_ms")
	b.ReportMetric(float64(pct(0.95).Milliseconds()), "p95_ms")
	b.ReportMetric(float64(sorted[len(sorted)-1].Milliseconds()), "max_ms")
}
