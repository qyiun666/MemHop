// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Retrieval channels of the scene scorer: BM25, f32 vector and entity
// fuzzy match, plus the query-text/keyword helpers and RRF fusion they
// feed into.

package scenefind

import (
	"cmp"
	"log/slog"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// buildSearchText joins the raw query and the extracted keywords; the
// channels score against this single text.
func buildSearchText(query string, keywords []string) string {
	searchText := strings.Join(keywords, " ")
	if query == "" {
		return searchText
	}
	if searchText == "" {
		return query
	}
	return query + " " + searchText
}

// indexTopicsByID maps the candidate topics by topic ID for score lookups.
func indexTopicsByID(topics []core.TopicSlot) map[uint64]core.TopicSlot {
	byID := make(map[uint64]core.TopicSlot, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	return byID
}

func filterByL3(topics []core.TopicSlot, l3Hash uint64) []core.TopicSlot {
	var filtered []core.TopicSlot
	for _, t := range topics {
		if slices.Contains(t.L3Refs, l3Hash) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func retrieveBM25(sparse *index.SparseIndex, topics []core.TopicSlot, text string) []index.ScoredDoc {
	terms := index.Tokenize(text)
	if len(terms) == 0 {
		return nil
	}
	var docs []index.ScoredDoc
	for _, t := range topics {
		if sc := sparse.BM25Score(terms, t.ID); sc > 0 {
			docs = append(docs, index.ScoredDoc{IDHash: t.ID, Score: sc})
		}
	}
	slices.SortFunc(docs, func(a, b index.ScoredDoc) int {
		return cmp.Compare(b.Score, a.Score)
	})
	return docs
}

// retrieveVector encodes the query and computes cosine similarity against
// each topic centroid; the channel is empty when the encoder is unavailable.
func retrieveVector(agentID uint64, engine *core.StorageEngine, enc Encoder,
	topics []core.TopicSlot, text string) []index.ScoredDoc {
	if enc == nil || !enc.IsAvailable() {
		return nil
	}
	queryVec, err := enc.Encode(text)
	if err != nil {
		slog.Warn("scenefind: vector channel encode failed, skipped", "error", err)
		return nil
	}
	if len(queryVec) == 0 {
		return nil
	}
	var centroidBuf []float32 // reused across topics to avoid one allocation per centroid
	var docs []index.ScoredDoc
	for _, t := range topics {
		sc, ok := centroidScore(agentID, engine, queryVec, t, &centroidBuf)
		if ok && sc > 0 {
			docs = append(docs, index.ScoredDoc{IDHash: t.ID, Score: sc})
		}
	}
	slices.SortFunc(docs, func(a, b index.ScoredDoc) int {
		return cmp.Compare(b.Score, a.Score)
	})
	return docs
}

// centroidScore reads one topic's centroid record and scores its cosine
// similarity to the query vector; centroidBuf is reused across calls.
func centroidScore(agentID uint64, engine *core.StorageEngine, queryVec []float32, t core.TopicSlot, centroidBuf *[]float32) (float32, bool) {
	if t.CentroidPageRef == 0 {
		return 0, false
	}
	_, vecData, err := engine.ReadRecord(agentID, t.CentroidPageRef)
	if err != nil || len(vecData) < len(queryVec)*4 {
		return 0, false
	}
	centroid, err := common.DecodeF32VecInto(vecData, len(queryVec), *centroidBuf)
	if err != nil {
		return 0, false
	}
	*centroidBuf = centroid
	return common.CosineSimilarity(queryVec, centroid), true
}

// rrfFuse merges ranked channel lists with Reciprocal Rank Fusion
// (k=rrfK, equal weights): score(id) = sum 1/(k+rank).
func rrfFuse(rankedLists ...[]index.ScoredDoc) map[uint64]float32 {
	scores := make(map[uint64]float32)
	for _, docs := range rankedLists {
		for i, doc := range docs {
			scores[doc.IDHash] += 1.0 / (rrfK + float32(i+1))
		}
	}
	return scores
}

func keywordSet(keywords []string) map[string]struct{} {
	set := make(map[string]struct{}, len(keywords))
	for _, kw := range keywords {
		set[strings.ToLower(kw)] = struct{}{}
	}
	return set
}

// keywordHit computes the overlap ratio of request keywords across the
// topic's Fused/User/Agent keywords.
func keywordHit(topic core.TopicSlot, kwSet map[string]struct{}) float32 {
	if len(kwSet) == 0 {
		return 0
	}
	fields := make([]string, 0, len(topic.FusedKeywords)+len(topic.UserKeywords)+len(topic.AgentKeywords))
	fields = append(fields, topic.FusedKeywords...)
	fields = append(fields, topic.UserKeywords...)
	fields = append(fields, topic.AgentKeywords...)
	seen := make(map[string]struct{}, len(fields))
	hit := 0
	for _, kw := range fields {
		k := strings.ToLower(kw)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		if _, ok := kwSet[k]; ok {
			hit++
		}
	}
	return float32(hit) / float32(len(kwSet))
}
