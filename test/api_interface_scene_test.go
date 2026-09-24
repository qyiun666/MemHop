// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Host journey for the L2 scene surface: the calls a host makes to manage the
// sessions it already has — rename, anchor to a project domain, read the whole
// transcript, fold two sessions into one, and correct memory by deleting.
//
// Every id here is one the library minted and the host got back from a call.
// That is the point of this file: a test that forged an id would pass while
// proving nothing a host can actually do.

package test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
)

// findScene looks a scene up in a listing — the way a host confirms a patch
// through the read it would otherwise use, rather than through the value the
// patch call itself returned.
func findScene(t *testing.T, db *testDB, sceneID string) memhop.SceneSlot {
	t.Helper()
	scenes, err := db.ListScenes("")
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	for _, s := range scenes {
		if s.SceneID == sceneID {
			return s
		}
	}
	t.Fatalf("scene %s missing from %+v", sceneID, scenes)
	return memhop.SceneSlot{}
}

// settleTurn runs one full turn the way a host does: read the session (which opens
// the turn), then close it with what the turn said. It returns the topic id that now
// carries it — and pins that it is the one Search minted, since a closing call names
// no id of either kind.
func settleTurn(t *testing.T, db *testDB, sceneID, user, agent string) string {
	t.Helper()
	id := openTurn(t, db, sceneID)
	closed, err := turn(db.Session, user, agent)
	if err != nil {
		t.Fatalf("turn(%q): %v", user, err)
	}
	if closed != id {
		t.Fatalf("Update settled topic %s, want the turn Search opened (%s)", closed, id)
	}
	return id
}

// UpdateScene is the host's only handle on a scene's own metadata, so the two
// things it can change have to show up in the reads a host actually uses.
func TestInterfaceSceneNameAndAnchor(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)

	// A library-named scene arrives as "session:<id>" and the host renames it.
	if got := findScene(t, db, sceneID); !strings.HasPrefix(got.SceneName, "session:") {
		t.Fatalf("fresh scene name = %q, want the library's session:<id> form", got.SceneName)
	}
	title := "重构 memhop 的写入路径"
	got, err := db.UpdateScene(sceneID, memhop.ScenePatch{Name: &title})
	if err != nil {
		t.Fatalf("UpdateScene rename: %v", err)
	}
	if got.SceneName != title {
		t.Fatalf("UpdateScene returned name %q", got.SceneName)
	}
	if listed := findScene(t, db, sceneID); listed.SceneName != title {
		t.Fatalf("ListScenes name = %q", listed.SceneName)
	}
	read, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if read.Scene.SceneName != title {
		t.Fatalf("Search scene name = %q", read.Scene.SceneName)
	}

	// An empty title is refused rather than silently clearing the name.
	empty := ""
	if _, err := db.UpdateScene(sceneID, memhop.ScenePatch{Name: &empty}); err == nil {
		t.Fatal("renaming to an empty title should fail")
	}
	if findScene(t, db, sceneID).SceneName != title {
		t.Fatal("the refused rename must leave the title in place")
	}

	// Anchoring needs a real project domain: a host gets its id from ImportL3,
	// because a graph id is hash(Domain) and no other call renders that.
	res, err := db.ImportL3([]internal.L3ImportItem{
		{Title: "写入路径", Domain: "memhop", NodeType: "concept", Content: "Update 的一次蒸馏"},
	}, internal.L3ImportSkip)
	if err != nil {
		t.Fatalf("ImportL3: %v", err)
	}
	if len(res.GraphIDs) != 1 {
		t.Fatalf("import reported graphs %v, want exactly one", res.GraphIDs)
	}
	domain := res.GraphIDs[0]

	if anchored, err := db.UpdateScene(sceneID, memhop.ScenePatch{L3ID: &domain}); err != nil {
		t.Fatalf("anchor: %v", err)
	} else if anchored.L3ID != domain {
		t.Fatalf("anchor echo L3ID = %q, want %q", anchored.L3ID, domain)
	}
	inDomain, err := db.ListScenes(domain)
	if err != nil {
		t.Fatalf("ListScenes(domain): %v", err)
	}
	if len(inDomain) != 1 || inDomain[0].SceneID != sceneID {
		t.Fatalf("domain listing = %+v, want only this scene", inDomain)
	}
	// The anchor is part of the ordinary scene read, not just the listing.
	if got := findScene(t, db, sceneID); got.L3ID != domain {
		t.Fatalf("listing L3ID = %q", got.L3ID)
	}

	// Replacing one domain with another loses the first, so it takes Force.
	other, err := db.ImportL3([]internal.L3ImportItem{
		{Title: "召回", Domain: "retrieval", NodeType: "concept", Content: "已退役的检索子系统"},
	}, internal.L3ImportSkip)
	if err != nil {
		t.Fatalf("ImportL3 second domain: %v", err)
	}
	if _, err := db.UpdateScene(sceneID, memhop.ScenePatch{L3ID: &other.GraphIDs[0]}); err == nil {
		t.Fatal("re-anchoring an anchored scene without Force should fail")
	}
	if got := findScene(t, db, sceneID); got.L3ID != domain {
		t.Fatalf("the refused re-anchor moved the scene to %q", got.L3ID)
	}
	if _, err := db.UpdateScene(sceneID, memhop.ScenePatch{L3ID: &other.GraphIDs[0], Force: true}); err != nil {
		t.Fatalf("forced re-anchor: %v", err)
	}
	if got := findScene(t, db, sceneID); got.L3ID != other.GraphIDs[0] {
		t.Fatalf("after Force L3ID = %q", got.L3ID)
	}

	// A stale domain id — one the host still holds but that has since been
	// deleted — is refused before the scene is touched. This is also the
	// DeleteL3 close: the graph leaves the listing and the anchor cannot land
	// on it any more.
	if err := db.DeleteL3(other.GraphIDs[0]); err != nil {
		t.Fatalf("DeleteL3: %v", err)
	}
	if _, err := db.GetL3(other.GraphIDs[0]); err == nil {
		t.Fatal("the deleted graph still reads back")
	}
	if _, err := db.UpdateScene(sceneID, memhop.ScenePatch{L3ID: &domain, Force: true}); err != nil {
		t.Fatalf("move back to the surviving domain: %v", err)
	}
	if _, err := db.UpdateScene(sceneID, memhop.ScenePatch{L3ID: &other.GraphIDs[0], Force: true}); err == nil {
		t.Fatal("anchoring to a deleted graph should fail")
	}
	if got := findScene(t, db, sceneID); got.L3ID != domain {
		t.Fatalf("the refused anchor left the scene on %q, want %q", got.L3ID, domain)
	}

	// Clearing is reversible, so it needs no Force — and the domain listing
	// must stop reporting the scene.
	if cleared, err := db.UpdateScene(sceneID, memhop.ScenePatch{L3ID: ptr("")}); err != nil {
		t.Fatalf("clear anchor: %v", err)
	} else if cleared.L3ID != "" {
		t.Fatalf("cleared scene still reports L3ID %q", cleared.L3ID)
	}
	if inDomain, err := db.ListScenes(other.GraphIDs[0]); err != nil || len(inDomain) != 0 {
		t.Fatalf("domain listing after clear = %+v err %v, want empty", inDomain, err)
	}
}

// SceneContext is the read a host uses to show or export a conversation, and it
// is the only one that writes nothing and the only one that sees through a
// Dream-fused group. Both halves have to hold together.
func TestInterfaceSceneContextReadsThroughFusion(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "ctx.meh"), llm.srv.URL,
		func(d *internal.MemHopDefaults) { d.DreamCompressMinTopics = 2 })
	db := newTestDB(t, m)
	t.Cleanup(func() { _ = db.Close() })

	sceneID := openSession(t, db)
	first := settleTurn(t, db, sceneID, "用户要求先拆分 Update 的蒸馏调用", "已拆出 extractOne,两条路径共用同一阶梯")
	second := settleTurn(t, db, sceneID, "用户要求把读不动的记录上报而不是跳过", "已改成只有 ErrNotFound 才跳过")

	// SceneContext costs no distillation: it reads, it does not consolidate.
	before := llm.count("keywords")
	ctx, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	if got := llm.count("keywords"); got != before {
		t.Fatalf("SceneContext triggered %d distillations, want 0", got-before)
	}
	if len(ctx.Topics) != 2 {
		t.Fatalf("context = %+v, want the two turns", ctx.Topics)
	}
	// Entries come in speaking order, each carrying its own two originals.
	for i, want := range []struct {
		id    string
		user  string
		agent string
		depth int
		child int
	}{
		{first, "用户要求先拆分 Update 的蒸馏调用", "已拆出 extractOne,两条路径共用同一阶梯", 1, 0},
		{second, "用户要求把读不动的记录上报而不是跳过", "已改成只有 ErrNotFound 才跳过", 1, 0},
	} {
		got := ctx.Topics[i]
		if got.TopicID != want.id || got.Depth != want.depth {
			t.Fatalf("entry %d = %s depth %d, want %s depth %d", i, got.TopicID, got.Depth, want.id, want.depth)
		}
		if len(got.Messages) != 2 {
			t.Fatalf("entry %d carries %d messages, want 2 originals", i, len(got.Messages))
		}
		if got.Messages[0].Role != memhop.RoleUser || got.Messages[0].Content != want.user {
			t.Fatalf("entry %d user message = %+v", i, got.Messages[0])
		}
		if got.Messages[1].Role != memhop.RoleAgent || got.Messages[1].Content != want.agent {
			t.Fatalf("entry %d agent message = %+v", i, got.Messages[1])
		}
	}

	// SceneContext opens no turn. The turn counter is not on the host-visible
	// scene record, so that contract is pinned where it is readable:
	// TestSceneContextOpensNoTurn in internal.

	// After consolidation the ordinary read shows one fused group, while
	// SceneContext still hands back the originals on the children it sunk.
	if _, err := db.Dream(context.Background(), sceneID); err != nil {
		t.Fatalf("Dream: %v", err)
	}
	fused, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after Dream: %v", err)
	}
	if len(fused.Topics) != 1 || fused.Topics[0].ParentID != nil {
		t.Fatalf("surface after Dream = %+v, want one fused group as the scene's only root", fused.Topics)
	}
	ctx2, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("SceneContext after Dream: %v", err)
	}
	if len(ctx2.Topics) != 3 {
		t.Fatalf("SceneContext returned %d entries, want the fused parent over the %d turns it swallowed",
			len(ctx2.Topics), len(fused.Topics))
	}
	// The parent comes first: it shares its earliest child's timestamp, so the
	// listing's depth key is what puts a group's summary above the originals it
	// introduces instead of in the middle of them.
	parent := ctx2.Topics[0]
	if parent.Depth != 1 || parent.ChildCount != 2 {
		t.Fatalf("first entry after fusion = %+v, want the fused parent owning both turns", parent)
	}
	if parent.TopicID != fused.Topics[0].ID {
		t.Fatalf("the root the ordinary read shows (%s) is not the parent this read expands (%s)",
			fused.Topics[0].ID, parent.TopicID)
	}
	// The summary is the parent's own single line, under the role Dream owns — 3,
	// which the public surface deliberately leaves unnamed.
	if len(parent.Messages) != 1 || parent.Messages[0].Content != "合并摘要保留全部细节" || parent.Messages[0].Role != 3 {
		t.Fatalf("the fused parent carries %+v, want the group's summary alone under role 3", parent.Messages)
	}
	// The sunk turns keep their own originals, which is the whole reason this read
	// exists. Claimed by id rather than by position: two turns settled inside one
	// millisecond tie on the timestamp and fall back to the id.
	byID := make(map[string]memhop.SceneContextTopic, len(ctx2.Topics))
	for _, e := range ctx2.Topics {
		byID[e.TopicID] = e
	}
	for _, want := range []struct{ id, user, agent string }{
		{first, "用户要求先拆分 Update 的蒸馏调用", "已拆出 extractOne,两条路径共用同一阶梯"},
		{second, "用户要求把读不动的记录上报而不是跳过", "已改成只有 ErrNotFound 才跳过"},
	} {
		got, ok := byID[want.id]
		if !ok {
			t.Fatalf("the sunk turn %s is missing from %+v", want.id, ctx2.Topics)
		}
		if got.Depth != 2 || got.ChildCount != 0 {
			t.Fatalf("sunk turn %s = depth %d children %d, want depth 2 with none under it", got.TopicID, got.Depth, got.ChildCount)
		}
		if len(got.Messages) != 2 || got.Messages[0].Content != want.user || got.Messages[1].Content != want.agent {
			t.Fatalf("sunk turn %s carries %+v, want its own two originals", got.TopicID, got.Messages)
		}
	}
}

// A host that resumed one conversation under a new session id folds the two
// back together; the primary's metadata wins and the history has to be whole.
func TestInterfaceMergeScenes(t *testing.T) {
	db, _ := openTestDB(t)
	primary := openSession(t, db)
	secondary := openSession(t, db)
	if primary == secondary {
		t.Fatal("two fresh sessions must not share an id")
	}
	primaryTurn := settleTurn(t, db, primary, "主会话的第一轮", "第一轮回复")
	secondaryTurn := settleTurn(t, db, secondary, "被重启的同一件事", "重启后的回复")

	if err := db.MergeScenes(primary, []string{secondary}); err != nil {
		t.Fatalf("MergeScenes: %v", err)
	}
	// The merge empties the domain's memory of the turn it held on the scene that
	// went under — a turn id derives from its scene, so that close is refused rather
	// than written onto the merged one, and the host has to read before writing again.
	if _, err := db.Update(memhop.TurnEnd{Input: "被吞掉的那一轮", Output: "不该落笔",
		CreatedAt: time.Now().UnixMilli()}); err == nil ||
		!strings.Contains(err.Error(), "no turn is open") {
		t.Fatalf("closing after the merge that swallowed the scene = %v, want the open-turn refusal", err)
	}
	// The secondary scene is gone from every listing and read.
	if _, err := db.Search(memhop.SearchQuery{SceneID: secondary}); err == nil {
		t.Fatal("the merged-away scene still reads back")
	}
	for _, s := range mustScenes(t, db) {
		if s.SceneID == secondary {
			t.Fatalf("merged scene still listed: %+v", s)
		}
	}
	// Its turn is now part of the primary's surface — the same two topic ids, not
	// two re-minted ones: a merge moves a turn's scene, it does not rewrite the turn.
	res, err := db.Search(memhop.SearchQuery{SceneID: primary})
	if err != nil {
		t.Fatalf("Search(primary): %v", err)
	}
	onSurface := make(map[string]bool, len(res.Topics))
	for _, topic := range res.Topics {
		onSurface[topic.ID] = true
	}
	if len(res.Topics) != 2 || !onSurface[primaryTurn] || !onSurface[secondaryTurn] {
		t.Fatalf("primary surface = %+v, want exactly the turns %s and %s", res.Topics, primaryTurn, secondaryTurn)
	}
	// The originals came along, so nothing was lost by the fold: each turn still
	// reads back its own two lines, and nothing else came with them.
	merged, err := db.SceneContext(primary)
	if err != nil {
		t.Fatalf("SceneContext(primary): %v", err)
	}
	if len(merged.Topics) != 2 {
		t.Fatalf("merged transcript = %+v, want the two turns", merged.Topics)
	}
	byID := make(map[string]memhop.SceneContextTopic, len(merged.Topics))
	for _, e := range merged.Topics {
		byID[e.TopicID] = e
	}
	for _, want := range []struct{ id, user, agent string }{
		{primaryTurn, "主会话的第一轮", "第一轮回复"},
		{secondaryTurn, "被重启的同一件事", "重启后的回复"},
	} {
		got, ok := byID[want.id]
		if !ok {
			t.Fatalf("the merged transcript lost turn %s: %+v", want.id, merged.Topics)
		}
		if len(got.Messages) != 2 || got.Messages[0].Content != want.user || got.Messages[1].Content != want.agent {
			t.Fatalf("merged turn %s carries %+v, want its own two originals", got.TopicID, got.Messages)
		}
	}
	// A merge names scenes the host holds; an id whose scene is gone is an
	// error that leaves the primary alone.
	gone := openSession(t, db)
	settleTurn(t, db, gone, "马上作废的第三会话", "用完即弃")
	if err := db.DeleteScene(gone); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}
	before := len(mustScenes(t, db))
	if err := db.MergeScenes(primary, []string{gone}); err == nil {
		t.Fatal("merging a deleted scene should fail")
	}
	if after := len(mustScenes(t, db)); after != before {
		t.Fatalf("a refused merge changed the scene count %d -> %d", before, after)
	}
	// The rejected call must not have touched the primary either: the batch
	// delete keys on the named ids, so a stale secondary cannot take the
	// surviving scene's own record with it.
	if survived, err := db.Search(memhop.SearchQuery{SceneID: primary}); err != nil || len(survived.Topics) != 2 {
		t.Fatalf("primary damaged by the refused merge: %d topics, err %v", len(survived.Topics), err)
	}
}

// The memory-correction pair: DeleteTopic takes one turn (and its subtree) out
// of a session, DeleteScene takes the session. Both must take the L4 originals
// with them — a deleted memory that still answers a keyword search is not
// deleted.
func TestInterfaceDeleteSceneAndTopic(t *testing.T) {
	db, _ := openTestDB(t)
	keep := openSession(t, db)
	drop := openSession(t, db)

	keepA := settleTurn(t, db, keep, "要留下的第一轮", "留下")
	keepB := settleTurn(t, db, keep, "要删掉的那一轮", "删掉")
	dropA := settleTurn(t, db, drop, "整个场景作废", "一起作废")

	// A turn's originals are addressed by its own topic id: the record carries no
	// list of them, so this read is the whole answer for what a turn is made of.
	ownedIDs := func(topicID string) []string {
		t.Helper()
		id := topicID
		hits, err := db.SearchL4(internal.L4Query{TopicID: &id})
		if err != nil {
			t.Fatalf("SearchL4(topic %s): %v", topicID, err)
		}
		out := make([]string, 0, len(hits))
		for _, h := range hits {
			out = append(out, h.ID)
		}
		return out
	}

	// DeleteTopic: only that turn leaves, its sibling stays addressable.
	refs := ownedIDs(keepB)
	if len(refs) != 2 {
		t.Fatalf("turn %s carries %d originals", keepB, len(refs))
	}
	if err := db.DeleteTopic(keepB); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	if found, err := db.SearchL4(internal.L4Query{IDs: refs}); err != nil || len(found) != 0 {
		t.Fatalf("deleted originals still readable: %+v err %v", found, err)
	}
	res, err := db.Search(memhop.SearchQuery{SceneID: keep})
	if err != nil {
		t.Fatalf("Search after DeleteTopic: %v", err)
	}
	if len(res.Topics) != 1 || res.Topics[0].ID != keepA {
		t.Fatalf("surface after DeleteTopic = %+v, want only %s", res.Topics, keepA)
	}
	// Deleting a topic that is not there is an error, not a no-op.
	if err := db.DeleteTopic(keepB); err == nil {
		t.Fatal("DeleteTopic of a missing topic should fail")
	}

	// DeleteScene: the whole session and its originals go.
	dropRefs := func() []string {
		t.Helper()
		c, err := db.SceneContext(drop)
		if err != nil {
			t.Fatalf("SceneContext(drop): %v", err)
		}
		if len(c.Topics) != 1 || c.Topics[0].TopicID != dropA {
			t.Fatalf("scene to delete holds %+v, want the one turn %s", c.Topics, dropA)
		}
		return ownedIDs(dropA)
	}
	refsOfDoom := dropRefs()
	before := len(mustScenes(t, db))
	if err := db.DeleteScene(drop); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}
	if after := len(mustScenes(t, db)); after != before-1 {
		t.Fatalf("scene count %d -> %d, want one fewer", before, after)
	}
	if _, err := db.Search(memhop.SearchQuery{SceneID: drop}); err == nil {
		t.Fatal("a deleted scene can still be opened")
	}
	if found, err := db.SearchL4(internal.L4Query{IDs: refsOfDoom}); err != nil || len(found) != 0 {
		t.Fatalf("deleted scene's originals survive: %+v err %v", found, err)
	}
	if err := db.DeleteScene(drop); err == nil {
		t.Fatal("DeleteScene of a missing scene should fail")
	}
	// The survivor is untouched by the cascade.
	if _, err := db.Search(memhop.SearchQuery{SceneID: keep}); err != nil {
		t.Fatalf("Search(keep) after DeleteScene: %v", err)
	}
}

// mustScenes is the plain listing a host would show in a sidebar.
func mustScenes(t *testing.T, db *testDB) []memhop.SceneSlot {
	t.Helper()
	scenes, err := db.ListScenes("")
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	return scenes
}

// ptr is the fixture for a patch field where "" and "unset" are different
// things — which is exactly what ScenePatch encodes with a *string.
func ptr[T any](v T) *T { return &v }

// A merge retargets rows; it does not rewrite what they hold. Each swallowed turn keeps its
// own distilled track, so the merged transcript reads as the turns it now owns — not as rows
// whose keywords quietly emptied out and get re-distilled as something else later.
func TestInterfaceMergeKeepsEachTurnsKeywordTrack(t *testing.T) {
	llm := newMockLLM(t)
	llmURL := llm.srv.URL
	dbPath := filepath.Join(t.TempDir(), "merge_kw.meh")
	db := newTestDB(t, openMockDB(t, dbPath, llmURL))
	primary := openSession(t, db)
	secondary := openSession(t, db)
	openTurn(t, db, primary)
	if _, err := turn(db.Session, "主场景的第一轮说了 mmap", "读路径零拷贝"); err != nil {
		t.Fatalf("turn on the primary: %v", err)
	}
	openTurn(t, db, secondary)
	if _, err := turn(db.Session, "被并场景的第一轮说了 crc", "帧内校验和"); err != nil {
		t.Fatalf("turn on the secondary: %v", err)
	}
	before, err := db.SceneContext(secondary)
	if err != nil || len(before.Topics) != 1 || len(before.Topics[0].Keywords) == 0 {
		t.Fatalf("the secondary's turn before the merge = %+v err %v", before, err)
	}
	moved := before.Topics[0]

	if err := db.MergeScenes(primary, []string{secondary}); err != nil {
		t.Fatalf("MergeScenes: %v", err)
	}
	merged, err := db.SceneContext(primary)
	if err != nil {
		t.Fatalf("SceneContext after the merge: %v", err)
	}
	var got *memhop.SceneContextTopic
	for i := range merged.Topics {
		if merged.Topics[i].TopicID == moved.TopicID {
			got = &merged.Topics[i]
		}
	}
	if got == nil {
		t.Fatalf("the swallowed turn is missing from the merged transcript: %+v", merged.Topics)
	}
	if strings.Join(got.Keywords, "|") != strings.Join(moved.Keywords, "|") {
		t.Fatalf("the merge rewrote that turn's keyword track: %q -> %q", moved.Keywords, got.Keywords)
	}
	if got.UserTimestamp != moved.UserTimestamp || got.AgentTimestamp != moved.AgentTimestamp {
		t.Fatalf("the merge moved the turn's own two bounds: %+v vs %+v", *got, moved)
	}
	// The scene that was swallowed is gone as a conversation: naming it on the pure read is
	// a refusal, not an empty transcript — the difference matters to a host that lists scenes
	// to decide whether a session still exists.
	if _, err := db.SceneContext(secondary); memhop.CodeOf(err) != memhop.ErrNotFound {
		t.Fatalf("reading the swallowed scene: want ErrNotFound, got %v", err)
	}

	// …and the check has to be repeated on a reopened file. Every read above is served from
	// the domain's caches, which a merge re-points rather than rewrites: a claim about what
	// the merge *wrote* is only testable once the records are the only source left.
	path := dbPath
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := openMockDB(t, path, llmURL)
	defer reopened.Close()
	sub, err := reopened.Primary()
	if err != nil {
		t.Fatalf("Primary after reopen: %v", err)
	}
	afterReopen, err := sub.SceneContext(primary)
	if err != nil {
		t.Fatalf("SceneContext after reopen: %v", err)
	}
	if len(afterReopen.Topics) != len(merged.Topics) {
		t.Fatalf("the reopened transcript has %d rows, the live one %d: %+v", len(afterReopen.Topics), len(merged.Topics), afterReopen.Topics)
	}
	for _, row := range afterReopen.Topics {
		var live *memhop.SceneContextTopic
		for i := range merged.Topics {
			if merged.Topics[i].TopicID == row.TopicID {
				live = &merged.Topics[i]
			}
		}
		if live == nil {
			t.Fatalf("a row vanished across the reopen: %+v", row)
		}
		if strings.Join(row.Keywords, "|") != strings.Join(live.Keywords, "|") || len(row.Keywords) == 0 {
			t.Fatalf("the merged turn %s kept its keywords only in the cache: live=%q reopened=%q",
				row.TopicID, live.Keywords, row.Keywords)
		}
	}
}

// An anchor says which project a conversation belongs to, so a merge that folds anchored
// scenes into an unanchored one has to keep the membership rather than the survivor's blank:
// dropping it would take the merged history out of the project listing without saying so.
// A survivor that already names a domain keeps that claim, and a merge that cannot tell
// which of two domains won is refused before anything is destroyed.
func TestInterfaceMergeCarriesTheProjectAnchor(t *testing.T) {
	db, _ := openTestDB(t)
	graphs := make([]string, 3)
	for i := range graphs {
		res, err := db.ImportL3([]memhop.L3ImportItem{{
			Title: "项目 " + string(rune('A'+i)), Domain: "proj" + string(rune('0'+i)),
			NodeType: "concept", Content: "x",
		}}, memhop.L3ImportSkip)
		if err != nil || len(res.GraphIDs) != 1 {
			t.Fatalf("ImportL3 %d: %+v err %v", i, res, err)
		}
		graphs[i] = res.GraphIDs[0]
	}
	newScene := func(anchored string) string {
		res, err := db.Search(memhop.SearchQuery{NewScene: true})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		openTurn(t, db, res.Scene.SceneID)
		if _, err := turn(db.Session, "一段对话的一轮", "答"); err != nil {
			t.Fatalf("turn: %v", err)
		}
		if anchored != "" {
			if _, err := db.UpdateScene(res.Scene.SceneID, memhop.ScenePatch{L3ID: &anchored}); err != nil {
				t.Fatalf("anchor: %v", err)
			}
		}
		return res.Scene.SceneID
	}

	// 1. The survivor has no anchor and the swallowed one does: the membership moves over.
	survivor, anchored := newScene(""), newScene(graphs[0])
	if err := db.MergeScenes(survivor, []string{anchored}); err != nil {
		t.Fatalf("merge into an unanchored survivor: %v", err)
	}
	listed, err := db.ListScenes(graphs[0])
	if err != nil || len(listed) != 1 || listed[0].SceneID != survivor {
		t.Fatalf("the merged conversation vanished from its project: %+v err %v", listed, err)
	}

	// 2. The survivor's own claim stands even when a swallowed scene names another domain.
	claimed, other := newScene(graphs[1]), newScene(graphs[2])
	if err := db.MergeScenes(claimed, []string{other}); err != nil {
		t.Fatalf("merge into an anchored survivor: %v", err)
	}
	if listed, err := db.ListScenes(graphs[1]); err != nil || len(listed) != 1 || listed[0].SceneID != claimed {
		t.Fatalf("the survivor lost the domain it named itself: %+v err %v", listed, err)
	}
	if listed, err := db.ListScenes(graphs[2]); err != nil || len(listed) != 0 {
		t.Fatalf("the swallowed scene's domain still claims the merged conversation: %+v err %v", listed, err)
	}

	// 3. Two swallowed scenes in two different domains: nothing to choose between, so the
	//    call refuses and destroys nothing at all.
	a, b, c := newScene(graphs[0]), newScene(graphs[1]), newScene("")
	err = db.MergeScenes(c, []string{a, b})
	if memhop.CodeOf(err) != memhop.ErrInvalidQuery {
		t.Fatalf("merging across two project domains: want ErrInvalidQuery, got %v", err)
	}
	// Five scenes: two from the merges above plus the three this call was asked about.
	if listed, err := db.ListScenes(""); err != nil || len(listed) != 5 {
		t.Fatalf("the refused merge already destroyed scenes: %+v err %v", listed, err)
	}
	for _, g := range graphs[:2] {
		if listed, err := db.ListScenes(g); err != nil || len(listed) == 0 {
			t.Fatalf("a refused merge emptied the project listing for %s: %+v err %v", g, listed, err)
		}
	}
}

// A merge moves the domain's own memory of which scene it is working, and that has two
// different consequences for a round that is open at the time — worth pinning because a host
// looping over rounds can hit either one. A round opened on the survivor keeps running: the
// merge leaves its key alone and it closes normally. A round left open on a scene being
// swallowed cannot be closed at all: that round's key names a scene that no longer exists,
// so the next read starts a fresh round on the survivor and the recorded content stays where
// it was written until the retention window takes it.
func TestInterfaceMergeMovesTheOpenRoundWithTheScene(t *testing.T) {
	t.Run("a round open on the survivor keeps working", func(t *testing.T) {
		db, _ := openTestDB(t)
		survivor := openSession(t, db)
		swallowed := openSession(t, db)
		openTurn(t, db, swallowed)
		if _, err := turn(db.Session, "被并方已收口的一轮", "答"); err != nil {
			t.Fatalf("close the swallowed scene's round: %v", err)
		}
		live := openTurn(t, db, survivor)
		if _, err := db.AppendArchive(planEvent(time.Now().UnixMilli(), "tool_call", "开着的那轮记的事")); err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := db.MergeScenes(survivor, []string{swallowed}); err != nil {
			t.Fatalf("MergeScenes: %v", err)
		}
		topic, err := turn(db.Session, "把开着的那轮收掉", "答")
		if err != nil {
			t.Fatalf("closing the survivor's round after the merge: %v", err)
		}
		if topic != live {
			t.Fatalf("the round closed into %s, not the key the read minted (%s)", topic, live)
		}
		kind := memhop.KindEvent
		rows, err := db.SearchL4(memhop.L4Query{TopicID: &live, Kind: &kind})
		if err != nil || len(rows) != 1 {
			t.Fatalf("the round's own event = %+v err %v", rows, err)
		}
	})

	t.Run("a round left open on the scene being swallowed cannot be closed", func(t *testing.T) {
		db, _ := openTestDB(t)
		survivor := openSession(t, db)
		// The domain's current scene is the one about to be swallowed, and the read that
		// created it left a round open on it.
		swallowed := openSession(t, db)
		doomed, err := db.Search(memhop.SearchQuery{SceneID: swallowed})
		if err != nil {
			t.Fatalf("open the doomed round: %v", err)
		}
		abandoned := doomed.NewTopicID
		if _, err := db.AppendArchive(planEvent(time.Now().UnixMilli(), "tool_call", "这一轮来不及收口")); err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := db.MergeScenes(survivor, []string{swallowed}); err != nil {
			t.Fatalf("MergeScenes: %v", err)
		}
		// The domain's own memory of the round went with the scene: the refusal says 「no turn
		// is open」 rather than letting the close aim at a scene that no longer exists.
		_, err = turn(db.Session, "想收掉那个被抛下的轮", "答")
		if memhop.CodeOf(err) != memhop.ErrInvalidQuery || !strings.Contains(err.Error(), "no turn is open") {
			t.Fatalf("closing the abandoned round: want ErrInvalidQuery saying \"no turn is open\", got %v", err)
		}
		// A fresh read works on the survivor, with a key of its own.
		next, err := db.Search(memhop.SearchQuery{})
		if err != nil {
			t.Fatalf("Search after the merge: %v", err)
		}
		if next.Scene.SceneID != survivor {
			t.Fatalf("the domain resumed %s, want the survivor %s", next.Scene.SceneID, survivor)
		}
		kind := memhop.KindEvent
		rows, err := db.SearchL4(memhop.L4Query{TopicID: &abandoned, Kind: &kind})
		if err != nil || len(rows) != 1 {
			t.Fatalf("the abandoned round's own record = %+v err %v, want it still readable by the key it was written under", rows, err)
		}
	})
}
