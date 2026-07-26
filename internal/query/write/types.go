// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Write-related DTOs for the MemHop Query layer.

package write

import (
	"encoding/json"

	"github.com/qyiun666/MemHop/internal/core/index"
	"github.com/qyiun666/MemHop/internal/core/storage"
	"github.com/qyiun666/MemHop/internal/query/encoder"
)

// TargetLayer identifies which layer to import into.
type TargetLayer string

const (
	TargetProfile   TargetLayer = "Profile"
	TargetTopic     TargetLayer = "Topic"
	TargetKnowledge TargetLayer = "Knowledge"
)

// MarshalJSON implements custom JSON encoding for TargetLayer.
func (t TargetLayer) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(t))
}

// UnmarshalJSON implements custom JSON decoding for TargetLayer.
func (t *TargetLayer) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*t = TargetLayer(v)
	return nil
}

// ActionItem is an L5 action chain item.
type ActionItem struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	ActionType  ActionType        `json:"action_type"`
	Parameters  map[string]string `json:"parameters,omitempty"`
}

// ActionType is the kind of action.
type ActionType string

const (
	ActionCreate  ActionType = "Create"
	ActionRead    ActionType = "Read"
	ActionUpdate  ActionType = "Update"
	ActionDelete  ActionType = "Delete"
	ActionExecute ActionType = "Execute"
	ActionQuery   ActionType = "Query"
	ActionCustom  ActionType = "Custom"
)

// MarshalJSON implements custom JSON encoding for ActionType.
func (a ActionType) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(a))
}

// UnmarshalJSON implements custom JSON decoding for ActionType.
func (a *ActionType) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*a = ActionType(v)
	return nil
}

// ---------------------------------------------------------------------------
// Store types (batch write pipeline)
// ---------------------------------------------------------------------------

// StoreBatch is a batch store request.
type StoreBatch struct {
	Items      []StoreItem `json:"items"`
	SourceInfo *string     `json:"source_info,omitempty"`
	ImportMode *ImportMode `json:"import_mode,omitempty"`
}

// StoreItem is a single item in a batch store operation.
type StoreItem struct {
	Content string `json:"content"`
	// Keywords are REQUIRED: pre-extracted facts/terms used for indexing
	// and encoding. BatchStore fails with an error when empty (no silent
	// keyword extraction fallback).
	Keywords   []string `json:"keywords"`
	Source     string   `json:"source"`
	SourceType string   `json:"source_type"`
	Score      float64  `json:"score"`
	TopicLabel *string  `json:"topic_label,omitempty"`
}

// StoreItemStatus is the per-item outcome of a batch store.
type StoreItemStatus struct {
	ID    string `json:"id"`    // resulting L1 node ID (16-char hex); the existing node's ID when deduplicated
	Dedup bool   `json:"dedup"` // true when the item was skipped as a duplicate of an existing node
}

// StoreResult is the response to a batch store.
type StoreResult struct {
	StoredCount uint32            `json:"stored_count"`
	ItemIDs     []string          `json:"item_ids"` // resulting node ID per input item (same order)
	Items       []StoreItemStatus `json:"items"`    // per-item status, same order as the input
}

// BatchDeps holds all dependencies injected into the batch store pipeline.
type BatchDeps struct {
	Engine      *storage.StorageEngine
	SparseIndex *index.SparseIndex
	L2Meta      *index.L2MetaIndex
	VectorDim   int
	Encoder     encoder.Encoder
}

// BatchReport is the internal result from the batch store pipeline.
type BatchReport struct {
	L4Docs          uint32 `json:"l4_docs"`
	L1NodesCreated  uint32 `json:"l1_nodes_created"`
	L1NodesUpdated  uint32 `json:"l1_nodes_updated"`
	L2TopicsUpdated uint32 `json:"l2_topics_updated"`
	EdgesCreated    uint32 `json:"edges_created"`
	DedupSkipped    uint32 `json:"dedup_skipped"`
	// Items holds the per-item outcome of Phase 3, same order as the input.
	Items []ItemOutcome `json:"items"`
}

// ItemOutcome reports the per-item result of the L1 write/dedup phase.
type ItemOutcome struct {
	NodeID uint64 `json:"node_id"` // resulting L1 node ID (dedup target when skipped)
	Dedup  bool   `json:"dedup"`   // true when the item was skipped as a duplicate
}

// ImportMode controls how existing data is handled during import.
type ImportMode string

const (
	ImportMerge     ImportMode = "Merge"
	ImportOverwrite ImportMode = "Overwrite"
	ImportSkip      ImportMode = "Skip"
)

// MarshalJSON implements custom JSON encoding for ImportMode.
func (m ImportMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(m))
}

// UnmarshalJSON implements custom JSON decoding for ImportMode.
func (m *ImportMode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*m = ImportMode(s)
	return nil
}

// ImportStatus indicates the overall import outcome.
type ImportStatus string

const (
	ImportSuccess        ImportStatus = "Success"
	ImportPartialSuccess ImportStatus = "PartialSuccess"
	ImportFailed         ImportStatus = "Failed"
)

// MarshalJSON implements custom JSON encoding for ImportStatus.
func (s ImportStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(s))
}

// UnmarshalJSON implements custom JSON decoding for ImportStatus.
func (s *ImportStatus) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*s = ImportStatus(v)
	return nil
}

// ImportError describes a single item failure during import.
type ImportError struct {
	Index   int    `json:"index"`
	Message string `json:"message"`
}
