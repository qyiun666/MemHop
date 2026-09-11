// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Public type surface of the MemHop facade. Response DTOs that carry record
// IDs use 16-char hex strings (api layer) while the internal/core layers stay
// on uint64. Input-only types remain aliases to the internal seam.

package api

import "github.com/qyiun666/MemHop/internal"

// ---- config ----

type (
	// LlmConfig holds LLM provider settings: the engine's only external
	// service, with no embedding service and no dimension to declare.
	LlmConfig = internal.LlmConfig
	// MemHopDefaults holds the host-facing business knobs (consolidation
	// thresholds and the idle-domain TTL); engine tuning constants are
	// package-private. Exported so hosts can name the type instead of copying
	// DefaultMemHopDefaults.
	MemHopDefaults = internal.MemHopDefaults
)

// DefaultMemHopDefaults is the shared default engine configuration. It is a
// value: pass it to Open as-is, or copy it and edit the copy to tune one open.
var DefaultMemHopDefaults = internal.DefaultMemHopDefaults

// ---- input / shared aliases ----

type (
	SearchQuery       = internal.SearchQuery
	L3ImportItem      = internal.L3ImportItem
	L3Relation        = internal.L3Relation
	L3ImportMode      = internal.L3ImportMode
	L3ImportResult    = internal.L3ImportResult
	L3NodeQuery       = internal.L3NodeQuery
	L4Query           = internal.L4Query
	ScenePatch        = internal.ScenePatch
	PlanStatus        = internal.PlanStatus
	DreamReport       = internal.DreamReport
	DreamStage        = internal.DreamStage
	SceneContext      = internal.SceneContext
	SceneContextTopic = internal.SceneContextTopic
	SceneMessage      = internal.SceneMessage
	GraphEdgeKind     = internal.GraphEdgeKind
	ContentType       = internal.ContentType
	ArchiveKind       = internal.ArchiveKind
)

// ---- response DTOs (ids are 16-char hex strings) ----

// ProfileSlot is the L0 profile as the library hands it back: the host-owned
// fields plus the ones only the library writes. The internal ID hash is hidden
// because it is an implementation detail. Write a profile with a ProfileInput —
// filling in one of the read-only fields here is accepted and ignored, which is
// exactly why it is not the type a host passes in.
type ProfileSlot struct {
	Name         string                `json:"name"`
	Role         string                `json:"role"`
	Personality  string                `json:"personality"`
	EmotionState internal.EmotionScore `json:"emotion_state"`
	MBTI         internal.MBTIScore    `json:"mbti"`
	Preferences  map[string]string     `json:"preferences"`
	// AgentType says which kind of agent this domain holds: AgentTypePrimary
	// for the domain the file was opened on, AgentTypeSub for one created
	// under it. It is stamped when the domain is created and inherited on
	// every host write, so editing a profile cannot move it between the two.
	AgentType   uint8 `json:"agent_type"`
	UpdatedAtMs int64 `json:"updated_at_ms"`
}

// ProfileInput is the profile as a host writes it — the argument to Open,
// SubAgent and UpdateL0. It holds exactly the fields the host owns: a domain's
// name, its role, its personality and its preferences. Name is required at all
// three entries (a blank one is refused) because it is how the domain is
// addressed; the other three may be left empty.
//
// The library-owned fields are absent rather than ignored. On an inbound record
// a blank EmotionState or a zero UpdatedAtMs cannot be told apart from "leave
// what is there", so a shape that carries them would silently discard whatever
// the domain already holds: Dream evolves EmotionState and MBTI, the library
// stamps UpdatedAtMs on every write, and AgentType is decided by how the domain
// came to exist (the file's own, or one created under it) rather than by a
// caller's claim.
type ProfileInput struct {
	Name        string            `json:"name"`
	Role        string            `json:"role"`
	Personality string            `json:"personality"`
	Preferences map[string]string `json:"preferences"`
}

// SceneNodeView is one L1 scene node as a host reads it. Every value is Dream's:
// Importance and the two emotion signals are what consolidation computed, and
// the pipeline is the only writer. EdgeIDs name the co-occurrence edges incident
// on the node. An edge has no read of its own, so those ids are useful exactly
// one way — two nodes sharing one are a pair Dream judged related.
type SceneNodeView struct {
	IDHash     string   `json:"id_hash"`
	SceneID    string   `json:"scene_id"`
	TopicIDs   []string `json:"topic_ids"`
	EdgeIDs    []string `json:"edge_ids"`
	Importance float32  `json:"importance"`
	Valence    float64  `json:"valence"`
	Arousal    float64  `json:"arousal"`
	CreatedAt  int64    `json:"created_at"`
	UpdatedAt  int64    `json:"updated_at"`
}

// SceneSlot is one L2 scene container — a host session. L3ID is its optional
// project-domain anchor.
type SceneSlot struct {
	SceneID   string `json:"scene_id"`
	SceneName string `json:"scene_name"`
	L3ID      string `json:"l3_id,omitempty"`
}

// TopicSlot is one L2 conversation node: a single turn settled by Update, or a
// Dream-fused group of turns. FusedKeywords is its only keyword track — the
// set a host reads back as its conversation context. What was said is not on
// the topic: the L4 archives a turn owns are addressed by that topic's id.
// A fused group names no children either; the turns it swallowed carry
// ParentID pointing back here.
// Name is the host's own label for it, written by RenameTopic and never derived
// by the engine; empty means nobody has named this topic yet.
type TopicSlot struct {
	ID             string   `json:"id"`
	SceneID        string   `json:"scene_id"`
	ParentID       *string  `json:"parent_id,omitempty"`
	Depth          uint8    `json:"depth"`
	Name           string   `json:"name,omitempty"`
	FusedKeywords  []string `json:"fused_keywords"`
	UserTimestamp  int64    `json:"user_timestamp"`
	AgentTimestamp int64    `json:"agent_timestamp"`
}

// SearchResult is the read surface of one scene: the scene record, its
// depth-1 topics in turn order, the domain's L0 profile, and NewTopicID — the
// topic this read opened for the turn the host is about to run. Update settles
// that turn into it, and the L4 records and L5 plan tree of that turn key on it.
type SearchResult struct {
	Profile      ProfileSlot `json:"profile"`
	ProfileBrief string      `json:"profile_brief"`
	Scene        SceneSlot   `json:"scene"`
	Topics       []TopicSlot `json:"topics"`
	NewTopicID   string      `json:"new_topic_id"`
}

// HypergraphSlot holds L3 hypergraph container metadata. UpdatedAt is the
// graph's change clock: an import that writes a node or an edge here moves it, as
// does a rename, while a batch that changed nothing leaves it where it was.
type HypergraphSlot struct {
	IDHash    string `json:"id_hash"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
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
// (KindUtterance) or an operation event (KindEvent) — and the same shape
// AppendArchive takes, where IDHash and TopicID are ignored and a Seq of 0 asks
// the library for a slot. TopicID is the topic that owns a stored record and Seq
// the slot it owns there, so (TopicID, Seq) is the address a replay rewrites.
// Role is one of RoleUser / RoleAgent / RoleSystem and qualifies utterances only:
// an event leaves it 0. ContentType says whether Content is prose or a reference to
// media; EventType names an event and is the host's own word for it.
type ArchiveSlot struct {
	IDHash      string      `json:"id_hash"`
	Kind        ArchiveKind `json:"kind"`
	Seq         uint64      `json:"seq"`
	ContentType ContentType `json:"content_type"`
	Role        uint8       `json:"role"`
	TopicID     string      `json:"topic_id"`
	EventType   string      `json:"event_type,omitempty"`
	NodeSeq     uint32      `json:"node_seq,omitempty"`
	CreatedAt   int64       `json:"created_at"`
	Content     string      `json:"content"`
}

// PlanNodeView is the external plan-tree node; Status is the string form. A step
// is addressed by Seq inside its turn, and ParentSeq is the step it hangs under
// (0 = a root).
type PlanNodeView struct {
	Seq        uint32         `json:"seq"`
	ParentSeq  uint32         `json:"parent_seq"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	Summary    string         `json:"summary"`
	CreatedAt  int64          `json:"created_at"`
	FinishedAt int64          `json:"finished_at"`
	UpdatedAt  int64          `json:"updated_at"`
	Children   []PlanNodeView `json:"children"`
}

// PlanStep is one step restated for PlanNodeUpdate. Seq is the ordinal the
// library handed out when the step was created — a host never invents one — and
// it addresses a step inside the turn named by the call's topicID.
//
// The fields are not symmetric. A blank Title/Summary keeps what the node
// already holds, so restating a step never rewinds its title or erases a folded
// summary. Status has no blank meaning: it is the string surface
// (in_progress / done / failed), every update states it, and an unknown value is
// refused before the node is touched.
type PlanStep struct {
	Seq     uint32     `json:"seq"`
	Title   string     `json:"title"`
	Status  PlanStatus `json:"status"`
	Summary string     `json:"summary"`
}

// PlanTree is the external forest view of one plan: every top-level step is a
// root, and Done/Total count every step of every tree rather than the roots
// alone.
type PlanTree struct {
	Roots      []PlanNodeView `json:"roots"`
	DoneCount  int            `json:"done_count"`
	TotalCount int            `json:"total_count"`
}
