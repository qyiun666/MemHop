// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0-L7 data models for the MemHop memory database.
package core

import (
	"fmt"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
)

// ContentType represents the type of content stored in an ArchiveSlot.
type ContentType uint8

const (
	ContentText     ContentType = 0
	ContentImage    ContentType = 1
	ContentVideo    ContentType = 2
	ContentDocument ContentType = 3
	ContentAudio    ContentType = 4
	ContentCode     ContentType = 5
	ContentOther    ContentType = 0xFF
)

var contentTypeNames = map[ContentType]string{
	ContentText: "text", ContentImage: "image", ContentVideo: "video",
	ContentDocument: "document", ContentAudio: "audio",
	ContentCode: "code", ContentOther: "other",
}

func (c ContentType) String() string { return common.EnumString(c, contentTypeNames, "ContentType") }

// CapabilityStatus represents the lifecycle state of an L5 capability.
type CapabilityStatus uint8

const (
	CapabilityDraft      CapabilityStatus = 0
	CapabilityActive     CapabilityStatus = 1
	CapabilityDeprecated CapabilityStatus = 2
)

var capabilityStatusNames = map[CapabilityStatus]string{
	CapabilityDraft: "draft", CapabilityActive: "active", CapabilityDeprecated: "deprecated",
}

func (c CapabilityStatus) String() string {
	return common.EnumString(c, capabilityStatusNames, "CapabilityStatus")
}

// CapabilityType describes how an L5 capability is implemented: a wrapper
// around a single MCP tool, a single skill, or a composite of several
// resources.
type CapabilityType string

const (
	CapabilityMCP       CapabilityType = "mcp"
	CapabilitySkill     CapabilityType = "skill"
	CapabilityComposite CapabilityType = "composite"
)

// CapabilityOrigin records where a capability came from.
type CapabilityOrigin string

const (
	CapabilityOriginImported     CapabilityOrigin = "imported"
	CapabilityOriginCrystallized CapabilityOrigin = "crystallized"
	CapabilityOriginHost         CapabilityOrigin = "host"
	// CapabilityOriginBuiltin marks the read-only reference manuals shipped
	// with the project; they are attached to L5 responses, never stored.
	CapabilityOriginBuiltin CapabilityOrigin = "builtin"
)

// HyperedgeKind classifies L1 hyperedges in the hypergraph skeleton.
type HyperedgeKind uint8

const (
	HyperCoOccurrence HyperedgeKind = 0
	HyperCausal       HyperedgeKind = 1
	HyperSemantic     HyperedgeKind = 2
	HyperTemporal     HyperedgeKind = 3
	HyperHierarchical HyperedgeKind = 4
	HyperSequence     HyperedgeKind = 5
)

var hyperedgeKindNames = map[HyperedgeKind]string{
	HyperCoOccurrence: "co_occurrence", HyperCausal: "causal",
	HyperSemantic: "semantic", HyperTemporal: "temporal",
	HyperHierarchical: "hierarchical", HyperSequence: "sequence",
}

func (k HyperedgeKind) String() string {
	return common.EnumString(k, hyperedgeKindNames, "HyperedgeKind")
}

// SourceKind identifies how an L3 HypergraphSlot was created.
type SourceKind uint8

const (
	SourcePath    SourceKind = 0
	SourceContext SourceKind = 1
	SourceURL     SourceKind = 2
	SourceManual  SourceKind = 3
)

var sourceKindNames = map[SourceKind]string{
	SourcePath: "path", SourceContext: "context",
	SourceURL: "url", SourceManual: "manual",
}

func (s SourceKind) String() string { return common.EnumString(s, sourceKindNames, "SourceKind") }

// GraphEdgeKind classifies edges within an L3 hypergraph.
type GraphEdgeKind uint8

const (
	EdgeRelated    GraphEdgeKind = 0
	EdgeCausal     GraphEdgeKind = 1
	EdgePartOf     GraphEdgeKind = 2
	EdgeSequence   GraphEdgeKind = 3
	EdgeDependency GraphEdgeKind = 4
	EdgeCustom     GraphEdgeKind = 5
)

var graphEdgeKindNames = map[GraphEdgeKind]string{
	EdgeRelated: "related", EdgeCausal: "causal", EdgePartOf: "part_of",
	EdgeSequence: "sequence", EdgeDependency: "dependency", EdgeCustom: "custom",
}

func (k GraphEdgeKind) String() string {
	return common.EnumString(k, graphEdgeKindNames, "GraphEdgeKind")
}

// Layer identifies which of the six cognitive memory layers a value belongs to.
type Layer uint8

const (
	LayerL0 Layer = 0
	LayerL1 Layer = 1
	LayerL2 Layer = 2
	LayerL3 Layer = 3
	LayerL4 Layer = 4
	LayerL5 Layer = 5
)

var layerNames = map[Layer]string{
	LayerL0: "L0", LayerL1: "L1", LayerL2: "L2",
	LayerL3: "L3", LayerL4: "L4", LayerL5: "L5",
}

func (l Layer) String() string { return common.EnumString(l, layerNames, "Layer") }

type ProfileSlot struct {
	IDHash          uint64            `json:"id_hash"`
	Name            string            `json:"name"`
	Role            string            `json:"role"`
	Personality     string            `json:"personality"`
	Preferences     map[string]string `json:"preferences"`
	Lexicon         map[string]string `json:"lexicon"`
	StyleTraits     []string          `json:"style_traits"`
	EmotionPatterns map[string]string `json:"emotion_patterns"`
}

// SceneNode is an L1 hypergraph node linking multiple L2 topics.
type SceneNode struct {
	IDHash        uint64   `json:"id_hash"`
	SceneID       uint64   `json:"scene_id"`
	TopicIDs      []uint64 `json:"topic_ids"`
	VectorPageRef uint64   `json:"vector_page_ref"`
	Importance    float32  `json:"importance"`
	Valence       float64  `json:"valence"`
	Arousal       float64  `json:"arousal"`
	CreatedAt     int64    `json:"created_at"`
	UpdatedAt     int64    `json:"updated_at"`
	EdgeIDs       []uint64 `json:"edge_ids"`
	// LastDecayAt: last decay time (ms); 0 = never decayed, first decay starts from CreatedAt.
	LastDecayAt int64 `json:"last_decay_at"`
}

// SceneEdge is an L1 hyperedge used by upper-layer decay logic.
type SceneEdge struct {
	IDHash    uint64        `json:"id_hash"`
	Kind      HyperedgeKind `json:"kind"`
	NodeIDs   []uint64      `json:"node_ids"`
	Weight    float32       `json:"weight"`
	CreatedAt int64         `json:"created_at"`
	// LastDecayAt: last decay time (ms); 0 = never decayed, first decay starts from CreatedAt.
	LastDecayAt int64 `json:"last_decay_at"`
}

// SceneNodeID derives the stable L1 node ID of a scene: hash("l1:"+hex(sceneID)).
// The node is created/updated only during Dream, but the ID is computable at
// query time without any index, which is what makes spreading-activation
// association a pure storage-level graph walk.
func SceneNodeID(sceneID uint64) uint64 {
	return common.HashID("l1:" + common.FormatHash(sceneID))
}

// SceneSlot is an L2 scene container holding multiple session topics;
// HitCount/LastHitAt fold the former L6 scene-usage feedback into the scene
// record (retrieval-hit statistics consumed by Dream's usage feedback).
type SceneSlot struct {
	SceneID    uint64 `json:"scene_id"`
	SceneName  string `json:"scene_name"`
	TopicCount int    `json:"topic_count"` // depth-1 root topics under this scene
	HitCount   uint32 `json:"hit_count"`   // cumulative retrieval hit count
	LastHitAt  int64  `json:"last_hit_at"` // last hit time (Unix ms)
}

// NewSceneSlot builds a SceneSlot from a name; ID is the xxhash64 of the name.
func NewSceneSlot(name string) SceneSlot {
	return SceneSlot{
		SceneID:   common.HashID(name),
		SceneName: name,
	}
}

// TopicSlot is an L2 dual-track session node (user/agent). Tree: parent_id
// (nil = depth-1 root) + children_ids. Depth 1 = raw turns, 2 = compression
// groups, 3 = meta summaries; depth >= 4 triggers subtree deletion on Dream.
type TopicSlot struct {
	ID          uint64   `json:"id"`
	SceneID     uint64   `json:"scene_id"`
	ParentID    *uint64  `json:"parent_id,omitempty"`
	ChildrenIDs []uint64 `json:"children_ids"`
	Depth       uint8    `json:"depth"`

	UserKeywords  []string `json:"user_keywords"`
	UserTimestamp int64    `json:"user_timestamp"` // Dream node = earliest user turn in group

	L4Refs []uint64 `json:"l4_refs"`
	L3Refs []uint64 `json:"l3_refs"`

	AgentKeywords  []string `json:"agent_keywords"`
	AgentTimestamp int64    `json:"agent_timestamp"` // Dream node = latest agent reply in group

	FusedKeywords []string `json:"fused_keywords"`

	CentroidPageRef uint64 `json:"centroid_page_ref"`
}

// ComputeTopicID derives a topic ID from sceneID and both timestamps.
// Dream-created fused topics use this form for deterministic replay.
func ComputeTopicID(sceneID uint64, userTS, agentTS int64) uint64 {
	combined := fmt.Sprintf("%d:%d:%d", sceneID, userTS, agentTS)
	return common.HashID(combined)
}

// ComputeTopicIDForText derives a Search-created topic ID that also binds
// the user text. This keeps the timestamp-only ID stable for Dream, while
// two different messages that happen to share scene and millisecond no
// longer collide and overwrite each other.
func ComputeTopicIDForText(sceneID uint64, userTS int64, text string) uint64 {
	combined := fmt.Sprintf("%d:%d:0:%s", sceneID, userTS, text)
	return common.HashID(combined)
}

// HypergraphSource represents the origin of an L3 hypergraph.
type HypergraphSource struct {
	Kind      SourceKind `json:"kind"`
	Value     string     `json:"value"`      // path or URL string; empty for Manual
	ContextID uint64     `json:"context_id"` // used when Kind == SourceContext
}

// HypergraphSlot holds L3 hypergraph container metadata.
type HypergraphSlot struct {
	IDHash    uint64           `json:"id_hash"`
	Name      string           `json:"name"`
	Source    HypergraphSource `json:"source"`
	CreatedAt int64            `json:"created_at"`
	UpdatedAt int64            `json:"updated_at"`
}

// HypergraphNode is a node within an L3 hypergraph.
type HypergraphNode struct {
	IDHash     uint64   `json:"id_hash"`
	GraphID    uint64   `json:"graph_id"`
	Title      string   `json:"title"`
	NodeType   string   `json:"node_type"`
	Content    string   `json:"content"`
	Keywords   []string `json:"keywords"`
	SourceRef  *string  `json:"source_ref,omitempty"`
	Importance float32  `json:"importance"`
	CreatedAt  int64    `json:"created_at"`
	UpdatedAt  int64    `json:"updated_at"`
}

// HypergraphEdge is an edge within an L3 hypergraph (supports hyperedges).
type HypergraphEdge struct {
	IDHash    uint64        `json:"id_hash"`
	GraphID   uint64        `json:"graph_id"`
	Kind      GraphEdgeKind `json:"kind"`
	NodeIDs   []uint64      `json:"node_ids"`
	Weight    float32       `json:"weight"`
	Label     *string       `json:"label,omitempty"`
	CreatedAt int64         `json:"created_at"`
}

// AdjacencyEntry is one entry in the adjacency index for a node.
type AdjacencyEntry struct {
	NodeHash     uint64
	EdgeHash     uint64
	Kind         GraphEdgeKind
	ConnectedIDs []uint64 // other nodes in this hyperedge (excluding NodeHash)
}

// Message roles in an ArchiveSlot.
const (
	RoleUser   uint8 = 0
	RoleAgent  uint8 = 1
	RoleSystem uint8 = 2
	RoleDream  uint8 = 3
)

// ArchiveSlot stores a user/agent chat message under an L2 scene context.
type ArchiveSlot struct {
	IDHash      uint64      `json:"id_hash"`
	ContentType ContentType `json:"content_type"`
	Role        uint8       `json:"role"`
	ContextID   uint64      `json:"context_id"`
	CreatedAt   int64       `json:"created_at"`
	Content     string      `json:"content"`
	Metadata    *string     `json:"metadata,omitempty"`
}

// Capability is an L5 reusable capability: a wrapper around host resources
// (MCP tools / skills) that MemHop stores and matches but never executes.
type Capability struct {
	IDHash        uint64           `json:"id_hash"`
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

// NormalizeCapabilityName returns the canonical lowercase name used for IDs
// and duplicate detection.
func NormalizeCapabilityName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// CapabilityID derives the stable L5 record ID from a capability name.
func CapabilityID(name string) uint64 {
	return common.HashID("capability:" + NormalizeCapabilityName(name))
}

// PromptCard renders the concise capability view intended for an LLM prompt.
func (c Capability) PromptCard() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[capability: %s]\n", c.Name)
	fmt.Fprintf(&b, "id: %s\n", common.FormatHash(c.IDHash))
	fmt.Fprintf(&b, "type: %s\n", c.Type)
	if c.Version != "" {
		fmt.Fprintf(&b, "version: %s\n", c.Version)
	}
	if c.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", c.Summary)
	}
	if c.Trigger != "" {
		fmt.Fprintf(&b, "trigger: %s\n", c.Trigger)
	}
	for _, r := range c.Resources {
		fmt.Fprintf(&b, "resource: %s %s", r.Type, r.Name)
		if r.Ref != "" {
			fmt.Fprintf(&b, " (%s)", r.Ref)
		}
		b.WriteByte('\n')
		if r.Description != "" {
			fmt.Fprintf(&b, "  use: %s\n", r.Description)
		}
	}
	if c.Workflow != nil {
		refs := make([]string, 0, len(c.Workflow.Steps))
		for _, step := range c.Workflow.Steps {
			refs = append(refs, step.Ref)
		}
		fmt.Fprintf(&b, "flow: %s\n", strings.Join(refs, " -> "))
	}
	if c.TriggerCount > 0 || c.SuccessRate > 0 {
		fmt.Fprintf(&b, "usage: %d, success_rate: %.2f\n", c.TriggerCount, c.SuccessRate)
	}
	return b.String()
}

// ResourceRef is one wrapped resource: an MCP tool or a skill together with
// usage instructions for the host. MemHop stores these references but does
// not execute them.
type ResourceRef struct {
	Type        CapabilityType `json:"type"` // mcp | skill
	Name        string         `json:"name"`
	Ref         string         `json:"ref,omitempty"` // MCP server address / skill path / command
	Description string         `json:"description,omitempty"`
	Config      *string        `json:"config,omitempty"`
}

// Workflow is the ordered orchestration of a composite capability.
type Workflow struct {
	Steps []WorkflowStep `json:"steps"`
}

// WorkflowStep is one orchestration step referencing a resource (by
// Resources[].Name) or another capability (by name).
type WorkflowStep struct {
	Ref    string `json:"ref"`
	Action string `json:"action,omitempty"`
}

// TrajectorySlot is an L7 operation trajectory event appended by the host
// during the agent loop; short-lived, purged by the host via DeleteTrajectory.
type TrajectorySlot struct {
	IDHash    uint64  `json:"id_hash"`          // hash(sessionID:seq)
	SessionID uint64  `json:"session_id"`       // owning L2 scene
	Seq       uint64  `json:"seq"`              // per-session increasing sequence
	EventType string  `json:"event_type"`       // turn_start/tool_call/tool_result/subagent_spawn/subagent_done/context_inject/llm_request/llm_output/turn_end
	Payload   string  `json:"payload"`          // event content (truncated to 4KB; no raw token stream)
	L4Ref     *uint64 `json:"l4_ref,omitempty"` // reference to the L4 archive instead of duplicating dialogue
	Timestamp int64   `json:"timestamp"`
}
