// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 topic metadata rebuild: one engine scan over an agent domain's topic
// records builds the whole cache in one pass.
package index

import "github.com/qyiun666/MemHop/internal/repo/core"

// BuildL2MetaFromEngine fills an L2MetaIndex from one agent domain's topic
// records in a single scan. Records that cannot be read or decoded are skipped
// (see core.IterAll): torn residue must not decide what the cache holds.
func BuildL2MetaFromEngine(engine *core.StorageEngine, agentID uint64) *L2MetaIndex {
	l2Meta := NewL2MetaIndex()
	for topic := range core.IterAll[core.TopicSlot](engine, agentID, core.RecL2Topic) {
		l2Meta.insertMeta(L2MetaFromTopic(&topic))
	}
	return l2Meta
}
