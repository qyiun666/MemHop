// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Unified retrieval index rebuild: one engine scan rebuilds sparse
// (depth<=2 topics only), L1Reverse and L2Meta. Run after Open, Dream
// compression or bulk writes.
package index

import (
	"encoding/json"
	"strings"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func RebuildSearchIndexes(engine *core.StorageEngine) (*SparseIndex, *L1ReverseIndex, *L2MetaIndex, error) {
	sparse, l1Reverse, l2Meta := buildIndexesFromEngine(engine)
	return sparse, l1Reverse, l2Meta, nil
}

func BuildL1ReverseIndex(engine *core.StorageEngine) *L1ReverseIndex {
	_, l1Reverse, _ := buildIndexesFromEngine(engine)
	return l1Reverse
}

// BuildL2MetaFromEngine scans only RecL2Topic records and fills an
// L2MetaIndex; corrupt or unparsable records are skipped (same tolerance
// as buildIndexesFromEngine). Used at Open time, where sparse/L1Reverse
// come from the snapshot instead.
func BuildL2MetaFromEngine(engine *core.StorageEngine) *L2MetaIndex {
	l2Meta := NewL2MetaIndex()
	for idHash := range engine.IndexByType(core.RecL2Topic) {
		_, data, err := engine.ReadRecord(idHash)
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

// buildIndexesFromEngine scans once, building sparse/L1Reverse/L2Meta;
// corrupt or unparsable records are skipped.
func buildIndexesFromEngine(engine *core.StorageEngine) (*SparseIndex, *L1ReverseIndex, *L2MetaIndex) {
	sparse := NewSparseIndex()
	l1Reverse := NewL1ReverseIndex()
	l2Meta := NewL2MetaIndex()
	for idHash := range engine.Index() {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil {
			continue // skip corrupt records
		}
		switch rt {
		case core.RecL1SceneNode:
			var node core.SceneNode
			if json.Unmarshal(data, &node) == nil && node.SceneID != 0 {
				l1Reverse.Add(node.SceneID, idHash)
			}
		case core.RecL2Topic:
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
	}
	return sparse, l1Reverse, l2Meta
}
