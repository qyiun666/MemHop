// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 topic record primitives: listing, creation and the read-modify-write
// mutations of the keyword track and parent link. Scene primitives
// stay in l2layer.go.
package repo

import (
	"cmp"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// TopicListQuery carries ListTopicsL2 inputs. MetaIdx is the L2MetaIndex mirror
// the listing reads: it is the same table the scene read path serves, so a
// listing never disagrees with what a scene shows, and SceneID is the scene
// whose topics come back.
type TopicListQuery struct {
	MetaIdx *index.L2MetaIndex
	SceneID uint64
	Depth   uint8
}

// ListTopicsL2 lists one scene's topics up to depth. depth is clamped to
// [1, MaxDepth]; results sorted by UserTimestamp.
func ListTopicsL2(q TopicListQuery) []core.TopicSlot {
	depth := q.Depth
	if depth == 0 {
		depth = 1
	} else if depth > MaxDepth {
		depth = MaxDepth
	}
	var out []core.TopicSlot
	for _, meta := range q.MetaIdx.TopicsByScene(q.SceneID) {
		if meta.Depth > depth {
			continue
		}
		out = append(out, meta.ToTopicSlot())
	}
	slices.SortFunc(out, func(a, b core.TopicSlot) int {
		if c := cmp.Compare(a.UserTimestamp, b.UserTimestamp); c != 0 {
			return c
		}
		// A fused parent is stamped with the group's earliest user timestamp,
		// so it ties with the first turn it swallowed. Shallower first keeps the
		// group's summary introducing its own originals instead of landing in
		// the middle of them at the sort's whim. A later pass that folds this
		// parent into a bigger group brings them level, and then the id decides —
		// still a fixed order, just no longer a meaningful one.
		if c := cmp.Compare(a.Depth, b.Depth); c != 0 {
			return c
		}
		// What ties on both keys is two topics at the same depth sharing one user
		// timestamp — and a fused parent is stamped with its group's earliest
		// turn's timestamp, so any same-depth topic holding that instant ties with
		// it. Without a final key the order is whatever the record scan yielded.
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

// RenameTopicL2 writes a caller-chosen name onto one topic. The record is
// rewritten whole, so the keyword track and the parent link survive untouched. A
// topic that is not there is an error rather than a new record: inventing one
// would leave a topic with no scene, no depth and no keywords behind an id
// nothing else refers to.
func RenameTopicL2(engine *core.StorageEngine, agentID uint64, topicID uint64, name string) (*core.TopicSlot, error) {
	topic, err := core.ReadTopicSlot(engine, agentID, topicID)
	if err != nil {
		return nil, err
	}
	topic.Name = name
	if err := core.WriteTopicSlot(engine, agentID, topic.ID, topic); err != nil {
		return nil, err
	}
	return topic, nil
}

// CreateTurnTopicL2 writes one turn topic under sceneHash with its single keyword
// track and both message timestamps. It is also the replay path: settling a topic
// id that already holds a topic rewrites the engine-owned half, so the host's own
// label and the place Dream gave that turn are read off the stored record and
// carried forward — re-settling the turn it was named in must not unname it, and a
// turn already sunk under a fused group must not come back to the surface beside
// the summary that replaced it. A stored record that cannot be read refuses the
// settle: writing depth 1 over a position nobody knows would put a second version
// of that turn on the read path.
func CreateTurnTopicL2(engine *core.StorageEngine, agentID uint64, sceneHash, topicID uint64, keywords []string, userTS, agentTS int64) (*core.TopicSlot, error) {
	topic := core.TopicSlot{
		ID:             topicID,
		SceneID:        sceneHash,
		Depth:          1,
		FusedKeywords:  keywords,
		UserTimestamp:  userTS,
		AgentTimestamp: agentTS,
	}
	stored, err := core.ReadTopicLenient(engine, agentID, topicID)
	if err != nil && common.CodeOf(err) != common.ErrNotFound {
		// Nothing stored is this turn's first settle; a record that will not read
		// back is a turn whose place in the tree nobody knows, and guessing it
		// could put two versions of one turn on the read path.
		return nil, err
	}
	if stored != nil {
		topic.Name = stored.Name
		topic.Depth = stored.Depth
		topic.ParentID = stored.ParentID
	}
	if err := core.WriteTopicSlot(engine, agentID, topic.ID, &topic); err != nil {
		return nil, err
	}
	return &topic, nil
}

// CreateFusedTopicL2 creates a compressed topic (depth 1) whose Keywords are
// the fusion of its children. The group's reconstructed text is an ordinary L4
// archive under the parent's own id, so the topic needs no follow-up write.
// The children are addressed by their own ParentID, never by a list here.
func CreateFusedTopicL2(engine *core.StorageEngine, agentID uint64, sceneID uint64, fusedKeywords []string, userTS, agentTS int64) error {
	topic := core.TopicSlot{
		ID:             core.ComputeTopicID(sceneID, userTS, agentTS),
		SceneID:        sceneID,
		Depth:          1,
		UserTimestamp:  userTS,
		AgentTimestamp: agentTS,
		FusedKeywords:  fusedKeywords,
	}
	return core.WriteTopicSlot(engine, agentID, topic.ID, &topic)
}
