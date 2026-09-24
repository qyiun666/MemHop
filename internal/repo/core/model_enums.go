// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Enumerations of the data model: the content medium, the archive kind and the
// speaker an L4 record carries, and the relation kind an L3 edge carries, each with
// its string form. Slot structures live in model.go.
package core

import "github.com/qyiun666/MemHop/internal/common"

// ContentType is the medium of an L4 record's Content.
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

// Valid reports whether c is a defined content type, read off the names table
// so adding a type needs no second edit here.
func (c ContentType) Valid() bool {
	_, ok := contentTypeNames[c]
	return ok
}

// ArchiveKind says which of a topic's L4 records a slot is: something somebody
// said, or something that happened while they said it. It is orthogonal to
// ContentType, which names the medium of Content.
type ArchiveKind uint8

const (
	KindUtterance ArchiveKind = 0 // a dialogue original, or a fused summary
	KindEvent     ArchiveKind = 1 // an operation event
)

var archiveKindNames = map[ArchiveKind]string{
	KindUtterance: "utterance", KindEvent: "event",
}

func (k ArchiveKind) String() string { return common.EnumString(k, archiveKindNames, "ArchiveKind") }

// Valid reports whether k is a defined archive kind, read off the names table.
func (k ArchiveKind) Valid() bool {
	_, ok := archiveKindNames[k]
	return ok
}

// ArchiveRole says who spoke an L4 utterance. An event record leaves it 0 — Role
// qualifies an utterance only, and 0 is RoleUser, so on an event the number carries
// no claim. RoleDream is the library's own mark on a fused group's summary.
type ArchiveRole uint8

const (
	RoleUser   ArchiveRole = 0
	RoleAgent  ArchiveRole = 1
	RoleSystem ArchiveRole = 2
	RoleDream  ArchiveRole = 3
)

var archiveRoleNames = map[ArchiveRole]string{
	RoleUser: "user", RoleAgent: "agent", RoleSystem: "system", RoleDream: "dream",
}

func (r ArchiveRole) String() string { return common.EnumString(r, archiveRoleNames, "ArchiveRole") }

// Valid reports whether r is a defined role, read off the names table so adding a
// role needs no second edit here.
func (r ArchiveRole) Valid() bool {
	_, ok := archiveRoleNames[r]
	return ok
}

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

// Valid reports whether k is a defined edge kind, read off the names table.
func (k GraphEdgeKind) Valid() bool {
	_, ok := graphEdgeKindNames[k]
	return ok
}
