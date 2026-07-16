// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Search-related DTOs for the MemHop Query layer.

package query

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
}

// DefaultMaxResults returns the default max results when not specified.
func DefaultMaxResults() int { return 20 }

// EffectiveMaxResults returns MaxResults if set, otherwise the default.
func (q SearchQuery) EffectiveMaxResults() int {
	if q.MaxResults > 0 {
		return q.MaxResults
	}
	return DefaultMaxResults()
}

// SearchDefaults holds default configuration for the search pipeline.
type SearchDefaults struct {
	MaxResults      int
	DefaultRRFK     float32
	ActivationBoost float32
	RecentChatBonus float32
}

// DefaultSearchConfig is the built-in default search configuration.
var DefaultSearchConfig = SearchDefaults{
	MaxResults:      20,
	DefaultRRFK:     60.0,
	ActivationBoost: 1.2,
	RecentChatBonus: 0.1,
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
	Profile            ProfileResult    `json:"profile"`
	Contexts           []ContextResult  `json:"contexts"`
	AssociatedContexts []ContextResult  `json:"associated_contexts"`
	Crystals           []CrystalSummary `json:"crystals"`
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
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Role             string            `json:"role"`
	Personality      string            `json:"personality"`
	Worldview        string            `json:"worldview"`
	Preferences      map[string]string `json:"preferences"`
	Lexicon          map[string]string `json:"lexicon"`
	StyleTraits      []string          `json:"style_traits"`
	EmotionPatterns  map[string]string `json:"emotion_patterns"`
	CreatedAt        int64             `json:"created_at"`
	UpdatedAt        int64             `json:"updated_at"`
}

// L1Graph is the full L1 layer graph for visualization.
type L1Graph struct {
	Nodes []L1Node `json:"nodes"`
	Edges []L1Edge `json:"edges"`
}

// L1Node is a node in the L1 visualization graph.
type L1Node struct {
	ID              string   `json:"id"`
	SceneID         string   `json:"scene_id"`
	TopicIDs        []string `json:"topic_ids"`
	Depth           uint32   `json:"depth"`
	Importance      float32  `json:"importance"`
	Valence         float64  `json:"valence"`
	Arousal         float64  `json:"arousal"`
	Summary         *string  `json:"summary,omitempty"`
	DominantEmotion *string  `json:"dominant_emotion,omitempty"`
	Keywords        []string `json:"keywords"`
	RecallScore     float32  `json:"recall_score"`
	CreatedAt       int64    `json:"created_at"`
	UpdatedAt       int64    `json:"updated_at"`
	EdgeIDs         []string `json:"edge_ids"`
}

// L1Edge is an edge in the L1 visualization graph.
type L1Edge struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	NodeIDs   []string `json:"node_ids"`
	Weight    float32  `json:"weight"`
	CreatedAt int64    `json:"created_at"`
}

// L3EntityHint is a hint for L3 knowledge graph entity import.
type L3EntityHint struct {
	Name       string `json:"name"`
	EntityType string `json:"type"`
}

// ============================================================================
// LLM Preprocessing types
// ============================================================================

// SearchPreprocessResult is the result of LLM search query preprocessing.
type SearchPreprocessResult struct {
	Keywords      []string       `json:"keywords"`
	NeedsL3Import bool           `json:"needs_l3_import"`
	L3Entities    []L3EntityHint `json:"l3_entities,omitempty"`
}

// WritePreprocessResult is the result of LLM write content preprocessing.
type WritePreprocessResult struct {
	Keywords   []string `json:"keywords"`
	Importance float32  `json:"importance"`
}

// L3SearchQuery is the unified L3 knowledge search request.
type L3SearchQuery struct {
	Keyword  string  `json:"keyword"`
	NodeType string  `json:"node_type,omitempty"`
	GraphID  string  `json:"graph_id,omitempty"`
	MinScore float64 `json:"min_score,omitempty"`
	Limit    int     `json:"limit,omitempty"`
}

// L3SearchResult is the result of an L3 knowledge search.
type L3SearchResult struct {
	Nodes []uint64 `json:"nodes"`
}
