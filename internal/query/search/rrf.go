// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// RRF (Reciprocal Rank Fusion) for multi-channel retrieval merging.

package search

import (
	"log/slog"
	"sort"

	"memhop/internal/core/index"
)

// RRFMerge fuses two ranked lists using Reciprocal Rank Fusion.
//
// Formula: score(d) = Σ 1.0 / (k + rank(d)), default k=60.
// Both inputs must be sorted by score descending (rank 1 = highest score).
func RRFMerge(bm25Ranked, vectorRanked []index.ScoredDoc, k float32) []index.ScoredDoc {
	if k <= 0 {
		slog.Warn("RRFMerge: k must be > 0, caller should guarantee valid k")
		k = 60.0
	}
	scores := make(map[uint64]float32)

	for i, doc := range bm25Ranked {
		scores[doc.IDHash] += 1.0 / (k + float32(i+1))
	}
	for i, doc := range vectorRanked {
		scores[doc.IDHash] += 1.0 / (k + float32(i+1))
	}

	merged := make([]index.ScoredDoc, 0, len(scores))
	for id, score := range scores {
		merged = append(merged, index.ScoredDoc{IDHash: id, Score: score})
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})
	return merged
}

// RRFMerge3 fuses three ranked lists (BM25 + vector + entity) using RRF.
func RRFMerge3(
	bm25Ranked, vectorRanked, entityRanked []index.ScoredDoc,
	k float32,
) []index.ScoredDoc {
	if k <= 0 {
		slog.Warn("RRFMerge3: k must be > 0, caller should guarantee valid k")
		k = 60.0
	}
	scores := make(map[uint64]float32)

	addRank := func(docs []index.ScoredDoc) {
		for i, doc := range docs {
			scores[doc.IDHash] += 1.0 / (k + float32(i+1))
		}
	}
	addRank(bm25Ranked)
	addRank(vectorRanked)
	addRank(entityRanked)

	merged := make([]index.ScoredDoc, 0, len(scores))
	for id, score := range scores {
		merged = append(merged, index.ScoredDoc{IDHash: id, Score: score})
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})
	return merged
}
