// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Engine benchmarks measured against the offline stub endpoint: no key, no quota, no
// network, so they answer "what does the engine itself cost" for a host deciding whether
// one more agent means one more file. The two call points that do ask a model — a turn's
// keyword distillation inside Update, a merge proposal inside Dream — are answered by the
// stub, so what they carry here is one localhost round-trip plus the engine work, not a
// provider's latency. BenchmarkUpdateTurn and friends in benchmark_test.go are the ones
// that report a real round trip.
//
// Run: go test ./test/ -bench BenchmarkEngine -benchtime=20x -count=1

package test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// noAutoDream keeps consolidation host-driven: a background pass scheduled by a settled
// round would move the read surface underneath a measurement that is trying to read it.
func noAutoDream(d *memhop.MemHopDefaults) { d.SceneDreamTopicThreshold = -1 }

// engineBench is a seeded corpus plus the handle it was written through.
type engineBench struct {
	db    *memhop.DB
	sess  *memhop.Session
	path  string
	llm   memhop.LlmConfig
	stamp int64
}

// round runs one host turn the way a host runs it: the read that opens the turn, what the
// turn saw and did, then the one call that closes it. Every write carries the same
// millisecond, which is what makes the turn's outcome word age together with its prose.
func (e *engineBench) round(b testing.TB, newScene bool) {
	b.Helper()
	e.stamp += 1000
	if _, err := e.sess.Search(memhop.SearchQuery{NewScene: newScene}); err != nil {
		b.Fatalf("Search: %v", err)
	}
	for _, in := range []memhop.ArchiveInput{
		{Kind: memhop.KindUtterance, ContentType: memhop.ContentText, Role: 1,
			CreatedAt: e.stamp, Content: "the user asks about the retry policy"},
		{Kind: memhop.KindEvent, ContentType: memhop.ContentText, EventType: "tool_call",
			CreatedAt: e.stamp, Content: "read config/retry.yaml"},
	} {
		if _, err := e.sess.AppendArchive(in); err != nil {
			b.Fatalf("AppendArchive: %v", err)
		}
	}
	if _, err := e.sess.Update(memhop.TurnEnd{
		Input: "retry policy?", Output: "exponential, capped at five attempts",
		Outcome: "answered", CreatedAt: e.stamp,
	}); err != nil {
		b.Fatalf("Update: %v", err)
	}
}

// seedEngineBench opens a file and settles scenes*turns rounds into it: one scene per group
// of `turns`, each opened by the round that asks for it (`NewScene` — an unnamed read
// continues the session the domain is on, so asking for scenes is what makes them). Turn
// text is short and fixed-width on purpose: these numbers are about rounds and bytes, not
// about how much prose a round happens to carry.
func seedEngineBench(b testing.TB, url string, scenes, turns int) *engineBench {
	b.Helper()
	return seedEngineBenchAt(b, filepath.Join(b.TempDir(), "engine.meh"), url, scenes, turns)
}

// seedEngineBenchAt is the same corpus at a path the caller keeps, for a probe that has to
// open the file from another process.
func seedEngineBenchAt(b testing.TB, path, url string, scenes, turns int) *engineBench {
	b.Helper()
	db := openMockDB(b, path, url, noAutoDream)
	sess, err := db.Primary()
	if err != nil {
		b.Fatalf("Primary: %v", err)
	}
	e := &engineBench{db: db, sess: sess, path: path, llm: testLLM(url),
		stamp: time.Now().Add(-time.Hour).UnixMilli()}
	for s := 0; s < scenes; s++ {
		for t := 0; t < turns; t++ {
			e.round(b, t == 0)
		}
	}
	// The shape every number below is labelled with. Printed, not assumed: the first run of
	// these benches claimed three scenes and settled 120 rounds into one, because an unnamed
	// Search continues the session the domain is already on rather than opening a new one.
	sc, err := e.sess.SceneContext("")
	if err != nil {
		b.Fatalf("corpus shape: %v", err)
	}
	listed, err := e.sess.ListScenes("")
	if err != nil {
		b.Fatalf("corpus scenes: %v", err)
	}
	b.Logf("corpus: %d scenes, %d rows on the scene a read of this domain addresses",
		len(listed), len(sc.Topics))
	return e
}

// BenchmarkEngineRecall is the read a decision loop makes before asking the model, and the
// one it can make as many times as it likes: a pure scene read of a domain that already has
// an open turn. It writes nothing, so the number is the read surface's cost — the topics a
// settled round leaves behind plus their keyword tracks.
func BenchmarkEngineRecall(b *testing.B) {
	url := newMockLLM(b).srv.URL
	e := seedEngineBench(b, url, 3, 40)
	defer e.db.Close()
	if _, err := e.sess.Search(memhop.SearchQuery{}); err != nil {
		b.Fatalf("open a turn: %v", err)
	}

	b.ResetTimer()
	for b.Loop() {
		res, err := e.sess.SceneContext("")
		if err != nil {
			b.Fatalf("SceneContext: %v", err)
		}
		if len(res.Topics) == 0 {
			b.Fatal("the scene came back with no topics")
		}
	}
}

// BenchmarkEngineOpenTurn measures the read that also opens a turn — the write on the read
// path, and the reason the library can settle a round the host never names.
func BenchmarkEngineOpenTurn(b *testing.B) {
	url := newMockLLM(b).srv.URL
	e := seedEngineBench(b, url, 3, 40)
	defer e.db.Close()

	b.ResetTimer()
	for b.Loop() {
		if _, err := e.sess.Search(memhop.SearchQuery{}); err != nil {
			b.Fatalf("Search: %v", err)
		}
	}
}

// BenchmarkEngineAppend measures one mid-round record against a turn that is already open:
// the call a host makes once per thing it sees or does, which is why its budget check is a
// refusal rather than a truncation.
func BenchmarkEngineAppend(b *testing.B) {
	url := newMockLLM(b).srv.URL
	e := seedEngineBench(b, url, 3, 40)
	defer e.db.Close()
	if _, err := e.sess.Search(memhop.SearchQuery{}); err != nil {
		b.Fatalf("open a turn: %v", err)
	}
	in := memhop.ArchiveInput{Kind: memhop.KindEvent, ContentType: memhop.ContentText,
		EventType: "tool_call", CreatedAt: e.stamp + 1, Content: "read config/retry.yaml"}

	b.ResetTimer()
	for b.Loop() {
		in.CreatedAt++
		if _, err := e.sess.AppendArchive(in); err != nil {
			b.Fatalf("AppendArchive: %v", err)
		}
	}
}

// BenchmarkEngineRound is the whole loop iteration a host pays per turn, and the number
// that decides how fast one .meh grows: bytes per round is reported alongside the timing,
// because for an append-only single file the growth rate is the operation to measure.
func BenchmarkEngineRound(b *testing.B) {
	url := newMockLLM(b).srv.URL
	e := seedEngineBench(b, url, 1, 20)
	defer e.db.Close()
	before, err := e.db.Stats()
	if err != nil {
		b.Fatalf("Stats: %v", err)
	}

	rounds := 0
	b.ResetTimer()
	for b.Loop() {
		e.round(b, false)
		rounds++
	}
	after, err := e.db.Stats()
	if err != nil {
		b.Fatalf("Stats: %v", err)
	}
	b.ReportMetric(float64(after.FileBytes-before.FileBytes)/float64(rounds), "B/round")
	b.ReportMetric(float64(after.RecordCount-before.RecordCount)/float64(rounds), "rec/round")
}

// benchReopen seeds a corpus and then times one Open of it, which is where every domain's
// indexes are rebuilt from its records. The timer is stopped before any of the setup exists,
// so a seeding second can never be charged to an iteration: the first run of this shape did
// exactly that, and reported an open costing 400× what it costs.
//
// A classic loop, because the close that precedes each open is also setup and b.Loop refuses
// to be entered with the timer stopped.
func benchReopen(b *testing.B, url string, scenes, turns int) {
	b.StopTimer()
	e := seedEngineBench(b, url, scenes, turns)
	stats, err := e.db.Stats()
	if err != nil {
		b.Fatalf("Stats: %v", err)
	}
	if stats.RecordCount < int64(scenes*turns) {
		b.Fatalf("the corpus holds %d records for %d rounds: this would time an almost empty file",
			stats.RecordCount, scenes*turns)
	}
	path, llm, reopened := e.path, e.llm, e.db
	b.Cleanup(func() {
		if !reopened.IsClosed() {
			_ = reopened.Close()
		}
	})
	for i := 0; i < b.N; i++ {
		if !reopened.IsClosed() {
			_ = reopened.Close()
		}
		b.StartTimer()
		db, err := memhop.Open(path, llm, memhop.DefaultMemHopDefaults,
			&memhop.ProfileInput{Name: "test-primary", Role: "offline fixture"})
		b.StopTimer()
		if err != nil {
			b.Fatalf("reopen: %v", err)
		}
		reopened = db
	}
	b.StartTimer()
}

// BenchmarkEngineReopen is the cost a host pays before it can read anything: opening a file
// rebuilds every domain's indexes from its records. This is the number behind "a worker
// brings its own library", and it carries no LLM call at all.
func BenchmarkEngineReopen(b *testing.B) {
	benchReopen(b, newMockLLM(b).srv.URL, 3, 40)
}

// BenchmarkEngineReopenFreshFile separates that scan from the fixed part of an open — the
// file lock, the two headers, the snapshot load — by measuring the same call against a file
// holding nothing but its primary profile. The gap between the two is what a growing corpus
// costs a restart, and it is the number a host that forks one library per worker reads as
// start-up latency.
func BenchmarkEngineReopenFreshFile(b *testing.B) {
	benchReopen(b, newMockLLM(b).srv.URL, 0, 0)
}

// BenchmarkEngineDreamPass measures one host-driven consolidation pass over a fixed-size
// surface: the pass folds a group, so the corpus is rebuilt untimed between iterations and
// every iteration measures the same starting shape.
func BenchmarkEngineDreamPass(b *testing.B) {
	url := newMockLLM(b).srv.URL
	var e *engineBench
	b.ResetTimer()
	// As above: rebuilding the corpus between iterations is setup, and a consolidation
	// pass that folded a group would otherwise leave the next one measuring a surface
	// smaller than the last.
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		if e != nil {
			_ = e.db.Close()
		}
		e = seedEngineBench(b, url, 1, 26)
		b.StartTimer()
		rep, err := e.sess.Dream(context.Background(), "")
		b.StopTimer()
		if err != nil {
			b.Fatalf("Dream: %v", err)
		}
		if rep.L2TopicsCompressed == 0 {
			b.Fatal("the pass compressed nothing")
		}
	}
	b.StartTimer()
}
