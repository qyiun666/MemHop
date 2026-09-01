// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 topic metadata rebuild: one engine scan rebuilds the L2Meta cache. Run
// after Open and at the end of Dream compression.
package index

import (
	"encoding/json"
	"iter"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// forEachTopic yields every parsable L2 topic record of one agent domain;
// corrupt or unparsable records are skipped (tolerated torn residue).
func forEachTopic(engine *core.StorageEngine, agentID uint64) iter.Seq2[uint64, *core.TopicSlot] {
	return func(yield func(uint64, *core.TopicSlot) bool) {
		for idHash := range engine.IndexByType(agentID, core.RecL2Topic) {
			_, data, err := engine.ReadRecord(agentID, idHash)
			if err != nil {
				continue // skip corrupt records
			}
			var topic core.TopicSlot
			if json.Unmarshal(data, &topic) != nil {
				continue // skip unparsable records
			}
			if !yield(idHash, &topic) {
				return
			}
		}
	}
}

// BuildL2MetaFromEngine fills an L2MetaIndex from one agent domain's topic
// records in a single scan.
func BuildL2MetaFromEngine(engine *core.StorageEngine, agentID uint64) *L2MetaIndex {
	l2Meta := NewL2MetaIndex()
	for _, topic := range forEachTopic(engine, agentID) {
		l2Meta.insertMeta(L2MetaFromTopic(topic))
	}
	return l2Meta
}
