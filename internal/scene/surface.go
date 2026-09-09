// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scene

import (
	"cmp"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SurfaceTopics returns one scene's depth-1 topics in turn order: the
// read surface a host injects as its conversation context. It is served from
// the L2Meta cache, so a read costs no record scan; ties break by ID to keep
// the order deterministic.
func SurfaceTopics(ac *domain.Context, sceneID uint64) []core.TopicSlot {
	out := make([]core.TopicSlot, 0, 16)
	for _, id := range ac.L2Meta.GetByScene(sceneID) {
		meta := ac.L2Meta.Get(id)
		if meta == nil || meta.Depth != 1 {
			continue
		}
		out = append(out, meta.ToTopicSlot())
	}
	slices.SortFunc(out, func(a, b core.TopicSlot) int {
		if a.UserTimestamp != b.UserTimestamp {
			return cmp.Compare(a.UserTimestamp, b.UserTimestamp)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

// ContextTopic renders one topic of a scene context: its keyword track, child
// count, and the L4 archives it owns. The domain's archive index is what makes
// a topic's own content findable — an archive id hashes its text, so nothing
// derives it from the topic. An archive the index names but the engine cannot
// read is an error, not a shorter transcript: a conversation missing one
// utterance looks exactly like a complete one.
func ContextTopic(ac *domain.Context, agentID uint64, t core.TopicSlot, children map[uint64]int) (core.SceneContextTopic, error) {
	refs := ac.Arch.Hashes(t.ID)
	st := core.SceneContextTopic{
		TopicID:    common.FormatHash(t.ID),
		Depth:      int(t.Depth),
		Keywords:   slices.Clone(t.FusedKeywords),
		ChildCount: children[t.ID],
		Messages:   make([]core.SceneMessage, 0, len(refs)),
	}
	for _, ref := range refs {
		arc, err := core.ReadArchiveSlot(ac.Engine, agentID, ref)
		if err != nil {
			if common.CodeOf(err) == common.ErrNotFound {
				return core.SceneContextTopic{}, common.NewError(common.ErrIO,
					"archive index names a missing record", err)
			}
			return core.SceneContextTopic{}, err
		}
		st.Messages = append(st.Messages, core.SceneMessage{Role: arc.Role, Type: arc.ContentType, Content: arc.Content, CreatedAt: arc.CreatedAt})
	}
	// The index lists a topic's archives by creation time, which does not say
	// who spoke first; a resumed conversation still has to read question-first.
	sortMessages(st.Messages)
	return st, nil
}

// sortMessages puts a topic's L4 messages in speaking order: by timestamp,
// with Role breaking ties (RoleUser precedes RoleAgent) because a host may
// stamp both sides of a turn the same millisecond and creation time alone
// cannot order them.
func sortMessages(msgs []core.SceneMessage) {
	slices.SortStableFunc(msgs, func(a, b core.SceneMessage) int {
		if c := cmp.Compare(a.CreatedAt, b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.Role, b.Role)
	})
}
