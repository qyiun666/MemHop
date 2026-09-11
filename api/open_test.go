// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"os"
	"path/filepath"
	"testing"
)

// The facade's own entry, end to end: what the file holds decides whether Open
// succeeds, and both kinds of domain come back as handles rather than as ids.
func TestOpenSettlesThePrimaryAndCreatesSubAgents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facade.meh")
	llm := LlmConfig{APIURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}

	if _, err := Open(path, llm, DefaultMemHopDefaults, nil); err == nil {
		t.Fatal("a new database with no primary profile must be refused")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the refused open left a file behind: %v", err)
	}

	db, err := Open(path, llm, DefaultMemHopDefaults, &ProfileSlot{Name: "Meow", Role: "assistant"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	primary, err := db.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	got, err := primary.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	if got.Name != "Meow" || got.AgentType != AgentTypePrimary {
		t.Fatalf("primary profile = %+v, want Meow stamped as the primary", got)
	}

	// The caller claims primary; the domain it gets is a sub-agent, because that
	// is what creating one under a file means.
	sub, err := db.SubAgent(llm, ProfileSlot{Name: "worker", Role: "helper", AgentType: AgentTypePrimary})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}
	subL0, err := sub.GetL0()
	if err != nil {
		t.Fatalf("sub GetL0: %v", err)
	}
	if subL0.Name != "worker" || subL0.AgentType != AgentTypeSub {
		t.Fatalf("sub profile = %+v, want it stamped as a sub-agent", subL0)
	}

	// Two domains, two memories: an edit in one is not visible in the other, and
	// a host write cannot move a domain between the two identities.
	if err := sub.UpdateL0(&ProfileSlot{Name: "worker", Role: "edited", AgentType: AgentTypePrimary}); err != nil {
		t.Fatalf("sub UpdateL0: %v", err)
	}
	if again, err := primary.GetL0(); err != nil || again.Role != "assistant" {
		t.Fatalf("the primary saw the sub-agent's edit: %+v %v", again, err)
	}
	if again, err := sub.GetL0(); err != nil || again.AgentType != AgentTypeSub || again.Role != "edited" {
		t.Fatalf("UpdateL0 did not keep the host field and the stamped one apart: %+v %v", again, err)
	}

	if db.IsClosed() {
		t.Fatal("an open database reports itself closed")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !db.IsClosed() {
		t.Fatal("a closed database reports itself open")
	}

	reopened, err := Open(path, llm, DefaultMemHopDefaults, &ProfileSlot{Name: "Usurper"})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	p2, err := reopened.Primary()
	if err != nil {
		t.Fatalf("Primary after reopen: %v", err)
	}
	if kept, err := p2.GetL0(); err != nil || kept.Name != "Meow" {
		t.Fatalf("reopen rewrote the primary: %+v %v", kept, err)
	}
	// The sub-agent domain is addressed by name, so a restart finds it again.
	s2, err := reopened.SubAgent(llm, ProfileSlot{Name: "worker"})
	if err != nil {
		t.Fatalf("SubAgent after reopen: %v", err)
	}
	if back, err := s2.GetL0(); err != nil || back.Role != "edited" {
		t.Fatalf("the sub-agent domain did not survive the restart: %+v %v", back, err)
	}
}
