// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Multi-agent lifecycle tests: registry stability across restarts and domain
// isolation at identical idHashes.

package internal

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func openPrimaryTestDB(t *testing.T, path string) *DB {
	t.Helper()
	db, err := OpenDB(path, testLLMConfig(), DefaultMemHopDefaults, primaryProfile("primary"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	return db
}

// A name is a domain's address, so it resolves to the same domain after a
// restart — the mapping is rebuilt from the on-file registry records — and two
// names never land in one place. Ids no longer cross the boundary, so sameness is
// shown by what the domain holds.
func TestSubAgentNameResolvesToTheSameDomainAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.meh")
	llm := testLLMConfig()
	db := openPrimaryTestDB(t, path)
	alice, err := db.SubAgent(llm, core.ProfileSlot{Name: "alice", Role: "first"})
	if err != nil {
		t.Fatalf("SubAgent alice: %v", err)
	}
	bob, err := db.SubAgent(llm, core.ProfileSlot{Name: "bob"})
	if err != nil {
		t.Fatalf("SubAgent bob: %v", err)
	}
	if err := alice.UpdateL0(&ProfileSlot{Name: "alice", Role: "edited"}); err != nil {
		t.Fatalf("alice UpdateL0: %v", err)
	}
	if err := bob.UpdateL0(&ProfileSlot{Name: "bob", Role: "other"}); err != nil {
		t.Fatalf("bob UpdateL0: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2 := openPrimaryTestDB(t, path)
	t.Cleanup(func() { _ = db2.Close() })
	back, err := db2.SubAgent(llm, core.ProfileSlot{Name: "alice"})
	if err != nil {
		t.Fatalf("SubAgent alice after restart: %v", err)
	}
	if got, err := back.GetL0(); err != nil || got.Role != "edited" {
		t.Fatalf("the name did not resolve to the domain it did before: %+v err=%v", got, err)
	}
	// A name nobody registered is a fresh, empty domain rather than an error or
	// somebody else's memory.
	fresh, err := db2.SubAgent(llm, core.ProfileSlot{Name: "carol"})
	if err != nil {
		t.Fatalf("SubAgent carol: %v", err)
	}
	if got, err := fresh.GetL0(); err != nil || got.Name != "carol" || got.Role == "edited" {
		t.Fatalf("carol did not get a domain of her own: %+v err=%v", got, err)
	}
}

// TestAgentDomainIsolation two agents writing the same idHash into one
// shared file never see each other's records.
func TestAgentDomainIsolation(t *testing.T) {
	db := openPrimaryTestDB(t, filepath.Join(t.TempDir(), "isolation.meh"))
	t.Cleanup(func() { _ = db.Close() })
	a, err := db.ensureRegistered("iso-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.ensureRegistered("iso-b")
	if err != nil {
		t.Fatal(err)
	}

	// Same profile slot identity in both domains: each sees only its own.
	if err := db.UpdateL0(a, &core.ProfileSlot{Name: "agent-a"}); err != nil {
		t.Fatalf("UpdateL0 a: %v", err)
	}
	if err := db.UpdateL0(b, &core.ProfileSlot{Name: "agent-b"}); err != nil {
		t.Fatalf("UpdateL0 b: %v", err)
	}
	pa, err := db.GetL0(a)
	if err != nil || pa == nil || pa.Name != "agent-a" {
		t.Fatalf("GetL0(a) = %+v err=%v, want agent-a", pa, err)
	}
	pb, err := db.GetL0(b)
	if err != nil || pb == nil || pb.Name != "agent-b" {
		t.Fatalf("GetL0(b) = %+v err=%v, want agent-b", pb, err)
	}

	// One open turn per domain: each domain's own scene mints its own topic id and
	// keeps it, so writing to a after b opened a later turn still lands on a's turn —
	// and the events under them stay per-agent. A host names neither id on the way in.
	_, turnA, _ := newTurnKeyFor(t, db, a)
	_, turnB, _ := newTurnKeyFor(t, db, b)
	if _, err := db.AppendArchive(a, core.ArchiveSlot{Kind: core.KindEvent, EventType: "tool_call", Content: "a", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendArchive(b, core.ArchiveSlot{Kind: core.KindEvent, EventType: "tool_call", Content: "b1", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendArchive(b, core.ArchiveSlot{Kind: core.KindEvent, EventType: "tool_call", Content: "b2", CreatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	ea, err := db.eventsOf(a, turnA)
	if err != nil || len(ea) != 1 || ea[0].Content != "a" {
		t.Fatalf("events of a = %+v err=%v, want 1 event 'a'", ea, err)
	}
	eb, err := db.eventsOf(b, turnB)
	if err != nil || len(eb) != 2 {
		t.Fatalf("events of b = %+v err=%v, want 2 events", eb, err)
	}
	if eb[0].Seq != core.LastUtteranceSeq+1 || eb[1].Seq != core.LastUtteranceSeq+2 {
		t.Errorf("b Seq allocation leaked across domains: %d %d", eb[0].Seq, eb[1].Seq)
	}
}
