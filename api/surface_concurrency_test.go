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

func turnEnd() TurnEnd {
	return TurnEnd{Input: "asked", Output: "answered", Outcome: "answered",
		CreatedAt: time.Now().UnixMilli()}
}
