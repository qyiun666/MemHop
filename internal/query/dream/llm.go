// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package dream implements the memory consolidation (dream) pipeline.
package dream

// LlmProvider is the interface for LLM-based memory consolidation.
type LlmProvider interface {
	Consolidate(input *ConsolidationInput) (*ConsolidationOutput, error)
}

// ============================================================================
// Input structures
// ============================================================================

// ConsolidationInput holds all data sent to the LLM for a dream cycle.
type ConsolidationInput struct {
	Scenes []SceneData `json:"scenes"`
}

// SceneData groups L2 nodes by scene.
type SceneData struct {
	SceneID uint64       `json:"scene_id"`
	Nodes   []L2NodeData `json:"nodes"`
}

// L2NodeData is per-node data sent to the LLM.
type L2NodeData struct {
	IDHash        uint64   `json:"id_hash"`
	CreatedAt     int64    `json:"created_at"`
	Depth         uint8    `json:"depth"`
	UserKeywords  []string `json:"user_keywords"`
	AgentKeywords []string `json:"agent_keywords"`
	FusedKeywords []string `json:"fused_keywords"`
	FusedSummary  *string  `json:"fused_summary,omitempty"`
	ChildrenIDs   []uint64 `json:"children_ids"`
}

// ============================================================================
// Output structures
// ============================================================================

// ConsolidationOutput holds the LLM response.
type ConsolidationOutput struct {
	L2Groups Section[[]L2Group] `json:"l2_groups"`
}

// SectionStatus indicates the parse state of a section.
type SectionStatus uint8

const (
	SectionValid       SectionStatus = iota // successfully parsed
	SectionEmpty                            // no data
	SectionParseFailed                      // LLM returned unparseable content
)

// Section wraps a value with parse status.
type Section[T any] struct {
	Value      T
	Status     SectionStatus
	ParseError string
}

// IsValid returns true if the section is Valid or Empty.
func (s Section[T]) IsValid() bool {
	return s.Status == SectionValid || s.Status == SectionEmpty
}

// NeedsRetry returns true if parsing failed.
func (s Section[T]) NeedsRetry() bool {
	return s.Status == SectionParseFailed
}

// NewValidSection creates a Valid section.
func NewValidSection[T any](v T) Section[T] {
	return Section[T]{Value: v, Status: SectionValid}
}

// NewEmptySection creates an Empty section.
func NewEmptySection[T any]() Section[T] {
	return Section[T]{Status: SectionEmpty}
}

// NewFailedSection creates a ParseFailed section.
func NewFailedSection[T any](errMsg string) Section[T] {
	return Section[T]{Status: SectionParseFailed, ParseError: errMsg}
}

// ============================================================================
// L2 merge output
// ============================================================================

// L2Group defines a group of depth-1 nodes to merge.
type L2Group struct {
	SceneID       uint64   `json:"scene_id"`
	NodeHashes    []uint64 `json:"node_hashes"`
	MergedTitle   string   `json:"merged_title"`
	MergedSummary string   `json:"merged_summary"`
}
