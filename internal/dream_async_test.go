// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/capabilities"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// slowLLMServer answers chat completions after delay ms, so the caller can
// observe that triggerSceneDream returns before the Dream pipeline ends.
func slowLLMServer(t *testing.T, delay time.Duration, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		time.Sleep(delay)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": content},
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestTriggerSceneDreamSchedulesBackground the trigger returns immediately
// (in-flight marker visible before the slow Dream finishes), repeated
// triggers for the same scene do not stack, and the marker is cleared once
// the background Dream exits.
func TestTriggerSceneDreamSchedulesBackground(t *testing.T) {
	srv := slowLLMServer(t, 200*time.Millisecond, `{"keywords":["x"]}`)
	db := newSearchTestDB(t, srv.URL)
	ac, err := db.contextFor(core.DefaultAgentID)
	if err != nil {
		t.Fatal(err)
	}
	sceneID := common.HashID("scene")

	ac.mu.Lock()
	db.triggerSceneDream(ac, sceneID)
	// The trigger must have returned with the Dream still running.
	_, inFlight := ac.dreamInFlight[sceneID]
	ac.mu.Unlock()
	if !inFlight {
		t.Fatal("trigger returned but the Dream is not in flight: expected async scheduling")
	}

	// A second trigger for the same scene must be a no-op (no stacking).
	ac.mu.Lock()
	db.triggerSceneDream(ac, sceneID)
	count := len(ac.dreamInFlight)
	ac.mu.Unlock()
	if count != 1 {
		t.Fatalf("in-flight scenes = %d, want 1 (dedup)", count)
	}

	// The background goroutine clears the marker after RunDream exits.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ac.mu.Lock()
		_, still := ac.dreamInFlight[sceneID]
		ac.mu.Unlock()
		if !still {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("in-flight marker never cleared: the background Dream goroutine did not exit")
}

// TestOpenInitializesDreamState locks the Open() contract that background
// Dream state is ready before the first Update consolidation trigger fires.
func TestOpenInitializesDreamState(t *testing.T) {
	cfg := &MemHopConfig{
		DBPath:   filepath.Join(t.TempDir(), "open.meh"),
		Defaults: *DefaultMemHopDefaults,
	}
	db, err := Open(cfg, capabilities.FS)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if db.agents == nil {
		t.Fatal("Open must initialize the agents registry")
	}
	if db.baseCtx == nil {
		t.Fatal("Open must initialize the base context")
	}
	// A trigger on the freshly opened DB must not panic (nil map write).
	ac, err := db.contextFor(core.DefaultAgentID)
	if err != nil {
		t.Fatal(err)
	}
	ac.mu.Lock()
	db.triggerSceneDream(ac, common.HashID("scene"))
	ac.mu.Unlock()
}
