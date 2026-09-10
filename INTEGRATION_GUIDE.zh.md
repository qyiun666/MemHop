# MemHop 宿主集成指南（Go API 方式）

> 面向直接以 **Go module 内嵌**方式集成 MemHop 的宿主程序（不经 MCP server）。
> 适用版本：**v1.6.3**。模块路径 `github.com/qyiun666/MemHop`，只允许 import `api` 包。

---

## 1. 集成形态

```
宿主进程
 ├─ go.mod: require github.com/qyiun666/MemHop（或 go.work replace → 本地 checkout）
 ├─ 只 import github.com/qyiun666/MemHop/api（禁止碰 internal/）
 ├─ 一个 .meh 文件 = 多个 agent 域（除文件级 L3/L5 公共池外相互隔离），调用一律经 Session(hexID) 定域
 └─ 外部服务依赖：
      └─ 只有一个 OpenAI 兼容 LLM（轮次提炼 / Dream 巩固 / Crystallize）
      └─ 无 embedding / 向量服务
```

### 硬性契约（宿主必须遵守）

| 契约 | 说明 |
|---|---|
| **单实例** | 一个 `.meh` 文件被排他锁独占；同一文件不能开第二个 `OpenMulti`。每次调用都跑在绑定某个 agent 域的 `Session` 上 |
| **串行调用** | 同一 agent 的操作（Search / Update / Dream / 写 API）由库内域级锁串行，跨 agent 在 `*MultiAgentDB` 上并行；宿主无需自行排队。`Lock()`/`Unlock()` 保留给宿主对文件做旁路写入的关键区——只串行化**默认域**，其他域照常运行；对已关闭的 DB 调用 `Lock()` 会 panic（此时 `Unlock()` 为空操作） |
| **LLM 只在写路径** | `Update` / `Dream` / `Crystallize` 会调 LLM，不可用即报错（不降级），`Update` 每轮恰好一次；`Search` 一次都不调——读路径永不被 LLM 拖住 |
| **ID 形态** | 所有对外 ID 均为 16 位小写 hex 字符串（xxhash64）；ID 一律由库发号，宿主按不透明字符串原样回传即可，没有任何进制转换要做。`api.DefaultAgentID` 是隐式域；`Search` 返回的轮次话题 id 就是该轮 L4 内容与 L5 计划树的寻址键。 |
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

### `LlmConfig`（可字面量构造）

| 字段 | 必填 | 内容 |
|---|---|---|
| APIURL | ✅ | OpenAI 兼容端点 URL |
| APIKey | ✅ | API Key（宿主从环境变量注入，禁止硬编码） |
| Model | ✅ | 模型名 |
| TimeoutSecs | 否 | LLM 调用超时秒数 |
| MaxOutputTokens | 否 | 最大输出 token 数 |

### `MemHopDefaults` 常用覆盖项

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

- `api.OpenMulti(cfg)`：唯一入口。引擎**不存储任何能力记录**：能力卡住在宿主自有的能力目录（如 `.meh` 同目录的 `plug/<包>/capability.json`）——宿主自扫该目录、自装配工具面；Open 时不注入任何东西。
- 中途主动落盘：`db.Checkpoint()`。
- 空间回收：`db.CompactTo(newPath)` 写出一份只含存活记录、自带重建索引的整理副本，**绝不碰正打开的文件**——`newPath` 必须还不存在。删除都是打墓碑，删过场景/图的域只在这里把字节还回来；换文件（Close → rename → Open）仍由宿主决定，这也是它留在 Go 侧、不做成 MCP 工具的原因（入参就是一个输出路径）。

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

无 `ctx` 参数（读路径没有任何可取消的 LLM/网络调用），也**没有任何检索开销**：不调 LLM、不做向量编码、不打分，命中走 L2Meta 内存缓存。新场景先由库命名 `session:<id>`，宿主用 `UpdateScene(sceneID, ScenePatch{Name: &name})` 换成人类可读标题——标题不会被后续读取冲掉（`Search` 读改写同一条记录，只推进轮次计数，不动名字）。唯一的写入就是这个计数：`NewTopicID` 由它派生。

**返回值 `SearchResult` 字段：**

| 字段 | 内容 | 宿主用途 |
|---|---|---|
| `Profile` | L0 画像快照（名字/角色/性格/情绪/MBTI/偏好） | 可拼入系统提示词 |
| `ProfileBrief` | 紧凑画像摘要（有界） | 轻量按轮注入；需要时才拉完整 `Profile` |
| `Scene` | 本轮读到的场景本体（含 `SceneID`/`SceneName`/`L3ID`） | 记下 `Scene.SceneID`，Update 与后续读都用它 |
| `Topics` | 该场景的 depth-1 话题集（按用户消息时间升序，每个带 `FusedKeywords`） | **拼进本次 LLM prompt 的记忆**；要看原文，用那一轮的话题 id 寻址 L4：`SearchL4(L4Query{TopicID})` |
| `NewTopicID` | 这次读取为即将进行的这一轮开出的话题 | 交给 `AppendArchive` 与 `Update`——一轮一个 id |

未知 `SceneID` 返回 `ErrNotFound`（库不会替你新建一个你指名要读的场景）；`SceneID` 为空则新建并返回其 id。

### 6.2 轮次进行中：`AppendArchive(topicID, ArchiveSlot)`

这一轮由宿主自己记录，一条记录一次调用，键就是 `Search` 铸出的话题 id。`AppendArchive`
是一条内容进入话题的唯一途径。

```go
err := db.AppendArchive(topicIDHex, api.ArchiveSlot{
    Kind:      api.KindUtterance, // 或 api.KindEvent
    Seq:       1,                 // 0 = 由库分配槽位
    Role:      api.RoleUser,      // 仅原文：RoleUser / RoleAgent / RoleSystem
    ContentType: api.ContentText, // 仅原文；非文本侧把媒体路径/URL 写在 Content 里
    Content:   userRawText,       // 必填
    CreatedAt: userTS,            // Unix 毫秒，必填 > 0
})
// 事件自己命名、不要说话者，并可挂在某个计划步骤上：
err = db.AppendArchive(topicIDHex, api.ArchiveSlot{
    Kind:      api.KindEvent,
    EventType: "tool_call",       // 仅事件；自由字符串，库不做白名单校验
    NodePath:  "1.1",             // 可选：缺失的步骤按 pending 建出来
    Content:   `{"tool":"grep"}`,
    CreatedAt: time.Now().UnixMilli(),
})
```

`IDHash` 与 `TopicID` 写入时被忽略：话题来自调用的键，记录 id 由 (话题, Seq) 派生——
这正是「读回来、改一个字段、写回它原来那个槽位」能成立的原因。`Seq: 0` 在该话题已有
槽位之上分配，并跳过 1 与 2（那两个属于对话）；显式写一个已被占用的 Seq 是**覆写**而
不是报错，跨 Kind 也一样。这就是重放的全部语义：重试的一轮改写自己的槽位而不是叠加
版本。代价也要说清：重放不再去填的槽位不会被回收，一句已撤回的话会留在转录里，直到
`DeleteTopic` 或保留窗到期。

以下全部在任何写入（包括 `NodePath` 建节点）**之前**拒绝：未定义的 `Kind`、空 `Content`、
`CreatedAt <= 0`、未定义的 `ContentType`、事件没有 `EventType`、原文带了 `EventType` 或
`NodePath`、以及值 3 那个角色（融合摘要的标记，库自己盖）；超预算同样拒写不截断——
事件 4 KiB、原文 64 KiB，被剪短的记录读回来和完整的无法区分。

### 6.3 轮次结束：`Update(sceneID, topicID)`

```go
err := db.Update(sceneIDHex, topicIDHex)
```

一次提炼把该话题已有的原文蒸馏成它的关键词，`Update` 自己不写任何内容：每轮恰好一次
LLM 调用，且排在该轮话题落盘之前，所以失败不会留下半轮话题——而宿主先前 append 的
记录原样待在它写下的地方。场景不存在返回 `ErrNotFound`；`topicID` 不是该场景开出的轮次、
或该话题已无可提炼内容，返回 `ErrInvalidQuery`（后者一次 LLM 都不调用）。同一个话题沉淀
两次只是按它当前的内容重算，因此超时的 `Update` 可以放心重试。

可沉淀的范围由一条规则钉住：`topicID` 必须是**该场景已开出的轮次**（`Search` 为该场景
已计到的某一轮铸出的 id）。写 Dream 融合出的话题、别的场景的轮次、或宿主自己拼的 id 都
被拒，且发生在 LLM 调用之前——拒绝时零留痕。重放当前轮、以及先开两轮再按任意顺序结算
仍然合法：这正是「at-least-once 写循环安全」与「陈旧重试不得改写别轮」的交界线。

### 6.4 巩固：`Dream(ctx, sceneID)`
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

一轮就是**恰好一个话题**：`Search` 为它铸出的那个 id，宿主记录的这一轮的一切都在它下面。对话原文与操作事件是同一类记录、只差一个 `Kind`，所以一轮能装下宿主记下的任意多条，而一次 `Update` 把它们蒸馏成一条关键词轨。

不发生长对话，而以 `Kind: api.KindEvent` 并排存在同一个键下——读的时候各走各的：`SceneContext` 与 `SearchL4(L4Query{TopicID, Kind: &KindUtterance})` 给说了什么，`Kind: &KindEvent` 给做了什么，`Crystallize` 只看事件轨。

**写入侧拥有各轴：** 内容类型（`text`/`image`/`video`/`document`/`audio`/`code`/`other`）与说话者由 `AppendArchive` 逐条声明，读回侧原样报告（`L4Query.Type` 过滤、`ArchiveSlot.ContentType`、`SceneContext` 的 `Messages[].Type`）；未定义的值以 `ErrInvalidQuery` 拒绝而不是落库。Dream 的融合摘要是唯一类型与角色都由库钉死的记录——`text` 与角色 3。一轮恰好一次 LLM 调用（`Update`），关键词永不落后于该话题现有内容。

---

## 8. 各层 API 速查

25 个会话方法按使用者分两类：

- **任务面（18 个）**——宿主每轮驱动、LLM 工具绑定的方法：`Search` / `AppendArchive` / `Update` / `Dream`（宿主自动循环）、`GetL0` / `UpdateL0`、`ListScenes` / `SceneContext`、`GetL3` / `ListL3` / `ImportL3` / `QueryL3Nodes` / `QueryL3Subgraph`、`SearchL4`、`ListTrajectorySessions` / `Crystallize`、`PlanCommit` / `PlanState`。
- **组装/管理面（7 个，外加 `MultiAgentDB` 全部 8 个）**——宿主代码在会话边界与管理通道调用，**不做成 LLM 工具**：`UpdateScene` / `MergeScenes` / `DeleteScene` / `DeleteTopic`、`UpdateL3` / `DeleteL3` / `DeleteL3Nodes`。能力格式整体离开了方法面：`ParseCapabilityPackage` / `ValidateCapabilityCard` 是包级函数（§8 L5）。

### L0 画像

```go
slot, err := db.GetL0()                       // *api.ProfileSlot
err = db.UpdateL0(&api.ProfileSlot{Name: "..."})
```

`UpdateL0` 只写宿主那一半——`Name`、`Role`、`Personality`、`Preferences`：`EmotionState` 与 `MBTI` 由库按画像现值保留（只有 Dream 会演化它们），`UpdatedAtMs` 由库戳写。因此不必先 `GetL0` 再回填，这三项传了也不生效。蒸馏那一半只由 Dream 维护，库不再提供单独的蒸馏入口。

### L2 场景管理

| 方法 | 说明 |
|---|---|
| `db.ListScenes(l3ID) ([]SceneSlot, error)` | 场景列表（`SceneID / SceneName / L3ID`）；`l3ID` 非空时只列挂到该项目域的场景，`""` 列全部 |
| `db.SceneContext(sceneID) (*SceneContext, error)` | 场景全貌（含各话题的 L4 原文），且**完全不写**——不开轮次，**会话恢复用这个**。与 `Search` 的取数差异是刻意的：它平铺到 depth 2，因为 Dream 融合组把原文下沉到了子话题，只有这条路能取回；每条带 `Depth` 与 `ChildCount`，于是一个融合父节点（它的消息是 Dream 的摘要）与它归并的那几轮可分辨。`TopicCount` 计的是本次返回的条目数（根与只有这条路能带回的下沉子条目都算） |
| `db.UpdateScene(sceneID, api.ScenePatch{Name, L3ID, Force}) (SceneSlot, error)` | 一次调用改标题（`Name`）/ 锚定到 L3 项目域（`L3ID`）/ 清除锚定（`L3ID: &""`）；未传的字段保持库里现值，**返回值就是写入后的场景** |
| `db.MergeScenes(primaryID, []secondaryIDs) error` | 场景合并 |
| `db.DeleteTopic(topicID) error` | 删除话题子树 + 其 L4 原文 + 索引，并修剪父话题 `ChildrenIDs`（记忆纠错） |
| `db.DeleteScene(sceneID) error` | 删除场景 + 全部话题/原文 + L1 节点；不存在返回 `ErrNotFound`（记忆纠错） |

改名与锚定是同一次调用：`UpdateScene` 读一次场景、只覆盖你点名的字段、写回一次，
并把写入后的场景回给你——核对归属不必再 `ListScenes` 扫全域。空标题 `ErrInvalidQuery`、未知场景 `ErrNotFound`，
锚定目标必须是已存在的 L3 项目域。锚定默认写一次——把已有**不同**锚定的场景改挂到
别处会被拒（`ErrInvalidQuery`），必须显式 `Force`；清除锚定不需要 `Force`，
因为清除后场景回到未锚定状态，随时可以重新挂。

### L3 知识图谱（稳定事实：人物 / 项目 / 偏好）

图池是**文件级**的：文件内所有 agent 域共享同一份 L3。某个 agent 导入的知识全家可见、可挂锚，删 agent 不删公共池；场景/原文/画像仍按域隔离。

```go
res, err := db.ImportL3([]api.L3ImportItem{{
    Title:    "小明的项目",          // 节点标题，必填
    Domain:   "project",
    NodeType: "project",            // person / project / preference ...
    Content:  "小明正在开发 MemHop",
    Keywords: []string{"小明", "MemHop"},
    SourceRef: "docs/xiaoming.md:1", // 位置引用（可选）
    Related:  []api.L3Relation{{Titles: []string{"小明", "项目"}, Kind: api.EdgePartOf}},
    // 一条关系 = 一条超边，成员是本条目 + titles 里的全部目标（给一个就是二元关系）
}}, api.L3ImportMerge)                 // Skip / Merge / Overwrite
// 返回 GraphIDs / CreatedIDs / UpdatedIDs / SkippedCount / EdgesCreated / Errors
```

`Related` 目标按标题在同图内解析，可在同批条目的后文（两阶段导入）。超边的身份是「成员节点 **+ kind**」，所以同一对节点可以同时挂 `related` 与 `part_of`；重导入同一批不会重复建边（按排序成员 + kind 去重）。无法解析 / 自引用 / 非法 kind 的条目记入 `Errors`。

`GraphIDs` 让这条路闭环：图 id = `hash(Domain)`，公开面上没有任何调用能渲染这个派生，所以 `ImportL3` 直接把它报出来——把场景挂到图上（`SearchQuery.L3ID` / `UpdateScene`）要的正是这个 id。

`GetL3 / ListL3 / QueryL3Nodes / QueryL3Subgraph / UpdateL3 / DeleteL3 / DeleteL3Nodes`。

`QueryL3Nodes` 的条件之间是 **AND**（`IDs` / `Keyword` / `NodeType`），所以只填 `GraphID` 即列出该图全部节点，`Keyword` 忽略大小写——与 L4 的关键词一致。`DeleteL3` 连节点带边整图删除；`DeleteL3Nodes(graphID, nodeIDs)` 只删指定节点并级联触及它们的超边（与其余记忆纠错接口一样仅 Go 侧），改一个错事实不再需要重建整图并丢掉绑定边。id 不是该图的节点即拒绝且什么都不删。

库发出的每个 L3 id 都只对应一种记录：`GetL3(节点 id)`、`UpdateL3(节点 id, …)`、`UpdateScene(scene, ScenePatch{L3ID: &节点 id})` 一律 `ErrNotFound`，不会跨种类读到、更不会写到。

L2↔L3 只有一条关系，握在场景手里：`SceneSlot.L3ID` 把一个会话锚定到某个项目域（多个会话可共享同一张图）。锚点在场景建立时给出（`SearchQuery.L3ID`），也可事后 `UpdateScene`；`ListScenes(l3ID)` 按域取回会话。话题不再携带图谱引用。

### L4 原文检索（查历史原文）

```go
arcs, err := db.SearchL4(api.L4Query{
    Keyword: "关键词",        // 内容子串，忽略大小写
    // Start: t0, End: t1,  // 时间范围（ms）
    // IDs: []string{...},  // 按 ID
    // TopicID: &topicHex,  // 只查该主题的存档
    // NodePath: "1.1",     // 只取归因到该计划步骤的记录（须与 TopicID 同填）
    // Type: &api.ContentImage, // 只查该内容类型
    // Limit: 50,           // 只保留最新 N 条命中（<=0 为全部）
})
```

`ArchiveSlot` 带 `Kind`（原文 / 事件）、`Seq`、`ContentType`（text/image/video/document/audio/code/other）、`Role`（`RoleUser` / `RoleAgent` / `RoleSystem`；库自己那个融合角色不作公开常量）、`TopicID`、`CreatedAt`、`Content`——媒体类型的 `Content` 是路径或 URI，不是二进制。每个查询字段都可选，填了的条件之间是 **AND** 关系——不分「三种模式」——结果按 `Seq` 升序；`NodePath` 只取归因到某个计划步骤的记录（步骤是轮次内的地址，因此必须与 `TopicID` 同填），于是一步做过什么能单独读回，不必先把整轮拉回来。
所以宿主最常用的读取各一次就够：`SearchL4(L4Query{TopicID: &topicID, Kind: &utterance})` 拿这一轮说了什么，换成 `&event` 拿做了什么，`Kind` 不填即两种都要；
`L4Query{IDs: []string{id}}` 取代原来的单条 getter（ID 不存在返回空列表，格式不合法返回 `ErrInvalidQuery`）；
空查询返回该域全部原文——域大了请先加时间范围或 `Limit`，否则这就是文件里的每一条原文。

### 能力（目录即能力——文件归宿主）

引擎**不存储任何能力记录**。能力卡的唯一事实源是宿主自有的能力目录（如 `.meh` 同目录的 `plug/<包>/capability.json`）：宿主自扫自装配、变更重启生效；草稿转正 = 文件转正。库保留格式本身，以包级函数导出：

| 函数 | 说明 |
|---|---|
| `api.ParseCapabilityPackage(data, source)` | 解析 `memhop-capability/v4` 文档（一个文件 = 一个包，1..N 张卡）为 `[]CapabilityImport`，整包校验 |
| `api.ValidateCapabilityCard(card)` | 对单张卡按同一契约校验（名称、摘要、资源、动作链） |

> 一张卡 = 名称 + N 个功能条目（resources，无卡片级 type），每个条目自带启动方式（`type: mcp|skill|api|composite` + `ref`/`config`）/说明（`desc`）/怎么用（`input`/`output`），与宿主工具规格逐字段同构——纯字段拷贝即可投影；composite 条目的动作链放 `config` 的 `{"steps":[{"tool":"...","args":{...}}]}`（每步 `tool` 必填）。面向 LLM 的整块文本用 capability 包的 `PromptCard` 渲染：名称、版本、摘要、触发条件，随后逐资源列启动方式/说明/用法——没有 `id:`/`package:`/`usage:` 三行，卡的身份就是宿主目录里的文件，不是库里的记录。

### 轮内事件 + 结晶

```go
// 每轮的事件是 L4 里 Kind=event 的内容，键就是 Search 返回的 NewTopicID
// （宿主不再自己派生轮键）。
err := db.AppendArchive(turnIDHex, api.ArchiveSlot{
    Kind:      api.KindEvent,
    EventType: "tool_call",   // 给这一步命名：
                              // llm_request / llm_output / tool_call / tool_result /
                              // subagent_spawn / subagent_done / context_inject /
                              // ask_user / user_reply（自由字符串，库不做白名单校验）
    Content:   "工具名+入参摘要", // 4 KiB 预算，超了直接拒
    CreatedAt: time.Now().UnixMilli(),
})
// Seq 与所属话题都由引擎按轮键填好；这一轮开出的计划节点也在同一个键下
// （NodePath 才是把事件绑到某一步的字段，见下面的计划面；裸轮事件留空）。

// 事件 → 能力候选：把一轮的事件轨对照宿主现有卡清单做纯提炼
// （payload 上限 128KB，超限从最旧丢弃）。
res, err := db.Crystallize(ctx, turnIDHex, existingCards)
// existingCards []api.CapabilityImport —— 宿主当前卡目录，从自己的能力
// 目录读出（首次运行为空）。
// res.Capabilities —— []CrystallizeCapability：{Action: "create|reuse|merge",
// ReuseID（已有卡的名称，不是 16-hex id）} + 卡载荷。
// 引擎不落盘：校验、对照目录去重、把草稿写进（如）plug/draft/ 全归宿主
// ——文件转正即激活。

// 轮枚举（如挑选可结晶轮次）。
sessions, err := db.ListTrajectorySessions()
// sessions[i] = TrajectorySessionSummary{SessionID hex（= 该轮话题 id）, Events, LastAppendAt}
```

事件轨用 `SearchL4(L4Query{TopicID: &topicID, Kind: &event})` 按 Seq 序读回。它是**按轮键
整体寻址**的：没有任何调用返回事件句柄，因为公开面上没有读者；Dream 自动清理超出保留窗
的内容。宿主传入的事件里 `EventType` / `NodePath` / `Content` / `CreatedAt` 按原样采用，库
负责 `Seq` 与所属话题，并把 `ContentType` 钉成 text、角色留 0——发生了事，没有谁在说话。
超过 4 KiB 拒写而不是截断：被剪短的事件读回来和完整事件无法区分。

`ListTrajectorySessions` 枚举记了事件的轮次，宿主因此不必记住自己给哪些轮写过日志。


### L5 计划树（Go 宿主面）

该轮的节点由它寻址，同一键下的内容（原文与事件）住在 L4。

| 调用 | 说明 |
|---|---|
| `db.AppendArchive(topicID, ev)`（`ev.NodePath` 非空） | 把步骤事件绑到该节点（节点缺失时按 pending 逐级建链），这也是**追加一步**的入口。`NodePath` 是**点号分隔**（`"1"`、`"1.2.1"`）且**它自己决定树形**：路径上缺失的每一段都会被建成 pending，所以打错一段就会多开一棵树，而 L5 不提供删节点的接口（旧树等所属轮次掉出保留窗由 Dream 回收）。`EventType` **由宿主自定**，与裸轮次事件同口径——引擎不按它分支，只在 `SearchL4` 与结晶 prompt 里原样回显，空值即 `ErrInvalidQuery`。惯例名（给读者的共享词表，不是许可集）：`plan_step`、`llm_request`、`llm_output`、`tool_call`、`tool_result`、`subagent_spawn`、`subagent_done`、`context_inject`、`ask_user`、`user_reply` |
| `db.PlanCommit(topicID, nodePath, ev, api.PlanStep{Title: "调研", Type: "step", Status: api.PlanStatusDone, Summary: s})` | 提交一步：推进节点状态、追加该步事件，`done` 子节点摘要自底向上折叠进父节点（父节点转为 `done` 只由宿主显式提交）。`nodePath` 沿点号路径缺失的节点按 pending 建出来——这就是追加一步的入口。`Title`/`Type`/`Summary` 留空即继承现值；未知 `Status` 在动树之前就被拒 |
| `db.PlanState(topicID)` | 读森林视图（`PlanTree.Roots` + `DoneCount` / `TotalCount`）——重启恢复计划树也走这个 |

`0000000000000000` 是保留值（记录未赋键时的值），L5 的读写入口一律拒绝它。

---

## 9. 导出类型清单（v1.6.3）

| 类别 | 名称 | 用途 |
|---|---|---|
| 配置 | `MemHopConfig` / **`LlmConfig`** / `MemHopDefaults` + `DefaultMemHopDefaults` | 全部装配面 |
| 输入别名 | `SearchQuery` / `ScenePatch` /
/ `ArchiveSlot`（写读两用） | 所有 ID 字段均为 16 位 hex 字符串 |
| ID 面 | **`DefaultAgentID`**（隐式域） | ID 一律由库发号（含轮次话题 id），宿主只回传，不做任何进制转换 |
| 枚举 | `GraphEdgeKind` / `CapabilityType` / `ContentType` / `PlanStatus` | 枚举别名 |

枚举常量同样导出：`L3ImportSkip/Merge/Overwrite`、`CapabilityMCP/Skill/API/Composite`、`EdgeRelated...EdgeCustom`、`ContentText/Image/Video/Document/Audio/Code/Other`。能力格式随记录层退役转为包级面存活：`CapabilityFormatV4` + `ParseCapabilityPackage` / `ValidateCapabilityCard`。

> 记录的 `Kind` 是 `api.KindUtterance` / `api.KindEvent`。L4 的 `role` 是裸 `uint8`，宿主可声明的三个是 `api.RoleUser` / `RoleAgent` / `RoleSystem`；值 3 是库给融合摘要自己盖的标记，刻意不作公开常量、`AppendArchive` 也拒它，所以宿主写不出一个「看起来像被巩固过」的记录。计划状态只有字符串一种编码：`api.PlanStatus*`（`PlanCommit` 入参 / `PlanState` 出参）。

---

## 10. 错误处理

所有错误均携带分类码：`api.CodeOf(err)` 返回数值码（非 MemHop 错误返回 0）。用导出的常量判断：

```go
if api.CodeOf(err) == api.ErrNotFound { ... }
```

错误码：`ErrConfig`、`ErrInvalidQuery`、`ErrNotFound`、`ErrAgentNotFound`（agentID 未注册或已删除）、`ErrIO`、`ErrClosed`、`ErrInvalidMagic`、`ErrCRCMismatch`、`ErrCorruption`、`ErrSerialization`、`ErrDeserialization`、`ErrLLM`。编号永不复用：`1002` 与 `9001` 已退役、不再重新发放。

---

## 11. 最小可运行骨架

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

    // 每轮对话：把说了什么、做了什么写进那个话题 id
    topicID := res.NewTopicID
    userTS := time.Now().UnixMilli()
    _ = db.AppendArchive(topicID, api.ArchiveSlot{Kind: api.KindUtterance, Seq: 1,
        Role: api.RoleUser, Content: "用户消息原文", CreatedAt: userTS})
    _ = db.AppendArchive(topicID, api.ArchiveSlot{Kind: api.KindEvent,
        EventType: "tool_call", Content: "grep ...", CreatedAt: userTS + 1})
    _ = db.AppendArchive(topicID, api.ArchiveSlot{Kind: api.KindUtterance, Seq: 2,
        Role: api.RoleAgent, Content: "Agent 回复原文", CreatedAt: time.Now().UnixMilli()})

    // 每轮对话：结束——把该话题的原文蒸馏成关键词
    if err := db.Update(sceneID, topicID); err != nil { log.Fatal(err) }


    // 空闲/定时（通常无需手动：话题数超阈值时 Update 已后台调度）
    if _, err := db.Dream(context.Background(), ""); err != nil {
        log.Fatal(err)
    }
}
```

---


## 12. 陷阱清单

1. **LLM 只影响 `Update` 与 `Dream`**：`Search` 与 `AppendArchive` 零 LLM，记录与读取永不被 LLM 拖垮；`Update` 每轮一次提炼，失败即报错且不落该轮话题——你先前 append 的记录原样留着。宿主需为收口失败做好重试：重试同一个 `TopicID` 是安全的。
格式版本为 `0x000F`：L3 知识图驻留保留共享域（`core.SharedPoolAgentID`），不跑迁移——`0x000E` 及更早的文件在 Open 时被拒绝：它们的归档把归属话题记在 `context_id` 这个当前记录已没有的键下、记录 id 又派生自 `l1:` / `l4:` 这两个已不存在的命名空间，按当前规则哪一条都指不到东西。
3. **时间戳用 Unix 毫秒**，`<=0` 报 `ErrInvalidQuery`。
4. **ID 是不透明 16 位 hex**：不要自行拼接/截断；响应里的 id 原样回传即可，门面上不再有 hex ⇄ 整数转换函数。
5. **`Search` 不写记忆内容**：它开启一个轮次（场景的轮次计数 +1），但不建任何话题记录——开了没沉淀的轮次不留残渣。想读原文用 `SceneContext` / `SearchL4`。重放一次 append（同 `(话题, Seq)`）是幂等的：记录 id 由那一对派生，重试只会覆盖不会叠加；而重放不再去填的槽位不会被回收。
6. **单文件多 agent 域**：所有租户驻留同一个 `.meh` 文件（`OpenMulti` → `CreateAgent(name)` → `Session(hexID)`），除文件级 L3 公共池外按域完全隔离；旧库（`FormatVersion < 0x000F`）既打不开也不迁移。
7. **内容与计划自动过期**：Dream 清掉 7 天前的话题内容与 7 天前的计划节点（仍在途的树豁免）；显式纠正走 `DeleteTopic` / `DeleteScene`。过了窗的话题只剩关键词轨，`Messages` 读回来是空的或 `Seq` 上有洞——那是合法的终局，不是读取失败。一切都按轮次话题 id 绑定，所以 `Update` 前后都能追加（id 在 `Search` 时已在手），但绝不要自造轮键。
8. **场景 id 由宿主保管，话题 id 由库保管**：`Update` 只接受已存在场景（先 `Search` 得到 `Scene.SceneID`）+ 该次读铸出的话题 id——没开轮就沉淀不了。库不会为一次沉淀自动建场景，也不会在 Dream 里合并场景——合并只走显式 `MergeScenes`，而它会把被并场景连记录删掉，宿主手里的旧 id 随即失效。每次 `Search` 恰好开启一个轮次：读两次只沉淀一次，就是跳掉一个轮次号，空洞不产生成本，且已给出的 id 永不重复。
9. **`SceneDreamTopicThreshold` 默认 24**：用部分字面量构造 `MemHopDefaults` 时该字段为 0，会**禁用**自动巩固——先赋 `*api.DefaultMemHopDefaults` 再覆盖。上下文规模由 Dream 保证有界（压缩后每场景 ≤20），禁用自动巩固就等于让注入无界增长。
