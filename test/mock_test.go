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
	"testing"
)

// mockLLM serves OpenAI-compatible /chat/completions and dispatches by the
// system prompt of each LLM call point; call counters are exposed.
type mockLLM struct {
	srv   *httptest.Server
	calls map[string]int
}

func newMockLLM(t *testing.T) *mockLLM {
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
			m.calls["keywords"]++
			content = `{"keywords":["重构","代码","测试"]}`
		case strings.Contains(lower, "l2 chat memory"):
			m.calls["consolidate"]++
			content = consolidateReply(user)
		case strings.Contains(lower, "l1 associative"):
			m.calls["distill"]++
			content = `{"emotion":{"valence":0.8,"arousal":0.6,"dominance":0.5},"mbti":{"i_e":0.2,"n_s":0.3,"t_f":-0.1,"j_p":0.4,"type":"ESFP"},"per_node":[]}`
		case strings.Contains(lower, "operation trajectory"):
			m.calls["crystallize"]++
			content = `{"capabilities":[{"action":"create","capability":{"format":"memhop-capability/v3","name":"重构流程","version":"1","type":"mcp","summary":"重构代码","trigger":"用户要求重构","resources":[{"type":"mcp","name":"read_file","ref":"read_file","desc":"读文件","input":"{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\"}},\"required\":[\"path\"]}","output":"内容","config":"{\"file\":\"a.go\"}"}]}}]}`
		default:
			t.Errorf("mockLLM: unknown system prompt: %.80s", sys)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// consolidateReply builds a merge group from the first two topic ids echoed
// in the consolidate user prompt ("- id=... depth=..." lines).
func consolidateReply(user string) string {
	idRe := regexp.MustCompile(`id=(\d+)`)
	sceneRe := regexp.MustCompile(`## scene_id = (\d+)`)
	ids := idRe.FindAllStringSubmatch(user, -1)
	scene := sceneRe.FindStringSubmatch(user)
	if len(scene) == 0 || len(ids) < 2 {
		return `{"l2_groups":[],"l2_compression_needed":false}`
	}
	return fmt.Sprintf(`{"l2_groups":[{"scene_id":%s,"node_hashes":[%s,%s],"merged_summary":"合并摘要保留全部细节"}],"l2_compression_needed":true}`,
		scene[1], ids[0][1], ids[1][1])
}
