// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// TestListScenesEmpty empty db returns an empty slice.
func TestListScenesEmpty(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	scenes, err := db.ListScenes()
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
	db := &DB{engine: engine}
	s1 := core.NewSceneSlot("工作")
	s2 := core.NewSceneSlot("学习")
	if err := core.WriteSceneSlot(engine, s1.SceneID, &s1); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteSceneSlot(engine, s2.SceneID, &s2); err != nil {
		t.Fatal(err)
	}
	scenes, err := db.ListScenes()
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
	db := &DB{engine: engine}
	primary := core.NewSceneSlot("主场景")
	secondary := core.NewSceneSlot("副场景")
	if err := core.WriteSceneSlot(engine, primary.SceneID, &primary); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteSceneSlot(engine, secondary.SceneID, &secondary); err != nil {
		t.Fatal(err)
	}
	t1 := newTopic(common.HashID("t1"), secondary.SceneID, 1000, []string{"a"})
	t2 := newTopic(common.HashID("t2"), secondary.SceneID, 2000, []string{"b"})
	if err := core.WriteTopicSlot(engine, t1.ID, &t1); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteTopicSlot(engine, t2.ID, &t2); err != nil {
		t.Fatal(err)
	}

	if err := db.MergeScenes(common.FormatHash(primary.SceneID), []string{common.FormatHash(secondary.SceneID)}); err != nil {
		t.Fatalf("MergeScenes: %v", err)
	}
	// Secondary scene record deleted.
	if _, err := core.ReadSceneSlot(engine, secondary.SceneID); err == nil {
		t.Fatal("secondary scene should be deleted")
	}
	// Primary scene remains.
	if _, err := core.ReadSceneSlot(engine, primary.SceneID); err != nil {
		t.Fatal("primary scene should remain")
	}
	// Topic ownership migrated.
	for _, id := range []uint64{t1.ID, t2.ID} {
		topics, err := core.ReadTopicSlot(engine, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(topics) == 0 || topics[0].SceneID != primary.SceneID {
			t.Fatalf("topic %d scene: want %d", id, primary.SceneID)
		}
	}
}

// TestMergeScenesInvalid invalid primary ID and empty secondary list error.
func TestMergeScenesInvalid(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	if err := db.MergeScenes("nothex", []string{"abc"}); err == nil {
		t.Fatal("want error for invalid primary id")
	}
	if err := db.MergeScenes(common.FormatHash(1), nil); err == nil {
		t.Fatal("want error for empty secondary ids")
	}
}

// TestMergeScenesPrimaryInSecondary primary must never be deleted by a merge.
func TestMergeScenesPrimaryInSecondary(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	primary := core.NewSceneSlot("主场景")
	secondary := core.NewSceneSlot("副场景")
	if err := core.WriteSceneSlot(engine, primary.SceneID, &primary); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteSceneSlot(engine, secondary.SceneID, &secondary); err != nil {
		t.Fatal(err)
	}
	t1 := newTopic(common.HashID("t1"), primary.SceneID, 1000, []string{"a"})
	t2 := newTopic(common.HashID("t2"), secondary.SceneID, 2000, []string{"b"})
	if err := core.WriteTopicSlot(engine, t1.ID, &t1); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteTopicSlot(engine, t2.ID, &t2); err != nil {
		t.Fatal(err)
	}

	// primary listed among secondaries -> rejected, nothing deleted.
	if err := db.MergeScenes(common.FormatHash(primary.SceneID), []string{
		common.FormatHash(primary.SceneID), common.FormatHash(secondary.SceneID),
	}); err == nil {
		t.Fatal("want error when primary is also a secondary")
	}
	if _, err := core.ReadSceneSlot(engine, primary.SceneID); err != nil {
		t.Fatal("primary scene must remain after rejected merge")
	}
	if _, err := core.ReadSceneSlot(engine, secondary.SceneID); err != nil {
		t.Fatal("secondary scene must remain after rejected merge")
	}
	for _, id := range []uint64{t1.ID, t2.ID} {
		if _, err := core.ReadTopicSlot(engine, id); err != nil {
			t.Fatalf("topic %d must remain after rejected merge", id)
		}
	}
}

// TestMergeScenesRemovesActiveScene merged secondary scenes drop from the active list.
func TestMergeScenesRemovesActiveScene(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	primary := core.NewSceneSlot("主场景")
	secondary := core.NewSceneSlot("副场景")
	if err := core.WriteSceneSlot(engine, primary.SceneID, &primary); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteSceneSlot(engine, secondary.SceneID, &secondary); err != nil {
		t.Fatal(err)
	}
	db.activeScenes = []uint64{primary.SceneID, secondary.SceneID}
	if err := db.MergeScenes(common.FormatHash(primary.SceneID), []string{common.FormatHash(secondary.SceneID)}); err != nil {
		t.Fatal(err)
	}
	if len(db.activeScenes) != 1 || db.activeScenes[0] != primary.SceneID {
		t.Fatalf("active scenes: want [%d], got %v", primary.SceneID, db.activeScenes)
	}
}

// TestListScenesTopicCounts: ListScenes fills TopicCount with depth-1 root
// topics only; compressed (depth>=2) nodes do not inflate it, and scenes
// without topics report 0.
func TestListScenesTopicCounts(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	s1 := core.NewSceneSlot("场景一")
	s2 := core.NewSceneSlot("场景二")
	s3 := core.NewSceneSlot("空场景")
	for _, s := range []core.SceneSlot{s1, s2, s3} {
		if err := core.WriteSceneSlot(engine, s.SceneID, &s); err != nil {
			t.Fatal(err)
		}
	}
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
		if err := core.WriteTopicSlot(engine, tp.ID, &tp); err != nil {
			t.Fatal(err)
		}
	}
	scenes, err := db.ListScenes()
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
