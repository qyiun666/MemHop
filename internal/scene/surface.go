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
// count, and the utterances it owns. The domain's content index is what makes a
// topic's own content findable, and it names the topic's slots in Seq order — the
// order the originals were spoken in, with nothing to tie-break on.
//
// Two read outcomes look alike and must not be judged alike. A slot the index
// names that the engine cannot read is mirror drift: the transcript would be short
// one line and read as complete, so it is a hard ErrIO. A topic whose content is
// empty, or whose Seq has a gap in it, is a turn the retention window has
// already reclaimed — a legal end state, reported as what it is rather than as a
// failure. Seq rides along on every message precisely so that gap stays
// distinguishable from a turn that never said those words.
func ContextTopic(ac *domain.Context, agentID uint64, t core.TopicSlot, children map[uint64]int) (core.SceneContextTopic, error) {
	refs := ac.L4.IDs(t.ID, core.KindUtterance)
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
					"content index names a missing record", err)
			}
			return core.SceneContextTopic{}, err
		}
		st.Messages = append(st.Messages, core.SceneMessage{
			Role: arc.Role, Type: arc.ContentType, Content: arc.Content,
			Seq: arc.Seq, CreatedAt: arc.CreatedAt,
		})
	}
	return st, nil
}
