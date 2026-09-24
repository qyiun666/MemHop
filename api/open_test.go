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

	db, err := Open(path, llm, DefaultMemHopDefaults, &ProfileInput{Name: "Meow", Role: "assistant"})
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

	// Creating a domain under a file makes it a sub-agent; the argument carries no
	// field that could claim otherwise.
	sub, err := db.SubAgent(llm, ProfileInput{Name: "worker", Role: "helper"})
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
	if err := sub.UpdateL0(ProfileInput{Name: "worker", Role: "edited"}); err != nil {
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

	reopened, err := Open(path, llm, DefaultMemHopDefaults, &ProfileInput{Name: "Usurper"})
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
	s2, err := reopened.SubAgent(llm, ProfileInput{Name: "worker"})
	if err != nil {
		t.Fatalf("SubAgent after reopen: %v", err)
	}
	if back, err := s2.GetL0(); err != nil || back.Role != "edited" {
		t.Fatalf("the sub-agent domain did not survive the restart: %+v %v", back, err)
	}
}

// One file, one holder — and the lock does not soften inside a single process: a second
// `Open` of a file this process already holds is refused exactly as another process's would
// be. That is the failure a host meets when it hands two agents one path (a worker's file
// path came out of a model), and it reads as "pick another path", not as damage: the holder
// keeps working untouched, and a second path opens fine alongside it. That is how "one
// library per agent" grows at runtime rather than only at start-up.
func TestOpenRefusesAFileThisProcessAlreadyHolds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "held.meh")
	llm := LlmConfig{APIURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}
	profile := &ProfileInput{Name: "Meow", Role: "assistant"}

	db, err := Open(path, llm, DefaultMemHopDefaults, profile)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	second, err := Open(path, llm, DefaultMemHopDefaults, profile)
	if err == nil {
		_ = second.Close()
		t.Fatal("a second Open of a file this process holds must be refused")
	}
	if code := CodeOf(err); code != ErrIO {
		t.Fatalf("the refusal carries code %d (%v), want ErrIO", code, err)
	}

	sess, err := db.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if _, err := sess.Search(SearchQuery{}); err != nil {
		t.Fatalf("the held database's own read after the refused open: %v", err)
	}

	worker, err := Open(filepath.Join(dir, "worker.meh"), llm, DefaultMemHopDefaults, profile)
	if err != nil {
		t.Fatalf("a second file while the first is held: %v", err)
	}
	if err := worker.Close(); err != nil {
		t.Fatalf("close the worker file: %v", err)
	}
}

// The L3 pool is per file, not per domain, and that is the boundary a host decides on when
// it spawns a worker: a second *domain* of the same file inherits the project knowledge with
// no re-import, while a second *file* starts with an empty pool of its own. So "one library
// per agent" carries the graph along only in the sub-agent shape — a worker on its own path
// brings nothing over, and that is the library's rule rather than a bug it can be talked out
// of (`TestKnowledgeGraphStaysInsideItsFile` exists so the two shapes cannot drift).
func TestKnowledgeGraphStaysInsideItsFile(t *testing.T) {
	dir := t.TempDir()
	llm := LlmConfig{APIURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}
	profile := &ProfileInput{Name: "Meow", Role: "assistant"}

	db, err := Open(filepath.Join(dir, "parent.meh"), llm, DefaultMemHopDefaults, profile)
	if err != nil {
		t.Fatalf("Open the parent file: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	parent, err := db.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if _, err := parent.ImportL3([]L3ImportItem{
		{Title: "auth", Domain: "proj", NodeType: "package", Content: "who logs in"},
	}, L3ImportOverwrite); err != nil {
		t.Fatalf("ImportL3: %v", err)
	}

	helper, err := db.SubAgent(llm, ProfileInput{Name: "worker", Role: "helper"})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}
	if got, err := helper.ListL3(); err != nil || len(got) != 1 {
		t.Fatalf("a second domain of the same file sees %+v (err %v), want the one graph the file holds", got, err)
	}

	workerDB, err := Open(filepath.Join(dir, "worker.meh"), llm, DefaultMemHopDefaults, profile)
	if err != nil {
		t.Fatalf("Open the worker file: %v", err)
	}
	t.Cleanup(func() { _ = workerDB.Close() })
	worker, err := workerDB.Primary()
	if err != nil {
		t.Fatalf("worker Primary: %v", err)
	}
	if got, err := worker.ListL3(); err != nil || len(got) != 0 {
		t.Fatalf("a second file inherited the first one's knowledge graph: %+v (err %v)", got, err)
	}
	if res, err := worker.ImportL3([]L3ImportItem{
		{Title: "auth", Domain: "proj", NodeType: "package", Content: "its own take"},
	}, L3ImportOverwrite); err != nil || len(res.GraphIDs) != 1 {
		t.Fatalf("the worker file cannot build its own graph: %+v (%v)", res, err)
	}
	if got, err := parent.ListL3(); err != nil || len(got) != 1 || got[0].Name != "proj" {
		t.Fatalf("the worker's import disturbed the parent file: %+v (err %v)", got, err)
	}
}

// A domain's own id — the one Session.AgentID renders — is the handle a host keeps when it
// would rather address one memory than re-say a name. DB.Agent takes it back to that same
// domain, across a reopening, and refuses an id this file never registered rather than
// opening an empty memory in its place. Every id the facade hands out round-trips, the
// primary's included.
func TestADomainIsAddressableByTheIDTheLibraryIssued(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ids.meh")
	llm := LlmConfig{APIURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}

	db, err := Open(path, llm, DefaultMemHopDefaults, &ProfileInput{Name: "Meow", Role: "assistant"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	primary, err := db.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	worker, err := db.SubAgent(llm, ProfileInput{Name: "worker", Role: "helper"})
	if err != nil {
		t.Fatalf("SubAgent worker: %v", err)
	}
	helper, err := db.SubAgent(llm, ProfileInput{Name: "helper", Role: "helper"})
	if err != nil {
		t.Fatalf("SubAgent helper: %v", err)
	}
	primaryID, workerID, helperID := primary.AgentID(), worker.AgentID(), helper.AgentID()
	if len(workerID) != 16 || workerID == helperID || workerID == primaryID {
		t.Fatalf("three domains answered %q %q %q, want three distinct 16-hex ids", primaryID, workerID, helperID)
	}
	if err := worker.UpdateL0(ProfileInput{Name: "worker", Role: "helper", Personality: "先写测试再动手"}); err != nil {
		t.Fatalf("worker UpdateL0: %v", err)
	}

	// The id lands on the domain it came from, and a second id on a different one.
	again, err := db.Agent(llm, workerID)
	if err != nil {
		t.Fatalf("Agent by id: %v", err)
	}
	if again.AgentID() != workerID {
		t.Fatalf("the handle came back with %q, want %q", again.AgentID(), workerID)
	}
	got, err := again.GetL0()
	if err != nil {
		t.Fatalf("GetL0 through the id handle: %v", err)
	}
	if got.Name != "worker" || got.Personality != "先写测试再动手" {
		t.Fatalf("the id addressed %+v, want worker's own profile", got)
	}
	other, err := db.Agent(llm, helperID)
	if err != nil {
		t.Fatalf("Agent by the second id: %v", err)
	}
	if slot, err := other.GetL0(); err != nil || slot.Name != "helper" {
		t.Fatalf("the second id landed on %+v (err %v), want helper's own domain", slot, err)
	}
	fromID, err := db.Agent(llm, primaryID)
	if err != nil {
		t.Fatalf("Agent by the primary's own id: %v", err)
	}
	if slot, err := fromID.GetL0(); err != nil || slot.Name != "Meow" || slot.AgentType != AgentTypePrimary {
		t.Fatalf("the primary's id addressed %+v (err %v), want the file's primary", slot, err)
	}

	// An id this file never registered is refused, not created; a name handed to the id
	// door is a parameter error, before any domain is touched.
	if _, err := db.Agent(llm, "ffffffffffffffff"); CodeOf(err) != ErrAgentNotFound {
		t.Fatalf("an unknown id answered %v (code %d), want ErrAgentNotFound", err, CodeOf(err))
	}
	if _, err := db.Agent(llm, "worker"); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("a name at the id door answered %v (code %d), want ErrInvalidQuery", err, CodeOf(err))
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(path, llm, DefaultMemHopDefaults, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	byID, err := reopened.Agent(llm, workerID)
	if err != nil {
		t.Fatalf("Agent by the same id after reopening: %v", err)
	}
	if slot, err := byID.GetL0(); err != nil || slot.Name != "worker" || slot.Personality != "先写测试再动手" {
		t.Fatalf("after reopening the id addressed %+v (err %v), want worker's profile", slot, err)
	}
}

// The listing is the other half of the id door: a host that inherited a `.meh` or lost its
// own roster reads DB.Agents to learn which domains the file holds, instead of guessing a
// name and quietly creating a second domain beside the real one. What it lists must be what
// it can open, by both keys, and survive a restart.
func TestAgentsDiscoversEveryDomainInTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roster.meh")
	llm := LlmConfig{APIURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}

	db, err := Open(path, llm, DefaultMemHopDefaults, &ProfileInput{Name: "Meow", Role: "assistant"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	primary, err := db.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	handles := map[string]string{}
	for _, name := range []string{"worker", "helper"} {
		s, err := db.SubAgent(llm, ProfileInput{Name: name, Role: "helper"})
		if err != nil {
			t.Fatalf("SubAgent %s: %v", name, err)
		}
		handles[s.AgentID()] = name
	}

	list, err := db.Agents()
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("Agents listed %d domains (%+v), want primary + two sub-agents", len(list), list)
	}
	if !list[0].Primary || list[0].ID != "0000000000000000" || list[0].Name != "Meow" {
		t.Fatalf("the list does not lead with the primary it was opened on: %+v", list[0])
	}
	if primary.AgentID() != list[0].ID {
		t.Fatalf("Primary reports %q while the list says %q", primary.AgentID(), list[0].ID)
	}
	for i := 1; i < len(list); i++ {
		if list[i].Primary || list[i-1].ID >= list[i].ID {
			t.Fatalf("the sub-agents are not id-ascending and unflagged: %+v", list)
		}
		if got, ok := handles[list[i].ID]; !ok || got != list[i].Name {
			t.Fatalf("Agents names %s/%s, want it to match the domain that got %q",
				list[i].ID, list[i].Name, got)
		}
	}

	// Everything listed is openable, and opens onto the domain the name belongs to.
	for _, entry := range list {
		reopened, err := db.Agent(llm, entry.ID)
		if err != nil {
			t.Fatalf("Agent(%s): %v", entry.ID, err)
		}
		slot, err := reopened.GetL0()
		if err != nil {
			t.Fatalf("GetL0(%s): %v", entry.ID, err)
		}
		if slot.Name != entry.Name || reopened.AgentID() != entry.ID {
			t.Fatalf("id %s opened %q, want the domain Agents named %q", entry.ID, slot.Name, entry.Name)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	after, err := Open(path, llm, DefaultMemHopDefaults, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = after.Close() })
	list2, err := after.Agents()
	if err != nil {
		t.Fatalf("Agents after reopening: %v", err)
	}
	if len(list2) != len(list) {
		t.Fatalf("the roster changed across restart: %d then %d", len(list), len(list2))
	}
	for i := range list {
		if list2[i] != list[i] {
			t.Fatalf("entry %d changed across restart: %+v then %+v", i, list[i], list2[i])
		}
	}
}
