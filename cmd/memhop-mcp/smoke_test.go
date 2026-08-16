// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPSmoke boots the real binary over stdio with an SDK client and
// exercises the offline tool paths (no Ollama / LLM needed: encoder and LLM
// clients are lazy and only contacted on Search/Dream/Crystallize).
func TestMCPSmoke(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "memhop-mcp")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	cmd := exec.Command(bin,
		"--db", filepath.Join(t.TempDir(), "smoke.meh"),
		"--vector-dim", "1024",
		"--encoder-addr", "http://127.0.0.1:11434",
		"--embed-model", "bge-m3",
	)
	cmd.Env = append(os.Environ(),
		"MEMHOP_LLM_API_URL=http://localhost:9999/v1",
		"MEMHOP_LLM_API_KEY=smoke-key",
		"MEMHOP_LLM_MODEL=smoke-model",
	)

	client := mcp.NewClient(&mcp.Implementation{Name: "smoke-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	// tools/list exposes all 26 tools.
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 26 {
		t.Errorf("expected 26 tools, got %d", len(tools.Tools))
	}
	names := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{
		"memhop_search", "memhop_update", "memhop_dream", "memhop_checkpoint", "memhop_status",
		"memhop_profile_get", "memhop_profile_update", "memhop_scene_list", "memhop_scene_merge",
		"memhop_knowledge_get", "memhop_knowledge_list", "memhop_knowledge_import",
		"memhop_knowledge_update", "memhop_knowledge_delete", "memhop_knowledge_nodes",
		"memhop_knowledge_subgraph", "memhop_archive_search", "memhop_archive_get",
		"memhop_plugin_import", "memhop_plugin_get", "memhop_plugin_delete", "memhop_plugin_list",
		"memhop_trajectory_append", "memhop_trajectory_read", "memhop_trajectory_delete",
		"memhop_crystallize",
	} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}

	// memhop_status: no-arg tool.
	status, err := callClient(t, session, "memhop_status", map[string]any{})
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

	// memhop_profile_update then memhop_profile_get: offline write path.
	if _, err := callClient(t, session, "memhop_profile_update", map[string]any{
		"name":         "smoke-agent",
		"role":         "tester",
		"style_traits": []string{"concise"},
	}); err != nil {
		t.Fatalf("memhop_profile_update: %v", err)
	}
	profile, err := callClient(t, session, "memhop_profile_get", map[string]any{})
	if err != nil {
		t.Fatalf("memhop_profile_get: %v", err)
	}
	var p memhopProfile
	if err := json.Unmarshal([]byte(profile), &p); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if p.Name != "smoke-agent" || p.Role != "tester" {
		t.Errorf("profile round-trip mismatch: %+v", p)
	}

	// memhop_checkpoint persists without closing.
	if _, err := callClient(t, session, "memhop_checkpoint", map[string]any{}); err != nil {
		t.Fatalf("memhop_checkpoint: %v", err)
	}

	// Unknown tool must fail as a protocol error (MCP spec: server-side error).
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "memhop_nope",
		Arguments: map[string]any{},
	}); err == nil {
		t.Error("unknown tool should fail as a protocol error")
	}
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
