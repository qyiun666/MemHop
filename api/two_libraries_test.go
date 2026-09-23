// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The shape a host actually deploys: one library per agent, so a recruited worker opens its
// own `.meh` beside the parent's while both are being driven at the same time. Two things
// have to hold and neither is visible from a single-file test: the two files must not be able
// to reach each other's records even through an id that happens to look alike, and the engine
// must not serialize one family behind the other's lock. Run under -race this is the
// concurrency check; run without it, the counts still catch a lost or crossed write.
func TestTwoLibrariesConcurrentlyInOneProcess(t *testing.T) {
	dir := t.TempDir()
	llm := stubLLM()
	t.Cleanup(llm.Close)
	cfg := surfaceLLM(llm.URL)

	parent, err := Open(filepath.Join(dir, "parent.meh"), cfg, DefaultMemHopDefaults,
		surfaceProfile())
	if err != nil {
		t.Fatalf("Open the parent library: %v", err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	worker, err := Open(filepath.Join(dir, "worker.meh"), cfg, DefaultMemHopDefaults,
		&ProfileInput{Name: "worker-primary", Role: "recruited"})
	if err != nil {
		t.Fatalf("Open the worker library: %v", err)
	}
	t.Cleanup(func() { _ = worker.Close() })

	parentSess, err := parent.Primary()
	if err != nil {
		t.Fatalf("parent Primary: %v", err)
	}
	// The second file is a separate family: it may register its own sub-domains without
	// touching the first file's name space.
	workerParent, err := worker.Primary()
	if err != nil {
		t.Fatalf("worker Primary: %v", err)
	}
	workerHelper, err := worker.SubAgent(cfg, ProfileInput{Name: "helper", Role: "sub"})
	if err != nil {
		t.Fatalf("worker SubAgent: %v", err)
	}

	handles := []*Session{parentSess, workerParent, workerHelper}
	const rounds = 6
	var wg sync.WaitGroup
	ids := make([]map[string]bool, len(handles))
	errs := make([]error, len(handles))
	for h := range handles {
		wg.Add(1)
		go func(h int) {
			defer wg.Done()
			sess := handles[h]
			seen := map[string]bool{}
			for i := 0; i < rounds; i++ {
				res, err := sess.Search(SearchQuery{})
				if err != nil {
					errs[h] = err
					return
				}
				seen[res.NewTopicID] = true
				if _, err := sess.AppendArchive(ArchiveInput{Kind: KindUtterance,
					ContentType: ContentText, Role: RoleUser,
					CreatedAt: turnStamp + int64(1000*i), Content: "并发写的一句"}); err != nil {
					errs[h] = err
					return
				}
				if _, err := sess.Update(TurnEnd{Input: "in", Output: "out",
					Outcome: "answered", CreatedAt: turnStamp + int64(1000*i)}); err != nil {
					errs[h] = err
					return
				}
			}
			ids[h] = seen
		}(h)
	}
	wg.Wait()
	for h, err := range errs {
		if err != nil {
			t.Fatalf("handle %d: %v", h, err)
		}
	}
	for h, seen := range ids {
		if len(seen) != rounds {
			t.Fatalf("handle %d minted %d distinct topic ids, want %d", h, len(seen), rounds)
		}
	}
	if strings.Join(keys(ids[0]), ",") == strings.Join(keys(ids[1]), ",") {
		t.Fatal("two different files handed out the same turn ids — the domains are not isolated")
	}

	// Each family reads back exactly its own rounds.
	for h, sess := range handles {
		sc, err := sess.SceneContext("")
		if err != nil {
			t.Fatalf("handle %d SceneContext: %v", h, err)
		}
		if len(sc.Topics) != rounds {
			t.Fatalf("handle %d lists %d rows, want %d", h, len(sc.Topics), rounds)
		}
	}
	st, err := parent.Stats()
	if err != nil {
		t.Fatalf("parent Stats: %v", err)
	}
	ws, err := worker.Stats()
	if err != nil {
		t.Fatalf("worker Stats: %v", err)
	}
	// The worker file carries two domains through the same loop the parent file carried one,
	// so it holds strictly more — and the parent's own total answers as if the worker had
	// never existed: no record of one family was written into the other's file.
	if st.RecordCount == 0 || ws.RecordCount <= st.RecordCount {
		t.Fatalf("the two files reached %d and %d records, want both non-empty and the two-domain file larger",
			st.RecordCount, ws.RecordCount)
	}
	if again, err := parent.Stats(); err != nil || again.RecordCount != st.RecordCount {
		t.Fatalf("the parent file answered %d records after the worker ran, want %d: %+v err %v",
			again.RecordCount, st.RecordCount, again, err)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// How far a domain id reaches is the question a host multiplies: one `.meh` per agent means
// many files, and each file has its own primary — the implicit zero domain, so its id is the
// same 16 zeros everywhere. Inside one file an id addresses exactly one domain, which is
// what `DB.Agent` needs to be unambiguous; across files it is not a key, and the pair
// (file, id) is. Pinning this is cheaper than watching a host build a global map on the id
// alone and quietly merge two agents' memories.
func TestAnAgentIDAddressesADomainInsideOneFile(t *testing.T) {
	dir := t.TempDir()
	llm := stubLLM()
	t.Cleanup(llm.Close)
	cfg := surfaceLLM(llm.URL)

	first, err := Open(filepath.Join(dir, "first.meh"), cfg, DefaultMemHopDefaults,
		&ProfileInput{Name: "first-primary", Role: "assistant"})
	if err != nil {
		t.Fatalf("Open the first library: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := Open(filepath.Join(dir, "second.meh"), cfg, DefaultMemHopDefaults,
		&ProfileInput{Name: "second-primary", Role: "assistant"})
	if err != nil {
		t.Fatalf("Open the second library: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	firstPrimary, err := first.Primary()
	if err != nil {
		t.Fatalf("first Primary: %v", err)
	}
	secondPrimary, err := second.Primary()
	if err != nil {
		t.Fatalf("second Primary: %v", err)
	}
	if firstPrimary.AgentID() != secondPrimary.AgentID() {
		t.Fatalf("the two primaries reported %q and %q, want the same id: both are the zero domain of their own file",
			firstPrimary.AgentID(), secondPrimary.AgentID())
	}

	// The same string therefore has to be answered by each file for itself.
	byID, err := second.Agent(cfg, secondPrimary.AgentID())
	if err != nil {
		t.Fatalf("second Agent by the primary id: %v", err)
	}
	slot, err := byID.GetL0()
	if err != nil {
		t.Fatalf("GetL0 through the id door: %v", err)
	}
	if slot.Name != "second-primary" {
		t.Fatalf("the id reached %q in the second file, want its own primary", slot.Name)
	}

	// Inside one file, a sub-agent's id differs from the primary's, so a host's map of that
	// file has no collisions to design around.
	sub, err := first.SubAgent(cfg, ProfileInput{Name: "worker", Role: "helper"})
	if err != nil {
		t.Fatalf("first SubAgent: %v", err)
	}
	if sub.AgentID() == firstPrimary.AgentID() {
		t.Fatalf("a sub-agent shares the primary's id %q, so the id would not name a domain", firstPrimary.AgentID())
	}
	if _, err := first.Agent(cfg, sub.AgentID()); err != nil {
		t.Fatalf("Agent by the sub-agent id: %v", err)
	}
}
