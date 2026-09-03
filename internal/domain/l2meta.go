// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package domain

import (
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// SyncL2Meta refreshes one topic entry of the agent's L2MetaIndex from the
// record just written; call it right after the engine writes. On read failure
// the entry is removed so stale metadata is never served.
func (c *Context) SyncL2Meta(idHash uint64) {
	if c.L2Meta == nil {
		return
	}
	topic, err := core.ReadTopicLenient(c.Engine, c.ID, idHash)
	if err != nil || topic == nil {
		c.L2Meta.Remove(idHash)
		return
	}
	c.L2Meta.Update(index.L2MetaFromTopic(topic))
}

// RemoveTopicsFromIndices drops the given topics from the agent's L2Meta
// cache; used by the DeleteScene / DeleteTopic paths after their records are
// tombstoned. Callers hold c.Mu.
func (c *Context) RemoveTopicsFromIndices(ids []uint64) {
	for _, id := range ids {
		c.L2Meta.Remove(id)
	}
}

// RetargetL2Meta moves every topic of the merged-away scenes to the primary
// scene in the L2MetaIndex, mirroring repo.MergeScenesL2 after a merge.
func (c *Context) RetargetL2Meta(primaryHash uint64, removed map[uint64]struct{}) {
	if c.L2Meta == nil {
		return
	}
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
