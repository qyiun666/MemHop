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
		*DefaultMemHopDefaults, primaryProfile("primary"))
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

	defaults := *DefaultMemHopDefaults
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

// Registration and the profile are two writes, so a crash between them leaves a
// domain that is registered but has no identity. Asking for the same name again
// finishes the job rather than leaving it that way.
func TestSubAgentHealsADomainLeftWithoutAProfile(t *testing.T) {
	primarySrv, _ := countingLLMServer(t, turnKeywords)
	db := openPrimaryOn(t, t.TempDir(), primarySrv.URL)

	// Only the first half: a registry record, no profile.
	id, err := db.CreateAgent("worker")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	agents, err := db.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("a refused SubAgent left %d domains behind: %+v", len(agents), agents)
	}
}
