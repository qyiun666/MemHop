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
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

// TestSSEMultiTenantIsolation boots the SSE server in-process and verifies
// that two tenants on one process are fully isolated: separate .meh files,
// no data visible across tenants, and the full 32-tool surface on each.
func TestSSEMultiTenantIsolation(t *testing.T) {
	srv, dbDir := newTestServer(t, nil)

	alice := connectTenant(t, srv.URL, "alice")
	bob := connectTenant(t, srv.URL, "bob")

	// tools/list exposes all 32 tools on the alice session.
	tools, err := alice.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 32 {
		t.Errorf("expected 32 tools, got %d", len(tools.Tools))
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
		"memhop_trajectory_append", "memhop_trajectory_read", "memhop_trajectory_stats", "memhop_trajectory_delete",
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

// TestSSETenantWhitelist verifies that --tenants restricts which tenants may
// open a database; unknown tenants are rejected without creating files.
func TestSSETenantWhitelist(t *testing.T) {
	srv, dbDir := newTestServer(t, []string{"alice"})

	// alice is allowed and opens fine.
	session := connectTenant(t, srv.URL, "alice")
	if _, err := session.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("alice list tools: %v", err)
	}

	// bob is not whitelisted: the SSE endpoint returns an error (no session).
	client := mcp.NewClient(&mcp.Implementation{Name: "smoke-client", Version: "0.0.1"}, nil)
	if _, err := client.Connect(context.Background(), &mcp.SSEClientTransport{
		Endpoint:   srv.URL + "/mcp/bob",
		HTTPClient: &http.Client{},
	}, nil); err == nil {
		t.Error("bob should be rejected by the whitelist")
	}
	if _, err := os.Stat(filepath.Join(dbDir, "bob.meh")); !os.IsNotExist(err) {
		t.Errorf("bob.meh should not exist, stat err = %v", err)
	}
}

// TestSSEInvalidTenant verifies malformed tenant ids never open a database.
func TestSSEInvalidTenant(t *testing.T) {
	srv, dbDir := newTestServer(t, nil)

	for _, bad := range []string{"alice/../root", "../escape", "has space", "dot.name"} {
		client := mcp.NewClient(&mcp.Implementation{Name: "smoke-client", Version: "0.0.1"}, nil)
		if _, err := client.Connect(context.Background(), &mcp.SSEClientTransport{
			Endpoint:   srv.URL + "/mcp/" + bad,
			HTTPClient: &http.Client{},
		}, nil); err == nil {
			t.Errorf("tenant %q should be rejected", bad)
		}
	}
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		t.Fatalf("read db-dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("invalid tenants must not create files, found %d entries", len(entries))
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
	for range n {
		wg.Go(func() {
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
		})
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

// testBase returns the shared engine config for offline tests. LLM
// credentials are test-only placeholders injected via environment
// variables, mirroring how the server reads them in production.
func testBase(t *testing.T) memhop.MemHopConfig {
	t.Helper()
	t.Setenv("MEMHOP_LLM_API_URL", "http://localhost:9999/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "smoke-cred")
	t.Setenv("MEMHOP_LLM_MODEL", "smoke-model")

	base := memhop.MemHopConfig{
		VectorDim:          1024,
		EncoderAddr:        "http://127.0.0.1:11434",
		EmbedModel:         "bge-m3",
		EncoderTimeoutSecs: 20,
	}
	base.LLM.APIURL = os.Getenv("MEMHOP_LLM_API_URL")
	base.LLM.APIKey = os.Getenv("MEMHOP_LLM_API_KEY")
	base.LLM.Model = os.Getenv("MEMHOP_LLM_MODEL")
	base.LLM.TimeoutSecs = 30
	base.LLM.MaxOutputTokens = 2048
	return base
}

// newTestServer boots an in-process SSE server over a temp db-dir.
func newTestServer(t *testing.T, tenants []string) (*httptest.Server, string) {
	t.Helper()
	return newTestServerWithDir(t, t.TempDir(), tenants)
}

// newTestServerWithDir boots an in-process SSE server over the given db-dir.
func newTestServerWithDir(t *testing.T, dbDir string, tenants []string) (*httptest.Server, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := newRegistry(testBase(t), dbDir, tenants, logger)
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
		if len(res.Content) == 0 {
			return "", errTool("tool error (no content)")
		}
		if text, ok := res.Content[0].(*mcp.TextContent); ok {
			return "", errTool("tool error: " + text.Text)
		}
		return "", errTool("tool error")
	}
	if len(res.Content) == 0 {
		return "", nil
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return "", errTool("unexpected content type")
	}
	return text.Text, nil
}

type errTool string

func (e errTool) Error() string { return string(e) }

// TestSSERegistryRejectsPathTraversal guards the tenant registry's defense
// in depth: even if a tenant id reached the registry, the resolved path
// must stay inside db-dir.
func TestSSERegistryRejectsPathTraversal(t *testing.T) {
	reg := newRegistry(testBase(t), t.TempDir(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	reg.open = func(cfg *memhop.MemHopConfig) (*memhop.DB, error) {
		return memhop.OpenWithEncoder(cfg, &smokeEncoder{dim: cfg.VectorDim})
	}
	for _, id := range []string{"..", ".", "a/b", "a\\b"} {
		if _, err := reg.get(id); err == nil {
			t.Errorf("tenant id %q should be rejected", id)
		}
	}
}

// TestSSECloseAllPersists checks that CloseAll persists every open tenant.
func TestSSECloseAllPersists(t *testing.T) {
	dbDir := t.TempDir()
	reg := newRegistry(testBase(t), dbDir, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	reg.open = func(cfg *memhop.MemHopConfig) (*memhop.DB, error) {
		return memhop.OpenWithEncoder(cfg, &smokeEncoder{dim: cfg.VectorDim})
	}
	if _, err := reg.get("alice"); err != nil {
		t.Fatalf("open alice: %v", err)
	}
	if _, err := reg.get("bob"); err != nil {
		t.Fatalf("open bob: %v", err)
	}
	if err := reg.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	if len(reg.entries) != 0 {
		t.Errorf("entries not cleared: %d", len(reg.entries))
	}
	for _, want := range []string{"alice.meh", "bob.meh"} {
		if _, err := os.Stat(filepath.Join(dbDir, want)); err != nil {
			t.Errorf("expected %s persisted: %v", want, err)
		}
	}
}
