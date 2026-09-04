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
// order must still be question-first. L4Refs are stored id-sorted and an
// archive id hashes from (topic, timestamp, content), so the fixture picks the
// case where that order says "answer first" — only the role tie-break can
// rescue it.
func TestSceneContextTopicOrdersSameTimestampByRole(t *testing.T) {
	engine := newTestEngine(t)
	const topicID uint64 = 0xfeed
	const ts int64 = 1500

	var userRef, agentRef uint64
	var userText, agentText string
	for i := 0; i < 64; i++ {
		suffix := string(rune('a'+i%26)) + string(rune('0'+i/26))
		userText, agentText = "question "+suffix, "answer "+suffix
		u, err := repo.AppendArchiveL4(engine, core.DefaultAgentID, topicID, core.RoleUser, core.ContentText, userText, ts)
		if err != nil {
			t.Fatalf("archive question: %v", err)
		}
		a, err := repo.AppendArchiveL4(engine, core.DefaultAgentID, topicID, core.RoleAgent, core.ContentText, agentText, ts)
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

	st, err := ContextTopic(engine, core.DefaultAgentID,
		core.TopicSlot{ID: topicID, SceneID: 0xbeef, Depth: 1, L4Refs: []uint64{agentRef, userRef}}, nil)
	if err != nil {
		t.Fatalf("ContextTopic: %v", err)
	}
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
