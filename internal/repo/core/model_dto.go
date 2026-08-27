// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Business DTOs of the storage-layer model package: pure request/response
// shapes shared by the composition root (internal), the capability packages
// and the repository layer. No methods, no business logic (G-01: bottom
// layer holds plain structures only).

package core

type SearchQuery struct {
	Text         string  `json:"text"`
	DirectedL2ID *string `json:"directed_l2_id,omitempty"`
	DirectedL3ID *string `json:"directed_l3_id,omitempty"`
	AutoCreate   bool    `json:"auto_create,omitempty"`
	Timestamp    int64   `json:"timestamp"`
}

type SearchResult struct {
	Profile            ProfileSlot `json:"profile"`
	ProfileBrief       string      `json:"profile_brief"`
	Contexts           []TopicSlot `json:"contexts"`
	AssociatedContexts []TopicSlot `json:"associated_contexts"`
	NewTopicID         uint64      `json:"new_topic_id,omitempty"`
}

// SceneMessage is one L4 archive message inside a scene context topic.
type SceneMessage struct {
	Role      uint8  `json:"role"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"`
}

// SceneContextTopic is one depth-1 topic with its L4 messages and child count.
type SceneContextTopic struct {
	TopicID    string         `json:"topic_id"`
	Depth      int            `json:"depth"`
	Keywords   []string       `json:"keywords"`
	L4IDs      []string       `json:"l4_ids,omitempty"` // 话题内的 L4 档案 ID,供按 ID 拉取原文
	Messages   []SceneMessage `json:"messages,omitempty"`
	ChildCount int            `json:"child_count"`
}

// SceneContext is a scene's full depth-1 conversation context.
type SceneContext struct {
	SceneName  string              `json:"scene_name"`
	TopicCount int                 `json:"topic_count"`
	Topics     []SceneContextTopic `json:"topics"`
}

type L3Graph struct {
	Slot  HypergraphSlot
	Nodes []HypergraphNode
	Edges []HypergraphEdge
}

type L3ImportItem struct {
	Title    string   `json:"title"`
	Domain   string   `json:"domain"`
	NodeType string   `json:"node_type"`
	Content  string   `json:"content"`
	Keywords []string `json:"keywords"`
}

type L3ImportResult struct {
	CreatedIDs   []string `json:"created_ids"`
	UpdatedIDs   []string `json:"updated_ids"`
	SkippedCount int      `json:"skipped_count"`
	Errors       []string `json:"errors,omitempty"`
}

// L3NodeQuery is a node query: GraphID required; one of IDs/Keyword/NodeType.
type L3NodeQuery struct {
	GraphID  string   `json:"graph_id"`
	IDs      []string `json:"ids,omitempty"`
	Keyword  string   `json:"keyword,omitempty"`
	NodeType string   `json:"node_type,omitempty"`
	Limit    int      `json:"limit,omitempty"` // <=0 means unlimited
}

type L3Subgraph struct {
	Nodes []HypergraphNode
	Edges []HypergraphEdge
}

// L4Query archive query: the three modes are exclusive with priority
// Keyword > time range > IDs; TopicID filters in all modes.
type L4Query struct {
	Keyword string   `json:"keyword,omitempty"` // mode 1: content substring
	Start   int64    `json:"start,omitempty"`   // mode 2: time range [Start, End] (ms)
	End     int64    `json:"end,omitempty"`
	IDs     []string `json:"ids,omitempty"`      // mode 3: by id
	TopicID *string  `json:"topic_id,omitempty"` // extra: only archives of this topic
}

// CapabilityImport is the memhop-capability/v3 JSON file loaded from a path.
// The resource tool-declaration fields (Name/Desc/Input/Output) mirror the
// host tool spec shape so hosts project capabilities with a pure field copy.
type CapabilityImport struct {
	Format    string         `json:"format"`
	Name      string         `json:"name"`
	Version   string         `json:"version,omitempty"`
	Type      CapabilityType `json:"type"`
	Summary   string         `json:"summary"`
	Trigger   string         `json:"trigger"`
	Resources []ResourceRef  `json:"resources"`
	Workflow  *Workflow      `json:"workflow,omitempty"`
}

// CapabilityListQuery filters L5 capabilities.
type CapabilityListQuery struct {
	Status  *CapabilityStatus `json:"status,omitempty"`
	Type    *CapabilityType   `json:"type,omitempty"`
	Keyword string            `json:"keyword,omitempty"`
}

// CapabilityPatch is the partial-update payload of UpdateCapability; nil
// fields are left unchanged. Name is immutable: the ID derives from it, so
// renaming means delete + import.
type CapabilityPatch struct {
	Version   *string
	Type      *CapabilityType
	Summary   *string
	Trigger   *string
	Status    *CapabilityStatus
	Resources *[]ResourceRef
	Workflow  *Workflow
}

// TrajectoryStats aggregates a session's L6 events for the host's
// "is this session worth crystallizing" decision (event volume, tool-call
// distribution, recency). Read-only; no engine state mutation.
type TrajectoryStats struct {
	Steps        int            `json:"steps"`          // 事件总数
	ToolUsage    map[string]int `json:"tool_usage"`     // EventType → 计数（turn_start/tool_call/...）
	LastAppendAt int64          `json:"last_append_at"` // 最后事件时间戳（Unix 毫秒）
}

// CrystallizeResult reports L5 capabilities created/reused/merged from a
// trajectory. Crystallized capabilities are drafts until the host activates
// them.
type CrystallizeResult struct {
	CreatedIDs []string            `json:"created_ids"`
	ReusedIDs  []string            `json:"reused_ids"`
	MergedIDs  []string            `json:"merged_ids"`
	Errors     []string            `json:"errors,omitempty"`
	Details    []CrystallizeDetail `json:"details,omitempty"` // v1.3: per-candidate disposition
}

// CrystallizeDetail is one candidate's disposition: which capability it
// created/reused/merged, or why it was skipped.
type CrystallizeDetail struct {
	Name         string `json:"name"`                    // 候选能力卡名
	Action       string `json:"action"`                  // create | reuse | merge | skip
	CapabilityID string `json:"capability_id,omitempty"` // 16 位 hex；skip 时为空
	Reason       string `json:"reason,omitempty"`        // skipped_reason（validate 失败原因）
}

// SceneHit is the retrieval result: the winning scene, its aggregated
// score, and the scene's topics ordered by fused relevance.
type SceneHit struct {
	SceneID uint64
	Score   float32
	Topics  []ScoredTopic
}

// ScoredTopic is one topic of the hit scene with its fused relevance s
type ScoredTopic struct {
	Topic TopicSlot
	Score float32
}

// L3ImportMode selects the conflict policy of ImportL3.
type L3ImportMode string

const (
	L3ImportSkip      L3ImportMode = "Skip"
	L3ImportMerge     L3ImportMode = "Merge"
	L3ImportOverwrite L3ImportMode = "Overwrite"
)
