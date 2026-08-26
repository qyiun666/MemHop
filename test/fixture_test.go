// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// locomoItem mirrors benches/fixtures/locomo10.json. The fixture is used only
// as realistic conversation material for the Search/Update ingestion loop —
// no QA evaluation, no LLM judge (the engine's own L0/L1/L4 correctness is
// asserted directly, see core_cycle_test.go).
type locomoItem struct {
	SampleID string          `json:"sample_id"`
	SpeakerA string          `json:"speaker_a"`
	SpeakerB string          `json:"speaker_b"`
	Sessions []locomoSession `json:"sessions"`
}

type locomoSession struct {
	ID    string       `json:"id"`
	Turns []locomoTurn `json:"turns"`
}

type locomoTurn struct {
	Text    string `json:"text"`
	Speaker string `json:"speaker"`
}

// loadLocomo10 reads the first n conversation items from the locomo fixture.
func loadLocomo10(tb testing.TB, n int) []locomoItem {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "benches", "fixtures", "locomo10.json"))
	if err != nil {
		tb.Fatalf("read fixture: %v", err)
	}
	var fx struct {
		Items []locomoItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		tb.Fatalf("parse fixture: %v", err)
	}
	if n <= 0 || n > len(fx.Items) {
		n = len(fx.Items)
	}
	if n == 0 {
		tb.Fatal("fixture has no items")
	}
	return fx.Items[:n]
}
