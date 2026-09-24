// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Acceptance item 10 is "open the agent's memory by id". The name door has had that proof
// for a while — a restart finds the same domain and its scene — but the id door was only
// ever checked down to the profile: a host that hands a worker's id to a new process is not
// asking whose name it is, it is asking for the conversation. So this walks the whole path:
// two rounds in the worker's own scene, close the file, open it again, hand back the id, and
// demand the same memory — same scene, same transcript, and the next turn continuing that
// conversation rather than starting a fresh one. The primary domain of the same file is
// measured alongside it, so the case cannot pass by both handles answering the same listing.

package test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceAgentOpensTheDomainsMemoryByID(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "by-id.meh")
	m := openMockDB(t, path, llm.srv.URL)
	primary, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	worker := mustSub(t, m, llm.srv.URL, "alpha")

	// Two settled rounds in one conversation, the way a host drives a worker.
	var firstTopic string
	for i, pair := range [][2]string{{"rust 的所有权", "靠移动语义"}, {"那借用呢", "借用来读"}} {
		res, err := worker.Search(memhop.SearchQuery{NewScene: i == 0})
		if err != nil {
			t.Fatalf("Search %d: %v", i, err)
		}
		if i == 0 {
			firstTopic = res.NewTopicID
		}
		if _, err := turn(worker, pair[0], pair[1]); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	id := worker.AgentID()
	memoryBefore := encode(t, mustVal(worker.SceneContext("")))
	scenesBefore := sceneIDs(mustVal(worker.ListScenes("")))
	primaryBefore := encode(t, mustVal(primary.SceneContext("")))

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := openMockDB(t, path, llm.srv.URL)
	t.Cleanup(func() { _ = reopened.Close() })

	byID, err := reopened.Agent(testLLM(llm.srv.URL), id)
	if err != nil {
		t.Fatalf("Agent(%s): %v", id, err)
	}
	if byID.AgentID() != id {
		t.Fatalf("the id door handed back domain %s, want %s", byID.AgentID(), id)
	}
	if slot := mustVal(byID.GetL0()); slot.Name != "alpha" {
		t.Fatalf("the reopened domain is %+v, want alpha", slot)
	}
	if got := encode(t, mustVal(byID.SceneContext(""))); got != memoryBefore {
		t.Fatalf("the id opened a different memory:\nbefore: %s\nafter:  %s", memoryBefore, got)
	}
	if got := sceneIDs(mustVal(byID.ListScenes(""))); len(got) != len(scenesBefore) || got[0] != scenesBefore[0] {
		t.Fatalf("the listing changed across the id door: %v vs %v", scenesBefore, got)
	}

	// The conversation continues where it stopped: a plain Search keeps the same scene and
	// mints a turn id that is not one of the two already used.
	res, err := byID.Search(memhop.SearchQuery{})
	if err != nil {
		t.Fatalf("Search after reopen: %v", err)
	}
	if res.Scene.SceneID != scenesBefore[0] {
		t.Fatalf("the reopened domain started a fresh scene %s, want %s", res.Scene.SceneID, scenesBefore[0])
	}
	if res.NewTopicID == firstTopic {
		t.Fatalf("the turn counter restarted: %s", res.NewTopicID)
	}
	if _, err := turn(byID, "第三轮", "接着答"); err != nil {
		t.Fatalf("third turn: %v", err)
	}
	if got := encode(t, mustVal(byID.SceneContext(""))); got == memoryBefore {
		t.Fatal("the settled third round changed nothing in the listing, so the read is not this domain's")
	}

	// Distinctness: the file's primary is a different domain and answers differently, so the
	// byte equality above is not two handles on one listing.
	p2 := mustVal(reopened.Primary())
	if got := encode(t, mustVal(p2.SceneContext(""))); got != primaryBefore {
		t.Fatalf("the primary domain's memory moved while only the worker wrote: %s vs %s", primaryBefore, got)
	}
	if _, err := reopened.Agent(testLLM(llm.srv.URL), "ffffffffffffffff"); memhop.CodeOf(err) != memhop.ErrAgentNotFound {
		t.Fatalf("an unknown id answered %v (code %d), want ErrAgentNotFound", err, memhop.CodeOf(err))
	}
}

func sceneIDs(scenes []memhop.SceneSlot) []string {
	out := make([]string, 0, len(scenes))
	for _, s := range scenes {
		out = append(out, s.SceneID)
	}
	return out
}

func encode(tb testing.TB, v any) string {
	tb.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		tb.Fatalf("encode: %v", err)
	}
	return string(raw)
}

// mustVal unwraps a call whose failure means the fixture is broken; a panic reads better in
// the failure log than a chained Fatalf here, and the offline suite uses this shape already.
func mustVal[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
