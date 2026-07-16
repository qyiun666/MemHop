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
	Scenes          []SceneData    `json:"scenes"`
	RecentDialogues []string       `json:"recent_dialogues"`
	ExistingChains  []ChainSummary `json:"existing_chains"`
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

// ChainSummary is a lightweight summary of an existing L5 ActionChain.
type ChainSummary struct {
	Title        string  `json:"title"`
	Trigger      string  `json:"trigger"`
	TriggerCount uint32  `json:"trigger_count"`
	Confidence   float32 `json:"confidence"`
}

// ============================================================================
// Output structures
// ============================================================================

// ConsolidationOutput holds the LLM response; each section is independently valid.
type ConsolidationOutput struct {
	L2Groups      Section[[]L2Group]     `json:"l2_groups"`
	L3Extractions Section[[]L3Extraction] `json:"l3_extractions"`
	Habits        Section[HabitAnalysis]  `json:"habits"`
	Crystals      Section[[]CrystalDef]   `json:"crystals"`
}

// SectionStatus indicates the parse state of a section.
type SectionStatus uint8

const (
	SectionValid      SectionStatus = iota // successfully parsed
	SectionEmpty                           // no data
	SectionParseFailed                     // LLM returned unparseable content
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

// ============================================================================
// L3 knowledge extraction output
// ============================================================================

// L3Extraction holds extracted concepts and relations for one context.
type L3Extraction struct {
	ContextID uint64        `json:"context_id"`
	Concepts  []LlmConcept  `json:"concepts"`
	Relations []LlmRelation `json:"relations"`
}

// LlmConcept is a concept entity extracted by the LLM.
type LlmConcept struct {
	Name        string   `json:"name"`
	NodeType    string   `json:"type"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
}

// LlmRelation is a semantic relation between concepts.
type LlmRelation struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// ============================================================================
// Habit analysis
// ============================================================================

// HabitAnalysis holds user language habit analysis results.
type HabitAnalysis struct {
	Lexicon          map[string]string `json:"lexicon"`
	StyleTraits      []string          `json:"style_traits"`
	EmotionPatterns  map[string]string `json:"emotion_patterns"`
}

// ============================================================================
// Crystal definitions
// ============================================================================

// CrystalDef is a procedural knowledge rule generated by the LLM.
type CrystalDef struct {
	Condition  string        `json:"condition"`
	Action     string        `json:"action"`
	Steps      []CrystalStep `json:"steps"`
	Confidence float32       `json:"confidence"`
}

// CrystalStep is one step in a crystal's action sequence.
type CrystalStep struct {
	Action     string  `json:"action"`
	Parameters *string `json:"parameters,omitempty"`
}
