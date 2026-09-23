// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Public type surface of the facade. A response shape that has to render a record id is
// a real struct here, with the id coming out as a string while the internal and core
// layers stay on uint64; a shape whose ids are already rendered — or that carries none
// — stays an alias to the internal seam. internal is not published, so the aliases
// below are where a host reads the field rules of what it fills in and of what it gets
// back.

package api

import "github.com/qyiun666/MemHop/internal"

// ---- config ----

type (
	// LlmConfig points the engine at its only external service: there is no
	// embedding service and no dimension to declare. TimeoutSecs is the whole HTTP
	// call's budget in seconds and MaxOutputTokens the cap on one reply; leave
	// either at 0 and the library answers with its own default (120 seconds,
	// 8192 tokens).
	LlmConfig = internal.LlmConfig
	// MemHopDefaults holds the host-facing business knobs (consolidation
	// thresholds, the idle-domain TTL and the content retention window);
	// engine tuning constants are package-private.
	MemHopDefaults = internal.MemHopDefaults
)

// DefaultMemHopDefaults is the shared default configuration. It is a value: pass it
// to Open as-is, or copy it and edit the copy to tune one open.
var DefaultMemHopDefaults = internal.DefaultMemHopDefaults

// ---- input aliases ----
//
// These are the shapes a host fills in.

type (
	// SearchQuery scopes one scene read. Leaving SceneID empty continues the domain's
	// current scene, so a host running one agent over one library fills nothing;
	// NewScene asks for a fresh scene instead, which L3ID may anchor to a project
	// domain.
	SearchQuery = internal.SearchQuery
	// TurnEnd is what Update records to close a turn: the stimulus that opened it,
	// the answer that ended it, and the host's own word for which arm it took.
	// CreatedAt is milliseconds and is the host's to supply.
	TurnEnd = internal.TurnEnd
	// L3ImportItem is one knowledge node of an ImportL3 batch: Title names it
	// inside its graph, Domain says which graph, and SourceRef carries a positional
	// reference (file:line, or a URL). Related declares same-graph hyperedges by
	// title, and a target may appear later in the same batch.
	L3ImportItem = internal.L3ImportItem
	// L3Relation is one import-time hyperedge. The edge spans {item.Title} ∪
	// Titles, so naming several titles stays one N-ary fact instead of dissolving
	// into pairs. An unset Kind means "related".
	L3Relation = internal.L3Relation
	// L3ImportMode is ImportL3's policy for a node the graph already holds. Skip
	// leaves the record alone and counts it in skipped_count. Merge folds the
	// imported values in: an empty imported value keeps the current one, keywords
	// union, and content grows only by what it does not already contain. Overwrite
	// replaces the mutable fields wholesale — and there an empty sourceRef clears
	// the reference, where Merge keeps it.
	L3ImportMode = internal.L3ImportMode
	// L3NodeQuery filters the nodes of one graph. GraphID is required and every
	// other condition that is set narrows the result: IDs, Keyword and NodeType
	// AND together. Keyword matches case-insensitively over title, content and the
	// keyword track; a Limit of 0 or less means no cap.
	L3NodeQuery = internal.L3NodeQuery
	// L4Query reads a turn's content. Dialogue and events live in one layer, so Kind
	// is a condition like any other and leaving it unset asks for both. Every field
	// is optional and the set ones AND. Order is what the query spans: Seq within
	// one topic, creation time across topics, and Limit keeps the tail of whichever
	// order applies. Start and End filter that creation time in milliseconds since
	// the epoch, the unit a stored record's CreatedAt carries. NodeSeq keeps one plan
	// step and its whole subtree, and is refused without TopicID because a step is
	// addressed inside a turn. IDs keeps only the archives it names — the ids an
	// earlier read handed back, which is the only way to fetch one archive again —
	// and is the one condition answered by id rather than by a scan when it is the
	// only one set. Keyword is a case-insensitive substring of the stored content
	// text alone: it searches neither an event's name nor a topic's keyword track. A
	// Kind or Type outside the defined vocabulary is refused rather than answered
	// with an empty set — a filter that can match nothing is indistinguishable from
	// a turn that holds nothing.
	L4Query = internal.L4Query
	// ScenePatch is UpdateScene's partial payload: a nil field is left alone, and
	// an empty Name is refused. An empty L3ID clears the anchor. Force is read only
	// by the re-anchor path, and only when the scene already has one: moving a
	// scene from graph A to graph B loses A, while clearing an anchor is
	// reversible and needs no Force.
	ScenePatch = internal.ScenePatch
	// PlanStatus is a plan step's state: "in_progress" (what a created step is),
	// "done" or "failed". Any other spelling is refused. There is no "leave it as
	// it was": restating a step always gives its status.
	PlanStatus = internal.PlanStatus
	// GraphEdgeKind names an L3 relation: related, causal, part_of, sequence,
	// dependency or custom. The same vocabulary is judged on both ends — the import
	// write and the subgraph filter refuse an undefined value.
	GraphEdgeKind = internal.GraphEdgeKind
	// ContentType is the medium of one L4 record's content: text, image, video,
	// document, audio, code, and 255 ("other") for a medium with no name of its own.
	ContentType = internal.ContentType
	// ArchiveKind says which of a turn's records an L4 slot is (see KindUtterance and
	// KindEvent). It is orthogonal to ContentType, which names the medium of the text.
	ArchiveKind = internal.ArchiveKind
)

// ---- response aliases ----
//
// These carry no id the facade has to render, so they are the internal shape.

type (
	// L3ImportResult reports one ImportL3 batch: the node ids it created and the
	// ones it rewrote, how many nodes it left alone under Skip mode, how many
	// hyperedges it built, and the per-item failures that did not stop the rest of
	// the batch. GraphIDs names every graph the batch resolved a domain into —
	// including one it wrote nothing new into, which is still the graph a host has
	// to hang a scene on.
	L3ImportResult = internal.L3ImportResult
	// DreamReport is one consolidation pass: what it actually did, stage by stage.
	// A pass that stopped partway returns the report filled so far beside the
	// error, so a non-nil report is not a success. ConsolidatedScenes counts scenes
	// where at least one merge group landed. Three counters do not count what their
	// bare names suggest: L2TopicsCompressed is the topics sunk into fused groups
	// (one group over two turns counts 2), L1NodesAdded is the scene nodes the sync
	// wrote — created, or re-stamped because their topic set moved — and
	// L1EdgesAdded is the co-occurrence edges created or strengthened. The two
	// removal counters span both stages that remove: the stale rebuild and the
	// decay, each counting the edges it took with it as well as the nodes.
	DreamReport = internal.DreamReport
	// DreamStage is one stage of that pass. Name is one of l4_prune, l5_prune,
	// l2_compress, index_rebuild, l1_nodes, l1_hyperedges, l1_rebuild, l1_decay or
	// l0_distill; Status is one of ok, skipped, cancelled or error. A stage the pass
	// never reached is absent from the list rather than reported as failed.
	DreamStage = internal.DreamStage
	// SceneContext is one scene's whole transcript: its topics at depth 1 and 2 in
	// user-timestamp order, each with the dialogue originals it owns.
	//
	// Depth 2 is included on purpose. Dream fuses a group of turns into a parent
	// topic and sinks the originals one level under it, and `Search` lists depth 1
	// only — so a fused group reads as its summary there, and this is the one read
	// that still shows what those turns actually said.
	SceneContext = internal.SceneContext
	// SceneContextTopic is one topic of that transcript. Depth says only whether a
	// topic is still at the surface (1) or has been folded away under a fused group
	// (2); it is not a parent-or-child marker, because a later pass folds the fused
	// parent itself and it then sits at depth 2 beside the turns it once owned.
	// ParentID is not reported, so a fused parent and its children come back as one
	// flat list and ChildCount is what identifies a parent: how many topics in that
	// list name this one as theirs. Keywords is the topic's distilled track.
	SceneContextTopic = internal.SceneContextTopic
	// SceneMessage is one line of a topic's dialogue, in the Seq order it was
	// written to. Seq is the slot the line holds in a space its topic's events
	// share, so the gaps in it are the slots this read did not get back — a turn
	// keeps its own numbering and the first two slots belong to the dialogue.
	// Content is the line itself, or a path for a non-text Type. CreatedAt is
	// milliseconds since the epoch.
	SceneMessage = internal.SceneMessage
)

// ---- response DTOs ----

// ProfileSlot is the L0 profile as the library hands it back: the fields a host writes
// plus the ones only the library writes; the internal id hash is not part of it. No
// call takes this type as an argument — a profile is written with ProfileInput.
//
// Personality is the one field with two writers (see UpdateL0): a host seeds it, and
// Dream's distillation replaces it with the summary the model derived from this
// domain's memories, so it reads back as whichever of the two ran last. Name, Role and
// Preferences have the host as their only writer.
//
// The two distilled fields carry the library's own vocabularies. EmotionState's three
// signals each run 0..1: valence 0 = very negative → 0.5 = neutral → 1 = very positive,
// arousal 0 = calm → 1 = highly excited, dominance 0 = submissive → 1 = dominant, so a
// 0 is an extreme reading rather than a missing one. MBTI holds four dimensions in
// [-1,1] (negative = I/N/T/J, positive = E/S/F/P, magnitude = strength) and Type is
// those four read as one word; an axis answered exactly 0 carries no strength and shows
// as X, and a blank Type means no distillation has run on this domain yet.
type ProfileSlot struct {
	Name         string                `json:"name"`
	Role         string                `json:"role"`
	Personality  string                `json:"personality"`
	EmotionState internal.EmotionScore `json:"emotion_state"`
	MBTI         internal.MBTIScore    `json:"mbti"`
	Preferences  map[string]string     `json:"preferences"`
	// AgentType says which kind of agent this domain holds — see AgentTypePrimary.
	// It is stamped when the domain comes to exist, so no host write can move it.
	AgentType   uint8 `json:"agent_type"`
	UpdatedAtMs int64 `json:"updated_at_ms"`
}

// ProfileInput is the profile as a host writes it — the argument to Open, SubAgent
// and UpdateL0. It holds exactly the fields the host owns. Name is required at all
// three entries (a blank one is refused) because it is how the domain is addressed;
// the other three may be left empty.
//
// The library-owned fields are absent rather than ignored. On an inbound record a
// blank EmotionState or a zero UpdatedAtMs cannot be told apart from "leave what is
// there", so a shape carrying them would silently discard whatever the domain already
// holds: Dream evolves EmotionState and MBTI, the library stamps UpdatedAtMs on every
// write, and AgentType follows how the domain came to exist, not a caller's claim.
type ProfileInput struct {
	Name        string            `json:"name"`
	Role        string            `json:"role"`
	Personality string            `json:"personality"`
	Preferences map[string]string `json:"preferences"`
}

// SceneNodeView is one L1 scene node as a host reads it. Every value is Dream's — the
// consolidation pipeline is the only writer. Importance starts at 1.0 when the scene
// gains its first turn and only falls from there. Valence and Arousal use the same 0..1
// scales as ProfileSlot.EmotionState, so a 0 here is an extreme reading, not a missing
// one; EmotionSet is what tells an extreme from never-stamped — false means no Dream
// pass has distilled this node, so those two signals carry no reading at all. TopicIDs
// are the depth-1 and depth-2 topics the last Dream's sync found under the scene — a
// snapshot, not a live listing: a topic created after that pass is not in it, and one
// deleted since still is until the next pass rebuilds the list. EdgeIDs name the
// co-occurrence edges incident on the node; an edge has no read of its own, so those
// ids are useful exactly one way — two nodes sharing one are a pair Dream judged
// related. CreatedAt and UpdatedAt are milliseconds since the epoch.
type SceneNodeView struct {
	ID         string   `json:"id"`
	SceneID    string   `json:"scene_id"`
	TopicIDs   []string `json:"topic_ids"`
	EdgeIDs    []string `json:"edge_ids"`
	Importance float64  `json:"importance"`
	Valence    float64  `json:"valence"`
	Arousal    float64  `json:"arousal"`
	EmotionSet bool     `json:"emotion_set"`
	CreatedAt  int64    `json:"created_at"`
	UpdatedAt  int64    `json:"updated_at"`
}

// SceneSlot is one L2 scene container — a host session. L3ID is its optional
// project-domain anchor.
type SceneSlot struct {
	SceneID   string `json:"scene_id"`
	SceneName string `json:"scene_name"`
	L3ID      string `json:"l3_id,omitempty"`
}

// TopicSlot is one L2 conversation node: a single turn closed by Update, or a
// Dream-fused group of turns. FusedKeywords is its only keyword track. What was said
// is not on the topic: the L4 archives a turn owns are addressed by that topic's id.
// A fused group names no children either; the turns it swallowed carry ParentID
// pointing back here. Name is the host's own label, written by RenameTopic and never
// derived by the engine; empty means nobody has named this topic yet. UserTimestamp is
// the turn's user-message time and AgentTimestamp its reply time (a fused group takes
// its earliest user and latest agent turn), both in milliseconds since the epoch — the
// unit every timestamp in this package carries.
type TopicSlot struct {
	ID             string   `json:"id"`
	SceneID        string   `json:"scene_id"`
	ParentID       *string  `json:"parent_id,omitempty"`
	Depth          uint8    `json:"depth"`
	Name           string   `json:"name,omitempty"`
	FusedKeywords  []string `json:"fused_keywords"`
	UserTimestamp  int64    `json:"user_timestamp"`
	AgentTimestamp int64    `json:"agent_timestamp"`
}

// SearchResult is the read surface of one scene. NewTopicID is the topic this read
// opened for the turn the host is about to run: Update closes that turn into it, and
// the turn's L4 records and L5 plan tree key on it.
type SearchResult struct {
	Profile      ProfileSlot `json:"profile"`
	ProfileBrief string      `json:"profile_brief"`
	Scene        SceneSlot   `json:"scene"`
	Topics       []TopicSlot `json:"topics"`
	NewTopicID   string      `json:"new_topic_id"`
}

// HypergraphSlot is one L3 graph's container metadata. UpdatedAt is the graph's change
// clock: an import that writes a node or an edge here moves it, as does a rename, while
// a batch that changed nothing leaves it where it was. Both timestamps are milliseconds
// since the epoch.
type HypergraphSlot struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// HypergraphNode is a node within an L3 hypergraph. CreatedAt and UpdatedAt are
// milliseconds since the epoch.
type HypergraphNode struct {
	ID        string   `json:"id"`
	GraphID   string   `json:"graph_id"`
	Title     string   `json:"title"`
	NodeType  string   `json:"node_type"`
	Content   string   `json:"content"`
	Keywords  []string `json:"keywords"`
	SourceRef *string  `json:"source_ref,omitempty"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
}

// HypergraphEdge is a hyperedge within an L3 hypergraph: an unordered relation over
// its member nodes, distinguished by Kind (the edge id covers members and kind, so one
// node pair can carry several relations at once). ID identifies the edge for a host
// comparing two reads; no method takes one. An edge is created by ImportL3 and deleted
// only with its graph by DeleteL3 — nothing edits or decays one. CreatedAt is
// milliseconds since the epoch.
type HypergraphEdge struct {
	ID        string        `json:"id"`
	GraphID   string        `json:"graph_id"`
	Kind      GraphEdgeKind `json:"kind"`
	NodeIDs   []string      `json:"node_ids"`
	CreatedAt int64         `json:"created_at"`
}

// L3Graph is the full view of one L3 hypergraph.
type L3Graph struct {
	Slot  HypergraphSlot   `json:"slot"`
	Nodes []HypergraphNode `json:"nodes"`
	Edges []HypergraphEdge `json:"edges"`
}

// L3Subgraph is a BFS subgraph view.
type L3Subgraph struct {
	Nodes []HypergraphNode `json:"nodes"`
	Edges []HypergraphEdge `json:"edges"`
}

// ArchiveSlot is one record of a topic's L4 content: a dialogue original (KindUtterance)
// or an operation event (KindEvent), as read back. TopicID is the topic that owns a
// stored record and Seq the slot it owns there, so (TopicID, Seq) is the address a read
// names. Role is what a host declared when it appended — RoleUser / RoleAgent /
// RoleSystem, and an event leaves it 0. A read can hand back one more value: RoleDream,
// the library's own mark on a fused group's summary — which no write may carry.
// ContentType says whether Content is prose or a reference to media; EventType names an
// event and is the host's own word for it. CreatedAt is milliseconds since the epoch.
type ArchiveSlot struct {
	ID          string      `json:"id"`
	Kind        ArchiveKind `json:"kind"`
	Seq         uint64      `json:"seq"`
	ContentType ContentType `json:"content_type"`
	Role        uint8       `json:"role"`
	TopicID     string      `json:"topic_id"`
	EventType   string      `json:"event_type,omitempty"`
	NodeSeq     uint32      `json:"node_seq,omitempty"`
	CreatedAt   int64       `json:"created_at"`
	Content     string      `json:"content"`
}

// ArchiveInput is what AppendArchive hands in: the eight fields a host actually
// decides about, and no others. Same family as ProfileInput — the library-owned
// fields are absent rather than ignored.
//
// ArchiveSlot carries two fields this layer fills in and a host has no standing to
// claim. Which turn a record belongs to is the library's to remember (Search minted
// it), so an inbound TopicID would be a second claim that either agrees or is
// silently dropped — and a record read from an older turn and appended again would
// land on the open one while appearing to name its origin. ID follows from the pair,
// so it is derived, never taken. A Seq of 0 asks the library for a slot; a named one
// is this turn's slot to rewrite, which is what makes a replay converge.
type ArchiveInput struct {
	Kind        ArchiveKind `json:"kind"`
	Seq         uint64      `json:"seq"`
	ContentType ContentType `json:"content_type"`
	Role        uint8       `json:"role"`
	EventType   string      `json:"event_type,omitempty"`
	NodeSeq     uint32      `json:"node_seq,omitempty"`
	CreatedAt   int64       `json:"created_at"`
	Content     string      `json:"content"`
}

// PlanNodeView is the external plan-tree node; Status is the string form. A step
// is addressed by Seq inside its turn, and ParentSeq is the step it hangs under —
// 0 for a step at the top level. A step whose parent is not in this tree heads its
// own branch all the same and keeps naming that absent step, so the roots of what
// a read returns are "ParentSeq 0 or not listed here", not just the former.
// CreatedAt, FinishedAt and UpdatedAt are milliseconds since the epoch; FinishedAt
// is 0 while the step is still in progress.
type PlanNodeView struct {
	Seq        uint32         `json:"seq"`
	ParentSeq  uint32         `json:"parent_seq"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	Summary    string         `json:"summary"`
	CreatedAt  int64          `json:"created_at"`
	FinishedAt int64          `json:"finished_at"`
	UpdatedAt  int64          `json:"updated_at"`
	Children   []PlanNodeView `json:"children"`
}

// PlanStep is one step restated for PlanNodeUpdate. Seq is the ordinal the
// library handed out when the step was created — a host never invents one — and
// it addresses a step inside the turn named by the call's topicID.
//
// The fields are not symmetric. A blank Title/Summary keeps what the node
// already holds, so restating a step never rewinds its title or erases a folded
// summary. Status has no blank meaning: it is the string surface
// (in_progress / done / failed), every update states it, and an unknown value is
// refused before the node is touched.
type PlanStep struct {
	Seq     uint32     `json:"seq"`
	Title   string     `json:"title"`
	Status  PlanStatus `json:"status"`
	Summary string     `json:"summary"`
}

// PlanTree is the external forest view of one plan: every top-level step is a
// root, and Done/Total count every step of every tree rather than the roots
// alone.
type PlanTree struct {
	Roots      []PlanNodeView `json:"roots"`
	DoneCount  int            `json:"done_count"`
	TotalCount int            `json:"total_count"`
}
