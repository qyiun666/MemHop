// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Business DTOs of the storage-layer model package: pure request and response
// shapes, no methods and no logic — the bottom layer holds plain structures
// only, so anything crossing a layer boundary is named here once.

package core

// SearchQuery is one scene-scoped read. An empty SceneID continues the domain's
// current scene — the one whose turn counter ran furthest, restored at the first
// read after an open or a sweep — so a host running one agent over one library
// names no scene at all. NewScene asks for a fresh one instead. L3ID anchors a
// scene to an L3 project domain and only a read that creates a scene takes it:
// handed in along with a scene this read continues, named or not, it is refused
// rather than ignored, because a dropped anchor makes an anchoring attempt look
// like it worked. UpdateScene moves the anchor of a scene that exists.
type SearchQuery struct {
	SceneID  string `json:"scene_id,omitempty"`
	L3ID     string `json:"l3_id,omitempty"`
	NewScene bool   `json:"new_scene,omitempty"`
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

// TurnEnd is what one turn leaves behind when its host closes it. Input and Output
// are the turn's dialogue originals and land on Seq 1 and Seq 2; Outcome is the
// host's own word for the arm that ended the turn (a decision-loop kernel's status
// name), which the engine stores verbatim and never branches on — the same posture
// an event's EventType has. CreatedAt is milliseconds, like every other timestamp on
// this surface.
type TurnEnd struct {
	Input     string
	Output    string
	Outcome   string
	CreatedAt int64
}

// SceneMessage is one L4 utterance inside a scene context topic. Type says
// prose or a reference to media. Seq is the slot the utterance holds in its
// topic, shared with the topic's events: a gap says the slot is empty — a
// reclaimed utterance is one such reason, a legal end state for a turn, not a
// read that lost a line.
type SceneMessage struct {
	Role      uint8       `json:"role"`
	Type      ContentType `json:"type"`
	Content   string      `json:"content"`
	Seq       uint64      `json:"seq"`
	CreatedAt int64       `json:"created_at"`
}

// SceneContextTopic is one topic of a scene context with its L4 messages and
// its child count. Depth says whether the topic is still on the scene's surface
// (1) or a Dream group has swallowed it (2) — and a fused summary is itself a
// topic, so a group folded into a later one sits at 2 level with the turns it
// summarizes. Name is a caller-supplied label, empty until one is set.
//
// The two timestamps are the row's own bounds and the only date a reader gets for a
// row whose messages have aged out: content sweeps, topic rows do not. A turn carries
// its stimulus and reply times; a fused group carries the earliest and latest of the
// turns it swallowed.
type SceneContextTopic struct {
	TopicID        string         `json:"topic_id"`
	Depth          int            `json:"depth"`
	Name           string         `json:"name,omitempty"`
	Keywords       []string       `json:"keywords"`
	Messages       []SceneMessage `json:"messages,omitempty"`
	ChildCount     int            `json:"child_count"`
	UserTimestamp  int64          `json:"user_timestamp"`
	AgentTimestamp int64          `json:"agent_timestamp"`
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
	GraphIDs     []string `json:"graph_ids"`
	CreatedIDs   []string `json:"created_ids"`
	UpdatedIDs   []string `json:"updated_ids"`
	SkippedCount int      `json:"skipped_count"`
	EdgesCreated int      `json:"edges_created,omitempty"`
	Errors       []string `json:"errors"`
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
// together, so an empty query selects the domain's whole content set — utterances
// AND events alike; Kind is a condition like any other, not a mode switch. The
// order is what a query spans: Seq within one topic, CreatedAt across topics
// (id breaking ties), and Limit keeps the tail of whichever order applies.
// Keyword matches case-insensitively. NodeSeq means a step and its whole
// subtree — an ordinal inside a turn, so it needs TopicID; zero leaves the
// condition unset, which is safe because step ordinals start at 1.
type L4Query struct {
	Keyword string       `json:"keyword,omitempty"`  // case-insensitive substring of Content
	Start   int64        `json:"start,omitempty"`    // created at or after (ms); 0 leaves the bound unset, a wrong scale is refused
	End     int64        `json:"end,omitempty"`      // created at or before (ms); same ruler as a write's CreatedAt
	IDs     []string     `json:"ids,omitempty"`      // only these archive ids, 16-char hex
	TopicID *string      `json:"topic_id,omitempty"` // only archives of this topic
	Type    *ContentType `json:"type,omitempty"`     // only archives of this content type
	Kind    *ArchiveKind `json:"kind,omitempty"`     // utterance or event; unset selects both
	NodeSeq uint32       `json:"node_seq,omitempty"` // this step and every step under it; needs TopicID
	Limit   int          `json:"limit,omitempty"`    // keep the tail of the read's order; <=0 means every match
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
	ConsolidatedScenes int          `json:"consolidated_scenes"`  // scenes with >=1 applied merge group
	L2TopicsCompressed int          `json:"l2_topics_compressed"` // topics sunk into groups, not group count
	L1NodesAdded       int          `json:"l1_nodes_added"`       // scene nodes created or updated by the sync
	L1EdgesAdded       int          `json:"l1_edges_added"`       // hyperedges created or strengthened
	L1NodesRemoved     int          `json:"l1_nodes_removed"`     // stale rebuild + decay removals
	L1EdgesRemoved     int          `json:"l1_edges_removed"`     // edges taken by stale rebuild and decay
	L0Updated          bool         `json:"l0_updated"`           // emotion/MBTI distillation ran and wrote back
	Stages             []DreamStage `json:"stages,omitempty"`
}

// L3ImportMode selects the conflict policy of ImportL3.
type L3ImportMode string

const (
	L3ImportSkip      L3ImportMode = "Skip"
	L3ImportMerge     L3ImportMode = "Merge"
	L3ImportOverwrite L3ImportMode = "Overwrite"
)

// Valid reports whether m is one of the three defined modes. The vocabulary lives
// here so a caller asks the type instead of listing the values again; a host may
// send any string, and an unnamed mode has no policy to run.
func (m L3ImportMode) Valid() bool {
	switch m {
	case L3ImportSkip, L3ImportMerge, L3ImportOverwrite:
		return true
	}
	return false
}
