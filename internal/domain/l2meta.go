// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package domain

import (
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// SyncL2Meta refreshes one topic entry of the agent's L2MetaIndex; call it with the
// slot right after the engine writes it. The written value is the only source — a
// refresh that read the record back would have to answer a read failure by dropping
// the entry. Callers hold c.Mu.
func (c *Context) SyncL2Meta(topic *core.TopicSlot) {
	c.L2Meta.Update(index.L2MetaFromTopic(topic))
}

// RemoveTopicsFromIndices drops the given topics from the L2Meta cache and from the
// plan trees they owned: a cached tree outliving its topic could keep a dead plan
// alive. The content mirror is not this function's business — whoever deletes those
// records drops it once the disk agrees. Callers hold c.Mu.
func (c *Context) RemoveTopicsFromIndices(ids []uint64) {
	for _, id := range ids {
		c.L2Meta.Remove(id)
		c.Plans.RemoveTopic(id)
	}
}

// RetargetL2Meta moves every topic of the merged-away scenes to the primary scene in
// the L2Meta cache; call it once the merge has been applied to the records.
func (c *Context) RetargetL2Meta(primaryHash uint64, removed map[uint64]struct{}) {
	for sid := range removed {
		c.L2Meta.RetargetScene(sid, primaryHash)
	}
}
