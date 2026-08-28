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
	sceneID, err := repo.CreateSceneL2(engine, core.DefaultAgentID, "append-scene")
	if err != nil {
		t.Fatalf("create scene: %v", err)
	}
	topicID := common.HashID("append-topic")
	if !repo.CreateTopicL2WithID(engine, core.DefaultAgentID, sceneID, topicID, []string{"kw"}, 1000, 0) {
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
	db := newTestDB(t, engine)
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
		id, err := db.AppendL4Message(core.DefaultAgentID, topicID, m.text, m.ts, m.role, core.ContentText)
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
	got := repo.QueryArchiveL4(engine, core.DefaultAgentID, 3, "", 0, 0, ids)
	if len(got) != 3 {
		t.Fatalf("read-back: %d archives, want 3", len(got))
	}
	for i, m := range msgs {
		if got[i].Role != m.role || got[i].Content != m.text || got[i].CreatedAt != m.ts {
			t.Errorf("archive %d = %+v, want role=%d text=%q ts=%d", i, got[i], m.role, m.text, m.ts)
		}
	}

	// Topic L4Refs must contain all three ids (append, not overwrite).
	topicHash, _ := common.ParseID(topicID)
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{Engine: engine, SceneID: topicHash, Num: 3})
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
	db := newTestDB(t, engine)
	topicID := newTopicForAppend(t, engine)

	if _, err := db.AppendL4Message(core.DefaultAgentID, topicID, "", 1000, core.RoleUser, core.ContentText); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("empty text: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.AppendL4Message(core.DefaultAgentID, topicID, "x", 0, core.RoleUser, core.ContentText); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("zero timestamp: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.AppendL4Message(core.DefaultAgentID, topicID, "x", 1000, core.RoleDream+1, core.ContentText); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("undefined role: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.AppendL4Message(core.DefaultAgentID, topicID, "x", 1000, core.RoleUser, core.ContentType(6)); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("undefined content type: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.AppendL4Message(core.DefaultAgentID, "nothex", "x", 1000, core.RoleUser, core.ContentText); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("malformed topic id: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.AppendL4Message(core.DefaultAgentID, common.FormatHash(common.HashID("missing")), "x", 1000, core.RoleUser, core.ContentText); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("missing topic: want ErrNotFound, got %v", err)
	}
	if out := repo.QueryArchiveL4(engine, core.DefaultAgentID, 2, "", 0, 5000, nil); len(out) != 0 {
		t.Fatalf("failed appends left orphan archives: %+v", out)
	}
}

// TestAppendL4MessageContentType: a non-text content type round-trips
// through the archive record and an undefined value is rejected.
func TestAppendL4MessageContentType(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topicID := newTopicForAppend(t, engine)

	id, err := db.AppendL4Message(core.DefaultAgentID, topicID, "img://cat.png", 2000, core.RoleUser, core.ContentImage)
	if err != nil {
		t.Fatalf("AppendL4Message(image): %v", err)
	}
	arc, err := db.GetArchive(core.DefaultAgentID, common.FormatHash(id))
	if err != nil {
		t.Fatalf("GetArchive: %v", err)
	}
	if arc.ContentType != core.ContentImage {
		t.Fatalf("content type = %v, want image", arc.ContentType)
	}
}

// TestSearchL4TypeFilter: the optional content-type filter narrows results
// within the query modes.
func TestSearchL4TypeFilter(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topicID := newTopicForAppend(t, engine)

	if _, err := db.AppendL4Message(core.DefaultAgentID, topicID, "文字内容", 2000, core.RoleUser, core.ContentText); err != nil {
		t.Fatalf("append text: %v", err)
	}
	if _, err := db.AppendL4Message(core.DefaultAgentID, topicID, "img://cat.png", 3000, core.RoleUser, core.ContentImage); err != nil {
		t.Fatalf("append image: %v", err)
	}
	img := core.ContentImage
	got, err := db.SearchL4(core.DefaultAgentID, L4Query{Start: 1000, End: 4000, Type: &img})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	if len(got) != 1 || got[0].ContentType != core.ContentImage {
		t.Fatalf("type filter: %+v, want only the image archive", got)
	}
}
