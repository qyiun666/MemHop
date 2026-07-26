# MemHop Go API 契约 (v0.60.0)

模块路径：`github.com/qyiun666/MemHop`（公开包为 `api/`，包名 `memhop`，导入方式 `import memhop "github.com/qyiun666/MemHop/api"`）

## 概述

MemHop v0.60.0 完成了对外 API 的**极致精简重构**：从 v0.57.x 的 46 个方法压缩到 **18 个方法**（减少 61%），保持类型安全的同时将同类操作按业务对象聚合。

**设计原则：**

1. **三大核心方法保持独立**：`Search` / `Update` / `Dream` 分别对应"查/写/巩固"三条主链路
2. **通用 CRUD 按 Layer 分发**：`Get(layer, id)` / `List(layer, req)` / `Delete(layer, id)` 覆盖所有层的基本增删改查
3. **分域 Op 结构体**：`Topic(op)` / `Knowledge(op)` / `Crystal(op)` 用数字 `Kind` 枚举承载各层特殊操作
4. **数字枚举 uint8**：所有 Kind 用 `1..N` 显式声明，便于序列化与调试
5. **业务命名替代层号命名**：`GetL2` → `Get(LayerTopic)`，`CreateActionChain` → `Crystal(COpCreateChain)`

**文件组织（api/ 目录，11 个源码文件）：**

| 文件 | 功能 |
|---|---|
| `memdb.go` | 生命周期（Open/OpenWithEncoder/Close/Checkpoint）+ 健康 + 类型别名导出 |
| `types_ops.go` | Layer 枚举 + 全部 Op/Result 结构体定义 |
| `search_api.go` | 核心：Search |
| `update_api.go` | 核心：Update（简化返回 error）+ 高级 UpdateMemory |
| `dream_api.go` | 核心：Dream(ctx, opts) |
| `crud_api.go` | 通用 CRUD：Get / List / Delete |
| `topic_api.go` | L0/L2 分域操作：Topic(op) |
| `knowledge_api.go` | L3 分域操作：Knowledge(op) |
| `crystal_api.go` | L5 分域操作：Crystal(op) |
| `store_api.go` | 批量写入：BatchStore / ImportMemory |
| `helpers_api.go` | 内部辅助函数：parseGraphID / subgraphToDTO 等 |

**通用约定：**

- **closed 检查**：每个方法首行检查实例是否已关闭，已关闭时返回 `ErrClosed`
- **ID 格式**：对外接口统一使用 16 位十六进制字符串（如 `"a1b2c3d4e5f67890"`），内部通过 `hash.ParseID` 转为 `uint64`；畸形 ID 直接报错
- **Timestamp 必填**：`Search` / `Update` / `UpdateMemory` 的时间戳均为**必填 Unix 毫秒**，`<= 0` 返回 `ErrInvalidQuery`（不回填当前时间）
- **并发安全**：公开方法持读锁；`Close` / `Dream` 持写锁；并发 `Dream` 第二个调用直接报错；`l1Reverse / l2Meta / profileCache` 用 `atomic.Pointer` 热替换
- **单实例契约**：一个 Agent 绑定一个 `.meh` 文件；文件排他锁保证同一文件同时只有一个 MemHop 实例（跨进程），第二次 Open 返回错误；多用户/命名空间为设计外
- **分词器单例**：分词器为进程级单例，首次 Open 加载字典耗时较长；同进程内以不同 engine 再次初始化会报错

## 完整方法清单（18 个）

| # | 分组 | 方法 | 参数 → 返回 |
|---|---|---|---|
| 1 | 生命周期 | `Open` | `(*Config) → (*MemHop, error)` |
| 2 | 生命周期 | `OpenWithEncoder` | `(*Config, Encoder) → (*MemHop, error)` |
| 3 | 生命周期 | `Close` | `() → error` |
| 4 | 生命周期 | `Checkpoint` | `() → error` |
| 5 | 核心 | `Search` | `(SearchQuery) → (*SearchResult, error)` |
| 6 | 核心 | `Update` | `(topicID, text, ts) → error` |
| 7 | 核心 | `Dream` | `(ctx, *DreamOptions) → (*DreamReport, error)` |
| 8 | 通用 CRUD | `Get` | `(Layer, id) → (*GetResult, error)` |
| 9 | 通用 CRUD | `List` | `(Layer, ListRequest) → (*ListResult, error)` |
| 10 | 通用 CRUD | `Delete` | `(Layer, id) → error` |
| 11 | 通用 CRUD | `UpdateMemory` | `(UpdateRequest) → (*UpdateResult, error)` |
| 12 | 分域 | `Topic` | `(TopicOp) → (*TopicResult, error)` |
| 13 | 分域 | `Knowledge` | `(KnowledgeOp) → (*KnowledgeResult, error)` |
| 14 | 分域 | `Crystal` | `(CrystalOp) → (*CrystalResult, error)` |
| 15 | 批量 | `BatchStore` | `(StoreBatch) → (*StoreResult, error)` |
| 16 | 批量 | `ImportMemory` | `(ImportRequest) → (*ImportResult, error)` |
| 17 | 状态 | `HealthCheck` | `() → (*HealthStatus, error)` |
| 18 | 状态 | `SessionStatus` | `() → (*SessionStatus, error)` |

外加包级工具：`memhop.IsDSLQuery(q string) bool`（判断字符串是否是 L3 DSL 查询）。

---

## 生命周期（4 个方法）

### `func Open(config *Config) (*MemHop, error)`

创建或打开一个 MemHop 数据库。必填项在 `config.Validate()` 中一次性校验，Open 阶段即失败：

- `DBPath` 非空
- `VectorDim` ∈ (0, 65535]
- `EncoderAddr` / `EmbedModel` 非空
- `LLM.APIURL` / `LLM.APIKey` / `LLM.Model` 非空
- `EncoderTimeoutSecs >= 0`（0 取默认 20s）

`Defaults` 的 nil 子配置（SearchWeights / DecayConfig / SessionConfig）在 Open 时一次性回填文档化默认值。Open 同时获取文件排他锁（拿不到锁返回 "database already open by another instance"），重建稀疏索引与 L2 元索引。

### `func OpenWithEncoder(config *Config, enc Encoder) (*MemHop, error)`

使用自定义 Encoder 打开数据库。调用者提供实现了 `Encoder` 接口（Encode/Dim/Mode/IsAvailable）的实例，用于离线测试、mock 或替换默认 HTTP 编码器。

### `func (m *MemHop) Close() error`

持久化所有数据并释放资源。内部流程：构建索引快照 → 编码器关闭 → 存储引擎关闭。编码器关闭失败不影响存储引擎关闭（标记为编码器错误）。

### `func (m *MemHop) Checkpoint() error`

将当前状态持久化到磁盘而不关闭数据库。构建索引快照后写入存储引擎的 A/B header。

---

## 核心方法（3 个方法，不合并）

### `func (m *MemHop) Search(q SearchQuery) (*SearchResult, error)`

核心对话循环的**用户侧**入口：回忆 + 存用户内容。运行完整搜索管线（BM25 + f16 向量 + 实体，三通道 RRF 融合）返回匹配上下文，同时将用户原文经 LLM `ExtractFacts` 提取 atomic facts 后追加到最佳匹配的 L2 topic 的 L4 Archive。

**SearchQuery 字段：**

| 字段 | 类型 | 说明 |
|---|---|---|
| `Text` | `string` | 搜索文本（对话原文） |
| `Timestamp` | `int64` | **必填**，消息的 Unix 毫秒时间戳；`<= 0` 返回 `ErrInvalidQuery` |
| `MaxResults` | `int` | 最大结果数（可选） |
| `DirectedL2ID` | `*string` | 定向 L2 检索：仅在该主题子树内搜索（可选） |
| `DirectedL3ID` | `*string` | 定向 L3 检索（预留，可选） |
| `AutoCreate` | `bool` | 是否自动创建话题（可选） |

**行为要点：**

- 当 `DirectedL2ID != nil` 时走定向路径（在目标 topic 所属 scene 内创建新 topic），否则走全量检索
- 检索结果按场景激活 + 时间最近性加权排序
- 每次 Search 都会在最佳匹配 scene 内创建一个新的 depth1 topic 作为本轮写入目标，用户原文 + facts 以 role=0（用户）落盘到该 topic
- LLM 不可用时回退到 gse 分词结果

**SearchResult 字段：**

| 字段 | 类型 | 说明 |
|---|---|---|
| `Profile` | `ProfileResult` | L0 档案摘要 |
| `Contexts` | `[]ContextResult` | 主匹配上下文列表 |
| `AssociatedContexts` | `[]ContextResult` | 关联上下文（激活扩散得来） |
| `Crystals` | `[]CrystalSummary` | 匹配的行动链 |
| `NewTopicID` | `string` | 本轮新建 topic 的 hex ID（写入目标），传给 `Update` 追加 Agent 回复；未创建 topic 时为空 |

### `func (m *MemHop) Update(topicID string, text string, timestamp int64) error`

核心对话循环的**Agent 侧**入口：将 Agent 回复以 role=1（agent）追加到指定 topic 的 L4 Archive，并同步更新 L2 关键词。

**v0.60.0 变化**：返回类型从 `(*UpdateResult, error)` 简化为 **`error`**。原返回值 `{Status:"Updated", ID:""}` 恒定，无信息量。

**参数：**

| 参数 | 类型 | 说明 |
|---|---|---|
| `topicID` | `string` | L2 topic ID（16 位 hex），取 `Search` 返回的 `SearchResult.NewTopicID` |
| `text` | `string` | Agent 回复原文 |
| `timestamp` | `int64` | **必填**，Unix 毫秒时间戳；`<= 0` 返回 `ErrInvalidQuery` |

### `func (m *MemHop) Dream(ctx context.Context, opts *DreamOptions) (*DreamReport, error)`

记忆巩固周期。**v0.60.0 变化**：合并原 `Dream(opts)` + `DreamWithContext(ctx, opts)` 为单一方法，`context.Context` 作为第一个参数（Go 惯用法）。传 `context.Background()` 表示不需要主动取消；ctx 透传到每个 LLM 调用并在阶段间检查，取消时返回包装后的 `ctx.Err()`。Dream 持实例写锁，并发 Dream 调用返回明确错误（不排队）。

**Dream 仅作用于 L0–L2**（L3 蒸馏与 L5 结晶为设计外），流水线固定五阶段（`DreamReport.Stages` 长度恒为 5）：

1. `l2_compress` — LLM 归组合并相关 topic，降级陈旧上下文
2. `l1_rebuild` — 重建连接 L2 上下文的超图骨架
3. `l1_decay` — 衰减情景重要性，剪枝弱节点/边
4. `l0_profile` — 基于巩固后的记忆重建 Agent 画像
5. `l0_distill` — 蒸馏情绪/MBTI 模式（`SkipDistill=true` 时该阶段标记为 skipped）

每次 Dream 调用最多发起 3 次 LLM 请求（Consolidate + 至多一次重试 + 一次 L0 蒸馏 Chat）。

**DreamOptions 字段：**

| 字段 | 类型 | 说明 |
|---|---|---|
| `LLM` | `LlmProvider` | 注入的 LLM 提供者（可选） |
| `Chat` | `ChatProvider` | 注入的 Chat 提供者（可选） |
| `L2IDs` | `[]string` | 仅处理指定 topic（可选；任一非法 hex ID 整体报错） |
| `SkipDistill` | `bool` | 跳过 L0 情绪/MBTI 蒸馏阶段 |

---

## 通用 CRUD（4 个方法）

### Layer 枚举

```go
type Layer uint8
const (
    LayerProfile   Layer = 0 // L0 — 单条 Agent 档案
    LayerScene     Layer = 1 // L1 — 场景图（Node + Hyperedge）
    LayerTopic     Layer = 2 // L2 — 主题/上下文
    LayerKnowledge Layer = 3 // L3 — 超图知识
    LayerArchive   Layer = 4 // L4 — 对话档案
    LayerCrystal   Layer = 5 // L5 — 行动链/结晶
)
```

### `func (m *MemHop) Get(layer Layer, id string) (*GetResult, error)`

按层读取单条记录。`GetResult` 采用 **union 结构**，只有对应字段被填充。

| Layer | id 语义 | 返回字段 |
|---|---|---|
| `LayerProfile` | 忽略（可为 `""`） | `Profile *ProfileSlot` |
| `LayerScene` | 忽略（可为 `""`） | `SceneGraph *L1Graph` |
| `LayerTopic` | L2 topic ID | `Topic *TopicDetail` |
| `LayerKnowledge` | L3 graph ID | `Knowledge *L3Detail` |
| `LayerArchive` | L4 archive ID | `Archive *Archive` |
| `LayerCrystal` | L5 chain ID | `Crystal *CrystalSummary` |

**示例：**

```go
profRes, err := mh.Get(memhop.LayerProfile, "")
profile := profRes.Profile

topicRes, err := mh.Get(memhop.LayerTopic, "a1b2c3d4e5f67890")
detail := topicRes.Topic
```

### `func (m *MemHop) List(layer Layer, req ListRequest) (*ListResult, error)`

按层批量列举。`ListRequest` 与 `ListResult` 均采用 union 结构，只需填/读对应层的字段。

**支持的 Layer：**`LayerTopic` / `LayerKnowledge` / `LayerArchive` / `LayerCrystal`（`LayerProfile` / `LayerScene` 只有单条记录，请用 `Get`）。

| Layer | req 字段 | 返回字段 |
|---|---|---|
| `LayerTopic` | `Topic *TopicListQuery` | `Topics *TopicListResult` |
| `LayerKnowledge` | `Knowledge *KnowledgeListQuery` | `Knowledge *KnowledgeListResult` |
| `LayerArchive` | `Archive *ArchiveQuery` | `Archives *ArchiveListResult` |
| `LayerCrystal` | `Crystal *CrystalListQuery` | `Crystals *CrystalListResult` |

**示例：**

```go
res, err := mh.List(memhop.LayerTopic, memhop.ListRequest{
    Topic: &memhop.TopicListQuery{Page: 1, PageSize: 20},
})
topics := res.Topics.Items
```

### `func (m *MemHop) Delete(layer Layer, id string) error`

按层删除单条记录。**支持的 Layer：**`LayerTopic` / `LayerKnowledge` / `LayerCrystal`（其他层无独立删除语义）。

删除 `LayerKnowledge` 时会同步：
- 使 L3 邻接缓存失效
- 清空 L3 度数索引

### `func (m *MemHop) UpdateMemory(req UpdateRequest) (*UpdateResult, error)`

高级更新入口：按 `Layer` 分发到 L0 / L2 / L3 / L5 的字段级更新（L2 支持 `dialogue_text` 对话追加）。

**UpdateRequest 字段：**

| 字段 | 类型 | 说明 |
|---|---|---|
| `ID` | `string` | 目标记录 ID（16 位 hex），必填 |
| `Layer` | `Layer` | 目标层枚举：`LayerProfile(0)` / `LayerTopic(2)` / `LayerKnowledge(3)` / `LayerCrystal(5)`，其他层报错 |
| `Fields` | `map[string]json.RawMessage` | 待更新字段的键值对（类型不匹配返回 `ErrInvalidQuery`） |
| `Timestamp` | `int64` | **必填**，Unix 毫秒时间戳；`<= 0` 返回 `ErrInvalidQuery` |

**UpdateResult 字段：**`Status UpdateStatus`（`"Created"` / `"Updated"` / `"Archived"`）+ `ID string`。

**支持字段矩阵：**

- `LayerProfile` — `Fields` 支持 `name` / `role` / `personality` / `worldview` / `preferences` / `lexicon` / `style_traits` / `emotion_patterns`
- `LayerTopic` — `Fields` 支持 `dialogue_text`（搭配 `role`: 0=user / 1=agent）与 topic 元字段
- `LayerKnowledge` — `Fields` 支持 L3 slot 元字段
- `LayerCrystal` — `Fields` 支持 L5 chain 元字段（Confidence/Status/...）

---

## 分域操作（3 个方法）

### `func (m *MemHop) Topic(op TopicOp) (*TopicResult, error)` — L0/L2 分域

**TopicOpKind 枚举：**

```go
const (
    TOpSetProfile TopicOpKind = 1 // L0 — 覆写 Profile
    TOpMerge      TopicOpKind = 2 // L2 — 合并次要 topic 到主 topic
    TOpSceneTree  TopicOpKind = 3 // L2 — 返回完整场景树
)
```

**参数与返回：**

| Kind | 必填参数 | 返回字段 |
|---|---|---|
| `TOpSetProfile` | `ProfileDelta` | `TopicResult{}`（空） |
| `TOpMerge` | `PrimaryID`, `MergeIDs` | `Merge *MergeResult` |
| `TOpSceneTree` | `SceneID` | `SceneTree *SceneTreeResult` |

**示例：**

```go
_, err := mh.Topic(memhop.TopicOp{
    Kind: memhop.TOpSetProfile,
    ProfileDelta: &memhop.ProfileDelta{
        Name: strPtr("助手"),
        Role: strPtr("AI Assistant"),
    },
})

treeRes, err := mh.Topic(memhop.TopicOp{
    Kind:    memhop.TOpSceneTree,
    SceneID: sceneID,
})
tree := treeRes.SceneTree
```

### `func (m *MemHop) Knowledge(op KnowledgeOp) (*KnowledgeResult, error)` — L3 分域

**KnowledgeOpKind 枚举（10 个）：**

```go
const (
    KOpCreateGraph       KnowledgeOpKind = 1  // 创建新的超图槽
    KOpAddNode           KnowledgeOpKind = 2  // 向图中添加节点
    KOpAddEdge           KnowledgeOpKind = 3  // 向图中添加超边
    KOpDeleteNode        KnowledgeOpKind = 4  // 按 16 位 hex ID 删除节点
    KOpDeleteEdge        KnowledgeOpKind = 5  // 按 16 位 hex ID 删除边
    KOpSearch            KnowledgeOpKind = 6  // L3 节点统一搜索
    KOpGetNodes          KnowledgeOpKind = 7  // 批量取节点（按 ID / 关键词 / 类型）
    KOpGraphQuery        KnowledgeOpKind = 8  // BFS 子图抽取
    KOpDSL               KnowledgeOpKind = 9  // DSL 查询（MATCH / PATH / SUBGRAPH）
    KOpDetectCommunities KnowledgeOpKind = 10 // Louvain 社区检测
)
```

**参数与返回：**

| Kind | 必填参数 | 返回字段 |
|---|---|---|
| `KOpCreateGraph` | `Name` | `Slot *HypergraphSlot` |
| `KOpAddNode` | `GraphID`, `Node` | — |
| `KOpAddEdge` | `GraphID`, `Edge` | — |
| `KOpDeleteNode` | `NodeID`（16 位 hex） | — |
| `KOpDeleteEdge` | `EdgeID`（16 位 hex） | — |
| `KOpSearch` | `SearchQuery` | `Search *L3SearchResult` |
| `KOpGetNodes` | `NodesQuery` | `Nodes *KnowledgeNodesResult` |
| `KOpGraphQuery` | `StartNode`, `MaxDepth`, `EdgeKinds`（可选） | `Subgraph *Subgraph` |
| `KOpDSL` | `DSLString` | `DSL *DSLQueryResult` |
| `KOpDetectCommunities` | `CommunityCfg`（可为 nil 走默认） | `Community *CommunityResult` |

**示例：**

```go
// 创建图 + 加节点
res, _ := mh.Knowledge(memhop.KnowledgeOp{Kind: memhop.KOpCreateGraph, Name: "知识图谱"})
graphID := memhop.FormatHash(res.Slot.IDHash)

node := &memhop.HypergraphNode{Title: "Go语言", NodeType: "concept"}
_, _ = mh.Knowledge(memhop.KnowledgeOp{
    Kind: memhop.KOpAddNode, GraphID: graphID, Node: node,
})

// 搜索
r, _ := mh.Knowledge(memhop.KnowledgeOp{
    Kind:        memhop.KOpSearch,
    SearchQuery: &memhop.L3SearchQuery{Keyword: "Go"},
})
for _, n := range r.Search.Nodes { /* ... */ }
```

### `func (m *MemHop) Crystal(op CrystalOp) (*CrystalResult, error)` — L5 分域

**CrystalOpKind 枚举（6 个）：**

```go
const (
    COpCreateChain      CrystalOpKind = 1 // 创建新的行动链
    COpAppendStep       CrystalOpKind = 2 // 追加步骤到已有链
    COpUpdateConfidence CrystalOpKind = 3 // EMA 更新链的置信度
    COpIncrTrigger      CrystalOpKind = 4 // 触发计数器 +1
    COpBatchDelete      CrystalOpKind = 5 // 批量删除结晶
    COpBatchUpdate      CrystalOpKind = 6 // 批量更新链字段
)
```

**参数与返回：**

| Kind | 必填参数 | 返回字段 |
|---|---|---|
| `COpCreateChain` | `ChainInput` | `ChainID string` |
| `COpAppendStep` | `ChainID`, `StepInput` | `StepID string` |
| `COpUpdateConfidence` | `ChainID`, `Success` | — |
| `COpIncrTrigger` | `ChainID` | — |
| `COpBatchDelete` | `IDs` | — |
| `COpBatchUpdate` | `Updates` | — |

**示例：**

```go
r, _ := mh.Crystal(memhop.CrystalOp{
    Kind: memhop.COpCreateChain,
    ChainInput: &memhop.L5ChainInput{
        Title:   "call_weather",
        Trigger: "user asks about weather",
        Steps: []memhop.L5StepInput{
            {Action: "call_weather_api"},
            {Action: "format_response"},
        },
    },
})
chainID := r.ChainID

_, _ = mh.Crystal(memhop.CrystalOp{
    Kind: memhop.COpAppendStep, ChainID: chainID,
    StepInput: &memhop.L5StepInput{Action: "log_result"},
})

_, _ = mh.Crystal(memhop.CrystalOp{Kind: memhop.COpIncrTrigger, ChainID: chainID})
_, _ = mh.Crystal(memhop.CrystalOp{Kind: memhop.COpUpdateConfidence, ChainID: chainID, Success: true})
```

---

## 批量写入（2 个方法）

### `func (m *MemHop) BatchStore(batch StoreBatch) (*StoreResult, error)`

批量写入 Store 项：五阶段管线（编码 → L4 归档 → L1 写入+去重 → L2 topic 更新 → 超边）。

**StoreItem 字段：**

| 字段 | 类型 | 说明 |
|---|---|---|
| `Content` | `string` | 内容原文 |
| `Keywords` | `[]string` | **必填**，预提取的关键词/事实；为空时整批报错，不做静默提取回退 |
| `Source` | `string` | 来源标签 |
| `SourceType` | `string` | 来源类型 |
| `Score` | `float64` | 重要性分数 |
| `TopicLabel` | `*string` | 目标 topic 标签（可选，缺省归入 "default"） |

**StoreResult 字段：**`StoredCount uint32` + `ItemIDs []string`（与输入同序的结果节点 ID）+ `Items []StoreItemStatus`（按 item 返回状态，`Dedup=true` 表示该项被去重跳过并指向已有节点）。

### `func (m *MemHop) ImportMemory(req ImportRequest) (*ImportResult, error)`

从外部结构化数据导入 L0 / L2 / L3。用于跨会话迁移、备份恢复。导入的 topic 会同步写入 L2 元索引并分配 SceneID，导入后立即可搜。

**ImportRequest 字段：**

| 字段 | 类型 | 说明 |
|---|---|---|
| `TargetLayer` | `TargetLayer` | `TargetProfile` / `TargetTopic` / `TargetKnowledge` |
| `Mode` | `ImportMode` | `ImportMerge` / `ImportOverwrite` / `ImportSkip` |
| `Data` | `ImportData` | 结构化数据（Profile / Topics / Knowledge） |
| `KnowledgeTitle` | `*string` | 导入 topic 时关联的 L3 知识标题（可选） |

**ImportResult 字段：**`Status ImportStatus` + `CreatedIDs / UpdatedIDs []string` + `SkippedCount int` + `Errors []ImportError` 等。

---

## 状态查询（2 个方法）

### `func (m *MemHop) HealthCheck() (*HealthStatus, error)`

返回数据库健康状态。统计各层记录数 + 收集编码器/一致性等问题。

**HealthStatus 字段：**

| 字段 | 类型 | 说明 |
|---|---|---|
| `OK` | `bool` | 是否健康 |
| `DBSizeBytes` | `uint64` | 数据库文件大小 |
| `LayerCounts` | `map[string]int` | 各层记录数（`l0_profile` / `l1_engram` / `l2_topic` / `l3_knowledge` / `l4_archive` / `l5_crystal`） |
| `LastDreamAt` | `*string` | 上次成功 Dream 的时间（RFC3339，进程内记录；从未 Dream 时缺省） |
| `EncoderConfigured` | `bool` | 编码器是否已配置且可用 |
| `Issues` | `[]string` | 问题列表 |

### `func (m *MemHop) SessionStatus() (*SessionStatus, error)`

返回当前会话激活状态：`ActiveTopicIDs []string` + `Count int` + `IsEmpty bool`。

---

## 包级工具

### `func IsDSLQuery(q string) bool`

判断字符串是否是 L3 DSL 查询（`MATCH` / `PATH` / `SUBGRAPH` 关键字开头）。用于路由：`true` → 走 `Knowledge(KOpDSL)`，`false` → 走 `Knowledge(KOpSearch)`。

---

## 类型别名与常量（memdb.go / types_ops.go）

memdb.go / types_ops.go 通过类型别名将内部 DTO 暴露给外部调用者。以下按类别列出。

### Config 相关

- **`type Config = config.MemHopConfig`** — 数据库配置（DBPath / VectorDim / EncoderAddr / EmbedModel / LLM / Defaults）
- **`type ConfigDefaults = config.MemHopDefaults`** — 默认配置（SearchWeights / DecayConfig / SessionConfig / AdjacencyCacheMaxEntries / TokenizerEngine）
- **`var DefaultDefaults = config.DefaultMemHopDefaults`** — 返回默认配置的函数

### Error 相关

| 别名 | 原始值 | 说明 |
|---|---|---|
| `ErrIO` | `mherrors.ErrIO` | I/O 错误 |
| `ErrInvalidMagic` | `mherrors.ErrInvalidMagic` | 无效魔数 |
| `ErrCRCMismatch` | `mherrors.ErrCRCMismatch` | CRC 校验不匹配 |
| `ErrCorruption` | `mherrors.ErrCorruption` | 数据损坏 |
| `ErrNotFound` | `mherrors.ErrNotFound` | 未找到 |
| `ErrVectorDimMismatch` | `mherrors.ErrVectorDimMismatch` | 向量维度不匹配 |
| `ErrSerialization` | `mherrors.ErrSerialization` | 序列化错误 |
| `ErrDeserialization` | `mherrors.ErrDeserialization` | 反序列化错误 |
| `ErrEncoder` | `mherrors.ErrEncoder` | 编码器错误 |
| `ErrConfig` | `mherrors.ErrConfig` | 配置错误 |
| `ErrLLM` | `mherrors.ErrLLM` | LLM 错误 |
| `ErrInvalidQuery` | `mherrors.ErrInvalidQuery` | 无效查询 |
| `ErrClosed` | `mherrors.ErrClosed` | 数据库已关闭 |

- **`type Error = mherrors.MemHopError`** — 错误结构体（Kind / Message / Cause）
- **`var NewError = mherrors.NewError`** — 创建 `*Error` 的工厂函数

### v0.60.0 新增 Op / Result 类型

| 类型 | 说明 |
|---|---|
| `Layer` | 层枚举（`uint8`），`LayerProfile` .. `LayerCrystal` |
| `GetResult` | Union，含 `Profile / SceneGraph / Topic / Knowledge / Archive / Crystal` |
| `ListRequest` | Union 查询，含 `Topic / Knowledge / Archive / Crystal` |
| `ListResult` | Union 结果，含 `Topics / Knowledge / Archives / Crystals` |
| `TopicOp` / `TopicOpKind` / `TopicResult` | L0/L2 分域操作 |
| `KnowledgeOp` / `KnowledgeOpKind` / `KnowledgeResult` | L3 分域操作 |
| `CrystalOp` / `CrystalOpKind` / `CrystalResult` | L5 分域操作 |
| `KnowledgeNodesResult` | KOpGetNodes 的返回结构 |

### Search / Query DTO

| 类型别名 | 说明 |
|---|---|
| `SearchQuery` | 搜索查询（Text / Timestamp必填 / MaxResults / DirectedL2ID / DirectedL3ID / AutoCreate） |
| `SearchResult` | 搜索结果（Profile / Contexts / AssociatedContexts / Crystals / NewTopicID） |
| `SearchDefaults` | 搜索默认值（MaxResults / DefaultRRFK / ActivationBonus / RecentChatBonus / MinRelevanceScore） |
| `ContextResult` | 上下文结果 |
| `ProfileResult` | 档案搜索结果项（L0 摘要） |
| `L1Preview` | L1 预览项（Summary / Importance / DominantEmotion / RecallScore） |
| `L3Preview` | L3 预览项（Title / TopNodes / Keywords / NodeCount） |
| `L3SearchQuery` | L3 节点搜索查询 |
| `L3SearchResult` | L3 节点搜索结果 |
| `RequestSource` | 请求来源（SourceAgent / SourcePlatform） |

### CRUD / 图数据 DTO

| 类型别名 | 说明 |
|---|---|
| `TopicListQuery` / `TopicListResult` / `TopicSummary` / `TopicDetail` | L2 topic 列表与详情 |
| `KnowledgeListQuery` / `KnowledgeListResult` | L3 列表 |
| `L3Detail` | L3 详情（Slot / Nodes / Edges） |
| `GraphNode` / `GraphEdge` / `Subgraph` | 图 DTO |
| `MergeResult` | L2 合并结果 |
| `SceneTreeResult` | 场景树结果 |
| `L1Graph` / `L1Node` / `L1Edge` | L1 场景图 |

### L3 社区检测

| 类型别名 | 说明 |
|---|---|
| `CommunityConfig` | 社区检测配置（Resolution / MaxHyperedgeSize） |
| `CommunityResult` | 社区检测结果 |
| `var DefaultCommunityConfig` | 默认社区检测配置（Resolution=1.0, MaxHyperedgeSize=10） |

### L4 / L5 / Profile DTO

| 类型别名 | 说明 |
|---|---|
| `ArchiveQuery` / `ArchiveListResult` / `Archive` | L4 档案 |
| `CrystalListQuery` / `CrystalListResult` / `CrystalSummary` | L5 结晶 |
| `ProfileSlot` | L0 档案槽 |
| `ProfileDelta` | 档案增量更新 |

### 导入 / 批写入 / 更新 DTO

| 类型别名 | 说明 |
|---|---|
| `ImportRequest` / `ImportResult` / `ImportData` / `ImportError` | 导入相关 |
| `ProfileImportData` / `TopicImportItem` / `KnowledgeImportItem` | 导入子结构 |
| `StoreBatch` / `StoreItem` / `StoreResult` | 批写入 |
| `UpdateRequest` | 更新请求（ID / Layer / Fields / Timestamp） |
| `UpdateL2Fields` / `UpdateL3Fields` / `UpdateL5Fields` | 各层字段结构 |
| `L5ChainInput` / `L5StepInput` / `L5ChainUpdate` | L5 输入结构 |
| `ActionItem` | 动作项（Title / Description / ActionType / Parameters） |
| `TargetLayer` / `ImportMode` / `ImportStatus` | 字符串枚举 |

#### Sentinel 常量

```go
var (
    TargetProfile   = write.TargetProfile   // "Profile"
    TargetTopic     = write.TargetTopic     // "Topic"
    TargetKnowledge = write.TargetKnowledge // "Knowledge"

    ImportMerge     = write.ImportMerge     // "merge"
    ImportOverwrite = write.ImportOverwrite // "overwrite"
    ImportSkip      = write.ImportSkip      // "skip"

    ImportSuccess = write.ImportSuccess
)
```

### Model 暴露（底层数据结构）

| 类型别名 | 说明 |
|---|---|
| `HypergraphNode` | L3 超图节点 |
| `HypergraphEdge` | L3 超图边 |
| `HypergraphSlot` | L3 超图谱位元信息 |
| `HypergraphSource` | 超图来源（Kind / Value / ContextID） |
| `SourceKind` | 来源类型（`uint8`） |
| `var SourceManual` | 手动来源标记 |

**GraphEdgeKind** 常量：`EdgeRelated` / `EdgeCausal` / `EdgePartOf` / `EdgeSequence` / `EdgeDependency` / `EdgeCustom`

### Dream / DSL

| 类型别名 | 说明 |
|---|---|
| `DreamOptions` | Dream 参数（LLM / Chat / L2IDs / SkipDistill） |
| `DreamReport` | Dream 巩固报告 |
| `LlmProvider` | LLM 提供者接口（`Consolidate(ctx, input) (*ConsolidationOutput, error)`） |
| `var NewOpenAIProvider` | 创建 OpenAI 兼容 LLM 提供者的工厂函数 |
| `DSLQueryResult` | DSL 查询结果（可选 Nodes / Edges / Hops / Subgraph） |

### Encoder

| 类型别名 | 说明 |
|---|---|
| `Encoder` | 编码器接口（Encode / Dim / Mode / IsAvailable） |
| `EncoderOutput` | 编码器输出（Dense `[]uint16`，f16 半精度） |

### Hash 辅助函数

| 函数 | 说明 |
|---|---|
| `HashID(s string) uint64` | xxhash3 字符串哈希 |
| `FormatHash(h uint64) string` | uint64 格式化为 16 位 hex 字符串（`%016x`） |
| `ParseID(id string) (uint64, error)` | 16 位 hex 字符串解析为 uint64 |

### Health / Status

| 类型别名 | 说明 |
|---|---|
| `HealthStatus` | 健康状态 |
| `SessionStatus` | 会话状态 |

