// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Public type surface of the MemHop facade. Response DTOs that carry record
// IDs use 16-char hex strings (api layer) while the internal/core layers stay
// on uint64. Input-only types remain aliases to the internal seam.

package api

import "github.com/qyiun666/MemHop/internal"

// ---- config ----

type (
	// MemHopConfig configures a MemHop database; nested fields (LLM, Defaults)
	// are assigned by field access, see DefaultMemHopDefaults. The LLM endpoint
	// is the only external service the engine contacts — no embedding service.
	MemHopConfig = internal.MemHopConfig
	// LlmConfig holds LLM provider settings; exported so hosts can build
	// MemHopConfig.LLM by literal instead of field-by-field assignment.
	LlmConfig = internal.LlmConfig
	// MemHopDefaults holds the host-facing business knobs (consolidation
	// thresholds and the idle-domain TTL); engine tuning constants are
	// package-private. Exported so hosts can name the type instead of copying
	// DefaultMemHopDefaults.
	MemHopDefaults = internal.MemHopDefaults
)

// DefaultMemHopDefaults is the shared default engine configuration; assign
// it to MemHopConfig.Defaults without naming the nested type.
var DefaultMemHopDefaults = internal.DefaultMemHopDefaults

// ---- input / shared aliases ----

type (
	SearchQuery              = internal.SearchQuery
	TurnUpdate               = internal.TurnUpdate
	L3ImportItem             = internal.L3ImportItem
	L3Relation               = internal.L3Relation
	L3ImportMode             = internal.L3ImportMode
	L3ImportResult           = internal.L3ImportResult
	L3NodeQuery              = internal.L3NodeQuery
	L4Query                  = internal.L4Query
	CapabilityListQuery      = internal.CapabilityListQuery
	CapabilityPatch          = internal.CapabilityPatch
	CapabilityImport         = internal.CapabilityImport
	TrajectorySessionSummary = internal.TrajectorySessionSummary
	PlanStatus               = internal.PlanStatus
	DreamReport              = internal.DreamReport
	DreamStage               = internal.DreamStage
	CrystallizeResult        = internal.CrystallizeResult
	CrystallizeDetail        = internal.CrystallizeDetail
	SceneContext             = internal.SceneContext
	SceneContextTopic        = internal.SceneContextTopic
	SceneMessage             = internal.SceneMessage
	ResourceRef              = internal.ResourceRef
	GraphEdgeKind            = internal.GraphEdgeKind
	CapabilityType           = internal.CapabilityType
	CapabilityStatus         = internal.CapabilityStatus
	CapabilityOrigin         = internal.CapabilityOrigin
	ContentType              = internal.ContentType
	Workflow                 = internal.Workflow
)

// ---- response DTOs (ids are 16-char hex strings) ----

// ProfileSlot is the public L0 profile singleton; the internal ID hash is
// hidden because it is an implementation detail.
type ProfileSlot struct {
	Name         string                `json:"name"`
	Role         string                `json:"role"`
	Personality  string                `json:"personality"`
	EmotionState internal.EmotionScore `json:"emotion_state"`
	MBTI         internal.MBTIScore    `json:"mbti"`
	Preferences  map[string]string     `json:"preferences"`
	UpdatedAtMs  int64                 `json:"updated_at_ms"`
}

// SceneSlot is one L2 scene container — a host session. L3ID is its optional
// project-domain anchor.
type SceneSlot struct {
	SceneID    string `json:"scene_id"`
	SceneName  string `json:"scene_name"`
	TopicCount int    `json:"topic_count"`
	HitCount   uint32 `json:"hit_count"`
	LastHitAt  int64  `json:"last_hit_at"`
	L3ID       string `json:"l3_id,omitempty"`
}

// TopicSlot is one L2 conversation node: a single turn written by Update, or
// a Dream-fused group of turns. FusedKeywords is its only keyword track — the
// set a host reads back as its conversation context.
type TopicSlot struct {
	ID             string   `json:"id"`
	SceneID        string   `json:"scene_id"`
	ParentID       *string  `json:"parent_id,omitempty"`
	ChildrenIDs    []string `json:"children_ids"`
	Depth          uint8    `json:"depth"`
	FusedKeywords  []string `json:"fused_keywords"`
	UserTimestamp  int64    `json:"user_timestamp"`
	AgentTimestamp int64    `json:"agent_timestamp"`
	L4Refs         []string `json:"l4_refs"`
}

// SearchResult is the read surface of one scene: the scene record, its
// depth-1 topics in turn order, the domain's L0 profile, and NewTopicID — the
// topic this read opened for the turn the host is about to run. Update settles
// that turn into it, and the L6 trajectory writes key on it.
type SearchResult struct {
	Profile      ProfileSlot `json:"profile"`
	ProfileBrief string      `json:"profile_brief"`
	Scene        SceneSlot   `json:"scene"`
	Topics       []TopicSlot `json:"topics"`
	NewTopicID   string      `json:"new_topic_id"`
}

// HypergraphSource is the origin of an L3 hypergraph.
type HypergraphSource struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	ContextID string `json:"context_id"`
}

// HypergraphSlot holds L3 hypergraph container metadata.
type HypergraphSlot struct {
	IDHash    string           `json:"id_hash"`
	Name      string           `json:"name"`
	Source    HypergraphSource `json:"source"`
	CreatedAt int64            `json:"created_at"`
	UpdatedAt int64            `json:"updated_at"`
}

// HypergraphNode is a node within an L3 hypergraph.
type HypergraphNode struct {
	IDHash     string   `json:"id_hash"`
	GraphID    string   `json:"graph_id"`
	Title      string   `json:"title"`
	NodeType   string   `json:"node_type"`
	Content    string   `json:"content"`
	Keywords   []string `json:"keywords"`
	SourceRef  *string  `json:"source_ref,omitempty"`
	Importance float32  `json:"importance"`
	CreatedAt  int64    `json:"created_at"`
	UpdatedAt  int64    `json:"updated_at"`
}

// HypergraphEdge is an edge within an L3 hypergraph.
type HypergraphEdge struct {
	IDHash    string        `json:"id_hash"`
	GraphID   string        `json:"graph_id"`
	Kind      GraphEdgeKind `json:"kind"`
	NodeIDs   []string      `json:"node_ids"`
	Weight    float32       `json:"weight"`
	Label     *string       `json:"label,omitempty"`
	CreatedAt int64         `json:"created_at"`
}

// L3Graph is the full view of one L3 hypergraph.
type L3Graph struct {
	Slot  HypergraphSlot   `json:"slot"`
	Nodes []HypergraphNode `json:"nodes"`
	Edges []HypergraphEdge `json:"edges"`
}

// L3Subgraph is a BFS subgraph view.
type L3Subgraph struct {
	Nodes []HypergraphNode `json:"nodes"`
	Edges []HypergraphEdge `json:"edges"`
}

// ArchiveSlot stores a user/agent chat message under an L2 scene context.
// Role is one of RoleUser / RoleAgent / RoleSystem / RoleDream.
type ArchiveSlot struct {
	IDHash      string      `json:"id_hash"`
	ContentType ContentType `json:"content_type"`
	Role        uint8       `json:"role"`
	ContextID   string      `json:"context_id"`
	CreatedAt   int64       `json:"created_at"`
	Content     string      `json:"content"`
	Metadata    *string     `json:"metadata,omitempty"`
}

// Capability is an L5 reusable capability.
type Capability struct {
	IDHash        string           `json:"id_hash"`
	Name          string           `json:"name"`
	Version       string           `json:"version"`
	Type          CapabilityType   `json:"type"`
	Summary       string           `json:"summary"`
	Trigger       string           `json:"trigger"`
	Resources     []ResourceRef    `json:"resources"`
	Workflow      *Workflow        `json:"workflow,omitempty"`
	Status        CapabilityStatus `json:"status"`
	Origin        CapabilityOrigin `json:"origin"`
	FileHash      string           `json:"file_hash,omitempty"`
	SuccessRate   float32          `json:"success_rate"`
	TriggerCount  uint32           `json:"trigger_count"`
	LastTriggered int64            `json:"last_triggered"`
	CreatedAt     int64            `json:"created_at"`
	UpdatedAt     int64            `json:"updated_at"`
}

// TrajectorySlot is one L6 operation trajectory event. SessionID is the
// trajectory key: the turn's topic id Search minted for it (TopicID then
// carries the same value), or the plan id of a plan-bound event.
// NodeType is NodeTypeEvent or NodeTypePlan; Status carries the numeric
// StatusPending.. codes (only meaningful on nodes a plan wrote).
// On the plan write paths (PlanAppend/PlanCommit) the record is forced to
// bare-event semantics — NodeType/ParentID/NodePath/Status/Summary are
// cleared and PlanID/PlanNodeRef/Seq are assigned by the library, so
// caller-supplied values in those fields are ignored.
type TrajectorySlot struct {
	IDHash    string `json:"id_hash"`
	SessionID string `json:"session_id"`
	Seq       uint64 `json:"seq"`
	EventType string `json:"event_type"`
	Payload   string `json:"payload"`
	TopicID   string `json:"topic_id,omitempty"` // L2 topic the turn resolves to, 16-char hex
	Timestamp int64  `json:"timestamp"`

	NodeType    uint8  `json:"node_type,omitempty"`
	PlanID      string `json:"plan_id,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
	NodePath    string `json:"node_path,omitempty"`
	Status      uint8  `json:"status,omitempty"`
	Summary     string `json:"summary,omitempty"`
	PlanType    string `json:"plan_type,omitempty"`
	PlanNodeRef string `json:"plan_node_ref,omitempty"`
	FinishedAt  int64  `json:"finished_at,omitempty"`
}

// PlanNodeView is the external plan-tree node; Status is the string form.
type PlanNodeView struct {
	NodePath   string         `json:"node_path"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	Type       string         `json:"type"`
	Summary    string         `json:"summary"`
	FinishedAt int64          `json:"finished_at"`
	ChildCount int            `json:"child_count"`
	TrajCount  int            `json:"traj_count"`
	Children   []PlanNodeView `json:"children"`
}

// PlanNode is the host-supplied full plan tree for SyncPlanTree; NodePath is
// assigned by the host, and Status is the string surface (pending/in_progress/
// running/done/failed; "" defaults to pending).
type PlanNode struct {
	NodePath string     `json:"node_path"`
	Title    string     `json:"title"`
	Type     string     `json:"type"`
	Status   string     `json:"status"`
	Summary  string     `json:"summary"`
	Children []PlanNode `json:"children"`
}

// PlanTree is the external forest view of one plan: every top-level step is
// a root, and Done/Total count all roots.
type PlanTree struct {
	Roots      []PlanNodeView `json:"roots"`
	DoneCount  int            `json:"done_count"`
	TotalCount int            `json:"total_count"`
}

// PlanSummary is one plan's footprint returned by ListPlans.
type PlanSummary struct {
	PlanID       string `json:"plan_id"`
	CreatedAt    int64  `json:"created_at"`
	LastActiveAt int64  `json:"last_active_at"`
	NodeCount    int    `json:"node_count"`
	DoneCount    int    `json:"done_count"`
	TotalCount   int    `json:"total_count"`
	Active       bool   `json:"active"`
}
