// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Business DTOs of the storage-layer model package: pure request/response
// shapes shared by the composition root (internal), the capability packages
// and the repository layer. No methods, no business logic (G-01: bottom
// layer holds plain structures only).

package core

// SearchQuery is one scene-scoped read. SceneID is the host's session id:
// empty asks the library for a fresh scene, non-empty must already exist.
// L3ID optionally anchors a newly created scene to a project domain and is
// read on creation only — an existing scene keeps its anchor. New scenes are
// named by the library ("session:<id>"), never by the caller; the host
// renames one afterwards with SetSceneName.
type SearchQuery struct {
	SceneID string `json:"scene_id,omitempty"`
	L3ID    string `json:"l3_id,omitempty"`
}

// SearchResult carries the L0 profile plus the read surface of one scene: the
// scene record, its depth-1 topics (the host's context for that session) and
// NewTopicID — the turn topic this read opened. The host runs its turn and
// hands that ID back to Update and to the L6 trajectory writes.
type SearchResult struct {
	Profile      ProfileSlot `json:"profile"`
	ProfileBrief string      `json:"profile_brief"`
	Scene        SceneSlot   `json:"scene"`
	Topics       []TopicSlot `json:"topics"`
	NewTopicID   uint64      `json:"new_topic_id"`
}

// TurnUpdate is one finished turn handed to Update: the topic id Search
// issued, the host's scene id, and both originals with their own timestamps.
// The library distills them into the topic's single keyword track; the
// originals are kept as L4 archives. Update is the only L4 write path, so the
// two content types are how a non-text turn gets recorded: both default to
// ContentText (the zero value) and a non-text slot carries its reference (a
// path or URL) in place of the text.
type TurnUpdate struct {
	SceneID   string      `json:"scene_id"`
	TopicID   string      `json:"topic_id"`
	UserText  string      `json:"user_text"`
	UserTS    int64       `json:"user_ts"`
	UserType  ContentType `json:"user_type,omitempty"`
	AgentText string      `json:"agent_text"`
	AgentTS   int64       `json:"agent_ts"`
	AgentType ContentType `json:"agent_type,omitempty"`
}

// SceneMessage is one L4 archive message inside a scene context topic. Type
// tells the host whether the content is prose or a reference to media.
type SceneMessage struct {
	Role      uint8       `json:"role"`
	Type      ContentType `json:"type"`
	Content   string      `json:"content"`
	CreatedAt int64       `json:"created_at"`
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

// L3ImportItem is one knowledge node of a batch import; SourceRef carries a
// positional reference (file:line / URL) and Related declares same-graph
// hyperedges resolved by title (targets may appear later in the batch).
type L3ImportItem struct {
	Title     string       `json:"title"`
	Domain    string       `json:"domain"`
	NodeType  string       `json:"node_type"`
	Content   string       `json:"content"`
	Keywords  []string     `json:"keywords"`
	SourceRef string       `json:"source_ref,omitempty"`
	Related   []L3Relation `json:"related,omitempty"`
}

// L3Relation is one import-time hyperedge: a target node of the same graph
// (by title) and the edge kind; empty kind means related.
type L3Relation struct {
	Title string        `json:"title"`
	Kind  GraphEdgeKind `json:"kind,omitempty"`
}

type L3ImportResult struct {
	CreatedIDs   []string `json:"created_ids"`
	UpdatedIDs   []string `json:"updated_ids"`
	SkippedCount int      `json:"skipped_count"`
	EdgesCreated int      `json:"edges_created,omitempty"`
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
// Keyword > time range > IDs; TopicID filters in all modes, Type narrows by
// content type.
type L4Query struct {
	Keyword string       `json:"keyword,omitempty"` // mode 1: content substring
	Start   int64        `json:"start,omitempty"`   // mode 2: time range [Start, End] (ms)
	End     int64        `json:"end,omitempty"`
	IDs     []string     `json:"ids,omitempty"`      // mode 3: by id
	TopicID *string      `json:"topic_id,omitempty"` // extra: only archives of this topic
	Type    *ContentType `json:"type,omitempty"`     // extra: only archives of this content type
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

// TrajectorySessionSummary is one L6 turn's footprint (one trajectory per
// agent turn); SessionID is the external 16-hex form so it feeds
// ReadTrajectory / Crystallize directly. Events older than the 7-day
// retention window are dropped by Dream automatically.
type TrajectorySessionSummary struct {
	SessionID    string `json:"session_id"`     // 16 位 hex
	Steps        int    `json:"steps"`          // 事件总数
	LastAppendAt int64  `json:"last_append_at"` // 最近事件时间戳（Unix 毫秒）
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

// DreamStage is one pipeline phase's outcome inside a DreamReport.
type DreamStage struct {
	Name       string `json:"name"`   // l2_compress/index_rebuild/l1_nodes/l1_hyperedges/l1_rebuild/l1_decay/l0_distill
	Status     string `json:"status"` // ok | skipped | cancelled | error
	DurationMs int64  `json:"duration_ms"`
}

// DreamReport is Dream's structured result for host observability; counts
// describe what this pass actually did. On mid-pipeline failures the
// partially filled report is returned together with the error.
type DreamReport struct {
	ConsolidatedScenes int          `json:"consolidated_scenes"` // 场景数（≥1 个合并组生效）
	L2TopicsCompressed int          `json:"l2_topics_compressed"`
	L1NodesAdded       int          `json:"l1_nodes_added"`   // 同步创建/更新的场景节点
	L1EdgesAdded       int          `json:"l1_edges_added"`   // 新建超边
	L1NodesRemoved     int          `json:"l1_nodes_removed"` // 陈旧重建 + 衰减移除
	L1EdgesRemoved     int          `json:"l1_edges_removed"`
	L0Updated          bool         `json:"l0_updated"` // 本轮执行了情感/MBTI 蒸馏并回写
	Stages             []DreamStage `json:"stages,omitempty"`
}

// L3ImportMode selects the conflict policy of ImportL3.
type L3ImportMode string

const (
	L3ImportSkip      L3ImportMode = "Skip"
	L3ImportMerge     L3ImportMode = "Merge"
	L3ImportOverwrite L3ImportMode = "Overwrite"
)
