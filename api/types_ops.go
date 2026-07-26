// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// v0.60.0 unified API types: Layer enum, per-domain Op/Result structs.
// Introduced to consolidate previous per-layer methods (GetL2/GetL3/...)
// into a small set of generic entry points (Get/List/Delete + Topic/Knowledge/Crystal).

package memhop

import (
	"github.com/qyiun666/MemHop/internal/query/crud"
)

// ============================================================================
// Layer enum — identifies which of the six cognitive layers to operate on
// ============================================================================

// Layer is the numeric identifier of a MemHop cognitive layer (L0..L5).
type Layer uint8

const (
	LayerProfile   Layer = 0 // L0 — agent profile (single record)
	LayerScene     Layer = 1 // L1 — scene graph (nodes + hyperedges)
	LayerTopic     Layer = 2 // L2 — topic/context
	LayerKnowledge Layer = 3 // L3 — hypergraph knowledge
	LayerArchive   Layer = 4 // L4 — dialogue archive
	LayerCrystal   Layer = 5 // L5 — action chain / crystal
)

// ============================================================================
// Get / List / Delete — union result carriers
// ============================================================================

// GetResult carries the value returned by MemHop.Get. Only one field is
// populated per call according to the Layer passed in.
type GetResult struct {
	Profile    *ProfileSlot    `json:"profile,omitempty"`
	SceneGraph *L1Graph        `json:"scene_graph,omitempty"`
	Topic      *TopicDetail    `json:"topic,omitempty"`
	Knowledge  *L3Detail       `json:"knowledge,omitempty"`
	Archive    *Archive        `json:"archive,omitempty"`
	Crystal    *CrystalSummary `json:"crystal,omitempty"`
}

// ListRequest carries the per-layer query DTO for MemHop.List. Only one field
// is required per call, matching the Layer being listed. When all fields are
// nil the pipeline uses page 1, page size 20 defaults where applicable.
type ListRequest struct {
	Topic     *TopicListQuery     `json:"topic,omitempty"`     // LayerTopic
	Knowledge *KnowledgeListQuery `json:"knowledge,omitempty"` // LayerKnowledge
	Archive   *ArchiveQuery       `json:"archive,omitempty"`   // LayerArchive
	Crystal   *CrystalListQuery   `json:"crystal,omitempty"`   // LayerCrystal
}

// ListResult carries the paginated response from MemHop.List. Only one field
// is populated per call according to the Layer.
type ListResult struct {
	Topics    *TopicListResult     `json:"topics,omitempty"`
	Knowledge *KnowledgeListResult `json:"knowledge,omitempty"`
	Archives  *ArchiveListResult   `json:"archives,omitempty"`
	Crystals  *CrystalListResult   `json:"crystals,omitempty"`
}

// ============================================================================
// TopicOp — L0/L2 miscellaneous operations that don't fit generic CRUD
// ============================================================================

// TopicOpKind identifies which sub-operation of MemHop.Topic to perform.
type TopicOpKind uint8

const (
	TOpSetProfile TopicOpKind = 1 // L0 — overwrite profile with delta
	TOpMerge      TopicOpKind = 2 // L2 — merge secondary topics into primary
	TOpSceneTree  TopicOpKind = 3 // L2 — full scene tree query
)

// TopicOp is the input envelope for MemHop.Topic. Fields required depend on Kind.
type TopicOp struct {
	Kind TopicOpKind

	// TOpSetProfile
	ProfileDelta *ProfileDelta

	// TOpMerge
	PrimaryID string
	MergeIDs  []string

	// TOpSceneTree
	SceneID string
}

// TopicResult is the union response for MemHop.Topic.
type TopicResult struct {
	Merge     *MergeResult     `json:"merge,omitempty"`
	SceneTree *SceneTreeResult `json:"scene_tree,omitempty"`
}

// ============================================================================
// KnowledgeOp — L3 hypergraph operations
// ============================================================================

// KnowledgeOpKind identifies which sub-operation of MemHop.Knowledge to perform.
type KnowledgeOpKind uint8

const (
	KOpCreateGraph       KnowledgeOpKind = 1  // create a new hypergraph slot
	KOpAddNode           KnowledgeOpKind = 2  // add a node to a graph
	KOpAddEdge           KnowledgeOpKind = 3  // add an edge to a graph
	KOpDeleteNode        KnowledgeOpKind = 4  // delete a node by 16-char hex ID
	KOpDeleteEdge        KnowledgeOpKind = 5  // delete an edge by 16-char hex ID
	KOpSearch            KnowledgeOpKind = 6  // unified L3 node search
	KOpGetNodes          KnowledgeOpKind = 7  // batch fetch nodes (by IDs / keyword / type)
	KOpGraphQuery        KnowledgeOpKind = 8  // BFS subgraph extraction
	KOpDSL               KnowledgeOpKind = 9  // DSL query (MATCH / PATH / SUBGRAPH)
	KOpDetectCommunities KnowledgeOpKind = 10 // Louvain community detection
)

// KnowledgeOp is the input envelope for MemHop.Knowledge. Fields required
// depend on Kind.
type KnowledgeOp struct {
	Kind KnowledgeOpKind

	// KOpCreateGraph
	Name string

	// KOpAddNode / KOpAddEdge: GraphID (for reference), Node/Edge (payload)
	GraphID string
	Node    *HypergraphNode
	Edge    *HypergraphEdge

	// KOpDeleteNode / KOpDeleteEdge: 16-char hex IDs, matching the ID
	// format returned by every other API surface.
	NodeID string
	EdgeID string

	// KOpSearch
	SearchQuery *L3SearchQuery

	// KOpGetNodes
	NodesQuery *crud.KnowledgeNodeQuery

	// KOpGraphQuery
	StartNode string
	MaxDepth  int
	EdgeKinds []string

	// KOpDSL
	DSLString string

	// KOpDetectCommunities
	CommunityCfg *CommunityConfig
}

// KnowledgeResult is the union response for MemHop.Knowledge.
type KnowledgeResult struct {
	Slot      *HypergraphSlot       `json:"slot,omitempty"`      // KOpCreateGraph
	Search    *L3SearchResult       `json:"search,omitempty"`    // KOpSearch
	Nodes     *KnowledgeNodesResult `json:"nodes,omitempty"`     // KOpGetNodes
	Subgraph  *Subgraph             `json:"subgraph,omitempty"`  // KOpGraphQuery
	DSL       *DSLQueryResult       `json:"dsl,omitempty"`       // KOpDSL
	Community *CommunityResult      `json:"community,omitempty"` // KOpDetectCommunities
}

// ============================================================================
// CrystalOp — L5 action chain operations
// ============================================================================

// CrystalOpKind identifies which sub-operation of MemHop.Crystal to perform.
type CrystalOpKind uint8

const (
	COpCreateChain      CrystalOpKind = 1 // create a new action chain
	COpAppendStep       CrystalOpKind = 2 // append a step to an existing chain
	COpUpdateConfidence CrystalOpKind = 3 // EMA update chain confidence
	COpIncrTrigger      CrystalOpKind = 4 // increment trigger counter
	COpBatchDelete      CrystalOpKind = 5 // batch delete crystals
	COpBatchUpdate      CrystalOpKind = 6 // batch update chain fields
)

// CrystalOp is the input envelope for MemHop.Crystal. Fields required depend
// on Kind.
type CrystalOp struct {
	Kind CrystalOpKind

	// COpAppendStep / COpUpdateConfidence / COpIncrTrigger
	ChainID string

	// COpCreateChain
	ChainInput *L5ChainInput

	// COpAppendStep
	StepInput *L5StepInput

	// COpUpdateConfidence
	Success bool

	// COpBatchDelete
	IDs []string

	// COpBatchUpdate
	Updates []L5ChainUpdate
}

// CrystalResult is the union response for MemHop.Crystal.
type CrystalResult struct {
	ChainID string `json:"chain_id,omitempty"` // COpCreateChain
	StepID  string `json:"step_id,omitempty"`  // COpAppendStep
}
