// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop"
)

// TestSSEMultiTenantIsolation boots the SSE server in-process and verifies
// that two tenants on one process are fully isolated: separate .meh files,
// no data visible across tenants, and the full 31-tool surface on each.
func TestSSEMultiTenantIsolation(t *testing.T) {
	srv, dbDir := newTestServer(t, nil)

	alice := connectTenant(t, srv.URL, "alice")
	bob := connectTenant(t, srv.URL, "bob")

	// tools/list exposes all 31 tools on the alice session.
	tools, err := alice.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 31 {
		t.Errorf("expected 31 tools, got %d", len(tools.Tools))
	}
	names := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{
		"memhop_search", "memhop_update", "memhop_dream", "memhop_checkpoint", "memhop_status",
		"memhop_profile_get", "memhop_profile_update", "memhop_scene_list", "memhop_scene_active_list", "memhop_scene_merge",
		"memhop_scene_topics",
		"memhop_knowledge_get", "memhop_knowledge_list", "memhop_knowledge_import",
		"memhop_knowledge_update", "memhop_knowledge_delete", "memhop_knowledge_nodes",
		"memhop_knowledge_subgraph", "memhop_archive_search", "memhop_archive_get",
		"memhop_capability_import", "memhop_capability_get", "memhop_capability_delete", "memhop_capability_list", "memhop_capability_update", "memhop_capability_activate", "memhop_capability_usage",
		"memhop_trajectory_append", "memhop_trajectory_read", "memhop_trajectory_delete",
		"memhop_crystallize",
	} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}

	// memhop_status: no-arg tool on the alice session.
	status, err := callClient(t, alice, "memhop_status", map[string]any{})
	if err != nil {
		t.Fatalf("memhop_status: %v", err)
	}
	var st statusResult
	if err := json.Unmarshal([]byte(status), &st); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if st.Closed || st.HasActiveScenes {
		t.Errorf("fresh db should be open without scenes: %+v", st)
	}

	// Alice writes a profile; her own reads see it.
	if _, err := callClient(t, alice, "memhop_profile_update", map[string]any{
		"name":         "alice-agent",
		"role":         "tester",
		"style_traits": []string{"concise"},
	}); err != nil {
		t.Fatalf("memhop_profile_update: %v", err)
	}
	profile, err := callClient(t, alice, "memhop_profile_get", map[string]any{})
	if err != nil {
		t.Fatalf("memhop_profile_get: %v", err)
	}
	var p memhopProfile
	if err := json.Unmarshal([]byte(profile), &p); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if p.Name != "alice-agent" || p.Role != "tester" {
		t.Errorf("profile round-trip mismatch: %+v", p)
	}

	// Bob must not see Alice's data: a fresh profile with empty name.
	profile, err = callClient(t, bob, "memhop_profile_get", map[string]any{})
	if err != nil {
		t.Fatalf("bob memhop_profile_get: %v", err)
	}
	if err := json.Unmarshal([]byte(profile), &p); err != nil {
		t.Fatalf("unmarshal bob profile: %v", err)
	}
	if p.Name != "" || p.Role != "" || len(p.StyleTraits) != 0 {
		t.Errorf("bob sees alice's profile: %+v", p)
	}

	// memhop_scene_list returns scene slots with topic counts on a fresh db.
	scenes, err := callClient(t, alice, "memhop_scene_list", map[string]any{})
	if err != nil {
		t.Fatalf("memhop_scene_list: %v", err)
	}
	if scenes != "[]" {
		t.Errorf("fresh db scene_list: want [], got %s", scenes)
	}

	// memhop_scene_active_list mirrors scene_list on a fresh db (no scenes).
	activeScenes, err := callClient(t, alice, "memhop_scene_active_list", map[string]any{})
	if err != nil {
		t.Fatalf("memhop_scene_active_list: %v", err)
	}
	if activeScenes != "[]" {
		t.Errorf("fresh db scene_active_list: want [], got %s", activeScenes)
	}

	// memhop_scene_topics on an unknown scene returns an error
	// (SceneContext rejects unknown scene ids).
	if _, err := callClient(t, alice, "memhop_scene_topics", map[string]any{"scene_id": "0000000000000000"}); err == nil {
		t.Error("memhop_scene_topics on unknown scene: expected error")
	}

	// Each tenant got its own .meh file on disk.
	for _, want := range []string{"alice.meh", "bob.meh"} {
		if _, err := os.Stat(filepath.Join(dbDir, want)); err != nil {
			t.Errorf("expected %s on disk: %v", want, err)
		}
	}
}

// TestSSEInvalidTenant rejects malformed or path-traversal tenant ids.
func TestSSEInvalidTenant(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	for _, path := range []string{
		"/mcp/..",
		"/mcp/",
		"/mcp/a/b",
		"/mcp/bad%20id",
		"/mcp/" + strings.Repeat("a", 65),
		"/other",
	} {
		client := mcp.NewClient(&mcp.Implementation{Name: "smoke-client", Version: "0.0.1"}, nil)
		if _, err := client.Connect(context.Background(), &mcp.SSEClientTransport{
			Endpoint: srv.URL + path,
		}, nil); err == nil {
			t.Errorf("path %q: expected connection error", path)
		}
	}
}

// TestSSETenantWhitelist enforces the --tenants whitelist: unlisted tenants
// cannot connect, listed ones can.
func TestSSETenantWhitelist(t *testing.T) {
	srv, dbDir := newTestServer(t, []string{"alice"})

	client := mcp.NewClient(&mcp.Implementation{Name: "smoke-client", Version: "0.0.1"}, nil)
	if _, err := client.Connect(context.Background(), &mcp.SSEClientTransport{
		Endpoint: srv.URL + "/mcp/bob",
	}, nil); err == nil {
		t.Error("bob should be rejected by the whitelist")
	}

	alice := connectTenant(t, srv.URL, "alice")
	if _, err := alice.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("alice should connect: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dbDir, "alice.meh")); err != nil {
		t.Errorf("expected alice.meh on disk: %v", err)
	}
}

// TestSSETenantReconnect verifies the DB survives client disconnects:
// reconnecting to the same tenant sees previously written data, because
// tenant DBs live until process exit, not until the session closes.
func TestSSETenantReconnect(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	alice := connectTenant(t, srv.URL, "alice")
	if _, err := callClient(t, alice, "memhop_profile_update", map[string]any{
		"name": "persistent-agent",
	}); err != nil {
		t.Fatalf("profile update: %v", err)
	}
	if err := alice.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}

	// Reconnect: same tenant, same DB instance, data still visible.
	alice2 := connectTenant(t, srv.URL, "alice")
	profile, err := callClient(t, alice2, "memhop_profile_get", map[string]any{})
	if err != nil {
		t.Fatalf("profile get after reconnect: %v", err)
	}
	var p memhopProfile
	if err := json.Unmarshal([]byte(profile), &p); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if p.Name != "persistent-agent" {
		t.Errorf("data lost after reconnect: %+v", p)
	}
}

// TestSSETenantConcurrentFirstConnect opens the same tenant from many
// goroutines at once: the registry mutex must open the DB exactly once and
// every connection must succeed.
func TestSSETenantConcurrentFirstConnect(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	const n = 8
	errCh := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := mcp.NewClient(&mcp.Implementation{Name: "smoke-client", Version: "0.0.1"}, nil)
			session, err := client.Connect(context.Background(), &mcp.SSEClientTransport{
				Endpoint:   srv.URL + "/mcp/alice",
				HTTPClient: &http.Client{},
			}, nil)
			if err != nil {
				errCh <- err
				return
			}
			defer session.Close()
			if _, err := session.ListTools(context.Background(), nil); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent connect failed: %v", err)
	}
}

// smokeEncoder lets the MCP smoke tests open tenant DBs without Ollama.
type smokeEncoder struct{ dim int }

func (e *smokeEncoder) Encode(string) ([]float32, error) {
	vec := make([]float32, e.dim)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func (*smokeEncoder) IsAvailable() bool { return true }

// ---- helpers ----

// newTestServer boots an in-process SSE server over a temp db-dir.
func newTestServer(t *testing.T, tenants []string) (*httptest.Server, string) {
	t.Helper()
	dbDir := t.TempDir()
	base := memhop.MemHopConfig{
		VectorDim:          1024,
		EncoderAddr:        "http://127.0.0.1:11434",
		EmbedModel:         "bge-m3",
		EncoderTimeoutSecs: 20,
		LLM: memhop.LlmConfig{
			APIURL:          "http://localhost:9999/v1",
			APIKey:          "smoke-key",
			Model:           "smoke-model",
			TimeoutSecs:     30,
			MaxOutputTokens: 2048,
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := newRegistry(base, dbDir, tenants, logger)
	// Keep the SSE smoke tests offline: open tenants through a mock encoder
	// instead of memhop.Open's Ollama health check.
	reg.open = func(cfg *memhop.MemHopConfig) (*memhop.DB, error) {
		return memhop.OpenWithEncoder(cfg, &smokeEncoder{dim: cfg.VectorDim})
	}
	srv := httptest.NewServer(newSSEHandler(reg))
	t.Cleanup(func() {
		srv.Close()
		if err := reg.CloseAll(); err != nil {
			t.Errorf("close all: %v", err)
		}
	})
	return srv, dbDir
}

// connectTenant opens an MCP session against /mcp/<tenant>.
func connectTenant(t *testing.T, baseURL, tenant string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "smoke-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.SSEClientTransport{
		Endpoint:   baseURL + "/mcp/" + tenant,
		HTTPClient: &http.Client{},
	}, nil)
	if err != nil {
		t.Fatalf("connect tenant %q: %v", tenant, err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// memhopProfile mirrors the JSON subset of ProfileSlot used by the smoke test.
type memhopProfile struct {
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	StyleTraits []string `json:"style_traits"`
}

// callClient invokes a tool and returns the first text content.
func callClient(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (string, error) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", mcpErr{res}
	}
	if len(res.Content) == 0 {
		return "", nil
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return "", errUnexpectedContent{res.Content[0]}
	}
	return text.Text, nil
}

type mcpErr struct{ res *mcp.CallToolResult }

func (e mcpErr) Error() string {
	if len(e.res.Content) == 0 {
		return "tool error (no content)"
	}
	if text, ok := e.res.Content[0].(*mcp.TextContent); ok {
		return "tool error: " + text.Text
	}
	return "tool error"
}

type errUnexpectedContent struct{ c mcp.Content }

func (e errUnexpectedContent) Error() string { return "unexpected content type" }
