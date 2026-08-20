// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"cmp"
	"encoding/json"
	"math"
	"slices"
	"sync"

	"github.com/qyiun666/MemHop/internal/common"
)

type PostingList struct {
	TermFreq map[uint64]uint32 `json:"tf"` // id_hash → term frequency
	DocFreq  uint32            `json:"df"` // number of documents containing this term
}

type ScoredDoc struct {
	IDHash uint64
	Score  float32
}

// Lock order (to prevent deadlock): storage → l2meta → sparse → l1reverse → l3index → l3cache

// SparseIndex is a BM25 full-text search index.
type SparseIndex struct {
	mu           sync.RWMutex
	k1           float32
	b            float32
	postings     map[string]*PostingList
	docLengths   map[uint64]uint32 // id_hash → document length
	docTerms     map[uint64][]string
	avgDocLength float32
	totalDocs    uint32
	totalTerms   uint64
	entityIndex  *EntityIndex
	// dirtyTerms records terms whose postings changed since the entity
	// fuzzy-match channel was last resynced. The sorted doc-id list is only
	// needed on the read path, so writes only mark terms dirty and defer the
	// O(K log K) sort to the first read (EntitySearch / Serialize).
	// All accesses must happen while holding s.mu.
	dirtyTerms map[string]struct{}
}

// NewSparseIndex creates a SparseIndex with default BM25 parameters (k1=1.2, b=0.75).
func NewSparseIndex() *SparseIndex {
	return &SparseIndex{
		k1:          1.2,
		b:           0.75,
		postings:    make(map[string]*PostingList),
		docLengths:  make(map[uint64]uint32),
		docTerms:    make(map[uint64][]string),
		entityIndex: NewEntityIndex(),
		dirtyTerms:  make(map[string]struct{}),
	}
}

func (s *SparseIndex) addDocumentLocked(idHash uint64, terms []string, docLen uint32) {
	if _, exists := s.docLengths[idHash]; exists {
		s.removeDocumentLocked(idHash)
	}
	s.docLengths[idHash] = docLen
	s.totalDocs++
	s.totalTerms += uint64(docLen)
	s.avgDocLength = float32(s.totalTerms) / float32(s.totalDocs)

	tfMap := make(map[string]uint32)
	for _, term := range terms {
		tfMap[term]++
	}
	newTerms := make([]string, 0, len(tfMap))
	for term, tf := range tfMap {
		pl, ok := s.postings[term]
		if !ok {
			pl = &PostingList{TermFreq: make(map[uint64]uint32)}
			s.postings[term] = pl
		}
		pl.TermFreq[idHash] = tf
		pl.DocFreq++
		newTerms = append(newTerms, term)
		s.markDirtyLocked(term)
	}
	slices.Sort(newTerms)
	s.docTerms[idHash] = newTerms
}

func (s *SparseIndex) removeDocumentLocked(idHash uint64) {
	docLen, exists := s.docLengths[idHash]
	if !exists {
		return
	}
	s.totalDocs--
	s.totalTerms -= uint64(docLen)
	if s.totalDocs > 0 {
		s.avgDocLength = float32(s.totalTerms) / float32(s.totalDocs)
	} else {
		s.avgDocLength = 0
	}

	terms := s.docTerms[idHash]
	if len(terms) == 0 {
		// Snapshot written before doc_terms existed: rebuild the term list
		// from postings for this one document.
		for term, pl := range s.postings {
			if _, ok := pl.TermFreq[idHash]; ok {
				terms = append(terms, term)
			}
		}
		slices.Sort(terms)
	}
	for _, term := range terms {
		pl := s.postings[term]
		if pl == nil {
			continue
		}
		delete(pl.TermFreq, idHash)
		if len(pl.TermFreq) == 0 {
			delete(s.postings, term)
		} else {
			pl.DocFreq = uint32(len(pl.TermFreq))
		}
		s.markDirtyLocked(term)
	}
	delete(s.docLengths, idHash)
	delete(s.docTerms, idHash)
}

// markDirtyLocked records that term's postings changed, deferring the entity
// fuzzy-match resync (and its O(K log K) doc-id sort) until the next read.
// Caller must hold s.mu write lock.
func (s *SparseIndex) markDirtyLocked(term string) {
	s.dirtyTerms[term] = struct{}{}
}

// ensureSortedLocked resyncs the entity channel for every dirty term,
// sorting each posting's doc ids exactly once. Caller must hold s.mu write
// lock.
func (s *SparseIndex) ensureSortedLocked() {
	if len(s.dirtyTerms) == 0 {
		return
	}
	for term := range s.dirtyTerms {
		s.syncEntityTermLocked(term)
	}
	clear(s.dirtyTerms)
}

// ensureSorted lazily sorts dirty postings and resyncs the entity channel
// before a read that depends on ordered doc-id lists. It is a no-op when
// everything is already synced. Double-checked: the read lock check and the
// upgrade are separate critical sections, so a concurrent writer may still
// interleave between them. Readers observe a snapshot that may lag the
// latest writes (unobservable under the single-instance serial contract);
// the entity index itself is mutated exclusively under the write lock, so
// a snapshot is never torn.
func (s *SparseIndex) ensureSorted() {
	s.mu.RLock()
	dirty := len(s.dirtyTerms) > 0
	s.mu.RUnlock()
	if !dirty {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureSortedLocked()
}

// syncEntityTermLocked keeps the entity fuzzy-match channel aligned with the
// BM25 postings: each indexed term maps to every L2 topic containing it.
// Only called from ensureSortedLocked; caller must hold s.mu write lock.
func (s *SparseIndex) syncEntityTermLocked(term string) {
	pl := s.postings[term]
	if pl == nil || len(pl.TermFreq) == 0 {
		s.entityIndex.RemoveEntity(term)
		return
	}
	ids := make([]uint64, 0, len(pl.TermFreq))
	for id := range pl.TermFreq {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	s.entityIndex.AddEntity(term, ids[0], ids)
}

// AddDocument indexes a document, removing any existing idHash first.
func (s *SparseIndex) AddDocument(idHash uint64, terms []string, docLen uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addDocumentLocked(idHash, terms, docLen)
}

func (s *SparseIndex) RemoveDocument(idHash uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeDocumentLocked(idHash)
}

// idf computes inverse document frequency: ln((N - n + 0.5) / (n + 0.5) + 1.0).
func (s *SparseIndex) idf(docFreq uint32) float32 {
	n := float32(docFreq)
	nt := float32(s.totalDocs)
	return float32(math.Log(float64((nt-n+0.5)/(n+0.5) + 1.0)))
}

func (s *SparseIndex) BM25Score(queryTerms []string, docIDHash uint64) float32 {
	docLen, ok := s.docLengths[docIDHash]
	if !ok {
		return 0
	}
	dl := float32(docLen)
	var score float32
	for _, term := range queryTerms {
		pl, ok := s.postings[term]
		if !ok {
			continue
		}
		tf, ok := pl.TermFreq[docIDHash]
		if !ok {
			continue
		}
		idfVal := s.idf(pl.DocFreq)
		tfFloat := float32(tf)
		tfNorm := (tfFloat * (s.k1 + 1)) / (tfFloat + s.k1*(1-s.b+s.b*dl/s.avgDocLength))
		score += idfVal * tfNorm
	}
	return score
}

func (s *SparseIndex) Search(queryTerms []string, k int) []ScoredDoc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	candidates := make(map[uint64]struct{})
	for _, term := range queryTerms {
		if pl, ok := s.postings[term]; ok {
			for docID := range pl.TermFreq {
				candidates[docID] = struct{}{}
			}
		}
	}

	scores := make([]ScoredDoc, 0, len(candidates))
	for docID := range candidates {
		sc := s.BM25Score(queryTerms, docID)
		if sc > 0 {
			scores = append(scores, ScoredDoc{IDHash: docID, Score: sc})
		}
	}
	slices.SortFunc(scores, func(a, b ScoredDoc) int {
		return cmp.Compare(b.Score, a.Score)
	})
	if k > 0 && len(scores) > k {
		scores = scores[:k]
	}
	return scores
}

func (s *SparseIndex) EntitySearch(query string) []ScoredDoc {
	s.ensureSorted()
	s.mu.RLock()
	defer s.mu.RUnlock()
	words := TokenizeWords(query)
	tokens := make([]string, len(words))
	copy(tokens, words)
	for i := 0; i+1 < len(words); i++ {
		tokens = append(tokens, words[i]+" "+words[i+1])
	}

	scoreMap := make(map[uint64]float32)
	for _, token := range tokens {
		for _, fr := range s.entityIndex.FuzzyMatch(token, 2) {
			score := 1.0 / (1.0 + float32(fr.Distance))
			for _, l2ID := range fr.L2IDs {
				if score > scoreMap[l2ID] {
					scoreMap[l2ID] = score
				}
			}
		}
	}
	results := make([]ScoredDoc, 0, len(scoreMap))
	for id, sc := range scoreMap {
		results = append(results, ScoredDoc{IDHash: id, Score: sc})
	}
	slices.SortFunc(results, func(a, b ScoredDoc) int {
		return cmp.Compare(b.Score, a.Score)
	})
	return results
}

func (s *SparseIndex) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int(s.totalDocs)
}

func (s *SparseIndex) IsEmpty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalDocs == 0
}

func (s *SparseIndex) TopTerms(n int) []struct {
	Term    string
	DocFreq uint32
} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	type tf struct {
		Term    string
		DocFreq uint32
	}
	all := make([]tf, 0, len(s.postings))
	for term, pl := range s.postings {
		all = append(all, tf{Term: term, DocFreq: pl.DocFreq})
	}
	slices.SortFunc(all, func(a, b tf) int {
		return cmp.Compare(b.DocFreq, a.DocFreq)
	})
	if n > 0 && len(all) > n {
		all = all[:n]
	}
	result := make([]struct {
		Term    string
		DocFreq uint32
	}, len(all))
	for i, a := range all {
		result[i] = struct {
			Term    string
			DocFreq uint32
		}{Term: a.Term, DocFreq: a.DocFreq}
	}
	return result
}

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

	if j.EntityIndex != nil {
		for name, entry := range j.EntityIndex.Entities {
			s.entityIndex.AddEntity(name, entry.NodeHash, entry.L2IDs)
		}
	}

	return s, nil
}
