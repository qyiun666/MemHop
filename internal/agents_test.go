// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Multi-agent lifecycle tests: registry stability across restarts and domain
// isolation at identical idHashes.

package internal

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func openMultiTestDB(t *testing.T, path string) *DB {
	t.Helper()
	cfg := &MemHopConfig{
		DBPath:   path,
		Defaults: DefaultMemHopDefaults,
	}
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return db
}

// TestAgentRegistryStableAcrossRestart CreateAgent hands out the same ID
// for the same name after a restart (rebuilt from on-file registry
// records), and different names never collide.
func TestAgentRegistryStableAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.meh")
	db := openMultiTestDB(t, path)
	alice, err := db.CreateAgent("alice")
	if err != nil {
		t.Fatalf("CreateAgent alice: %v", err)
	}
	bob, err := db.CreateAgent("bob")
	if err != nil {
		t.Fatalf("CreateAgent bob: %v", err)
	}
	if alice == bob || alice == core.DefaultAgentID || bob == core.DefaultAgentID {
		t.Fatalf("agent IDs must be distinct and non-default: %d %d", alice, bob)
	}
	if again, err := db.CreateAgent("alice"); err != nil || again != alice {
		t.Fatalf("CreateAgent alice again: id=%d err=%v, want %d", again, err, alice)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2 := openMultiTestDB(t, path)
	t.Cleanup(func() { _ = db2.Close() })
	if again, err := db2.CreateAgent("alice"); err != nil || again != alice {
		t.Fatalf("after restart CreateAgent alice: id=%d err=%v, want %d", again, err, alice)
	}
	agents, err := db2.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("ListAgents = %d agents, want 2", len(agents))
	}
}

// TestAgentDomainIsolation two agents writing the same idHash into one
// shared file never see each other's records.
func TestAgentDomainIsolation(t *testing.T) {
	db := openMultiTestDB(t, filepath.Join(t.TempDir(), "isolation.meh"))
	t.Cleanup(func() { _ = db.Close() })
	a, err := db.CreateAgent("iso-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateAgent("iso-b")
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

	// Same trajectory session id in both domains: events stay per-agent.
	session := common.FormatHash(common.HashID("shared-session"))
	if err := db.AppendArchive(a, session, core.ArchiveSlot{Kind: core.KindEvent, EventType: "tool_call", Content: "a", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendArchive(b, session, core.ArchiveSlot{Kind: core.KindEvent, EventType: "tool_call", Content: "b1", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendArchive(b, session, core.ArchiveSlot{Kind: core.KindEvent, EventType: "tool_call", Content: "b2", CreatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	ea, err := db.eventsOf(a, session)
	if err != nil || len(ea) != 1 || ea[0].Content != "a" {
		t.Fatalf("events of a = %+v err=%v, want 1 event 'a'", ea, err)
	}
	eb, err := db.eventsOf(b, session)
	if err != nil || len(eb) != 2 {
		t.Fatalf("events of b = %+v err=%v, want 2 events", eb, err)
	}
	if eb[0].Seq != core.LastUtteranceSeq+1 || eb[1].Seq != core.LastUtteranceSeq+2 {
		t.Errorf("b Seq allocation leaked across domains: %d %d", eb[0].Seq, eb[1].Seq)
	}
}
