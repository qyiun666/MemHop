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
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
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

// benchTurn settles one finished turn in the host's session.
func benchTurn(tb testing.TB, db *testsupport.Handle, sceneID, user, agent string, ts int64) {
	tb.Helper()
	if _, err := db.Update(memhop.TurnUpdate{
		SceneID: sceneID, UserText: user, UserTS: ts, AgentText: agent, AgentTS: ts + 500,
	}); err != nil {
		tb.Fatalf("Update: %v", err)
	}
}

// benchSession opens a host session (scene) and returns its id.
func benchSession(tb testing.TB, db *testsupport.Handle, name string) string {
	tb.Helper()
	res, err := db.Search(memhop.SearchQuery{SceneName: name})
	if err != nil {
		tb.Fatalf("Search: %v", err)
	}
	return res.Scene.SceneID
}

// BenchmarkUpdateTurn measures the hot write path: one finished turn costs a
// single LLM distillation plus a topic, two L4 archives and the cache sync.
func BenchmarkUpdateTurn(b *testing.B) {
	fx := loadLocomoSmoke(b)
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	var turns []string
	for _, s := range fx.Sessions {
		for _, tn := range s.Turns {
			turns = append(turns, tn.Text)
		}
	}
	if len(turns) == 0 {
		b.Fatal("no turns in fixture")
	}
	sceneID := benchSession(b, db, "locomo ingest")

	base := time.Now().UnixMilli()
	i := 0
	for b.Loop() {
		benchTurn(b, db, sceneID, turns[i%len(turns)], "好的，记下了。", base+int64(i)*1000)
		i++
	}
}

// BenchmarkSceneRead measures the session read: a cache-only lookup of the
// scene's depth-1 surface, with no LLM call and no embedding.
func BenchmarkSceneRead(b *testing.B) {
	fx := loadLocomoSmoke(b)
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	base := time.Now().UnixMilli()
	sceneID := benchSession(b, db, "locomo seed")
	i := 0
	for _, s := range fx.Sessions {
		for _, tn := range s.Turns {
			benchTurn(b, db, sceneID, tn.Text, "好的", base+int64(i)*1000)
			i++
		}
	}
	if i == 0 {
		b.Fatal("seed produced no turns")
	}

	b.ResetTimer()
	for b.Loop() {
		res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
		if err != nil {
			b.Fatalf("Search: %v", err)
		}
		if len(res.Topics) == 0 {
			b.Fatal("session surface came back empty")
		}
	}
}

// BenchmarkAppendL4 measures the storage-only append path (no distillation)
// hosts use for a turn's intermediate messages.
func BenchmarkAppendL4(b *testing.B) {
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	base := time.Now().UnixMilli()
	sceneID := benchSession(b, db, "append bench")
	topicID, err := db.Update(memhop.TurnUpdate{
		SceneID: sceneID, UserText: "先看看日志", UserTS: base, AgentText: "已看完", AgentTS: base + 500,
	})
	if err != nil {
		b.Fatalf("seed Update: %v", err)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		ts := base + int64(i+1)*1000
		if _, err := db.AppendL4Message(topicID, "agent reply for benchmark", ts, 1, 0); err != nil {
			b.Fatalf("AppendL4Message: %v", err)
		}
		i++
	}
}

// BenchmarkDreamConsolidation measures the Dream pipeline after seeding >20
// related turns in one session: real compression with LLM consolidate,
// summary archive, fused topic, child sink, L1 rebuild and cache rebuild.
func BenchmarkDreamConsolidation(b *testing.B) {
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

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
	sceneID := benchSession(b, db, "running habits")
	for i, text := range related {
		benchTurn(b, db, sceneID, text, "好的", base+int64(i)*1000)
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := db.Dream(context.Background(), sceneID); err != nil {
			b.Fatalf("Dream: %v", err)
		}
	}
}

// BenchmarkMemoryLoop measures the real host memory loop: Update-only turns
// in one session with the automatic Dream the engine schedules once the
// session surface passes the threshold, plus periodic L0/L2 verification.
func BenchmarkMemoryLoop(b *testing.B) {
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	// Same-topic turns: once the session surface exceeds
	// SceneDreamTopicThreshold(24), Update schedules a Dream on its own.
	related := []string{
		"我喜欢早上六点去公园慢跑", "跑步的时候我习惯听播客", "我每周跑步大概三次，每次五公里",
		"跑完步我会喝一杯蛋白粉", "我早上六点出门跑步", "慢跑时我听健身播客",
		"我一周跑三次步", "每次跑步五公里左右", "运动后我喝蛋白粉补充",
		"清晨六点我在公园跑步", "跑步路上我放播客听", "每周三次是我的跑步频率",
		"五公里是我每次的跑步距离", "跑完我习惯喝杯蛋白粉", "我早上喜欢去公园慢跑",
		"边跑步边听播客是我的习惯", "我每周坚持跑三次", "每次我都跑五公里",
		"跑步后我必喝蛋白粉", "六点起床去公园跑步", "慢跑配播客最舒服",
		"一周三次跑步不间断", "五公里跑完很爽", "蛋白粉是跑后必备", "早上公园跑步我很享受",
		"我跑步时会戴耳机听音乐", "跑步前我会热身十分钟", "我穿红色的跑鞋",
		"周末我在河边跑步", "傍晚跑步人少风景好", "跑步后我会拉伸",
		"我计划参加下个月的马拉松", "马拉松报名费三百元", "我每天跑步打卡",
		"跑步让我精神很好", "我买了新运动水壶",
	}
	base := time.Now().UnixMilli()
	sceneID := benchSession(b, db, "loop session")
	var dreams, checks int
	prevDepth1 := -1
	turns := 0
	for b.Loop() {
		// Cycle the material by index so the benchmark measures steady state:
		// the session keeps growing past the threshold and the scheduled Dream
		// keeps compressing it, as in real use.
		text := related[turns%len(related)]
		ts := base + int64(turns)*1000
		benchTurn(b, db, sceneID, text, "好的，记下了。", ts)
		turns++
		// Periodic L0/L2 verification: profile readable, session intact.
		if turns%10 == 0 {
			if _, err := db.GetL0(); err != nil {
				b.Fatalf("GetL0 after %d turns: %v", turns, err)
			}
			res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
			if err != nil || len(res.Topics) == 0 {
				b.Fatalf("session read after %d turns: topics=%d err=%v", turns, len(res.Topics), err)
			}
			checks++
		}
		// Count auto-consolidations by watching the depth-1 surface shrink.
		res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
		if err == nil {
			if prevDepth1 >= 0 && len(res.Topics) < prevDepth1 {
				dreams++
			}
			prevDepth1 = len(res.Topics)
		}
	}
	b.ReportMetric(float64(dreams), "auto_consolidations")
	b.ReportMetric(float64(checks), "l0l2_checks")
}

// BenchmarkSceneReadLatency reports the session-read latency distribution
// (min/p50/p95/max in ms) so stability is visible, not just the mean.
func BenchmarkSceneReadLatency(b *testing.B) {
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	base := time.Now().UnixMilli()
	seeds := []string{
		"我喜欢早上六点去公园慢跑",
		"我的狗叫旺财，是一只金毛",
		"我住在北京市朝阳区",
		"我现在做后端开发工作",
	}
	scenes := make([]string, 0, len(seeds))
	for i, s := range seeds {
		ts := base + int64(i)*2000
		sceneID := benchSession(b, db, "latency "+s)
		benchTurn(b, db, sceneID, s, "好的", ts)
		scenes = append(scenes, sceneID)
	}

	lat := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	i := 0
	for b.Loop() {
		start := time.Now()
		if _, err := db.Search(memhop.SearchQuery{SceneID: scenes[i%len(scenes)]}); err != nil {
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
