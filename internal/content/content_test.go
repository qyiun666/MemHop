// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package content

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestParseTopicIDRejectsReservedZero(t *testing.T) {
	if _, err := ParseTopicID("0000000000000000"); err == nil {
		t.Fatal("the all-zero topic id is reserved")
	}
	if _, err := ParseTopicID("not-hex"); err == nil {
		t.Fatal("non-hex topic id must be rejected")
	}
	if got, err := ParseTopicID("0000000000000009"); err != nil || got != 9 {
		t.Fatalf("ParseTopicID = %d, %v", got, err)
	}
}

// Every record the append boundary refuses, and why: the kinds own different axes,
// the consolidation role is the library's, and a record over budget is refused
// rather than shortened.
func TestValidateAppendRefusals(t *testing.T) {
	long := strings.Repeat("字", 2048) // 6144 bytes: over the event budget, under the utterance one
	cases := []struct {
		name string
		in   core.ArchiveSlot
	}{
		{"undefined kind", core.ArchiveSlot{Kind: core.ArchiveKind(9), EventType: "x", Content: "c", CreatedAt: 1}},
		{"no content", core.ArchiveSlot{Kind: core.KindEvent, EventType: "x", CreatedAt: 1}},
		{"no timestamp", core.ArchiveSlot{Kind: core.KindEvent, EventType: "x", Content: "c"}},
		{"undefined content type", core.ArchiveSlot{Kind: core.KindUtterance, ContentType: core.ContentType(9), Content: "c", CreatedAt: 1}},
		{"event without a name", core.ArchiveSlot{Kind: core.KindEvent, Content: "c", CreatedAt: 1}},
		{"event over budget", core.ArchiveSlot{Kind: core.KindEvent, EventType: "x", Content: long, CreatedAt: 1}},
		{"utterance with an event name", core.ArchiveSlot{Kind: core.KindUtterance, Role: core.RoleUser, EventType: "x", Content: "c", CreatedAt: 1}},
		{"utterance on a plan step", core.ArchiveSlot{Kind: core.KindUtterance, Role: core.RoleUser, NodeSeq: 1, Content: "c", CreatedAt: 1}},
		{"utterance as the library's dream role", core.ArchiveSlot{Kind: core.KindUtterance, Role: core.RoleDream, Content: "c", CreatedAt: 1}},
		{"utterance with an unnameable role", core.ArchiveSlot{Kind: core.KindUtterance, Role: 77, Content: "c", CreatedAt: 1}},
	}
	for _, tc := range cases {
		if err := ValidateAppend(tc.in); err == nil {
			t.Errorf("%s: want refusal, got nil", tc.name)
		}
	}
	// The same record with the axis it is allowed to carry is accepted, so each
	// refusal above is its own reason and not one blanket check.
	for _, ok := range []core.ArchiveSlot{
		{Kind: core.KindEvent, EventType: "x", Content: "c", CreatedAt: 1},
		{Kind: core.KindUtterance, Role: core.RoleUser, Content: "c", CreatedAt: 1},
		{Kind: core.KindUtterance, Role: core.RoleSystem, ContentType: core.ContentImage, Content: "a.png", CreatedAt: 1},
		// the same payload an event may not carry is fine as an utterance
		{Kind: core.KindUtterance, Role: core.RoleUser, Content: long, CreatedAt: 1},
	} {
		if err := ValidateAppend(ok); err != nil {
			t.Errorf("valid record %+v refused: %v", ok, err)
		}
	}
}

// The transcript a distillation reads is the topic's own content in slot order,
// each line attributed: without the label the sides collapse and the extraction
// loses who asserted what.
func TestRenderForDistillLabelsEveryLineInSeqOrder(t *testing.T) {
	utterances := []core.ArchiveSlot{
		{Seq: 1, Role: core.RoleUser, Content: "问 A"},
		{Seq: 2, Role: core.RoleAgent, Content: "答 A"},
		{Seq: 5, Role: core.RoleSystem, Content: "注入的上下文"},
	}
	want := "User: 问 A\nAssistant: 答 A\nSystem: 注入的上下文"
	if got := RenderForDistill(utterances); got != want {
		t.Fatalf("rendered transcript = %q, want %q", got, want)
	}
	if got := RenderForDistill(nil); got != "" {
		t.Fatalf("no utterances must render empty, got %q", got)
	}
}

// newReadFixture builds a domain whose content mirror the writer keeps current,
// which is what makes a read enumerable at all.
func newReadFixture(t *testing.T) (*core.StorageEngine, *domain.Context) {
	t.Helper()
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, domain.NewContext(core.DefaultAgentID, context.Background(), engine, nil, nil)
}

func writeSlot(t *testing.T, engine *core.StorageEngine, ac *domain.Context, in core.ArchiveSlot) {
	t.Helper()
	if err := repo.AppendArchiveL4(engine, core.DefaultAgentID, ac.L4, &in); err != nil {
		t.Fatalf("archive seq %d: %v", in.Seq, err)
	}
}

// A turn reads question-first by construction, not by tie-break: the user's text
// takes Seq 1 and the reply Seq 2. These are archived in the hostile order, in the
// same millisecond, so only Seq can put them right.
func TestReadOrdersUtterancesBySeqNotWriteOrder(t *testing.T) {
	engine, ac := newReadFixture(t)
	const topicID uint64 = 0xfeed
	writeSlot(t, engine, ac, core.ArchiveSlot{
		TopicID: topicID, Seq: core.SeqAgent, Kind: core.KindUtterance,
		Role: core.RoleAgent, ContentType: core.ContentText, Content: "answer", CreatedAt: 1500,
	})
	writeSlot(t, engine, ac, core.ArchiveSlot{
		TopicID: topicID, Seq: core.SeqUser, Kind: core.KindUtterance,
		Role: core.RoleUser, ContentType: core.ContentText, Content: "question", CreatedAt: 1500,
	})

	got, err := Read(core.DefaultAgentID, ac, topicID, core.KindUtterance)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || got[0].Content != "question" || got[1].Content != "answer" {
		t.Fatalf("turn read %v, want question then answer", got)
	}
	if got[0].Seq != core.SeqUser || got[1].Seq != core.SeqAgent {
		t.Fatalf("Seq must ride along so a gap stays visible: %+v", got)
	}
}

// L4 holds a turn's events beside its originals, so a read that asks for one kind
// must not answer with the other: a conversation shown with a line per recorded
// operation is not the same text.
func TestReadUtterancesExcludesEvents(t *testing.T) {
	engine, ac := newReadFixture(t)
	const topicID uint64 = 0xfeed
	writeSlot(t, engine, ac, core.ArchiveSlot{
		TopicID: topicID, Seq: core.SeqUser, Kind: core.KindUtterance,
		Role: core.RoleUser, ContentType: core.ContentText, Content: "question", CreatedAt: 1001,
	})
	for seq := uint64(3); seq < 33; seq++ {
		writeSlot(t, engine, ac, core.ArchiveSlot{
			TopicID: topicID, Seq: seq, Kind: core.KindEvent, EventType: "tool_call",
			ContentType: core.ContentText, Content: "an operation", CreatedAt: int64(1000 + seq),
		})
	}
	writeSlot(t, engine, ac, core.ArchiveSlot{
		TopicID: topicID, Seq: core.SeqAgent, Kind: core.KindUtterance,
		Role: core.RoleAgent, ContentType: core.ContentText, Content: "answer", CreatedAt: 1002,
	})

	got, err := Read(core.DefaultAgentID, ac, topicID, core.KindUtterance)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d records, want the two originals only", len(got))
	}
}

// A hole in Seq is what a reclaimed slot leaves behind. It is a legal end state for
// an old turn, so the read reports it as two records with a gap in their Seq rather
// than as a failure — and a reader can tell the two apart afterwards.
func TestReadReportsSeqGaps(t *testing.T) {
	engine, ac := newReadFixture(t)
	const topicID uint64 = 0xfeed
	for _, seq := range []uint64{core.SeqUser, 3} {
		writeSlot(t, engine, ac, core.ArchiveSlot{
			TopicID: topicID, Seq: seq, Kind: core.KindUtterance,
			Role: core.RoleUser, ContentType: core.ContentText, Content: "kept", CreatedAt: 1000,
		})
	}

	got, err := Read(core.DefaultAgentID, ac, topicID, core.KindUtterance)
	if err != nil {
		t.Fatalf("a reclaimed slot is not a read failure: %v", err)
	}
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 3 {
		t.Fatalf("gap not visible in the reported Seq: %+v", got)
	}
}
