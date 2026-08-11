// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0-L5 data models for the MemHop memory database.
package core

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

// ============================================================================
// ContentType — L4 archive content type
// ============================================================================

// ContentType represents the type of content stored in an ArchiveSlot.
type ContentType uint8

const (
	ContentText     ContentType = 0
	ContentImage    ContentType = 1
	ContentVideo    ContentType = 2
	ContentDocument ContentType = 3
	ContentAudio    ContentType = 4
	ContentCode     ContentType = 5
	ContentOther    ContentType = 0xFF
)

var contentTypeNames = map[ContentType]string{
	ContentText: "text", ContentImage: "image", ContentVideo: "video",
	ContentDocument: "document", ContentAudio: "audio",
	ContentCode: "code", ContentOther: "other",
}

func (c ContentType) String() string { return common.EnumString(c, contentTypeNames, "ContentType") }

func (c ContentType) MarshalJSON() ([]byte, error) { return common.EnumMarshal(c) }

func (c *ContentType) UnmarshalJSON(data []byte) error { return common.EnumAssign(c, data) }

// ============================================================================
// ChainStatus — L5 action chain status
// ============================================================================

// ChainStatus represents the lifecycle state of an ActionChainSlot.
type ChainStatus uint8

const (
	ChainDraft      ChainStatus = 0
	ChainActive     ChainStatus = 1
	ChainDeprecated ChainStatus = 2
)

var chainStatusNames = map[ChainStatus]string{
	ChainDraft: "draft", ChainActive: "active", ChainDeprecated: "deprecated",
}

func (c ChainStatus) String() string { return common.EnumString(c, chainStatusNames, "ChainStatus") }

func (c ChainStatus) MarshalJSON() ([]byte, error) { return common.EnumMarshal(c) }

func (c *ChainStatus) UnmarshalJSON(data []byte) error { return common.EnumAssign(c, data) }

// ============================================================================
// HyperedgeKind — L1 hyperedge type
// ============================================================================

// HyperedgeKind classifies L1 hyperedges in the hypergraph skeleton.
type HyperedgeKind uint8

const (
	HyperCoOccurrence HyperedgeKind = 0
	HyperCausal       HyperedgeKind = 1
	HyperSemantic     HyperedgeKind = 2
	HyperTemporal     HyperedgeKind = 3
	HyperHierarchical HyperedgeKind = 4
	HyperSequence     HyperedgeKind = 5
)

var hyperedgeKindNames = map[HyperedgeKind]string{
	HyperCoOccurrence: "co_occurrence", HyperCausal: "causal",
	HyperSemantic: "semantic", HyperTemporal: "temporal",
	HyperHierarchical: "hierarchical", HyperSequence: "sequence",
}

func (k HyperedgeKind) String() string {
	return common.EnumString(k, hyperedgeKindNames, "HyperedgeKind")
}

func (k HyperedgeKind) MarshalJSON() ([]byte, error) { return common.EnumMarshal(k) }

func (k *HyperedgeKind) UnmarshalJSON(data []byte) error { return common.EnumAssign(k, data) }

// ============================================================================
// SourceKind — L3 hypergraph source type
// ============================================================================

// SourceKind identifies how an L3 HypergraphSlot was created.
type SourceKind uint8

const (
	SourcePath    SourceKind = 0
	SourceContext SourceKind = 1
	SourceURL     SourceKind = 2
	SourceManual  SourceKind = 3
)

var sourceKindNames = map[SourceKind]string{
	SourcePath: "path", SourceContext: "context",
	SourceURL: "url", SourceManual: "manual",
}

func (s SourceKind) String() string { return common.EnumString(s, sourceKindNames, "SourceKind") }

func (s SourceKind) MarshalJSON() ([]byte, error) { return common.EnumMarshal(s) }

func (s *SourceKind) UnmarshalJSON(data []byte) error { return common.EnumAssign(s, data) }

// ============================================================================
// GraphEdgeKind — L3 hypergraph edge type
// ============================================================================

// GraphEdgeKind classifies edges within an L3 hypergraph.
type GraphEdgeKind uint8

const (
	EdgeRelated    GraphEdgeKind = 0
	EdgeCausal     GraphEdgeKind = 1
	EdgePartOf     GraphEdgeKind = 2
	EdgeSequence   GraphEdgeKind = 3
	EdgeDependency GraphEdgeKind = 4
	EdgeCustom     GraphEdgeKind = 5
)

var graphEdgeKindNames = map[GraphEdgeKind]string{
	EdgeRelated: "related", EdgeCausal: "causal", EdgePartOf: "part_of",
	EdgeSequence: "sequence", EdgeDependency: "dependency", EdgeCustom: "custom",
}

func (k GraphEdgeKind) String() string {
	return common.EnumString(k, graphEdgeKindNames, "GraphEdgeKind")
}

func (k GraphEdgeKind) MarshalJSON() ([]byte, error) { return common.EnumMarshal(k) }

func (k *GraphEdgeKind) UnmarshalJSON(data []byte) error { return common.EnumAssign(k, data) }

// ============================================================================
// Layer — memory layer identifier
// ============================================================================

// Layer identifies which of the six cognitive memory layers a value belongs to.
type Layer uint8

const (
	LayerL0 Layer = 0
	LayerL1 Layer = 1
	LayerL2 Layer = 2
	LayerL3 Layer = 3
	LayerL4 Layer = 4
	LayerL5 Layer = 5
)

var layerNames = map[Layer]string{
	LayerL0: "L0", LayerL1: "L1", LayerL2: "L2",
	LayerL3: "L3", LayerL4: "L4", LayerL5: "L5",
}

func (l Layer) String() string { return common.EnumString(l, layerNames, "Layer") }

func (l Layer) MarshalJSON() ([]byte, error) { return common.EnumMarshal(l) }

func (l *Layer) UnmarshalJSON(data []byte) error { return common.EnumAssign(l, data) }

// ============================================================================
// L0 ProfileSlot — agent 画像
// ============================================================================

type ProfileSlot struct {
	IDHash          uint64            `json:"id_hash"`          //agent唯一标识
	Name            string            `json:"name"`             // agent名称
	Role            string            `json:"role"`             // agent角色
	Personality     string            `json:"personality"`      // agent人格
	Preferences     map[string]string `json:"preferences"`      // agent偏好
	Lexicon         map[string]string `json:"lexicon"`          // agent词汇表
	StyleTraits     []string          `json:"style_traits"`     // agent风格特征
	EmotionPatterns map[string]string `json:"emotion_patterns"` // agent情感模式
}

// ============================================================================
// L1 SceneNode / SceneEdge — 超级图（关联记忆图）
// ============================================================================

// SceneNode 是 L1 超级图节点，关联多个独立的 L2 Topic。
type SceneNode struct {
	IDHash        uint64   `json:"id_hash"`         // 节点唯一哈希标识
	SceneID       uint64   `json:"scene_id"`        // 所属 L2 Scene ID
	TopicIDs      []uint64 `json:"topic_ids"`       // 关联的 L2 Topic ID 列表
	VectorPageRef uint64   `json:"vector_page_ref"` // 向量嵌入页面引用（f32）
	Importance    float32  `json:"importance"`      // 重要性分数（Dream 衰减用）
	Valence       float64  `json:"valence"`         // 情感效价（正/负，影响衰减速率）
	Arousal       float64  `json:"arousal"`         // 情感唤醒度（强度，影响衰减速率）
	CreatedAt     int64    `json:"created_at"`      // 创建时间戳（毫秒）
	UpdatedAt     int64    `json:"updated_at"`      // 最后更新时间戳（毫秒）
	EdgeIDs       []uint64 `json:"edge_ids"`        // 关联的超图边 ID 列表
	LastDecayAt   int64    `json:"last_decay_at"`   // 上次衰减时间戳（毫秒），0=从未衰减，首次从 CreatedAt 起算，与 SceneEdge 对称
}

// SceneEdge 是 L1 超边，用于上层衰减逻辑。
type SceneEdge struct {
	IDHash    uint64        `json:"id_hash"`    // 边唯一哈希标识
	Kind      HyperedgeKind `json:"kind"`       // 边的语义类型（共现/因果/语义/时序/层级/序列）
	NodeIDs   []uint64      `json:"node_ids"`   // 关联的 L1 SceneNode ID 列表
	Weight    float32       `json:"weight"`     // 边权重，用于衰减和排序
	CreatedAt int64         `json:"created_at"` // 创建时间戳（毫秒）
	// LastDecayAt 是上次衰减时间戳（毫秒）；0 表示从未衰减过，
	// 此时首次衰减从 CreatedAt 开始计算。
	LastDecayAt int64 `json:"last_decay_at"`
}

// ============================================================================
// L2 SceneSlot / TopicSlot — 场景与话题
// ============================================================================

// SceneSlot 是 L2 的场景容器，一个场景包含多个会话 Topic。
type SceneSlot struct {
	SceneID   uint64 `json:"scene_id"`   // 场景唯一 ID（由场景名哈希生成）
	SceneName string `json:"scene_name"` // 场景名称
}

// NewSceneSlot 从名称创建 SceneSlot，ID 由名称 xxhash64 生成。
func NewSceneSlot(name string) SceneSlot {
	return SceneSlot{
		SceneID:   common.HashID(name),
		SceneName: name,
	}
}

// TopicSlot 是 L2 双轨会话节点（用户- Agent 双轨道）。
//
// 树结构：parent_id（nil = depth-1 根节点）+ children_ids。
// Depth 1 = 原始会话轮，2 = 压缩组，3 = 元摘要；
// depth >= 4 触发子树删除（Dream 压缩时）。
type TopicSlot struct {
	ID          uint64   `json:"id"`                  // 节点唯一 ID
	SceneID     uint64   `json:"scene_id"`            // 所属场景 ID
	ParentID    *uint64  `json:"parent_id,omitempty"` // 父节点 ID（nil 表示 depth-1 根节点）
	ChildrenIDs []uint64 `json:"children_ids"`        // 子节点 ID 列表
	Depth       uint8    `json:"depth"`               // 树深度（1=原始会话轮, 2=压缩组, 3=元摘要, >=4 触发子树删除）

	// User track — 用户侧
	UserKeywords  []string `json:"user_keywords"`  // 用户关键词
	UserTimestamp int64    `json:"user_timestamp"` // 用户发言时间戳（毫秒）；Dream 压缩节点 = 组内最早的用户发言时间

	// 关联引用（用户/agent 合并）
	L4Refs []uint64 `json:"l4_refs"` // 关联的 L4 档案 ID 列表（user/agent 追加去重）
	L3Refs []uint64 `json:"l3_refs"` // 关联的 L3 超图节点 ID 列表

	// Agent track — 助手侧
	AgentKeywords  []string `json:"agent_keywords"`  // agent 关键词
	AgentTimestamp int64    `json:"agent_timestamp"` // agent 回复时间戳（毫秒）；Dream 压缩节点 = 组内最后一个 agent 回复时间

	// Compression fields (depth >= 2) — 融合字段（深度 >= 2 时填充）
	FusedKeywords []string `json:"fused_keywords"` // 融合后的关键词列表

	// Retrieval 检索
	CentroidPageRef uint64 `json:"centroid_page_ref"` // 本体向量嵌入页面引用（f32）
}

// ComputeTopicID 根据 sceneID 和用户/agent 时间戳计算 Topic 唯一 ID。
func ComputeTopicID(sceneID uint64, userTS, agentTS int64) uint64 {
	combined := fmt.Sprintf("%d:%d:%d", sceneID, userTS, agentTS)
	return common.HashID(combined)
}

// ============================================================================
// L3 Hypergraph — Slot / Node / Edge（原生 uint64 JSON）
// ============================================================================

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

// ============================================================================
// L4 ArchiveSlot — 聊天记录
// ============================================================================

// ArchiveSlot 消息角色取值。
const (
	RoleUser   uint8 = 0 // 用户发言
	RoleAgent  uint8 = 1 // agent 回复
	RoleSystem uint8 = 2 // 系统消息
	RoleDream  uint8 = 3 // 梦境产物（MergedSummary 归档）
)

// ArchiveSlot 存储用户与 agent 的历史对话消息，
// 每条消息归属于一个 L2 Context（场景），构成完整的对话上下文。
type ArchiveSlot struct {
	IDHash      uint64      `json:"id_hash"`            // 消息唯一哈希标识
	ContentType ContentType `json:"content_type"`       // 内容类型（TEXT/IMAGE/CODE 等）
	Role        uint8       `json:"role"`               // 消息角色：见 Role* 常量
	ContextID   uint64      `json:"context_id"`         // 所属 L2 Context（场景）ID
	CreatedAt   int64       `json:"created_at"`         // 创建时间戳（毫秒）
	Content     string      `json:"content"`            // 消息正文
	Metadata    *string     `json:"metadata,omitempty"` // 可选元数据（JSON 格式扩展字段）
}

// ============================================================================
// L5 ActionChainSlot / ActionStep — 动作链
// ============================================================================

// ActionChainSlot 是 L5 动作链。
type ActionChainSlot struct {
	IDHash        uint64      `json:"id_hash"`        // 动作链唯一哈希标识（xxhash64）
	Title         string      `json:"title"`          // 动作链描述性标题
	Trigger       string      `json:"trigger"`        // 触发条件（关键词 / 场景描述）
	Status        ChainStatus `json:"status"`         // 生命周期状态（0=draft, 1=active, 2=deprecated）
	Confidence    float32     `json:"confidence"`     // 置信度（0.0 ~ 1.0）
	SuccessRate   float32     `json:"success_rate"`   // 历史执行成功率（0.0 ~ 1.0）
	TriggerCount  uint32      `json:"trigger_count"`  // 累计触发次数
	LastTriggered int64       `json:"last_triggered"` // 最后一次触发时间戳（毫秒）
	CreatedAt     int64       `json:"created_at"`     // 创建时间戳（毫秒）
	UpdatedAt     int64       `json:"updated_at"`     // 最后更新时间戳（毫秒）
}

// ActionStep is an individual step within an ActionChainSlot.
type ActionStep struct {
	IDHash     uint64  `json:"id_hash"`              // 步骤唯一哈希标识（xxhash64）
	ChainID    uint64  `json:"chain_id"`             // 所属 ActionChainSlot 的 IDHash
	StepOrder  uint16  `json:"step_order"`           // 执行顺序序号（0-based）
	Action     string  `json:"action"`               // 动作指令描述
	Parameters *string `json:"parameters,omitempty"` // 可选参数（JSON 格式）
	CreatedAt  int64   `json:"created_at"`           // 创建时间戳（毫秒）
}
