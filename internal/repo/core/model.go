// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0-L6 data models for the MemHop memory database.
package core

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
)

// ProfileSlot is the L0 profile singleton of one agent domain. Ownership:
// Name/Role/Preferences are host-authored and never touched by Dream;
// Personality is seeded by the host and evolved by Dream distillation;
// EmotionState/MBTI are distilled signals.
type ProfileSlot struct {
	IDHash       uint64            `json:"id_hash"`
	Name         string            `json:"name"`
	Role         string            `json:"role"`
	Personality  string            `json:"personality"`
	EmotionState EmotionScore      `json:"emotion_state"`
	MBTI         MBTIScore         `json:"mbti"`
	Preferences  map[string]string `json:"preferences"`
	UpdatedAtMs  int64             `json:"updated_at_ms"`
}

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
	// LastDecayAt: last decay time (ms); 0 = never decayed, first decay starts from CreatedAt.
	LastDecayAt int64 `json:"last_decay_at"`
}

// SceneEdge is an L1 hyperedge used by upper-layer decay logic.
type SceneEdge struct {
	IDHash    uint64        `json:"id_hash"`
	Kind      HyperedgeKind `json:"kind"`
	NodeIDs   []uint64      `json:"node_ids"`
	Weight    float32       `json:"weight"`
	CreatedAt int64         `json:"created_at"`
	// LastDecayAt: last decay time (ms); 0 = never decayed, first decay starts from CreatedAt.
	LastDecayAt int64 `json:"last_decay_at"`
}

// SceneNodeID derives the stable L1 node ID of a scene: hash("l1:"+hex(sceneID)).
// The node is created/updated only during Dream, but the ID is computable
// without any index — which is what lets DeleteScene drop it right away.
func SceneNodeID(sceneID uint64) uint64 {
	return common.HashID("l1:" + common.FormatHash(sceneID))
}

// SceneSlot is an L2 scene container — one host session's conversation. The
// scene ID is the host's session id, never a hash of its name. HitCount /
// LastHitAt fold the former L6 scene-usage feedback into the scene record
// (read-side statistics consumed by Dream's usage feedback).
type SceneSlot struct {
	SceneID    uint64 `json:"scene_id"`
	SceneName  string `json:"scene_name"`
	TopicCount int    `json:"topic_count"` // depth-1 root topics under this scene
	HitCount   uint32 `json:"hit_count"`   // times the scene was read by Search
	LastHitAt  int64  `json:"last_hit_at"` // last Search read time (Unix ms)
	// TurnSeq counts turns Search has opened here: each read bumps it and
	// returns hash("turn:"+sceneID:TurnSeq) as the topic id Update settles
	// into, so turn ids never depend on message timestamps. Absent = 0.
	TurnSeq uint64 `json:"turn_seq,omitempty"`
	L3ID    uint64 `json:"l3_id"` // 场景固定挂靠的目录/项目域 L3 图（N:1）
}

// NewSceneSlot builds a scene record for a host-owned scene ID.
func NewSceneSlot(sceneID uint64, name string) SceneSlot {
	return SceneSlot{
		SceneID:   sceneID,
		SceneName: name,
	}
}

// TopicSlot is one L2 conversation node: a single turn settled by Update, or a
// Dream-fused group of turns. A scene's depth-1 topic set IS the host's
// context for that session, so a topic carries exactly one keyword track
// (FusedKeywords). What was said lives in the L4 archives keyed by this topic's
// own ID — a topic lists none of them.
// Tree: parent_id (nil = depth-1 root) + children_ids. Depth 1 = current
// surface (turns and fused groups), 2+ = sunk history; depth >= 4 is deleted
// on Dream.
type TopicSlot struct {
	ID          uint64   `json:"id"`
	SceneID     uint64   `json:"scene_id"`
	ParentID    *uint64  `json:"parent_id,omitempty"`
	ChildrenIDs []uint64 `json:"children_ids"`
	Depth       uint8    `json:"depth"`

	FusedKeywords []string `json:"fused_keywords"`

	UserTimestamp  int64 `json:"user_timestamp"`  // turn: user message time; fused: earliest user turn in group
	AgentTimestamp int64 `json:"agent_timestamp"` // turn: agent reply time; fused: latest agent turn in group
}

// ComputeTopicID derives a topic ID from sceneID and both timestamps.
// Dream-created fused topics use this form for deterministic replay.
func ComputeTopicID(sceneID uint64, userTS, agentTS int64) uint64 {
	return common.HashID(ComputeTopicKey(sceneID, userTS, agentTS))
}

// ComputeTurnTopicID derives the ID of the turn topic Search opened for a
// scene, from the scene's turn counter rather than its message timestamps:
// Search issues the ID before the turn's texts exist. The "turn:" namespace
// keeps it apart from ComputeTopicID, which Dream uses for fused parents over
// the same (minTS, maxTS) pair.
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

// HypergraphNode is a node within an L3 hypergraph. Importance has no write
// path in the engine and stays out of the public DTO; it remains here so a
// record written by an older file still decodes.
type HypergraphNode struct {
	IDHash     uint64   `json:"id_hash"`
	GraphID    uint64   `json:"graph_id"`
	Title      string   `json:"title"`
	NodeType   string   `json:"node_type"`
	Content    string   `json:"content"`
	Keywords   []string `json:"keywords"`
	SourceRef  *string  `json:"source_ref,omitempty"`
	Importance float32  `json:"importance"`
	CreatedAt  int64    `json:"created_at"`
	UpdatedAt  int64    `json:"updated_at"`
}

// HypergraphEdge is a hyperedge within an L3 hypergraph: an unordered relation
// over its member nodes, identified by members plus Kind (see repo.CreateEdgeL3).
// Weight and Label have no write path in the engine and stay out of the public
// DTO; they remain here so older records still decode.
type HypergraphEdge struct {
	IDHash    uint64        `json:"id_hash"`
	GraphID   uint64        `json:"graph_id"`
	Kind      GraphEdgeKind `json:"kind"`
	NodeIDs   []uint64      `json:"node_ids"`
	Weight    float32       `json:"weight"`
	Label     *string       `json:"label,omitempty"`
	CreatedAt int64         `json:"created_at"`
}

// Message roles in an ArchiveSlot. A host declaring an utterance picks one of
// RoleUser / RoleAgent / RoleSystem; the append boundary refuses RoleDream, which
// is the library's own stamp on a fused group's summary and stays off the public
// constants, so a host cannot write a record that reads as consolidated. Role
// qualifies an utterance — an event record leaves it 0.
const (
	RoleUser   uint8 = 0
	RoleAgent  uint8 = 1
	RoleSystem uint8 = 2
	RoleDream  uint8 = 3
)

// Utterances hold Seq 1 and 2 of their topic. Auto-allocation starts above these
// two slots: a host records events while the turn runs and appends the originals
// whenever it chooses, and the dialogue still lands on the slots a reader looks
// for them on. Naming a reserved Seq explicitly writes that slot, overwriting
// whatever kind holds it.
const (
	SeqUser  uint64 = 1
	SeqAgent uint64 = 2
	// LastUtteranceSeq is the highest slot an utterance may occupy; the first
	// event of a topic is one past it.
	LastUtteranceSeq = SeqAgent
)

// ArchiveSlot stores one piece of a topic's content: a dialogue original
// (KindUtterance) or an operation event the host recorded (KindEvent).
// (ContextID, Seq) addresses it, so re-writing one Seq overwrites in place.
// Role, ContentType and EventType are orthogonal axes, not three names for one
// thing: Role says who spoke (utterances only), ContentType says what Content
// *is* (prose or a reference to media), EventType says what *happened* — events
// only, and the host names it.
type ArchiveSlot struct {
	IDHash      uint64      `json:"id_hash"`
	Kind        ArchiveKind `json:"kind"`
	Seq         uint64      `json:"seq"`
	ContentType ContentType `json:"content_type"`
	Role        uint8       `json:"role"`
	ContextID   uint64      `json:"context_id"`
	EventType   string      `json:"event_type,omitempty"`
	NodePath    string      `json:"node_path,omitempty"`
	CreatedAt   int64       `json:"created_at"`
	Content     string      `json:"content"`
}

// HashContent derives the id of one topic's content slot:
// hash("l4:"+topicID+":"+seq). The id is positional rather than content-derived:
// writing the same (topic, seq) again lands on the same record, which the engine
// re-points instead of keeping a second live copy. That is what lets a replayed
// turn converge without a list of the ids it supersedes.
func HashContent(topicID, seq uint64) uint64 {
	return common.HashID(fmt.Sprintf("l4:%d:%d", topicID, seq))
}

// Plan node status.
const (
	StatusPending    uint8 = 0
	StatusInProgress uint8 = 1
	StatusDone       uint8 = 2
	StatusFailed     uint8 = 3
	StatusRunning    uint8 = 4
)

// PlanNode is one node of an L6 plan tree. L6 holds nothing but these: a turn's
// events live in L4 beside its dialogue originals. TopicID is the turn topic
// that opened the tree and NodePath the host's dotted address inside it, so
// naming the turn is all a read needs to get its whole tree back.
// UpdatedAt is what the retention window reads — a commit stamps it, so a plan
// the host went quiet on stops being exempt once its last commit falls outside
// the window, while an in-flight one keeps its tree mid-task.
type PlanNode struct {
	IDHash     uint64 `json:"id_hash"`
	TopicID    uint64 `json:"topic_id"`
	ParentID   uint64 `json:"parent_id,omitempty"` // 0 = root
	NodePath   string `json:"node_path"`           // "1" / "1.2.1"
	Status     uint8  `json:"status"`
	Title      string `json:"title,omitempty"`       // empty = the view falls back to NodePath
	PlanType   string `json:"plan_type,omitempty"`   // plan/step/tool_call; empty = plain node
	Summary    string `json:"summary,omitempty"`     // completion abbreviation
	FinishedAt int64  `json:"finished_at,omitempty"` // stamped on a terminal status only
	UpdatedAt  int64  `json:"updated_at"`
}

// HashPlanNode derives a plan node id from the owning topic + nodePath,
// namespaced under a "plan:" prefix so it never collides with a content id
// (hash("l4:"+topic+":"+seq)) or a turn topic (hash("turn:"+scene:seq)).
func HashPlanNode(topicID uint64, nodePath string) uint64 {
	return common.HashID("plan:" + fmt.Sprintf("%d:%s", topicID, nodePath))
}
