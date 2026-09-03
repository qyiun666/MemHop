// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Enumerations of the L0-L6 data model: content/capability/edge/layer
// tags with their string forms. Slot structures live in model.go.
package core

import "github.com/qyiun666/MemHop/internal/common"

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

// Valid reports whether c is one of the defined content types. The names table
// is the single source of truth, so adding a type needs no second edit here.
func (c ContentType) Valid() bool {
	_, ok := contentTypeNames[c]
	return ok
}

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
	CapabilityMCP   CapabilityType = "mcp"
	CapabilitySkill CapabilityType = "skill"
	// CapabilityAPI wraps one method of the MemHop Go API (api package);
	// the host calls it directly through the library facade.
	CapabilityAPI       CapabilityType = "api"
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
