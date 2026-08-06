// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package l2

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// newTestEngine creates a storage engine backed by a temp file.
func newTestEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	engine, err := storage.Create(filepath.Join(t.TempDir(), "l2.meh"), 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close(&storage.IndexSnapshotData{}) })
	return engine
}

// mustCreateScene creates a scene and returns its formatted id.
func mustCreateScene(t *testing.T, engine *storage.StorageEngine, name string) string {
	t.Helper()
	sceneID, err := CreateScene(engine, name)
	if err != nil {
		t.Fatal(err)
	}
	return hash.FormatHash(sceneID)
}

// mustCreateTopic creates a topic and returns its formatted id.
func mustCreateTopic(t *testing.T, engine *storage.StorageEngine, sceneID string, userTS, agentTS int64, l3Refs []uint64) string {
	t.Helper()
	id, err := CreateTopic(engine, sceneID, []string{fmt.Sprintf("kw-%d", userTS)}, nil, userTS, agentTS)
	if err != nil {
		t.Fatal(err)
	}
	if len(l3Refs) > 0 {
		topic, err := record.ReadTopicSlot(engine, id)
		if err != nil {
			t.Fatal(err)
		}
		topic.L3Refs = l3Refs
		if err := record.WriteTopicSlot(engine, id, topic); err != nil {
			t.Fatal(err)
		}
	}
	return hash.FormatHash(id)
}

// parseIDOrZero parses a formatted hash id in tests, failing on error.
func parseIDOrZero(t *testing.T, id string) uint64 {
	t.Helper()
	h, err := hash.ParseID(id)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// ============================================================================
// CreateScene / ListScenes
// ============================================================================

func TestCreateAndListScenes(t *testing.T) {
	engine := newTestEngine(t)
	sceneA := mustCreateScene(t, engine, "scene-a")
	sceneB := mustCreateScene(t, engine, "scene-b")

	scenes := ListScenes(engine, []string{sceneA, sceneB, hash.FormatHash(hash.HashID("missing"))})
	if len(scenes) != 2 {
		t.Fatalf("expected 2 scenes, got %v", scenes)
	}
	if scenes[0].SceneName != "scene-a" || scenes[1].SceneName != "scene-b" {
		t.Errorf("scene names mismatch: %+v", scenes)
	}
}

// ============================================================================
// CreateTopic / ListTopics
// ============================================================================

func TestCreateAndListTopics(t *testing.T) {
	engine := newTestEngine(t)
	sceneA := mustCreateScene(t, engine, "scene-a")
	sceneB := mustCreateScene(t, engine, "scene-b")

	t1 := mustCreateTopic(t, engine, sceneA, 300, 400, nil)
	t2 := mustCreateTopic(t, engine, sceneA, 100, 200, nil)
	_ = mustCreateTopic(t, engine, sceneB, 500, 600, nil)

	topics, err := ListTopics(engine, sceneA, 0) // depth==0 -> 默认 1
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 2 {
		t.Fatalf("expected 2 topics in scene A, got %v", topics)
	}
	// 按 UserTimestamp 升序：t2(100) 在 t1(300) 之前
	if topics[0].ID != parseIDOrZero(t, t2) || topics[1].ID != parseIDOrZero(t, t1) {
		t.Errorf("topics not sorted by UserTimestamp: %+v", topics)
	}

	// depth=2 没有话题
	deep, err := ListTopics(engine, sceneA, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(deep) != 0 {
		t.Errorf("expected no depth-2 topics, got %v", deep)
	}
}

// ============================================================================
// CompressTopics
// ============================================================================

func TestCompressTopics(t *testing.T) {
	engine := newTestEngine(t)
	sceneA := mustCreateScene(t, engine, "scene-a")

	t1 := mustCreateTopic(t, engine, sceneA, 300, 400, []uint64{10, 20})
	t2 := mustCreateTopic(t, engine, sceneA, 100, 500, []uint64{20, 30})
	parent := mustCreateTopic(t, engine, sceneA, 0, 0, nil)

	res, err := CompressTopics(engine, []uint64{
		parseIDOrZero(t, t1), parseIDOrZero(t, t2),
	}, parseIDOrZero(t, parent))
	if err != nil {
		t.Fatal(err)
	}

	// L3Refs 合体去重
	if len(res.L3Refs) != 3 || res.L3Refs[0] != 10 || res.L3Refs[1] != 20 || res.L3Refs[2] != 30 {
		t.Errorf("L3Refs mismatch: %v", res.L3Refs)
	}
	// 最早 UserTimestamp / 最晚 AgentTimestamp
	if res.UserTimestamp != 100 || res.AgentTimestamp != 500 {
		t.Errorf("timestamps mismatch: user=%d agent=%d", res.UserTimestamp, res.AgentTimestamp)
	}

	// 挂载：ParentID 改写 + Depth+1
	for _, idStr := range []string{t1, t2} {
		topic, err := record.ReadTopicSlot(engine, parseIDOrZero(t, idStr))
		if err != nil {
			t.Fatal(err)
		}
		if topic.ParentID == nil || *topic.ParentID != parseIDOrZero(t, parent) {
			t.Errorf("topic %s parent not set: %+v", idStr, topic.ParentID)
		}
		if topic.Depth != 2 {
			t.Errorf("topic %s depth should be 2, got %d", idStr, topic.Depth)
		}
	}
}

func TestCompressTopicsDeletesAtMaxDepth(t *testing.T) {
	engine := newTestEngine(t)
	sceneA := mustCreateScene(t, engine, "scene-a")
	parent := mustCreateTopic(t, engine, sceneA, 0, 0, nil)

	// 直接构造 depth=3 的话题，下沉后 depth=4 应被删除
	deep := model.TopicSlot{
		ID:             hash.HashID("deep-topic"),
		SceneID:        parseIDOrZero(t, sceneA),
		Depth:          3,
		UserTimestamp:  100,
		AgentTimestamp: 200,
	}
	if err := record.WriteTopicSlot(engine, deep.ID, &deep); err != nil {
		t.Fatal(err)
	}

	res, err := CompressTopics(engine, []uint64{deep.ID}, parseIDOrZero(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	if engine.Contains(deep.ID) {
		t.Error("depth-4 topic should be deleted")
	}
	// 被删话题的聚合值仍收集
	if res.UserTimestamp != 100 || res.AgentTimestamp != 200 {
		t.Errorf("timestamps mismatch: user=%d agent=%d", res.UserTimestamp, res.AgentTimestamp)
	}
}

// ============================================================================
// ListAllTopics
// ============================================================================

func TestListAllTopics(t *testing.T) {
	engine := newTestEngine(t)
	sceneA := mustCreateScene(t, engine, "scene-a")
	sceneB := mustCreateScene(t, engine, "scene-b")
	// 乱序创建，验证按 UserTimestamp 升序
	mustCreateTopic(t, engine, sceneA, 300, 400, nil)
	mustCreateTopic(t, engine, sceneB, 100, 200, nil)
	mustCreateTopic(t, engine, sceneA, 200, 300, nil)

	all := ListAllTopics(engine)
	if len(all) != 3 {
		t.Fatalf("expected 3 topics, got %d", len(all))
	}
	if all[0].UserTimestamp != 100 || all[1].UserTimestamp != 200 || all[2].UserTimestamp != 300 {
		t.Errorf("topics not sorted by UserTimestamp: %+v", all)
	}
}

// ============================================================================
// DeleteL2
// ============================================================================

func TestDeleteScenes(t *testing.T) {
	engine := newTestEngine(t)
	sceneA := mustCreateScene(t, engine, "scene-a")
	sceneB := mustCreateScene(t, engine, "scene-b")
	tA1 := mustCreateTopic(t, engine, sceneA, 100, 200, nil)
	tA2 := mustCreateTopic(t, engine, sceneA, 300, 400, nil)
	tB1 := mustCreateTopic(t, engine, sceneB, 500, 600, nil)

	if !DeleteL2(engine, []string{sceneA}, 1) {
		t.Fatal("DeleteL2(scene) returned false")
	}
	// 场景 A 的话题与场景记录都删掉
	for _, idStr := range []string{tA1, tA2, sceneA} {
		if engine.Contains(parseIDOrZero(t, idStr)) {
			t.Errorf("%s should be deleted", idStr)
		}
	}
	// 场景 B 及其话题保留
	if !engine.Contains(parseIDOrZero(t, tB1)) || !engine.Contains(parseIDOrZero(t, sceneB)) {
		t.Error("scene B should survive")
	}
}

func TestDeleteTopics(t *testing.T) {
	engine := newTestEngine(t)
	sceneA := mustCreateScene(t, engine, "scene-a")
	t1 := mustCreateTopic(t, engine, sceneA, 100, 200, nil)
	t2 := mustCreateTopic(t, engine, sceneA, 300, 400, nil)

	if !DeleteL2(engine, []string{t1}, 2) {
		t.Fatal("DeleteL2(topic) returned false")
	}
	if engine.Contains(parseIDOrZero(t, t1)) {
		t.Error("topic 1 should be deleted")
	}
	if !engine.Contains(parseIDOrZero(t, t2)) {
		t.Error("topic 2 should survive")
	}
	if !engine.Contains(parseIDOrZero(t, sceneA)) {
		t.Error("scene should survive")
	}
}

// ============================================================================
// MergeScenes
// ============================================================================

func TestMergeScenes(t *testing.T) {
	engine := newTestEngine(t)
	sceneA := mustCreateScene(t, engine, "scene-a")
	sceneB := mustCreateScene(t, engine, "scene-b")
	_ = mustCreateTopic(t, engine, sceneA, 100, 200, nil)
	tB1 := mustCreateTopic(t, engine, sceneB, 300, 400, nil)
	tB2 := mustCreateTopic(t, engine, sceneB, 500, 600, nil)

	if !MergeScenes(engine, sceneA, []string{sceneB}) {
		t.Fatal("MergeScenes returned false")
	}

	// 副场景话题改挂主场景
	for _, idStr := range []string{tB1, tB2} {
		topic, err := record.ReadTopicSlot(engine, parseIDOrZero(t, idStr))
		if err != nil {
			t.Fatal(err)
		}
		if topic.SceneID != parseIDOrZero(t, sceneA) {
			t.Errorf("topic %s should belong to scene A, got %d", idStr, topic.SceneID)
		}
	}
	// 副场景记录删除，主场景保留
	if engine.Contains(parseIDOrZero(t, sceneB)) {
		t.Error("secondary scene should be deleted")
	}
	if !engine.Contains(parseIDOrZero(t, sceneA)) {
		t.Error("primary scene should survive")
	}
	// 主场景话题数 = 3
	topics, err := ListTopics(engine, sceneA, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 3 {
		t.Errorf("expected 3 topics in scene A, got %d", len(topics))
	}
}
