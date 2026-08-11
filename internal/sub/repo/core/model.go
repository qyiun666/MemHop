// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0-L5 data models for the MemHop memory database.
package core

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/sub/common"
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

func (c ContentType) MarshalJSON() ([]byte, error) { return common.EnumMarshal(c) }

func (c *ContentType) UnmarshalJSON(data []byte) error { return common.EnumAssign(c, data) }

// ChainStatus represents the lifecycle state of an ActionChainSlot.
type ChainStatus uint8

const (
	ChainDraft      ChainStatus = 0
	ChainActive     ChainStatus = 1
	ChainDeprecated ChainStatus = 2
)

var chainStatusNames = map[ChainStatus]string{
	ChainDraft: "draft", ChainActive: "active", ChainDeprecated: "deprecated",
}

func (c ChainStatus) String() string { return common.EnumString(c, chainStatusNames, "ChainStatus") }

func (c ChainStatus) MarshalJSON() ([]byte, error) { return common.EnumMarshal(c) }

func (c *ChainStatus) UnmarshalJSON(data []byte) error { return common.EnumAssign(c, data) }

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

func (k HyperedgeKind) MarshalJSON() ([]byte, error) { return common.EnumMarshal(k) }

func (k *HyperedgeKind) UnmarshalJSON(data []byte) error { return common.EnumAssign(k, data) }

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

func (s SourceKind) MarshalJSON() ([]byte, error) { return common.EnumMarshal(s) }

func (s *SourceKind) UnmarshalJSON(data []byte) error { return common.EnumAssign(s, data) }

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

func (k GraphEdgeKind) MarshalJSON() ([]byte, error) { return common.EnumMarshal(k) }

func (k *GraphEdgeKind) UnmarshalJSON(data []byte) error { return common.EnumAssign(k, data) }

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

func (l Layer) MarshalJSON() ([]byte, error) { return common.EnumMarshal(l) }

func (l *Layer) UnmarshalJSON(data []byte) error { return common.EnumAssign(l, data) }

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

// SceneSlot is an L2 scene container holding multiple session topics.
type SceneSlot struct {
	SceneID   uint64 `json:"scene_id"`
	SceneName string `json:"scene_name"`
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

// ComputeTopicID derives a unique topic ID from sceneID and both timestamps.
func ComputeTopicID(sceneID uint64, userTS, agentTS int64) uint64 {
	combined := fmt.Sprintf("%d:%d:%d", sceneID, userTS, agentTS)
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

// ActionChainSlot is an L5 action chain.
type ActionChainSlot struct {
	IDHash        uint64      `json:"id_hash"`
	Title         string      `json:"title"`
	Trigger       string      `json:"trigger"`
	Status        ChainStatus `json:"status"`
	Confidence    float32     `json:"confidence"`
	SuccessRate   float32     `json:"success_rate"`
	TriggerCount  uint32      `json:"trigger_count"`
	LastTriggered int64       `json:"last_triggered"`
	CreatedAt     int64       `json:"created_at"`
	UpdatedAt     int64       `json:"updated_at"`
}

// ActionStep is an individual step within an ActionChainSlot.
type ActionStep struct {
	IDHash     uint64  `json:"id_hash"`
	ChainID    uint64  `json:"chain_id"`
	StepOrder  uint16  `json:"step_order"`
	Action     string  `json:"action"`
	Parameters *string `json:"parameters,omitempty"`
	CreatedAt  int64   `json:"created_at"`
}
