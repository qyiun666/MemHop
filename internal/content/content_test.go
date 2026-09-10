// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package content

import (
	"strings"
	"testing"

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

func TestTrimByBudgetKeepsNewest(t *testing.T) {
	events := []core.ArchiveSlot{
		{Content: strings.Repeat("a", 60)},
		{Content: strings.Repeat("b", 60)},
		{Content: strings.Repeat("c", 60)},
	}
	if got := TrimByBudget(events, 100); len(got) != 1 || got[0].Content[0] != 'c' {
		t.Fatalf("trim = %+v, want only the newest event", got)
	}
	if got := TrimByBudget(events, 1000); len(got) != 3 {
		t.Fatalf("under budget must keep all: %+v", got)
	}
	if got := TrimByBudget(events, 1); len(got) != 1 || got[0].Content[0] != 'c' {
		t.Fatalf("tiny budget must still keep the newest: %+v", got)
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
