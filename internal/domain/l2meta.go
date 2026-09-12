// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package domain

import (
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// SyncL2Meta refreshes one topic entry of the agent's L2MetaIndex; call it with
// the slot right after the engine writes it. The written value is the only
// source: a refresh that read the record back would have to answer a read failure
// somehow, and dropping the entry is the answer that hides a turn the host was
// just told had settled. Callers hold c.Mu.
func (c *Context) SyncL2Meta(topic *core.TopicSlot) {
	c.L2Meta.Update(index.L2MetaFromTopic(topic))
}

// RemoveTopicsFromIndices drops the given topics from the L2Meta cache and from
// the plan trees they owned. A deleted topic must not leave a cached tree behind
// — the aggregate carries the recency an expiry sweep is exempt on, so a stale
// one could keep a dead plan alive. The content mirror is deliberately not this
// function's business: whoever deletes those records drops the mirror once the
// disk agrees. Callers hold c.Mu.
func (c *Context) RemoveTopicsFromIndices(ids []uint64) {
	for _, id := range ids {
		c.L2Meta.Remove(id)
		c.Plans.RemoveTopic(id)
	}
}

// RetargetL2Meta moves every topic of the merged-away scenes to the primary scene
// in the L2MetaIndex; call it once the merge has been applied to the records.
func (c *Context) RetargetL2Meta(primaryHash uint64, removed map[uint64]struct{}) {
	for sid := range removed {
		for _, id := range c.L2Meta.GetByScene(sid) {
			meta := c.L2Meta.Remove(id)
			if meta == nil {
				continue
			}
			meta.SceneID = primaryHash
			c.L2Meta.Update(meta)
		}
	}
}
