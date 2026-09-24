// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The concurrency contract is the one an integrating host cannot work around: calls on one
// domain serialize, calls on different domains do not, and the turn id a `Search` mints is
// load-bearing — it comes from the scene's counter, so a counter that advanced twice for one
// open, or a read that wrote without the lock, shows up as two turns minted with the same id
// and one settled round quietly overwriting another's records. None of that was being tested
// at the facade: the offline suite runs one caller per domain, and the seven `go func` sites
// in the repo sit deep inside the engine. This drives many goroutines through the public
// surface and asserts the outcomes a host can actually observe. Run it under -race.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A host spawning workers gives each its own domain: calls on different domains run in
// parallel and touch the file under the engine's own rules, while a domain carries exactly
// one open turn. This drives that pattern through the public surface and checks what the
// host then sees — every worker's rounds all present on its own scene, nothing visible
// across domains, and no turn id handed out twice. Run it under -race.
func TestConcurrentWorkersOnTheirOwnDomains(t *testing.T) {
	m, _, stubURL := openSurfaceLibrary(t)
	defer func() { _ = m.Close() }()

	const workers = 6
	const rounds = 4

	sessions := make([]*Session, 0, workers)
	for i := 0; i < workers; i++ {
		sess, err := m.SubAgent(surfaceLLM(stubURL), ProfileInput{Name: fmt.Sprintf("worker-%d", i)})
		if err != nil {
			t.Fatalf("SubAgent %d: %v", i, err)
		}
		sessions = append(sessions, sess)
	}

	var wg sync.WaitGroup
	failures := make([]string, workers)
	for i, sess := range sessions {
		wg.Add(1)
		go func(worker int, sess *Session) {
			defer wg.Done()
			seen := map[string]bool{}
			for r := 0; r < rounds; r++ {
				res, err := sess.Search(SearchQuery{})
				if err != nil {
					failures[worker] = fmt.Sprintf("search: %v", err)
					return
				}
				if seen[res.NewTopicID] {
					failures[worker] = fmt.Sprintf("turn id %s handed out twice", res.NewTopicID)
					return
				}
				seen[res.NewTopicID] = true
				if _, err := sess.AppendArchive(ArchiveInput{
					Kind: KindEvent, ContentType: ContentText, EventType: "tool_call",
					Content: "looked something up", CreatedAt: time.Now().UnixMilli(),
				}); err != nil {
					failures[worker] = fmt.Sprintf("append: %v", err)
					return
				}
				if _, err := sess.Update(TurnEnd{Input: "asked", Output: "answered",
					Outcome: "answered", CreatedAt: time.Now().UnixMilli()}); err != nil {
					failures[worker] = fmt.Sprintf("update: %v", err)
					return
				}
			}
			// The worker's own scene has to show everything it settled, with its own rows
			// addressed by the ids it was handed.
			ctx, err := sess.SceneContext("")
			if err != nil {
				failures[worker] = fmt.Sprintf("scene context: %v", err)
				return
			}
			if len(ctx.Topics) != rounds {
				failures[worker] = fmt.Sprintf("this domain lists %d turns, want %d", len(ctx.Topics), rounds)
				return
			}
			for _, row := range ctx.Topics {
				if !seen[row.TopicID] {
					failures[worker] = fmt.Sprintf("the listing carries a turn this worker never opened: %s", row.TopicID)
					return
				}
				id := row.TopicID
				// Two originals from the close, the event appended mid-round, and the
				// turn_outcome the same close recorded: four rows, none of them another
				// worker's.
				if hits, err := sess.SearchL4(L4Query{TopicID: &id}); err != nil || len(hits) != 4 {
					failures[worker] = fmt.Sprintf("turn %s owns %d records (err %v), want 4", id, len(hits), err)
					return
				}
			}
		}(i, sess)
	}
	wg.Wait()
	for i, failure := range failures {
		if failure != "" {
			t.Errorf("worker %d: %s", i, failure)
		}
	}
}

// One handle shared across goroutines is still safe on the file — the domain lock
// serialises it — and a read raced with a background consolidation must answer either the
// before or the after state, never a half-built one.
func TestConcurrentReadsDuringWrites(t *testing.T) {
	m, sess, _ := openSurfaceLibrary(t)
	defer func() { _ = m.Close() }()

	for i := 0; i < 12; i++ {
		if _, err := sess.Search(SearchQuery{NewScene: i == 0}); err != nil {
			t.Fatalf("Search %d: %v", i, err)
		}
		if _, err := sess.Update(TurnEnd{Input: "asked", Output: "answered",
			Outcome: "answered", CreatedAt: time.Now().UnixMilli()}); err != nil {
			t.Fatalf("Update %d: %v", i, err)
		}
	}
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		return string(raw)
	}
	baseline, err := sess.SceneContext("")
	if err != nil {
		t.Fatalf("baseline read: %v", err)
	}
	known := encode(baseline)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ctx, err := sess.SceneContext("")
				if err != nil {
					t.Errorf("concurrent read: %v", err)
					return
				}
				if len(ctx.Topics) == 0 {
					t.Error("a concurrent read saw an empty scene while it held 12 turns")
					return
				}
			}
		}()
	}
	// Consolidate the same scene the readers are pulling, then settle another round.
	if _, err := sess.Dream(context.Background(), ""); err != nil {
		t.Errorf("Dream during concurrent reads: %v", err)
	}
	if _, err := sess.Search(SearchQuery{}); err != nil {
		t.Errorf("Search during concurrent reads: %v", err)
	}
	if _, err := sess.Update(TurnEnd{Input: "late", Output: "later",
		Outcome: "answered", CreatedAt: time.Now().UnixMilli()}); err != nil {
		t.Errorf("Update during concurrent reads: %v", err)
	}
	close(stop)
	wg.Wait()

	current, err := sess.SceneContext("")
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if encode(current) == known {
		t.Fatalf("the scene answered the same after a Dream and a new round as before it:\n%s", known)
	}
}

// The other shape a spawning host hits is one name asked for twice at once: the model decided to
// start a worker another goroutine is already starting, or a call was retried while the first was
// in flight. Two domains under one name is amnesia with no error to read — the roster would list
// the name once, `SubAgent` would resolve it to whichever id the registry scan happened to keep,
// and each half of what that worker remembers would sit where nothing points. Registration is
// serialised on `agentsMu` and the profile is seeded under the domain lock, so this shape has
// outcomes worth asserting: one id per name inside the process, one roster entry per name on the
// disk, and a domain that still closes a round afterwards. Run it under -race.
func TestSameNameAskedForAtOnceOpensOneDomain(t *testing.T) {
	llm := stubLLM()
	t.Cleanup(llm.Close)
	path := filepath.Join(t.TempDir(), "spawn.meh")
	m, err := Open(path, surfaceLLM(llm.URL), DefaultMemHopDefaults, surfaceProfile())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const callers = 8
	names := []string{"shared-a", "shared-b"}
	ids := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sess, err := m.SubAgent(surfaceLLM(llm.URL), ProfileInput{Name: names[n%len(names)]})
			if err != nil {
				errs[n] = err
				return
			}
			ids[n] = sess.AgentID()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("SubAgent(%s), caller %d: %v", names[i%len(names)], i, err)
		}
	}
	opened := map[string][]string{}
	for i, id := range ids {
		opened[names[i%len(names)]] = append(opened[names[i%len(names)]], id)
	}
	for _, name := range names {
		distinct := map[string]bool{}
		for _, id := range opened[name] {
			distinct[id] = true
		}
		if len(distinct) != 1 {
			t.Fatalf("%q opened as %d different domains in one process: %v", name, len(distinct), opened[name])
		}
	}

	// The same answers have to come off the disk: a second registry record is precisely what a
	// restart hands back as a second domain, and this process's maps are gone by then.
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	m2, err := Open(path, surfaceLLM(llm.URL), DefaultMemHopDefaults, surfaceProfile())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = m2.Close() }()
	roster, err := m2.Agents()
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	seen := map[string]int{}
	for _, a := range roster {
		seen[a.Name]++
	}
	for _, name := range names {
		if seen[name] != 1 {
			t.Fatalf("the reopened roster names %q %d times, want one domain per name: %+v", name, seen[name], roster)
		}
	}

	// Asking by name again resolves to the domain that was opened, and that domain still runs the
	// loop: the storm of admissions left one profile and a usable turn counter behind.
	sess, err := m2.SubAgent(surfaceLLM(llm.URL), ProfileInput{Name: "shared-a"})
	if err != nil {
		t.Fatalf("SubAgent by name after restart: %v", err)
	}
	if sess.AgentID() != opened["shared-a"][0] {
		t.Fatalf("%q reopened as %s, the same process had opened it as %s",
			"shared-a", sess.AgentID(), opened["shared-a"][0])
	}
	if prof, err := sess.GetL0(); err != nil || prof.Name != "shared-a" {
		t.Fatalf("the domain's own profile says %+v (err %v), want the name it was opened by", prof, err)
	}
	if _, err := sess.Search(SearchQuery{}); err != nil {
		t.Fatalf("Search on the restarted domain: %v", err)
	}
	if _, err := sess.Update(turnEnd()); err != nil {
		t.Fatalf("Update on the restarted domain: %v", err)
	}
}

func turnEnd() TurnEnd {
	return TurnEnd{Input: "asked", Output: "answered", Outcome: "answered",
		CreatedAt: time.Now().UnixMilli()}
}
