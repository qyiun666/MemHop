// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scene

import (
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Each utterance keeps the Seq it came from — the only thing that tells a reclaimed
// line from one never spoken — and the keyword track is a copy, not a window into the
// record.
func TestContextTopicRendersTheTopicAndTheUtterancesGiven(t *testing.T) {
	const (
		topicID uint64 = 0xfeed
		childID uint64 = 0xbeef
	)
	keywords := []string{"原始", "关键词"}
	st := ContextTopic(
		core.TopicSlot{
			ID: topicID, SceneID: childID, Depth: 2, Name: "那一轮",
			FusedKeywords: keywords,
		},
		map[uint64]int{topicID: 3},
		[]core.ArchiveSlot{
			{Seq: 1, Role: core.RoleUser, ContentType: core.ContentText, Content: "问", CreatedAt: 100},
			{Seq: 4, Role: core.RoleAgent, ContentType: core.ContentImage, Content: "a.png", CreatedAt: 400},
		},
	)

	if st.TopicID != common.FormatHash(topicID) || st.Depth != 2 || st.Name != "那一轮" {
		t.Fatalf("topic fields lost: %+v", st)
	}
	if st.ChildCount != 3 {
		t.Fatalf("child count = %d, want the number this topic's parent holds", st.ChildCount)
	}
	if len(st.Messages) != 2 || st.Messages[1].Seq != 4 || st.Messages[1].Type != core.ContentImage {
		t.Fatalf("utterances not paired with their slot and medium: %+v", st.Messages)
	}
	if !slices.Equal(st.Keywords, keywords) {
		t.Fatalf("keyword track = %v, want %v", st.Keywords, keywords)
	}
	st.Keywords[0] = "改过的"
	if keywords[0] != "原始" {
		t.Fatal("the view aliases the record's keyword slice")
	}
	if got := ContextTopic(core.TopicSlot{ID: topicID}, nil, nil); len(got.Messages) != 0 {
		t.Fatalf("a topic handed no utterances must render none: %+v", got.Messages)
	}
}
