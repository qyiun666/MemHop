// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

// TestStreamableMultiTenant boots the Streamable HTTP transport in-process
// and verifies per-request tenant routing: initialize and tools/list hit
// the tenant registry through the same /mcp/<tenant-id> path as SSE.
func TestStreamableMultiTenant(t *testing.T) {
	dbDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := newRegistry(testBase(t), dbDir, nil, logger)
	reg.open = func(cfg *memhop.MemHopConfig) (*memhop.DB, error) {
		return memhop.OpenWithEncoder(cfg, &smokeEncoder{dim: cfg.VectorDim})
	}
	srv := httptest.NewServer(newStreamableHandler(reg))
	t.Cleanup(func() {
		srv.Close()
		if err := reg.CloseAll(); err != nil {
			t.Errorf("close all: %v", err)
		}
	})

	post := func(tenant, method string, id int) map[string]any {
		t.Helper()
		body := map[string]any{
			"jsonrpc": "2.0", "id": id, "method": method,
			"params": map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "smoke", "version": "0.0.1"},
			},
		}
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/"+tenant, strings.NewReader(string(raw)))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post %s: %v", method, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("post %s: status %d", method, resp.StatusCode)
		}
		// The go-sdk serves streamable responses as text/event-stream by
		// default; collect the data: payload of the (single) message event.
		var data []byte
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if after, ok := strings.CutPrefix(line, "data: "); ok {
				data = append(data, after...)
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatalf("read %s: %v", method, err)
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode %s (%q): %v", method, string(data), err)
		}
		return out
	}

	// initialize resolves the tenant and starts a stateless session.
	initResp := post("alice", "initialize", 1)
	if initResp["error"] != nil {
		t.Fatalf("initialize error: %v", initResp["error"])
	}
	// tools/list through the same tenant path.
	listResp := post("alice", "tools/list", 2)
	if listResp["error"] != nil {
		t.Fatalf("tools/list error: %v", listResp["error"])
	}
	// A second tenant gets its own registry entry (isolated .meh file).
	post("bob", "initialize", 1)
	if len(reg.entries) != 2 {
		t.Errorf("expected 2 tenant entries, got %d", len(reg.entries))
	}
	// Malformed tenant path is rejected with no server (400).
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp/bad..path",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bad tenant request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad tenant: status %d, want 400", resp.StatusCode)
	}
}
