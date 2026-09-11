// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Business DTOs of the storage-layer model package: pure request and response
// shapes, no methods and no logic — the bottom layer holds plain structures
// only, so anything crossing a layer boundary is named here once.

package core

// SearchQuery is one scene-scoped read. An empty SceneID asks for a fresh scene;
// a non-empty one must already exist. L3ID optionally anchors a newly created
// scene to a project domain and is read on creation only — an existing scene
// keeps the anchor it has.
type SearchQuery struct {
	SceneID string `json:"scene_id,omitempty"`
	L3ID    string `json:"l3_id,omitempty"`
}

// SearchResult carries the L0 profile plus the read surface of one scene: the
// scene record, its depth-1 topics, and NewTopicID — the topic this read opened.
type SearchResult struct {
	Profile      ProfileSlot `json:"profile"`
	ProfileBrief string      `json:"profile_brief"`
	Scene        SceneSlot   `json:"scene"`
	Topics       []TopicSlot `json:"topics"`
	NewTopicID   uint64      `json:"new_topic_id"`
}

// SceneMessage is one L4 utterance inside a scene context topic. Type says
// whether the content is prose or a reference to media. Seq is the slot the
// utterance holds in its topic, and it is what makes a gap visible: Seq skipping
// a value means that utterance was reclaimed, which is a legal end state for a
// turn, not a read that lost a line.
type SceneMessage struct {
	Role      uint8       `json:"role"`
	Type      ContentType `json:"type"`
	Content   string      `json:"content"`
	Seq       uint64      `json:"seq"`
	CreatedAt int64       `json:"created_at"`
}

// SceneContextTopic is one topic of a scene context with its L4 messages and
// its child count. Depth tells a fused parent (1) from a sunk turn (2).
// Name is a caller-supplied label, empty until one is set.
type SceneContextTopic struct {
	TopicID    string         `json:"topic_id"`
	Depth      int            `json:"depth"`
	Name       string         `json:"name,omitempty"`
	Keywords   []string       `json:"keywords"`
	Messages   []SceneMessage `json:"messages,omitempty"`
	ChildCount int            `json:"child_count"`
}

// SceneContext is a scene's whole transcript, flattened to depth 2 on purpose:
// a Dream-fused group keeps its originals on the child topics it sunk, and this
// is the only read that brings them back. Search returns depth-1 topics only,
// so it shows a fused group as its summary.
type SceneContext struct {
	SceneName string              `json:"scene_name"`
	Topics    []SceneContextTopic `json:"topics"`
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

// L3Relation is one import-time hyperedge: the member nodes of a single
// relation, named by title inside the same graph. Titles lists the far side,
// so the edge spans {item.Title} ∪ Titles — one entry is an ordinary binary
// relation, several entries are one N-ary fact ("these belong together") that
// stays a single edge instead of dissolving into pairs. Targets may appear
// later in the same batch. Empty kind means related.
type L3Relation struct {
	Titles []string      `json:"titles"`
	Kind   GraphEdgeKind `json:"kind,omitempty"`
}

// L3ImportResult reports one import batch. CreatedIDs/UpdatedIDs are node ids;
// GraphIDs are the graphs the batch resolved its domains into (created or reused,
// and including one it added nothing to) — a graph id derives from its domain
// label, and this is where a batch reports it.
type L3ImportResult struct {
	GraphIDs     []string `json:"graph_ids,omitempty"`
	CreatedIDs   []string `json:"created_ids"`
	UpdatedIDs   []string `json:"updated_ids"`
	SkippedCount int      `json:"skipped_count"`
	EdgesCreated int      `json:"edges_created,omitempty"`
	Errors       []string `json:"errors,omitempty"`
}

// L3NodeQuery is a node query over one graph: GraphID is required and every
// other condition that is set filters, so IDs/Keyword/NodeType AND together.
// Keyword matches case-insensitively over title, content and the keyword track.
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

// L4Query archive query: every field is optional and the set conditions AND
// together, so a topic-only or type-only read works. L4 holds both kinds of a
// turn's content, so Kind is a condition like any other — leaving it unset means
// an empty query selects utterances AND events. Results are sorted by Seq.
// Keyword is matched case-insensitively, the same way the L3 node filter matches
// one. An empty query returns the domain's whole archive set — that is a lot of
// text for a caller with a context window, so Limit caps the result to its most
// recent matches. NodeSeq keeps only the records bound to one plan step — that
// step and every step under it; a step is addressed inside a turn, so it means
// nothing without TopicID. Zero leaves the condition unset, which is safe because
// the library hands step ordinals out from 1.
type L4Query struct {
	Keyword string       `json:"keyword,omitempty"`  // case-insensitive substring of Content
	Start   int64        `json:"start,omitempty"`    // created at or after (ms)
	End     int64        `json:"end,omitempty"`      // created at or before (ms)
	IDs     []string     `json:"ids,omitempty"`      // 16 位 hex 档案 ID
	TopicID *string      `json:"topic_id,omitempty"` // only archives of this topic
	Type    *ContentType `json:"type,omitempty"`     // only archives of this content type
	Kind    *ArchiveKind `json:"kind,omitempty"`     // utterance or event; unset selects both
	NodeSeq uint32       `json:"node_seq,omitempty"` // this step and every step under it; needs TopicID
	Limit   int          `json:"limit,omitempty"`    // keep the newest N matches; <=0 means every match
}

// ScenePatch is the partial-update payload of UpdateScene; nil fields are left
// unchanged. An empty L3ID clears the anchor; Force is read only by the
// re-anchor path.
type ScenePatch struct {
	Name  *string
	L3ID  *string
	Force bool
}

// DreamStage is one pipeline phase's outcome inside a DreamReport.
type DreamStage struct {
	Name       string `json:"name"`   // l4_prune/l5_prune/l2_compress/index_rebuild/l1_nodes/l1_hyperedges/l1_rebuild/l1_decay/l0_distill
	Status     string `json:"status"` // ok | skipped | cancelled | error
	DurationMs int64  `json:"duration_ms"`
}

// DreamReport is one consolidation pass's structured result; counts describe
// what this pass actually did. On mid-pipeline failures the partially filled
// report is returned together with the error.
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
