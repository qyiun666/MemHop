// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scene

import (
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SurfaceTopics returns one scene's depth-1 topics in the order they were spoken: the
// user timestamp, with the topic id breaking a tie, so a batch of turns stamped with
// one time comes back in the same order on every read. Served from the L2Meta cache,
// so a read costs no record scan.
func SurfaceTopics(ac *domain.Context, sceneID uint64) []core.TopicSlot {
	metas := ac.L2Meta.TopicsByScene(sceneID)
	out := make([]core.TopicSlot, 0, len(metas))
	for _, meta := range metas {
		if meta.Depth != 1 {
			continue
		}
		out = append(out, meta.ToTopicSlot())
	}
	slices.SortFunc(out, core.CompareTopicOrder)
	return out
}

// ContextTopic renders one topic of a scene context: its keyword track, child count,
// own time bounds, and the utterances it owns, in the Seq order they were written to.
//
// The utterances come from the topic's own content read: which ids a topic owns is
// the mirror's answer, and only it can tell a reclaimed slot from a missing record.
// Every message carries its Seq — the slot this line holds in a space the topic's
// events share with it — so a reader sees which of a turn's slots came back without
// the list's length having to carry that.
func ContextTopic(t core.TopicSlot, children map[uint64]int, utterances []core.ArchiveSlot) core.SceneContextTopic {
	// Cloned non-nil: every list of the DTO this fills answers as [], and an empty
	// track cloned is the one place that could answer as nothing at all.
	keywords := make([]string, len(t.FusedKeywords))
	copy(keywords, t.FusedKeywords)
	st := core.SceneContextTopic{
		TopicID:    common.FormatHash(t.ID),
		Depth:      int(t.Depth),
		Name:       t.Name,
		Keywords:   keywords,
		ChildCount: children[t.ID],
		Messages:   make([]core.SceneMessage, 0, len(utterances)),
		// The row's own bounds travel with it: a reader dates this topic even after
		// every message it owned has been swept, and no other pure read has them.
		UserTimestamp:  t.UserTimestamp,
		AgentTimestamp: t.AgentTimestamp,
	}
	for _, arc := range utterances {
		st.Messages = append(st.Messages, core.SceneMessage{
			Role: arc.Role, Type: arc.ContentType, Content: arc.Content,
			Seq: arc.Seq, CreatedAt: arc.CreatedAt,
		})
	}
	return st
}
