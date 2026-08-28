# MemHop 宿主集成指南（Go API 方式）

> 面向直接以 **Go module 内嵌**方式集成 MemHop 的宿主程序（不经 MCP server）。
> 适用版本：**v1.3.4**。模块路径 `github.com/qyiun666/MemHop`，只允许 import `api` 包。

---

## 1. 集成形态

```
宿主进程
 ├─ go.mod: require github.com/qyiun666/MemHop（或 go.work replace → 本地 checkout）
 ├─ 只 import github.com/qyiun666/MemHop/api（禁止碰 internal/）
 ├─ 一个 Agent = 一个 *api.DB = 一个 .meh 文件
 └─ 外部服务依赖：
      ├─ Ollama（embedding 编码，原生 HTTP /api/embed，无 SDK）
      └─ OpenAI 兼容 LLM（关键词抽取 / Dream 巩固 / Crystallize）
```

### 硬性契约（宿主必须遵守）

| 契约 | 说明 |
|---|---|
| **单实例** | 一个 `.meh` 文件被排他锁独占；同一文件不能开第二个 `*DB` |
| **串行调用** | 同一 agent 的操作（Search / Update / Dream / 写 API）由库内域级锁串行，跨 agent 在 `*MultiAgentDB` 上并行；宿主无需自行排队。`Lock()`/`Unlock()` 保留给宿主对文件做旁路写入的关键区——只串行化**默认域**，其他域照常运行；对已关闭的 DB 调用 `Lock()` 会 panic（此时 `Unlock()` 为空操作） |
| **LLM 硬依赖** | Search / Update / RefineTopicKeywords 内部做关键词抽取，LLM 不可用直接报错（不降级） |
| **ID 形态** | 所有对外 ID 均为 16 位小写 hex 字符串（xxhash64）；宿主按不透明字符串传递，响应里的 id 原样回传即可；`api.FormatID` / `api.ParseID` / `api.FormatAgentID` / `api.ParseAgentID` 覆盖极少数转换场景 |
| **时间戳** | 一律 Unix 毫秒；`<= 0` 视为非法参数（`ErrInvalidQuery`） |

---

## 2. 前置依赖

| 依赖 | 要求 | 示例 |
|---|---|---|
| Go 1.27+ | 构建要求 | — |
| Ollama | 运行中的 embedding 服务 | `http://localhost:11434` + `nomic-embed-text`（dim 768） |
| LLM | OpenAI 兼容 API | DeepSeek / OpenAI / 任意兼容端点 |

编码器也可自研：实现 `api.Encoder` 接口（`Encode(text string) ([]float32, error)` + `IsAvailable() bool`），用 `api.OpenMultiWithEncoder` 注入。

---

## 3. 引入依赖

```bash
go get github.com/qyiun666/MemHop@latest
```

```go
import "github.com/qyiun666/MemHop/api"
```

宿主所有用到的类型（配置、查询、结果、各层模型、错误码）都是 `api` 包的类型别名，无需其他 import。

---

## 4. 构造配置 `MemHopConfig`

`api.MemHopConfig` 是唯一的组装入口。**加粗 = 必填**（`Validate()` 强制）。

### 顶层字段

| 字段 | 类型 | 内容 / 要求 |
|---|---|---|
| **DBPath** | string | `.meh` 文件路径。不存在自动创建；已存在则校验向量维度 |
| **VectorDim** | int | 向量维度，(0, 65535]。**必须与 Ollama embedding 模型输出维度一致**（不匹配则 Open 失败，且不迁移旧文件） |
| **EncoderAddr** | string | Ollama HTTP 地址，如 `http://localhost:11434` |
| **EmbedModel** | string | embedding 模型名，如 `nomic-embed-text` |
| EncoderTimeoutSecs | int | 编码超时秒数（≥0，0=默认） |
| **LLM** | LlmConfig | 见下 |
| Defaults | MemHopDefaults | 引擎调优参数，推荐 `*api.DefaultMemHopDefaults` 后按需覆盖 |

### `LlmConfig`（v1.2.7 起导出，可字面量构造）

| 字段 | 必填 | 内容 |
|---|---|---|
| APIURL | ✅ | OpenAI 兼容端点 URL |
| APIKey | ✅ | API Key（宿主从环境变量注入，禁止硬编码） |
| Model | ✅ | 模型名 |
| TimeoutSecs | 否 | LLM 调用超时秒数 |
| MaxOutputTokens | 否 | 最大输出 token 数 |

### `MemHopDefaults` 常用覆盖项（v1.2.7 起导出）

`MemHopDefaults` 只暴露三个业务开关。引擎调优常量（RRF k、场景加分、衰减速率、L1 扩散激活限制、评分阈值）已内部化为 `internal/tuning.go` 包级常量，不再可配置——宿主不应需要调整；如有调整诉求请提 issue。

| 字段 | 默认 | 含义 |
|---|---|---|
| Capacity | 7 | 活跃场景数边界：达到后 **Update 会对最老场景触发一次 Dream**（已做可压缩性预检，场景话题不足时跳过） |
| SearchDreamContextThreshold | 30 | Search 返回上下文超过该话题数时触发场景 Dream；**0 表示禁用触发**（部分字面量构造时注意） |
| DreamCompressMinTopics | 20 | 场景话题数达到该值才执行压缩 |

---

## 5. 打开 / 关闭数据库

```go
cfg := &api.MemHopConfig{
    DBPath:      "/data/agent.meh",
    VectorDim:   768,
    EncoderAddr: "http://localhost:11434",
    EmbedModel:  "nomic-embed-text",
    LLM: api.LlmConfig{
        APIURL:          os.Getenv("LLM_URL"),
        APIKey:          os.Getenv("LLM_KEY"),
        Model:           os.Getenv("LLM_MODEL"),
        TimeoutSecs:     60,
        MaxOutputTokens: 8192,
    },
    Defaults: *api.DefaultMemHopDefaults,
}

dbm, err := api.OpenMulti(cfg)
if err != nil { /* 处理 ErrConfig / ErrVectorDimMismatch / ErrCorruption */ }
defer dbm.Close() // 写检查点快照 + 关闭编码器 + 释放 mmap/文件锁
```

- `api.OpenMulti(cfg)`：默认构建 Ollama HTTP 编码器。
- `api.OpenMultiWithEncoder(cfg, enc)`：注入自研编码器（mock / 本地模型）。
- Open 成功即自动挂载内置能力卡（只读，不写入 .meh）。
- 中途主动落盘：`db.Checkpoint()`。

---

## 6. 核心记忆循环（每轮对话必走）

宿主按轮次驱动：**轮次开始 Search（回忆+存储）→ 轮次结束 Update（归档回复）→ 空闲时 Dream（巩固）**。

### 6.1 轮次开始：`Search(ctx, q)`

```go
res, err := db.Search(ctx, api.SearchQuery{
    Text:      userRawText,               // 用户本轮原文，必填
    Timestamp: time.Now().UnixMilli(),    // Unix 毫秒，必填 > 0
    // 可选路由（三选一，默认普通检索）：
    // DirectedL2ID: &sceneIDHex,  // 强制写入指定场景
    // DirectedL3ID: &graphIDHex,  // 仅检索引用了该 L3 图谱的主题
    // AutoCreate:   true,         // 跳过检索，直接新建场景+主题
})
```

`ctx` 可取消 LLM 关键词提取、编码调用与内部触发的 Dream——传入请求级 context。

**返回值 `SearchResult` 字段：**

| 字段 | 内容 | 宿主用途 |
|---|---|---|
| `Profile` | L0 画像快照（名字/角色/性格/情绪/MBTI/偏好） | 可拼入系统提示词 |
| `ProfileBrief` | 紧凑画像摘要（名字/角色/性格/主要偏好/情绪，有界） | 轻量按轮注入；仅在需要时拉完整 `Profile` |
| `Contexts` | 命中场景的上下文（`TopicSlot` 列表，深度≤1） | **拼进本次 LLM prompt 的记忆** |
| `AssociatedContexts` | 关联场景的主题（L1 超图扩散激活） | 可选附加记忆 |
| `NewTopicID` | 本轮新建主题 ID（16 位 hex）；`""`=命中旧主题 | 传给 Update 使用 |

**副作用须知（Search 是写操作，不只读）**：LLM 抽关键词 → 三通道检索（BM25 + f32 向量 + 实体 BK-Tree，RRF 融合）→ 建主题 + 编码器算 centroid 向量 + 写一条 L4 原文存档 + 关联 L3 图谱 + 激活场景 + 场景使用计数（并入场景记录）。编码器不可用直接报错。

### 6.2 轮次结束：`Update`

```go
err := db.Update(topicID, agentReplyText, time.Now().UnixMilli())
// topicID: Search 返回的 NewTopicID，或 Contexts 中已有主题的 ID（16 位 hex）
// 返回 nil 表示已追加并更新索引；topic 不存在返回 ErrNotFound
```

追加一条 `Role=Agent` 的 L4 存档 → 刷新主题关键词与 BM25 索引。内部调 LLM 抽关键词。

### 6.3 空闲时：`Dream(ctx, sceneID)`

```go
rep, err := db.Dream(ctx, "")       // sceneID 传 "" = 巩固全部活跃场景
// 或 db.Dream(ctx, sceneIDHex)     // 只巩固指定场景
```

执行 L2→L1→L0 压缩 / 衰减 / 画像蒸馏（多次 LLM 调用，耗时较长）——放后台 goroutine 或对话间隔执行。
返回结构化 `*DreamReport` 供宿主观测：`ConsolidatedScenes / L2TopicsCompressed / L1NodesAdded|Removed / L1EdgesAdded|Removed / L0Updated`，外加 `Stages []DreamStage{Name, Status, DurationMs}`（状态取值 `ok | skipped | cancelled | error`）。空报告表示无内容可巩固，不算错误；管线中途失败时部分填充的报告随错误一起返回。

---

## 7. N:N 回合：`AppendL4Message` + `RefineTopicKeywords`

标准轮次是 1:1（一条用户消息 + 一条回复）。用户连发多条、agent 只回一条（或反之）时，每条消息若都走 Search 会各建一个新主题，回合在记忆中断裂。用 `AppendL4Message` 把同回合的多条消息追加到**同一个既有主题**：

```go
// 1. 第一条消息用 Search 建主题（拿到 topicID）。
res, _ := db.Search(ctx, api.SearchQuery{Text: userMsg1, Timestamp: t1})
topicID := res.NewTopicID

// 2. 后续消息追加到同一主题。role 是裸 uint8：
//    0 = 用户，1 = agent，2 = system，3 = dream（>3 拒绝）。
//    contentType 选记录类别：文本类（text/document/code）的 Content 存原文；
//    媒体类（image/audio/video）的 Content 存路径或 URI，由宿主解析。
id1, err := db.AppendL4Message(topicID, userMsg2, t2, 0, api.ContentText)
id2, err := db.AppendL4Message(topicID, agentMsg, t3, 1, api.ContentText)
id3, err := db.AppendL4Message(topicID, "img://shot.png", t3+1, 0, api.ContentImage)

// 3. 归档最后回复（AppendL4Message 或 Update）。
db.Update(topicID, finalReply, t4)

// 4. N:N 收尾：按 L4Refs 全量原文重新提取关键词，追加的消息从此可被关键词检索。
//    ctx 可取消 LLM 调用。
if err := db.RefineTopicKeywords(ctx, topicID); err != nil { /* LLM 失败等 */ }
```

- `AppendL4Message(topicID, text, timestamp, role, contentType) (string, error)` — 纯存储追加：**不抽关键词、不调 LLM**（LLM 不可用时仍可调用）；新 id 自动追加进主题 L4Refs。返回新档案的 16 位 hex id。内容类型约定：`text`/`document`/`code` 的 `Content` 存原文；`image`/`audio`/`video` 的 `Content` 存路径或 URI（mime/size/sha256 放 `Metadata`）。未定义值拒绝。
- `RefineTopicKeywords(ctx, topicID) error` — 守卫 + 幂等：仅当 `L4Refs > 2` **且** user/agent 关键词轨任一非空时执行，否则 no-op 返回 nil。流程：按 L4Refs 顺序合并全量 L4 原文 → LLM 提取 → 存 `FusedKeywords` 并清空双轨（**保留时间戳**，Dream 压缩依赖）→ 重建 BM25。错误发生在写入前，主题保持原样。

---

## 8. 各层 API 速查

### L0 画像

```go
slot, err := db.GetL0()                       // *api.ProfileSlot
err = db.UpdateL0(&api.ProfileSlot{Name: "..."})
err = db.DistillL0(ctx)                       // 只跑 Dream 的情感/MBTI 蒸馏阶段
```

日常由 Dream 自动蒸馏，仅强制写入时手动维护；`DistillL0` 是长对话后的轻量刷新入口（域内无画像样本时空转）。

### L2 场景管理

| 方法 | 说明 |
|---|---|
| `db.ListScenes() ([]SceneSlot, error)` | 场景列表（`SceneID / SceneName / TopicCount`） |
| `db.SceneContext(sceneID) (*SceneContext, error)` | 场景全貌（含各主题 L4 消息）——**会话恢复用这个** |
| `db.MergeScenes(primaryID, []secondaryIDs) error` | 场景合并 |
| `db.ActiveSceneIDs() []string` | 当前活跃场景 ID |
| `db.DeleteTopic(topicID) error` | 删除话题子树 + 其 L4 原文 + 索引，并修剪父话题 `ChildrenIDs`（记忆纠错） |
| `db.DeleteScene(sceneID) error` | 删除场景 + 全部话题/原文 + L1 节点；不存在返回 `ErrNotFound`（记忆纠错） |

### L3 知识图谱（稳定事实：人物 / 项目 / 偏好）

```go
res, err := db.ImportL3([]api.L3ImportItem{{
    Title:    "小明的项目",          // 节点标题，必填
    Domain:   "project",
    NodeType: "project",            // person / project / preference ...
    Content:  "小明正在开发 MemHop",
    Keywords: []string{"小明", "MemHop"},
    SourceRef: "docs/xiaoming.md:1", // 位置引用（可选）
    Related:  []api.L3Relation{{Title: "小明", Kind: api.EdgePartOf}}, // 关系边（可选）
}}, api.L3ImportMerge)                 // Skip / Merge / Overwrite
// 返回 CreatedIDs / UpdatedIDs / SkippedCount / EdgesCreated / Errors
```

`Related` 目标按标题在同图内解析，可在同批条目的后文（两阶段导入）；重导
入同一批不会重复建边；无法解析 / 自引用 / 非法 kind 的条目记入 `Errors`。

`GetL3 / ListL3 / QueryL3Nodes / QueryL3Subgraph / UpdateL3 / DeleteL3`。Search 检索时自动把匹配的图谱挂到新主题（L3Refs）——这正是 `DirectedL3ID` 限定检索的原理。

### L4 原文检索（查历史原文）

```go
arcs, err := db.SearchL4(api.L4Query{
    Keyword: "关键词",        // 模式1：内容子串
    // Start: t0, End: t1,  // 模式2：时间范围（ms）
    // IDs: []string{...},  // 模式3：按 ID
    // TopicID: &topicHex,  // 附加过滤：只查该主题的存档
    // Type: &api.ContentImage, // 附加过滤：只查该内容类型
})
```

`ArchiveSlot` 字段：`ContentType`（text/image/video/document/audio/code）、`Role`（0=user/1=agent/2=system/3=dream）、`ContextID`、`CreatedAt`、`Content`、`Metadata`——媒体类型的 `Content` 是路径或 URI，不是二进制。单条读取用 `db.GetArchive(id)`。

### L5 能力卡（宿主把工具/技能登记给 LLM）

| 方法 | 说明 |
|---|---|
| `db.ListCapabilities(CapabilityListQuery{Status, Type, Keyword})` | 列出能力卡 |
| `db.ImportCapability(path)` | 导入 memhop-capability/v3 JSON 文件 |
| `db.GetCapability(id)` / `db.DeleteCapability(id)` | 读 / 删 |
| `db.UpdateCapability(id, CapabilityPatch{...})` | 部分更新（内置卡只读，被拒绝） |
| `db.ActivateCapability(id)` | 草稿 → 激活 |
| `db.RecordCapabilityUsage(id, success)` | 使用后反馈 |

> 内置能力工具箱（6 张英文卡：`memhop-guide` 总纲 + 5 张 LLM 可调用说明书）Open 时自动挂载，`ListCapabilities` 直接返回（只读、不落 `.meh`）；说明书卡 `type: "api"`、`ref: "api:MethodName"`，宿主在门面上直接调用。默认分层注入——只投影一行索引（`id + name + summary + trigger`）+ guide 卡，参数详情按需 `GetCapability(id)` 获取。资源即工具声明（`name/desc/input/output` 与宿主工具规格同构；`input` 为 JSON Schema 字符串），宿主纯字段拷贝即可投影。

### L6 轨迹 + 结晶（v1.2.7 新增能力）

```go
// 每轮一条轨迹：search 开启一轮、update 结束一轮——每轮派生新轮 ID（如 会话ID+轮次 的 hash）。
err := db.AppendTrajectory(turnIDHex, api.TrajectorySlot{
    EventType: "tool_call",   // 分类轮内每一步：
                              // llm_request / llm_output / tool_call / tool_result /
                              // subagent_spawn / subagent_done / context_inject /
                              // ask_user / user_reply（自由字符串，库不做白名单校验）
    Payload:   "工具名+入参摘要", // 超 4KB 自动截断
    // TopicID: topicIDHex,  // 本轮命中的 L2 话题 ID（search 命中或 update 新建）；
                              // 结晶时同话题的跨轮轨迹会合并进同一次 prompt
    Timestamp: time.Now().UnixMilli(),
})
// Seq / SessionID 由引擎自动分配，宿主不要填

// L6 → L5：把轮轨迹沉淀为能力草稿。带 L2 TopicID 的轮会先聚合同话题的
// 兄弟轮（payload 上限 128KB，超限从最旧丢弃）。
res, err := db.Crystallize(ctx, turnIDHex)
// res.CreatedIDs / ReusedIDs / MergedIDs / Errors
// res.Details — 逐候选处置明细：[]CrystallizeDetail{
//   {Name, Action: "create|reuse|merge|skip", CapabilityID, Reason}}
// 草稿随后用 ActivateCapability 激活

// 轮枚举（如挑选可结晶轮次）。
sessions, err := db.ListTrajectorySessions()
// sessions[i] = TrajectorySessionSummary{SessionID hex（每轮一条）, Steps, LastAppendAt}
```

`ReadTrajectory(turnID)` 按 Seq 序读全部事件。保留期由库内管理：Dream 自动清理 7 天前的事件（L6 是过程索引，持久产物在 L4/L5），无删除接口。

---

## 9. 导出类型清单（v1.4.1）

| 类别 | 名称 | 用途 |
|---|---|---|
| 配置 | `MemHopConfig` / `Encoder` / **`LlmConfig`** / `MemHopDefaults` + `DefaultMemHopDefaults` | 配置 + 自研编码器契约 |
| 输入别名 | `SearchQuery` / `L3ImportItem` / `L3Relation` / `L3ImportMode` / `L3ImportResult` / `L3NodeQuery` / `L4Query` / `CapabilityListQuery` / `CapabilityPatch` / `CapabilityImport` / `SceneContext` / `SceneMessage` / `TrajectorySessionSummary` / `CrystallizeResult` / `CrystallizeDetail` / `DreamReport` / `DreamStage` / `ResourceRef` / `Workflow` | 输入与无 ID 结果（string ID 均为 hex） |
| 响应 DTO | `ProfileSlot` / `SceneSlot` / `TopicSlot` / `SearchResult` / `HypergraphSlot` / `HypergraphNode` / `HypergraphEdge` / `HypergraphSource` / `L3Graph` / `L3Subgraph` / `ArchiveSlot` / `Capability` / `TrajectorySlot` | 所有 ID 字段均为 16 位 hex 字符串（v1.4.1 起） |
| ID 工具 | **`FormatID`** / **`ParseID`** / **`FormatAgentID`** / **`ParseAgentID`**（v1.4.1 新增） | 极少数场景的 hex ⇄ uint64 转换 |
| 枚举 | `GraphEdgeKind` / `CapabilityType` / `CapabilityStatus` / `CapabilityOrigin` / `ContentType` | 枚举别名 |

枚举常量同样导出：`L3ImportSkip/Merge/Overwrite`、`CapabilityMCP/Skill/API/Composite`、`CapabilityDraft/Active/Deprecated`、`CapabilityOrigin*`、`EdgeRelated...EdgeCustom`、`ContentText/Image/Video/Document/Audio/Code/Other`。

> 注意：`Role*` 常量**未**导出——`AppendL4Message` 直接收裸 `uint8`（0=用户，1=agent，2=system，3=dream）；内容类型用导出的 `Content*` 常量。

---

## 10. 错误处理

所有错误均携带分类码：`api.CodeOf(err)` 返回数值码（非 MemHop 错误返回 0）。用导出的常量判断：

```go
if api.CodeOf(err) == api.ErrNotFound { ... }
```

错误码：`ErrConfig`、`ErrVectorDimMismatch`、`ErrInvalidQuery`、`ErrNotFound`、`ErrAgentNotFound`（agentID 未注册或已删除）、`ErrIO`、`ErrClosed`、`ErrInvalidMagic`、`ErrCRCMismatch`、`ErrCorruption`、`ErrSerialization`、`ErrDeserialization`、`ErrEncoder`、`ErrLLM`。

---

## 11. 最小可运行骨架（v1.4.1 签名）

```go
package main

import (
    "context"
    "log"
    "os"
    "time"

    "github.com/qyiun666/MemHop/api"
)

func main() {
    dbm, err := api.OpenMulti(&api.MemHopConfig{
        DBPath:      os.Getenv("MEH_PATH"),        // /data/agent.meh
        VectorDim:   768,
        EncoderAddr: os.Getenv("OLLAMA_URL"),      // http://localhost:11434
        EmbedModel:  "nomic-embed-text",
        LLM: api.LlmConfig{
            APIURL: os.Getenv("LLM_URL"),
            APIKey: os.Getenv("LLM_KEY"),
            Model:  os.Getenv("LLM_MODEL"),
        },
        Defaults: *api.DefaultMemHopDefaults,
    })
    if err != nil { log.Fatal(err) }
    defer dbm.Close()

    // Multi-agent is the only mode: bind every call to one agent domain.
    agentID, err := dbm.CreateAgent("guide-agent")
    if err != nil { log.Fatal(err) }
    db, err := dbm.Session(agentID)
    if err != nil { log.Fatal(err) }

    // 每轮对话：开始
    res, err := db.Search(context.Background(), api.SearchQuery{
        Text:      "用户消息原文",
        Timestamp: time.Now().UnixMilli(),
    })
    if err != nil { log.Fatal(err) }
    _ = res // Profile + Contexts → 拼进 prompt

    // 每轮对话：结束。NewTopicID 就是 16 位 hex 主题 ID
    // （"" 表示命中旧主题，从 Contexts 取已有 ID）。
    if err := db.Update(res.NewTopicID, "Agent 回复原文", time.Now().UnixMilli()); err != nil {
        log.Fatal(err)
    }

    // 空闲/定时
    if _, err := db.Dream(context.Background(), ""); err != nil {
        log.Fatal(err)
    }
}
```

---

## 12. 陷阱清单

1. **LLM 挂掉 = 记忆循环挂掉**：Search/Update/RefineTopicKeywords 不降级。集成前先做好 LLM 可用性兜底。
2. **编码器维度锁死**：`VectorDim` 与模型输出不一致 → Open 失败（`ErrVectorDimMismatch`）；不迁移旧文件——`FormatVersion < 0x0009` 的旧库在 Open 时直接拒绝。
3. **时间戳用 Unix 毫秒**，`<=0` 报 `ErrInvalidQuery`。
4. **ID 是不透明 16 位 hex**：不要自行拼接/截断；响应里的 id 原样回传即可，`api.FormatID` / `api.ParseID` 覆盖极少数转换。
5. **Search 是写操作**：不需要新建记忆时，读历史用 `SceneContext` / `SearchL4`。
6. **单文件多 agent 域**：v1.4 起所有租户驻留同一个 `.meh` 文件（`OpenMulti` → `CreateAgent(name)` → `Session(hexID)`），按域完全隔离；旧库（`FormatVersion < 0x0009`）无法打开、不做迁移。
7. **内置能力卡只读**：`UpdateCapability` 对内置卡返回错误。
8. **轨迹自动过期**：Dream 自动清理 7 天前的事件；对外只有追加与查询（`AppendTrajectory` / `ReadTrajectory` / `ListTrajectorySessions`），无删除接口。
9. **Capacity 语义（v1.2.7）**：活跃集合无界累积；达到 `Capacity` 时 Update 对最老场景触发 Dream——场景话题数低于 `DreamCompressMinTopics` 时跳过（已预检）。
10. **`SearchDreamContextThreshold` 默认 30**：用部分字面量构造 `MemHopDefaults` 时该字段为 0，会**禁用** Search 触发的 Dream——先赋 `*api.DefaultMemHopDefaults` 再覆盖。
