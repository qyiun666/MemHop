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
	t.Cleanup(func() { engine.Close() })
	return engine
}

// A turn's reading order is question-first by construction, not by tie-break: the
// user's text sits on Seq 1 and the reply on Seq 2. This case writes them in the
// hostile order — answer archived first, same millisecond — so the read has to
// follow Seq rather than any timestamp-then-role sort.
func TestSceneContextTopicOrdersBySeqNotWriteOrder(t *testing.T) {
	engine := newTestEngine(t)
	ac := newTestContext(t, engine)
	const topicID uint64 = 0xfeed
	const ts int64 = 1500

	for _, in := range []repo.ArchiveContent{
		{TopicID: topicID, Seq: core.SeqAgent, Kind: core.KindUtterance, Role: core.RoleAgent, Type: core.ContentText, Text: "answer", CreatedAt: ts},
		{TopicID: topicID, Seq: core.SeqUser, Kind: core.KindUtterance, Role: core.RoleUser, Type: core.ContentText, Text: "question", CreatedAt: ts},
	} {
		if _, err := repo.AppendArchiveL4(engine, core.DefaultAgentID, ac.L4, in); err != nil {
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
		t.Fatalf("turn read answer-first: %+v", st.Messages)
	}
	if st.Messages[0].Seq != core.SeqUser || st.Messages[1].Seq != core.SeqAgent {
		t.Fatalf("Seq must ride along so a gap is visible: %+v", st.Messages)
	}
}

// L4 holds a turn's events beside its originals. A conversation context is the
// dialogue, so the event kind must stay out of it — reading them in would show
// dozens of lines for a two-line turn.
func TestSceneContextTopicExcludesEvents(t *testing.T) {
	engine := newTestEngine(t)
	ac := newTestContext(t, engine)
	const topicID uint64 = 0xfeed

	write := func(seq uint64, kind core.ArchiveKind, text string) {
		t.Helper()
		if _, err := repo.AppendArchiveL4(engine, core.DefaultAgentID, ac.L4, repo.ArchiveContent{
			TopicID: topicID, Seq: seq, Kind: kind, Type: core.ContentText,
			EventType: "tool_call", Text: text, CreatedAt: 1000 + int64(seq),
		}); err != nil {
			t.Fatalf("write %s slot %d: %v", kind, seq, err)
		}
	}
	write(core.SeqUser, core.KindUtterance, "question")
	for seq := uint64(3); seq < 33; seq++ {
		write(seq, core.KindEvent, "an operation")
	}
	write(core.SeqAgent, core.KindUtterance, "answer")

	st, err := ContextTopic(ac, core.DefaultAgentID,
		core.TopicSlot{ID: topicID, SceneID: 0xbeef, Depth: 1}, nil)
	if err != nil {
		t.Fatalf("ContextTopic: %v", err)
	}
	if len(st.Messages) != 2 {
		t.Fatalf("messages = %d, want the two originals only: %+v", len(st.Messages), st.Messages)
	}
}

// A gap in Seq is how a reclaimed utterance looks. It is a legal end state for an
// old turn, so it must not be an error — and it must be distinguishable, which is
// what the Seq on each message is for.
func TestSceneContextTopicExposesSeqGaps(t *testing.T) {
	engine := newTestEngine(t)
	ac := newTestContext(t, engine)
	const topicID uint64 = 0xfeed

	for _, seq := range []uint64{core.SeqUser, 3} {
		if _, err := repo.AppendArchiveL4(engine, core.DefaultAgentID, ac.L4, repo.ArchiveContent{
			TopicID: topicID, Seq: seq, Kind: core.KindUtterance, Type: core.ContentText,
			Text: "kept", CreatedAt: 1000,
		}); err != nil {
			t.Fatalf("write slot %d: %v", seq, err)
		}
	}
	st, err := ContextTopic(ac, core.DefaultAgentID,
		core.TopicSlot{ID: topicID, SceneID: 0xbeef, Depth: 1}, nil)
	if err != nil {
		t.Fatalf("a reclaimed slot is not a read failure: %v", err)
	}
	if len(st.Messages) != 2 || st.Messages[0].Seq != 1 || st.Messages[1].Seq != 3 {
		t.Fatalf("gap not visible in the reported Seq: %+v", st.Messages)
	}
}
