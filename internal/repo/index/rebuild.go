// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Unified retrieval index rebuild: one engine scan rebuilds sparse and
// L2Meta. Run after Open, Dream compression or bulk writes.
package index

import (
	"encoding/json"
	"iter"
	"strings"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func RebuildSearchIndexes(engine *core.StorageEngine, agentID uint64) (*SparseIndex, *L2MetaIndex) {
	return buildIndexesFromEngine(engine, agentID)
}

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
// records. Used at Open time, where sparse comes from the snapshot instead.
func BuildL2MetaFromEngine(engine *core.StorageEngine, agentID uint64) *L2MetaIndex {
	l2Meta := NewL2MetaIndex()
	for _, topic := range forEachTopic(engine, agentID) {
		l2Meta.insertMeta(L2MetaFromTopic(topic))
	}
	return l2Meta
}

// buildIndexesFromEngine scans one agent domain once, building sparse/L2Meta.
func buildIndexesFromEngine(engine *core.StorageEngine, agentID uint64) (*SparseIndex, *L2MetaIndex) {
	sparse := NewSparseIndex()
	l2Meta := NewL2MetaIndex()
	for idHash, topic := range forEachTopic(engine, agentID) {
		if topic.Depth <= 2 {
			// Uncompressed topics carry User+Agent keywords; compressed ones carry FusedKeywords.
			text := strings.Join(topic.UserKeywords, " ")
			if len(topic.AgentKeywords) > 0 {
				text += " " + strings.Join(topic.AgentKeywords, " ")
			}
			if len(topic.FusedKeywords) > 0 {
				text += " " + strings.Join(topic.FusedKeywords, " ")
			}
			terms := Tokenize(text)
			sparse.AddDocument(idHash, terms, uint32(len(terms)))
		}
		l2Meta.insertMeta(L2MetaFromTopic(topic))
	}
	return sparse, l2Meta
}
