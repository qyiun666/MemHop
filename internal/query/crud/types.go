// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// CRUD list, graph, and detail DTOs for the MemHop Query layer.

package crud

import (
	"encoding/json"

	"memhop/internal/core/model"
)

// TopicListQuery is the L2 list query.
type TopicListQuery struct {
	Page       int     `json:"page"`
	PageSize   int     `json:"page_size"`
	ActiveOnly bool    `json:"active_only,omitempty"`
	Keyword    *string `json:"keyword,omitempty"`
}

// TopicListResult is the paginated L2 list response.
type TopicListResult struct {
	Items    []TopicSummary `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	HasMore  bool           `json:"has_more"`
}

// TopicSummary is an L2 list item.
type TopicSummary struct {
	ID            string   `json:"id"`
	Depth         uint8    `json:"depth"`
	SceneID       string   `json:"scene_id"`
	UserKeywords  []string `json:"user_keywords"`
	AgentKeywords []string `json:"agent_keywords"`
	FusedKeywords []string `json:"fused_keywords"`
	FusedSummary  *string  `json:"fused_summary,omitempty"`
	TurnCount     int      `json:"turn_count"`
	IsActive      bool     `json:"is_active"`
	CreatedAt     int64    `json:"created_at"`
	L4Count       int      `json:"l4_count"`
	L3Count       int      `json:"l3_count"`
	UpdatedAt     int64    `json:"updated_at"`
}

// TopicDetail is the full L2 TopicSlot view.
type TopicDetail struct {
	ID             string   `json:"id"`
	ParentID       *string  `json:"parent_id,omitempty"`
	Depth          uint8    `json:"depth"`
	SceneID        string   `json:"scene_id"`
	UserKeywords   []string `json:"user_keywords"`
	UserTimestamp  int64    `json:"user_timestamp"`
	AgentKeywords  []string `json:"agent_keywords"`
	AgentTimestamp int64    `json:"agent_timestamp"`
	FusedKeywords  []string `json:"fused_keywords"`
	FusedSummary   *string  `json:"fused_summary,omitempty"`
	ChildrenIDs    []string `json:"children_ids"`
	UserL4Refs     []string `json:"user_l4_refs"`
	UserL3Refs     []string `json:"user_l3_refs"`
	AgentL4Refs    []string `json:"agent_l4_refs"`
	AgentL3Refs    []string `json:"agent_l3_refs"`
	CreatedAt      int64    `json:"created_at"`
	UpdatedAt      int64    `json:"updated_at"`
}

// KnowledgeListQuery is the L3 list query.
type KnowledgeListQuery struct {
	Page          int     `json:"page"`
	PageSize      int     `json:"page_size"`
	DomainFilter  *string `json:"domain_filter,omitempty"`
	KnowledgeType *string `json:"knowledge_type,omitempty"`
	Keyword       *string `json:"keyword,omitempty"`
}

// KnowledgeListResult is the paginated L3 list response.
type KnowledgeListResult struct {
	Items    []KnowledgeSummary `json:"items"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
	HasMore  bool               `json:"has_more"`
}

// KnowledgeSummary is an L3 list item.
type KnowledgeSummary struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Domain        string  `json:"domain"`
	KnowledgeType string  `json:"knowledge_type"`
	Importance    float32 `json:"importance"`
	Confidence    float32 `json:"confidence"`
	UpdatedAt     int64   `json:"updated_at"`
}

// KnowledgeDetail is the full L3 knowledge node view.
type KnowledgeDetail struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Domain        string   `json:"domain"`
	KnowledgeType string   `json:"knowledge_type"`
	Text          string   `json:"text"`
	Summary       *string  `json:"summary,omitempty"`
	Keywords      []string `json:"keywords"`
	EdgePtrs      []string `json:"edge_ptrs"`
	ArchiveRefs   []string `json:"archive_refs"`
	SourceRef     *string  `json:"source_ref,omitempty"`
	Importance    float32  `json:"importance"`
	Confidence    float32  `json:"confidence"`
	CreatedAt     int64    `json:"created_at"`
	UpdatedAt     int64    `json:"updated_at"`
}

// KnowledgeNodeDetail is a single L3 node for batch get.
type KnowledgeNodeDetail struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Text          *string  `json:"text,omitempty"`
	Keywords      []string `json:"keywords"`
	Domain        string   `json:"domain"`
	KnowledgeType string   `json:"knowledge_type"`
	CreatedAt     int64    `json:"created_at"`
	Importance    float32  `json:"importance"`
}

// KnowledgeNodeQuery is the unified L3 node query (by IDs, keyword, or type).
type KnowledgeNodeQuery struct {
	ByIds     *ByIdsQuery     `json:"ByIds,omitempty"`
	ByKeyword *ByKeywordQuery `json:"ByKeyword,omitempty"`
	ByType    *ByTypeQuery    `json:"ByType,omitempty"`
}

// ByIdsQuery retrieves nodes by IDs.
type ByIdsQuery struct {
	IDs         []string `json:"ids"`
	IncludeText bool     `json:"include_text,omitempty"`
}

// ByKeywordQuery retrieves nodes by keyword within a graph.
type ByKeywordQuery struct {
	GraphID string `json:"graph_id"`
	Keyword string `json:"keyword"`
	Limit   int    `json:"limit,omitempty"`
}

// ByTypeQuery retrieves nodes by type within a graph.
type ByTypeQuery struct {
	GraphID  string `json:"graph_id"`
	NodeType string `json:"node_type"`
	Limit    int    `json:"limit,omitempty"`
}

// KnowledgeNodesResult is the batch L3 node result.
type KnowledgeNodesResult struct {
	Nodes     []KnowledgeNodeDetail `json:"nodes"`
	Total     int                   `json:"total"`
	Requested int                   `json:"requested"`
}

// L3Detail is the detailed L3 hypergraph view.
type L3Detail struct {
	Slot  GraphSlot   `json:"slot"`
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphNode is the public DTO for an L3 hypergraph node.
type GraphNode struct {
	ID         string   `json:"id"`
	GraphID    string   `json:"graph_id"`
	Title      string   `json:"title"`
	NodeType   string   `json:"node_type"`
	Content    string   `json:"content"`
	Keywords   []string `json:"keywords"`
	SourceRef  *string  `json:"source_ref,omitempty"`
	Importance float32  `json:"importance"`
	Summary    *string  `json:"summary,omitempty"`
	CreatedAt  int64    `json:"created_at"`
	UpdatedAt  int64    `json:"updated_at"`
}

// GraphEdge is the public DTO for an L3 hypergraph edge.
type GraphEdge struct {
	ID          string              `json:"id"`
	GraphID     string              `json:"graph_id"`
	Kind        model.GraphEdgeKind `json:"kind"`
	NodeIDs     []string            `json:"node_ids"`
	Weight      float32             `json:"weight"`
	Label       *string             `json:"label,omitempty"`
	Description *string             `json:"description,omitempty"`
	Confidence  float32             `json:"confidence"`
	CreatedAt   int64               `json:"created_at"`
}

// GraphSlot is the public DTO for an L3 hypergraph container.
type GraphSlot struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	NodeCount uint32 `json:"node_count"`
	EdgeCount uint32 `json:"edge_count"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// Subgraph is the result of subgraph extraction.
type Subgraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// TraversalHop is a single hop in graph traversal.
type TraversalHop struct {
	Depth    int       `json:"depth"`
	FromNode uint64    `json:"from_node"`
	Edge     GraphEdge `json:"edge"`
	ToNode   uint64    `json:"to_node"`
}

// MergeResult is the result of merging L2 contexts.
type MergeResult struct {
	PrimaryID        string   `json:"primary_id"`
	MergedCount      uint32   `json:"merged_count"`
	NewTurnCount     uint32   `json:"new_turn_count"`
	AbsorbedTopicIDs []string `json:"absorbed_topic_ids"`
}

// SceneTreeResult is the full scene tree query result.
type SceneTreeResult struct {
	SceneID           string        `json:"scene_id"`
	TotalTurns        uint32        `json:"total_turns"`
	DepthDistribution [4]uint32     `json:"depth_distribution"`
	Nodes             []TopicDetail `json:"nodes"`
	Edges             [][2]string   `json:"edges"`
}

// TimeRange represents an inclusive time range as [start_ms, end_ms].
type TimeRange [2]int64

// ArchiveQuery is the L4 archive query.
type ArchiveQuery struct {
	TopicID   *string    `json:"topic_id,omitempty"`
	Keyword   *string    `json:"keyword,omitempty"`
	TimeRange *TimeRange `json:"time_range,omitempty"`
	Page      int        `json:"page"`
	PageSize  int        `json:"page_size"`
}

// ArchivePageQuery is the paginated archive query.
type ArchivePageQuery struct {
	Page        int     `json:"page"`
	PageSize    int     `json:"page_size"`
	StartTime   *int64  `json:"start_time,omitempty"`
	EndTime     *int64  `json:"end_time,omitempty"`
	ContentType *string `json:"content_type,omitempty"`
}

// ArchiveListResult is the paginated archive list response.
type ArchiveListResult struct {
	Items    []Archive `json:"items"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
	HasMore  bool      `json:"has_more"`
}

// Archive is the public archive DTO.
type Archive struct {
	ID          string   `json:"id"`
	Content     string   `json:"content"`
	ContentType string   `json:"content_type"`
	Role        uint8    `json:"role"` // 0=user, 1=agent, 2=system
	ContextID   uint64   `json:"context_id"`
	TopicID     *string  `json:"topic_id,omitempty"`
	EngramIDs   []string `json:"engram_ids"`
	Metadata    *string  `json:"metadata,omitempty"`
	CreatedAt   int64    `json:"created_at"`
}

// ArchiveRef is a lightweight L4 reference.
type ArchiveRef struct {
	ID             string  `json:"id"`
	ContextID      string  `json:"context_id"`
	ContentType    string  `json:"content_type"`
	CreatedAt      int64   `json:"created_at"`
	SourceAgent    *string `json:"source_agent,omitempty"`
	SourcePlatform *string `json:"source_platform,omitempty"`
}

// CrystalListQuery is the L5 list query.
type CrystalListQuery struct {
	Page            uint32  `json:"page"`
	PageSize        uint32  `json:"page_size"`
	StatusFilter    *string `json:"status_filter,omitempty"`
	MinTriggerCount *uint32 `json:"min_trigger_count,omitempty"`
	Keyword         *string `json:"keyword,omitempty"`
}

// CrystalListResult is the paginated L5 list response.
type CrystalListResult struct {
	Crystals []CrystalSummary `json:"crystals"`
	Total    uint32           `json:"total"`
	Page     uint32           `json:"page"`
}

// CrystalSummary is an L5 list item.
type CrystalSummary struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Condition     string  `json:"condition"`
	Status        string  `json:"status"`
	TriggerCount  uint32  `json:"trigger_count"`
	SuccessRate   float32 `json:"success_rate"`
	LastTriggered *int64  `json:"last_triggered,omitempty"`
	CreatedAt     int64   `json:"created_at"`
}

// SessionStatus is the aggregate session view.
type SessionStatus struct {
	ActiveTopicIDs []string `json:"active_topic_ids"`
	Count          int      `json:"count"`
	IsEmpty        bool     `json:"is_empty"`
}

// ============================================================================
// Engram (L1) list types
// ============================================================================

// EngramListQuery is the L1 engram list query.
type EngramListQuery struct {
	Page          int      `json:"page"`
	PageSize      int      `json:"page_size"`
	StateFilter   *string  `json:"state_filter,omitempty"`
	MinImportance *float32 `json:"min_importance,omitempty"`
	Keyword       *string  `json:"keyword,omitempty"`
}

// EngramListResult is the paginated L1 engram list response.
type EngramListResult struct {
	Items    []EngramResult `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	HasMore  bool           `json:"has_more"`
}

// EngramResult is a single L1 engram in list results.
type EngramResult struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	Summary          *string  `json:"summary,omitempty"`
	Keywords         []string `json:"keywords"`
	MemoryState      string   `json:"memory_state"`
	Importance       float32  `json:"importance"`
	SourceType       string   `json:"source_type"`
	CreatedAt        int64    `json:"created_at"`
	UpdatedAt        int64    `json:"updated_at"`
	EdgeCount        int      `json:"edge_count"`
	AssociatedTopics []string `json:"associated_topics"`
}

// ============================================================================
// L3 Node/Edge list types
// ============================================================================

// NodeListQuery is the query for listing L3 nodes by graph.
type NodeListQuery struct {
	Page          int      `json:"page"`
	PageSize      int      `json:"page_size"`
	NodeType      *string  `json:"node_type,omitempty"`
	Keyword       *string  `json:"keyword,omitempty"`
	MinImportance *float32 `json:"min_importance,omitempty"`
}

// NodeListResult is the paginated L3 node list response.
type NodeListResult struct {
	Items    []GraphNode `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	HasMore  bool        `json:"has_more"`
}

// EdgeListQuery is the query for listing L3 edges by graph.
type EdgeListQuery struct {
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
	Kind     *model.GraphEdgeKind `json:"kind,omitempty"`
	NodeID   *string              `json:"node_id,omitempty"`
}

// EdgeListResult is the paginated L3 edge list response.
type EdgeListResult struct {
	Items    []GraphEdge `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	HasMore  bool        `json:"has_more"`
}

// ============================================================================
// L4 Search, Merge, Profile types
// ============================================================================

// L4SearchQuery is the query for L4 archive searches.
type L4SearchQuery struct {
	Recent    *int       `json:"recent,omitempty"`
	TimeRange *TimeRange `json:"time_range,omitempty"`
	Keywords  []string   `json:"keywords,omitempty"`
}

// MergeNodesRequest is the request to merge secondary scenes into a main scene.
type MergeNodesRequest struct {
	MainSceneID       string   `json:"main_scene_id"`
	SecondarySceneIDs []string `json:"secondary_scene_ids"`
}

// MergeNodesResult is the result of merging scenes.
type MergeNodesResult struct {
	MainSceneID     string `json:"main_scene_id"`
	MergedNodeCount uint32 `json:"merged_node_count"`
}

// UpdateProfileRequest is the request to update L0 profile.
type UpdateProfileRequest struct {
	Name            *string           `json:"name,omitempty"`
	Role            *string           `json:"role,omitempty"`
	Personality     *string           `json:"personality,omitempty"`
	Worldview       *string           `json:"worldview,omitempty"`
	Preferences     map[string]string `json:"preferences,omitempty"`
	Lexicon         map[string]string `json:"lexicon,omitempty"`
	StyleTraits     []string          `json:"style_traits,omitempty"`
	EmotionPatterns map[string]string `json:"emotion_patterns,omitempty"`
}

// ProfileDelta is an alias for UpdateProfileRequest.
type ProfileDelta = UpdateProfileRequest

// ---------------------------------------------------------------------------
// L3 Search types (used by SearchL3Nodes which lives in crud)
// ---------------------------------------------------------------------------

// L3SearchQuery is the L3 knowledge search query.
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

// ---------------------------------------------------------------------------
// L1 Graph types
// ---------------------------------------------------------------------------

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

// ============================================================================
// Update / L5 types (moved from write to break cyclic dep)
// ============================================================================

// UpdateRequest is a layer-generic memory update request.
type UpdateRequest struct {
	ID     string                     `json:"id"`
	Layer  uint8                      `json:"layer"`
	Fields map[string]json.RawMessage `json:"fields"`
}

// UpdateResult is the response to an update request.
type UpdateResult struct {
	Status UpdateStatus `json:"status"`
	ID     string       `json:"id"`
}

// UpdateStatus indicates what happened during an update.
type UpdateStatus string

const (
	StatusCreated  UpdateStatus = "Created"
	StatusUpdated  UpdateStatus = "Updated"
	StatusArchived UpdateStatus = "Archived"
)

// MarshalJSON implements custom JSON encoding for UpdateStatus.
func (s UpdateStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(s))
}

// UnmarshalJSON implements custom JSON decoding for UpdateStatus.
func (s *UpdateStatus) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*s = UpdateStatus(v)
	return nil
}

// UpdateL2Fields are partial update fields for an L2 TopicSlot.
type UpdateL2Fields struct {
	UserKeywords  []string `json:"user_keywords,omitempty"`
	AgentKeywords []string `json:"agent_keywords,omitempty"`
	FusedSummary  *string  `json:"fused_summary,omitempty"`
	L3Refs        []string `json:"l3_refs,omitempty"`
}

// UpdateL3Fields are partial update fields for an L3 hypergraph.
type UpdateL3Fields struct {
	Name *string `json:"name,omitempty"`
}

// UpdateL5Fields are partial update fields for an L5 action chain.
type UpdateL5Fields struct {
	Title         *string  `json:"title,omitempty"`
	Trigger       *string  `json:"trigger,omitempty"`
	Status        *string  `json:"status,omitempty"`
	Confidence    *float32 `json:"confidence,omitempty"`
	SuccessRate   *float32 `json:"success_rate,omitempty"`
	TriggerCount  *uint32  `json:"trigger_count,omitempty"`
	LastTriggered *int64   `json:"last_triggered,omitempty"`
}

// L5ChainInput defines a new L5 ActionChain to create.
type L5ChainInput struct {
	Title   string        `json:"title"`
	Trigger string        `json:"trigger"`
	Steps   []L5StepInput `json:"steps,omitempty"`
}

// L5StepInput defines a step to append to an ActionChain.
type L5StepInput struct {
	Action     string  `json:"action"`
	Parameters *string `json:"parameters,omitempty"` // JSON string
}

// L5ChainUpdate pairs a chain ID with field updates for batch operations.
type L5ChainUpdate struct {
	ID     string         `json:"id"`
	Fields UpdateL5Fields `json:"fields"`
}
