// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/common"
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
	for i := 0; i < b.N; i++ {
		ts := base + int64(i)*1000
		res, err := db.Search(sub.SearchQuery{
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
			res, err := db.Search(sub.SearchQuery{
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
	for i := 0; i < b.N; i++ {
		ts := base + int64(1_000_000+i)*1000
		if _, err := db.Search(sub.SearchQuery{
			Text:      query,
			Timestamp: ts,
		}); err != nil {
			b.Fatalf("Search: %v", err)
		}
	}
}

// BenchmarkUpdate measures appending an agent reply to an existing topic.
func BenchmarkUpdate(b *testing.B) {
	fx := loadLocomoSmoke(b)
	db := testsupport.OpenMemHopB(b)
	defer db.Close()

	base := time.Now().UnixMilli()
	res, err := db.Search(sub.SearchQuery{
		Text:       fx.Sessions[0].Turns[0].Text,
		AutoCreate: true,
		Timestamp:  base,
	})
	if err != nil {
		b.Fatalf("seed Search: %v", err)
	}
	topicID := common.FormatHash(res.Contexts[0].ID)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ts := base + int64(i+1)*1000
		if !db.Update(topicID, "agent reply for benchmark", ts) {
			b.Fatal("Update failed")
		}
	}
}
