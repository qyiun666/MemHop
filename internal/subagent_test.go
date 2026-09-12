// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Sub-agent domains: addressed by name, each with its own LLM endpoint. The
// endpoint tests are the ones that matter — a domain whose override silently fell
// back to the library-wide transport would still work, and would only be wrong in
// which model answered.

package internal

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// runTurn drives one full turn on a domain-bound handle: open it, record both
// originals, settle. Settling is the one call that reaches the LLM, so a counting
// stub sees exactly one hit per turn.
func runTurn(t *testing.T, sess *Session) {
	t.Helper()
	res, err := sess.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	topicID := common.FormatHash(res.NewTopicID)
	utterances := []core.ArchiveSlot{
		{Kind: core.KindUtterance, Seq: core.SeqUser, Role: core.RoleUser, Content: "跑一下测试", CreatedAt: 1000},
		{Kind: core.KindUtterance, Seq: core.SeqAgent, Role: core.RoleAgent, Content: "全绿", CreatedAt: 2000},
	}
	for _, slot := range utterances {
		if err := sess.AppendArchive(topicID, slot); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := sess.Update(common.FormatHash(res.Scene.SceneID), topicID); err != nil {
		t.Fatalf("update: %v", err)
	}
}

func openPrimaryOn(t *testing.T, dir, endpoint string) *DB {
	t.Helper()
	db, err := OpenDB(filepath.Join(dir, "db.meh"),
		LlmConfig{APIURL: endpoint, APIKey: "test", Model: "mock"},
		DefaultMemHopDefaults, primaryProfile("primary"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// The name is the domain's address, so asking twice is one domain and the profile
// written first is the one that stays; a different name is a different domain.
func TestSubAgentIsIdempotentByName(t *testing.T) {
	primarySrv, _ := countingLLMServer(t, turnKeywords)
	db := openPrimaryOn(t, t.TempDir(), primarySrv.URL)

	sub := LlmConfig{APIURL: primarySrv.URL, APIKey: "test", Model: "mock"}
	first, err := db.SubAgent(sub, core.ProfileSlot{Name: "worker", Role: "first"})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}
	second, err := db.SubAgent(sub, core.ProfileSlot{Name: "worker", Role: "second"})
	if err != nil {
		t.Fatalf("SubAgent again: %v", err)
	}

	got, err := second.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	if got.Role != "first" {
		t.Fatalf("the second call rewrote the profile: %+v", got)
	}
	if got.AgentType != core.AgentTypeSub {
		t.Fatalf("a sub-agent domain must be stamped as one, got %d", got.AgentType)
	}
	// The primary is not addressable by name, so a sub-agent can never land on it.
	primaryL0, err := first.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	other, err := db.SubAgent(sub, core.ProfileSlot{Name: "someone-else"})
	if err != nil {
		t.Fatalf("SubAgent other: %v", err)
	}
	otherL0, err := other.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	if otherL0.Name != "someone-else" || primaryL0.Name != "worker" {
		t.Fatalf("two names must be two domains: %+v vs %+v", primaryL0, otherL0)
	}
}

// A domain's own endpoint has to be the one its turns use. Both stubs count, so
// this fails if the hottest LLM path reaches for the library-wide transport
// instead of the one injected into the domain.
func TestSubAgentRunsOnItsOwnEndpoint(t *testing.T) {
	primarySrv, primaryCalls := countingLLMServer(t, turnKeywords)
	subSrv, subCalls := countingLLMServer(t, turnKeywords)
	db := openPrimaryOn(t, t.TempDir(), primarySrv.URL)

	primary, err := db.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	sub, err := db.SubAgent(LlmConfig{APIURL: subSrv.URL, APIKey: "test", Model: "mock"},
		core.ProfileSlot{Name: "worker"})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}

	runTurn(t, primary)
	runTurn(t, sub)

	if got := primaryCalls.Load(); got != 1 {
		t.Fatalf("the primary endpoint took %d distillations, want 1", got)
	}
	if got := subCalls.Load(); got != 1 {
		t.Fatalf("the sub-agent endpoint took %d distillations, want 1", got)
	}
}

// The override has to outlive the domain context. The idle sweep drops a context
// and the next access rebuilds it, so an override stored on the context would
// quietly fall back to the library-wide endpoint once a domain went idle long
// enough — an hour into a session, with nothing to indicate it.
func TestSubAgentEndpointSurvivesIdleReclaim(t *testing.T) {
	primarySrv, primaryCalls := countingLLMServer(t, turnKeywords)
	subSrv, subCalls := countingLLMServer(t, turnKeywords)

	defaults := DefaultMemHopDefaults
	defaults.AgentIdleTTLMs = 1 // reclaim on the next access after any pause at all
	db, err := OpenDB(filepath.Join(t.TempDir(), "idle.meh"),
		LlmConfig{APIURL: primarySrv.URL, APIKey: "test", Model: "mock"},
		defaults, primaryProfile("primary"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	sub, err := db.SubAgent(LlmConfig{APIURL: subSrv.URL, APIKey: "test", Model: "mock"},
		core.ProfileSlot{Name: "worker"})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}
	runTurn(t, sub)
	if got := subCalls.Load(); got != 1 {
		t.Fatalf("the first turn took %d distillations on the sub endpoint, want 1", got)
	}

	time.Sleep(10 * time.Millisecond) // long enough for the 1ms TTL to have passed
	runTurn(t, sub)

	if got := subCalls.Load(); got != 2 {
		t.Fatalf("after an idle reclaim the sub endpoint took %d, want 2", got)
	}
	if got := primaryCalls.Load(); got != 0 {
		t.Fatalf("the reclaimed domain fell back to the primary endpoint: %d calls", got)
	}
}

// The sweep's decision has to survive the window it cannot prevent: a caller
// stamps its activity, is descheduled past the TTL, and takes a lock on a context
// the table has already dropped — where its writes would land on caches nothing
// reads back. So the removal marks the context inside the same lock hold, and
// lockAgent re-fetches when it finds the mark. This pins the mark and the drop
// landing together; the re-fetch would need a stall staged between two statements
// of one function, which nothing but a seam in the production path can arrange.
func TestIdleReclaimMarksTheDomainItDrops(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	defaults := DefaultMemHopDefaults
	defaults.AgentIdleTTLMs = 1 // idle by the next access after any pause at all
	db, err := OpenDB(filepath.Join(t.TempDir(), "reclaim.meh"),
		LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"},
		defaults, primaryProfile("primary"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	sub, err := db.SubAgent(LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"},
		core.ProfileSlot{Name: "worker"})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}
	dropped, err := db.contextFor(sub.agentID)
	if err != nil {
		t.Fatalf("contextFor: %v", err)
	}

	time.Sleep(10 * time.Millisecond) // long enough for the 1ms TTL to have passed
	rebuilt, err := db.contextFor(sub.agentID)
	if err != nil {
		t.Fatalf("contextFor: %v", err)
	}
	if rebuilt == dropped {
		t.Fatal("the idle domain was not reclaimed, so this test would prove nothing")
	}
	if !dropped.Reclaimed.Load() {
		t.Fatal("the dropped context carries no mark, so a caller still holding it cannot tell")
	}
	if dropped.OpCtx.Err() == nil {
		t.Fatal("the dropped domain's work context was left alive")
	}

	ac, err := db.lockAgent(sub.agentID)
	if err != nil {
		t.Fatalf("lockAgent: %v", err)
	}
	defer ac.Mu.Unlock()
	if ac.Reclaimed.Load() {
		t.Fatal("lockAgent took an operation to a domain the table had dropped")
	}
}

// A host that reconnects names its new endpoint on a domain it is still holding,
// so the replacement has to reach the live context: with the idle sweep disabled
// there is no rebuild left to credit, and a domain still running on the endpoint
// it was created with is the bug this pins.
func TestSubAgentMovesALiveDomainToItsNewEndpoint(t *testing.T) {
	primarySrv, _ := countingLLMServer(t, turnKeywords)
	firstSrv, firstCalls := countingLLMServer(t, turnKeywords)
	secondSrv, secondCalls := countingLLMServer(t, turnKeywords)

	defaults := DefaultMemHopDefaults
	defaults.AgentIdleTTLMs = 0 // never reclaimed: only a replacement can move this domain
	db, err := OpenDB(filepath.Join(t.TempDir(), "swap.meh"),
		LlmConfig{APIURL: primarySrv.URL, APIKey: "test", Model: "mock"},
		defaults, primaryProfile("primary"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	endpoint := func(url string) LlmConfig {
		return LlmConfig{APIURL: url, APIKey: "test", Model: "mock"}
	}
	sub, err := db.SubAgent(endpoint(firstSrv.URL), core.ProfileSlot{Name: "worker"})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}
	runTurn(t, sub)
	if got := firstCalls.Load(); got != 1 {
		t.Fatalf("the first endpoint took %d distillations, want 1", got)
	}

	if _, err := db.SubAgent(endpoint(secondSrv.URL), core.ProfileSlot{Name: "worker"}); err != nil {
		t.Fatalf("SubAgent again: %v", err)
	}
	runTurn(t, sub)

	if got := secondCalls.Load(); got != 1 {
		t.Fatalf("the swapped endpoint took %d distillations, want 1: the live domain still runs on the old one", got)
	}
	if got := firstCalls.Load(); got != 1 {
		t.Fatalf("turns still reach the endpoint the host replaced: %d calls, want only the first turn", got)
	}
}

// Registration and the profile are two writes, so a crash between them leaves a
// domain that is registered but has no identity. Asking for the same name again
// finishes the job rather than leaving it that way.
func TestSubAgentHealsADomainLeftWithoutAProfile(t *testing.T) {
	primarySrv, _ := countingLLMServer(t, turnKeywords)
	db := openPrimaryOn(t, t.TempDir(), primarySrv.URL)

	// Only the first half: a registry record, no profile. This is exactly where
	// the two writes stop if the process dies between them.
	id, err := db.ensureRegistered("worker")
	if err != nil {
		t.Fatalf("ensureRegistered: %v", err)
	}
	if has, err := repo.HasProfileL0(db.engine, id); err != nil || has {
		t.Fatalf("the half-created domain should hold no profile: has=%v err=%v", has, err)
	}

	sess, err := db.SubAgent(LlmConfig{APIURL: primarySrv.URL, APIKey: "test", Model: "mock"},
		core.ProfileSlot{Name: "worker", Role: "helper"})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}
	got, err := sess.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	if got.Name != "worker" || got.Role != "helper" || got.AgentType != core.AgentTypeSub {
		t.Fatalf("the healed profile = %+v", got)
	}
}

// A name is a tenant key stored in the file, so it is capped; the cap is about
// record size, not about which characters a host may use.
func TestSubAgentRefusesAnUnusableName(t *testing.T) {
	primarySrv, _ := countingLLMServer(t, turnKeywords)
	db := openPrimaryOn(t, t.TempDir(), primarySrv.URL)
	sub := LlmConfig{APIURL: primarySrv.URL, APIKey: "test", Model: "mock"}

	if _, err := db.SubAgent(sub, core.ProfileSlot{Name: "   "}); err == nil {
		t.Fatal("a blank name must be refused")
	}
	if _, err := db.SubAgent(LlmConfig{APIURL: "http://x"}, core.ProfileSlot{Name: "worker"}); err == nil {
		t.Fatal("a half-specified endpoint must be refused before any domain is created")
	}
	long := make([]byte, maxSubAgentNameBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := db.SubAgent(sub, core.ProfileSlot{Name: string(long)}); err == nil {
		t.Fatal("a name past the cap must be refused")
	}
	listed, unresolved := repo.ListAgentRegistry(db.engine)
	if unresolved != nil {
		t.Fatalf("a registry with no records has nothing unresolved: %v", unresolved)
	}
	if len(listed) != 0 {
		t.Fatalf("a refused SubAgent left %d domains on disk: %+v", len(listed), listed)
	}
}

// A tenant key that will not read back is still a domain, and the name it carried is
// exactly what cannot be recovered. So while one is pending no name can be proven
// free: creating a tenant would hand the host an empty domain under a name a real
// domain already holds, and the memory behind the unreadable key becomes
// unreachable — not listable, not deletable, not reopenable by name.
func TestSubAgentRefusedWhileATenantKeyWillNotResolve(t *testing.T) {
	primarySrv, _ := countingLLMServer(t, turnKeywords)
	db := openPrimaryOn(t, t.TempDir(), primarySrv.URL)
	sub := LlmConfig{APIURL: primarySrv.URL, APIKey: "test", Model: "mock"}

	worker, err := db.SubAgent(sub, core.ProfileSlot{Name: "worker", Role: "keeper"})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}
	workerID := db.nameToID["worker"]
	runTurn(t, worker)
	if _, err := db.engine.WriteRecord(workerID, core.RecAgentRegistry, workerID, []byte(`{"ke`)); err != nil {
		t.Fatalf("damage the tenant key: %v", err)
	}

	if _, err := db.SubAgent(sub, core.ProfileSlot{Name: "other"}); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("a new name must not be handed out while a key is unresolved, got %v", err)
	}
	listed, unresolved := repo.ListAgentRegistry(db.engine)
	if len(listed) != 0 {
		t.Fatalf("the refusal created a domain: %+v", listed)
	}
	if unresolved == nil {
		t.Fatal("the damaged domain must stay reported until it reads back")
	}

	// The refusal is scoped to creating, not to using: every name that resolved
	// still reaches the domain it belongs to, with its memory intact.
	again, err := db.SubAgent(sub, core.ProfileSlot{Name: "worker"})
	if err != nil {
		t.Fatalf("an already-resolved name must still work: %v", err)
	}
	if again.agentID != workerID {
		t.Fatalf("the resolved name moved to domain %d, want %d", again.agentID, workerID)
	}
	if got, err := again.GetL0(); err != nil || got.Role != "keeper" {
		t.Fatalf("the worker domain reads %+v/%v, want the profile it was created with", got, err)
	}
}
