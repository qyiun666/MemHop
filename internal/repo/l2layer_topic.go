// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 topic record primitives: listing, creation and the read-modify-write
// mutations of the keyword track and parent link. Scene primitives
// stay in l2layer.go.
package repo

import (
	"cmp"
	"slices"

	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// TopicListQuery carries ListTopicsL2 inputs. MetaIdx is the L2MetaIndex
// cache: when set, candidates are rebuilt from it instead of unmarshalling
// every topic record; a nil MetaIdx falls back to the full record scan
// with identical semantics. ByScene restricts the listing to SceneID; unset
// lists the whole domain.
type TopicListQuery struct {
	Engine  *core.StorageEngine
	AgentID uint64
	MetaIdx *index.L2MetaIndex
	SceneID uint64
	Depth   uint8
	ByScene bool
}

// ListTopicsL2 lists the topics of one scene (ByScene) or of the whole domain,
// up to depth. depth is clamped to [1, MaxDepth]; results sorted by
// UserTimestamp.
func ListTopicsL2(q TopicListQuery) ([]core.TopicSlot, error) {
	depth := q.Depth
	if depth == 0 {
		depth = 1
	} else if depth > MaxDepth {
		depth = MaxDepth
	}
	var out []core.TopicSlot
	if q.MetaIdx != nil {
		for _, meta := range q.MetaIdx.Iter() {
			if meta.Depth > depth {
				continue
			}
			if q.ByScene && meta.SceneID != q.SceneID {
				continue
			}
			out = append(out, meta.ToTopicSlot())
		}
	} else {
		for _, topic := range core.CollectAllTopics(q.Engine, q.AgentID) {
			if topic.Depth > depth {
				continue
			}
			if q.ByScene && topic.SceneID != q.SceneID {
				continue
			}
			out = append(out, topic)
		}
	}
	slices.SortFunc(out, func(a, b core.TopicSlot) int {
		if c := cmp.Compare(a.UserTimestamp, b.UserTimestamp); c != 0 {
			return c
		}
		// A fused parent is stamped with the group's earliest user timestamp,
		// so it ties with the first turn it swallowed. Shallower first keeps the
		// group's summary introducing its own originals instead of landing in
		// the middle of them at the sort's whim.
		return cmp.Compare(a.Depth, b.Depth)
	})
	return out, nil
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

// CreateTurnTopicL2 writes one turn topic (depth 1) under sceneHash with its
// single keyword track and both message timestamps. It is also the replay path:
// settling a topic id that already holds a topic rewrites the engine-owned half,
// so the host's own label is read off the stored record and carried forward —
// otherwise re-settling the turn it was named in would silently unname it.
func CreateTurnTopicL2(engine *core.StorageEngine, agentID uint64, sceneHash, topicID uint64, keywords []string, userTS, agentTS int64) bool {
	topic := core.TopicSlot{
		ID:             topicID,
		SceneID:        sceneHash,
		Depth:          1,
		FusedKeywords:  keywords,
		UserTimestamp:  userTS,
		AgentTimestamp: agentTS,
	}
	if stored, err := core.ReadTopicLenient(engine, agentID, topicID); err == nil && stored != nil {
		topic.Name = stored.Name
	}
	return core.WriteTopicSlot(engine, agentID, topic.ID, &topic) == nil
}

// CreateFusedTopicL2 creates a compressed topic (depth 1) whose Keywords are
// the fusion of its children. The group's reconstructed text is an ordinary L4
// archive under the parent's own id, so the topic needs no follow-up write.
// The children are addressed by their own ParentID, never by a list here.
func CreateFusedTopicL2(engine *core.StorageEngine, agentID uint64, sceneID uint64, fusedKeywords []string, userTS, agentTS int64) bool {
	topic := core.TopicSlot{
		ID:             core.ComputeTopicID(sceneID, userTS, agentTS),
		SceneID:        sceneID,
		Depth:          1,
		UserTimestamp:  userTS,
		AgentTimestamp: agentTS,
		FusedKeywords:  fusedKeywords,
	}
	return core.WriteTopicSlot(engine, agentID, topic.ID, &topic) == nil
}
