// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Shared mock OpenAI-compatible LLM server for the offline interface
// tests; dispatches by the system prompt of each LLM call point.

package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// mockLLM serves OpenAI-compatible /chat/completions and dispatches by the
// system prompt of each LLM call point; call counters are exposed.
type mockLLM struct {
	srv *httptest.Server
	// calls counts each call point. The counter sits behind a mutex because a consolidation pass
	// the round close scheduled runs on its own goroutine: without the lock, a test reading what
	// the model was asked races the handler that answers it — which is why every case that cares
	// about counts used to switch that trigger off instead of observing it.
	mu    sync.Mutex
	calls map[string]int
	// offContract, when set, is what every call point gets back instead of its own
	// contractual reply — the injection point for a model that answers off contract.
	// Set it before the call under test; the handler only reads it. The call counters
	// still tick, so a test can tell "refused after asking" from "never asked".
	offContract string
}

func newMockLLM(t testing.TB) *mockLLM {
	t.Helper()
	m := &mockLLM{calls: map[string]int{}}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var sys, user string
		for _, msg := range req.Messages {
			switch msg.Role {
			case "system":
				sys = msg.Content
			case "user":
				user = msg.Content
			}
		}
		var content string
		lower := strings.ToLower(sys)
		switch {
		case strings.Contains(lower, "meaningful keywords"):
			m.tally("keywords")
			content = `{"keywords":["重构","代码","测试"]}`
		case strings.Contains(lower, "l2 chat memory"):
			m.tally("consolidate")
			content = consolidateReply(user)
		case strings.Contains(lower, "l1 associative"):
			m.tally("distill")
			content = `{"emotion":{"valence":0.8,"arousal":0.6,"dominance":0.5},"mbti":{"i_e":0.2,"n_s":0.3,"t_f":-0.1,"j_p":0.4,"type":"ESFP"},"personality":"务实直接，注重代码质量，面对重构任务条理清晰，习惯先补测试再动手","per_node":[]}`
		default:
			t.Errorf("mockLLM: unknown system prompt: %.80s", sys)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if m.offContract != "" {
			content = m.offContract
		}
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// tally records one call from the handler goroutine.
func (m *mockLLM) tally(what string) {
	m.mu.Lock()
	m.calls[what]++
	m.mu.Unlock()
}

// count reads one call point's total.
func (m *mockLLM) count(what string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[what]
}

// consolidateReply builds a merge group from the first two topic ids echoed
// in the consolidate user prompt ("- id=... depth=..." lines).
func consolidateReply(user string) string {
	idRe := regexp.MustCompile(`id=(\d+)`)
	ids := idRe.FindAllStringSubmatch(user, -1)
	if len(ids) < 2 {
		return `{"l2_groups":[]}`
	}
	return fmt.Sprintf(`{"l2_groups":[{"node_hashes":[%s,%s],"merged_summary":"合并摘要保留全部细节"}]}`,
		ids[0][1], ids[1][1])
}
