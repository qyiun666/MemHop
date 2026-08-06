// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package model defines L0-L5 data models for the MemHop memory database.
// All struct field JSON tags match the Rust serde output exactly.
package model

import (
	"encoding/json"
	"fmt"
)

// ============================================================================
// ContentType — L4 archive content type (archive.rs)
// ============================================================================

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

func (c ContentType) String() string {
	if s, ok := contentTypeNames[c]; ok {
		return s
	}
	return fmt.Sprintf("ContentType(%d)", c)
}

func (c ContentType) MarshalJSON() ([]byte, error) {
	return json.Marshal(uint8(c))
}

func (c *ContentType) UnmarshalJSON(data []byte) error {
	var v uint8
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*c = ContentType(v)
	return nil
}

// ============================================================================
// ChainStatus — L5 action chain status (action_chain.rs)
// ============================================================================

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

func (c ChainStatus) String() string {
	if s, ok := chainStatusNames[c]; ok {
		return s
	}
	return fmt.Sprintf("ChainStatus(%d)", c)
}

func (c ChainStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(uint8(c))
}

func (c *ChainStatus) UnmarshalJSON(data []byte) error {
	var v uint8
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*c = ChainStatus(v)
	return nil
}

// ============================================================================
// HyperedgeKind — L1 hyperedge type (hyperedge.rs)
// ============================================================================

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
	if s, ok := hyperedgeKindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("HyperedgeKind(%d)", k)
}

func (k HyperedgeKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(uint8(k))
}

func (k *HyperedgeKind) UnmarshalJSON(data []byte) error {
	var v uint8
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*k = HyperedgeKind(v)
	return nil
}

// ============================================================================
// SourceKind — L3 hypergraph source type (hypergraph.rs)
// ============================================================================

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

func (s SourceKind) String() string {
	if n, ok := sourceKindNames[s]; ok {
		return n
	}
	return fmt.Sprintf("SourceKind(%d)", s)
}

func (s SourceKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(uint8(s))
}

func (s *SourceKind) UnmarshalJSON(data []byte) error {
	var v uint8
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*s = SourceKind(v)
	return nil
}

// ============================================================================
// GraphEdgeKind — L3 hypergraph edge type (hypergraph.rs)
// ============================================================================

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
	if s, ok := graphEdgeKindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("GraphEdgeKind(%d)", k)
}

func (k GraphEdgeKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(uint8(k))
}

func (k *GraphEdgeKind) UnmarshalJSON(data []byte) error {
	var v uint8
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*k = GraphEdgeKind(v)
	return nil
}

// ============================================================================
// Layer — memory layer identifier
// ============================================================================

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

func (l Layer) String() string {
	if s, ok := layerNames[l]; ok {
		return s
	}
	return fmt.Sprintf("Layer(%d)", l)
}

func (l Layer) MarshalJSON() ([]byte, error) {
	return json.Marshal(uint8(l))
}

func (l *Layer) UnmarshalJSON(data []byte) error {
	var v uint8
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*l = Layer(v)
	return nil
}
