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
	ScenePatch               = internal.ScenePatch
	CapabilityImport         = internal.CapabilityImport
	CapabilityPackageDoc     = internal.CapabilityPackageDoc
	TrajectorySessionSummary = internal.TrajectorySessionSummary
	PlanStatus               = internal.PlanStatus
	DreamReport              = internal.DreamReport
	DreamStage               = internal.DreamStage
	CrystallizeOutput        = internal.CrystallizeOutput
	CrystallizeCapability    = internal.CrystallizeCapability
	SceneContext             = internal.SceneContext
	SceneContextTopic        = internal.SceneContextTopic
	SceneMessage             = internal.SceneMessage
	ResourceRef              = internal.ResourceRef
	GraphEdgeKind            = internal.GraphEdgeKind
	CapabilityType           = internal.CapabilityType
	ContentType              = internal.ContentType
	ArchiveKind              = internal.ArchiveKind
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

// TopicSlot is one L2 conversation node: a single turn settled by Update, or a
// Dream-fused group of turns. FusedKeywords is its only keyword track — the
// set a host reads back as its conversation context. What was said is not on
// the topic: the L4 archives a turn owns are addressed by that topic's id.
type TopicSlot struct {
	ID             string   `json:"id"`
	SceneID        string   `json:"scene_id"`
	ParentID       *string  `json:"parent_id,omitempty"`
	ChildrenIDs    []string `json:"children_ids"`
	Depth          uint8    `json:"depth"`
	FusedKeywords  []string `json:"fused_keywords"`
	UserTimestamp  int64    `json:"user_timestamp"`
	AgentTimestamp int64    `json:"agent_timestamp"`
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
	IDHash    string   `json:"id_hash"`
	GraphID   string   `json:"graph_id"`
	Title     string   `json:"title"`
	NodeType  string   `json:"node_type"`
	Content   string   `json:"content"`
	Keywords  []string `json:"keywords"`
	SourceRef *string  `json:"source_ref,omitempty"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
}

// HypergraphEdge is a hyperedge within an L3 hypergraph: an unordered relation
// over its member nodes, distinguished by Kind (the edge id covers members and
// kind, so one node pair can carry several relations at once).
type HypergraphEdge struct {
	IDHash    string        `json:"id_hash"`
	GraphID   string        `json:"graph_id"`
	Kind      GraphEdgeKind `json:"kind"`
	NodeIDs   []string      `json:"node_ids"`
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

// ArchiveSlot is one record of a topic's L4 content: a dialogue original
// (KindUtterance) or an operation event (KindEvent). ContextID is the topic that
// owns it and Seq the slot it owns there, so (ContextID, Seq) is the address a
// replay can rewrite. Role is one of RoleUser / RoleAgent (settled by Update) or
// RoleDream (a fused group's summary) and qualifies utterances only; ContentType
// says whether Content is prose or a reference to media; EventType names an
// event and is the host's own word for it.
type ArchiveSlot struct {
	IDHash      string      `json:"id_hash"`
	Kind        ArchiveKind `json:"kind"`
	Seq         uint64      `json:"seq"`
	ContentType ContentType `json:"content_type"`
	Role        uint8       `json:"role"`
	ContextID   string      `json:"context_id"`
	EventType   string      `json:"event_type,omitempty"`
	NodePath    string      `json:"node_path,omitempty"`
	CreatedAt   int64       `json:"created_at"`
	Content     string      `json:"content"`
}

// TrajectorySlot is one turn event — the read view of an L4 record of kind
// event. SessionID is the topic id Search minted for the turn, which holds both
// that turn's events and the plan tree it opened. Plan nodes are read through
// PlanState (PlanNodeView), not here: they are L6 records, not content.
//
// NodePath names the step an event belongs to; the library stamps it on write, so
// a host can attribute an event to a step without deriving anything. A bare turn
// event leaves it empty.
//
// On every write path (AppendTrajectory, PlanCommit) SessionID/NodePath/Seq are
// assigned by the library and are read-only here; of the event you pass, only
// EventType, Payload and Timestamp are stored.
type TrajectorySlot struct {
	IDHash    string `json:"id_hash"`
	SessionID string `json:"session_id"`
	Seq       uint64 `json:"seq"`
	EventType string `json:"event_type"`
	Payload   string `json:"payload"`
	Timestamp int64  `json:"timestamp"`

	NodePath string `json:"node_path,omitempty"`
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
	Children   []PlanNodeView `json:"children"`
}

// PlanStep is one host commit's node-side fields, passed to PlanCommit for the
// node named by NodePath (a node missing along the path is created as pending,
// which is how a step is added).
//
// The fields are not symmetric. A blank Title/Type/Summary keeps what the node
// already holds, so committing a step again never rewinds its title or erases a
// folded summary. Status has no blank meaning: it is the string surface
// (pending / in_progress / running / done / failed), every commit states it
// explicitly, and an unknown value is refused before the tree moves.
type PlanStep struct {
	Title   string `json:"title"`
	Type    string `json:"type"` // plan/step/tool_call; empty = plain node
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

// PlanTree is the external forest view of one plan: every top-level step is
// a root, and Done/Total count all roots.
type PlanTree struct {
	Roots      []PlanNodeView `json:"roots"`
	DoneCount  int            `json:"done_count"`
	TotalCount int            `json:"total_count"`
}
