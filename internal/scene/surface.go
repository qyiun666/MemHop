// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scene

import (
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SurfaceTopics returns one scene's depth-1 topics in turn order, served from
// the L2Meta cache so a read costs no record scan; ties break by ID to keep the
// order deterministic.
func SurfaceTopics(ac *domain.Context, sceneID uint64) []core.TopicSlot {
	out := make([]core.TopicSlot, 0, 16)
	for _, id := range ac.L2Meta.GetByScene(sceneID) {
		meta := ac.L2Meta.Get(id)
		if meta == nil || meta.Depth != 1 {
			continue
		}
		out = append(out, meta.ToTopicSlot())
	}
	slices.SortFunc(out, core.CompareTopicOrder)
	return out
}

// ContextTopic renders one topic of a scene context: its keyword track, child
// count, and the utterances it owns, in the Seq order they were written to.
//
// The utterances come from the topic's own content read, already ordered and
// already judged: which ids a topic owns is the mirror's answer, and only it can
// tell a reclaimed slot from a missing record. What rides along on every message is
// the Seq itself, precisely so that a gap in it stays distinguishable from a turn
// that never said those words.
func ContextTopic(t core.TopicSlot, children map[uint64]int, utterances []core.ArchiveSlot) core.SceneContextTopic {
	st := core.SceneContextTopic{
		TopicID:    common.FormatHash(t.ID),
		Depth:      int(t.Depth),
		Name:       t.Name,
		Keywords:   slices.Clone(t.FusedKeywords),
		ChildCount: children[t.ID],
		Messages:   make([]core.SceneMessage, 0, len(utterances)),
	}
	for _, arc := range utterances {
		st.Messages = append(st.Messages, core.SceneMessage{
			Role: arc.Role, Type: arc.ContentType, Content: arc.Content,
			Seq: arc.Seq, CreatedAt: arc.CreatedAt,
		})
	}
	return st
}
