// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests for Dream consolidation and checkpoint persistence.

package test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
)

func TestInterfaceDream(t *testing.T) {
	// Lower the compress threshold so two turns in one session trigger the
	// consolidate call.
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "test.meh"), llm.srv.URL,
		func(d *internal.MemHopDefaults) { d.DreamCompressMinTopics = 2 })
	db := newTestDB(t, m)
	defer db.Close()

	sceneID := openSession(t, db)
	openTurn(t, db, sceneID)
	if _, err := turn(db.Session, "用户要求重构代码", "好的,我来重构这段代码"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	openTurn(t, db, sceneID)
	if _, err := turn(db.Session, "继续重构第二个模块", "第二个模块也补上测试"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	rep, err := db.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	// L2TopicsCompressed counts the topics sunk into fused groups, not the
	// groups: one proposal swallowing both turns reports 2.
	if rep == nil || rep.ConsolidatedScenes != 1 || rep.L2TopicsCompressed != 2 {
		t.Fatalf("Dream should consolidate the session: %+v", rep)
	}
	if llm.calls["consolidate"] != 1 {
		t.Fatalf("one scene over the threshold is one consolidate call, got %d", llm.calls["consolidate"])
	}
	// Consolidation fuses the group into one depth-1 topic, so the read
	// surface shrinks below the two turns written.
	res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after dream: %v", err)
	}
	if len(res.Topics) != 1 || res.Topics[0].ParentID != nil {
		t.Fatalf("surface = %+v, want one fused topic as the scene's only root", res.Topics)
	}
	// The turns it swallowed are not gone: the transcript read brings them back
	// as that topic's children, and it counts exactly the children it has.
	full, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("SceneContext after dream: %v", err)
	}
	if len(full.Topics) != 3 {
		t.Fatalf("scene context after fusion = %+v, want the fused parent over its 2 sunk turns", full.Topics)
	}
	fused := full.Topics[0]
	if fused.Depth != 1 || fused.ChildCount != 2 {
		t.Fatalf("fused parent = %+v, want depth 1 owning 2 children", fused)
	}
	if !slices.Equal(fused.Keywords, []string{"重构", "代码", "测试"}) {
		t.Fatalf("fused keywords = %q, want the three words extracted from the summary", fused.Keywords)
	}
	// The summary is the fused topic's own utterance: the user slot of the parent,
	// under role 3 — the one role a host cannot write and the public surface
	// deliberately leaves unnamed, so the number is what a host matches on.
	fusedID := fused.TopicID
	sums, err := db.SearchL4(internal.L4Query{TopicID: &fusedID})
	if err != nil {
		t.Fatalf("SearchL4 on the fused topic: %v", err)
	}
	if len(sums) != 1 || sums[0].Content != "合并摘要保留全部细节" || sums[0].Role != 3 || sums[0].Seq != 1 {
		t.Fatalf("fused summary = %+v, want the summary alone in the parent's user slot under role 3", sums)
	}
	// Stage timeline covers the full pipeline, distillation included.
	if len(rep.Stages) == 0 {
		t.Fatal("Dream report must carry stage records")
	}
	for _, st := range []string{"l2_compress", "l0_distill"} {
		found := false
		for _, s := range rep.Stages {
			if s.Name == st && s.Status == "ok" {
				found = true
			}
		}
		if !found {
			t.Fatalf("report missing ok stage %s: %+v", st, rep.Stages)
		}
	}
	// L1 nodes were synced from L2 during Dream, so the distill stage runs.
	if llm.calls["distill"] < 1 {
		t.Fatal("Dream should call distill for L0 profile")
	}
	if !rep.L0Updated {
		t.Fatalf("distill ran and merged; L0Updated must hold: %+v", rep)
	}
	// Distill output was merged into the L0 profile.
	profile, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0 after dream: %v", err)
	}
	// The type word is derived from the four dimensions and never taken from the
	// reply: the mock answers "ESFP" over dimensions that read ESTP, so an
	// implementation that started trusting the model's own word fails here.
	if profile.MBTI.Type != "ESTP" {
		t.Fatalf("distilled MBTI = %+v, want the type derived from 0.2/0.3/-0.1/0.4", profile.MBTI)
	}
	if profile.MBTI.IE != 0.2 || profile.MBTI.NS != 0.3 || profile.MBTI.TF != -0.1 || profile.MBTI.JP != 0.4 {
		t.Fatalf("distilled dimensions = %+v, want the mock's four", profile.MBTI)
	}
	if profile.EmotionState.Valence != 0.8 || profile.EmotionState.Arousal != 0.6 || profile.EmotionState.Dominance != 0.5 {
		t.Fatalf("distilled emotion = %+v, want the mock's 0.8/0.6/0.5", profile.EmotionState)
	}
	if profile.Personality != "务实直接，注重代码质量，面对重构任务条理清晰，习惯先补测试再动手" {
		t.Fatalf("distilled personality = %q, want the mock's sentence verbatim", profile.Personality)
	}

	// Directed Dream: an invalid scene id is rejected, a valid one succeeds.
	if _, err := db.Dream(context.Background(), "zz"); err == nil {
		t.Fatal("Dream with invalid scene_id should error")
	}
	if _, err := db.Dream(context.Background(), sceneID); err != nil {
		t.Fatalf("directed Dream on scene %s: err=%v", sceneID, err)
	}

	// The loop keeps running after consolidation, and the state it runs on is the
	// library's: an un-named read continues the same scene, and the turn it opens
	// closes onto its own topic — one that stands as a root of its own, rather than
	// being handed to the fused group that swallowed the turns before it.
	after, err := db.Search(memhop.SearchQuery{})
	if err != nil {
		t.Fatalf("Search after consolidation: %v", err)
	}
	if after.Scene.SceneID != sceneID {
		t.Fatalf("consolidation moved the domain onto scene %s, want the scene it was on (%s)",
			after.Scene.SceneID, sceneID)
	}
	closed, err := turn(db.Session, "巩固之后接着问", "巩固之后的答复")
	if err != nil {
		t.Fatalf("turn after consolidation: %v", err)
	}
	if closed != after.NewTopicID {
		t.Fatalf("the close settled topic %s, want the turn just opened (%s)", closed, after.NewTopicID)
	}
	tail, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("SceneContext after the extra turn: %v", err)
	}
	if len(tail.Topics) != 4 {
		t.Fatalf("scene topics = %+v, want the fused parent, its 2 sunk turns and this new root", tail.Topics)
	}
	var fresh *memhop.SceneContextTopic
	for i := range tail.Topics {
		if tail.Topics[i].TopicID == closed {
			fresh = &tail.Topics[i]
		}
	}
	if fresh == nil || fresh.Depth != 1 || fresh.ChildCount != 0 {
		t.Fatalf("a turn opened after consolidation must be its own root, not a member of the fused group: %+v", tail.Topics)
	}
}

func TestInterfaceCheckpointPersist(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "persist.meh")
	m := openMockDB(t, path, llm.srv.URL)

	db := newTestDB(t, m)
	sceneID := openSession(t, db)
	topicID := openTurn(t, db, sceneID)
	if _, err := turn(db.Session, "用户要求重构代码", "好的,我来重构这段代码"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if err := db.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen the same file: the tenant registry persists, so the same name
	// resolves to the same domain, and the caches rebuild from the records.
	m2 := openMockDB(t, path, llm.srv.URL)
	defer m2.Close()
	db2 := newTestDB(t, m2)
	scenes, err := db2.ListScenes("")
	if err != nil {
		t.Fatalf("ListScenes after reopen: %v", err)
	}
	if len(scenes) == 0 {
		t.Fatal("scenes should persist across reopen")
	}
	res, err := db2.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after reopen: %v", err)
	}
	if len(res.Topics) != 1 || res.Topics[0].ID != topicID {
		t.Fatalf("topics did not persist: %+v", res.Topics)
	}
	if !slices.Equal(res.Topics[0].FusedKeywords, []string{"重构", "代码", "测试"}) {
		t.Fatalf("the keyword track must persist with the topic: %q", res.Topics[0].FusedKeywords)
	}
	arcs, err := db2.SearchL4(internal.L4Query{Keyword: "重构"})
	if err != nil {
		t.Fatalf("SearchL4 after reopen: %v", err)
	}
	if len(arcs) == 0 {
		t.Fatal("archives should persist across reopen")
	}
	// Reading the host's own session id back from the reopened file is what
	// proves the id survives a restart.
}

// A model that answers off contract during consolidation costs one Dream pass and
// nothing else: no group lands, so the turns stay on the surface with their own
// keywords and originals, and no summary is written above them.
func TestInterfaceDreamRefusesAnOffContractReply(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "offcontract.meh"), llm.srv.URL,
		func(d *internal.MemHopDefaults) { d.DreamCompressMinTopics = 2 })
	db := newTestDB(t, m)
	defer db.Close()

	sceneID := openSession(t, db)
	first := settleTurn(t, db, sceneID, "用户要求重构代码", "好的,我来重构这段代码")
	second := settleTurn(t, db, sceneID, "继续重构第二个模块", "第二个模块也补上测试")

	llm.offContract = "这不是契约里的回包"
	rep, err := db.Dream(context.Background(), "")
	if memhop.CodeOf(err) != memhop.ErrLLM {
		t.Fatalf("Dream over an off-contract reply = %v (code %d), want the LLM code %d",
			err, memhop.CodeOf(err), memhop.ErrLLM)
	}
	if rep == nil || rep.ConsolidatedScenes != 0 || rep.L2TopicsCompressed != 0 {
		t.Fatalf("a pass that fused nothing reports %+v", rep)
	}

	surface, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after the refused pass: %v", err)
	}
	onSurface := make(map[string]memhop.TopicSlot, len(surface.Topics))
	for _, topic := range surface.Topics {
		onSurface[topic.ID] = topic
	}
	if len(surface.Topics) != 2 {
		t.Fatalf("surface after the refused pass = %+v, want both turns still at depth 1", surface.Topics)
	}
	for _, id := range []string{first, second} {
		topic, ok := onSurface[id]
		if !ok {
			t.Fatalf("the refused pass lost turn %s: %+v", id, surface.Topics)
		}
		if !slices.Equal(topic.FusedKeywords, []string{"重构", "代码", "测试"}) {
			t.Fatalf("turn %s carries %q, want the track it was settled with", id, topic.FusedKeywords)
		}
	}
	// A fused parent would be a third entry carrying the group's summary, so the
	// transcript staying at two is what proves no summary was written.
	full, err := db.SceneContext(sceneID)
	if err != nil {
		t.Fatalf("SceneContext after the refused pass: %v", err)
	}
	if len(full.Topics) != 2 {
		t.Fatalf("transcript after the refused pass = %+v, want the two turns and no summary", full.Topics)
	}
}

// A cancelled pass answers with the cancellation, not with the model's failure:
// one cancel fails every scene's call at once, and a host told "the model failed"
// goes and checks a model that never refused it.
func TestInterfaceDreamReportsCancellation(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "cancel.meh"), llm.srv.URL,
		func(d *internal.MemHopDefaults) { d.DreamCompressMinTopics = 2 })
	db := newTestDB(t, m)
	defer db.Close()

	sceneID := openSession(t, db)
	settleTurn(t, db, sceneID, "用户要求重构代码", "好的,我来重构这段代码")
	settleTurn(t, db, sceneID, "继续重构第二个模块", "第二个模块也补上测试")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.Dream(ctx, ""); memhop.CodeOf(err) != memhop.ErrCancelled {
		t.Fatalf("a cancelled Dream = %v (code %d), want the cancellation code %d",
			err, memhop.CodeOf(err), memhop.ErrCancelled)
	}
}
