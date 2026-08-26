// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Multi-agent lifecycle tests: registry stability across restarts, domain
// isolation at identical idHashes, and full-domain deletion.

package internal

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func openMultiTestDB(t *testing.T, path string) *DB {
	t.Helper()
	cfg := &MemHopConfig{
		DBPath:    path,
		VectorDim: 768,
		Defaults:  *DefaultMemHopDefaults,
	}
	db, err := Open(cfg, &mockEncoder{vec: testVec})
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
	if err := db.AppendTrajectory(a, session, core.TrajectorySlot{EventType: "tool_call", Payload: "a", Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendTrajectory(b, session, core.TrajectorySlot{EventType: "tool_call", Payload: "b1", Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendTrajectory(b, session, core.TrajectorySlot{EventType: "tool_call", Payload: "b2", Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	ea, err := db.ReadTrajectory(a, session)
	if err != nil || len(ea) != 1 || ea[0].Payload != "a" {
		t.Fatalf("ReadTrajectory(a) = %+v err=%v, want 1 event 'a'", ea, err)
	}
	eb, err := db.ReadTrajectory(b, session)
	if err != nil || len(eb) != 2 {
		t.Fatalf("ReadTrajectory(b) = %+v err=%v, want 2 events", eb, err)
	}
	if eb[0].Seq != 1 || eb[1].Seq != 2 {
		t.Errorf("b Seq allocation leaked across domains: %d %d", eb[0].Seq, eb[1].Seq)
	}
}

// TestDeleteAgent removes every record of the domain and the tenant
// mapping; the name can be re-registered afterwards with a fresh ID.
func TestDeleteAgent(t *testing.T) {
	db := openMultiTestDB(t, filepath.Join(t.TempDir(), "delete.meh"))
	t.Cleanup(func() { _ = db.Close() })
	a, err := db.CreateAgent("victim")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateL0(a, &core.ProfileSlot{Name: "victim"}); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteAgent(core.DefaultAgentID); err == nil {
		t.Fatal("DeleteAgent must reject the default domain")
	}
	if err := db.DeleteAgent(a); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	agents, err := db.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 0 {
		t.Fatalf("ListAgents after delete = %+v, want empty", agents)
	}
	// The domain's records are gone: fresh profile in the re-created domain.
	a2, err := db.CreateAgent("victim")
	if err != nil {
		t.Fatal(err)
	}
	if a2 == a {
		t.Fatal("re-registered tenant must get a fresh agentID")
	}
	if p, err := db.GetL0(a2); err != nil || (p != nil && p.Name == "victim") {
		t.Fatalf("deleted domain leaked into re-registration: %+v err=%v", p, err)
	}
}

// TestDeleteAgentUnderConcurrency races domain operations against
// DeleteAgent: every op either completes before the delete or fails with
// ErrAgentNotFound, and a stale handle never revives the deleted domain.
func TestDeleteAgentUnderConcurrency(t *testing.T) {
	db := openMultiTestDB(t, filepath.Join(t.TempDir(), "delcon.meh"))
	t.Cleanup(func() { _ = db.Close() })
	a, err := db.CreateAgent("busy")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				if err := db.UpdateL0(a, &core.ProfileSlot{Name: "busy"}); err != nil {
					if common.CodeOf(err) != common.ErrAgentNotFound {
						t.Errorf("UpdateL0 racing DeleteAgent: %v", err)
					}
					return
				}
			}
		}()
	}
	time.Sleep(5 * time.Millisecond) // let the writers ramp up
	if err := db.DeleteAgent(a); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	wg.Wait()

	// No orphan records: the engine's agent index must not contain the domain.
	for id := range db.engine.IterAgents() {
		if id == a {
			t.Fatal("orphan records survived DeleteAgent")
		}
	}

	if err := db.UpdateL0(a, &core.ProfileSlot{Name: "zombie"}); common.CodeOf(err) != common.ErrAgentNotFound {
		t.Fatalf("deleted domain revived on write: err=%v", err)
	}
	if _, err := db.GetL0(a); common.CodeOf(err) != common.ErrAgentNotFound {
		t.Fatalf("deleted domain revived on read: err=%v", err)
	}
}
