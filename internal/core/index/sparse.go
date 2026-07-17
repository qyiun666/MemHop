// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"encoding/json"
	"math"
	"sort"
	"sync"

	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
	"memhop/internal/core/storage"
)

// PostingList is an inverted list for a single term.
type PostingList struct {
	TermFreq map[uint64]uint32 `json:"tf"` // id_hash → term frequency
	DocFreq  uint32            `json:"df"` // number of documents containing this term
}

// ScoredDoc pairs a document ID hash with a relevance score.
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
	avgDocLength float32
	totalDocs    uint32
	totalTerms   uint64
	entityIndex  *EntityIndex
}

// NewSparseIndex creates a SparseIndex with default BM25 parameters (k1=1.2, b=0.75).
func NewSparseIndex() *SparseIndex {
	return &SparseIndex{
		k1:          1.2,
		b:           0.75,
		postings:    make(map[string]*PostingList),
		docLengths:  make(map[uint64]uint32),
		entityIndex: NewEntityIndex(),
	}
}

// NewSparseIndexWithParams creates a SparseIndex with custom BM25 parameters.
func NewSparseIndexWithParams(k1, b float32) *SparseIndex {
	return &SparseIndex{
		k1:          k1,
		b:           b,
		postings:    make(map[string]*PostingList),
		docLengths:  make(map[uint64]uint32),
		entityIndex: NewEntityIndex(),
	}
}

// internal helpers: caller must hold s.mu.
func (s *SparseIndex) addDocumentLocked(idHash uint64, terms []string, docLen uint32) {
	if _, exists := s.docLengths[idHash]; exists {
		s.removeDocumentLocked(idHash)
	}
	s.docLengths[idHash] = docLen
	s.totalDocs++
	s.totalTerms += uint64(docLen)
	s.avgDocLength = float32(s.totalTerms) / float32(s.totalDocs)

	// Build per-document term frequency map.
	tfMap := make(map[string]uint32)
	for _, term := range terms {
		tfMap[term]++
	}
	for term, tf := range tfMap {
		pl, ok := s.postings[term]
		if !ok {
			pl = &PostingList{TermFreq: make(map[uint64]uint32)}
			s.postings[term] = pl
		}
		pl.TermFreq[idHash] = tf
		pl.DocFreq++
	}
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
	for _, pl := range s.postings {
		if _, removed := pl.TermFreq[idHash]; removed {
			delete(pl.TermFreq, idHash)
			pl.DocFreq--
		}
	}
	// Remove empty postings.
	for term, pl := range s.postings {
		if pl.DocFreq == 0 {
			delete(s.postings, term)
		}
	}
	delete(s.docLengths, idHash)
}

// AddDocument indexes a document. If idHash already exists, it is removed first.
func (s *SparseIndex) AddDocument(idHash uint64, terms []string, docLen uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addDocumentLocked(idHash, terms, docLen)
}

// RemoveDocument removes a document from the index.
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

// BM25Score computes the BM25 score for a document given query terms.
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

// Search finds top-k documents matching the query terms using inverted index pruning.
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
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Score > scores[j].Score
	})
	if k > 0 && len(scores) > k {
		scores = scores[:k]
	}
	return scores
}

// EntitySearch scores L2 documents by entity recognition in the query text.
func (s *SparseIndex) EntitySearch(query string) []ScoredDoc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	words := TokenizeWords(query)
	tokens := make([]string, len(words))
	copy(tokens, words)
	// Add adjacent word pairs for multi-word entity matching.
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
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// EntitySearchNodes returns matched L3 node hashes and their L2 IDs.
func (s *SparseIndex) EntitySearchNodes(query string) []struct {
	NodeHash uint64
	L2IDs    []uint64
} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	words := TokenizeWords(query)
	seen := make(map[uint64][]uint64)
	for _, word := range words {
		for _, fr := range s.entityIndex.FuzzyMatch(word, 2) {
			if _, exists := seen[fr.NodeHash]; !exists {
				seen[fr.NodeHash] = fr.L2IDs
			}
		}
	}
	var result []struct {
		NodeHash uint64
		L2IDs    []uint64
	}
	for nh, ids := range seen {
		result = append(result, struct {
			NodeHash uint64
			L2IDs    []uint64
		}{NodeHash: nh, L2IDs: ids})
	}
	return result
}

// BuildEntityIndex builds the entity index from L3 graph nodes in the storage engine.
func (s *SparseIndex) BuildEntityIndex(engine *storage.StorageEngine) error {
	type nodeInfo struct {
		nodeHash uint64
		graphID  uint64
		title    string
		keywords []string
	}

	var allNodes []nodeInfo
	l2ByGraph := make(map[uint64][]uint64)

	// First pass: collect L3 graph nodes.
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return true
		}
		if rt == storage.RecL3GraphNode {
			var node graphNodeJSON
			if json.Unmarshal(data, &node) == nil {
				allNodes = append(allNodes, nodeInfo{
					nodeHash: node.IDHash,
					graphID:  node.GraphID,
					title:    node.Title,
					keywords: node.Keywords,
				})
			}
		}
		return true
	})

	// Second pass: build graph → L2 mapping from topic L3 refs.
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return true
		}
		if rt == storage.RecL2Topic {
			var topic topicJSON
			if json.Unmarshal(data, &topic) == nil {
				for _, ref := range topic.UserL3Refs {
					l2ByGraph[ref] = append(l2ByGraph[ref], topic.ID)
				}
				for _, ref := range topic.AgentL3Refs {
					l2ByGraph[ref] = append(l2ByGraph[ref], topic.ID)
				}
			}
		}
		return true
	})

	// Build entities from collected nodes.
	nodesByGraph := make(map[uint64][]nodeInfo)
	for _, n := range allNodes {
		nodesByGraph[n.graphID] = append(nodesByGraph[n.graphID], n)
	}
	for graphID, nodes := range nodesByGraph {
		l2IDs := l2ByGraph[graphID]
		for _, n := range nodes {
			s.entityIndex.AddEntity(n.title, n.nodeHash, l2IDs)
			for _, kw := range n.keywords {
				s.entityIndex.AddEntity(kw, n.nodeHash, l2IDs)
			}
			// Add as BM25 document for entity search.
			docTerms := TokenizeWords(n.title)
			for _, kw := range n.keywords {
				docTerms = append(docTerms, TokenizeWords(kw)...)
			}
			s.AddDocument(n.nodeHash, docTerms, uint32(len(docTerms)))
		}
	}
	return nil
}

// graphNodeJSON and topicJSON are minimal structs for JSON deserialization.
type graphNodeJSON struct {
	IDHash   uint64   `json:"id_hash"`
	GraphID  uint64   `json:"graph_id"`
	Title    string   `json:"title"`
	Keywords []string `json:"keywords"`
}

type topicJSON struct {
	ID          uint64   `json:"id"`
	UserL3Refs  []uint64 `json:"user_l3_refs"`
	AgentL3Refs []uint64 `json:"agent_l3_refs"`
}

// HasEntityIndex returns true if the entity index has entries.
func (s *SparseIndex) HasEntityIndex() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.entityIndex.IsEmpty()
}

// EntityIndexRef returns a reference to the entity index.
func (s *SparseIndex) EntityIndexRef() *EntityIndex {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entityIndex
}

// Len returns the number of indexed documents.
func (s *SparseIndex) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int(s.totalDocs)
}

// IsEmpty returns true if no documents are indexed.
func (s *SparseIndex) IsEmpty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalDocs == 0
}

// Merge merges another SparseIndex into this one. Assumes disjoint document IDs.
func (s *SparseIndex) Merge(other *SparseIndex) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for term, otherPL := range other.postings {
		pl, ok := s.postings[term]
		if !ok {
			pl = &PostingList{TermFreq: make(map[uint64]uint32)}
			s.postings[term] = pl
		}
		for docID, tf := range otherPL.TermFreq {
			pl.TermFreq[docID] = tf
		}
		pl.DocFreq = uint32(len(pl.TermFreq))
	}
	for docID, docLen := range other.docLengths {
		s.docLengths[docID] = docLen
	}
	s.totalDocs += other.totalDocs
	s.totalTerms += other.totalTerms
	if s.totalDocs > 0 {
		s.avgDocLength = float32(s.totalTerms) / float32(s.totalDocs)
	}
}

// TopTerms returns the top-n terms by document frequency.
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
	sort.Slice(all, func(i, j int) bool {
		return all[i].DocFreq > all[j].DocFreq
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

// sparseIndexJSON is the JSON serialization format for SparseIndex.
type sparseIndexJSON struct {
	K1           float32                 `json:"k1"`
	B            float32                 `json:"b"`
	Postings     map[string]*PostingList `json:"postings"`
	DocLengths   map[string]uint32       `json:"doc_lengths"`
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

// Serialize serializes the SparseIndex to JSON bytes.
func (s *SparseIndex) Serialize() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	docLens := make(map[string]uint32, len(s.docLengths))
	for k, v := range s.docLengths {
		docLens[hash.FormatHash(k)] = v
	}

	j := sparseIndexJSON{
		K1:           s.k1,
		B:            s.b,
		Postings:     s.postings,
		DocLengths:   docLens,
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

// DeserializeSparseIndex restores a SparseIndex from JSON bytes.
func DeserializeSparseIndex(data []byte) (*SparseIndex, error) {
	var j sparseIndexJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "sparse index", err)
	}

	docLens := make(map[uint64]uint32, len(j.DocLengths))
	for k, v := range j.DocLengths {
		id, err := hash.ParseID(k)
		if err != nil {
			return nil, mherrors.NewError(mherrors.ErrDeserialization, "parse doc id", err)
		}
		docLens[id] = v
	}

	s := &SparseIndex{
		k1:           j.K1,
		b:            j.B,
		postings:     j.Postings,
		docLengths:   docLens,
		avgDocLength: j.AvgDocLength,
		totalDocs:    j.TotalDocs,
		totalTerms:   j.TotalTerms,
		entityIndex:  NewEntityIndex(),
	}

	if j.EntityIndex != nil {
		for name, entry := range j.EntityIndex.Entities {
			s.entityIndex.AddEntity(name, entry.NodeHash, entry.L2IDs)
		}
	}

	return s, nil
}
