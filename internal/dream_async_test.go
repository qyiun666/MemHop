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

	"github.com/qyiun666/MemHop/internal/common"
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
	sceneID := common.HashID("scene")

	db.triggerSceneDream(sceneID)
	// The trigger must have returned with the Dream still running.
	db.dreamMu.Lock()
	_, inFlight := db.dreamInFlight[sceneID]
	db.dreamMu.Unlock()
	if !inFlight {
		t.Fatal("trigger returned but the Dream is not in flight: expected async scheduling")
	}

	// A second trigger for the same scene must be a no-op (no stacking).
	db.triggerSceneDream(sceneID)
	db.dreamMu.Lock()
	count := len(db.dreamInFlight)
	db.dreamMu.Unlock()
	if count != 1 {
		t.Fatalf("in-flight scenes = %d, want 1 (dedup)", count)
	}

	// The background goroutine clears the marker after RunDream exits.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		db.dreamMu.Lock()
		_, still := db.dreamInFlight[sceneID]
		db.dreamMu.Unlock()
		if !still {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("in-flight marker never cleared: the background Dream goroutine did not exit")
}

// TestOpenInitializesDreamState locks the Open() contract that background
// Dream state is ready before the first Search/Update trigger fires.
func TestOpenInitializesDreamState(t *testing.T) {
	cfg := &MemHopConfig{
		DBPath:    filepath.Join(t.TempDir(), "open.meh"),
		VectorDim: 768,
		Defaults:  *DefaultMemHopDefaults,
	}
	db, err := Open(cfg, &mockEncoder{vec: testVec})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if db.dreamInFlight == nil {
		t.Fatal("Open must initialize dreamInFlight")
	}
	if db.dreamCancel == nil {
		t.Fatal("Open must initialize dreamCancel")
	}
	// A trigger on the freshly opened DB must not panic (nil map write).
	db.triggerSceneDream(common.HashID("scene"))
}
