// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Import-related DTOs for the MemHop Query layer.

package importx

import (
	"github.com/qyiun666/MemHop/internal/query/write"
)

// ImportRequest is a memory import request.
type ImportRequest struct {
	TargetLayer    write.TargetLayer `json:"target_layer"`
	Data           ImportData        `json:"data"`
	Mode           write.ImportMode  `json:"mode"`
	KnowledgeTitle *string           `json:"knowledge_title,omitempty"`
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
	Status         write.ImportStatus  `json:"status"`
	ID             *string             `json:"id,omitempty"`
	IDs            []string            `json:"ids,omitempty"`
	CreatedIDs     []string            `json:"created_ids"`
	UpdatedIDs     []string            `json:"updated_ids"`
	SkippedCount   int                 `json:"skipped_count"`
	Errors         []write.ImportError `json:"errors"`
	KnowledgeTitle *string             `json:"knowledge_title,omitempty"`
	NodeCount      int                 `json:"node_count"`
}
