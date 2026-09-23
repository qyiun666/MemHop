# MemHop 宿主集成指南（Go API 方式）

> 面向直接以 **Go module 内嵌**方式集成 MemHop 的宿主程序。
> 适用版本：**v1.6.6**。模块路径 `github.com/qyiun666/MemHop`，只允许 import `api` 包。

> 本指南描述的就是当前的面：`api.Open` → `api.DB`，域以句柄持有（`Primary` /
> `SubAgent`，agent id 不越边界），没有能力面，也没有轮次列举——宿主读某一轮做过的事
> 用 `SearchL4{TopicID, Kind: event}`。下面的方法清单与 `api/surface_public_test.go`
> 钉住的一致；`go doc github.com/qyiun666/MemHop/api.Session` 仍是每个方法的权威文本，
> 因为 `internal` 不发布，被内嵌提升的方法只有那一条命令能查到。

---

## 1. 集成形态

```
宿主进程
 ├─ go.mod: require github.com/qyiun666/MemHop（或 go.work replace → 本地 checkout）
 ├─ 只 import github.com/qyiun666/MemHop/api（禁止碰 internal/）
 ├─ 一个 .meh 文件 = 多个 agent 域（除文件级 L3 公共池外相互隔离），每个域都是一个句柄：
 │   DB.Primary() 拿文件被打开所依据的那个域，DB.SubAgent(llm, profile) 按名字建/取一个
 │   ——agent id 从不越过这条边界
 └─ 外部服务依赖：
      └─ 只有一个 OpenAI 兼容 LLM（轮次提炼 / Dream 巩固）
      └─ 无 embedding / 向量服务
```

### 硬性契约（宿主必须遵守）

| 契约 | 说明 |
|---|---|
| **单实例** | 一个 `.meh` 文件被排他锁独占；同一文件开不出第二个 `api.Open`。每次调用都跑在绑定某个 agent 域的 `Session` 上 |
| **串行调用** | 同一 agent 的操作（Search / Update / Dream / 写 API）由库内域级锁串行——LLM 调用也在这把锁里，于是一次慢响应顶住的是它自己那个域，不是整个文件。跨 agent 并行，宿主无需自行排队；宿主没有对文件的旁路写入口，也就没有需要它自己守的关键区 |
| **LLM 只在写路径** | `Update` 与 `Dream` 会调 LLM，不可用即报错（不降级）；`Update` 每轮恰好一次，提炼失败不落该轮话题（轮上已写的记录留着），那一轮保持开着可重试。`Search` 一次都不调——读路径永不被 LLM 拖住 |
| **ID 形态** | 所有对外 ID 均为 16 位小写 hex 字符串（xxhash64）；ID 一律由库发号，宿主按不透明字符串原样回传即可，没有任何进制转换要做。域在这条边界上不以 id 称呼——你握着库给你的那个 `*Session` 就是。`Search` 返回的轮次话题 id 就是该轮 L4 内容与该轮计划树的寻址键。 |
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

## 4. 构造入参 `LlmConfig` / `MemHopDefaults`

`Open` 把端点、调参旋钮与主域画像作为三个独立入参收下，没有整份配置对象可组装。
**加粗 = 必填**（端点在碰文件系统之前就校验）。

### `LlmConfig`（可字面量构造）

| 字段 | 必填 | 内容 |
|---|---|---|
| APIURL | ✅ | OpenAI 兼容端点 URL |
| APIKey | ✅ | API Key（宿主从环境变量注入，禁止硬编码） |
| Model | ✅ | 模型名 |
| TimeoutSecs | 否 | LLM 调用超时秒数 |
| MaxOutputTokens | 否 | 最大输出 token 数 |

### `MemHopDefaults` 常用覆盖项

`MemHopDefaults` 只暴露三个业务开关。其余调优常量住在读它的那一段旁边，不再可配置：L1 的衰减速率与共现相似度下限随 Dream 的阶段放在 `internal/dream`，各次调用的输出预算放在 `internal/cap/llmops`，蒸馏的样本预算放在 `internal/cap/profile`。宿主不应需要调整；如有调整诉求请提 issue。

| 字段 | 默认 | 含义 |
|---|---|---|
| SceneDreamTopicThreshold | 24 | 某场景 depth-1 话题数超过该值时，`Update` 后台调度该场景 Dream；**0 表示禁用触发** |
| DreamCompressMinTopics | 20 | 场景话题数达到该值才执行压缩 |
| AgentIdleTTLMs | 3600000 | 某 agent 域空闲超过该毫秒数即从内存回收（下次访问按其记录重建）；0 = 关闭回收。默认域与共享 L3 域永不回收。唯一重建不出来的是**开着的那一轮**：中途被回收的那个轮次，其剩余写入与收束一律被拒，直到宿主重开一轮——不会静默写到新轮上；代价是那一轮之前已写的记录留在一个从未沉淀的话题下，读面不再点名它。取值要长于你最长的一个轮次，或者直接置 0 |
| ContentRetentionMs | 604800000（7 天） | 一轮的记录（L4 内容与 L5 计划节点）保留多久后被 Dream 清走；0 或更小取库默认。没有「永不过期」的写法：要更长就把窗口调大，而不是关掉清扫。这个窗口量的是墙上时钟而不是轮数：钟比窗口还老的记录（一次回填、一份测试种子）会在第一次真跑起来的 Dream 里被扫掉，连被读到的机会都没有 |

---

## 5. 打开 / 关闭数据库

```go
lib, err := api.Open(
    "/data/agent.meh",     // 路径
    api.LlmConfig{         // LLM 端点：必填，在碰文件系统之前就校验
        APIURL:          os.Getenv("LLM_URL"),
        APIKey:          os.Getenv("LLM_KEY"),
        Model:           os.Getenv("LLM_MODEL"),
        TimeoutSecs:     60,
        MaxOutputTokens: 8192,
    },
    api.DefaultMemHopDefaults, // 调参旋钮；要改就复制一份改
    &api.ProfileInput{Name: "guide", Role: "assistant"}, // 文件还不存在时必填
)
if err != nil { /* 处理 ErrConfig / ErrInvalidQuery / ErrInvalidMagic / ErrCorruption */ }
defer lib.Close() // 写检查点快照 + 释放 mmap/文件锁

// 域以句柄形式交回，不以 id 交回。下文的 `db` 始终是这样一个句柄：
// 一个 *api.Session，本指南里每个业务调用都跑在它上面。
db, err := lib.Primary()                                               // 文件被打开所依据的那个域
worker, err := lib.SubAgent(workerLLM, api.ProfileInput{Name: "worker"}) // 按名字
```

路径由宿主自己限定。`Open` 会创建递给它的那个文件，底下既没有沙箱也没有名字注册表——所以
当一条路径、或某个 worker 文件从它派生出的 agent 名字，是从模型的工具参数里来的，宿主
要先把它解进自己的目录再递进来。`CompactTo` 是同一种入参：一条任意文件写路径。

`api.Open` 是唯一入口，它做什么由「文件在不在」与「主域画像在不在」两件事决定：

| 文件 | 主域画像 | 传入的画像 | 结果 |
|---|---|---|---|
| 在 | 在 | 任意 | **成功，入参不被采纳**——文件自己那份是事实源 |
| 在 | 不在 | 没传 | `ErrConfig` |
| 在 | 不在 | 传了 | 校验后写入 → 成功 |
| 不在 | — | 没传 | `ErrConfig`，**且不留下任何文件** |
| 不在 | — | 传了 | 校验后建文件并播种 → 成功 |

- 主域是隐式的零号域，所以一个文件恰好有一个、也不需要扫描去找。`Primary()` 返回它的
  句柄；**宿主从头到尾看不到任何 agent id**。
- `SubAgent(llm, profile)` 第一次调用建出名为 `profile.Name` 的域，之后每次返回同一个
  ——名字就是域的地址，创建时冻结。`llm` 是该域自己的端点，所以子 agent 可以跑在另一个
  模型上。画像只在域还没有画像时才写，这同时把「崩在两次写之间」的半截域补完。
  `AgentType` 由库盖章而不采信入参：这样建出来的域就是子 agent。
- 两条拒绝都发生在碰文件系统之前，所以被拒的 `Open` 不在宿主的路径上留文件让下一次尝试
  走错分支。
- 中途主动落盘：`lib.Checkpoint()`。
- 空间回收：`lib.CompactTo(newPath)` 写出一份只含存活记录、自带重建索引的整理副本，**绝不碰正打开的文件**——`newPath` 必须还不存在。删除都是打墓碑，删过场景/图的域只在这里把字节还回来；换文件（Close → rename → Open）仍由宿主决定，写到哪儿也是宿主的决定（入参就是一个输出路径）。

---

## 6. 核心记忆循环（每轮对话必走）

宿主按轮次驱动：**轮次开始 Search（读本会话记忆并开启本轮）→ 轮次进行中 AppendArchive（记录做过的事）→ 轮次结束 Update（记下这一轮怎么开、怎么收，并把整轮沉淀进那次读开出的话题，蒸出的关键词轨随调用返回）**；巩固在场景话题数超阈值时由引擎后台调度，宿主也可显式 Dream。**一个 L2 场景 = 宿主的一个会话，一轮 = 一个话题**：这两个 id 都由库自持——Search 续用该域当前场景并在其上开下一轮，宿主跑一个 agent 对一个库时写入侧不点名任何 id、不在调用间携带键；读哪个场景引擎从不猜。

### 6.1 轮次开始：`Search(q)`

```go
res, err := db.Search(api.SearchQuery{
    SceneID:   sceneIDHex,  // 空 = 续用该域当前场景（重开文件后恢复轮次计数跑得最远的那个）；
                          // 非空 = 定到那个场景，必须已存在
    NewScene:  false,       // true = 另开一个新场景——同一域上另起一条会话的唯一写法
    L3ID:      graphIDHex,  // 给场景挂 L3 项目域，只被「这一读会新建场景」的两条路径采纳
                          // （NewScene、该域还没有场景的第一读）；续用一条会话时递来即拒
                          // （ErrInvalidQuery），不是忽略
})
```

无 `ctx` 参数（读路径没有任何可取消的 LLM/网络调用），也**没有任何检索开销**：不调 LLM、不做向量编码、不打分，命中走 L2Meta 内存缓存。新场景先由库命名 `session:<id>`，宿主用 `UpdateScene(sceneID, ScenePatch{Name: &name})` 换成人类可读标题——标题不会被后续读取冲掉（`Search` 读改写同一条记录，只推进轮次计数，不动名字）。唯一的写入就是这个计数：`NewTopicID` 由它派生。

**返回值 `SearchResult` 字段：**

| 字段 | 内容 | 宿主用途 |
|---|---|---|
| `Profile` | L0 画像快照（名字/角色/性格/情绪/MBTI/偏好） | 可拼入系统提示词 |
| `ProfileBrief` | 紧凑画像摘要（有界） | 轻量按轮注入；需要时才拉完整 `Profile` |
| `Scene` | 本轮读到的场景本体（含 `SceneID`/`SceneName`/`L3ID`） | 只供读侧与纠错侧——`SceneContext`、`UpdateScene`、`MergeScenes` 用它；写入路径不需要它，库自持当前场景 |
| `Topics` | 该场景的 depth-1 话题集（按用户消息时间升序，每个带 `FusedKeywords`） | **拼进本次 LLM prompt 的记忆**；要看原文，用那一轮的话题 id 寻址 L4：`SearchL4(L4Query{TopicID})` |
| `NewTopicID` | 这次读取为即将进行的这一轮开出的话题 | 这一轮内容的读/纠错键（`SearchL4{TopicID}`、`DeleteTopic`）；写入调用（`AppendArchive`、`Update`、计划族）**不**收它——库自持开着的这一轮 |

未知 `SceneID` 返回 `ErrNotFound`（库不会替你新建一个你指名要读的场景）；`SceneID` 为空则续用该域当前场景，只有它一个场景都没有时才新建。`NewScene: true` 跳过这一切，直接开一个新场景。

### 6.2 轮次进行中：`AppendArchive(ArchiveInput)`

这一轮由宿主自己记录，一条记录一次调用，写进 `Search` 开着的这一轮——是哪一轮由库自持，
所以这个调用不点名任何 id，写的东西也落不到宿主没在做的轮上、或某个任何读取都列不出的孤儿键
下。没有开着的轮时这次调用被拒（`ErrInvalidQuery`，消息含 `no turn is open`）。这一轮说了
什么（问与答）由收口的 `Update` 写，`AppendArchive` 负责其间发生的事。`AppendArchive` 返回
这条记录占用的槽位（`Seq`），一条内容进入话题的唯一途径就是它。

```go
seq, err := db.AppendArchive(api.ArchiveInput{
    Kind:      api.KindUtterance, // 或 api.KindEvent
    Seq:       0,                 // 0 = 由库分配槽位（Seq 1、2 属于 Update 写的对话，故分配跳过它们）；
                                  // 拿到的槽位就是返回值
    Role:      api.RoleUser,      // 仅原文：RoleUser / RoleAgent / RoleSystem
    ContentType: api.ContentText, // 仅原文；非文本侧把媒体路径/URL 写在 Content 里
    Content:   userRawText,       // 必填
    CreatedAt: userTS,            // Unix 毫秒——秒级与微秒级那两段值一律拒
})
// 事件自己命名、不要说话者，并可挂在某个计划步骤上：
seq, err = db.AppendArchive(api.ArchiveInput{
    Kind:      api.KindEvent,
    EventType: "tool_call",       // 仅事件；自由字符串，库不做白名单校验
    NodeSeq:   2,                 // 可选：本轮 PlanNodeAdd 发回的步骤序号
                                  // （0 = 这条事件不绑任何步骤）
    Content:   `{"tool":"grep"}`,
    CreatedAt: time.Now().UnixMilli(),
})
```

`ID` 与 `TopicID` 写入时被忽略：话题就是 `Search` 开着的这一轮，记录 id 由 (话题, Seq) 派生——
这正是「读回来、改一个字段、写回它原来那个槽位」能成立的原因。`Seq: 0` 在该话题已有
槽位之上分配，并跳过 1 与 2（那两个属于对话）；分配是库在替宿主挑格子，所以它会先确认
那一格是空的——那一格的记录读不回来时这次写入被拒并带出该读自己的错误码，而不是覆掉它。
显式写一个已被占用的 Seq 是**覆写**而
不是报错，跨 Kind 也一样。这就是重放的全部语义：重试的一轮改写自己的槽位而不是叠加
版本。代价也要说清：重放不再去填的槽位不会被回收，一句已撤回的话会留在转录里，直到
`DeleteTopic` 或保留窗到期。

以下全部在任何写入（append 从不建计划步骤——建步骤只有 `PlanNodeAdd`，parentSeq 0 即开出本轮的树）
**之前**拒绝：未定义的 `Kind`、空 `Content`、`CreatedAt <= 0` 或落在秒级 / 微秒级那两段
（单位是 Unix 毫秒：秒级的记录一落盘就老于保留窗，下一次巩固会把整轮转录扫走；微秒级的
永不过期）、未定义的 `ContentType`、
事件没有 `EventType`、原文带了 `EventType` 或 `NodeSeq`、事件的 `NodeSeq` 指向本轮计划
从未建出的步骤（`ErrInvalidQuery`，且什么都不落库）、以及值 3 那个角色（融合摘要的标记，
库自己盖）；超预算同样拒写不截断——事件整条 4 KiB（名字与正文合计）、原文 64 KiB，被剪短的记录读回来和完整的
无法区分。

### 6.3 轮次结束：`Update(TurnEnd)`

```go
topic, err := db.Update(api.TurnEnd{
    Input:     userRawText,   // 落到用户对话槽（Seq 1）
    Output:    agentReply,    // 落到 Agent 对话槽（Seq 2）
    Outcome:   "已解决",       // 作为一条 `turn_outcome` 事件按本次调用追加
    CreatedAt: time.Now().UnixMilli(), // Unix 毫秒——由宿主提供
})
// topic.FusedKeywords 就是这一轮被蒸出的关键词轨——不必再读一次
```

`Update` 收口 `Search` 开着的这一轮，并在同一次调用里沉淀它：`Input` 与 `Output` 落到读者
会去找的那两个对话槽（所以重收同一轮是原地覆写这两行，而不是叠加版本），`Outcome` 按调用
次数追加一条 `turn_outcome` 事件（一次挂起加一次恢复是两条事实，不是一行写两遍）。空的字段
什么都不写；三条全空即 `ErrInvalidQuery`。每条都和 `AppendArchive` 过同一道
`content.ValidateAppend` 预算校验，且在任何落盘之前整批校验。

随后它读该话题已有的原文，跑恰好一次 LLM 提炼，排在话题落盘之前——所以失败或空结果不会留下
半轮话题，而轮上已有的记录（宿主 append 的，以及 `Update` 自己那三条）原样留着。一次失败的
调用把那一轮保持开着，宿主可以再收一次；重放是改写那两个对话槽而不是叠加。原文已被保留窗裁光
的轮次被拒（`ErrInvalidQuery`），一次 LLM 都不调；没有开着的轮时同样 `ErrInvalidQuery`，消息
含 `no turn is open`。

`Search` 与 `Update` 是成对出现的一轮：一轮 = 一次 `Search` + 一次（可重放的）`Update`。是哪
一场景、哪一轮都由库自持，这个调用不点名 id；而引擎会复核它所收口的确是该场景真开出过的某一
轮，所以一次删除或合并之后留下的陈旧重试会在 LLM 调用之前被拒、零留痕——这正是「at-least-once
写循环安全」而「陈旧重试不得改写已收口的轮」的交界线。

### 6.4 巩固：`Dream(ctx, sceneID)`

```go
rep, err := db.Dream(ctx, "")       // sceneID 传 "" = 遍历域内全部场景
// 或 db.Dream(ctx, sceneIDHex)     // 只巩固指定场景
```

通常**不需要宿主调用**：某场景 depth-1 话题数超过 `Defaults.SceneDreamTopicThreshold`（默认 24）时，`Update` 会在后台调度该场景的巩固（同场景在途不重复调度）。

执行 L2→L1→L0 压缩 / 衰减 / 画像蒸馏（多次 LLM 调用，耗时较长）——放后台 goroutine 或对话间隔执行。
返回结构化 `*DreamReport` 供宿主观测：`ConsolidatedScenes / L2TopicsCompressed / L1NodesAdded|Removed / L1EdgesAdded|Removed / L0Updated`，外加 `Stages []DreamStage{Name, Status, DurationMs}`（状态取值 `ok | skipped | cancelled | error`）。其中三个数容易被读错：`L2TopicsCompressed` 数的是**沉进融合组的话题**，不是组数；`L1NodesAdded` 数的是同步这一步**写过**的场景节点，含只因话题集变了而被回戳的既有节点，不只是新建的那些；`L1EdgesAdded` 数的是本轮新建**或抬权**的共现边。两个移除计数横跨「陈旧重建」与「衰减」两个阶段，并各自把它带走的边一并算进去。空报告表示无内容可巩固，不算错误；管线中途失败时部分填充的报告随错误一起返回。场景读回上下文的规模不是一条硬上限：depth-1 话题数越过 `SceneDreamTopicThreshold` 就调度该场景 Dream，Dream 只把 LLM 判定成一组的话题合并上去，没被选中的仍留在 depth-1。

### 6.5 接决策循环内核

一个 `.meh` 文件、一个决策循环、一个 agent 域——公开面就是照这个形状做的。开着的场景与
开着的轮次都由库自持，所以内核侧不需要自己记账：

| 循环在做的事 | 库这一侧 |
|---|---|
| 决定起一轮 | `Search(SearchQuery{})`——什么都不填进去，回来时该场景的下一轮已开着 |
| 跑它的各个分支（模型调用、工具调用、沙箱答复） | 值得留下的每一条事实一次 `AppendArchive`，写进开着的那一轮 |
| 带一个状态词结束这一轮 | `Update(TurnEnd{Input, Output, Outcome, CreatedAt})` |
| 一步一步计划这一轮 | `PlanNodeAdd` / `PlanNodeUpdate` / `PlanState`，全在开着的那一轮上 |
| 睡觉（定时器也可能撞在轮还开着的时候） | `Dream`——它既不弄丢域自持着的那一轮，也不扫掉那一轮已经写下的记录 |

`Outcome` 收的是内核对「哪条分支结束了这一轮」的自称：引擎原样存下、绝不按它分支——与一条
事件的 `EventType` 同一个姿态。所以词表不强加给内核，进来这一趟也没有翻译。

多个 agent 是同一个形状重复一遍，不是把形状加宽。模型用一个工具调用招来的 worker，有自己的
文件、自己的内核、自己的会话句柄，上表对它一字不改：库不留任何进程级共享状态，所以同进程里
这样两套彼此看不见对方的轮次，也不会撞到同一个 id。宿主只需要多做一件事——worker 的文件路径
是从模型里出来的，得由宿主把它限定在自己的目录内（见第 5 节）。两套也可能被喂了同一个路径，而那一
种撞车是库替宿主挡下来的唯一一处：拿着一个已被占用的文件（本进程占的也算）再 `Open` 一次，回来的是
`ErrIO`，先持有它的那边一点都不受影响，宿主该做的动作是换一个路径，而不是把这份库读成坏了
（`TestOpenRefusesAFileThisProcessAlreadyHolds`）。
同一个选择也决定了 worker 对项目知道多少：L3 是「一份文件一个池」，所以另开一条路径的 worker 起手是一张
空图，而作为父文件的子域建出来的 worker 什么都不必重导就继承那张图（`TestKnowledgeGraphStaysInsideItsFile`）。
worker 属于哪一种由宿主定，库不替它猜。

决策循环内核的记忆端口本来就是这个形状：召回在问模型之前跑，记回在这一轮的终点跑（每条
退出分支都算）。两者并不对称——内核带着工具调用一圈要召回好几次——所以对应关系是：开轮的
那次读用 `Search`——挂在内核「每次调用一次」的那个钩子上，不要挂在召回上；中间每一次
召回用 `SceneContext("")`（纯读，不点名、不吃轮次）；终点那一次收束用 `Update`。适配器手里只剩一个会话句柄和内核对「这轮怎么结束的」那个称呼。
写入调用一个 id 也不收，宿主既不自己造一个、也不替库记着。唯一可能被宿主握住的是 `NewTopicID`——开轮那次读交回来的那个名字，而它只在两件事上需要：这一轮还没收就想读它自己的事件轨（`SearchL4{TopicID, Kind: event}`），或把它撤回（`DeleteTopic`）。计划树连这个都不必：`PlanState` 读的就是库替你开着的那一轮。轮中写进去的一条事件，在 `Update` 收掉这一轮之前就能按那个名字读到。

宿主剩下的只有八条要知道的事实，不是八个要写的适配器：

- **召回端口要把「还没有场景」翻成一次空答案。** `SceneContext("")` 对一个从没读过的域按契约
  报 `ErrNotFound`——它不替一次读新建场景。这是库的契约，但决策循环是**每次问模型之前**都读
  一遍记忆，而端口一报错内核就把整次调用判终止：于是「还没有任何可记的东西」的第一轮反而会
  打死这只猫。只认这一码，返回空记录；其余错误原样上抛，那里才是真正的故障。
- **同一轮收两次是重放，不是延续。** 那两个对话槽留下的是最后一次收束说的那一对问答，而每次
  结局各自成为一条事件——`TestTwoClosesOfOneTurnKeepBothEndingsAndTheLastDialogue` 钉住的就是这
  个残留。所以「挂起等输入 → 恢复」的一轮不该在同一轮上收两次束，上面那条对应关系正是为此：
  每次调用读一次（`Search`）、收一次（`Update`），两条臂各占一轮、各留自己的话。库里没有任何
  东西会把两条臂并成一条；而收束也不是攒一轮历史的地方，那归 `AppendArchive`。
- **只有 `Update` 与 `Dream` 调模型。** `Update` 恰好一次调用，在域锁内；开轮的那次读、以及
  轮内的每一次写都是确定性的。`Update` 可能调度的后台巩固同样要拿域锁，所以一趟慢的 Dream
  会顶住**该域**的其它调用——但顶不住别的域。
- **时间戳一律毫秒。** 这个面上每个时间戳都是 Unix 毫秒，写边界拒掉秒级那一段（1e9–1e11），
  因为存下去的值会被下一次巩固当成过期扫走。带 `time.Time` 或 Unix 秒的内核只在填结构体那一处
  换算一次（`t.UnixMilli()`），这条路上只剩这一次换算。
- **「最近 N 轮」数的是话题，不是记录。** 一轮是场景表面上的一个话题（`Search` 与
  `SceneContext` 按轮次顺序交回），而 `SearchL4{Limit}` 上限的是**记录**条数。所以按 N 轮召回
  要先取那 N 个话题，再逐个读它名下的内容；这个面上没有任何一条 L4 查询按轮次计数。这些读各自花多少，量的是库对整个域的扫描，不是窗口大小：300 轮（900 条记录）的一份域上，一次 `SceneContext("")` 交出全部 300 个话题，五次跑下来 1.4–2.2 ms，而按话题再补一次读（`SearchL4{TopicID}`）每条 43–64 微秒。所以「为每条召回项再查一次结局」的适配器付的是 N × 数十微秒，这个量级上不必为了「保持便宜」再加读面。
- **把场景读折成进 prompt 的几行，是四个判定，这里一次说清。** `SceneContext("")` 交出的清单是
  **刻意摊平到 depth ≤ 2** 的：一个巩固组是 depth-1 的话题，它的摘要作为这个话题自己的一条原文、
  带着 `Role: api.RoleDream` 回来，而被它吞掉的那几轮就是它的 depth-2 子节点——这是唯一会列出这些
  子的读，也是那些原文唯一还能回来的地方。于是：（1）`Depth > 1` 的行丢掉，那些是已经被某个父摘要
  取代的原文；（2）`ChildCount > 0` 的行，取那条 `RoleDream` 当这条记忆的正文；（3）其余的取这一轮
  自己的 `RoleUser`/`RoleAgent` 那两句；（4）一条记忆的时间取它最早那条消息，不是最后一条。（1）与
  （2）正是防止同一件事进模型两遍：照着清单原样渲染，一个巩固组就会带着它的摘要出现一次、再被它
  取代的每一轮各出现一次。引擎不渲染正文（那是宿主的事），但「这一行是什么」归库说，而它就写在行上：
  `Depth`、`ChildCount`、`Role`。
- **门面里凡是与引擎自己那个类型不同的形状，都在藏东西。** 这里大多数类型是引擎类型的别名，
  所以调用点填的那个结构体就是引擎读到的那个，值跨这道边界不需要任何转换步骤。少数几个不同
  的不是改名而是扣留：扣住一个 id 给宿主渲染成 hex（`TopicSlot`、`ArchiveSlot`），或扣住宿主
  没权写的字段（`ProfileInput` 只装可写的四项，而 `ProfileSlot` 是读回的整形；`PlanStep` 不带
  轮次键，因为那一轮归库记）。
- **多个循环互不串台，中途加开的那个也不串。** 开着的轮次归属于某个 agent 域、没有任何一处
  是进程级的，所以第二个文件（或同一份文件上的第二个域）写不到第一个的轮次上；而且**在第一轮
  开轮之后、收束之前**才去开第二个文件也是常规用法：每次收束仍然落回自己那次读开出的轮
  （两条都由 `api/surface_turn_test.go` 钉住）。子 agent 域是另一件事：它给的是「多个域共享一份
  文件与同一张 L3 知识图」，第二个决策循环不需要它，需要的是再一次 `Open`。

---

## 7. 一轮 = 一个话题

一轮就是**恰好一个话题**：`Search` 为它铸出、此后由库自持的那个 id，宿主记录的这一轮的一切都在它下面。对话原文与操作事件是同一类记录、只差一个 `Kind`，所以一轮能装下宿主记下的任意多条，而一次 `Update` 把它们蒸馏成一条关键词轨。

不发生长对话，而以 `Kind: api.KindEvent` 并排存在同一个键下——读的时候各走各的：`SceneContext` 与 `SearchL4(L4Query{TopicID, Kind: &KindUtterance})` 给说了什么，`Kind: &KindEvent` 给做了什么。这些事件之后变成什么由宿主自己决定——引擎不落能力卡，也没有结晶调用。

**写入侧拥有各轴：** 内容类型（`text`/`image`/`video`/`document`/`audio`/`code`/`other`）与说话者由 `AppendArchive` 逐条声明，读回侧原样报告（`L4Query.Type` 过滤、`ArchiveSlot.ContentType`、`SceneContext` 的 `Messages[].Type`）；未定义的值以 `ErrInvalidQuery` 拒绝而不是落库。Dream 的融合摘要是唯一类型与角色都由库钉死的记录——`text` 与角色 3。一轮恰好一次 LLM 调用（`Update`），关键词永不落后于该话题现有内容。

---

## 8. 各层 API 速查

25 个会话方法按使用者分两类：

- **任务面（18 个）**——宿主每轮驱动、LLM 工具绑定的方法：`Search` / `AppendArchive` / `Update` / `Dream`（宿主自动循环）、`GetL0` / `UpdateL0`、`ListL1`、`ListScenes` / `SceneContext`、`GetL3` / `ListL3` / `ImportL3` / `QueryL3Nodes` / `QueryL3Subgraph`、`SearchL4`、`PlanNodeAdd` / `PlanNodeUpdate` / `PlanState`。
- **组装/管理面（7 个）**——宿主代码在会话边界与管理通道调用，**不做成 LLM 工具**：`UpdateScene` / `RenameTopic` / `MergeScenes` / `DeleteScene` / `DeleteTopic`、`UpdateL3` / `DeleteL3`。

文件级生命周期与诊断在 `api.DB` 上（7 个）：`Primary` / `SubAgent`，加 `Checkpoint` / `CompactTo` / `Close` / `IsClosed` / `Stats`（文件字节数 + 全文件可达记录数，压缩决策的数据来源）。整个面上没有任何能力相关的方法：引擎不存卡、不解析卡，宿主读某一轮做过的事就用 `SearchL4{Kind: event}`，之后怎么组织是它自己的事。

### L0 画像

```go
prof, err := db.GetL0()                       // *api.ProfileSlot —— 读回全量
err = db.UpdateL0(&api.ProfileInput{Name: "..."})
```

`UpdateL0` 收 `ProfileInput`，这个类型里就只有宿主那四项——`Name`、`Role`、`Personality`、`Preferences`。库自有的几项不是「传了不生效」，而是根本不在形状里，也就传不进来：`EmotionState` 与 `MBTI` 只由 Dream 演化，`UpdatedAtMs` 由库戳写，`AgentType` 在域创建时就已定。一次写入从记录里继承的是那两个蒸馏信号与 `AgentType`，`UpdatedAtMs` 由它自己戳，所以不必先 `GetL0` 再回填。蒸馏那一半只由 Dream 维护，库不再提供单独的蒸馏入口。

`Personality` 是唯一有两个写者的字段，也是唯一**不被继承**的那一项：Dream 的蒸馏会用模型从本域记忆里推出来的人格摘要替换它，所以读回来的是两者中较晚的那一个。因此一次省略 `Personality` 的 `UpdateL0` 会清掉上一趟蒸出的摘要，下一趟再重新演化——想保住就从 `GetL0` 把它带回来。`Name`、`Role`、`Preferences` 只有宿主一个写者。

### L2 场景管理

| 方法 | 说明 |
|---|---|
| `db.ListScenes(l3ID) ([]SceneSlot, error)` | 场景列表（`SceneID / SceneName / L3ID`）；`l3ID` 非空时只列挂到该项目域的场景，`""` 列全部 |
| `db.SceneContext(sceneID) (*SceneContext, error)` | 场景全貌（含各话题的 L4 原文），且**完全不写**——不开轮次，**会话恢复用这个**，一轮开了轮之后每次召回也用它；`sceneID` 留空即读该域正在做的那条会话，重复调用不必持有任何东西。与 `Search` 的取数差异是刻意的：它平铺到 depth 2，因为 Dream 融合组把原文下沉到了子话题，只有这条路能取回。认出「一个融合组」看 `ChildCount > 0`（它自己那条唯一的消息带着 role 3，即 Dream 给组摘要盖的记号），`Depth` 只回答这条话题还在不在场景表浅——被后一轮巩固折走的组会与它归并的那几轮同为 depth 2，分不出父子。返回的这些条目本身就是全量计数——根与只有这条路能带回的下沉子条目都在里面 |
| `db.UpdateScene(sceneID, api.ScenePatch{Name, L3ID, Force}) (SceneSlot, error)` | 一次调用改标题（`Name`）/ 锚定到 L3 项目域（`L3ID`）/ 清除锚定（`L3ID: &""`）；未传的字段保持库里现值，**返回值就是写入后的场景** |
| `db.MergeScenes(primaryID, []secondaryIDs) error` | 场景合并 |
| `db.DeleteTopic(topicID) error` | 删除话题子树 + 其 L4 原文 + 索引；子树即 `parent_id` 指向它的那批话题（记忆纠错） |
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

`GetL3` / `ListL3` / `QueryL3Nodes` / `QueryL3Subgraph` / `UpdateL3` / `DeleteL3`。删除只有一个粒度：整图。
`QueryL3Subgraph` 的 `edgeKinds` 只走点名的那几种边，空清单即不加这条约束；六个常量之外的
边种类以 `ErrInvalidQuery` 拒绝——写侧本就拒收它，而滤出一个空子图会被宿主读成「这张图
没有这种边」。

`QueryL3Nodes` 的条件之间是 **AND**（`IDs` / `Keyword` / `NodeType`），所以只填 `GraphID` 即列出该图全部节点，`Keyword` 忽略大小写——与 L4 的关键词一致。L3 的每一份读都按 id 升序返回（图里的节点、边，`ListL3` 的图槽），`Limit` 取的是这条确定顺序的前 N 个，所以同一个查询每次给出的都是同一份清单。`DeleteL3` 连节点带边整图删掉，再清掉本文件里每个域中指向它的场景锚点；改一个错事实走 `ImportL3` 的 `Merge` 模式，不做节点级删除。

库发出的每个 L3 id 都只对应一种记录：`GetL3(节点 id)`、`UpdateL3(节点 id, …)`、`UpdateScene(scene, ScenePatch{L3ID: &节点 id})` 一律 `ErrNotFound`，不会跨种类读到、更不会写到。

L2↔L3 只有一条关系，握在场景手里：`SceneSlot.L3ID` 把一个会话锚定到某个项目域（多个会话可共享同一张图）。锚点只在**建立场景的那一读**给出（`SearchQuery.L3ID` 只被新建路径采纳，续用会话时递来即拒），也可事后 `UpdateScene`；`ListScenes(l3ID)` 按域取回会话。话题不再携带图谱引用。

### L4 原文检索（查历史原文）

```go
arcs, err := db.SearchL4(api.L4Query{
    Keyword: "关键词",        // 内容子串，忽略大小写
    // Start: t0, End: t1,  // 时间范围（ms）
    // IDs: []string{...},  // 按 ID
    // TopicID: &topicHex,  // 只查该主题的存档
    // NodeSeq: 2,              // 只取归因到该步骤或其任一子步的记录（须与 TopicID 同填）
    // Type: &api.ContentImage, // 只查该内容类型
    // Limit: 50,           // 保留该次定序末尾的 N 条（<=0 为全部）
})
```

`ArchiveInput` 是写入侧的形状：`Kind`、`Seq`、`ContentType`、`Role`、`EventType`、`NodeSeq`、
`CreatedAt`、`Content`——没有 `ID` 也没有 `TopicID`。一条记录属于哪一轮由库自持（是 `Search` 铸的
键），所以把读回的一条原样递回来写时，它没有地方声称自己的出处。存下之后同一个形状叫
`ArchiveSlot`，它带 `Kind`（原文 / 事件）、`Seq`、`ContentType`（text/image/video/document/audio/code/other）、`Role`（`RoleUser` / `RoleAgent` / `RoleSystem`，另加 `RoleDream`——那是库盖在巩固组摘要上的标记，读得到、追加时被拒）、`TopicID`、`CreatedAt`、`Content`——媒体类型的 `Content` 是路径或 URI，不是二进制。每个查询字段都可选，填了的条件之间是 **AND** 关系——不分「三种模式」。顺序看查询宽度：填了 `TopicID` 按该轮内 `Seq` 升序，跨轮读取按记录自己的 `CreatedAt` 升序、同值以记录 id 收尾，`Limit` 保留该顺序末尾的 N 条。所以宿主最常用的读取各一次就够：`SearchL4(L4Query{TopicID: &topicID, Kind: &utterance})` 拿这一轮说了什么，换成 `&event` 拿做了什么，`Kind` 不填即两种都要；`NodeSeq` 只取归因到某个计划步骤**及其全部子步**的记录（闭包沿父子链接求出——序号是整数，没有前缀形状可匹配；步骤是轮次内的地址，因此必须与 `TopicID` 同填），于是一步做过什么能单独读回，不必先把整轮拉回来。
`L4Query{IDs: []string{id}}` 取代原来的单条 getter（ID 不存在返回空列表，格式不合法返回 `ErrInvalidQuery`）；
空查询返回该域全部原文——域大了请先加时间范围或 `Limit`，否则这就是文件里的每一条原文。

### 轮内事件（一轮做了什么，与它说了什么并排）

```go
// 每轮的事件是 L4 里 Kind=event 的内容，写进 Search 开着的这一轮
// （宿主不点名任何轮键）。
_, err := db.AppendArchive(api.ArchiveInput{
    Kind:      api.KindEvent,
    EventType: "tool_call",   // 宿主自己起名，任意非空即可；库不设白名单
    Content:   "工具名+入参摘要", // 整条 4 KiB 预算（含事件名），超了直接拒
    CreatedAt: time.Now().UnixMilli(),
})
// Seq 与所属话题都由引擎按轮键填好；这一轮开出的计划节点也在同一个键下
// （NodeSeq 才是把事件绑到某一步的字段，见下面的计划面；裸轮事件留 0）。
```

事件轨用 `SearchL4(L4Query{TopicID: &topicID, Kind: &event})` 按 Seq 序读回。它是**按轮键
整体寻址**的：没有任何调用返回事件句柄，因为公开面上没有读者；Dream 自动清理超出保留窗
的内容。宿主传入的事件里 `EventType` / `NodeSeq` / `Content` / `CreatedAt` 按原样采用，库
负责 `Seq` 与所属话题，并把 `ContentType` 钉成 text、角色留 0——发生了事，没有谁在说话。
整条（名字 + 正文）超过 4 KiB 拒写而不是截断：被剪短的事件读回来和完整事件无法区分。

这些事件之后变成什么，是宿主的决定，引擎不分毫插手：库里没有能力面——不落能力记录、
不解析卡片格式、也没有结晶调用。宿主要从自己的动作日志里提炼东西，用的是自己的提示词、
自己的去重规则、自己的文件布局。也没有一个「列出记了事件的轮次」的口：那些轮键是
`Search` 发出去的，append 过的宿主自己手里就有。

### L5 计划树（Go 宿主面）

计划属于开出它的那一轮：**库把整棵树种在本轮的话题 id 下**——`Search` 铸出、此后自持的那个——同一个键在 L4 寻址本轮的内容。一步由**轮内自增序号**（`Seq`：库从 1 起发号的 `uint32`，创建口把它返回给宿主，宿主只回传）指认，`ParentSeq` 说明它挂在哪个步骤下（`0` = 根）。序号发在「仍然指着某个序号的那些东西」之上——既包括现存的步骤，也包括这一轮仍然活着、绑到某一步的事件——所以一轮的编号可以出现空洞：一个还有事件指着它的序号不会再被发给新的一步（两者各自老化，被保留窗裁掉的步骤会留下指着它的事件）。既没有计划 id 要铸，也没有路径字符串要拼，计划调用不点名话题 id（都作用在 `Search` 开着的这一轮上），树回来走 `PlanState()`。

| 调用 | 说明 |
|---|---|
| `seq, err := db.PlanNodeAdd(0, title)` | 用第一个根步骤开出本轮的计划树，并交回此后指认该步的序号。一轮的树起初一个步骤也没有，所以本轮第一次有计划也走这个调用；没有单独的「建树」调用 |
| `seq, err := db.PlanNodeAdd(parentSeq, title)` | 给树加一个步骤并拿到它的序号。`parentSeq` 为 `0` 即把该步挂在顶层，这也是一个森林再加一个根；其他取值必须指认这棵树上已有的步骤——父序号不在树上即 `ErrNotFound`，且不会顺手长出这个父。新建的步骤就是 `in_progress`，所以这里不索要状态；标题可以先留空、之后由 `PlanNodeUpdate` 补，留空时视图按序号显示这一步 |
| `err := db.PlanNodeUpdate(api.PlanStep{Seq: seq, Status: api.PlanStatusDone, Summary: s})` | 重述一个步骤：它的 `Status` 加上本节点自己的 `Title`/`Summary`。`Status` 每次都必须给出（没有「保持原样」的写法），而 `Title`/`Summary` 留空即保留现值——改一步既不会倒退它的标题，也不会抹掉已折进来的摘要。一步到达终态就记下 `FinishedAt`；把一个已定的步骤重述成 `in_progress` 会把它重新打开，并清掉那个完成时间。一个 `Done` 父节点的**直接子全部到达终态**后，它的摘要由孩子们折上来。词表外的状态、本轮从未建出的序号（`ErrNotFound`）都在**动节点之前**被拒，树保持得和拒之前一模一样。这个调用不写任何内容 |
| `tree, err := db.PlanState()` | 读森林视图（`PlanTree.Roots` + `DoneCount` / `TotalCount`，两个计数按**每棵树的每一步**汇总，不是只数根；每个 `PlanNodeView` 带 `Seq` / `ParentSeq` / `Status` / `Summary` / `Children`）——重启恢复计划树也走这个 |
| `db.AppendArchive(ev)`（`ev.NodeSeq` 非 0） | 把事件绑到**本轮树上已有的某一步**。那一步必须先存在：一个谁都没建出来的序号会让整条记录被拒（`ErrInvalidQuery`）且零留痕——事件指了一个计划里没有的步骤，就是计划与记录对不上，树是 `PlanNodeAdd` 的事；写错的序号也因此静悄悄多不开一棵树。`EventType` **由宿主自定**，与裸轮次事件同口径——引擎不按它分支，只经 `SearchL4` 原样回显那个名字，空值即 `ErrInvalidQuery`。惯例名（给读者的共享词表，不是许可集）：`plan_step`、`llm_request`、`llm_output`、`tool_call`、`tool_result`、`subagent_spawn`、`subagent_done`、`context_inject`、`ask_user`、`user_reply` |

状态只有三个值，各一种字符串写法：`api.PlanStatusInProgress`（`in_progress`）、`api.PlanStatusDone`（`done`）、`api.PlanStatusFailed`（`failed`）。引擎不保留「已计划、未开始」这一态——一步存在是因为宿主建了它，而它一存在就在进行中。

计划的写读面在任务面那 18 个方法里——树种在 `Search` 为这个域开着的这一轮上，所以这些调用不点名话题 id。

全零键 `0000000000000000` 是保留值（记录未赋键时的值）：`Search` 绝不会在它上面开轮，而仍收话题 id 的读/纠错口（`SearchL4{TopicID}`、`RenameTopic`、`DeleteTopic`）一律拒绝它。

---

## 9. 导出类型清单（v1.6.6）

| 类别 | 名字 | 用途 |
|---|---|---|
| 入口与句柄 | **`Open`** → `*DB`，再由 `DB.Primary()` / `DB.SubAgent(llm, profile)` → `*Session` | 只有这两条进来路；agent 域是握在手里的句柄，从不以 id 命名 |
| 配置 | **`LlmConfig`** / `MemHopDefaults` + `DefaultMemHopDefaults` | `Open` 要的端点与调参入参 |
| 入参形状 | **`ProfileInput`** / `SearchQuery` / `TurnEnd` / `ScenePatch` / `L3ImportItem` / `L3Relation` / `L3ImportMode` / `L3NodeQuery` / `L4Query` / `PlanStep` / `ArchiveInput`（L4 的写形状；读回是 `ArchiveSlot`） | 宿主唯一能写的画像形状就是 `ProfileInput`，它四项里只有 `Name` 必填 |
| 响应 DTO | `ProfileSlot` / `SceneNodeView` / `SceneSlot` / `TopicSlot` / `SceneContext` / `SceneContextTopic` / `SceneMessage` / `SearchResult` / `HypergraphSlot` / `HypergraphNode` / `HypergraphEdge` / `L3Graph` / `L3Subgraph` / `L3ImportResult` / `PlanTree` / `PlanNodeView` / `DreamReport` / `DreamStage` | 每个 id 字段都是 16 位 hex 字符串，且每一个都由库发号 |
| 枚举 | `GraphEdgeKind` / `ContentType` / `ArchiveKind` / `PlanStatus` / `AgentTypePrimary` + `AgentTypeSub` | 一次调用写在里面的词汇 |
| 错误 | `Code` + 各 `Err*` 常量，用 `CodeOf(err)` 取回数字码 | 错误串背后的那一层分类 |

枚举常量同样导出：`L3ImportSkip` / `Merge` / `Overwrite`、`EdgeRelated`…`EdgeCustom`、
`ContentText`…`ContentOther`、`KindUtterance` / `KindEvent`、`RoleUser` / `RoleAgent` /
`RoleSystem` / `RoleDream`、`PlanStatusInProgress` / `PlanStatusDone` / `PlanStatusFailed`。

这份清单里没有任何换算 id 的东西：没有 `FormatID` / `ParseID`，任何签名里也没有数字
id——宿主把自己拿到的 hex 字符串原样回传，不自己拼。也没有任何能力类型：卡片形态、
磁盘文档格式与激活语义都是宿主的资产，引擎一概不参与。

---

## 10. 错误处理

所有错误均携带分类码：`api.CodeOf(err)` 返回数值码（非 MemHop 错误返回 0）。用导出的常量判断：

```go
if api.CodeOf(err) == api.ErrNotFound { ... }
```

错误码：`ErrConfig`、`ErrInvalidQuery`、`ErrNotFound`、`ErrIO`、`ErrClosed`、`ErrInvalidMagic`、`ErrCRCMismatch`、`ErrCorruption`、`ErrSerialization`、`ErrDeserialization`、`ErrCancelled`（调用方自己的上下文先结束——被取消的 `Dream`、退避中途被放弃的 LLM 调用）与 `ErrLLM`。`ErrAgentNotFound` 虽然导出，公开面上却没有任何调用能产出它：域以句柄交回，宿主手里没有一个「未注册」的 agentID。`api.NewError(code, message)` 供自己拒绝一个入参的调用方（例如工具层拒一个词表外的取值）构造同一套错误，于是它的拒绝与库自己的拒绝带同一个码。编号永不复用：`1002` 与 `9001` 已退役、不再重新发放。

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
    lib, err := api.Open(
        os.Getenv("MEH_PATH"), // /data/agent.meh
        api.LlmConfig{
            APIURL: os.Getenv("LLM_URL"),
            APIKey: os.Getenv("LLM_KEY"),
            Model:  os.Getenv("LLM_MODEL"),
        },
        api.DefaultMemHopDefaults,
        // 只在文件还不存在时被采纳。
        &api.ProfileInput{Name: "guide-agent", Role: "assistant"},
    )
    if err != nil { log.Fatal(err) }
    defer lib.Close()

    // 文件的主域。子 agent 域同样由
    // lib.SubAgent(llm, api.ProfileInput{Name: ...}) 取得。
    db, err := lib.Primary()
    if err != nil { log.Fatal(err) }

    // 一个宿主会话 = 一个场景，且由库自持：空的 SearchQuery 续用该域当前场景（首次访问
    // 时新建）并开下一轮。NewScene: true 则另开一条会话。宿主跑一个 agent 对一个库时，
    // 写入调用不携带任何 id。
    res, err := db.Search(api.SearchQuery{})
    if err != nil { log.Fatal(err) }
    _ = res // Profile/ProfileBrief + Topics（每话题 FusedKeywords）→ 拼进 prompt

    // 计划先行：一步存在是因为在这里建了它（一步一次调用；parentSeq 0 即开树），
    // 事件只能绑到树上已有的步骤。创建口交回此后指认该步的序号。
    userTS := time.Now().UnixMilli()
    fix, err := db.PlanNodeAdd(0, "定位回归")
    if err != nil { log.Fatal(err) }
    leaf, err := db.PlanNodeAdd(fix, "修复")
    if err != nil { log.Fatal(err) }

    // 每轮对话：把做过的事记进 Search 开着的这一轮。说了什么（问/答）在收口时给出。
    _, _ = db.AppendArchive(api.ArchiveInput{Kind: api.KindEvent,
        EventType: "tool_call", NodeSeq: leaf, Content: "grep ...", CreatedAt: userTS + 1})
    _ = db.PlanNodeUpdate(api.PlanStep{Seq: leaf, Status: api.PlanStatusDone,
        Summary: "…"})

    // 每轮对话：结束——Update 把这一轮的输入/输出写进对话槽、把结果作为一条事件追加，
    // 并把整轮蒸馏成关键词轨拿回它。
    if _, err := db.Update(api.TurnEnd{Input: "用户消息原文", Output: "Agent 回复原文",
        CreatedAt: time.Now().UnixMilli()}); err != nil { log.Fatal(err) }


    // 空闲/定时（通常无需手动：话题数超阈值时 Update 已后台调度）
    if _, err := db.Dream(context.Background(), ""); err != nil {
        log.Fatal(err)
    }
}
```

---


## 12. 陷阱清单

1. **LLM 只影响 `Update` 与 `Dream`**：`Search` 与 `AppendArchive` 零 LLM，记录与读取永不被 LLM 拖垮；`Update` 每轮一次提炼，失败即报错且不落该轮话题——轮上已有的记录原样留着。一次失败的 `Update` 把那一轮保持开着，宿主可以再收一次。
2. **没有 embedding 服务，也没有维度要声明**：header 偏移 6 的两个字节是保留位。格式
   版本为 `0x0012`：L3 知识图驻留保留共享域（`core.SharedPoolAgentID`），话题记录带着
   宿主给它起的 `name`，域身份（`agent_type`）写在该域自己的画像上。不跑迁移——
   `0x0011` 及更早的文件在 Open 时被拒绝，理由不是一个「可以容错解码」的字段：那批
   文件的画像里根本没有 `agent_type`，于是**每一个**域都读成主 agent，而当前规则要求
   一个文件恰好一个主；放它打开，错的是每个域的域身份，不是某一处取值。那批文件还各自
   带着更细的偏差——计划节点存在点号路径下而不是轮内序号上、状态字节用的是已退役的
   编号（`0` 表示 pending、`2` 表示 done）、事件绑在从未建出的步骤上、归档把归属话题记
   在 `context_id` 这个当前记录已没有的键下、id 派生自 `l1:` / `l4:` 这两个已不存在的
   命名空间。按当前规则，哪一条都指不到东西。
3. **时间戳用 Unix 毫秒**，`<=0` 报 `ErrInvalidQuery`。
4. **ID 是不透明 16 位 hex**：不要自行拼接/截断；响应里的 id 原样回传即可，门面上不再有 hex ⇄ 整数转换函数。
5. **`Search` 不写记忆内容**：它开启一个轮次（场景的轮次计数 +1），但不建任何话题记录——开了没沉淀的轮次不留残渣。想读原文用 `SceneContext` / `SearchL4`。重放一次 append（同 `(话题, Seq)`）是幂等的：记录 id 由那一对派生，重试只会覆盖不会叠加；而重放不再去填的槽位不会被回收。
6. **单文件多 agent 域**：所有租户驻留同一个 `.meh` 文件——`api.Open` 落定文件被打开所依据的那个域，`DB.SubAgent(llm, profile)` 按名字在其下建/取一个——除文件级 L3 公共池外按域完全隔离；旧库（`FormatVersion < 0x0012`）既打不开也不迁移。
7. **内容与计划自动过期**：Dream 清掉保留窗外的内容与计划节点（窗口默认 7 天，宿主经 `Defaults.ContentRetentionMs` 可配）（仍在途的树豁免）；显式纠正走 `DeleteTopic` / `DeleteScene`。过了窗的话题只剩关键词轨，`Messages` 读回来是空的或 `Seq` 上有洞——那是合法的终局，不是读取失败。融合组的摘要按**写下它的那一次巩固**计龄，不按它取代的那几轮，所以它能活过那些原文：子话题的原文被扫走之后，父话题仍可能带着 Dream 自己写的那段文本。一切都按库自持的开轮绑定，所以在这一轮进行中随时追加，收口交给 `Update`——没有要宿主保管或自造的轮键。
8. **场景与开着的轮都由库自持**：`Search` 续用该域当前场景并开下一轮；写调用（`Update`、`AppendArchive`、计划族）都作用在这一轮上，不点名 id。没有开着的轮时——从没 `Search` 过，或那一轮/那个场景被删了（`DeleteTopic`/`DeleteScene`）或被合并吞掉（`MergeScenes`）——它们返回 `ErrInvalidQuery`，消息含 `no turn is open`。库不会为一次写入自动建场景，也不会在 Dream 里合并场景——合并只走显式 `MergeScenes`，而它会把被并场景连记录删掉，宿主手里的旧 id 随即失效。**合并之前先收口**：开了没收口的轮次还没有话题记录，所以不在被改写的范围之内——它的 id 指着一个已经不存在的场景，此后再也收不了口。每次 `Search` 恰好开启一个轮次：读两次只收口一次，就是跳掉一个轮次号，空洞不产生成本，且已给出的 id 永不重复。
9. **`SceneDreamTopicThreshold` 默认 24**：用部分字面量构造 `MemHopDefaults` 时该字段为 0，会**禁用**自动巩固——先赋 `api.DefaultMemHopDefaults` 再覆盖。上下文规模只由 Dream 收敛，不是硬上限，禁用自动巩固就等于让注入无界增长。
