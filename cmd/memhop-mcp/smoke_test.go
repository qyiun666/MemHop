// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

// TestSSEMultiTenantIsolation boots the SSE server in-process and verifies
// that two tenants on one process are isolated (scenes, archives, profiles)
// while the L3 knowledge graph is the file-wide shared pool, and that the
// full tool surface is present on each tenant.
func TestSSEMultiTenantIsolation(t *testing.T) {
	srv, dbDir := newTestServer(t, nil)

	alice := connectTenant(t, srv.URL, "alice")
	bob := connectTenant(t, srv.URL, "bob")

	// tools/list exposes all 24 tools on the alice session.
	tools, err := alice.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 24 {
		t.Errorf("expected 24 tools, got %d", len(tools.Tools))
	}
	toolsByName := make(map[string]*mcp.Tool, len(tools.Tools))
	for _, tool := range tools.Tools {
		toolsByName[tool.Name] = tool
	}
	for _, want := range []string{
		"memhop_search", "memhop_update", "memhop_dream", "memhop_checkpoint", "memhop_status",
		"memhop_l1_nodes",
		"memhop_profile_get", "memhop_profile_update", "memhop_scene_list", "memhop_scene_merge",
		"memhop_scene_topics", "memhop_scene_rename", "memhop_topic_rename",
		"memhop_knowledge_get", "memhop_knowledge_list", "memhop_knowledge_import",
		"memhop_knowledge_update", "memhop_knowledge_delete", "memhop_knowledge_nodes",
		"memhop_knowledge_subgraph", "memhop_archive_search", "memhop_archive_get",
		"memhop_archive_append",
		"memhop_trajectory_read",
	} {
		if _, ok := toolsByName[want]; !ok {
			t.Errorf("missing tool %q", want)
		}
	}

	// A tool's contract with its client is the argument names: renaming one is a
	// broken tool, and no call in this suite would notice — every call below passes
	// the names the tools have today. Required lists are pinned per tool, and a
	// required name has to be a declared property, or a client is told to send
	// something the schema never describes.
	for _, want := range []struct {
		name     string
		required []string
	}{
		{"memhop_search", nil},
		{"memhop_update", []string{"scene_id", "topic_id"}},
		{"memhop_dream", nil},
		{"memhop_checkpoint", nil},
		{"memhop_status", nil},
		{"memhop_l1_nodes", nil},
		{"memhop_profile_get", nil},
		{"memhop_profile_update", []string{"name"}},
		{"memhop_scene_list", nil},
		{"memhop_scene_merge", []string{"primary_id", "secondary_ids"}},
		{"memhop_scene_topics", []string{"scene_id"}},
		{"memhop_scene_rename", []string{"scene_id", "name"}},
		{"memhop_topic_rename", []string{"topic_id", "name"}},
		{"memhop_knowledge_get", []string{"id"}},
		{"memhop_knowledge_list", nil},
		{"memhop_knowledge_import", []string{"items", "mode"}},
		{"memhop_knowledge_update", []string{"id"}},
		{"memhop_knowledge_delete", []string{"id"}},
		{"memhop_knowledge_nodes", []string{"graph_id"}},
		{"memhop_knowledge_subgraph", []string{"graph_id", "start_node_id"}},
		{"memhop_archive_search", nil},
		{"memhop_archive_get", []string{"id"}},
		{"memhop_archive_append", []string{"topic_id", "content", "timestamp"}},
		{"memhop_trajectory_read", []string{"session_id"}},
	} {
		tool, ok := toolsByName[want.name]
		if !ok {
			continue // already reported as missing above
		}
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Errorf("%s: input schema is %T, want the decoded object", want.name, tool.InputSchema)
			continue
		}
		if got := requiredNames(t, want.name, schema); !slices.Equal(got, want.required) {
			t.Errorf("%s: required = %v, want %v", want.name, got, want.required)
		}
	}

	// An import item is validated against the inner schema, not the tool's, so
	// that list has to say what the batch pre-check refuses — title and domain.
	// A required entry nothing enforces makes every client send a field it need
	// not, and one the pre-check does enforce but the list omits lets a client
	// believe an item is acceptable that then fails the whole batch.
	importSchema, ok := toolsByName["memhop_knowledge_import"].InputSchema.(map[string]any)
	if !ok {
		t.Fatal("memhop_knowledge_import: input schema is not the decoded object")
	}
	items, _ := importSchema["properties"].(map[string]any)["items"].(map[string]any)
	item, _ := items["items"].(map[string]any)
	if item == nil {
		t.Fatal("memhop_knowledge_import: items does not describe its element")
	}
	if got := requiredNames(t, "memhop_knowledge_import item", item); !slices.Equal(got, []string{"title", "domain"}) {
		t.Errorf("memhop_knowledge_import item required = %v, want [title domain]", got)
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
	if st.Closed || st.SceneCount != 0 {
		t.Errorf("fresh db should be open with no scenes: %+v", st)
	}

	// Alice writes a profile; her own reads see it.
	if _, err := callClient(t, alice, "memhop_profile_update", map[string]any{
		"name":        "alice-agent",
		"role":        "tester",
		"personality": "concise",
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
	if p.Name != "alice-agent" || p.Role != "tester" || p.Personality != "concise" {
		t.Errorf("profile round-trip mismatch: %+v", p)
	}

	// Bob must not see Alice's data. Bob's own domain does carry an identity —
	// the tenant name it was created under — so what this pins is that bob's
	// profile is bob's, with none of alice's three fields in it.
	profile, err = callClient(t, bob, "memhop_profile_get", map[string]any{})
	if err != nil {
		t.Fatalf("bob memhop_profile_get: %v", err)
	}
	if err := json.Unmarshal([]byte(profile), &p); err != nil {
		t.Fatalf("unmarshal bob profile: %v", err)
	}
	if p.Name != "bob" {
		t.Errorf("bob's profile should carry his own tenant name: %+v", p)
	}
	if p.Role == "tester" || p.Personality == "concise" || p.Name == "alice-agent" {
		t.Errorf("bob sees alice's profile: %+v", p)
	}

	// memhop_scene_list answers an empty list on a fresh db (a scene slot carries
	// no topic count).
	scenes, err := callClient(t, alice, "memhop_scene_list", map[string]any{})
	if err != nil {
		t.Fatalf("memhop_scene_list: %v", err)
	}
	if scenes != "[]" {
		t.Errorf("fresh db scene_list: want [], got %s", scenes)
	}

	// memhop_scene_topics on an unknown scene returns an error
	// (SceneContext rejects unknown scene ids).
	if _, err := callClient(t, alice, "memhop_scene_topics", map[string]any{"scene_id": "0000000000000000"}); err == nil {
		t.Error("memhop_scene_topics on unknown scene: expected error")
	}

	// All tenants share one .meh file on disk (each gets its own agent
	// domain inside it).
	if _, err := os.Stat(filepath.Join(dbDir, "memhop.meh")); err != nil {
		t.Errorf("expected memhop.meh on disk: %v", err)
	}
}

// TestSSETenantWhitelist verifies that --tenants restricts which tenants may
// open a database; unknown tenants are rejected without creating files.
func TestSSETenantWhitelist(t *testing.T) {
	srv, _ := newTestServer(t, []string{"alice"})

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

// ---- helpers ----

// requiredNames reads one object schema's required list. A required name that is
// not one of the schema's properties is reported on the way out: a client is then
// told to send something the schema never describes.
func requiredNames(t *testing.T, what string, schema map[string]any) []string {
	t.Helper()
	props, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]any)
	var names []string
	for _, entry := range required {
		name, ok := entry.(string)
		if !ok {
			t.Errorf("%s: a required entry is %T, want an argument name", what, entry)
			continue
		}
		names = append(names, name)
		if _, declared := props[name]; !declared {
			t.Errorf("%s: %q is required but is not one of its properties", what, name)
		}
	}
	return names
}

// testLLM returns the LLM endpoint for offline tests. Credentials are test-only
// placeholders injected via environment variables, mirroring how the server reads
// them in production.
func testLLM(t *testing.T) memhop.LlmConfig {
	t.Helper()
	t.Setenv("MEMHOP_LLM_API_URL", "http://localhost:9999/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "smoke-cred")
	t.Setenv("MEMHOP_LLM_MODEL", "smoke-model")

	return memhop.LlmConfig{
		APIURL:          os.Getenv("MEMHOP_LLM_API_URL"),
		APIKey:          os.Getenv("MEMHOP_LLM_API_KEY"),
		Model:           os.Getenv("MEMHOP_LLM_MODEL"),
		TimeoutSecs:     30,
		MaxOutputTokens: 2048,
	}
}

// newTestServer boots an in-process SSE server over a temp db-dir.
func newTestServer(t *testing.T, tenants []string) (*httptest.Server, string) {
	t.Helper()
	return newTestServerWithDir(t, t.TempDir(), tenants)
}

// newTestServerWithDir boots an in-process SSE server over the given db-dir.
func newTestServerWithDir(t *testing.T, dbDir string, tenants []string) (*httptest.Server, string) {
	t.Helper()
	return newTestServerOver(t, testLLM(t), dbDir, tenants), dbDir
}

// newTestServerOver boots the in-process SSE server from a caller-supplied LLM
// endpoint, so a test can point the engine at a stub. The tuning knobs stay at
// their zero value, which is what the server itself runs with.
func newTestServerOver(t *testing.T, llm memhop.LlmConfig, dbDir string, tenants []string) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := newRegistry(llm, memhop.MemHopDefaults{}, dbDir, tenants, logger)
	srv := httptest.NewServer(newSSEHandler(reg))
	t.Cleanup(func() {
		srv.Close()
		if err := reg.CloseAll(); err != nil {
			t.Errorf("close all: %v", err)
		}
	})
	return srv
}

// stubLLMServer answers every chat completion with one keyword payload, so
// Update's single distillation succeeds without an external LLM.
func stubLLMServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{
			map[string]any{"message": map[string]any{
				"role": "assistant", "content": `{"keywords":["go","test"]}`,
			}},
		}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// turnTestLLM is testLLM rewired to the stub LLM endpoint.
func turnTestLLM(t *testing.T) memhop.LlmConfig {
	t.Helper()
	llm := testLLM(t)
	llm.APIURL = stubLLMServer(t).URL + "/v1"
	return llm
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
	Name        string `json:"name"`
	Role        string `json:"role"`
	Personality string `json:"personality"`
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
	reg := newRegistry(testLLM(t), memhop.MemHopDefaults{}, t.TempDir(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, id := range []string{"..", ".", "a/b", "a\\b"} {
		if _, err := reg.get(id); err == nil {
			t.Errorf("tenant id %q should be rejected", id)
		}
	}
}

// TestSSECloseAllPersists checks that CloseAll persists the one shared database and
// that shutdown is final: a session still connected when the process stops would
// otherwise reopen the file, and nothing closes that revived database again.
func TestSSECloseAllPersists(t *testing.T) {
	dbDir := t.TempDir()
	reg := newRegistry(testLLM(t), memhop.MemHopDefaults{}, dbDir, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if _, err := os.Stat(filepath.Join(dbDir, "memhop.meh")); err != nil {
		t.Errorf("expected memhop.meh persisted: %v", err)
	}
	if _, err := reg.get("alice"); !errors.Is(err, errRegistryClosed) {
		t.Errorf("a request after shutdown reopened the shared database: %v", err)
	}
	if err := reg.OpenShared(); !errors.Is(err, errRegistryClosed) {
		t.Errorf("an explicit open after shutdown reopened the shared database: %v", err)
	}
}

// A burst of first requests for the same tenant settles on one server: opening
// the domain happens outside the registry lock, so the work may be repeated and
// two servers may be built, but only one of them may be what the next request
// finds. A different tenant is a different domain, and its first request must not
// be decided by another tenant's cold start.
func TestRegistryConcurrentFirstAccessServesOneServerPerTenant(t *testing.T) {
	reg := newRegistry(testLLM(t), memhop.MemHopDefaults{}, t.TempDir(), nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = reg.CloseAll() })

	const tenants = 2
	got := make([]*mcp.Server, tenants)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, name := range []string{"alice", "bob"} {
		for range 24 {
			wg.Add(1)
			go func(i int, name string) {
				defer wg.Done()
				srv, err := reg.get(name)
				if err != nil {
					t.Errorf("get %s: %v", name, err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if got[i] != nil && got[i] != srv {
					t.Errorf("%s was served a second server", name)
				}
				got[i] = srv
			}(i, name)
		}
	}
	wg.Wait()
	if got[0] == nil || got[1] == nil || got[0] == got[1] {
		t.Fatalf("tenants did not each get their own server: %v %v", got[0] != nil, got[1] != nil)
	}
	if n := len(reg.entries); n != tenants {
		t.Fatalf("registry holds %d entries, want %d", n, tenants)
	}
}

// TestSSETurnFlow drives the hot path the way a host does, over MCP:
// memhop_search opens a scene and issues the turn's topic id, memhop_archive_append
// records what the turn said and what it did under that one key, memhop_update
// distills it, and the next read hands that turn back.
func TestSSETurnFlow(t *testing.T) {
	srv := newTestServerOver(t, turnTestLLM(t), t.TempDir(), nil)
	alice := connectTenant(t, srv.URL, "alice")

	opened, err := callClient(t, alice, "memhop_search", map[string]any{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var turn struct {
		Scene struct {
			SceneID string `json:"scene_id"`
		} `json:"scene"`
		NewTopicID string            `json:"new_topic_id"`
		Topics     []json.RawMessage `json:"topics"`
	}
	if err := json.Unmarshal([]byte(opened), &turn); err != nil {
		t.Fatalf("search output: %v (%s)", err, opened)
	}
	if len(turn.NewTopicID) != 16 || turn.Scene.SceneID == "" {
		t.Fatalf("search must open a turn: %s", opened)
	}
	if len(turn.Topics) != 0 {
		t.Fatalf("opening a turn must create no topic, got %d", len(turn.Topics))
	}

	// The turn's own record: two spoken lines and one operation, all under the key
	// Search issued, differentiated only by kind.
	appends := []map[string]any{
		{"topic_id": turn.NewTopicID, "kind": "utterance", "role": "user",
			"content": "go 项目怎么跑测试", "timestamp": 1000},
		{"topic_id": turn.NewTopicID, "kind": "utterance", "role": "agent",
			"content": "go test ./...", "timestamp": 2000},
		{"topic_id": turn.NewTopicID, "kind": "event", "event_type": "tool_call",
			"content": "ran go test", "timestamp": 1500},
	}
	for _, args := range appends {
		if _, err := callClient(t, alice, "memhop_archive_append", args); err != nil {
			t.Fatalf("archive append %v: %v", args["kind"], err)
		}
	}
	// An utterance that does not say who spoke is refused: a label-less transcript
	// is the thing keyword extraction cannot recover.
	if _, err := callClient(t, alice, "memhop_archive_append", map[string]any{
		"topic_id": turn.NewTopicID, "kind": "utterance",
		"content": "anonymous", "timestamp": 2100,
	}); err == nil {
		t.Fatal("an utterance without a role must be refused")
	}

	// An engine refusal reaches the client with its code, the only channel a tool
	// client has: the reserved all-zero topic key is refused on the read side exactly
	// as it is on the write side, and a client has to be able to tell that refusal
	// from a record that will not read.
	_, zeroKey := callClient(t, alice, "memhop_archive_search", map[string]any{
		"topic_id": "0000000000000000"})
	if zeroKey == nil {
		t.Fatal("the reserved zero topic key must be refused")
	}
	if want := fmt.Sprintf("[%d]", memhop.ErrInvalidQuery); !strings.Contains(zeroKey.Error(), want) {
		t.Fatalf("an engine refusal must carry its code %s, got %v", want, zeroKey)
	}

	// The tool layer's own refusals carry a code by the same rule: a client has no
	// other channel, and "this argument is outside the vocabulary" has to be tellable
	// from "that record will not read". Each of these is judged before the database is
	// touched, so the code is the resolver's and not whatever a lookup would have said.
	for _, tc := range []struct {
		name string
		tool string
		args map[string]any
		code memhop.Code
	}{
		{"append kind", "memhop_archive_append", map[string]any{
			"topic_id": turn.NewTopicID, "kind": "nonsense", "role": "user",
			"content": "x", "timestamp": 2200}, memhop.ErrInvalidQuery},
		{"append role", "memhop_archive_append", map[string]any{
			"topic_id": turn.NewTopicID, "kind": "utterance", "role": "nonsense",
			"content": "x", "timestamp": 2201}, memhop.ErrInvalidQuery},
		{"append content_type", "memhop_archive_append", map[string]any{
			"topic_id": turn.NewTopicID, "kind": "utterance", "role": "user",
			"content_type": "nonsense", "content": "x", "timestamp": 2202}, memhop.ErrInvalidQuery},
		{"import mode", "memhop_knowledge_import", map[string]any{
			"items": []any{map[string]any{"title": "t", "domain": "d", "content": "c"}},
			"mode":  "nonsense"}, memhop.ErrInvalidQuery},
		{"relation kind", "memhop_knowledge_import", map[string]any{
			"items": []any{map[string]any{"title": "t", "domain": "d", "content": "c",
				"related": []any{map[string]any{"titles": []any{"other"}, "kind": "nonsense"}}}},
			"mode": "Skip"}, memhop.ErrInvalidQuery},
		{"subgraph edge kind", "memhop_knowledge_subgraph", map[string]any{
			"graph_id": "0123456789abcdef", "start_node_id": "0123456789abcdef",
			"edge_kinds": []any{"nonsense"}}, memhop.ErrInvalidQuery},
		{"archive by an id nothing holds", "memhop_archive_get", map[string]any{
			"id": "0123456789abcdef"}, memhop.ErrNotFound},
	} {
		_, err := callClient(t, alice, tc.tool, tc.args)
		if err == nil {
			t.Fatalf("%s outside the vocabulary: want a refusal", tc.name)
		}
		if want := fmt.Sprintf("[%d]", tc.code); !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: a tool refusal must carry its code %s, got %v", tc.name, want, err)
		}
	}

	// An optional filter sent as an empty string is no filter. The resolvers'
	// empty-value defaults serve the append path, where a record naming no kind is an
	// utterance; a search naming no kind wants both — and this topic holds three
	// records, one of them an event.
	both, err := callClient(t, alice, "memhop_archive_search", map[string]any{
		"topic_id": turn.NewTopicID, "kind": ""})
	if err != nil {
		t.Fatalf("archive search with an empty kind: %v", err)
	}
	var slots []struct {
		Kind int `json:"kind"`
	}
	if err := json.Unmarshal([]byte(both), &slots); err != nil {
		t.Fatalf("search output: %v (%s)", err, both)
	}
	var eventCount int
	for _, s := range slots {
		if s.Kind == int(memhop.KindEvent) {
			eventCount++
		}
	}
	if len(slots) != 3 || eventCount != 1 {
		t.Fatalf("an empty kind must not narrow the read: %d records, %d events (%s)",
			len(slots), eventCount, both)
	}

	// The topic key is no different: an empty one is not an address, and a client
	// that sends "" means "no topic condition". That read spans the domain, which at
	// this point holds exactly the three records above — same counts, or the empty
	// key became a condition of its own.
	unscoped, err := callClient(t, alice, "memhop_archive_search", map[string]any{"topic_id": ""})
	if err != nil {
		t.Fatalf("archive search with an empty topic key: %v", err)
	}
	var wide []struct {
		Kind int `json:"kind"`
	}
	if err := json.Unmarshal([]byte(unscoped), &wide); err != nil {
		t.Fatalf("unscoped search output: %v (%s)", err, unscoped)
	}
	var wideEvents int
	for _, s := range wide {
		if s.Kind == int(memhop.KindEvent) {
			wideEvents++
		}
	}
	if len(wide) != len(slots) || wideEvents != eventCount {
		t.Fatalf(`"topic_id": "" must read like no topic condition at all: %d records / %d events vs %d / %d`,
			len(wide), wideEvents, len(slots), eventCount)
	}

	settled, err := callClient(t, alice, "memhop_update", map[string]any{
		"scene_id": turn.Scene.SceneID, "topic_id": turn.NewTopicID,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(settled, `"ok":true`) {
		t.Fatalf("update reported %s, want ok", settled)
	}

	reread, err := callClient(t, alice, "memhop_search", map[string]any{"scene_id": turn.Scene.SceneID})
	if err != nil {
		t.Fatalf("search again: %v", err)
	}
	if !strings.Contains(reread, turn.NewTopicID) {
		t.Fatalf("the turn must be back in the session surface: %s", reread)
	}

	events, err := callClient(t, alice, "memhop_trajectory_read", map[string]any{"session_id": turn.NewTopicID})
	if err != nil {
		t.Fatalf("trajectory read: %v", err)
	}
	if !strings.Contains(events, "ran go test") {
		t.Fatalf("the turn's events must read back by its topic id: %s", events)
	}
	// The read is the event track, not the whole topic: what was spoken stays out
	// of it, so a host asking for one turn's operations does not also get its
	// dialogue.
	if strings.Contains(events, "怎么跑测试") {
		t.Fatalf("trajectory read leaked the dialogue: %s", events)
	}
	if got, err := callClient(t, alice, "memhop_archive_search", map[string]any{
		"topic_id": turn.NewTopicID, "kind": "utterance",
	}); err != nil || !strings.Contains(got, "怎么跑测试") {
		t.Fatalf("utterances of the turn: %s err=%v", got, err)
	}

	// A turn cannot be settled without the id Search issued: the missing
	// required argument is refused rather than silently minting a topic.
	if _, err := callClient(t, alice, "memhop_update", map[string]any{
		"scene_id": turn.Scene.SceneID,
	}); err == nil {
		t.Fatal("update without topic_id must be rejected")
	}
}
