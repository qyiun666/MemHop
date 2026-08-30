// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0-L6 data models for the MemHop memory database.
package core

import (
	"fmt"
	"strings"

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
	IDHash        uint64   `json:"id_hash"`
	SceneID       uint64   `json:"scene_id"`
	TopicIDs      []uint64 `json:"topic_ids"`
	VectorPageRef uint64   `json:"vector_page_ref"`
	Importance    float32  `json:"importance"`
	Valence       float64  `json:"valence"`
	Arousal       float64  `json:"arousal"`
	CreatedAt     int64    `json:"created_at"`
	UpdatedAt     int64    `json:"updated_at"`
	EdgeIDs       []uint64 `json:"edge_ids"`
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
// The node is created/updated only during Dream, but the ID is computable at
// query time without any index, which is what makes spreading-activation
// association a pure storage-level graph walk.
func SceneNodeID(sceneID uint64) uint64 {
	return common.HashID("l1:" + common.FormatHash(sceneID))
}

// SceneSlot is an L2 scene container holding multiple session topics;
// HitCount/LastHitAt fold the former L6 scene-usage feedback into the scene
// record (retrieval-hit statistics consumed by Dream's usage feedback).
type SceneSlot struct {
	SceneID    uint64 `json:"scene_id"`
	SceneName  string `json:"scene_name"`
	TopicCount int    `json:"topic_count"` // depth-1 root topics under this scene
	HitCount   uint32 `json:"hit_count"`   // cumulative retrieval hit count
	LastHitAt  int64  `json:"last_hit_at"` // last hit time (Unix ms)
	L3ID       uint64 `json:"l3_id"`       // 新增：场景固定挂靠的目录/项目域 L3 图（N:1）
}

// NewSceneSlot builds a SceneSlot from a name; ID is the xxhash64 of the name.
func NewSceneSlot(name string) SceneSlot {
	return SceneSlot{
		SceneID:   common.HashID(name),
		SceneName: name,
	}
}

// TopicSlot is an L2 dual-track session node (user/agent). Tree: parent_id
// (nil = depth-1 root) + children_ids. Depth 1 = raw turns, 2 = compression
// groups, 3 = meta summaries; depth >= 4 triggers subtree deletion on Dream.
type TopicSlot struct {
	ID          uint64   `json:"id"`
	SceneID     uint64   `json:"scene_id"`
	ParentID    *uint64  `json:"parent_id,omitempty"`
	ChildrenIDs []uint64 `json:"children_ids"`
	Depth       uint8    `json:"depth"`

	UserKeywords  []string `json:"user_keywords"`
	UserTimestamp int64    `json:"user_timestamp"` // Dream node = earliest user turn in group

	L4Refs []uint64 `json:"l4_refs"`
	L3Refs []uint64 `json:"l3_refs"`

	AgentKeywords  []string `json:"agent_keywords"`
	AgentTimestamp int64    `json:"agent_timestamp"` // Dream node = latest agent reply in group

	FusedKeywords []string `json:"fused_keywords"`

	CentroidPageRef uint64 `json:"centroid_page_ref"`
}

// ComputeTopicID derives a topic ID from sceneID and both timestamps.
// Dream-created fused topics use this form for deterministic replay.
func ComputeTopicID(sceneID uint64, userTS, agentTS int64) uint64 {
	combined := fmt.Sprintf("%d:%d:%d", sceneID, userTS, agentTS)
	return common.HashID(combined)
}

// ComputeTopicIDForText derives a Search-created topic ID that also binds
// the user text. This keeps the timestamp-only ID stable for Dream, while
// two different messages that happen to share scene and millisecond no
// longer collide and overwrite each other.
func ComputeTopicIDForText(sceneID uint64, userTS int64, text string) uint64 {
	combined := fmt.Sprintf("%d:%d:0:%s", sceneID, userTS, text)
	return common.HashID(combined)
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

// HypergraphEdge is an edge within an L3 hypergraph (supports hyperedges).
type HypergraphEdge struct {
	IDHash    uint64        `json:"id_hash"`
	GraphID   uint64        `json:"graph_id"`
	Kind      GraphEdgeKind `json:"kind"`
	NodeIDs   []uint64      `json:"node_ids"`
	Weight    float32       `json:"weight"`
	Label     *string       `json:"label,omitempty"`
	CreatedAt int64         `json:"created_at"`
}

// AdjacencyEntry is one entry in the adjacency index for a node.
type AdjacencyEntry struct {
	NodeHash     uint64
	EdgeHash     uint64
	Kind         GraphEdgeKind
	ConnectedIDs []uint64 // other nodes in this hyperedge (excluding NodeHash)
}

// Message roles in an ArchiveSlot.
const (
	RoleUser   uint8 = 0
	RoleAgent  uint8 = 1
	RoleSystem uint8 = 2
	RoleDream  uint8 = 3
)

// ArchiveSlot stores a user/agent chat message under an L2 scene context.
type ArchiveSlot struct {
	IDHash      uint64      `json:"id_hash"`
	ContentType ContentType `json:"content_type"`
	Role        uint8       `json:"role"`
	ContextID   uint64      `json:"context_id"`
	CreatedAt   int64       `json:"created_at"`
	Content     string      `json:"content"`
	Metadata    *string     `json:"metadata,omitempty"`
}

// Capability is an L5 reusable capability: a wrapper around host resources
// (MCP tools / skills) that MemHop stores and matches but never executes.
type Capability struct {
	IDHash        uint64           `json:"id_hash"`
	Name          string           `json:"name"`
	Version       string           `json:"version"`
	Type          CapabilityType   `json:"type"`
	Summary       string           `json:"summary"`
	Trigger       string           `json:"trigger"`
	Resources     []ResourceRef    `json:"resources"`
	Workflow      *Workflow        `json:"workflow,omitempty"`
	Status        CapabilityStatus `json:"status"`
	Origin        CapabilityOrigin `json:"origin"`
	FileHash      string           `json:"file_hash,omitempty"`
	SuccessRate   float32          `json:"success_rate"`
	TriggerCount  uint32           `json:"trigger_count"`
	LastTriggered int64            `json:"last_triggered"`
	CreatedAt     int64            `json:"created_at"`
	UpdatedAt     int64            `json:"updated_at"`
}

// NormalizeCapabilityName returns the canonical lowercase name used for IDs
// and duplicate detection.
func NormalizeCapabilityName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// CapabilityID derives the stable L5 record ID from a capability name.
func CapabilityID(name string) uint64 {
	return common.HashID("capability:" + NormalizeCapabilityName(name))
}

// PromptCard renders the concise capability view intended for an LLM prompt.
func (c Capability) PromptCard() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[capability: %s]\n", c.Name)
	fmt.Fprintf(&b, "id: %s\n", common.FormatHash(c.IDHash))
	fmt.Fprintf(&b, "type: %s\n", c.Type)
	if c.Version != "" {
		fmt.Fprintf(&b, "version: %s\n", c.Version)
	}
	if c.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", c.Summary)
	}
	if c.Trigger != "" {
		fmt.Fprintf(&b, "trigger: %s\n", c.Trigger)
	}
	for _, r := range c.Resources {
		fmt.Fprintf(&b, "resource: %s %s", r.Type, r.Name)
		if r.Ref != "" {
			fmt.Fprintf(&b, " (%s)", r.Ref)
		}
		b.WriteByte('\n')
		if r.Desc != "" {
			fmt.Fprintf(&b, "  use: %s\n", r.Desc)
		}
		if r.Input != "" {
			fmt.Fprintf(&b, "  input: %s\n", r.Input)
		}
		if r.Output != "" {
			fmt.Fprintf(&b, "  output: %s\n", r.Output)
		}
	}
	if c.Workflow != nil {
		refs := make([]string, 0, len(c.Workflow.Steps))
		for _, step := range c.Workflow.Steps {
			refs = append(refs, step.Ref)
		}
		fmt.Fprintf(&b, "flow: %s\n", strings.Join(refs, " -> "))
	}
	if c.TriggerCount > 0 || c.SuccessRate > 0 {
		fmt.Fprintf(&b, "usage: %d, success_rate: %.2f\n", c.TriggerCount, c.SuccessRate)
	}
	return b.String()
}

// ResourceRef is one wrapped resource (an MCP tool, a skill, or an api
// method). The tool-declaration fields (Name/Desc/Input/Output) mirror the
// host tool spec shape exactly (meowire ToolSpec semantics): a host projects
// a resource to its own tool declaration with a pure field copy, no format
// conversion. MemHop stores these references but does not execute them.
type ResourceRef struct {
	Type   CapabilityType `json:"type"`             // mcp | skill | api
	Name   string         `json:"name"`             // tool name (ToolSpec.Name)
	Desc   string         `json:"desc"`             // call contract for the LLM (ToolSpec.Desc)
	Input  string         `json:"input,omitempty"`  // args JSON Schema string (ToolSpec.Input)
	Output string         `json:"output,omitempty"` // output description (ToolSpec.Output)
	Ref    string         `json:"ref,omitempty"`    // MCP server address / skill path / api:Method / command
	Config *string        `json:"config,omitempty"` // connection config (endpoint etc.), not an args schema
}

// Workflow is the ordered orchestration of a composite capability.
type Workflow struct {
	Steps []WorkflowStep `json:"steps"`
}

// WorkflowStep is one orchestration step referencing a resource (by
// Resources[].Name) or another capability (by name). Args carries the step
// parameters a host replays the action chain with (JSON Schema in Input).
type WorkflowStep struct {
	Ref    string         `json:"ref"`
	Action string         `json:"action,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
}

// Plan node type for TrajectorySlot: either a raw trajectory event or a plan node.
const (
	NodeTypeEvent uint8 = 0 // 轨迹事件
	NodeTypePlan  uint8 = 1 // 计划节点
)

// Plan node status (only meaningful for NodeTypePlan nodes).
const (
	StatusPending    uint8 = 0
	StatusInProgress uint8 = 1
	StatusDone       uint8 = 2
	StatusFailed     uint8 = 3
)

// TrajectorySlot is an L6 operation trajectory event appended by the host;
// one trajectory per agent turn (search starts it, update ends it), so
// SessionID is a turn key and Seq only counts within the turn. Short-lived:
// Dream purges events older than the 7-day retention window.
type TrajectorySlot struct {
	IDHash    uint64 `json:"id_hash"`    // hash(sessionID:seq)
	SessionID uint64 `json:"session_id"` // host-chosen turn key (parsed from the api's 16-hex id)
	Seq       uint64 `json:"seq"`        // per-turn increasing sequence

	NodeType    uint8  `json:"node_type"`               // 0=轨迹事件  1=计划节点
	PlanID      uint64 `json:"plan_id"`                 // 所属任务根（无任务=单个根）
	ParentID    uint64 `json:"parent_id,omitempty"`     // 父节点（0=根）
	NodePath    string `json:"node_path"`               // "1" / "1.2.1" / "1.2.2"
	Status      uint8  `json:"status,omitempty"`        // 仅节点：0=pending 1=in_progress 2=done 3=failed
	Summary     string `json:"summary,omitempty"`       // 仅节点：完成缩写摘要
	PlanNodeRef uint64 `json:"plan_node_ref,omitempty"` // 仅事件：挂到的计划节点（HashPlanNode(planID,nodePath)）

	EventType string `json:"event_type"`         // llm_request/llm_output/tool_call/tool_result/subagent_spawn/subagent_done/context_inject/ask_user/user_reply
	Payload   string `json:"payload"`            // event content (truncated to 4KB; no raw token stream)
	TopicID   uint64 `json:"topic_id,omitempty"` // L2 topic the turn resolves to (0 = unknown); set by the host from the search hit or the update's topic
	Timestamp int64  `json:"timestamp"`
}

// HashPlanNode derives a plan node id from planID + nodePath.
func HashPlanNode(planID uint64, nodePath string) uint64 {
	return common.HashID(fmt.Sprintf("%d:%s", planID, nodePath))
}
