// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// mustScene writes a scene record under a host-chosen id and returns it.
func mustScene(t *testing.T, engine *core.StorageEngine, sceneID uint64, name string) core.SceneSlot {
	t.Helper()
	slot := core.NewSceneSlot(sceneID, name)
	if err := core.WriteSceneSlot(engine, core.DefaultAgentID, sceneID, &slot); err != nil {
		t.Fatalf("write scene %d: %v", sceneID, err)
	}
	return slot
}

// TestListScenesEmpty empty db returns an empty slice.
func TestListScenesEmpty(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	scenes, err := db.ListScenes(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if len(scenes) != 0 {
		t.Fatalf("want 0 scenes, got %d", len(scenes))
	}
}

// TestListScenesReturnsIDName multiple scenes return scene_id + scene_name.
func TestListScenesReturnsIDName(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	s1 := mustScene(t, engine, 11, "工作")
	s2 := mustScene(t, engine, 12, "学习")

	scenes, err := db.ListScenes(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if len(scenes) != 2 {
		t.Fatalf("want 2 scenes, got %d", len(scenes))
	}
	byID := make(map[uint64]string, len(scenes))
	for _, s := range scenes {
		byID[s.SceneID] = s.SceneName
	}
	if byID[s1.SceneID] != "工作" || byID[s2.SceneID] != "学习" {
		t.Fatalf("unexpected scenes: %v", byID)
	}
}

// TestMergeScenesMovesTopics topics move to primary, secondary deleted, primary kept.
func TestMergeScenesMovesTopics(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	primary := mustScene(t, engine, 21, "主场景")
	secondary := mustScene(t, engine, 22, "副场景")

	t1 := newTopic(common.HashID("t1"), secondary.SceneID, 1000, []string{"a"})
	t2 := newTopic(common.HashID("t2"), secondary.SceneID, 2000, []string{"b"})
	for _, tp := range []core.TopicSlot{t1, t2} {
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, tp.ID, &tp); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.MergeScenes(core.DefaultAgentID, common.FormatHash(primary.SceneID),
		[]string{common.FormatHash(secondary.SceneID)}); err != nil {
		t.Fatalf("MergeScenes: %v", err)
	}
	if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, secondary.SceneID); err == nil {
		t.Fatal("secondary scene should be deleted")
	}
	if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, primary.SceneID); err != nil {
		t.Fatal("primary scene should remain")
	}
	for _, id := range []uint64{t1.ID, t2.ID} {
		topics, err := core.ReadTopicSlot(engine, core.DefaultAgentID, id)
		if err != nil {
			t.Fatal(err)
		}
		if topics == nil || topics.SceneID != primary.SceneID {
			t.Fatalf("topic %d scene: want %d", id, primary.SceneID)
		}
	}
}

// Merging also retargets the cached entries, otherwise the scene read keeps
// serving topics under a scene id that no longer exists.
func TestMergeScenesRetargetsCache(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	primary := mustScene(t, engine, 31, "主场景")
	secondary := mustScene(t, engine, 32, "副场景")
	topic := newTopic(common.HashID("cached"), secondary.SceneID, 1000, []string{"a"})
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topic.ID, &topic); err != nil {
		t.Fatal(err)
	}
	ac := testDefaultContext(db)
	ac.SyncL2Meta(topic.ID)

	if err := db.MergeScenes(core.DefaultAgentID, common.FormatHash(primary.SceneID),
		[]string{common.FormatHash(secondary.SceneID)}); err != nil {
		t.Fatalf("MergeScenes: %v", err)
	}
	if got := ac.L2Meta.Get(topic.ID); got == nil || got.SceneID != primary.SceneID {
		t.Fatalf("cache not retargeted: %+v", got)
	}
	if ids := ac.L2Meta.GetByScene(secondary.SceneID); len(ids) != 0 {
		t.Fatalf("merged scene still cached: %v", ids)
	}
}

// TestMergeScenesInvalid invalid primary ID and empty secondary list error.
func TestMergeScenesInvalid(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.MergeScenes(core.DefaultAgentID, "nothex", []string{"abc"}); err == nil {
		t.Fatal("want error for invalid primary id")
	}
	if err := db.MergeScenes(core.DefaultAgentID, common.FormatHash(1), nil); err == nil {
		t.Fatal("want error for empty secondary ids")
	}
}

// TestMergeScenesPrimaryInSecondary primary must never be deleted by a merge.
func TestMergeScenesPrimaryInSecondary(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	primary := mustScene(t, engine, 41, "主场景")
	secondary := mustScene(t, engine, 42, "副场景")
	t1 := newTopic(common.HashID("t1"), primary.SceneID, 1000, []string{"a"})
	t2 := newTopic(common.HashID("t2"), secondary.SceneID, 2000, []string{"b"})
	for _, tp := range []core.TopicSlot{t1, t2} {
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, tp.ID, &tp); err != nil {
			t.Fatal(err)
		}
	}

	// primary listed among secondaries -> rejected, nothing deleted.
	if err := db.MergeScenes(core.DefaultAgentID, common.FormatHash(primary.SceneID), []string{
		common.FormatHash(primary.SceneID), common.FormatHash(secondary.SceneID),
	}); err == nil {
		t.Fatal("want error when primary is also a secondary")
	}
	for _, s := range []core.SceneSlot{primary, secondary} {
		if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, s.SceneID); err != nil {
			t.Fatalf("scene %d must survive a rejected merge", s.SceneID)
		}
	}
	for _, id := range []uint64{t1.ID, t2.ID} {
		if _, err := core.ReadTopicSlot(engine, core.DefaultAgentID, id); err != nil {
			t.Fatalf("topic %d must remain after rejected merge", id)
		}
	}
}

// TestListScenesTopicCounts: ListScenes fills TopicCount with depth-1 root
// topics only; compressed (depth>=2) nodes do not inflate it, and scenes
// without topics report 0.
func TestListScenesTopicCounts(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	s1 := mustScene(t, engine, 51, "场景一")
	s2 := mustScene(t, engine, 52, "场景二")
	s3 := mustScene(t, engine, 53, "空场景")

	// s1: two depth-1 roots.
	t1 := newTopic(common.HashID("s1t1"), s1.SceneID, 1000, []string{"a"})
	t2 := newTopic(common.HashID("s1t2"), s1.SceneID, 2000, []string{"b"})
	// s2: one root + one compressed node (depth 2, parented).
	t3 := newTopic(common.HashID("s2t1"), s2.SceneID, 3000, []string{"c"})
	parent := common.HashID("s2t2-parent")
	t4 := newTopic(common.HashID("s2t2"), s2.SceneID, 4000, []string{"d"})
	t4.Depth = 2
	t4.ParentID = &parent
	for _, tp := range []core.TopicSlot{t1, t2, t3, t4} {
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, tp.ID, &tp); err != nil {
			t.Fatal(err)
		}
	}
	scenes, err := db.ListScenes(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	got := map[uint64]int{}
	for _, s := range scenes {
		got[s.SceneID] = s.TopicCount
	}
	if got[s1.SceneID] != 2 {
		t.Errorf("场景一: want 2, got %d", got[s1.SceneID])
	}
	if got[s2.SceneID] != 1 {
		t.Errorf("场景二: want 1 (depth-1 roots only), got %d", got[s2.SceneID])
	}
	if got[s3.SceneID] != 0 {
		t.Errorf("空场景: want 0, got %d", got[s3.SceneID])
	}
}

// TestDeleteTopicRemovesSubtreeAndArchives deleting a topic removes its
// subtree, the referenced L4 archives, and its L2Meta entries.
func TestDeleteTopicRemovesSubtreeAndArchives(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	ac := testDefaultContext(db)
	scene := mustScene(t, engine, 61, "工作")

	parentID := common.HashID("parent")
	childID := common.HashID("child")
	arcID := common.HashID("arc:1")
	parent := newTopic(parentID, scene.SceneID, 1000, []string{"a"})
	parent.L4Refs = []uint64{arcID}
	parent.ChildrenIDs = []uint64{childID}
	child := newTopic(childID, scene.SceneID, 2000, []string{"b"})
	child.ParentID = &parentID
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, parentID, &parent); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, childID, &child); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, arcID, &core.ArchiveSlot{
		IDHash: arcID, ContextID: parentID, Content: "原文", CreatedAt: 1500,
	}); err != nil {
		t.Fatal(err)
	}
	ac.L2Meta.Update(index.L2MetaFromTopic(&parent))
	ac.L2Meta.Update(index.L2MetaFromTopic(&child))

	if err := db.DeleteTopic(core.DefaultAgentID, common.FormatHash(parentID)); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	for _, id := range []uint64{parentID, childID} {
		if topics, err := core.ReadTopicSlot(engine, core.DefaultAgentID, id); err == nil && topics != nil {
			t.Errorf("topic %d should be deleted", id)
		}
		if ac.L2Meta.Get(id) != nil {
			t.Errorf("l2meta entry %d should be removed", id)
		}
	}
	if arcs, err := core.ReadArchiveSlot(engine, core.DefaultAgentID, arcID); err == nil && arcs != nil {
		t.Error("archive should be deleted")
	}
	// The scene record survives a topic deletion.
	if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, scene.SceneID); err != nil {
		t.Error("scene should survive topic deletion")
	}
}

// TestDeleteTopicNotFound missing topic returns ErrNotFound.
func TestDeleteTopicNotFound(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.DeleteTopic(core.DefaultAgentID, common.FormatHash(common.HashID("ghost"))); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestDeleteSceneNotFound missing scene returns ErrNotFound (consistent
// with DeleteTopic).
func TestDeleteSceneNotFound(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.DeleteScene(core.DefaultAgentID, common.FormatHash(common.HashID("ghost-scene"))); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestDeleteTopicPrunesParentChild deleting a child removes it from the
// surviving parent's ChildrenIDs.
func TestDeleteTopicPrunesParentChild(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	_ = testDefaultContext(db)
	scene := mustScene(t, engine, 71, "工作")

	parentID := common.HashID("parent")
	childID := common.HashID("child")
	parent := newTopic(parentID, scene.SceneID, 1000, []string{"a"})
	parent.ChildrenIDs = []uint64{childID}
	child := newTopic(childID, scene.SceneID, 2000, []string{"b"})
	child.ParentID = &parentID
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, parentID, &parent); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, childID, &child); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteTopic(core.DefaultAgentID, common.FormatHash(childID)); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	stored, err := core.ReadTopicSlot(engine, core.DefaultAgentID, parentID)
	if err != nil || stored == nil {
		t.Fatalf("parent should survive: %v", err)
	}
	for _, id := range stored.ChildrenIDs {
		if id == childID {
			t.Fatal("parent ChildrenIDs must not reference the deleted child")
		}
	}
}

// TestDeleteSceneRemovesEverything deleting a scene removes its record, all
// topics (all depths), archives and their L2Meta entries.
func TestDeleteSceneRemovesEverything(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	ac := testDefaultContext(db)
	scene := mustScene(t, engine, 3, "工作")

	t1 := newTopic(common.HashID("t1"), scene.SceneID, 1000, []string{"a"})
	t2 := newTopic(common.HashID("t2"), scene.SceneID, 2000, []string{"b"})
	t3 := newTopic(common.HashID("t3"), scene.SceneID, 3000, []string{"c"})
	t3.ParentID = &t2.ID
	arcID := common.HashID("arc:2")
	if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, arcID, &core.ArchiveSlot{
		IDHash: arcID, ContextID: t1.ID, Content: "原文", CreatedAt: 1500,
	}); err != nil {
		t.Fatal(err)
	}
	t1.L4Refs = []uint64{arcID}
	for _, topic := range []core.TopicSlot{t1, t2, t3} {
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topic.ID, &topic); err != nil {
			t.Fatal(err)
		}
		ac.L2Meta.Update(index.L2MetaFromTopic(&topic))
	}

	if err := db.DeleteScene(core.DefaultAgentID, common.FormatHash(scene.SceneID)); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}
	if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, scene.SceneID); err == nil {
		t.Error("scene record should be deleted")
	}
	for _, topic := range []core.TopicSlot{t1, t2, t3} {
		if topics, err := core.ReadTopicSlot(engine, core.DefaultAgentID, topic.ID); err == nil && topics != nil {
			t.Errorf("topic %d should be deleted", topic.ID)
		}
		if ac.L2Meta.Get(topic.ID) != nil {
			t.Errorf("l2meta entry %d should be removed", topic.ID)
		}
	}
	if arcs, err := core.ReadArchiveSlot(engine, core.DefaultAgentID, arcID); err == nil && arcs != nil {
		t.Error("archive should be deleted")
	}
}

// A scene is named by the library when it is created; SetSceneName is the
// host's only way to title one. The title must survive a later Search, which
// rewrites that very record to bump its hit counter and turn sequence.
func TestSetSceneNameSurvivesLaterTurns(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)

	res, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("open scene: %v", err)
	}
	sceneHex := common.FormatHash(res.Scene.SceneID)
	if _, err := db.Update(core.DefaultAgentID, turnOf(res.Scene.SceneID, res.NewTopicID)); err != nil {
		t.Fatalf("settle turn: %v", err)
	}
	if err := db.SetSceneName(core.DefaultAgentID, sceneHex, "rust 学习"); err != nil {
		t.Fatalf("SetSceneName: %v", err)
	}
	again, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: sceneHex})
	if err != nil {
		t.Fatalf("reopen scene: %v", err)
	}
	if again.Scene.SceneName != "rust 学习" {
		t.Fatalf("name after reopen = %q, want the title to persist", again.Scene.SceneName)
	}
	if again.Scene.HitCount <= res.Scene.HitCount {
		t.Fatalf("reopen did not bump HitCount: %d -> %d", res.Scene.HitCount, again.Scene.HitCount)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("LLM calls = %d, want 1 (only Update distills)", got)
	}
	if err := db.SetSceneName(core.DefaultAgentID, sceneHex, ""); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Errorf("empty title: want ErrInvalidQuery, got %v", err)
	}
	if err := db.SetSceneName(core.DefaultAgentID, common.FormatHash(common.HashID("ghost")), "x"); common.CodeOf(err) != common.ErrNotFound {
		t.Errorf("unknown scene: want ErrNotFound, got %v", err)
	}
}

// When a host stamps both sides of a turn the same millisecond, the reading
// order must still be question-first. L4Refs are stored id-sorted and an
// archive id hashes from (topic, timestamp, content), so the fixture picks the
// case where that order says "answer first" — only the role tie-break can
// rescue it.
func TestSceneContextTopicOrdersSameTimestampByRole(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	const topicID uint64 = 0xfeed
	const ts int64 = 1500

	var userRef, agentRef uint64
	var userText, agentText string
	for i := 0; i < 64; i++ {
		suffix := string(rune('a'+i%26)) + string(rune('0'+i/26))
		userText, agentText = "question "+suffix, "answer "+suffix
		u, err := repo.AppendArchiveL4(db.engine, core.DefaultAgentID, topicID, core.RoleUser, core.ContentText, userText, ts)
		if err != nil {
			t.Fatalf("archive question: %v", err)
		}
		a, err := repo.AppendArchiveL4(db.engine, core.DefaultAgentID, topicID, core.RoleAgent, core.ContentText, agentText, ts)
		if err != nil {
			t.Fatalf("archive answer: %v", err)
		}
		if a < u {
			userRef, agentRef = u, a
			break
		}
	}
	if userRef == 0 {
		t.Fatal("fixture lost its teeth: no candidate archived the answer before the question")
	}

	st := db.sceneContextTopic(core.DefaultAgentID,
		core.TopicSlot{ID: topicID, SceneID: 0xbeef, Depth: 1, L4Refs: []uint64{agentRef, userRef}}, nil)
	if len(st.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(st.Messages))
	}
	if st.Messages[0].Content != userText || st.Messages[1].Content != agentText {
		t.Fatalf("same-millisecond turn read answer-first: %+v", st.Messages)
	}
}

// A resumed topic reads question-first: the timestamp decides, and when a host
// stamped both sides of a turn the same millisecond the role decides — never
// the arbitrary order the archive ids happen to hash into.
func TestSortSceneMessagesSpeakingOrder(t *testing.T) {
	same := []SceneMessage{
		{Role: core.RoleAgent, Content: "answer", CreatedAt: 1500},
		{Role: core.RoleUser, Content: "question", CreatedAt: 1500},
	}
	sortSceneMessages(same)
	if same[0].Content != "question" || same[1].Content != "answer" {
		t.Fatalf("same-millisecond turn not question-first: %+v", same)
	}

	across := []SceneMessage{
		{Role: core.RoleUser, Content: "next question", CreatedAt: 2000},
		{Role: core.RoleAgent, Content: "earlier answer", CreatedAt: 1000},
	}
	sortSceneMessages(across)
	if across[0].Content != "earlier answer" {
		t.Fatalf("role tie-break overrode the timestamps: %+v", across)
	}
}
