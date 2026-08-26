# MemHop 宿主集成指南（Go API 方式）

> 面向直接以 **Go module 内嵌**方式集成 MemHop 的宿主程序（不经 MCP server）。
> 适用版本：**v1.3.3**。模块路径 `github.com/qyiun666/MemHop`，只允许 import `api` 包。

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
| **串行调用** | 同一 `*DB` 上的 Search / Update / Dream / 写 API 必须由宿主串行调用（多 goroutine 时自行排队） |
| **LLM 硬依赖** | Search / Update / RefineTopicKeywords 内部做关键词抽取，LLM 不可用直接报错（不降级） |
| **ID 形态** | 所有对外 ID 均为 16 位小写 hex 字符串（xxhash64）；宿主按不透明字符串传递，uint64 用 `fmt.Sprintf("%016x", n)` 格式化（api 未再导出 ID 工具函数） |
| **时间戳** | 一律 Unix 毫秒；`<= 0` 视为非法参数（`ErrInvalidQuery`） |

---

## 2. 前置依赖

| 依赖 | 要求 | 示例 |
|---|---|---|
| Go 1.26+ | 构建要求 | — |
| Ollama | 运行中的 embedding 服务 | `http://localhost:11434` + `nomic-embed-text`（dim 768） |
| LLM | OpenAI 兼容 API | DeepSeek / OpenAI / 任意兼容端点 |

编码器也可自研：实现 `api.Encoder` 接口（`Encode(text string) ([]float32, error)` + `IsAvailable() bool`），用 `api.OpenWithEncoder` 注入。

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

db, err := api.Open(cfg)
if err != nil { /* 处理 ErrConfig / ErrVectorDimMismatch / ErrCorruption */ }
defer db.Close() // 写检查点快照 + 关闭编码器 + 释放 mmap/文件锁
```

- `api.Open`：默认构建 Ollama HTTP 编码器。
- `api.OpenWithEncoder(cfg, enc)`：注入自研编码器（mock / 本地模型）。
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
| `Profile` | L0 画像快照（名字/角色/性格/偏好/词汇表/风格/情绪模式） | 可拼入系统提示词 |
| `ProfileBrief` | 紧凑画像摘要（名字/角色/主要偏好/风格/情绪，有界） | 轻量按轮注入；仅在需要时拉完整 `Profile` |
| `Contexts` | 命中场景的上下文（`TopicSlot` 列表，深度≤1） | **拼进本次 LLM prompt 的记忆** |
| `AssociatedContexts` | 关联场景的主题（L1 超图扩散激活） | 可选附加记忆 |
| `NewTopicID` | 本轮新建主题 ID（16 位 hex）；0=命中旧主题 | 传给 Update 使用 |

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
ok, err := db.Dream(ctx, "")       // sceneID 传 "" = 巩固全部活跃场景
// 或 db.Dream(ctx, sceneIDHex)     // 只巩固指定场景
// ok=false 表示无内容可巩固，不算错误
```

执行 L2→L1→L0 压缩 / 衰减 / 画像蒸馏（多次 LLM 调用，耗时较长）——放后台 goroutine 或对话间隔执行。

---

## 7. N:N 回合：`AppendL4Message` + `RefineTopicKeywords`

标准轮次是 1:1（一条用户消息 + 一条回复）。用户连发多条、agent 只回一条（或反之）时，每条消息若都走 Search 会各建一个新主题，回合在记忆中断裂。用 `AppendL4Message` 把同回合的多条消息追加到**同一个既有主题**：

```go
// 1. 第一条消息用 Search 建主题（拿到 topicID）。
res, _ := db.Search(ctx, api.SearchQuery{Text: userMsg1, Timestamp: t1})
topicID := fmt.Sprintf("%016x", res.NewTopicID)

// 2. 后续消息追加到同一主题。role 是裸 uint8：
//    0 = 用户，1 = agent，2 = system，3 = dream（>3 拒绝）。
id1, err := db.AppendL4Message(topicID, userMsg2, t2, 0)   // 追加用户原文
id2, err := db.AppendL4Message(topicID, agentMsg, t3, 1)   // 追加 agent 原文

// 3. 归档最后回复（AppendL4Message 或 Update）。
db.Update(topicID, finalReply, t4)

// 4. N:N 收尾：按 L4Refs 全量原文重新提取关键词，追加的消息从此可被关键词检索。
//    ctx 可取消 LLM 调用。
if err := db.RefineTopicKeywords(ctx, topicID); err != nil { /* LLM 失败等 */ }
```

- `AppendL4Message(topicID, text, timestamp, role) (uint64, error)` — 纯存储追加：**不抽关键词、不调 LLM**（LLM 不可用时仍可调用）；新 id 自动追加进主题 L4Refs。返回裸 uint64 id，用 `fmt.Sprintf("%016x", id)` 转 16 位 hex。
- `RefineTopicKeywords(ctx, topicID) error` — 守卫 + 幂等：仅当 `L4Refs > 2` **且** user/agent 关键词轨任一非空时执行，否则 no-op 返回 nil。流程：按 L4Refs 顺序合并全量 L4 原文 → LLM 提取 → 存 `FusedKeywords` 并清空双轨（**保留时间戳**，Dream 压缩依赖）→ 重建 BM25。错误发生在写入前，主题保持原样。

---

## 8. 各层 API 速查

### L0 画像

```go
slot, err := db.GetL0()                       // *api.ProfileSlot
err = db.UpdateL0(&api.ProfileSlot{Name: "..."})
```

日常由 Dream 自动蒸馏，仅强制写入时手动维护。

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
}}, api.L3ImportMerge)                 // Skip / Merge / Overwrite
// 返回 CreatedIDs / UpdatedIDs / SkippedCount / Errors
```

`GetL3 / ListL3 / QueryL3Nodes / QueryL3Subgraph / UpdateL3 / DeleteL3`。Search 检索时自动把匹配的图谱挂到新主题（L3Refs）——这正是 `DirectedL3ID` 限定检索的原理。

### L4 原文检索（查历史原文）

```go
arcs, err := db.SearchL4(api.L4Query{
    Keyword: "关键词",        // 模式1：内容子串
    // Start: t0, End: t1,  // 模式2：时间范围（ms）
    // IDs: []string{...},  // 模式3：按 ID
    // TopicID: &topicHex,  // 附加过滤：只查该主题的存档
})
```

`ArchiveSlot` 字段：`ContentType`（text/image/video/document/audio/code）、`Role`（0=user/1=agent/2=system/3=dream）、`ContextID`、`CreatedAt`、`Content`、`Metadata`。单条读取用 `db.GetArchive(id)`。

### L5 能力卡（宿主把工具/技能登记给 LLM）

| 方法 | 说明 |
|---|---|
| `db.ListCapabilities(CapabilityListQuery{Status, Type, Keyword})` | 列出能力卡 |
| `db.ImportCapability(path)` | 导入 memhop-capability/v2 JSON 文件 |
| `db.GetCapability(id)` / `db.DeleteCapability(id)` | 读 / 删 |
| `db.UpdateCapability(id, CapabilityPatch{...})` | 部分更新（内置卡只读，被拒绝） |
| `db.ActivateCapability(id)` | 草稿 → 激活 |
| `db.RecordCapabilityUsage(id, success)` | 使用后反馈 |

> 内置能力工具箱（19 张：13 张 manual API 说明书 + 6 张 atomic 原子卡）Open 时自动挂载，`ListCapabilities` 直接返回（只读、不落 `.meh`）；manual 卡 `type: "api"`、`ref: "api:MethodName"`，宿主在 `*api.DB` 上直接调用。

### L7 轨迹 + 结晶（v1.2.7 新增能力）

```go
// 记录关键操作（工具调用 / 子任务 / 决策）。
err := db.AppendTrajectory(sessionIDHex, api.TrajectorySlot{
    EventType: "tool_call",   // turn_start / tool_call / tool_result /
                              // subagent_spawn / subagent_done / context_inject /
                              // llm_request / llm_output / turn_end
    Payload:   "工具名+入参摘要", // 超 4KB 自动截断
    // L4Ref: &archiveIDHex,  // 可关联对话存档，避免重复存对话
    Timestamp: time.Now().UnixMilli(),
})
// Seq / SessionID 由引擎自动分配，宿主不要填

// 判断该会话是否值得结晶（事件量 / 工具调用分布 / 最近活跃度）：
stats, err := db.TrajectoryStats(sessionIDHex)
// stats.Steps         — 事件总数
// stats.ToolUsage     — EventType → 计数（map）
// stats.LastAppendAt  — 最后事件时间戳（Unix 毫秒）

// L7 → L5：把轨迹沉淀为能力草稿。
res, err := db.Crystallize(ctx, sessionIDHex)
// res.CreatedIDs / ReusedIDs / MergedIDs / Errors
// res.Details — 逐候选处置明细：[]CrystallizeDetail{
//   {Name, Action: "create|reuse|merge|skip", CapabilityID, Reason}}
// 草稿随后用 ActivateCapability 激活
```

`ReadTrajectory(sessionID)` 按 Seq 序读全部事件；`DeleteTrajectory(sessionID)` 清理（轨迹是短期数据，宿主负责清，MemHop 不自动清）。

---

## 9. 导出类型清单（v1.2.7）

| 别名 | 来源 | 用途 |
|---|---|---|
| `MemHopConfig` / `Encoder` | internal | 配置 + 自研编码器契约 |
| **`LlmConfig`** | internal（v1.2.7 新增导出） | `MemHopConfig.LLM` 可字面量构造 |
| **`MemHopDefaults`** | internal（v1.2.7 新增导出） | 可命名默认配置类型，替代复制 |
| `SearchQuery` / `SearchResult` | internal | 检索输入/输出 |
| `SceneContext` / `SceneSlot` | internal / core | 场景视图 |
| `L3Graph` / `L3ImportItem` / `L3ImportMode` / `L3ImportResult` / `L3NodeQuery` / `L3Subgraph` | internal | 知识图谱 |
| `L4Query` / `ArchiveSlot` | internal / core | 原文检索 |
| `Capability` / `CapabilityListQuery` / `CapabilityPatch` / `CrystallizeResult` / **`CrystallizeDetail`** | core / internal | 能力卡 |
| **`TrajectoryStats`** / `TrajectorySlot` | internal / core | 轨迹 + 统计 |
| **`TopicSlot`** | core（v1.2.7 新增导出） | `SearchResult.Contexts` 元素类型 |
| **`ResourceRef`** | core（v1.2.7 新增导出） | `Capability.Resources` 元素类型 |
| `ProfileSlot` / `HypergraphSlot` / `HypergraphNode` / `GraphEdgeKind` | core | 模型 |

枚举常量同样导出：`L3ImportSkip/Merge/Overwrite`、`CapabilityMCP/Skill/API/Composite`、`CapabilityDraft/Active/Deprecated`、`CapabilityOrigin*`、`EdgeRelated...EdgeCustom`。

> 注意：`Role*` 常量**未**导出——`AppendL4Message` 直接收裸 `uint8`（0=用户，1=agent，2=system，3=dream）。

---

## 10. 错误处理

所有错误均携带分类码：`api.CodeOf(err)` 返回数值码（非 MemHop 错误返回 0）。用导出的常量判断：

```go
if api.CodeOf(err) == api.ErrNotFound { ... }
```

错误码：`ErrConfig`、`ErrVectorDimMismatch`、`ErrInvalidQuery`、`ErrNotFound`、`ErrIO`、`ErrClosed`、`ErrInvalidMagic`、`ErrCRCMismatch`、`ErrCorruption`、`ErrSerialization`、`ErrDeserialization`、`ErrEncoder`、`ErrLLM`。

---

## 11. 最小可运行骨架（v1.2.7 签名）

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "time"

    "github.com/qyiun666/MemHop/api"
)

func main() {
    db, err := api.Open(&api.MemHopConfig{
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
    defer db.Close()

    // 每轮对话：开始
    res, err := db.Search(context.Background(), api.SearchQuery{
        Text:      "用户消息原文",
        Timestamp: time.Now().UnixMilli(),
    })
    if err != nil { log.Fatal(err) }
    _ = res // Profile + Contexts → 拼进 prompt

    // 每轮对话：结束。NewTopicID 是 uint64——先转 16 位 hex
    // （0 表示命中旧主题，从 Contexts 取已有 ID）。
    topicID := fmt.Sprintf("%016x", res.NewTopicID)
    if err := db.Update(topicID, "Agent 回复原文", time.Now().UnixMilli()); err != nil {
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
2. **编码器维度锁死**：`VectorDim` 与模型输出不一致 → Open 失败（`ErrVectorDimMismatch`），且不迁移旧文件。
3. **时间戳用 Unix 毫秒**，`<=0` 报 `ErrInvalidQuery`。
4. **ID 是不透明 16 位 hex**：不要自行拼接/截断；用 `fmt.Sprintf("%016x", n)` 生成，需要校验时用 `strconv`。
5. **Search 是写操作**：不需要新建记忆时，读历史用 `SceneContext` / `SearchL4`。
6. **一个 Agent 一个 DB**：多 Agent = 多 `.meh` 文件，宿主负责映射；MemHop 本身无多租户（那是 MCP server 层的事）。
7. **内置能力卡只读**：`UpdateCapability` 对内置卡返回错误。
8. **轨迹短期数据**：宿主负责按会话清理（`DeleteTrajectory`），MemHop 不自动清。
9. **Capacity 语义（v1.2.7）**：活跃集合无界累积；达到 `Capacity` 时 Update 对最老场景触发 Dream——场景话题数低于 `DreamCompressMinTopics` 时跳过（已预检）。
10. **`SearchDreamContextThreshold` 默认 30**：用部分字面量构造 `MemHopDefaults` 时该字段为 0，会**禁用** Search 触发的 Dream——先赋 `*api.DefaultMemHopDefaults` 再覆盖。
