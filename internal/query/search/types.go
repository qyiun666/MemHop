// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search-related DTOs for the MemHop Query layer.

package search

import (
	"sync/atomic"

	"github.com/qyiun666/MemHop/internal/common/config"
	"github.com/qyiun666/MemHop/internal/core/index"
	"github.com/qyiun666/MemHop/internal/core/storage"
	"github.com/qyiun666/MemHop/internal/query/crud"
	"github.com/qyiun666/MemHop/internal/query/encoder"
)

// RequestSource records who initiated an API request.
type RequestSource struct {
	SourceAgent    *string `json:"source_agent,omitempty"`
	SourcePlatform *string `json:"source_platform,omitempty"`
}

// IsEmpty returns true when both fields are nil.
func (r RequestSource) IsEmpty() bool {
	return r.SourceAgent == nil && r.SourcePlatform == nil
}

// SearchQuery is the search request.
type SearchQuery struct {
	Text         string  `json:"text"`
	MaxResults   int     `json:"max_results,omitempty"`
	DirectedL2ID *string `json:"directed_l2_id,omitempty"`
	DirectedL3ID *string `json:"directed_l3_id,omitempty"`
	AutoCreate   bool    `json:"auto_create,omitempty"`
	// Timestamp is required: the Unix-millisecond time of this dialogue turn.
	// Timestamp <= 0 is rejected with ErrInvalidQuery.
	Timestamp int64 `json:"timestamp"`
}

// EffectiveMaxResults returns MaxResults if set, otherwise 20.
func (q SearchQuery) EffectiveMaxResults() int {
	if q.MaxResults > 0 {
		return q.MaxResults
	}
	return 20
}

// SearchDefaults holds default configuration for the search pipeline.
type SearchDefaults struct {
	MaxResults        int
	DefaultRRFK       float32
	ActivationBonus   float32
	RecentChatBonus   float32
	MinRelevanceScore float32 // minimum RRF score to consider a match (0 = disabled)
}

// DefaultSearchConfig is the built-in default search configuration.
var DefaultSearchConfig = SearchDefaults{
	MaxResults:        20,
	DefaultRRFK:       60.0,
	ActivationBonus:   0.02,
	RecentChatBonus:   0.01,
	MinRelevanceScore: 0.015, // ~1/60: at least one channel must rank in top ~60
}

// L1Preview is a lightweight L1 node summary for agent decision-making.
type L1Preview struct {
	ID              string   `json:"id"`
	Summary         *string  `json:"summary,omitempty"`
	Importance      *float64 `json:"importance,omitempty"`
	DominantEmotion *string  `json:"dominant_emotion,omitempty"`
	MatchedKeywords []string `json:"matched_keywords"`
	RecallScore     *float64 `json:"recall_score,omitempty"`
}

// L3Preview is a lightweight L3 hypergraph summary.
type L3Preview struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	TopNodes  []string `json:"top_nodes"`
	Keywords  []string `json:"keywords"`
	NodeCount uint32   `json:"node_count"`
}

// SearchResult is the top-level search response.
type SearchResult struct {
	Profile            ProfileResult         `json:"profile"`
	Contexts           []ContextResult       `json:"contexts"`
	AssociatedContexts []ContextResult       `json:"associated_contexts"`
	Crystals           []crud.CrystalSummary `json:"crystals"`
	// NewTopicID is the hex ID of the depth1 topic created for this turn.
	// It is the write target: pass it to Update to append the agent reply.
	// Empty only when no topic was created (e.g. directed target not found).
	NewTopicID string `json:"new_topic_id,omitempty"`
}

// ContextResult represents an L2 context hit from search.
type ContextResult struct {
	ID             string   `json:"id"`
	ParentID       *string  `json:"parent_id,omitempty"`
	Depth          uint8    `json:"depth"`
	SceneID        string   `json:"scene_id"`
	UserKeywords   []string `json:"user_keywords"`
	UserTimestamp  int64    `json:"user_timestamp"`
	AgentKeywords  []string `json:"agent_keywords"`
	AgentTimestamp int64    `json:"agent_timestamp"`
	FusedKeywords  []string `json:"fused_keywords,omitempty"`
	FusedSummary   *string  `json:"fused_summary,omitempty"`
	ChildrenIDs    []string `json:"children_ids,omitempty"`
	L4Refs         []string `json:"l4_refs,omitempty"`
	L3Refs         []string `json:"l3_refs,omitempty"`
	RetrievalScore float32  `json:"retrieval_score"`
}

// ProfileResult is the L0 agent profile in search results.
type ProfileResult struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Role            string            `json:"role"`
	Personality     string            `json:"personality"`
	Worldview       string            `json:"worldview"`
	Preferences     map[string]string `json:"preferences"`
	Lexicon         map[string]string `json:"lexicon"`
	StyleTraits     []string          `json:"style_traits"`
	EmotionPatterns map[string]string `json:"emotion_patterns"`
	CreatedAt       int64             `json:"created_at"`
	UpdatedAt       int64             `json:"updated_at"`
}

// SearchDeps holds all dependencies injected into the search pipeline.
type SearchDeps struct {
	SparseIndex          *index.SparseIndex
	L2Meta               *index.L2MetaIndex
	VectorDim            int
	Engine               *storage.StorageEngine
	Encoder              encoder.Encoder
	Weights              *config.SearchWeights
	L1Reverse            *index.L1ReverseIndex
	PreprocessedKeywords []string
	ProfileCache         *atomic.Pointer[ProfileResult] // &MemHop.profileCache for caching; nil = no cache
}
