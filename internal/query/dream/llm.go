// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package dream implements the memory consolidation (dream) pipeline.
package dream

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// LlmProvider is the interface for LLM-based memory consolidation.
// Implementations must honor ctx cancellation and deadlines.
type LlmProvider interface {
	Consolidate(ctx context.Context, input *ConsolidationInput) (*ConsolidationOutput, error)
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

// UnmarshalJSON accepts scene_id and node_hashes as either JSON numbers or
// strings. LLMs often quote 20-digit uint64 hashes because they exceed the
// JSON safe-integer range (2^53).
func (g *L2Group) UnmarshalJSON(data []byte) error {
	var raw struct {
		SceneID       json.RawMessage   `json:"scene_id"`
		NodeHashes    []json.RawMessage `json:"node_hashes"`
		MergedTitle   string            `json:"merged_title"`
		MergedSummary string            `json:"merged_summary"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	sceneID, err := parseFlexUint64(raw.SceneID)
	if err != nil {
		return fmt.Errorf("l2group scene_id: %w", err)
	}
	hashes := make([]uint64, 0, len(raw.NodeHashes))
	for _, h := range raw.NodeHashes {
		v, err := parseFlexUint64(h)
		if err != nil {
			return fmt.Errorf("l2group node_hashes: %w", err)
		}
		hashes = append(hashes, v)
	}
	g.SceneID = sceneID
	g.NodeHashes = hashes
	g.MergedTitle = raw.MergedTitle
	g.MergedSummary = raw.MergedSummary
	return nil
}

// parseFlexUint64 parses a JSON number or quoted string into uint64.
// Accepts decimal first, then hex (with or without 0x prefix), since LLMs
// echo IDs in whichever form they appeared in the input.
func parseFlexUint64(raw json.RawMessage) (uint64, error) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, fmt.Errorf("empty uint64 value")
	}
	if v, err := strconv.ParseUint(s, 10, 64); err == nil {
		return v, nil
	}
	return strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
}
