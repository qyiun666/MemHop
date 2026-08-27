// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Sparse index snapshot serialization: the JSON wire format used by the
// .meh snapshot area, kept apart from the live BM25 read/write paths.

package index

import (
	"encoding/json"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
)

type sparseIndexJSON struct {
	K1           float32                 `json:"k1"`
	B            float32                 `json:"b"`
	Postings     map[string]*PostingList `json:"postings"`
	DocLengths   map[uint64]uint32       `json:"doc_lengths"`
	DocTerms     map[uint64][]string     `json:"doc_terms,omitempty"`
	AvgDocLength float32                 `json:"avg_doc_length"`
	TotalDocs    uint32                  `json:"total_docs"`
	TotalTerms   uint64                  `json:"total_terms"`
	EntityIndex  *entityIndexJSON        `json:"entity_index,omitempty"`
}

type entityIndexJSON struct {
	Entities map[string]entityEntryJSON `json:"entities"`
}

type entityEntryJSON struct {
	NodeHash uint64   `json:"node_hash"`
	L2IDs    []uint64 `json:"l2_ids"`
}

func (s *SparseIndex) Serialize() ([]byte, error) {
	// Snapshot bytes must reflect the fully synced entity channel. When no
	// term is dirty the read lock alone guarantees a consistent snapshot;
	// otherwise upgrade to the write lock, resync, and marshal atomically.
	s.mu.RLock()
	if len(s.dirtyTerms) == 0 {
		defer s.mu.RUnlock()
		return s.serializeLocked()
	}
	s.mu.RUnlock()
	s.mu.Lock()
	s.ensureSortedLocked()
	defer s.mu.Unlock()
	return s.serializeLocked()
}

// serializeLocked marshals the index to JSON. Caller must hold s.mu (read
// or write lock).
func (s *SparseIndex) serializeLocked() ([]byte, error) {
	j := sparseIndexJSON{
		K1:           s.k1,
		B:            s.b,
		Postings:     s.postings,
		DocLengths:   s.docLengths,
		DocTerms:     s.docTerms,
		AvgDocLength: s.avgDocLength,
		TotalDocs:    s.totalDocs,
		TotalTerms:   s.totalTerms,
	}

	if !s.entityIndex.IsEmpty() {
		ej := &entityIndexJSON{
			Entities: make(map[string]entityEntryJSON, len(s.entityIndex.entities)),
		}
		for name, entry := range s.entityIndex.entities {
			ej.Entities[name] = entityEntryJSON{
				NodeHash: entry.nodeHash,
				L2IDs:    entry.l2IDs,
			}
		}
		j.EntityIndex = ej
	}

	return json.Marshal(j)
}

func DeserializeSparseIndex(data []byte) (*SparseIndex, error) {
	var j sparseIndexJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, common.NewError(common.ErrDeserialization, "sparse index", err)
	}

	s := &SparseIndex{
		k1:           j.K1,
		b:            j.B,
		postings:     j.Postings,
		docLengths:   j.DocLengths,
		docTerms:     j.DocTerms,
		avgDocLength: j.AvgDocLength,
		totalDocs:    j.TotalDocs,
		totalTerms:   j.TotalTerms,
		entityIndex:  NewEntityIndex(),
		dirtyTerms:   make(map[string]struct{}),
	}
	if s.docTerms == nil {
		rebuildSparseDocTerms(s)
	}

	if j.EntityIndex != nil {
		for name, entry := range j.EntityIndex.Entities {
			s.entityIndex.AddEntity(name, entry.NodeHash, entry.L2IDs)
		}
	}

	return s, nil
}

// rebuildSparseDocTerms reconstructs the doc → sorted-terms map from the
// postings of a legacy snapshot that did not persist docTerms.
func rebuildSparseDocTerms(s *SparseIndex) {
	s.docTerms = make(map[uint64][]string)
	for term, pl := range s.postings {
		for id := range pl.TermFreq {
			s.docTerms[id] = append(s.docTerms[id], term)
		}
	}
	for id, terms := range s.docTerms {
		slices.Sort(terms)
		s.docTerms[id] = terms
	}
}
