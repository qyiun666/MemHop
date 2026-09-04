// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for the L5 capability lifecycle: import a card,
// correct its definition, feed usage back, re-import at the next startup, and
// read it back after a restart. The host that does this is meowagent's toolbox
// — it imports cards at startup, activates what it wires up as a tool, and
// records every run. Nothing here forges an id: the built-in cards the
// refusals name are read out of ListCapabilities first.

package test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
	"github.com/qyiun666/MemHop/internal"
)

// mustFindCapability reads one card back through the listing, so an assertion
// on it proves the write landed rather than just echoed.
func mustFindCapability(t *testing.T, sess *memhop.Session, id string) memhop.Capability {
	t.Helper()
	got := mustListAll(t, sess, internal.CapabilityListQuery{IDs: []string{id}})
	if len(got) != 1 {
		t.Fatalf("capability %s: %d cards found, want 1", id, len(got))
	}
	return got[0]
}

// writeCapabilityFile drops one memhop-capability/v3 document in dir and
// returns its path. trigger varies so two writes are distinguishable files.
func writeCapabilityFile(t *testing.T, dir, name, trigger string) string {
	t.Helper()
	path := filepath.Join(dir, name+".json")
	body := fmt.Sprintf(`{"format":"memhop-capability/v3","name":%q,"version":"1","type":"mcp",`+
		`"summary":"读取文件内容","trigger":%q,`+
		`"resources":[{"type":"mcp","name":"read_file","ref":"read_file","desc":"读一个文件"}]}`, name, trigger)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write capability file: %v", err)
	}
	return path
}

func TestInterfaceCapabilityLifecycle(t *testing.T) {
	db, _ := openTestDB(t)
	imported, err := db.ImportCapability(writeCapabilityFile(t, t.TempDir(), "读文件", "用户要求读文件"))
	if err != nil {
		t.Fatalf("ImportCapability: %v", err)
	}
	// An import lands active with no usage history yet: the host wires it up as
	// a tool right away and starts recording runs against the id it got back.
	if imported.Status != memhop.CapabilityActive || imported.Origin != memhop.CapabilityOriginImported {
		t.Fatalf("imported card = %+v", imported)
	}
	if imported.TriggerCount != 0 || imported.SuccessRate != 0 || imported.LastTriggered != 0 || imported.FileHash == "" {
		t.Fatalf("fresh card = %+v, want no usage history and the file hash it came from", imported)
	}
	id := imported.IDHash
	if got := mustFindCapability(t, db.Session, id); got.Name != "读文件" || got.Trigger != "用户要求读文件" {
		t.Fatalf("imported card reads back %+v", got)
	}

	// The patch is partial, so every field it leaves nil survives. Name is not
	// patchable at all — the id derives from it, so renaming means re-import.
	trigger := "用户给出文件路径并要求查看"
	updated, err := db.UpdateCapability(id, memhop.CapabilityPatch{Trigger: &trigger})
	if err != nil {
		t.Fatalf("UpdateCapability: %v", err)
	}
	if updated.Name != "读文件" || updated.Summary != "读取文件内容" || updated.Trigger != trigger {
		t.Fatalf("patched card = %+v", updated)
	}
	// The stored definition no longer matches the bytes on disk.
	if got := mustFindCapability(t, db.Session, id); got.Trigger != trigger || got.FileHash != "" {
		t.Fatalf("after update: %+v", got)
	}

	// Usage feedback is a running average the host reads back to rank cards.
	used, err := db.RecordCapabilityUsage(id, true)
	if err != nil {
		t.Fatalf("RecordCapabilityUsage(success): %v", err)
	}
	if used.TriggerCount != 1 || used.SuccessRate != 1 || used.LastTriggered == 0 {
		t.Fatalf("first run = %+v, want one trigger at 100%%", used)
	}
	used, err = db.RecordCapabilityUsage(id, false)
	if err != nil {
		t.Fatalf("RecordCapabilityUsage(failure): %v", err)
	}
	if used.TriggerCount != 2 || used.SuccessRate != 0.5 {
		t.Fatalf("two runs with one failure = %+v, want two triggers at 50%%", used)
	}
	if got := mustFindCapability(t, db.Session, id); got.TriggerCount != 2 || got.SuccessRate != 0.5 {
		t.Fatalf("usage did not reach the listing: %+v", got)
	}

	// Status is the host's own switch: deprecate a card without deleting its
	// history, then activate it again.
	deprecated := memhop.CapabilityDeprecated
	if _, err := db.UpdateCapability(id, memhop.CapabilityPatch{Status: &deprecated}); err != nil {
		t.Fatalf("deprecate: %v", err)
	}
	if got := mustFindCapability(t, db.Session, id); got.Status != memhop.CapabilityDeprecated {
		t.Fatalf("status after deprecate = %q", got.Status)
	}
	activated, err := db.ActivateCapability(id)
	if err != nil {
		t.Fatalf("ActivateCapability: %v", err)
	}
	if activated.Status != memhop.CapabilityActive || activated.TriggerCount != 2 {
		t.Fatalf("activate echoed %+v", activated)
	}

	if err := db.DeleteCapability(id); err != nil {
		t.Fatalf("DeleteCapability: %v", err)
	}
	// Deleting what is already gone is reported, exactly as DeleteScene,
	// DeleteTopic and DeleteL3Nodes report it: a host reconciling its cards has
	// to be able to tell a real deletion from a no-op.
	if err := db.DeleteCapability(id); err == nil {
		t.Fatal("deleting a capability twice should be refused")
	}
	if left := mustListStored(t, db.Session); len(left) != 0 {
		t.Fatalf("cards left after delete: %+v", left)
	}
}

// A host re-imports the same files on every startup, and two of those imports
// take different paths through the engine depending on whether the card was
// corrected in between.
func TestInterfaceCapabilityReimport(t *testing.T) {
	db, _ := openTestDB(t)

	// Untouched card: re-importing identical bytes writes nothing at all, so
	// the file does not grow once per startup.
	path := writeCapabilityFile(t, t.TempDir(), "稳定卡", "原触发词")
	stable, err := db.ImportCapability(path)
	if err != nil {
		t.Fatalf("ImportCapability: %v", err)
	}
	again, err := db.ImportCapability(path)
	if err != nil {
		t.Fatalf("re-import identical bytes: %v", err)
	}
	if again.IDHash != stable.IDHash || again.CreatedAt != stable.CreatedAt || again.UpdatedAt != stable.UpdatedAt {
		t.Fatalf("unchanged re-import rewrote the card: %+v vs %+v", again, stable)
	}

	// A corrected card no longer matches the file, so the next startup import
	// restores the shipped definition — while the usage history the host
	// recorded survives the overwrite.
	if _, err := db.RecordCapabilityUsage(stable.IDHash, true); err != nil {
		t.Fatalf("RecordCapabilityUsage: %v", err)
	}
	edited, err := db.ImportCapability(path)
	if err != nil {
		t.Fatalf("re-import after usage: %v", err)
	}
	if edited.TriggerCount != 1 || edited.SuccessRate != 1 || edited.CreatedAt != stable.CreatedAt {
		t.Fatalf("re-import lost the usage history or the original card: %+v", edited)
	}

	// Importing under a name that already exists is an upsert on the same id,
	// not a second card.
	rewritten := writeCapabilityFile(t, t.TempDir(), "稳定卡", "改过的触发词")
	second, err := db.ImportCapability(rewritten)
	if err != nil {
		t.Fatalf("import same name, new content: %v", err)
	}
	if second.IDHash != stable.IDHash || second.Trigger != "改过的触发词" {
		t.Fatalf("same-name import minted a second card: %+v", second)
	}
	if len(mustListAll(t, db.Session, internal.CapabilityListQuery{IDs: []string{stable.IDHash}})) != 1 {
		t.Fatal("one name now addresses more than one stored card")
	}
}

// The built-in toolbox is the manual the engine ships, not host data: every
// write path refuses it, and a host that only reads is unaffected.
func TestInterfaceBuiltinCapabilitiesAreReadOnly(t *testing.T) {
	db, _ := openTestDB(t)
	builtin := mustFindBuiltin(t, db.Session)

	summary := "覆盖官方卡"
	if _, err := db.UpdateCapability(builtin.IDHash, memhop.CapabilityPatch{Summary: &summary}); err == nil {
		t.Fatal("UpdateCapability on a built-in should be refused")
	}
	if _, err := db.ActivateCapability(builtin.IDHash); err == nil {
		t.Fatal("ActivateCapability on a built-in should be refused")
	}
	if _, err := db.RecordCapabilityUsage(builtin.IDHash, true); err == nil {
		t.Fatal("RecordCapabilityUsage on a built-in should be refused")
	}
	if err := db.DeleteCapability(builtin.IDHash); err == nil {
		t.Fatal("DeleteCapability on a built-in should be refused")
	}
	if got := mustFindCapability(t, db.Session, builtin.IDHash); got.Summary != builtin.Summary || got.Origin != memhop.CapabilityOriginBuiltin {
		t.Fatalf("a refused write moved a built-in card: %+v", got)
	}
}

// Filtering is how a host finds the card it means without pulling the whole
// toolbox, and the whole point of storing it is that the read survives a
// restart with the same domain id and the same usage.
func TestInterfaceCapabilityQueryAndPersistence(t *testing.T) {
	llm := newMockLLM(t)
	filePath := filepath.Join(t.TempDir(), "cap.meh")
	importDir := t.TempDir()
	m := openMockMulti(t, filePath, llm.srv.URL)
	db := newTestDB(t, m)
	imported, err := db.ImportCapability(writeCapabilityFile(t, importDir, "压缩场景", "用户要求压缩历史"))
	if err != nil {
		db.Close()
		t.Fatalf("ImportCapability: %v", err)
	}
	if _, err := db.RecordCapabilityUsage(imported.IDHash, true); err != nil {
		db.Close()
		t.Fatalf("RecordCapabilityUsage: %v", err)
	}
	if _, err := db.ImportCapability(writeCapabilityFile(t, importDir, "检索知识", "用户提问某个概念")); err != nil {
		db.Close()
		t.Fatalf("ImportCapability second card: %v", err)
	}
	before := mustListStored(t, db.Session)
	if len(before) != 2 {
		db.Close()
		t.Fatalf("stored cards = %d, want the two imports", len(before))
	}
	if err := db.Checkpoint(); err != nil {
		db.Close()
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen by name: the registry has to hand back the same domain id, or every
	// id the host stored alongside the file becomes unreachable.
	reopened := openMockMulti(t, filePath, llm.srv.URL)
	t.Cleanup(func() { _ = reopened.Close() })
	agentID, err := reopened.CreateAgent("test")
	if err != nil {
		t.Fatalf("CreateAgent after reopen: %v", err)
	}
	sess, err := reopened.Session(agentID)
	if err != nil {
		t.Fatalf("Session after reopen: %v", err)
	}
	if got := mustFindCapability(t, sess, imported.IDHash); got.Name != "压缩场景" || got.TriggerCount != 1 {
		t.Fatalf("card after reopen = %+v", got)
	}
	if after := mustListStored(t, sess); len(after) != len(before) {
		t.Fatalf("stored cards changed across the restart: %+v", after)
	}

	byKeyword := mustListAll(t, sess, internal.CapabilityListQuery{Keyword: "压缩"})
	if len(byKeyword) != 1 || byKeyword[0].Name != "压缩场景" {
		t.Fatalf("keyword filter matched %+v", byKeyword)
	}
	mcp := memhop.CapabilityMCP
	active := memhop.CapabilityActive
	byKind := mustListAll(t, sess, internal.CapabilityListQuery{Type: &mcp, Status: &active})
	if len(byKind) < 2 {
		t.Fatalf("type+status filter matched %d cards, want both imports with the built-ins", len(byKind))
	}
	for _, c := range byKind {
		if c.Type != memhop.CapabilityMCP || c.Status != memhop.CapabilityActive {
			t.Fatalf("filter leaked %+v", c)
		}
	}
}

func mustListAll(t *testing.T, sess *memhop.Session, q internal.CapabilityListQuery) []memhop.Capability {
	t.Helper()
	caps, err := sess.ListCapabilities(q)
	if err != nil {
		t.Fatalf("ListCapabilities(%+v): %v", q, err)
	}
	return caps
}

// mustListStored returns the cards the host itself wrote, with the read-only
// built-in toolbox filtered out.
func mustListStored(t *testing.T, sess *memhop.Session) []memhop.Capability {
	t.Helper()
	var out []memhop.Capability
	for _, c := range mustListAll(t, sess, internal.CapabilityListQuery{}) {
		if c.Origin != memhop.CapabilityOriginBuiltin {
			out = append(out, c)
		}
	}
	return out
}

func mustFindBuiltin(t *testing.T, sess *memhop.Session) memhop.Capability {
	t.Helper()
	for _, c := range mustListAll(t, sess, internal.CapabilityListQuery{}) {
		if c.Origin == memhop.CapabilityOriginBuiltin {
			return c
		}
	}
	t.Fatal("no built-in capability was loaded at open")
	return memhop.Capability{}
}
