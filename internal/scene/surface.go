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
// count, and its L4 messages. An archive ref that names no record is reported
// in L4IDs without a message (a replayed Update legally retires the ids of the
// turn it replaced); an archive that cannot be read is an error, because a
// transcript missing one utterance looks exactly like a complete one.
func ContextTopic(engine *core.StorageEngine, agentID uint64, t core.TopicSlot, children map[uint64]int) (core.SceneContextTopic, error) {
	st := core.SceneContextTopic{
		TopicID:    common.FormatHash(t.ID),
		Depth:      int(t.Depth),
		Keywords:   slices.Clone(t.FusedKeywords),
		ChildCount: children[t.ID],
		L4IDs:      make([]string, 0, len(t.L4Refs)),
	}
	for _, ref := range t.L4Refs {
		st.L4IDs = append(st.L4IDs, common.FormatHash(ref))
		arc, err := core.ReadArchiveSlot(engine, agentID, ref)
		if err != nil {
			if common.CodeOf(err) == common.ErrNotFound {
				continue
			}
			return core.SceneContextTopic{}, err
		}
		st.Messages = append(st.Messages, core.SceneMessage{Role: arc.Role, Type: arc.ContentType, Content: arc.Content, CreatedAt: arc.CreatedAt})
	}
	// L4Refs are persisted id-sorted, which says nothing about who spoke
	// first; a resumed conversation still has to read question-first.
	sortMessages(st.Messages)
	return st, nil
}

// sortMessages puts a topic's L4 messages in speaking order: by timestamp,
// with Role breaking ties (RoleUser precedes RoleAgent) because a host may
// stamp both sides of a turn the same millisecond and the id order that
// L4Refs carry is arbitrary.
func sortMessages(msgs []core.SceneMessage) {
	slices.SortStableFunc(msgs, func(a, b core.SceneMessage) int {
		if c := cmp.Compare(a.CreatedAt, b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.Role, b.Role)
	})
}
