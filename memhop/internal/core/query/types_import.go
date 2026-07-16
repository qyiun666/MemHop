// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Import-related DTOs for the MemHop Query layer.

package query

import "encoding/json"

// ImportRequest is a memory import request.
type ImportRequest struct {
	TargetLayer    TargetLayer `json:"target_layer"`
	Data           ImportData  `json:"data"`
	Mode           ImportMode  `json:"mode"`
	KnowledgeTitle *string     `json:"knowledge_title,omitempty"`
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

// ImportData is the polymorphic import payload.
type ImportData struct {
	Profile   *ProfileImportData    `json:"profile,omitempty"`
	Topics    []TopicImportItem     `json:"topics,omitempty"`
	Knowledge []KnowledgeImportItem `json:"knowledge,omitempty"`
}

// ProfileImportData holds L0 profile import fields.
type ProfileImportData struct {
	Name            *string           `json:"name,omitempty"`
	Role            *string           `json:"role,omitempty"`
	Personality     *string           `json:"personality,omitempty"`
	Worldview       *string           `json:"worldview,omitempty"`
	Preferences     map[string]string `json:"preferences,omitempty"`
	Lexicon         map[string]string `json:"lexicon,omitempty"`
	StyleTraits     []string          `json:"style_traits,omitempty"`
	EmotionPatterns map[string]string `json:"emotion_patterns,omitempty"`
}

// TopicImportItem is a single topic to import.
type TopicImportItem struct {
	Title           string   `json:"title"`
	Summary         *string  `json:"summary,omitempty"`
	Keywords        []string `json:"keywords"`
	KnowledgeDomain *string  `json:"knowledge_domain,omitempty"`
}

// KnowledgeImportItem is a single knowledge node to import.
type KnowledgeImportItem struct {
	Title         string   `json:"title"`
	Domain        string   `json:"domain"`
	KnowledgeType string   `json:"knowledge_type"`
	Text          string   `json:"text"`
	Summary       *string  `json:"summary,omitempty"`
	Keywords      []string `json:"keywords"`
	SourceRef     *string  `json:"source_ref,omitempty"`
}

// ImportResult is the response to an import request.
type ImportResult struct {
	Status         ImportStatus  `json:"status"`
	ID             *string       `json:"id,omitempty"`
	IDs            []string      `json:"ids,omitempty"`
	CreatedIDs     []string      `json:"created_ids"`
	UpdatedIDs     []string      `json:"updated_ids"`
	SkippedCount   int           `json:"skipped_count"`
	Errors         []ImportError `json:"errors"`
	KnowledgeTitle *string       `json:"knowledge_title,omitempty"`
	NodeCount      int           `json:"node_count"`
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

// StoreBatch is a batch store request.
type StoreBatch struct {
	Items      []StoreItem `json:"items"`
	SourceInfo *string     `json:"source_info,omitempty"`
	ImportMode *ImportMode `json:"import_mode,omitempty"`
}

// StoreItem is a single item in a batch store operation.
type StoreItem struct {
	Content    string   `json:"content"`
	Keywords   []string `json:"keywords"`
	Source     string   `json:"source"`
	SourceType string   `json:"source_type"`
	Score      float64  `json:"score"`
	TopicLabel *string  `json:"topic_label,omitempty"`
	// TODO: Layer uint8 removed — per-item layer routing requires significant
	// pipeline restructuring; currently all items follow the fixed L1+L2+L4 path.
}

// StoreResult is the response to a batch store.
type StoreResult struct {
	StoredCount uint32   `json:"stored_count"`
	ItemIDs     []string `json:"item_ids"`
}
