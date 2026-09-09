// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scene

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func newTestEngine(t *testing.T) *core.StorageEngine {
	t.Helper()
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { engine.Close(nil) })
	return engine
}

// When a host stamps both sides of a turn the same millisecond, the reading
// order must still be question-first. The archive index hands a topic's records
// back in write order, so the adversarial case is an answer archived before its
// question — only the role tie-break can rescue it.
func TestSceneContextTopicOrdersSameTimestampByRole(t *testing.T) {
	engine := newTestEngine(t)
	ac := newTestContext(t, engine)
	const topicID uint64 = 0xfeed
	const ts int64 = 1500

	for _, in := range []repo.ArchiveContent{
		{TopicID: topicID, Role: core.RoleAgent, Type: core.ContentText, Text: "answer", CreatedAt: ts},
		{TopicID: topicID, Role: core.RoleUser, Type: core.ContentText, Text: "question", CreatedAt: ts},
	} {
		if _, err := repo.AppendArchiveL4(engine, core.DefaultAgentID, ac.Arch, in); err != nil {
			t.Fatalf("archive %q: %v", in.Text, err)
		}
	}
	st, err := ContextTopic(ac, core.DefaultAgentID,
		core.TopicSlot{ID: topicID, SceneID: 0xbeef, Depth: 1}, nil)
	if err != nil {
		t.Fatalf("ContextTopic: %v", err)
	}
	if len(st.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(st.Messages))
	}
	if st.Messages[0].Content != "question" || st.Messages[1].Content != "answer" {
		t.Fatalf("same-millisecond turn read answer-first: %+v", st.Messages)
	}
}

// A resumed topic reads question-first: the timestamp decides, and when a host
// stamped both sides of a turn the same millisecond the role decides — never
// the arbitrary order the archive ids happen to hash into.
func TestSortSceneMessagesSpeakingOrder(t *testing.T) {
	same := []core.SceneMessage{
		{Role: core.RoleAgent, Content: "answer", CreatedAt: 1500},
		{Role: core.RoleUser, Content: "question", CreatedAt: 1500},
	}
	sortMessages(same)
	if same[0].Content != "question" || same[1].Content != "answer" {
		t.Fatalf("same-millisecond turn not question-first: %+v", same)
	}

	across := []core.SceneMessage{
		{Role: core.RoleUser, Content: "next question", CreatedAt: 2000},
		{Role: core.RoleAgent, Content: "earlier answer", CreatedAt: 1000},
	}
	sortMessages(across)
	if across[0].Content != "earlier answer" {
		t.Fatalf("role tie-break overrode the timestamps: %+v", across)
	}
}
