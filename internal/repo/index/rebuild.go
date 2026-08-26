// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Unified retrieval index rebuild: one engine scan rebuilds sparse and
// L2Meta. Run after Open, Dream compression or bulk writes.
package index

import (
	"encoding/json"
	"strings"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func RebuildSearchIndexes(engine *core.StorageEngine, agentID uint64) (*SparseIndex, *L2MetaIndex, error) {
	sparse, l2Meta := buildIndexesFromEngine(engine, agentID)
	return sparse, l2Meta, nil
}

// BuildL2MetaFromEngine scans only RecL2Topic records of one agent domain
// and fills an L2MetaIndex; corrupt or unparsable records are skipped
// (same tolerance as buildIndexesFromEngine). Used at Open time, where
// sparse comes from the snapshot instead.
func BuildL2MetaFromEngine(engine *core.StorageEngine, agentID uint64) *L2MetaIndex {
	l2Meta := NewL2MetaIndex()
	for idHash := range engine.IndexByType(agentID, core.RecL2Topic) {
		_, data, err := engine.ReadRecord(agentID, idHash)
		if err != nil {
			continue // skip corrupt records
		}
		var topic topicSlotJSON
		if json.Unmarshal(data, &topic) != nil {
			continue // skip unparsable records
		}
		l2Meta.insertMeta(topicToL2Meta(idHash, &topic))
	}
	return l2Meta
}

// buildIndexesFromEngine scans one agent domain once, building sparse/L2Meta;
// corrupt or unparsable records are skipped.
func buildIndexesFromEngine(engine *core.StorageEngine, agentID uint64) (*SparseIndex, *L2MetaIndex) {
	sparse := NewSparseIndex()
	l2Meta := NewL2MetaIndex()
	for idHash := range engine.Index(agentID) {
		rt, data, err := engine.ReadRecord(agentID, idHash)
		if err != nil {
			continue // skip corrupt records
		}
		if rt != core.RecL2Topic {
			continue
		}
		var topic topicSlotJSON
		if json.Unmarshal(data, &topic) != nil {
			continue // skip unparsable records
		}
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
		l2Meta.insertMeta(topicToL2Meta(idHash, &topic))
	}
	return sparse, l2Meta
}
