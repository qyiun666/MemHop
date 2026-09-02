# MemHop 宿主集成指南（Go API 方式）

> 面向直接以 **Go module 内嵌**方式集成 MemHop 的宿主程序（不经 MCP server）。
> 适用版本：**v1.5.0**。模块路径 `github.com/qyiun666/MemHop`，只允许 import `api` 包。

---

## 1. 集成形态

```
宿主进程
 ├─ go.mod: require github.com/qyiun666/MemHop（或 go.work replace → 本地 checkout）
 ├─ 只 import github.com/qyiun666/MemHop/api（禁止碰 internal/）
 ├─ 一个 .meh 文件 = 多个隔离 agent 域，调用一律经 Session(hexID) 定域
 └─ 外部服务依赖：
      └─ 只有一个 OpenAI 兼容 LLM（轮次提炼 / Dream 巩固 / Crystallize）
      └─ 无 embedding / 向量服务（v1.5.0 起随检索子系统一并退役）
```

### 硬性契约（宿主必须遵守）

| 契约 | 说明 |
|---|---|
| **单实例** | 一个 `.meh` 文件被排他锁独占；同一文件不能开第二个 `OpenMulti`。每次调用都跑在绑定某个 agent 域的 `Session` 上 |
| **串行调用** | 同一 agent 的操作（Search / Update / Dream / 写 API）由库内域级锁串行，跨 agent 在 `*MultiAgentDB` 上并行；宿主无需自行排队。`Lock()`/`Unlock()` 保留给宿主对文件做旁路写入的关键区——只串行化**默认域**，其他域照常运行；对已关闭的 DB 调用 `Lock()` 会 panic（此时 `Unlock()` 为空操作） |
| **LLM 只在写路径** | `Update` / `Dream` / `Crystallize` 会调 LLM，不可用即报错（不降级），`Update` 每轮恰好一次；`Search` 一次都不调——读路径永不被 LLM 拖住 |
| **ID 形态** | 所有对外 ID 均为 16 位小写 hex 字符串（xxhash64）；宿主按不透明字符串传递，响应里的 id 原样回传即可；`api.FormatID` / `api.ParseID` / `api.FormatAgentID` / `api.ParseAgentID` 覆盖极少数转换场景 |
| **时间戳** | 一律 Unix 毫秒；`<= 0` 视为非法参数（`ErrInvalidQuery`） |

---

## 2. 前置依赖

| 依赖 | 要求 | 示例 |
|---|---|---|
| Go 1.27+ | 构建要求 | — |
| LLM | OpenAI 兼容 API | DeepSeek / OpenAI / 任意兼容端点 |

就这两项。MemHop 不联系任何 embedding / 向量服务，也不需要数据库、缓存或独立服务：一个可写文件路径 + 一个 LLM 端点。

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
| **DBPath** | string | `.meh` 文件路径，不存在自动创建 |
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

`MemHopDefaults` 只暴露三个业务开关。其余调优常量（巩固 prompt 上限、衰减速率、L1 建边阈值）已内部化为 `internal/tuning.go` 包级常量，不再可配置——宿主不应需要调整；如有调整诉求请提 issue。

| 字段 | 默认 | 含义 |
|---|---|---|
| SceneDreamTopicThreshold | 24 | 某场景 depth-1 话题数超过该值时，Update 后台调度该场景 Dream；**0 表示禁用触发** |
| DreamCompressMinTopics | 20 | 场景话题数达到该值才执行压缩 |
| AgentIdleTTLMs | 3600000 | 某 agent 域空闲超过该毫秒数即从内存回收（下次访问按其记录重建）；0 = 关闭回收 |

---

## 5. 打开 / 关闭数据库

```go
cfg := &api.MemHopConfig{
    DBPath: "/data/agent.meh",
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
if err != nil { /* 处理 ErrConfig / ErrInvalidMagic / ErrCorruption */ }
defer dbm.Close() // 写检查点快照 + 释放 mmap/文件锁
```

- `api.OpenMulti(cfg)`：唯一入口。
- Open 成功即自动挂载内置能力卡（只读，不写入 .meh）。
- 中途主动落盘：`db.Checkpoint()`。

---

## 6. 核心记忆循环（每轮对话必走）

宿主按轮次驱动：**轮次开始 Search（读本会话记忆并开启本轮）→ 轮次结束 Update（把整轮沉淀进那次读开出的话题）**；巩固在场景话题数超阈值时由引擎后台调度，宿主也可显式 Dream。**一个 L2 场景 = 宿主的一个会话，一轮 = 一个话题**：读哪个场景由宿主决定，但话题 id 一律由库铸出，宿主不自造标识。

### 6.1 轮次开始：`Search(q)`

```go
res, err := db.Search(api.SearchQuery{
    SceneID:   sceneIDHex,  // 空 = 请库新建场景（宿主会话首次进入）；非空必须已存在
    L3ID:      graphIDHex,  // 可选：仅在新建场景时挂到某个 L3 项目域
})
```

无 `ctx` 参数（读路径没有任何可取消的 LLM/网络调用），也**没有任何检索开销**：不调 LLM、不做向量编码、不打分，命中走 L2Meta 内存缓存。新场景先由库命名 `session:<id>`，宿主用 `SetSceneName(sceneID, name)` 换成人类可读标题——标题不会被后续读取冲掉（`Search` 读改写同一条记录时只动计数，不动名字）。唯一写的是场景记录：命中计数（喂 Dream 的重要性反馈）与轮次计数——后者就是 `NewTopicID` 的来源。

**返回值 `SearchResult` 字段：**

| 字段 | 内容 | 宿主用途 |
|---|---|---|
| `Profile` | L0 画像快照（名字/角色/性格/情绪/MBTI/偏好） | 可拼入系统提示词 |
| `ProfileBrief` | 紧凑画像摘要（有界） | 轻量按轮注入；需要时才拉完整 `Profile` |
| `Scene` | 本轮读到的场景本体（含 `SceneID`/`SceneName`/`L3ID`/`TopicCount`） | 记下 `Scene.SceneID`，Update 与后续读都用它 |
| `Topics` | 该场景的 depth-1 话题集（按用户消息时间升序，每个带 `FusedKeywords` 与 `L4Refs`） | **拼进本次 LLM prompt 的记忆**；要看原文按 `L4Refs` 走 `GetArchive`/`SearchL4` |
| `NewTopicID` | 这次读取为即将进行的这一轮开出的话题 | 交给 `Update` 与 `AppendTrajectory`——一轮一个 id |

未知 `SceneID` 返回 `ErrNotFound`（库不会替你新建一个你指名要读的场景）；`SceneID` 为空则新建并返回其 id。

### 6.2 轮次结束：`Update(TurnUpdate)`

```go
topicID, err := db.Update(api.TurnUpdate{
    SceneID:   sceneIDHex,             // 必须是已存在场景（先 Search 拿到）
    TopicID:   topicIDHex,             // 必填：Search 返回的 NewTopicID，本轮就落在这里
    UserText:  userRawText,             // 用户原文，必填；非文本侧把媒体路径/URL 写在这里
    UserTS:    userTS,                  // Unix 毫秒，必填 > 0
    UserType:  api.ContentText,         // 可选：image/video/document/audio/code/other
    AgentText: agentReplyText,          // agent 原文，必填
    AgentTS:   agentTS,                 // 不早于 UserTS
    AgentType: api.ContentText,         // 可选，取值同 UserType
})
```

两个类型字段的零值就是 `ContentText`，不填的宿主与上一版记录结果完全一致。`Update` 是引擎唯一的 L4 写入口，因此一轮的内容类型只在这里声明；读回走 `ArchiveSlot.ContentType`、`L4Query.Type` 过滤，或 `SceneContext` 的 `Messages[].Type`。

一次调用把整轮落进那个话题：两条 L4 原文档案（`RoleUser` + `RoleAgent`）+ 一次 LLM 提炼出的话题关键词，返回值就是传进去的 16 位 hex id。同一个 `TopicID` 沉淀两次是**覆盖**而不是新增（档案 id 由话题 id 与原文哈希得出），所以超时的 `Update` 可以放心重试。

要点：**提炼排在所有写入之前**——LLM 调用失败或提炼不出关键词时整次调用报错且零写入，不会留下半轮记忆。场景不存在返回 `ErrNotFound`，双文本/双时间戳非法返回 `ErrInvalidQuery`。

### 6.3 巩固：`Dream(ctx, sceneID)`

```go
rep, err := db.Dream(ctx, "")       // sceneID 传 "" = 遍历域内全部场景
// 或 db.Dream(ctx, sceneIDHex)     // 只巩固指定场景
```

通常**不需要宿主调用**：某场景 depth-1 话题数超过 `Defaults.SceneDreamTopicThreshold`（默认 24）时，`Update` 会在后台调度该场景的巩固（同场景在途不重复调度）。

执行 L2→L1→L0 压缩 / 衰减 / 画像蒸馏（多次 LLM 调用，耗时较长）——放后台 goroutine 或对话间隔执行。
返回结构化 `*DreamReport` 供宿主观测：`ConsolidatedScenes / L2TopicsCompressed / L1NodesAdded|Removed / L1EdgesAdded|Removed / L0Updated`，外加 `Stages []DreamStage{Name, Status, DurationMs}`（状态取值 `ok | skipped | cancelled | error`）。空报告表示无内容可巩固，不算错误；管线中途失败时部分填充的报告随错误一起返回。压缩后每个场景的 depth-1 话题数受 `Consolidate` 约束（≤20），这就是宿主读回上下文的规模上界。

---

## 7. 一轮 = 一个话题

一轮就是一条用户消息加一条回复，对应**恰好一个话题**。`Update` 把这两条原文写成两条 L4 档案，所以一轮的原文永远可以从话题的 `L4Refs` 取回——L4 里不再有别的东西。

两条消息**之间**发生的事（工具调用、中间输出、子 agent 结果）属于执行过程而不是对话内容，归本轮的 L6 轨迹：用同一个话题 id 走 `AppendTrajectory(topicID, …)`（见 §8 L6）。这就是原来 N:N 追加路径的去向。

**v1.5.0 移除：** `AppendL4Message`（往已沉淀的话题继续追加消息）与 `RefineTopicKeywords`（按全量原文重算该话题关键词）。它们让「一轮要提炼几次」变成宿主的判断题；现在一轮恰好一次 LLM 调用，关键词也永不落后于本轮原文。L4 内容类型（`text`/`document`/`code`/`image`/`audio`/`video`）在**读侧**继续有效——`L4Query.Type` 过滤与 `ArchiveSlot.ContentType` 照样如实回报记录内容——只是引擎当前写进去的都是 `text`。

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
| `db.SetSceneName(sceneID, name) error` | 给场景起人类可读标题（空名 `ErrInvalidQuery`、未知场景 `ErrNotFound`；后续读取不会覆盖） |
| `db.SetSceneL3ID(sceneID, l3ID, force) error` | 把场景锚定到 L3 项目域——默认写一次，`force` 才纠正，空 `l3ID` 清除 |
| `db.MergeScenes(primaryID, []secondaryIDs) error` | 场景合并 |
| `db.ListScenesByL3(l3ID) []SceneSlot` | 列出挂到某个 L3 项目域的场景（= 宿主会话） |
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

`GetL3 / ListL3 / QueryL3Nodes / QueryL3Subgraph / UpdateL3 / DeleteL3`。

L2↔L3 只有一条关系，握在场景手里：`SceneSlot.L3ID` 把一个会话锚定到某个项目域（多个会话可共享同一张图）。锚点在场景建立时给出（`SearchQuery.L3ID`），也可事后 `SetSceneL3ID`；`ListScenesByL3` 按域取回会话。话题不再携带图谱引用。

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
// 每轮一条轨迹：轮 ID 就是 Search 返回的 NewTopicID（宿主不再自己派生轮键）。
err := db.AppendTrajectory(turnIDHex, api.TrajectorySlot{
    EventType: "tool_call",   // 分类轮内每一步：
                              // llm_request / llm_output / tool_call / tool_result /
                              // subagent_spawn / subagent_done / context_inject /
                              // ask_user / user_reply（自由字符串，库不做白名单校验）
    Payload:   "工具名+入参摘要", // 超 4KB 自动截断
    Timestamp: time.Now().UnixMilli(),
})
// Seq / SessionID / TopicID 都由引擎按轮键填好，宿主不要自己填

// L6 → L5：把一轮的轨迹沉淀为能力草稿（payload 上限 128KB，超限从最旧丢弃）。
// 传计划 id 而不是话题 id，就把整棵计划树绑定事件一起结晶。
res, err := db.Crystallize(ctx, turnIDHex)
// res.CreatedIDs / ReusedIDs / MergedIDs / Errors
// res.Details — 逐候选处置明细：[]CrystallizeDetail{
//   {Name, Action: "create|reuse|merge|skip", CapabilityID, Reason}}
// 草稿随后用 ActivateCapability 激活

// 轮枚举（如挑选可结晶轮次）。
sessions, err := db.ListTrajectorySessions()
// sessions[i] = TrajectorySessionSummary{SessionID hex（= 该轮话题 id）, Steps, LastAppendAt}
```

`ReadTrajectory(turnID)` 按 Seq 序读全部事件。保留期由库内管理：Dream 自动清理 7 天前的事件（L6 是过程索引，持久产物在 L4/L5），无删除接口。

---

## 9. 导出类型清单（v1.5.0）

| 类别 | 名称 | 用途 |
|---|---|---|
| 配置 | `MemHopConfig` / **`LlmConfig`** / `MemHopDefaults` + `DefaultMemHopDefaults` | 全部装配面 |
| 输入别名 | `SearchQuery` / `TurnUpdate` / `L3ImportItem` / `L3Relation` / `L3ImportMode` / `L3ImportResult` / `L3NodeQuery` / `L4Query` / `CapabilityListQuery` / `CapabilityPatch` / `CapabilityImport` / `SceneContext` / `SceneMessage` / `TrajectorySessionSummary` / `CrystallizeResult` / `CrystallizeDetail` / `DreamReport` / `DreamStage` / `ResourceRef` / `Workflow` | 输入与无 ID 结果（string ID 均为 hex） |
| 响应 DTO | `ProfileSlot` / `SceneSlot` / `TopicSlot` / `SearchResult` / `HypergraphSlot` / `HypergraphNode` / `HypergraphEdge` / `HypergraphSource` / `L3Graph` / `L3Subgraph` / `ArchiveSlot` / `Capability` / `TrajectorySlot` | 所有 ID 字段均为 16 位 hex 字符串（v1.4.1 起） |
| ID 工具 | **`FormatID`** / **`ParseID`** / **`FormatAgentID`** / **`ParseAgentID`**（v1.4.1 新增） | 极少数场景的 hex ⇄ uint64 转换 |
| 枚举 | `GraphEdgeKind` / `CapabilityType` / `CapabilityStatus` / `CapabilityOrigin` / `ContentType` / `PlanStatus` | 枚举别名 |

枚举常量同样导出：`L3ImportSkip/Merge/Overwrite`、`CapabilityMCP/Skill/API/Composite`、`CapabilityDraft/Active/Deprecated`、`CapabilityOrigin*`、`EdgeRelated...EdgeCustom`、`ContentText/Image/Video/Document/Audio/Code/Other`。

> L4 的 `role` 是裸 `uint8`，导出常量为 `api.RoleUser` / `RoleAgent` / `RoleSystem` / `RoleDream`（值 0-3）。计划树用 `api.NodeTypeEvent` / `NodeTypePlan`、读侧数值码 `api.Status*` 与写侧字符串值 `api.PlanStatus*`。

---

## 10. 错误处理

所有错误均携带分类码：`api.CodeOf(err)` 返回数值码（非 MemHop 错误返回 0）。用导出的常量判断：

```go
if api.CodeOf(err) == api.ErrNotFound { ... }
```

错误码：`ErrConfig`、`ErrInvalidQuery`、`ErrNotFound`、`ErrAgentNotFound`（agentID 未注册或已删除）、`ErrIO`、`ErrClosed`、`ErrInvalidMagic`、`ErrCRCMismatch`、`ErrCorruption`、`ErrSerialization`、`ErrDeserialization`、`ErrLLM`。编号永不复用：`1002`（向量维度不匹配）与 `9001`（编码器）已随检索子系统一并退役。

---

## 11. 最小可运行骨架（v1.5.0 签名）

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
        DBPath: os.Getenv("MEH_PATH"), // /data/agent.meh
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

    // 一个宿主会话 = 一个场景。首次进入用空 SceneID 让库建场景。
    opened, err := db.Search(api.SearchQuery{})
    if err != nil { log.Fatal(err) }
    sceneID := opened.Scene.SceneID

    // 每轮对话：开始——读本会话记忆（零 LLM），拼进 prompt，并记下本轮话题
    res, err := db.Search(api.SearchQuery{SceneID: sceneID})
    if err != nil { log.Fatal(err) }
    _ = res // Profile/ProfileBrief + Topics（每话题 FusedKeywords）→ 拼进 prompt

    // 每轮对话：结束——整轮沉淀进那次读开出的话题
    userTS := time.Now().UnixMilli()
    topicID, err := db.Update(api.TurnUpdate{
        SceneID:   sceneID,
        TopicID:   res.NewTopicID,
        UserText:  "用户消息原文",
        UserTS:    userTS,
        AgentText: "Agent 回复原文",
        AgentTS:   time.Now().UnixMilli(),
    })
    if err != nil { log.Fatal(err) }
    // 本轮中间发生的一切都挂在同一个话题 id 上：
    _ = db.AppendTrajectory(topicID, api.TrajectorySlot{
        EventType: "tool_call", Payload: "grep ...", Timestamp: userTS + 1,
    })

    // 空闲/定时（通常无需手动：话题数超阈值时 Update 已后台调度）
    if _, err := db.Dream(context.Background(), ""); err != nil {
        log.Fatal(err)
    }
}
```

---


## 12. 陷阱清单

1. **LLM 只影响写路径**：`Search` 零 LLM，读永不被 LLM 拖垮；`Update` 每轮一次提炼，失败即报错且零写入（不会留半轮记忆）。宿主需为沉淀失败做好重试——重试同一个 `TopicID` 是安全的。
2. **没有 embedding 服务，也没有维度要声明**：文件头偏移 6 的两字节在 v1.5.0 前存向量维度，现在是保留位——v1.4.x 写的库照样打开。格式版本仍是 `0x0009`：单轨关键词在解码点归一，不跑迁移也不需要；`FormatVersion < 0x0009` 的旧库依旧拒绝。
3. **时间戳用 Unix 毫秒**，`<=0` 报 `ErrInvalidQuery`。
4. **ID 是不透明 16 位 hex**：不要自行拼接/截断；响应里的 id 原样回传即可，`api.FormatID` / `api.ParseID` 覆盖极少数转换。
5. **`Search` 不写记忆内容**：它开启一个轮次（场景的命中计数与轮次计数各 +1），但不建任何话题记录——开了没沉淀的轮次不留残渣。想读原文用 `SceneContext` / `SearchL4` / `GetArchive`。重放同一个 `Update`（同 `TopicID`）是幂等的：话题就是那个 id，档案 id 由它派生，重试只会覆盖不会叠加。
6. **单文件多 agent 域**：v1.4 起所有租户驻留同一个 `.meh` 文件（`OpenMulti` → `CreateAgent(name)` → `Session(hexID)`），按域完全隔离；旧库（`FormatVersion < 0x0009`）无法打开、不做迁移。
7. **内置能力卡只读**：`UpdateCapability` 对内置卡返回错误。
8. **轨迹自动过期**：Dream 自动清理 7 天前的事件；对外只有追加与查询（`AppendTrajectory` / `ReadTrajectory` / `ListTrajectorySessions`），无删除接口。一轮的轨迹按该轮话题 id 绑定，所以 `Update` 前后都能追加（id 在 `Search` 时已在手），但绝不要自造轮键。
9. **场景 id 由宿主保管，话题 id 由库保管**：`Update` 只接受已存在场景（先 `Search` 得到 `Scene.SceneID`）+ 该次读铸出的话题 id——没开轮就沉淀不了。库不会为一次沉淀自动建场景，也不会在 Dream 里合并场景——合并只走显式 `MergeScenes`，而它会把被并场景连记录删掉，宿主手里的旧 id 随即失效。每次 `Search` 恰好开启一个轮次：读两次只沉淀一次，就是跳掉一个轮次号，空洞不产生成本，且已给出的 id 永不重复。
10. **`SceneDreamTopicThreshold` 默认 24**：用部分字面量构造 `MemHopDefaults` 时该字段为 0，会**禁用**自动巩固——先赋 `*api.DefaultMemHopDefaults` 再覆盖。上下文规模由 Dream 保证有界（压缩后每场景 ≤20），禁用自动巩固就等于让注入无界增长。
