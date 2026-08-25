// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// newTopicForAppend creates a scene and a depth-1 topic with a known ID,
// mirroring what Search produces for a topic to append to.
func newTopicForAppend(t *testing.T, engine *core.StorageEngine) string {
	t.Helper()
	sceneID, err := repo.CreateSceneL2(engine, "append-scene")
	if err != nil {
		t.Fatalf("create scene: %v", err)
	}
	topicID := common.HashID("append-topic")
	if !repo.CreateTopicL2WithID(engine, sceneID, topicID, []string{"kw"}, 1000, 0) {
		t.Fatal("create topic")
	}
	return common.FormatHash(topicID)
}

// TestAppendL4Message appends user/agent messages to one topic: the
// returned ids are distinct, each archive round-trips with the right
// role/content/timestamp, and all ids land in the topic's L4Refs (append
// semantics, not overwrite).
func TestAppendL4Message(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	topicID := newTopicForAppend(t, engine)

	type msg struct {
		text string
		ts   int64
		role uint8
	}
	msgs := []msg{
		{"用户第一条", 2000, core.RoleUser},
		{"agent 回复", 3000, core.RoleAgent},
		{"用户补充", 4000, core.RoleUser},
	}
	var ids []uint64
	for _, m := range msgs {
		id, err := db.AppendL4Message(topicID, m.text, m.ts, m.role)
		if err != nil {
			t.Fatalf("AppendL4Message(%q): %v", m.text, err)
		}
		if id == 0 {
			t.Fatalf("AppendL4Message(%q): zero id", m.text)
		}
		if slices.Contains(ids, id) {
			t.Fatalf("AppendL4Message(%q): duplicate id %d", m.text, id)
		}
		ids = append(ids, id)
	}

	// Read-back by id: role, content and timestamp must be exact.
	got := repo.QueryArchiveL4(engine, 3, "", 0, 0, []string{
		common.FormatHash(ids[0]), common.FormatHash(ids[1]), common.FormatHash(ids[2]),
	})
	if len(got) != 3 {
		t.Fatalf("read-back: %d archives, want 3", len(got))
	}
	for i, m := range msgs {
		if got[i].Role != m.role || got[i].Content != m.text || got[i].CreatedAt != m.ts {
			t.Errorf("archive %d = %+v, want role=%d text=%q ts=%d", i, got[i], m.role, m.text, m.ts)
		}
	}

	// Topic L4Refs must contain all three ids (append, not overwrite).
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{Engine: engine, SceneID: topicID, Num: 3})
	if err != nil {
		t.Fatalf("list topic: %v", err)
	}
	if len(topics) != 1 {
		t.Fatalf("topics = %d, want 1", len(topics))
	}
	if len(topics[0].L4Refs) != 3 {
		t.Fatalf("L4Refs = %v, want 3 refs", topics[0].L4Refs)
	}
	for _, id := range ids {
		if !slices.Contains(topics[0].L4Refs, id) {
			t.Errorf("L4Refs %v missing %d", topics[0].L4Refs, id)
		}
	}
}

// TestAppendL4MessageErrors: empty text, non-positive timestamp, undefined
// role and a malformed topic id are rejected; a missing topic fails before
// any write, so no orphan L4 archive is left behind.
func TestAppendL4MessageErrors(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	topicID := newTopicForAppend(t, engine)

	if _, err := db.AppendL4Message(topicID, "", 1000, core.RoleUser); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("empty text: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.AppendL4Message(topicID, "x", 0, core.RoleUser); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("zero timestamp: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.AppendL4Message(topicID, "x", 1000, core.RoleDream+1); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("undefined role: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.AppendL4Message("nothex", "x", 1000, core.RoleUser); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("malformed topic id: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.AppendL4Message(common.FormatHash(common.HashID("missing")), "x", 1000, core.RoleUser); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("missing topic: want ErrNotFound, got %v", err)
	}
	if out := repo.QueryArchiveL4(engine, 2, "", 0, 5000, nil); len(out) != 0 {
		t.Fatalf("failed appends left orphan archives: %+v", out)
	}
}
