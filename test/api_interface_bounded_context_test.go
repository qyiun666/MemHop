// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceBoundedContext(t *testing.T) {
	t.Run("normal dream keeps latest and full transcript", func(t *testing.T) {
		llm := newMockLLM(t)
		db := openBoundedContextDB(t, llm.srv.URL, 2, 100)
		sceneID, settled := settleBoundedContextTurns(t, db, 5)
		if _, err := db.Dream(context.Background(), sceneID); err != nil {
			t.Fatalf("Dream: %v", err)
		}

		res, full := assertBoundedSearch(t, db, sceneID, 2)
		if len(full.Topics) != len(settled) {
			t.Fatalf("SceneContext kept %d topics, want all %d", len(full.Topics), len(settled))
		}
		for i, row := range full.Topics {
			if row.TopicID != settled[i] || len(row.Messages) != 2 {
				t.Fatalf("full transcript row %d = %+v, want settled topic %s with both originals", i, row, settled[i])
			}
		}
		if got := []string{res.Topics[0].ID, res.Topics[1].ID}; !slices.Equal(got, settled[len(settled)-2:]) {
			t.Fatalf("Search returned %v, want the latest topics %v", got, settled[len(settled)-2:])
		}
		assertOmittedTopicJSON(t, res, 3)
	})

	t.Run("failed dream does not remove the read bound", func(t *testing.T) {
		llm := newBoundedContextLLM(t, true, false)
		db := openBoundedContextDB(t, llm.srv.URL, 2, 2)
		sceneID, _ := settleBoundedContextTurns(t, db, 5)
		if _, err := db.Dream(context.Background(), sceneID); err == nil {
			t.Fatal("Dream succeeded over an off-contract consolidation reply")
		}
		res, full := assertBoundedSearch(t, db, sceneID, 2)
		if res.OmittedTopicCount != 3 || len(full.Topics) != 5 {
			t.Fatalf("failed Dream: omitted=%d full=%d, want 3 and 5", res.OmittedTopicCount, len(full.Topics))
		}
	})

	t.Run("slow dream in flight does not remove the read bound", func(t *testing.T) {
		llm := newBoundedContextLLM(t, false, true)
		db := openBoundedContextDB(t, llm.srv.URL, 2, 2)
		sceneID, _ := settleBoundedContextTurns(t, db, 3)
		select {
		case <-llm.started:
		case <-time.After(10 * time.Second):
			t.Fatal("the threshold did not start a Dream")
		}

		type answer struct {
			res *memhop.SearchResult
			err error
		}
		done := make(chan answer, 1)
		go func() {
			res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
			done <- answer{res: res, err: err}
		}()
		close(llm.release)
		var got answer
		select {
		case got = <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Search did not return after the in-flight Dream finished")
		}
		if got.err != nil {
			t.Fatalf("Search: %v", got.err)
		}
		full, err := db.SceneContext(sceneID)
		if err != nil {
			t.Fatalf("SceneContext: %v", err)
		}
		assertBoundedResult(t, got.res, surfaceTopicIDs(full.Topics), 2)
	})

	t.Run("positive threshold at the topic count omits none", func(t *testing.T) {
		llm := newMockLLM(t)
		db := openBoundedContextDB(t, llm.srv.URL, 3, 2)
		sceneID, settled := settleBoundedContextTurns(t, db, 3)
		res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Topics) != len(settled) || res.OmittedTopicCount != 0 {
			t.Fatalf("at-threshold Search returned %d topics and omitted %d, want %d and 0",
				len(res.Topics), res.OmittedTopicCount, len(settled))
		}
	})

	t.Run("negative threshold is explicitly unbounded", func(t *testing.T) {
		llm := newMockLLM(t)
		db := openBoundedContextDB(t, llm.srv.URL, -1, 2)
		sceneID, settled := settleBoundedContextTurns(t, db, 5)
		res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Topics) != len(settled) || res.OmittedTopicCount != 0 {
			t.Fatalf("negative threshold returned %d topics and omitted %d, want %d and 0",
				len(res.Topics), res.OmittedTopicCount, len(settled))
		}
		assertOmittedTopicJSON(t, res, 0)
	})
}

func openBoundedContextDB(t *testing.T, llmURL string, threshold, compressMin int) *testDB {
	t.Helper()
	return newTestDB(t, openMockDB(t, filepath.Join(t.TempDir(), "bounded.meh"), llmURL,
		func(d *memhop.MemHopDefaults) {
			d.SceneDreamTopicThreshold = threshold
			d.DreamCompressMinTopics = compressMin
		}))
}

func settleBoundedContextTurns(t *testing.T, db *testDB, count int) (string, []string) {
	t.Helper()
	base := time.Now().Add(-time.Hour).UnixMilli()
	sceneID := ""
	settled := make([]string, 0, count)
	for i := 0; i < count; i++ {
		res, err := db.Search(memhop.SearchQuery{NewScene: i == 0})
		if err != nil {
			t.Fatalf("Search %d: %v", i, err)
		}
		sceneID = res.Scene.SceneID
		topic, err := db.Update(memhop.TurnEnd{
			Input: fmt.Sprintf("question-%02d", i), Output: fmt.Sprintf("answer-%02d", i),
			CreatedAt: base + int64(i)*1000,
		})
		if err != nil {
			t.Fatalf("Update %d: %v", i, err)
		}
		settled = append(settled, topic.ID)
	}
	return sceneID, settled
}

func assertBoundedSearch(t *testing.T, db *testDB, sceneID string, limit int) (*memhop.SearchResult, *memhop.SceneContext) {
	t.Helper()
	full, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	assertBoundedResult(t, res, surfaceTopicIDs(full.Topics), limit)
	return res, full
}

func assertBoundedResult(t *testing.T, res *memhop.SearchResult, surface []string, limit int) {
	t.Helper()
	if len(res.Topics) > limit {
		t.Fatalf("Search returned %d depth-1 topics, want at most %d", len(res.Topics), limit)
	}
	wantOmitted := len(surface) - len(res.Topics)
	if res.OmittedTopicCount != wantOmitted {
		t.Fatalf("OmittedTopicCount=%d, want %d (%d full - %d returned)",
			res.OmittedTopicCount, wantOmitted, len(surface), len(res.Topics))
	}
	start := len(surface) - len(res.Topics)
	for i, topic := range res.Topics {
		if topic.ID != surface[start+i] {
			t.Fatalf("topic %d=%s, want latest surface topic %s", i, topic.ID, surface[start+i])
		}
	}
}

func surfaceTopicIDs(rows []memhop.SceneContextTopic) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Depth == 1 {
			ids = append(ids, row.TopicID)
		}
	}
	return ids
}

func assertOmittedTopicJSON(t *testing.T, res *memhop.SearchResult, want int) {
	t.Helper()
	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal SearchResult: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("Unmarshal SearchResult: %v", err)
	}
	got, ok := wire["omitted_topic_count"]
	if !ok || got != float64(want) {
		t.Fatalf("omitted_topic_count=%v (present=%v), want %d", got, ok, want)
	}
}

type boundedContextLLM struct {
	srv     *httptest.Server
	started chan struct{}
	release chan struct{}
	once    sync.Once
	fail    bool
	block   bool
}

func newBoundedContextLLM(t *testing.T, fail, block bool) *boundedContextLLM {
	t.Helper()
	m := &boundedContextLLM{fail: fail, block: block}
	if block {
		m.started = make(chan struct{})
		m.release = make(chan struct{})
	}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
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
		switch lower := strings.ToLower(sys); {
		case strings.Contains(lower, "meaningful keywords"):
			content = `{"keywords":["bounded","context","test"]}`
		case strings.Contains(lower, "l2 chat memory"):
			if m.fail {
				content = `not-json`
			} else {
				if m.block {
					m.once.Do(func() { close(m.started) })
					<-m.release
					content = `{"l2_groups":[]}`
				} else {
					content = consolidateReply(user)
				}
			}
		case strings.Contains(lower, "l1 associative"):
			content = `{"emotion":{"valence":0.5,"arousal":0.5,"dominance":0.5},"mbti":{"i_e":0,"n_s":0,"t_f":0,"j_p":0},"personality":"steady","per_node":[]}`
		default:
			t.Errorf("boundedContextLLM: unknown system prompt: %.80s", sys)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		})
	}))
	t.Cleanup(func() {
		if m.release != nil {
			select {
			case <-m.release:
			default:
				close(m.release)
			}
		}
		m.srv.Close()
	})
	return m
}
