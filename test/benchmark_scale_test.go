// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline scale measurements over settled host turns. Seeding, checkpointing,
// verification and cleanup stay outside the three timed operations.
//
// Run: go test ./test/ -run '^$' -bench '^BenchmarkScale$' -benchtime=1x -count=1 -v

package test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// BenchmarkScale measures one reopen, one full Dream pass and one compacted copy
// of the same non-empty corpus at each scale. Custom metrics keep the operations
// separate; a single ns/op number would hide which part grew with the corpus.
func BenchmarkScale(b *testing.B) {
	for _, rounds := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("rounds=%d", rounds), func(b *testing.B) {
			b.StopTimer()
			url := newMockLLM(b).srv.URL
			e, initial, seeded := seedScaleCorpus(b, url, rounds)
			path, llm := e.path, e.llm
			if err := e.db.Close(); err != nil {
				b.Fatalf("close seeded corpus: %v", err)
			}

			defaults := memhop.DefaultMemHopDefaults
			noAutoDream(&defaults)
			start := time.Now()
			b.StartTimer()
			reopened, err := memhop.Open(path, llm, defaults,
				&memhop.ProfileInput{Name: "test-primary", Role: "offline fixture"})
			b.StopTimer()
			reopenDuration := time.Since(start)
			if err != nil {
				b.Fatalf("reopen %d rounds: %v", rounds, err)
			}
			b.Cleanup(func() {
				if !reopened.IsClosed() {
					_ = reopened.Close()
				}
			})
			sess, err := reopened.Primary()
			if err != nil {
				b.Fatalf("Primary after reopen: %v", err)
			}
			corpus, err := reopened.Stats()
			if err != nil {
				b.Fatalf("corpus Stats: %v", err)
			}

			start = time.Now()
			b.StartTimer()
			report, err := sess.Dream(context.Background(), "")
			b.StopTimer()
			dreamDuration := time.Since(start)
			if err != nil {
				b.Fatalf("Dream %d rounds: %v", rounds, err)
			}
			if report.L2TopicsCompressed == 0 || len(report.Stages) != len(dreamStages) {
				b.Fatalf("Dream did not exercise the full corpus: %+v", report)
			}
			afterDream, err := reopened.Stats()
			if err != nil {
				b.Fatalf("post-Dream Stats: %v", err)
			}

			compactPath := filepath.Join(b.TempDir(), "compacted.meh")
			start = time.Now()
			b.StartTimer()
			err = reopened.CompactTo(compactPath)
			b.StopTimer()
			compactDuration := time.Since(start)
			if err != nil {
				b.Fatalf("CompactTo %d rounds: %v", rounds, err)
			}
			compacted := openMockDB(b, compactPath, url, noAutoDream)
			compactStats, err := compacted.Stats()
			if err != nil {
				b.Fatalf("compacted Stats: %v", err)
			}
			if compactStats.RecordCount != afterDream.RecordCount {
				b.Fatalf("compaction changed live records: got=%d want=%d",
					compactStats.RecordCount, afterDream.RecordCount)
			}

			b.ReportMetric(float64(rounds), "corpus_rounds")
			b.ReportMetric(float64(corpus.RecordCount), "corpus_records")
			b.ReportMetric(float64(corpus.FileBytes), "corpus_file_bytes")
			b.ReportMetric(float64(seeded.FileBytes-initial.FileBytes)/float64(rounds), "growth_B/round")
			b.ReportMetric(float64(reopenDuration)/float64(time.Millisecond), "reopen_ms")
			b.ReportMetric(float64(dreamDuration)/float64(time.Millisecond), "dream_ms")
			b.ReportMetric(float64(afterDream.FileBytes), "post_dream_file_bytes")
			b.ReportMetric(float64(compactDuration)/float64(time.Millisecond), "compact_ms")
			b.ReportMetric(float64(compactStats.RecordCount), "compact_records")
			b.ReportMetric(float64(compactStats.FileBytes), "compact_file_bytes")
			b.Logf("corpus rounds=%d records=%d file_bytes=%d growth_bytes=%d; post-Dream records=%d file_bytes=%d; compact records=%d file_bytes=%d",
				rounds, corpus.RecordCount, corpus.FileBytes, seeded.FileBytes-initial.FileBytes,
				afterDream.RecordCount, afterDream.FileBytes, compactStats.RecordCount, compactStats.FileBytes)
			for _, stage := range report.Stages {
				b.Logf("Dream stage=%s status=%s duration_ms=%d", stage.Name, stage.Status, stage.DurationMs)
			}
			b.StartTimer()
		})
	}
}

// seedScaleCorpus settles ten scenes with an equal number of rounds in each. Its
// record and byte counts are measured, not inferred from the requested rounds.
func seedScaleCorpus(b *testing.B, url string, rounds int) (*engineBench, memhop.DBStats, memhop.DBStats) {
	b.Helper()
	const scenes = 10
	if rounds%scenes != 0 {
		b.Fatalf("rounds=%d cannot fill %d scenes evenly", rounds, scenes)
	}
	path := filepath.Join(b.TempDir(), "scale.meh")
	db := openMockDB(b, path, url, noAutoDream)
	initial, err := db.Stats()
	if err != nil {
		b.Fatalf("initial Stats: %v", err)
	}
	sess, err := db.Primary()
	if err != nil {
		b.Fatalf("Primary: %v", err)
	}
	e := &engineBench{db: db, sess: sess, path: path, llm: testLLM(url),
		stamp: time.Now().Add(-time.Duration(rounds+1) * time.Second).UnixMilli()}
	for scene := 0; scene < scenes; scene++ {
		for turn := 0; turn < rounds/scenes; turn++ {
			e.round(b, turn == 0)
		}
	}
	listed, err := sess.ListScenes("")
	if err != nil {
		b.Fatalf("corpus scenes: %v", err)
	}
	seeded, err := db.Stats()
	if err != nil {
		b.Fatalf("seeded Stats: %v", err)
	}
	if len(listed) != scenes || seeded.RecordCount < int64(rounds) || seeded.FileBytes <= initial.FileBytes {
		b.Fatalf("empty or malformed scale corpus: scenes=%d records=%d initial_bytes=%d seeded_bytes=%d",
			len(listed), seeded.RecordCount, initial.FileBytes, seeded.FileBytes)
	}
	return e, initial, seeded
}
