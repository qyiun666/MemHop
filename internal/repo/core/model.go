// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0-L5 data models for the MemHop memory database.
package core

import (
	"cmp"
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
)

// ProfileSlot is the L0 profile singleton of one agent domain. Field ownership
// is not uniform: EmotionState/MBTI are distilled signals, AgentType is stamped
// once when the domain is created, and UpdatedAtMs is written by the library — a
// write that leaves any of them unset keeps the stored value rather than taking
// the caller's zero.
type ProfileSlot struct {
	IDHash       uint64            `json:"id_hash"`
	Name         string            `json:"name"`
	Role         string            `json:"role"`
	Personality  string            `json:"personality"`
	EmotionState EmotionScore      `json:"emotion_state"`
	MBTI         MBTIScore         `json:"mbti"`
	Preferences  map[string]string `json:"preferences"`
	AgentType    uint8             `json:"agent_type"`
	UpdatedAtMs  int64             `json:"updated_at_ms"`
}

// Which kind of agent a domain holds. The primary is the implicit zero domain
// the file is opened on, so a file has exactly one of them and its identity
// needs no scan to find; every registered domain is a sub agent. Zero being the
// primary is what lets a freshly created domain start out correctly stamped
// before its creator says otherwise.
const (
	AgentTypePrimary uint8 = 0
	AgentTypeSub     uint8 = 1
)

// SceneNode is an L1 hypergraph node linking multiple L2 topics.
type SceneNode struct {
	IDHash     uint64   `json:"id_hash"`
	SceneID    uint64   `json:"scene_id"`
	TopicIDs   []uint64 `json:"topic_ids"`
	Importance float32  `json:"importance"`
	Valence    float64  `json:"valence"`
	Arousal    float64  `json:"arousal"`
	CreatedAt  int64    `json:"created_at"`
	UpdatedAt  int64    `json:"updated_at"`
	EdgeIDs    []uint64 `json:"edge_ids"`
}

// SceneEdge is an L1 hyperedge over a set of member nodes, carrying a weight
// that decays over time.
type SceneEdge struct {
	IDHash    uint64        `json:"id_hash"`
	Kind      HyperedgeKind `json:"kind"`
	NodeIDs   []uint64      `json:"node_ids"`
	Weight    float32       `json:"weight"`
	CreatedAt int64         `json:"created_at"`
	// LastDecayAt: last decay time (ms); 0 = never decayed, first decay starts from CreatedAt.
	LastDecayAt int64 `json:"last_decay_at"`
}

// SceneNodeID derives the stable L1 node ID of a scene:
// hash("scene-node:"+hex(sceneID)). The namespace carries no layer number so a
// renumbering never re-keys it. The ID follows from the scene id alone, so
// removing a node needs no index lookup first.
func SceneNodeID(sceneID uint64) uint64 {
	return common.HashID("scene-node:" + common.FormatHash(sceneID))
}

// SceneSlot is an L2 scene container. Its id is assigned outside the engine and
// never derived from SceneName.
type SceneSlot struct {
	SceneID   uint64 `json:"scene_id"`
	SceneName string `json:"scene_name"`
	// TurnSeq counts the turns opened here: hash("turn:"+sceneID:TurnSeq) is a
	// turn's topic id, so turn ids never depend on message timestamps.
	// Absent = 0.
	TurnSeq uint64 `json:"turn_seq,omitempty"`
	L3ID    uint64 `json:"l3_id"` // 场景固定挂靠的目录/项目域 L3 图（N:1）
}

// NewSceneSlot builds a scene record for a caller-supplied scene id and name.
func NewSceneSlot(sceneID uint64, name string) SceneSlot {
	return SceneSlot{
		SceneID:   sceneID,
		SceneName: name,
	}
}

// TopicSlot is one L2 conversation node: a single turn, or a fused group of
// turns. A topic carries exactly one keyword track (FusedKeywords); what was
// said lives in the L4 archives keyed by this topic's own id, which a topic does
// not list.
// Tree: ParentID (nil = depth-1 root) is the only link a record carries — a
// topic's children are found by the readers that look for them, not by a list on
// the parent. Depth 1 is the surface a scene read lists, 2+ is sunk history;
// depth >= 4 is never kept.
type TopicSlot struct {
	ID       uint64  `json:"id"`
	SceneID  uint64  `json:"scene_id"`
	ParentID *uint64 `json:"parent_id,omitempty"`
	Depth    uint8   `json:"depth"`

	// Name is a caller-supplied label for this topic. Nothing here derives into
	// it, so consolidating or rewriting the record goes around the name and never
	// over it. Empty is a state a reader can tell apart from a name — it says
	// nobody has named this topic yet — and omitempty keeps that state off the
	// disk entirely.
	Name string `json:"name,omitempty"`

	FusedKeywords []string `json:"fused_keywords"`

	UserTimestamp  int64 `json:"user_timestamp"`  // turn: user message time; fused: earliest user turn in group
	AgentTimestamp int64 `json:"agent_timestamp"` // turn: agent reply time; fused: latest agent turn in group
}

// CompareTopicOrder orders a scene's topics by the turn they were spoken in,
// breaking a tie on ID so the order is deterministic.
func CompareTopicOrder(a, b TopicSlot) int {
	if a.UserTimestamp != b.UserTimestamp {
		return cmp.Compare(a.UserTimestamp, b.UserTimestamp)
	}
	return cmp.Compare(a.ID, b.ID)
}

// ComputeTopicID derives a topic ID from sceneID and both timestamps.
// Dream-created fused topics use this form for deterministic replay.
func ComputeTopicID(sceneID uint64, userTS, agentTS int64) uint64 {
	return common.HashID(ComputeTopicKey(sceneID, userTS, agentTS))
}

// ComputeTurnTopicID derives a turn topic's ID from the scene's turn counter
// rather than its message timestamps: the counter form is issuable before a
// turn's texts exist. The "turn:" namespace keeps it apart from ComputeTopicID,
// the timestamp form, which addresses a fused group over the same
// (minTS, maxTS) pair.
func ComputeTurnTopicID(sceneID, seq uint64) uint64 {
	return common.HashID(fmt.Sprintf("turn:%d:%d", sceneID, seq))
}

// ComputeTopicKey is the shared timestamp key form of a topic ID.
func ComputeTopicKey(sceneID uint64, userTS, agentTS int64) string {
	return fmt.Sprintf("%d:%d:%d", sceneID, userTS, agentTS)
}

// HypergraphSource represents the origin of an L3 hypergraph.
type HypergraphSource struct {
	Kind      SourceKind `json:"kind"`
	Value     string     `json:"value"`      // path or URL string; empty for Manual
	ContextID uint64     `json:"context_id"` // used when Kind == SourceContext
}

// HypergraphSlot holds L3 hypergraph container metadata.
type HypergraphSlot struct {
	IDHash    uint64           `json:"id_hash"`
	Name      string           `json:"name"`
	Source    HypergraphSource `json:"source"`
	CreatedAt int64            `json:"created_at"`
	UpdatedAt int64            `json:"updated_at"`
}

// HypergraphNode is a node within an L3 hypergraph.
type HypergraphNode struct {
	IDHash    uint64   `json:"id_hash"`
	GraphID   uint64   `json:"graph_id"`
	Title     string   `json:"title"`
	NodeType  string   `json:"node_type"`
	Content   string   `json:"content"`
	Keywords  []string `json:"keywords"`
	SourceRef *string  `json:"source_ref,omitempty"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
}

// HypergraphEdge is a hyperedge within an L3 hypergraph: an unordered relation
// over its member nodes, identified by members plus Kind (see repo.CreateEdgeL3).
// There is no weight: the engine computes none, so an edge carries exactly what
// identifies it — who is related, and how.
type HypergraphEdge struct {
	IDHash    uint64        `json:"id_hash"`
	GraphID   uint64        `json:"graph_id"`
	Kind      GraphEdgeKind `json:"kind"`
	NodeIDs   []uint64      `json:"node_ids"`
	CreatedAt int64         `json:"created_at"`
}

// Message roles in an ArchiveSlot. An utterance carries one of RoleUser /
// RoleAgent / RoleSystem; RoleDream is the library's own stamp on a fused group's
// summary and is never an utterance role. Role qualifies an utterance — an event
// record leaves it 0.
const (
	RoleUser   uint8 = 0
	RoleAgent  uint8 = 1
	RoleSystem uint8 = 2
	RoleDream  uint8 = 3
)

// Utterances hold Seq 1 and 2 of their topic. Auto-allocation starts above these
// two slots: a caller records events while the turn runs and appends the
// originals whenever it chooses, and the dialogue still lands on the slots a
// reader looks for them on. Naming a reserved Seq explicitly writes that slot,
// overwriting whatever kind holds it.
const (
	SeqUser  uint64 = 1
	SeqAgent uint64 = 2
	// LastUtteranceSeq is the highest slot an utterance may occupy; the first
	// event of a topic is one past it.
	LastUtteranceSeq = SeqAgent
)

// ArchiveSlot stores one piece of a topic's content: a dialogue original
// (KindUtterance) or an operation event (KindEvent).
// (TopicID, Seq) addresses it, so re-writing one Seq overwrites in place.
// Role, ContentType and EventType are orthogonal axes, not three names for one
// thing: Role says who spoke (utterances only), ContentType says what Content
// *is* (prose or a reference to media), EventType says what *happened* — events
// only, and named by the caller.
type ArchiveSlot struct {
	IDHash      uint64      `json:"id_hash"`
	Kind        ArchiveKind `json:"kind"`
	Seq         uint64      `json:"seq"`
	ContentType ContentType `json:"content_type"`
	Role        uint8       `json:"role"`
	TopicID     uint64      `json:"topic_id"`
	EventType   string      `json:"event_type,omitempty"`
	NodeSeq     uint32      `json:"node_seq,omitempty"`
	CreatedAt   int64       `json:"created_at"`
	Content     string      `json:"content"`
}

// HashContent derives the id of one topic's content slot:
// hash("content:"+topicID+":"+seq). The id is positional rather than content-derived:
// writing the same (topic, seq) again lands on the same record, which the engine
// re-points instead of keeping a second live copy. That is what lets a replayed
// turn converge without a list of the ids it supersedes.
func HashContent(topicID, seq uint64) uint64 {
	return common.HashID(fmt.Sprintf("content:%d:%d", topicID, seq))
}

// Plan node status. Three states, and a created node starts in the first one:
// there is no "planned but not started" state, so the zero value is the state a
// fresh node is really in. in_progress is the one "this step is being worked on"
// value: a second synonym would offer two words nothing here can tell apart.
// done and failed are the two terminal states.
const (
	StatusInProgress uint8 = 0
	StatusDone       uint8 = 1
	StatusFailed     uint8 = 2
)

// PlanNode is one node of an L5 plan tree, and one node is one record: L5 holds
// nothing but these. A node is addressed by Seq, a per-topic ordinal the library
// hands out (1, 2, 3 …), and ParentSeq names the step it hangs on, 0 being a
// root. TopicID is the turn topic that owns the tree, so naming the turn is all a
// read needs to get its whole tree back.
// UpdatedAt is what a retention sweep reads — every write stamps it, so a plan
// nobody has written to stops being exempt once its last write falls outside the
// window, while one still being worked on keeps its tree mid-task.
type PlanNode struct {
	IDHash     uint64 `json:"id_hash"`
	TopicID    uint64 `json:"topic_id"`
	Seq        uint32 `json:"seq"`        // ordinal inside the topic, never 0
	ParentSeq  uint32 `json:"parent_seq"` // 0 = root
	Status     uint8  `json:"status"`
	Title      string `json:"title,omitempty"`       // empty = the view falls back to Seq
	Summary    string `json:"summary,omitempty"`     // completion abbreviation
	CreatedAt  int64  `json:"created_at"`            // stamped once, when the node is created
	FinishedAt int64  `json:"finished_at,omitempty"` // stamped on a terminal status only
	UpdatedAt  int64  `json:"updated_at"`
}

// HashPlanNode derives a plan node id from the owning topic + step ordinal,
// namespaced under a "plan:" prefix so it never collides with a content id
// (hash("content:"+topic+":"+seq)) or a turn topic (hash("turn:"+scene:seq)).
// The ordinal is an address inside one turn; this is the record key under it.
func HashPlanNode(topicID uint64, seq uint32) uint64 {
	return common.HashID(fmt.Sprintf("plan:%d:%d", topicID, seq))
}
