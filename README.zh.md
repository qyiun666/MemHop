<p align="center">
  <h1 align="center">MemHop</h1>
  <p align="center">
    <strong>AI Agent 的长期记忆数据库 —— 六层认知架构，单文件嵌入式，纯 Go 实现，零基础设施。</strong>
  </p>
  <p align="center">
    <a href="README.md">English</a>
    &middot;
    <a href="https://qyiun666.github.io/meowagent.github.io/">官方网站</a>
    &middot;
    <a href="https://github.com/meowagent/meowagent">MeowAgent (即将开源)</a>
  </p>
</p>

<p align="center">
  <a href="https://github.com/qyiun666/MemHop/actions/workflows/workflow.yml"><img src="https://github.com/qyiun666/MemHop/actions/workflows/workflow.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/qyiun666/MemHop"><img src="https://pkg.go.dev/badge/github.com/qyiun666/MemHop.svg" alt="Go Reference"></a>
  <img src="https://img.shields.io/badge/go-1.27+-00ADD8.svg" alt="go">
  <img src="https://img.shields.io/badge/license-MIT%20OR%20Apache--2.0-blue.svg" alt="license">
</p>

<p align="center">
  <strong>当前版本：v1.6.4</strong>
</p>

---

MemHop 是一个面向 AI Agent / 大模型（LLM）应用的**嵌入式长期记忆数据库**，纯 Go 实现。它不是一个向量数据库——它是以人脑知识组织方式为蓝本的记忆系统：具备身份认同、情景回忆、语义压缩、知识图谱和归档存储。一个 Agent，一个 `.meh` 文件，零基础设施。

MemHop 是 **Agent 专用**记忆数据库：每个 Agent 绑定唯一的 `.meh` 文件，文件级排他锁保证同一文件同时只有一个实例（第二次 `Open` 直接报错）。支持 **Linux、macOS、Windows** 全平台，无 cgo，除 LLM 接口外无任何外部服务。

作为 [MeowAgent](https://github.com/meowagent/meowagent)（即将开源）的大脑记忆模块，MemHop 以内嵌器官而非独立服务的形式运行。无需启动服务器，无需管理配置——打开文件，Agent 便拥有记忆。

> **我们对 Agent 记忆的立场。** 记忆不应该是事后用向量数据库插件外挂上去的附属品，也不该是被塞进上下文窗口的纯文本日志。没有内化记忆的 Agent，不过是一个假装聪明的无状态函数。MemHop 的存在基于一个信念：记忆必须是*认知的*——像人脑一样结构化、压缩、巩固、遗忘——并且是*内嵌的*——活在 Agent 进程内部，而非躲在一次网络调用的背后。一个文件，零基础设施，心智随每次对话成长。

## 核心特性

- **六层认知架构** — L0 画像 → L1 纠缠图 → L2 上下文 → L3 知识 → L4 归档 → L5 计划，配合 Dream 巩固管线
- **场景即会话的记忆循环** — 一个 L2 场景 = 宿主的一个会话。`Search` 按场景 id 直取该会话的 depth-1 话题集（纯内存读，零 LLM、零 embedding），并**顺手开启本轮**：返回本轮要落进去的话题 id。随后由宿主自己记录这一轮——`AppendArchive` 写下说了什么与做了什么（对话原文与操作事件同为 L4 内容、只差一个 `Kind`），`Update` 再把这一轮的原文一次提炼成该话题的关键词收口。一轮拥有的东西全在这个 id 下，该轮开出的任务树才是 L5。话题的 `FusedKeywords` 集合就是宿主每轮注入的上下文
- **V2 追加写入存储** — `.meh` 格式（`FormatVersion=0x0012`），A/B 双头 + 记录级 CRC32 + 撕裂尾帧截断恢复，mmap 零拷贝读取，快照/检查点。记录帧携带 8 字节 `agent_id`（26 字节帧头），引擎按 `(agent, idHash)` 域索引全部记录。L3 知识图记录驻留文件级保留公共域，该域不再承载别的东西。**仅认 `0x0012`**——`0x0011` 及更早的 `.meh` 数据文件 Open 时显式拒绝、无迁移路径：那批文件的画像上没有 `agent_type`，解码回来每个域都读作主 agent——错的不是某一个值而是每个域同时错，而当前规则要求一个文件恰好一个主
- **多 Agent 域** — `Open(path, llm, defaults, profile)` 返回库句柄，域一律以句柄形式取、不以 id 取：`Primary()` 是文件被打开所依据的那个域，`SubAgent(llm, profile)` 是按名字建/取的子域。多个 agent 共享一个 `.meh` 文件，各自拥有完全隔离的域（话题缓存、Dream 管线、域级锁）；同 agent 串行、跨 agent 并行；空闲域按访问节奏回收内存（`Defaults.AgentIdleTTLMs`），记录仍在文件。例外是 L3（见下）：知识图是文件级公共池
- **L1 场景超图** — Dream 在关键词集合重叠的场景间创建共现超边（Jaccard ≥ `L1EdgeMinSimilarity`）并按时间衰减剪枝；一条边以建边时那个相似度定重，此后只淡出——只有某一端名下真的多了轮次才会重新加权，把一对没长过的场景再测一遍（哪怕同一批轮次被重新蒸馏成了别的措辞）不算重新经历。L1 由 Dream 维护，供显式图查询与后续关联消费——读取路径不打分、不扩散
- **Dream 巩固管线** — 作用于 L0–L2，另对内容与计划树各做一次保留期清理：`l4_prune`（丢弃 7 天前的话题内容）与 `l5_prune`（丢弃 7 天前的计划节点，仍在途的树豁免）排在最前，随后 L2 压缩 → L2Meta 缓存重建 → L1 节点/超边重建 → L1 衰减 → L0 蒸馏（情绪/MBTI）；某场景 depth-1 话题数超过 `Defaults.SceneDreamTopicThreshold` 时由 `Update` 后台调度该场景巩固，返回逐阶段 `DreamReport`
- **L3 知识图谱** — 多独立超图，节点导入支持位置引用（source_ref）与关系边（related；边的身份是「成员节点 + kind」，同一对节点可并存多种关系），整图删除，关键词/类型/ID 条件按 AND 组合，BFS 子图查询。图池是**文件级**的：文件内所有 agent 域共享一份 L3（项目知识导一次全家可见），公共池的寿命跟文件走、不跟任何单个域走
- **设计层面单实例** — 一个 `.meh` 文件只有一个持有者：全平台文件排他锁强制（linux/darwin/windows），第二次 `Open` 直接失败；内嵌形态无服务进程、无后台守护
- **极简依赖、可内嵌** — 4 个直接 Go 依赖（xxhash、go-openai、go-sdk、golang.org/x/sys）；关键词提炼没有本地兜底，LLM 返回不可解析就直接报错；**引擎不联系任何 embedding / 向量服务**，配置里也没有维度要声明，`sync.RWMutex` + `atomic.Pointer`，零基础设施
- **MCP Server** — `cmd/memhop-mcp` 将 26 个公开会话方法中的 20 个以 24 个 MCP 工具通过多租户 HTTP 暴露（SSE + streamable-http，官方 `modelcontextprotocol/go-sdk`）：单进程服务多个宿主，共享一个 `.meh` 文件，每个租户按 URL 路径 `/mcp/<tenant-id>` 隔离到自己的子 agent 域（以租户名为地址，所以重连回到同一个域；`os.Root` 锚定 db 目录；L3 知识图是全租户共享的唯一公共池）。刻意只留在 Go 侧：L5 计划写读面（`PlanCreate`/`PlanNodeAdd`/`PlanNodeUpdate`/`PlanState`）、记忆纠错（`DeleteTopic`/`DeleteScene`）与文件维护（`CompactTo`，入参就是一个输出路径）——这些要由持有会话状态、或该决定文件写到哪里的宿主来调

## 快速开始

> 完整集成指南（配置、各层 API、轮次与轨迹、陷阱）：
> [INTEGRATION_GUIDE.zh.md](INTEGRATION_GUIDE.zh.md) · English: [INTEGRATION_GUIDE.md](INTEGRATION_GUIDE.md)

```go
import (
    "context"
    "log"
    "os"
    "time"

    memhop "github.com/qyiun666/MemHop/api"
)

db, err := memhop.Open(
    "agent.meh", // 整个数据库就是这一个文件；无需服务，也不声明维度
    memhop.LlmConfig{ // 必填：在碰文件系统之前就校验
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
    memhop.DefaultMemHopDefaults,
    // 文件还不存在时必填：打开一个文件总得知道这是谁的记忆。已存在的文件
    // 保留它自己那份主域画像，这个入参不被采纳。
    &memhop.ProfileInput{Name: "my-agent", Role: "assistant"},
)
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// 一个 .meh 文件承载多个隔离域，而且域以句柄而不是 id 的形式交回。Primary
// 是文件被打开所依据的那个域；SubAgent 按名字建/取一个子域，可选地给它
// 挂自己的 LLM 端点。
sess, err := db.Primary()
if err != nil {
    log.Fatal(err)
}

// 读记忆 = 读一个场景（场景就是宿主的一个会话），同时开启即将进行的这一轮。
// SceneID 为空 → 库新建场景（名字由库生成）并返回其 id（L3ID 可选：挂到某个 L3 项目域）；
// SceneID 非空 → 该场景必须已存在，否则 ErrNotFound。
// 纯内存读：不调 LLM、不做向量编码、不打分；NewTopicID = 本轮要落进去的话题。
res, err := sess.Search(memhop.SearchQuery{})
if err != nil {
    log.Fatal(err)
}
sceneID := res.Scene.SceneID
for _, topic := range res.Topics { // 该会话的 depth-1 话题集 = 本轮上下文
    _ = topic.FusedKeywords
}

// 一轮进行中：宿主把这一轮的所见所行写进 Search 开出的那个话题。
// 对话与事件是同一类记录，只差一个 Kind。
topicID := res.NewTopicID
_ = sess.AppendArchive(topicID, memhop.ArchiveSlot{
    Kind:      memhop.KindUtterance,
    Seq:       1, // 槽位 1 与 2 属于对话
    Role:      memhop.RoleUser,
    Content:   "昨天我们讨论了什么？",
    CreatedAt: time.Now().UnixMilli(),
})
_ = sess.AppendArchive(topicID, memhop.ArchiveSlot{
    Kind:      memhop.KindUtterance,
    Seq:       2,
    Role:      memhop.RoleAgent,
    Content:   "Agent：...",
    CreatedAt: time.Now().UnixMilli(),
})
_ = sess.AppendArchive(topicID, memhop.ArchiveSlot{
    Kind:      memhop.KindEvent,
    EventType: "tool_call",
    Content:   `{"tool":"grep"}`,
    CreatedAt: time.Now().UnixMilli(),
})

// 一轮结束：Update 把这些原文一次提炼成该轮话题的关键词。
// 显式重写同一个 Seq 是覆盖而不是新增，超时后整轮可安全重放。
err := sess.Update(sceneID, topicID)
if err != nil {
    log.Fatal(err)
}

// Dream 巩固（L0-L2）；sceneID 传空串 = 遍历域内全部场景。
// 场景话题数超阈值时 Update 已会自行后台调度，通常无需手动调用。
report, err := sess.Dream(context.Background(), "")
```



> **并发契约。** 同一 agent 的操作（Search / Update / Dream / 写 API）由库内域级锁串行，跨 agent 在 `*DB` 上并行，宿主无需自行排队。`*memhop.Session` 除绑定的域外不携带任何跨域状态。文件排他锁仍保证一个 `.meh` 文件只能被一个进程打开；`*DB` 不暴露任何锁接口——域锁是库的，宿主自己的临界区请自行加锁。

前置条件：Go 1.27+，OpenAI 兼容的 LLM 接口（`Config.LLM` 必填）；无需任何 embedding / 向量服务

### API 概览

| 分组 | 方法 |
|------|------|
| 核心循环 | `Search(q) → topicID` · `PlanCreate(topicID, title) → seq` / `PlanNodeAdd(topicID, parentSeq, title) → seq` / `PlanNodeUpdate(topicID, PlanStep{Seq, Status, …})`（计划先于步骤，一次一步） · `AppendArchive(topicID, ArchiveSlot{...})` · `Update(sceneID, topicID)` · `Dream(ctx, sceneID)` |
| L0 画像 | `GetL0` · `UpdateL0` |
| L1 纠缠图（只读） | `ListL1() → []SceneNodeView` —— 本域全部场景节点，顺序稳定、id 为 hex。节点与它们之间的共现边都由 Dream 建立，Dream 是唯一写入方，所以没有 L1 写接口。`Importance` / `Valence` / `Arousal` 是巩固算出来的值；`EdgeIDs` 本身没有读取口——两个节点共享同一个 id 就意味着 Dream 判定它们相关 |
| L2 上下文 | `ListScenes([l3ID])` · `UpdateScene(id, {Name, L3ID, Force})` · `RenameTopic(topicID, name)` · `SceneContext` · `MergeScenes` · `DeleteTopic` · `DeleteScene` |
| L3 知识 | `GetL3` · `ListL3` · `ImportL3`（返回本批写入的 `graph_ids`） · `UpdateL3` · `DeleteL3` · `QueryL3Nodes` · `QueryL3Subgraph` |
| L4 归档 | `AppendArchive(topicID, ArchiveSlot{Kind, Seq, Role, ContentType, EventType, NodeSeq, Content, CreatedAt})` 是一条记录进入话题的唯一途径（`Seq: 0` 由库分配；写一个已被占用的槽位就是覆写），事件的 `NodeSeq` 必须指向本轮计划里已创建的那一步（`0` 即不绑任何步骤）。`SearchL4(q)` 是唯一读取面，两类内容都在里面；关键词（忽略大小写）/ 时间段 / id / 话题 / `Kind`（原文 or 事件）/ `NodeSeq`（**某一步及其全部子步**归因的记录，步骤只在它那一轮内成立；`0` 即不加这条约束）/ 内容类型都是条件而不是模式，`Kind` 不填即两种都要，`Limit` 保留该次读取排序后的末尾 N 条（单话题按槽位序，跨话题按记录自己的时间序） |
| 轮内事件（L4 的 `Kind=event`） | 一轮一个键：Search 为该轮开出的话题 id；事件本身住在 L4（`Kind=event`），7 天自动清理、无删除接口，读它用 `SearchL4(L4Query{TopicID, Kind: &KindEvent})`，写它用 `AppendArchive`。一个话题的首条事件是 `Seq=3`，因为槽位 1 与 2 属于对话 |
| L5 计划树 | `PlanCreate(topicID, title) → seq` · `PlanNodeAdd(topicID, parentSeq, title) → seq` · `PlanNodeUpdate(topicID, PlanStep{Seq, Status, Title, Summary})` · `PlanState(topicID)` —— 计划树是 L5 唯一自己的记录：一节点一条，一个步骤由「开出它的那一轮 + 该轮内库顺序发号的序号」说清（`ParentSeq` 指它挂在谁下面，0 即根；序号只保证**同时活着的两步**不共用一个，不保证永久唯一——保留窗扫掉哪一步就腾出它的序号，而事件按自己的时间老化、可以活过它所标注的那一步，回到一个被扫空的旧轮次时，手里的旧序号要按新步骤的地址对待），所以 `PlanState(topic)` 与 `SearchL4{TopicID, Kind}` 是同一个键、两层存储，而 `SearchL4{TopicID, NodeSeq}` 能单独读回某一步及其全部子步做过的事。节点**只因被创建而存在**：`parentSeq` 指向树上没有的步骤是拒绝，而不是顺手补出一个父节点（因此打错一个序号不会长出第二棵树）。`PlanNodeUpdate` 只重述已在树上的那一步——`Status` 每次必须给（没有「保持不变」这种写法），`Title`/`Summary` 留空继承现值。新建的步骤就是 `in_progress`，模型里没有「已计划未开始」这一档；撤回一步没有接口，也不需要一个：手段就是不在此后的轮里再创建它。计划写面不落任何内容：一步的轨迹是宿主自己 append 的 L4 记录（仅 Go module 暴露，MCP 工具集未接入） |
| DB 句柄 | `Open(path, llm, defaults, profile)` · `Primary()` · `SubAgent(llm, profile)` · `Checkpoint` · `CompactTo(newPath)`（写出整理后的副本，仅 Go） · `Close` · `IsClosed` |

## 架构

```
层级  名称            人脑类比              机制
───── ────────────── ───────────────────  ─────────────────────────────────────────────
 L5    Plan            任务树                一节点一步，键是开出它的那一轮；过期的树由 Dream 清扫
 L4    Archive         轮内内容             一轮的对话原文与操作事件（Kind），按 (话题, Seq) 寻址；7 天窗口——越过它的只有关键词轨存活
 L3    Knowledge       语义记忆             多源超图知识库
 L2    Context         工作记忆             场景表浅话题 + 被 Dream 折进融合组的那一层（一条话题至多下沉一次）
 L1    Engram          场景超图             场景节点 + 关键词重叠超边；由 Dream 维护，供显式图查询
 L0    Profile         身份认同             Agent 人格、偏好与语言习惯
```

### Dream 管线

Dream 周期是一个自动记忆巩固过程，受人脑睡眠中处理经历的机制启发。Dream **仅作用于 L0–L2**（L3 蒸馏为设计外）另对 L4 内容与 L5 计划节点各做一次保留期清理，共五个阶段：

1. **L2 压缩** — LLM 归组合并相关话题，每个目标场景一个 goroutine 并行处理，把被合并的话题下沉为 depth-1 融合节点（子节点降级为历史）
2. **L1 重建** — 从 L2 同步场景节点，并在同一趟扫盘中重建 L2Meta 话题缓存、创建/刷新场景间关键词重叠超边
3. **L1 衰减** — 衰减场景重要性与边权，剪枝弱节点
4. **L0 画像** — 基于巩固后的记忆重建 Agent 画像
5. **L0 蒸馏** — 蒸馏情绪/MBTI 模式（恒执行；L1 采样为空时自动跳过）

触发方式：某场景的 depth-1 话题数超过 `Defaults.SceneDreamTopicThreshold`（默认 24）时，`Update` 在后台调度该场景的 Dream；宿主也可显式调用。`Dream(ctx, sceneID) (*DreamReport, error)` 整个周期持有域锁，`sceneID` 传空 = 遍历域内全部场景（话题数不足 `DreamCompressMinTopics` 的场景自动跳过），并在阶段间响应 `ctx` 取消。

### 读取与写入路径

**没有打分检索。** 场景 = 宿主的会话，所以引擎不再猜"这条消息属于哪个场景"：

| 路径 | 做什么 | 代价 |
|------|--------|------|
| `Search(SearchQuery{SceneID, L3ID})` | 空 `SceneID` → 新建场景（名字由库生成）并返回其 id；非空 → 返回该场景的 depth-1 话题集（按用户消息时间升序）+ L0 画像，外加 `NewTopicID`：本次读取为即将进行的这一轮开出的话题 | 纯内存读（L2Meta 缓存），零 LLM、零 embedding、零打分；唯一写是场景记录（轮次计数） |
| `AppendArchive(topicID, ArchiveSlot{Kind, ...})` | 一轮内容的唯一写入面：原文声明谁说的、是什么媒介；事件自己命名，并可挂在某个计划步骤上。`Seq: 0` 在两个对话槽之上分配 | 零 LLM；被拒的记录一字节不留（含顺路要建的节点）。事件 4 KiB、原文 64 KiB，超预算是拒写不是截断 |
| `Update(sceneID, topicID)` | 把该话题已有的原文蒸馏成它的 `FusedKeywords`；它自己不写任何内容 | 每轮恰好 1 次 LLM 调用，且排在该轮话题落盘之前，失败不留半成品话题。内容已被 7 天窗裁光的轮次直接 `ErrInvalidQuery`，一次 LLM 也不调用 |

宿主注入的上下文就是该场景 depth-1 话题的关键词集合；要看某轮原文，用那一轮的话题 id 去寻址 L4——`SearchL4(L4Query{TopicID})`——或直接用已经带回消息的 `SceneContext`。上下文规模由 Dream 保证有界（`Consolidate` 要求压缩后每场景话题数 ≤ 20）。

随检索一并移除的：三通道 RRF 打分、L1 扩散激活（`AssociatedContexts`）、话题向量质心与 embedding 依赖、`AutoCreate` / `DirectedL2ID` / `DirectedL3ID` 三条路由，以及话题级 `L3Refs`（L2↔L3 关系现只由场景锚点 `SceneSlot.L3ID` 承载）。
## 测试与基准

MemHop 的测试套件只驱动公开 `api` 表面——即宿主（如 MeowAgent）实际发起的调用——并直接断言引擎自身的记忆结构，而非外部可答性 judge。

### 集成测试（`test/`，build tag `integration`）

- **记忆循环**（`TestCoreCycleUpdateDream`）：按真实宿主的调用方式把 N 轮灌进一个场景，每几轮做一次**周期性 L0/L2/L4 一致性检查**——L0 画像可读、场景读回非空、L4 保留原文逐字一致；Dream 巩固后场景读回的 depth-1 话题集必须收缩，且事实仍能从 L4 原文取回。
- **关键词保真与持久**（`TestKeywordFidelity`/`TestKeywordPersistence`/`TestDreamCompressionFidelity`）：一轮提炼出的关键词忠实承载该轮含义、在噪声轮次后仍在场景读回里、并经受住 Dream 压缩。
- **API 契约**（`TestInterface*`：读路径零 LLM、写路径每轮恰好一次提炼、未知场景拒绝、检查点跨重启）、**e2e 流程**（`TestE2E*`）、**长输入健壮性**（`TestExtractKeywordsLongInputRealLLM`/`TestUpdateLongTurnNeverFails`）。

### 基准（`go test -tags integration -bench .`）

所有基准都驱动真实 api 循环（真实 LLM，无外部 judge）：

| 基准 | 测量 |
|------|------|
| `BenchmarkMemoryLoop` | 稳态 Search+Update 记忆循环，含引擎**自动调度的 Dream**（场景 depth-1 话题数超过阈值）与周期性 L0/L2 验证 |
| `BenchmarkUpdateTurn` | 一轮沉淀延迟（一次提炼 + 话题与两条 L4 写入） |
| `BenchmarkSceneRead` / `BenchmarkSceneReadLatency` | 场景读回吞吐与延迟分布（min/p50/p95/max） |
| `BenchmarkAppendL4` | 纯存储追加延迟（不调 LLM） |
| `BenchmarkDreamConsolidation` | 完整 Dream 流水线延迟 |

### 为什么不跑外部数据集基准？

公开记忆基准（LoCoMo、LongMemEval）评估的是“检索 → LLM judge 可答性”——与 MemHop 分层设计要断言的（L0 画像蒸馏、L1 场景图一致性、L2 压缩语义、L4 原文归档）是不同的问题。形态最贴近的 LongMemEval（多会话 user-assistant 对话、约 500 题）单题需 115K–1.5M tokens，不具备作为持续集成基准的可行性。因此 MemHop 通过 api 循环直接验证自身的记忆结构，而非追逐一个泛化的 QA 分数。

## 项目结构

```
api/                         ← 对外门面：open（唯一入口）/ session（唯一的业务句柄，hex id 面）/
                               types / mapping / errors / exports
cmd/memhop-mcp/              ← MCP server 二进制：24 个工具，只建在 api 包上
internal/ 根                 ← 大方法 + 组合根：config / db / session / models / exports +
                               agents / l0…l5 / l3query / search / update / dream
internal/scene|turn|dream|graph|content|plan
                             ← 第 3 层小方法包，一个认知面一个包：都不自己拿域锁，也互不 import
internal/domain              ← 域状态容器：Context（域锁、三份缓存、OpCtx）、计划缓存、
                               L2Meta 镜像维护
internal/config              ← 宿主侧配置类型（MemHopConfig / MemHopDefaults / LlmConfig）
internal/llm                 ← OpenAI 兼容传输：Chat 与截断升级重试
internal/cap/                ← 第 4 层能力包，身份中立、依赖注入：engram / llmops / profile / knowledge
internal/repo/               ← 数据层：l0layer–l5layer + agentlayer（记录读写）
internal/repo/index/         ← 索引层：l2meta / rebuild（单遍扫描）/ l4（一个话题名下的内容）
internal/repo/core/          ← .meh 引擎：engine / frame / header / snapshot / reclaim /
                               record / model / mmap / filelock
internal/common/             ← 最底层工具：enum / errors / hash / sliceutil / timeutil
test/                         ← 集成测试（integration build tag）：离线宿主面那一半与真实 LLM 那一半
benches/fixtures/             ← 基准数据集（locomo10、locomo_smoke、longmemeval_smoke）
```

依赖方向严格单向：`api → internal → repo → core`，`common` 位于最底层（不引用任何其他 internal 包）。


> 说明：`docs/` 与 `AGENTS.md` 有意保留为本地文件（见 `.gitignore`），因此公开 clone 中 `docs/` 下的链接可能无法打开。

### LLM 调用与成本模型

- **读路径**（`Search`）：**零 LLM、零 embedding**，只走 L2Meta 内存缓存。
- **写路径**（`Update`）：每轮恰好一次关键词提炼（用户原文 + Agent 原文一起喂），输出上限 512 token 起、截断时逐级升预算，最后一次格式约束重试仍不可解析则 `ErrLLM` 上抛，这一轮不写入。
- **Dream**：每次巩固对达到话题数下限（`DreamCompressMinTopics`，默认 20）的场景各调一次 L2 合并，再加一次 L0 蒸馏（最多 200 个排序后的 L1 样本，每个样本最多 20 个关键词）。输出上限分别为 8192 / 2048 token。
- 成本敏感时，给 `Config.LLM` 配一个快速小模型即可（便宜 API 模型或本地兼容端点）；关键词提取不需要旗舰模型。

## 开发

```bash
go build ./...                          # 构建
go vet ./...                            # 静态分析
go test ./internal/...                  # 单元测试（不依赖外部服务）
go test -tags integration ./test/...    # 集成测试（需要 LLM key）
```

集成测试针对真实 LLM 运行（引擎侧不再需要任何 embedding 服务）。通过环境变量 `MEMHOP_TEST_LLM_KEY` / `MEMHOP_TEST_LLM_URL` / `MEMHOP_TEST_LLM_MODEL` 配置 LLM（仅设置 key 时默认使用 DeepSeek 接口），或通过 `test/testsupport/key_config.json` 配置。

## 版本历史

| 版本 | 日期                 | 亮点 | 核心改动 |
|------|----------------------|------|---------|
| v1.6.4 | 2026-09-11 | 公开面围绕 `Open` 重做：域以句柄交回，id 不再越过边界；随后的分包审查把方法归位、删掉没人读的东西 | 1. **入口换成 `Open(path, llm, defaults, profile)` → `*api.DB`**，成败由文件里有什么决定：文件在 + 主域画像在 → 文件自己那份说了算、入参被忽略；文件在而画像不在 → 带了就播种、没带就拒；文件不在 → 带了就建库播种、没带就拒**且不留文件**。两条拒绝都排在碰文件系统之前。`OpenMulti`、`MultiAgentDB`、`MemHopConfig` 删除<br>2. **域以句柄交回**：`Primary()` 给文件被打开所依据的那个域，`SubAgent(llm, profile)` 按 `profile.Name` 建/取子域、可挂自己的 LLM 端点。`CreateAgent`/`ListAgents`/`Session(hexID)`/`DefaultAgentID` 删除——宿主不再持有也不再回传域 id<br>3. **L0 新增 `AgentType`**（0=主 / 1=子）：建域时盖章、宿主写画像时继承现值，所以改画像动不了域身份<br>4. **L1 开只读面**：`ListL1() → []SceneNodeView`，顺序稳定、id 为 hex。Dream 仍是唯一写入方，没有 L1 写接口；`EdgeIDs` 没有独立读取口，两个节点共享同一个 id 即意味着 Dream 判定它们相关<br>5. **L2 新增 `RenameTopic(topicID, name)`**：名字归宿主、引擎不派生，所以三条会重写话题记录的路径都不碰它——建父话题、把子话题沉下去、以及重放同一轮（`Update` 第二次结算同一个轮次键时只重写引擎那半，名字从存量记录带过来）；空名被拒（空是「还没命名」），未知话题 `ErrNotFound`，新名字在 `Search`/`SceneContext` 上立刻可见<br>6. **整体退役**：能力面（`Crystallize`、`ParseCapabilityPackage`/`ValidateCapabilityCard`、`internal/cap/capability` 整包）、`ListTrajectorySessions`（读事件轨走 `SearchL4{TopicID, Kind:event}`）、`DeleteL3Nodes`（L3 删除只剩整图一个粒度）、`DeleteAgent` 及其整域删除链。公开面 27 + 8 → **26 + 6**<br>7. **格式 `0x0011` → `0x0012`**：`0x0011` 及更早的文件 Open 时被拒、不迁移——它们的画像没有 `agent_type`，每个域都会解码成主 agent，而新规则要求一个文件恰好一个主<br>8. **MCP 仍 24 个工具**：删 `memhop_crystallize` 与 `memhop_trajectory_sessions`，新增 `memhop_l1_nodes` 与 `memhop_topic_rename`。每个 `/mcp/<tenant>` 按租户名建/取自己的子域；共享库启动时打开，配置不可用即拒绝启动。工具文案与实现对齐：`memhop_dream` 在阶段失败时把部分报告连同错误一起带回（此前统一的 `handle` 一律丢弃返回值，而门面承诺的恰恰是那份「已经做到哪一步」），`memhop_profile_update` 把 `name` 声明为必填并写明四项是整写而非打补丁（全 `omitempty` 的形状读起来像补丁，漏填即清空那一项），`memhop_scene_list` 不再承诺话题条数，`memhop_scene_topics` 说的是 depth ≤ 2 的平铺清单，`memhop_search` 不再声称话题带 L4 原文 id。<br>9. **`DefaultMemHopDefaults` 从指针改为值**：要调参就复制一份改，不再共享一个可改坏的全局<br>10. **读错误分类留在读侧（两处）**：`repo.GetProfileL0` 不再把一切改写成 `ErrNotFound`——读不动报 `ErrIO`、解不开报 `ErrDeserialization`。三处上游分支（`GetL0` 何时给空画像、`UpdateL0` 何时不继承蒸馏半区、Dream 蒸馏遇瞬时失败是否重写画像）此前**永不触发**，其中一处的注释还以那条分支为立论依据；被修掉的后果是一次瞬时读失败就能让 `UpdateL0` 抹掉该域的 `EmotionState`/`MBTI`/域身份。契约测试 `TestUnreadableProfileIsNotAbsentProfile` 钉住「解不开的画像不落盘」。同一分类补到压缩下沉：一个「点名却读不动」的组成员此前被当成已经消失而跳过，于是父摘要与该条自己的原文会同时留在场景里——现在改写攒到最后一次批写，成员读不动就整组不动、把错误交回巩固侧回滚（`TestCompressTopicsL2RefusesUnreadableMember`）<br>11. **Dream 的缓存重建排在 L1 阶段之前**：L2Meta 整表重建一算出来就装回域上下文——L1 只写 L1 记录，一次 L1 失败不会让按当前 L2 记录算出的缓存失效。反向的漏项补进文档：本包压缩改写话题深度时**没有增量镜像步**，那次重建就是唯一对账点（`TestStructureStagesKeepsRebuildAcrossL1Failure`）<br>12. **轮次键的取锁与解析各只剩一处**：`db.lockSession`（取域锁 → 解析轮键 → 解析失败先解锁）此前全仓零调用，而 L4/L5 各口手写同一段前言，现由 `AppendArchive`/`PlanNodeAdd`/`PlanNodeUpdate`/`PlanState`/`Update` 共用；「解析话题键并拒保留全零」原本在 `turn` 与 `content` 各写一份（只有错误文案不同），合一到 `content.ParseTopicID`，并补上 `RenameTopic`/`DeleteTopic` 两处只解析不拒零的入口<br>13. **同一判断与同一形状合一**：「索引点名却读不到 = `ErrIO`」的三份等价实现收成 `repo.ReadArchivesByIDs` 一处，`scene.ContextTopic` 退成纯渲染（小包不再跨层调 `core`）；与 `core.ArchiveSlot` 同形的 `repo.ArchiveContent` 删除（每条内容此前被复制两趟）；`CompressTopicsL2` 不再返回无人读取的时间界（同一件事 Dream 自己算过，且拿去铸父 id 的就是那个值）；L1 情感回填从「L0 profile primitives」模块迁回 L1 文件，并停止把读失败说成「节点不存在」<br>14. **死代码与无读者字段净删**：引擎内手工加减的记录计数器（header 的计数由索引现算，Open 时那次读入没有消费者）、`ListTopicsL2` 里按 id 读单个话题的那个模式取值（模式字节本身已换成具名 `ByScene`）、`MemHopConfig.Validate`、`L2MetaIndex.Len`、L3 的 `Node.Importance`/`Edge.Weight`/`Edge.Label`（无写入路径或只写常量、不进公开 DTO）、同形的两份样本形状合一：留下 `core.DistillSample` 那一份具体形状，`llmops.L1Sample` 改为指向它的恒等别名（与 `EmotionScore`/`MBTIScore` 同一先例）。**磁盘布局与 `FormatVersion` 一律不动**：旧文件里多出的 JSON 键本来就在解码时被跳过<br>15. **公开面收敛**（本轮 breaking 的部分）：<br>A) **话题不再存子话题清单**：`TopicSlot.ChildrenIDs` 连宿主可见的 `children_ids` 键一起删除。引擎内没有任何一处遍历它——子树闭包与宿主看到的 `child_count` 都由子话题自己的 `parent_id` 现算——它只贡献了删除时的一次修剪步、缓存里多镜像的一个字段，以及「与 `parent_id` 说法不一致」的可能；`scene.PruneParentChild` 与失去唯一调用者的 `common.RemoveOnce` 随之删除<br>B) **画像写入换 `api.ProfileInput`**：`Open`/`SubAgent`/`UpdateL0` 的入参只含宿主四项（`Name`/`Role`/`Personality`/`Preferences`），库自有那四项从「传了不采信」变成没有位置可传；读回形状仍是全量。四项里只有 `Name` 必填（三个入口一致，空白名一律 `ErrInvalidQuery` 拒绝，而不是存下一个无从指认的画像，`TestUpdateL0RequiresName`）<br>C) **两个纯 `len` 键删除**：`PlanNodeView.child_count`（同一视图里 `Children` 全量返回）与 `SceneContext.topic_count`（就是本次返回的条目数）；`SceneContextTopic.child_count` **保留**——它由 `parent_id` 数出来，宿主自行计算要重扫整份平铺列表<br>D) **图槽的 `updated_at` 改为内容变化钟**：一次导入对真写过内容的每张图各推进一次（一图一次写，不是每条记录一次），只被读到而没被写过的图不动——skip 模式重导不再让图看起来刚变过<br>16. **文档与实现对齐**：`PlanTree.DoneCount/TotalCount` 两处说明都写「数根」，而 `CountForest` 是沿每棵树逐节点递归汇总（以 `TestPlanStateForestMultipleRoots` 钉住的口径为准）；L5 族那份「统一前言」文档写的是全仓零调用的函数、「内容只有一个写入口」被巩固摘要的写入打破——两处都改成跑着的样子。宿主的两份集成指南另算一处：它们挂着「部分更新」的告示继续教已不存在的入口（`OpenMulti`/`CreateAgent`/`Session(hexID)`/`MultiAgentDB`/`Lock()`/`Crystallize`/`ListTrajectorySessions`/`ParseCapabilityPackage`），§1 的硬性契约表与 §9 的导出类型清单整段是旧的，一处还把格式版本写成 `0x0011`——两份都按当前面重写，告示随之改为陈述现状；「压缩后每场景 ≤20 个话题」这条规模保证在代码里没有对应常量，改成按触发式收敛的说法；`content.Append` 那句「唯一写入口」的系统级断言收回到本包边界（两个入口的事实只在 `internal/agent.md` 讲一次）；`llm` 包两处注释引用了不存在的 `Config` 类型且字段数少一项，测试与 `internal/agent.md` 里的三处失效指针（一个已改名的测试、`CreateAgent`、一段「原先由 X 读 Y」的过程叙述）一并清掉；重写时的示例本身也有一处断路：§5 把 `db` 绑成 `api.Open` 返回的 `*api.DB`，而后文每个示例都在 `db` 上调 Session 方法（`Search`/`AppendArchive`/`Update`/`Dream`/`SearchL4`），宿主照抄编译不过——现在两份都按 §11 已有示例的写法统一为 `lib` = `*api.DB`、`db` = `lib.Primary()` 给的 `*api.Session`，两份指南的可编译完整示例各自 `go vet` 通过。<br>17. **对消费方 breaking**：入口、句柄类型、方法集与格式版本同时变，加上第 15 项的三个 JSON 键消失、`ProfileInput` 换型与 `UpdateL0` 开始拒空白名（此前会存下无名画像），宿主 meowagent 需在其自身的跟版轮次里适配。<br>18. **审查轮补的读面五处**：L3 的三份列举此前直接交出哈希表扫描的顺序，同一个调用两次可以给出两个顺序，`QueryL3Nodes` 的 `Limit` 落在任意子集上——现在节点、边、图槽都按 id 升序，`Limit` 是这条确定顺序的前 N 个（`TestL3ReadsAreOrderStable`）；`QueryL3Subgraph` 此前把「BFS 走到却读不动」的节点静默跳过、交回一张小一号的图，而那个节点是某条边点名的成员，现在如实上报读失败（`TestQueryL3SubgraphReportsUnreadableNode`）；`SearchL4` 的话题过滤只做 hex 解析、不拒保留的全零键，没拿到轮次键的查询会收到空清单、与「这一轮真没内容」分不清，现在与写侧同口径拒绝（`TestSearchL4RefusesReservedZeroTopic`）；另有两处宿主读面把「读不回的记录」答成「少一条」——`ListL1` 的场景节点列举，与 `SearchL4` 不带话题过滤的那条全域扫描（它底下 `repo.QueryArchivesL4` 的文档本就写着「记录读不回是错误」，只有这条分支没照做），宿主拿到一份短了的清单时分不清「Dream 没建过它」与「这条坏了」（`TestListL1ReportsUnreadableNode`、`TestQueryArchivesL4ScanReportsUnreadableRecord`）<br>19. **审查轮补的记忆质量**（这一轮问的是「一轮记忆写进去后还回不回得来」，不是接口能不能调）：一个场景落地的融合组现在**成员互斥**——相邻两条线程可以都点名同一轮，两组都应用等于把那一轮沉两次，第一个组的摘要管着一个不再应答它的子、那一轮也落到最深的读路径之下；后到的重叠组按「提出但未应用」计入 rejected（`TestApplyGroupsRejectsOverlappingGroups`）；父话题 id 由组的时间界派生，成员互斥的两个组仍可能撞出同一个，父 id 已被占用时本组同样按「提出但未应用」拒掉（`TestApplyGroupsRefusesCollidingParentID`）。重放一轮不再把已沉入组的轮次拉回 surface：`depth` 与 `parent_id` 连同宿主的 `name` 一起从存量记录带过来，存量记录读不回时这次沉淀拒绝而不是猜一个位置（`TestCreateTurnTopicL2ReplayKeepsSunkPosition`）。衰减级联不再把读不动的边说成「已剪好」，一个节点不能继续指向一张没人剪过的边（`TestRemoveNodeFromEdgeReportsUnreadableEdge`）；L1 重建也不再因为一个 depth-3 话题「读不动」就把顶着它的节点删掉（`TestRebuildFromL2StopsOnUnreadableDeepTopic`）；话题列举在两个排序键打平时以记录 id 收尾，同一个场景两次读给出同一个顺序（`TestListTopicsL2BreaksTiesOnID`）；解不开的 payload 现在按 `ErrDeserialization` 报出（此前交出裸 `json` 错误，任何按错误码分道的调用方看到的都是 0 码），重放那一侧的拒绝也有了自己的测试（`TestCreateTurnTopicL2RefusesUndecodableRecord`）。顺手净删：`index` 里与 `core.IterAll` 同形的第二份扫描、`domain` 两处永不成立的 `L2Meta == nil` 分支、`ensureRegistered` 里第二份永不触发的空名校验、一个零调用的 L3 测试辅助函数；`scene.DetachGraph` 不再把命中的场景解码两遍；`cap` 的「不认识层」纪律与四个包的现状冲突，改写为「不认识编排，预算各归各包」。决定删除或覆写的枚举必须整个读得回来：L1 同步此前把「读不回的节点」当作「还没有」，就地新建一条覆盖上去（重要度、情感、`created_at` 一次归零，`EdgeIDs` 清空，而共现边那侧还指着它），它统计话题的那份扫描同病——一条读不回的子话题从节点的清单里静默消失；话题子树闭包、场景批删、场景归并三处此前跳过一个读不回的成员却**照样返回成功**，于是留下一个没有场景的话题、或一个父亲已被删的子话题。新增 `core.CollectAllStrict` 作为跳过式扫描的严格对偶，`MergeScenesL2` 的返回值由 bool 改为 error；`DeleteL2` 连它的两个模式常量一并删除，批删换成 `repo.DeleteL2Records`/`DeletePlanNodesByIDs`——只照给定 id 落墓碑、不读任何东西，枚举与删除就此分家：级联把所有全域枚举排在第一张墓碑之前（一次 `DeleteScene` 原本把话题桶扫三遍，其中两遍在删过之后才扫），被拒一次盘上原封不动；巩固的组回滚按自己写过的 id 定点撤销，不再被正是引发回滚的那条记录挡住。计划树的保留窗清扫同族：枚举不全就整轮不扫（在途豁免按整棵树算，读不回的那一个偏偏可能就是唯一没做完的），域内的计划缓存仍按幸存记录分组（分组逻辑收成 `GroupPlanNodes` 一份），因为镜像少一个节点与它被保留窗裁掉是同一个形状，而删除少一个不是（`TestSyncL1NodesFromL2KeepsANodeItCannotRead`、`TestSyncL1NodesFromL2StopsOnUnreadableTopic`、`TestTopicClosureL2RefusesUnreadableChild`、`TestTopicIDsBySceneL2RefusesUnreadableTopic`、`TestDeleteSceneRefusesWhileThePlanBucketIsUnreadable`、`TestMergeScenesL2RefusesUnreadableTopic`、`TestPlanNodeScansReportAnUnreadableNode`、`TestPrunePlanStageSkipsWhenTheTreeIsIncomplete`、`TestApplyGroupsRollsBackTheGroupWhenASinkRefuses`）。同族再补四处：读不动的共现边被「新建一条」回答，衰减读的时钟从零起算，且绕过「权重不比现值大就不写」那道守卫（`TestBuildHyperedgesReportsUnreadableEdge`）；子图起点读不回被答成「没有这个节点」（`TestQueryL3SubgraphReportsUnreadableStartNode`）；`DeleteGraphL3` 是这一族最后一个 bool 站点，跳过一个读不回的成员照样把图删了（`TestDeleteL3RefusesUnreadableNode`）；租户注册表丢掉解不出名字的记录，于是 `SubAgent` 会给一个已被占用的名字新建第二个空域（`TestSubAgentRefusedWhileATenantKeyWillNotResolve`）——现在只要有一条键解不出就拒「建」，`Open` 与所有已解析出的名字不受影响。两个话题写入口不再把因压成布尔（此前调用点面对 `false` 只能自己编一句 `NewError(ErrIO, "...", nil)`，「存量记录读不回」与「写失败」到宿主耳朵里是同一句话、同一个码）；一个融合组只要有一个成员引擎看不见就整组拒，不再拿看得见的几个算时间界、铸出一个「摘要说两件事、底下只沉一条」的父话题 (`TestApplyGroupsRefusesAGroupItCannotSeeWhole`)。<br>20. **读侧剩下的定序与拒绝改诚实**：跨话题的 `SearchL4` 此前按 `Seq` 排序，而 `Seq` 是**轮内**槽位号，于是 `Limit` 留下的是「槽位最多的那一轮」而不是最新内容，`Seq` 相同的记录又按扫描顺序出现，同一查询两次可给出不同子集（现在单话题仍按槽位，跨话题按记录自己的时间、以 id 收尾，`TestDomainWideL4ReadOrdersByTimeAndKeepsNewest`）。`Dream` 在压缩已落盘之后被取消，会整段跳过 L2Meta 重建，于是该域继续列出这一趟已经吞掉的轮次，直到缓存被回收或重开文件（取消检查点移到装回之后，`TestCancelledDreamReconcilesTheReadPath`）。图导入批次用一份「跳过读不回的槽」的扫描预载标签→图，于是导入一张被坏槽占着的标签会答成「这里没有图」并在同一标签下铸出第二张图，该域节点从此分属两个 id（播种与改名检查都改走严格扫描，且严格拒绝会把读不回的那条记录 id 带出来——引擎没有别的口能指出哪条坏了，`TestImportL3RefusesUnreadableGraphSlot`）。话题列举只保留镜像那条路，删掉生产上不可达的扫记录回退分支与它永远不会产生的 error，宽容版 `CollectAllTopics` 随之消失；一批次给多张图盖章失败时，合并出的错误行按图 id 升序而不是 map 顺序。 随它一起退役的还有图槽的来源：`HypergraphSlot.source` 的 kind 恒为 "manual"、另两个字段没有任何写入路径能设置，`SourceKind` 四个值里三个连写点都没有，于是那个形状、那份枚举与 `source` 键一并删除，旧文件里多出的键在解码时被忽略（对读这个键的宿主是 breaking）。同一批的「每图标题集 / 每图边键」此前是按图懒加载、且来自一份跳过读不回记录的列举，于是对一个读不回的节点，Merge 导入会答「这个标题还没有」，按位置式 id 原地覆写那条读不回的记录、还算进 CreatedIDs 当成新增；现在三张索引都在批次创建时一次建好、且走严格扫描——顺带把「K 张图各扫两遍全池」换成整池两遍。 上抛给宿主的错误一律带码：`ReadRecord` 把帧解码器的裸 `io.EOF` 译成 `ErrCorruption`（裸错误经 `api.CodeOf` 取到 0，而 0 是「成功」那一档），`Dream` 的「一个场景都没巩固成」带 `ErrLLM`。取消单列一档 `ErrCancelled`（5008）：Dream 的每个检查点、LLM 传输里被调用方撤掉的两种等待（请求在途、退避待重试）都报它，`ctx.Err()` 留在 cause 里；回复被输出上限截断带 `ErrLLM` 而 cause 仍是 `ErrTruncated`（升级预算的那次重试靠它判定），此前它带着 0 码到达宿主。`memhop_dream` 跑在 MCP 请求自己的上下文上，客户端走开后这一趟在下一个检查点停下，不再顶着一把域锁跑完整条流水线。 内容读取的定序说法各处统一：一个话题内看 `Seq`，跨话题看记录自己的时间、以 id 收尾，`Limit` 取该序末尾。门面注释、`L4Query` 文档、MCP 检索工具描述与两份指南此前都还写着「按 Seq 升序」，其中 MCP 那句把它和「只保留最新 N 条」并排，自相矛盾。 同名再调 `SubAgent` 现在换的是活域的 transport，而不只是留给未来重建的那张表：端点覆盖此前只在建上下文时生效，重连的宿主每一轮仍打在已被替换的端点上（旧 key 一失效就是每轮 `ErrLLM`）。 场景链三处断路补上：锚解不开的 `Search` 不再留下一条宿主指认不了的场景记录；级联删除把场景与话题记录的墓碑排到最后，中途被拒的一次仍可重试，而不是留下「记录已删、缓存还列着」的半成品；`SearchL4` 填了未定义的 `Kind`/`Type` 与写侧同口径拒绝，不再用空清单含糊作答。另把「未沉淀轮次不留残渣」限定到 `Search` 自己那一侧，并写明读取带回的 `role 3` 是融合组摘要的库自有标记。 LLM 那一层在记忆质量上收紧四处：合法 JSON 却整块没答的蒸馏回包不再抹掉画像那半区并捏出类型词；本次没交给模型的节点 id 在 `llmops` 内丢掉，不再让每次 Dream 都停在 L1 情感回填；任何输出预算不越端点声明的上限，截断升级正好用满那份余量；巩固 prompt 的目标条数随配置渲染且明写让位于「不同主题禁并」。两处无人读取的回包载荷（`l2_compression_needed`、每组 `scene_id` 回显）删除，退化的一成员组计入「提出但未应用」。 被合并掉的场景带走它自己的 L1 节点，衰减也会剪掉成员已不在本域的共现边——一次场景删除就此收敛，而不是留下一个还能与活场景配对几天的幽灵。空闲回收在同一个持锁区间内给摘掉的上下文打标，回收前一瞬取到它的调用方重取域，而不是写在一份已作废的缓存上。 |
| v1.6.3 | 2026-09-10 | 内容只住在 L4：一轮的对话与事件同层，L5 只剩计划树，`Update` 只蒸馏 | 1. **记录并入 L4**：`ArchiveSlot` 加 `Kind`（`KindUtterance`/`KindEvent`）、`Seq`、`EventType`、`NodePath`，归档 id 改为**位置式** `hash("content:"+话题+":"+seq)`，于是同 (话题, Seq) 重写就是原地覆写；那套「先枚举话题旧的归档、再给没重写到的打墓碑」差分连同它需要的第二份清单一起删除<br>2. **L6 只剩计划节点**：新记录类型 `core.PlanNode`（帧值 `0x0F`，随能力记录层退役空出来的号），`TrajectorySlot`/`NodeType*`/`PlanNodeRef` 消失；该轮格式版本落在 `0x000F`（本版本最终为 `0x0010`，见第 10 项），`0x000E` 及更早的文件 Open 时显式拒绝、无迁移（中间那版 `0x000E` 从未随 tag 发布；它们把事件存在一个已不存在的记录类型里，旧归档还把归属话题记在 `context_id` 键下）<br>3. **`AppendArchive(topicID, ArchiveSlot)` 是内容的唯一写入面**，`content.ValidateAppend` 是唯一校验闸：`Kind` 必须已定义，原文声明角色（user/agent/system，融合角色一律拒）与媒介，事件自己命名，`NodePath` 只属于事件，超预算拒写不截断（事件 4 KiB、原文 64 KiB）；校验严格排在任何写入之前，被拒的记录零留痕<br>4. **`Update(sceneID, topicID)` 不写内容**：读该话题已有的原文、按 `Seq` 序渲染成带说话者标签的转录、一次蒸馏。无可提炼内容即 `ErrInvalidQuery` 且不碰 LLM；提炼失败时宿主先前 append 的记录原样留着。删除：`TurnUpdate`、`turn.WriteArchives`、`llmops.ExtractTurnKeywords`<br>5. **公开面 26 → 25**：删 `AppendTrajectory` 与 `ReadTrajectory`（读取由 `SearchL4{TopicID, Kind}` 完全覆盖，与当年删 `GetArchive` 同判据）、新增 `AppendArchive`、计划写面的事件入参换 `ArchiveSlot`（该写面在第 10 项被 `PlanSet` 取代）、`api.TrajectorySlot` 退役、`RoleSystem` 上架而 `RoleDream` 下架、`TrajectorySessionSummary.Steps` 改名 `Events`、`SceneMessage.Seq` 暴露以便宿主把「被保留窗裁掉」与「少读一行」分开<br>6. **保留清理拆分、跨层级联退役**：`index/traj.go` 改造为条目带 `Kind` 的 `index/l4.go`，Dream 分 `l4_prune`（内容）与 `l5_prune`（计划节点，仍豁免在途树）共用 7 天 `ContentRetention`，节点过期不再带走正文。转录完整性判据改写为：索引点名却读不到 ⇒ `ErrIO`；话题在而内容为空或有洞 ⇒ 合法的过期终局<br>7. **MCP 24 个工具不变、名字变动**：`memhop_trajectory_append` → `memhop_archive_append`，`memhop_update` 入参收缩到 `{scene_id, topic_id}`，`memhop_trajectory_read` 留作便捷工具、实现改走 `SearchL4{TopicID, Kind:event}`，`memhop_archive_search` 补 `kind` 过滤<br>8. **明码标价的回退**：超 7 天的轮次与融合话题正文永久不可回读、只剩关键词轨（Dream 融合链从不回读 L4 原文，关键词轨本来就是唯一长寿产物）；融合摘要降为 7 天寿命的中间产物；宿主一轮的热路径从 1 次调用变 3 次<br>9. **字段与层号收敛**（同版本第二轮）：Dream 的 usage-feedback 阶段随场景读侧计数（`hit_count`/`last_hit_at`）一起退役，`topic_count` 本就没落过盘——三键全删，`api.SceneSlot` 收窄为 `{scene_id, scene_name, l3_id}`；`ArchiveSlot.ContextID` 改名 `TopicID`（字段与 JSON 键同改）；最后两个含层号的 id 命名空间去层号（`l1:`→`scene-node:`、`l4:`→`content:`）；计划层接管空出的 L5 号位，认知栈由七层收敛为六层 L0–L5（`RecL5PlanNode` 帧值不变、`l5_prune`、`internal/l5.go`、`repo/l5layer.go`），格式版本 `0x000F`；`L4Query` 新增 `NodePath` 条件（不与 `TopicID` 同填即拒），公开面仍 25 + 8、MCP 仍 24<br>10. **计划层重做**：一轮的树**一步一步写**——`PlanCreate(topicID, title) → seq`、`PlanNodeAdd(topicID, parentSeq, title) → seq`、`PlanNodeUpdate(topicID, PlanStep{Seq, Status, Title, Summary})`，顶替 v1.6.2 的 `PlanCommit`（后者每一步都强制绑一条事件，因而根本没法重述计划）。节点只因某个创建调用把它建出来而存在：事件指向树上没有的步骤会被拒而不是长出一枝，`PlanNodeAdd` 的父序号不在树上即 `ErrNotFound` 而不是补出一条链——这同时关掉了 v1.6.2 自认的那处代价「打错一段路径凭空多出一棵树」。一个步骤由**该轮内库顺序发号的序号**寻址（`1, 2, 3 …`；`ParentSeq` 说它挂在谁下面，0 即根），记录 id 随之派生（`hash("plan:"+话题+":"+序号)`）；序号是一轮之内的**地址**而不是记录 id，因此它以一个普通整数越过门面。`SearchL4` 的步骤条件仍按**子树**取（一步拆开后它做的事在孩子身上），只是分支改为沿父子链接求闭包，不再拿字符串前缀去匹配。节点去掉 `plan_type`；状态词表是 `in_progress` / `done` / `failed`——`pending` 与 `running` 都不在了，而新建的步骤本身就是 `in_progress`，所以只有重述一步时才要给它状态；父摘要只在**全部直接子到达终态**后才折，步骤被重开时清掉它的 `FinishedAt`。没有步骤删除接口：放弃一步的手段就是不在此后的轮里再创建它，陈旧的树随保留窗一起被收走。格式版本 `0x0011`（`0x0010` 及更早拒绝打开、无迁移）；公开面 27 + 8、MCP 仍 24 |
| v1.6.2 | 2026-09-07 | 计划事件 `EventType` 归宿主命名（词表退役） | 1. **`planEventTypes` 名单与 `plan.ValidateEvent` 删除**：计划绑定事件（`nodePath` 非空）的 `EventType` 与裸轮次事件同口径——任意非空宿主命名即接受、原样存回。判据是这条约束不挣自己的饭钱：引擎从不按 `EventType` 分支（全仓非测试引用只有那次查表、`ReadTrajectory` 的字段回显、结晶 prompt 的一行格式化），名单唯一的行为就是拒写；代价全在宿主侧——事件名被拒即轨迹静默少一条（沙箱裁决反问类审计事件正属此类）<br>2. **校验点回归一处**：`trajectory.ValidateEvent`（非空 `EventType` + `Timestamp > 0` + payload ≤ 4KB）。`AppendTrajectory` 的计划分支与 `PlanCommit` 仍在 `EnsureNode` / `UpdateNodeLocked` **之前**调用它，被拒的写零留痕（「校验晚于改树」那条顺序不变量原样保留）<br>3. **交付面口径统一**：MCP 的 `memhop_trajectory_append` 本就声明 `event_type` 由宿主自定，而计划写面不在 MCP 工具面上——本轮把 Go 面对齐到交付面已有的口径，24 个工具不变<br>4. **那一轮不增删方法、不触及记录布局**：`event_type` 是记录内的 JSON 字符串字段，收紧与放宽都不改变帧结构（方法数与格式版本在随后未发布的轮次里再次变动，见 v1.6.3 行）；**对宿主是放宽方向**，无需改调用点即可受益<br>5. 测试：`TestPlanEventVocabularyRejectsUnknown` 改写为 `TestPlanEventNamesAreHostOwned`（钉住「宿主命名被接受 + 名字未被改写 + 空 `EventType` 仍在建链前被拒」），`api`/`test` 面三处拒写断言改用空 `EventType` 触发；决策档案 `notes/implemented/simplification/2026-09-07-plan-event-vocabulary-retirement.md` |
| v1.6.1 | 2026-09-06 | 公开面再收敛（34→32）；内置说明书卡删除；公开面按使用者分两类；L5 记录层退役（目录即能力） | 1. **`ActivateCapability` 删除**：repo 层的激活本就是「读卡→置 active→写回」，与 `UpdateCapability(id, CapabilityPatch{Status: &active})` 的状态补丁完全重合——同一能力只留一个实现，激活语义并入 `UpdateCapability`（并入后同过卡片校验，夹具缺 summary/resources 的裸卡激活会被拒）<br>2. **`PlanReplace` 删除**：`SyncPlanTree(planID, nil)` 清整树（节点与绑定事件全删、保留 planID、事件 Seq 归 1），播种下一个任务 = 单节点同步（空 status 落 pending）；`0000000000000000` 拒绝语义不变（ParsePlanID 在 nil 判断之前）<br>3. **MCP 31 → 30 工具**：`memhop_capability_activate` 并入 `memhop_capability_update` 的 `status` 参数<br>4. **内置说明书卡删除**：九张常驻内存的说明书卡（`internal.BuiltinCards` 代码组装、只列不存）整体下线——对标主流能力面设计（Claude Code / OpenClaw 的 skill、MCP），能力池只装 **LLM 可触发单元**，「库自身方法怎么调」是文档不是能力：它存在的唯一结局是被宿主 skip（meowagent）或与 MCP 工具描述双份重复（约 28KB 随每次全量 `ListCapabilities` 流动）。池里只剩宿主导入的卡、结晶草稿与 `plug/` 注入；方法用法说明归 `go doc api.Session` / `INTEGRATION_GUIDE.md`。随机制一并删除：ListCapabilities 的 stored-shadow 去重段、结晶折回的保留名检查（`ApplyCandidate` 删 `reserved` 谓词）、`CapabilityOriginBuiltin` 常量——**对消费方 breaking**：meowagent 的 `Origin == builtin` skip 成死代码、其编译在自身适配轮次前会断<br>5. **公开面按使用者分两类（仅注释与文档，代码零结构变化）**：32 个会话方法声明为任务面（22 个——宿主每轮驱动 + LLM 工具绑定的全部读写）与组装/管理面（10 个 + DB 8 个——会话边界与管理通道，不做成 LLM 工具）；由 `api/surface_public_test.go` 分组清单钉住，`go doc api.Session` 与 `INTEGRATION_GUIDE.md` §8 同步<br>6. 格式 `0x000B` → `0x000C`（`0x0F` 能力帧型随记录层退役）；`0x000B` 及更早文件 Open 时显式拒绝、无迁移；MCP 30 → 24 工具（六个能力工具含 `memhop_capability_get` 随记录层一并删除）；打 tag 后 meowagent 侧跟版适配（调用点：折叠两项的 `ActivateCapability`/`PlanReplace`、第 4 项的 `CapabilityOriginBuiltin`、第 7 项的 L5 面）<br>7. **L5 记录层退役（目录即能力）**：引擎不再存储能力卡——唯一事实源是宿主自有的 `plug/<包>/capability.json` 目录（宿主自扫自装配；结晶草稿落 `plug/draft/`；activate = 文件转正；变更重启生效）。删除：五个会话方法（`ImportCapability`/`UpdateCapability`/`DeleteCapability`/`ListCapabilities`/`RecordCapabilityUsage`——公开面 32→27，任务面 22→20、管理面 10→7）、八个类型（`Capability`/`CapabilityImportResult`/`CapabilityListQuery`/`CapabilityPatch`/`CrystallizeResult`/`CrystallizeDetail` 与 `CapabilityStatus`/`CapabilityOrigin` 及其常量）、整套 `plug/` 注入机制（Open 时扫描、`FileHash` 水位、重注入零写入、patch 幸存）与 core 记录层（`RecL5Capability`、typed 读写器、`0x0F` 帧型）。保留为纯能力并自 `api` 导出：`CapabilityFormatV4`、`ParseCapabilityPackage(data, source)`、`ValidateCapabilityCard(card)`——v4 格式的唯一事实源；`Crystallize(ctx, turnID, existing []CapabilityImport)` 转纯提炼：轨迹轮 + 宿主现有卡清单进、候选列表出（`Action` create/reuse/merge，`ReuseID` 从 16-hex id 改指已有卡名，未过校验的候选原样返回——过滤职责移交宿主）；引擎零落盘。`PromptCard` 迁入 capability 包并删去宿主无法复刻的 `id:`/`package:`/`usage:` 三行。**对消费方 breaking**：meowagent 的五个能力调用点与八个类型在其自身适配轮次前编译会断 |
| v1.6.0 | 2026-09-04 | 接口去 fallback；L3/L5 文件级公共池；L5 统一卡 + `plug/` 自动注入 | v1.5.0 tag 打在了 API 面完成之前——require 它会报 `undefined: api.DefaultAgentID / api.NewPlanID / api.ScenePatch`，本版本交付补齐后的完整线。1. **ID 一律库内发号**：`api.DefaultAgentID` 指隐式域，`api.NewPlanID(name)` 铸计划 id，`api/ids.go` 四个 Format/Parse 桥删除<br>2. **公开面合并**：`SetSceneName`+`SetSceneL3ID` → `UpdateScene(id, ScenePatch)`、`PlanAppend` → `AppendTrajectory(key, nodePath, ev)`、`ListScenesByL3` → `ListScenes(l3ID)`；删除 `Lock`/`Unlock`/`Session.Checkpoint`/`IsClosed`/`AgentID`/`DistillL0`/`ListPlans`/`GetArchive`/`GetCapability`（现为 34 + 8）<br>3. **接口不允许 fallback，有问题就返回 error**：LLM 关键词输出不可解析即 `ErrLLM`、该轮零留痕（gse/分词兜底删除，直接依赖 5 → 4）；被拒的 `PlanCommit` 树与事件零变化；超预算轨迹 payload 拒绝而非截断；`DeleteCapability`/`DeleteAgent`/`MergeScenes` 对未知 id 报错而非静默成功；读路径瞬时错误一律上报不再跳条<br>4. **图导入闭环**：Skip 重导补建边、既有图槽复用（改名在重导后存活）、整批先校验、`L3Relation.Titles` 声明真 N 元超边、`UpdateL3` 撞名拒绝且重导路由确定、删图级联清场景锚点、`SyncPlanTree` 删分支镜像进轨迹索引<br>5. **格式**：本轮只改派生与校验，不改记录布局；MCP 工具面不变（31）；每条修复都以把本仓导进自身 L3 超图实测过，逐条记录见 CHANGELOG<br>6. **L3 知识图升级为文件级公共池**：一个文件所有 agent 域共享同一份知识图——记录迁入保留共享域 `core.SharedPoolAgentID`（宿主不可见、`DeleteAgent` 不动池），场景锚点校验改读公共域，`DeleteL3` 分两阶段（公共锁内删图，释放后跨域清锚）<br>7. **L5 统一卡 + 能力文件级公共池 + `plug/` 自动注入**：一卡一形态——名称 + N 个功能条目，无卡片级 `Type`/`Workflow`，动作链归宿到 `composite` 条目的 `config`（`{"steps":[{"tool":...}]}`）；`memhop-capability/v4` 插件包文档（一个文件 = 一个插件包，包名随卡落 `Package` 字段）；`ImportCapability` 返回逐卡处置（created/updated/errors），`CapabilityListQuery` 新增 `Package` 过滤；L5 记录迁入同一公共池；每次 Open 自动导入 `<meh 同目录>/plug/<包>/capability.json`（坏包 Warn 跳过不阻断）；格式 0x0009 → 0x000B，0x000B 之前的文件 Open 拒绝、不迁移 |
| v1.5.0 | 2026-09-01 | L2 换轨：场景 = 宿主会话，轮次 ID 归库管 | 1. **`Search` 读场景 + 开一轮**：入参只剩 `{scene_id, l3_id}`，都可空——`scene_id` 空则库铸一个新场景（名字由库生成 `session:<id>`，`scene_name` 入参删除，改名走 `UpdateScene`），非空但场景不存在则 `ErrNotFound`，`l3_id` 只在新建时挂项目域。返回 `{profile, profile_brief, scene, topics, new_topic_id}`：场景本体 + 该场景 depth-1 话题集（本轮该注入的上下文）+ 本次读取为将要进行的这一轮铸出的话题 id，取 `hash("turn:" + 场景:轮次)`，轮次来自新增的场景级 `turn_seq` 计数。**删除** `contexts`/`associated_contexts`/`auto_create`/`directed_l2_id`/`directed_l3_id`、`ctx` 参数以及读路径上全部 LLM/embedding/打分。这次计数写是读的唯一写且**必须成功**（写失败就报错，不再降级返回可能重复的 id）<br>2. **`Update` 把整轮沉淀进那个 id**：`TurnUpdate{scene_id, topic_id, user_text, user_ts, user_type, agent_text, agent_ts, agent_type}` 返回同一个 id；一次提炼排在所有写入之前，LLM 失败零留痕；`topic_id` 空/非 hex/全零 → `ErrInvalidQuery`；同 id 重放覆盖这一轮并给被取代的那两条原文打墓碑。双时间戳退为纯时序字段，不再参与身份派生<br>3. **N:N 追加面删除**：`AppendL4Message`（多条消息进一个话题）与 `RefineTopicKeywords`（按全量原文重算该话题）整体下线——一轮的 L4 原文恒为两条，两条之间的事归本轮 L6 轨迹。L4 内容类型在写入侧声明（`user_type`/`agent_type`，零值即 `text`，非文本侧把媒体路径或 URL 当作 text 存），读回侧 `SceneMessage.type` 报告类型<br>4. **L6 轨迹按话题 id 绑定**：`AppendTrajectory` / `ReadTrajectory` / `Crystallize` 的轮键就是该轮话题 id，事件的 `topic_id` 由该键回填，宿主不再自派生轮键；计划绑定事件继续用计划 id，跨轮折叠（连带 `TrajIndex.TopicEvents`）删除，跨轮聚合单位回归为「一个计划」<br>5. **话题单轨**：删除 `user_keywords`/`agent_keywords`/`centroid_page_ref`/`l3_refs`，只留 `fused_keywords`（磁盘字段名不变）；Dream 压缩、L1 超边、宿主注入共用同一轨，不设摘要字段（原文在 L4）<br>6. **场景 ID 由库铸**：`NewSceneSlot(sceneID, name)` 不再哈希名字，`CreateSceneL2WithID` 幂等复用既有场景；`timestamp:文本` 自动命名场景的路径消失<br>7. **检索子系统整体删除**：`internal/cap/scenefind`（BM25+向量+实体三通道、RRF、场景加分、L1 扩散）、话题质心、`RecVecCentroid`、`Encoder`/`HttpEncoder`/`OpenMultiWithEncoder` 与 encoder 配置全部移除——**引擎不再联系任何 embedding 服务**<br>8. **`VectorDim` 从配置面删除**（连带 `CheckVectorDim`/`ErrVectorDimMismatch`/MCP `--vector-dim`）；文件头偏移 6 的两字节改保留位，格式版本仍 `0x0009`<br>9. **死索引岛整体删除**：失去读者的 BM25 / 实体模糊 / BK-tree / L3 索引（L3 节点查询本就是记录扫描）、两个 Levenshtein 实现与零调用的 `common.FormatIDs`<br>10. **巩固触发改轴**：`activeScenes`/`Capacity` 窗口与 `ActiveSceneIDs`/`HasActiveScenes` 删除，`Update` 在场景 depth-1 话题数超 `SceneDreamTopicThreshold`（默认 24）时后台调度该场景 Dream；`Dream(ctx,"")` 遍历域内全部场景<br>11. **不 bump 格式版本**：`TopicSlot.UnmarshalJSON` 在解码点把旧库两轨归一进 `fused_keywords`，`turn_seq` 是增量字段（老场景解码为 0，首次读取即开第 1 轮）。轮次话题 ID 走 `"turn:"` 命名空间（与 Dream 融合节点分域）；删除零调用的 `ComputeTopicIDForText` 与死字段 `SceneNode.VectorPageRef`<br>12. 场景归并不再发生于 Dream（会删掉宿主正持有的 sceneID），`MergeScenes` 保留为显式接口。**交付面**：MCP `memhop_search` 去掉 `scene_name`、出参带 `new_topic_id`，`memhop_update` 新增必填 `topic_id`，轨迹类工具的键即该话题 id，`memhop_status` 改报 `scene_count`，删除 `memhop_scene_active_list`、新增 `memhop_scene_rename`（30 → 31 工具）；**L0 画像的字段所有权由库强制**——`UpdateL0` 只写宿主四项，`EmotionState`/`MBTI` 从库里现值继承、`updated_at_ms` 由库戳写，故 `memhop_profile_update` 退化为纯转发，Go 宿主拿到同一份保证；`SyncPlanTree` 的空白 `Title`/`PlanType`/`Status`/`Summary` 继承节点现值而不是把步骤退回，Dream 单个融合组失败回滚本组已写记录，瞬时读失败不再被报成「记录不存在」；DSH 插件面（`dsh/`、`dsh-adapter/`）在本版本内一并退役，交付只剩库与 MCP server13. **公开面按「宿主是否真的用得着」重排（Session 43 → 33，DB 9 → 7；发布后的接口审查又补进 `DeleteL3Nodes` 与 `CompactTo`，现为 34 + 8）**：只以 `api/` 为标尺审计，MCP 有工具不再是留一个方法的理由。随实现链删除 `Lock`/`Unlock`、`Session.Checkpoint`/`IsClosed`/`AgentID`、`DistillL0`、`ListPlans`、`GetArchive`、`GetCapability`、`api.CapabilityImport`；合并 `ListScenesByL3`→`ListScenes(l3ID)`、`SetSceneName`+`SetSceneL3ID`→`UpdateScene(id, ScenePatch)`、`PlanAppend`→`AppendTrajectory(key, nodePath, ev)`。**id 一律库内发号**：四个 `Format*`/`Parse*` 桥删除，改为 `api.DefaultAgentID` 与 `api.NewPlanID(name)`（`plan:` 命名空间，撞保留 0 即借位，库自己发的 id 自己一定认）。四处静默失败修掉：`SearchL4` 只填 `TopicID` 或只填 `Type` 返回空集（改为「填了就 AND」）；场景改挂回 `nil` 却什么都没改（现在须 `Force`，且锚定目标须存在）；`Dream` 传未知场景返回零值报告（现在 `ErrNotFound`）；`UpdateScene` 漏在门面重写而把 uint64 id 抬给宿主（补映射，并改为**返回写入后的场景**，`api/surface_public_test.go` 新增反射守卫：宿主可见签名里任何 uint64 id 字段即失败）<br>14. **发布后按层接口审查（逐条回源码 + 用本仓 L3 超图把公开面导入成图实测）**——修掉两处会把「传错 id」变成静默数据损失的 core 层缺陷：**记录读取现在校验记录类型**（`GetL3(节点 id)` 之类跨类型读取一律 `ErrNotFound`，不再出现「改名把节点记录改写成图槽」），**超边身份含 kind**（此前同一对节点的 `related` 与 `part_of` 互相覆盖，只剩最后写入的那种；重复导入按「排序成员 + kind」去重，对旧文件里 pair-only 哈希的边同样幂等）。公开面补齐：`api.Session` 为 12 个只靠提升的契约重方法补上**门面注释**（此前 `go doc` 只看得到 22/34 个方法），`ImportL3` 结果新增 `graph_ids`（图 id 由 Domain 哈希派生，此前宿主只能按名字列表反查），新增 `DeleteL3Nodes`（节点级删除 + 级联其超边）与 `MultiAgentDB.CompactTo`（墓碑删除后的空间回收出口）。语义收紧与一致性：`Update` 只能沉淀**该场景已开出的轮次**（写 Dream 融合节点、跨场景 id、宿主自造 id 一律拒，重放与乱序结算照旧允许）；`QueryL3Nodes` 的 ids/keyword/node_type 改为按 AND 组合（此前是优先级 switch，同时传会静默忽略两个），`SearchL4` 关键词改为忽略大小写并新增 `Limit`（只留最新 N 条命中），MCP `memhop_archive_search` 缺省即截 50 条；`SceneContext` 的 depth≤2 平铺是刻意为之（Dream 下沉的原文只有这条读路径取得回），契约注释随之改正。删除公开面上的死字段：`HypergraphNode.importance`、`HypergraphEdge.weight/label`、`ArchiveSlot.metadata` 与 `RoleSystem` 常量（引擎无任何写入路径，磁盘字段保留以解旧文件）；事件写入侧额外清零 `plan_type`（该字段按记录契约只属于计划节点）。安全与交付面：`memhop_capability_import` 的路径改由 `--capability-dir`（缺省 `--db-dir`）经 `os.Root` 锚定，越界即拒；`memhop_dream` 的 `scene_id` 改为可选（此前 MCP 侧调不到全域巩固）；`memhop_knowledge_import` 的描述与 `ListL3` 说明书卡片此前各有一处与真实返回不符，已改；卡片资源名与 `memhop_*` 工具名的对应关系写进各卡片 summary；MCP 侧删除 api DTO 全量转 hex 后已成 no-op 的 70 行 `idsToHex` 转换（其 4 个键名在公开 DTO 上根本不存在）。新增枚举词表漂移守卫（MCP `content_type` 映射与卡片里的 `related|causal|…`、`text|image|…` 逐一对齐引擎 `String()`）。**格式版本仍 0x0009**：本轮只改派生与校验，不改记录布局。 |
| v1.4.2 | 2026-08-31 | L6 计划树 + L2 目录归属 | 1. L6 承载任务树：`TrajectorySlot.NodeType` 区分轮次事件与计划节点，节点 ID 由 `HashPlanNode(planID, nodePath)` 在 `plan:` 命名空间下稳定派生，事件经 `PlanNodeRef` 挂节点<br>2. 三形态 `PlanAppend` / `PlanCommit` / `PlanState`，另加 `PlanReplace`（重规划、保留 planID）、`SyncPlanTree`（整树快照对齐，不产生 `plan_step`）、`ListPlans`（重启恢复）<br>3. **Model A 显式折叠**：父节点仅由宿主显式 commit 为 done；每次 commit 后把已 done 子节点摘要按 `NodePath` 数值序自底向上汇总进父摘要，不覆盖宿主写的父摘要<br>4. `PlanTree.Roots` 是**森林**（顶层步骤各为一根；父记录缺失的节点提升为根而非丢弃）<br>5. L2 场景 → L3 目录域（N:1）：`SceneSlot.L3ID`、可选 `SearchQuery.L3ID` 前置筛选并在命中时回填、`ListScenesByL3`、`SetSceneL3ID(sceneID, l3ID, force)`（默认写一次，force 纠错、空值清除）<br>6. 域级 `planCache`，`PlanState`/`ListPlans`/rollup 不再每次全扫引擎<br>7. api 导出常量：`Role*`、`NodeType*`、数值 `Status*`（读侧）、字符串 `PlanStatus*` 与 `PlanStatus` 类型（写/查询侧）；新增第五态 `running`<br>8. 写入面强制权威语义：所有计划节点字段与 `Seq` 在写入时被覆盖，计划事件 `EventType` 受白名单约束<br>9. 加固：`0000000000000000` 为裸事件 `PlanID` 保留值，五个计划入口一律拒绝（此前 `PlanReplace` 传全零会删掉全域轨迹事件）；Dream 的计划豁免收窄为「7 天窗口内仍活动」，被放弃的计划不再无限堆积<br>10. 无格式变更（仍 `0x0009`，字段为 JSON 增量，v1.4.1 文件直接打开），MCP 工具集不变（31）——计划面本期**仅 Go module 可用** |
| v1.4.1 | 2026-08-28 | 类型契约清理：hex 出参 DTO、L0 画像 v2、L3 超图激活 | 1. api 出参 DTO 改为真实 struct——所有 ID 字段以 16 位 hex 字符串出参（含 `SearchResult.NewTopicID` / `AppendL4Message` 返回值 / `AgentID()`），新增 `api.FormatID` / `api.ParseID`<br>2. L0 画像 v2（`FormatVersion 0x0009`）：字段所有权（Name/Role/Preferences 宿主独占，Personality 宿主播种 + Dream 蒸馏演化）、typed `EmotionState`/`MBTI` 蒸馏信号、删除死字段 lexicon/style_traits<br>3. 库内零 hex 往返（repo 层 ID 入参 uint64 化，质心哈希 `HashBytes` 直算）<br>4. L3 导入新增 `source_ref`（位置引用）与 `related`（同图内按标题建超边，两阶段解析支持前向引用、重导入幂等；结果含 `edges_created`，导出 `L3Relation` 类型）<br>5. `AppendL4Message` 新增 `contentType`（导出 Content* 七常量；text/document/code 存原文，image/audio/video 存路径或 URI，mime/size/sha256 走 Metadata）、`L4Query.Type` 过滤与 MCP `archive_search` 的 `content_type` 参数<br>6. L6 每轮一条轨迹：SessionID 改为轮键（search 开轮、update 收轮），事件带 `TopicID` 支撑跨轮结晶，对外面收敛为追加+查询（删除 `TrajectoryStats` / `DeleteTrajectory` / `PruneTrajectory`，33 → 31 工具），Dream 新增 `l6_prune` 自动清理 7 天前事件<br>7. distill/consolidate LLM 解析失败补一次格式约束重试<br>8. **破坏性变更**：`FormatVersion != 0x0009`（即 ≤ 0x0008）的 `.meh` 文件在 Open 时被拒绝，无迁移 |
| v1.4.0 | 2026-08-26 | 多 agent 记忆数据库 | 1. 一个 `.meh` 文件承载多个完全隔离的 agent 域：记录帧新增 `agent_id`（26 字节帧头），引擎索引与快照（0x02）按 agent 分域，租户注册记录把名字映射到稳定的 crypto/rand agentID<br>2. `api.OpenMulti` / `AgentSession` / `CreateAgent` / `ListAgents` / `DeleteAgent`；`Open` 对单 agent 宿主零改动（默认域）<br>3. 业务层重构为按 agent 的 `agentContext` + 域级锁（同 agent 串行、跨 agent 并行）、空闲域内存回收与域化 Dream 管线<br>4. L7 轨迹层改编号为 **L6**（认知层收敛为 L0–L6）<br>5. MCP registry 共享单个 `MultiAgentDB`（单文件 `<db-dir>/memhop.meh`），`os.Root` 锚定 db 目录<br>6. 删除重复结构体/转换层（`topicSlotJSON`、`topicToL2Meta`、单元素切片包装）<br>7. Go 1.23–1.26 标准库现代化（`iter.Seq2`、`unique.Make`、`os.Root`）<br>8. 零新增依赖<br>9. **破坏性变更**：`FormatVersion <= 0x0007` 的旧 `.meh` 文件在 Open 时被拒绝，无迁移；`api.DB` 上提升自 `internal.DB` 的方法新增 `agentID` 参数（门面方法签名不变），`Lock()` 对已关闭的 DB 会 panic |
| v1.3.4 | 2026-08-26 | L5 工具声明同构 | 1. `memhop-capability` 格式升级 v3：`ResourceRef` 的 `description` 改名 `desc` 并新增 `input`（JSON Schema 字符串）/`output`——工具声明字段与宿主工具规格（meowire `ToolSpec`）完全同构，宿主纯字段拷贝即可投影、零格式转换<br>2. `WorkflowStep` 新增 `args`——动作链参数官方化（不再依赖私有 config 格式）<br>3. 结晶 prompt 输出 v3 形状（`type`/`resources` 取代 `kind`/`manifest`）<br>4. `validateCapabilityImport` 强制资源名非空并校验 `input` 为合法 JSON<br>5. **破坏性变更**：v2 卡导入被拒绝（format 必须为 `memhop-capability/v3`）；旧版本写入的存量能力记录读取时 `desc/input/output` 为空<br>6. 内置能力工具箱（`capabilities/*.json`）全部重写为 v3 并携带真实 JSON Schema |
| v1.3.3 | 2026-08-26 | 检索评分归一化 + 参数面收敛 | 1. vector floor 从“覆盖式垄断”改为“仅抬升未过线场景”（floor = threshold + cosine×0.5）：真实信号（RRF + 关键词重叠 + 加分）决定排序，语义兜底保留<br>2. `MemHopDefaults` 从 24 字段收敛到 3 个业务开关（`Capacity` / `DreamCompressMinTopics` / `SearchDreamContextThreshold`）；删除 4 个死字段（`MaxResults` / `DefaultTimeoutSecs` / `DefaultMaxOutputTokens` / `MaxDepth`），16 个调优常量移入包级私有 `internal/tuning.go`<br>3. `TopScene` / `SpreadingActivation` / `applySceneBonuses` / `rrfFuse` 签名去掉 defaults 参数<br>4. **破坏性变更**：引用被删字段的宿主需同步清理<br>5. 格式版本不变（仍为 `0x0007`）<br>6. MCP 工具集不变（32 个）
| v1.3.2 | 2026-08-26 | API 修复：异步 Dream + 删除接口 + Update 简化 | 1. Search/Update 不再被内部触发的 Dream 阻塞（后台 goroutine、按场景 in-flight 防重入、Close 取消在途 Dream）<br>2. 新增 `DeleteTopic`（子树闭包 + L4 + 索引 + 父话题 ChildrenIDs 修剪）与 `DeleteScene`（场景 + 全部话题 + 原文 + L1 节点 + 激活集）用于记忆纠错<br>3. `Update` 返回值由 `(bool, error)` 简化为 `error`<br>4. `SearchResult.ProfileBrief`——紧凑画像摘要（name/role/偏好/风格/情绪，带边界）<br>5. 格式版本 不变（仍为 `0x0007`）<br>6. MCP 工具集不变（32 个）
| v1.3.0 | 2026-08-26 | L1 场景超图 + 扩散激活联想 | 1. Dream 在场景间创建真实的 `RecL1Hyperedge` 共现边（关键词重叠 Jaccard ≥ `L1EdgeMinSimilarity`）；Search 的 `AssociatedContexts` 由空转的同场景列表替换为图遍历（每跳激活 × 边权 × 衰减系数，≤ `L1EdgeMaxHops`，取 Top `L1AssocMaxScenes` 个其他场景）<br>2. L6 场景使用记录删除——命中计数并入 L2 `SceneSlot`（`HitCount`/`LastHitAt`）<br>3. `L1ReverseIndex`（含快照字段）与 4 个 L1 死函数删除，联想变为纯存储层图读取<br>4. `.meh` 格式升至 `0x0007`——0x0006 文件 Open 时被拒绝，不迁移<br>5. 新默认项：`L1EdgeMinSimilarity`（0.15）、`L1EdgeMaxHops`（2）、`L1ActivationDampening`（0.5）、`L1ActivationThreshold`（0.05）、`L1AssocMaxScenes`（3） |
| v1.2.7 | 2026-08-25 | 宿主对齐 + 双语集成指南 | 1. `Search(ctx, q)` 与 `RefineTopicKeywords(ctx, id)` 接收 context（可取消 LLM 关键词提取、编码调用与内部触发的 Dream）<br>2. `api` 导出 `LlmConfig` / `MemHopDefaults` / `TopicSlot` / `ResourceRef` / `CrystallizeDetail` / `TrajectoryStats`<br>3. 新增 `TrajectoryStats`（会话级 L7 统计）+ `memhop_trajectory_stats` MCP 工具（31 → 32 工具）<br>4. `CrystallizeResult.Details`——逐候选 create/reuse/merge/skip 处置明细<br>5. `AppendL4Message`（纯 L4 追加，不调 LLM）<br>6. 活跃场景容量策略：Update 在达到 Capacity 时对最老场景触发 Dream（带可压缩性预检）；`SearchDreamContextThreshold` 零值守卫<br>7. 仓库根目录新增双语集成指南（`INTEGRATION_GUIDE.md` / `INTEGRATION_GUIDE.zh.md`） |
| v1.2.5 | 2026-08-20 | MCP server 重写 | 1. `cmd/memhop-mcp` 对照 `api` 公开门面完全重写（v1.2.4 曾删除）：31 个 MCP 工具与 `api.DB` 方法一一对应<br>2. 多租户 HTTP 暴露——SSE + streamable-http（2025-03-26 spec、无状态），租户按 URL 路径 `/mcp/<tenant-id>` 隔离到独立 `.meh` 文件，懒打开注册表 + 首开互斥<br>3. 工具输出中记录 ID 统一 16 位 hex 字符串序列化（uint64 JSON 数字在 JS/TS 宿主丢精度）<br>4. 租户 ID 白名单 + 路径逃逸拦截（防御纵深）<br>5. LLM 凭据仅环境变量（无 CLI flag）<br>6. go-sdk v1.7.0 回归直接依赖（3→4）<br>7. config/registry/tools/streamable 离线测试 + 多租户 SSE 冒烟<br>8. 代码清理：删除冗余枚举 JSON 辅助函数（`~uint8` 默认 JSON 行为等价）、`CodeOf` 迁移 Go 1.26 `errors.AsType`、cosine 标量循环（1024 维快 2.7×）、删除 `internal/repo/open.go` 转发层（17 函数 + 8 alias，internal 直调 core/index）、Update 移除 `ParseID→FormatHash` 往返转换 |
| v1.2.4 | 2026-08-19 | api/ 公开门面 + internal/ 平铺 | 1. 公开 Go API 从根包迁移至 `github.com/qyiun666/MemHop/api`（根目录 `memhop.go`/`types.go` 移除）<br>2. `internal/sub/` 上提平铺为 `internal/`（`package sub` → `package internal`），`internal/sub/repo` → `internal/repo`，`internal/sub/common` → `internal/common`<br>3. `cmd/memhop-mcp` 移除（v1.2.5 重写回归）<br>4. 构建配置同步（Makefile fmt、pre-commit hook、CI gofmt）<br>5. 破坏性变更：直接 import 根包的宿主需切换到 `/api` |
| v1.2.3 | 2026-08-18 | MCP 兼容性修复 + DSH 接入 + 检索质量修复 | 1. MCP 工具 schema 修复（无参工具 `properties` 不再输出 null，兼容严格 MCP 客户端）<br>2. 工具输出 ID 全部改为 16 位 hex 字符串（uint64 JSON 数字在 JS/TS 宿主丢精度，`new_topic_id` 回传失败已修复）<br>3. 新增 `--transport streamable-http`（2025-03-26 规范，Stateless 多租户，DSH 的 dsh-mcp-client 支持）<br>4. DeepSeek Harness 接入文档与引导词（`docs/dsh/`）<br>5. streamable-http 冒烟测试<br>6. 关键词提取 prompt 全面优化（语义完整 + 同义词变体 + 短语）+ Search 按相关性返回全部相关话题（移除场景上下文截断），LoCoMo 召回 0.392 → 0.668、实体命中 0.284 → 0.877 |
| v1.2.1 | 2026-08-16 | MCP Server + L5 能力层 | 1. 新增 `cmd/memhop-mcp` 二进制：多租户 SSE MCP Server（官方 go-sdk v1.7.0），将全部公开 API 映射为 28 个工具（search/update/dream/checkpoint/status、画像、场景、知识图谱、归档、能力、轨迹/结晶）<br>2. 租户路径隔离 `/mcp/<tenant-id>`<br>3. 优雅退出时落盘快照<br>4. 离线 SSE 冒烟测试（`make test-mcp`）<br>5. 使用文档见 `docs/mcp/`（本地）<br>6. L5 插件层重构为能力层（`memhop-capability/v1`：manual/atomic/composite 三种 kind，`ActivateCapability` 实现 draft→active 生命周期，指纹去重，Crystallize 产出 create/reuse/merge 候选）<br>7. 内置能力工具箱（`capabilities/`，embed 只读，Open 时自动挂载）<br>8. `Update` 返回 `(bool, error)`<br>9. `.meh` 格式升至 `0x0005`——0x0004 文件（v1.2.0 插件记录）在 Open 时被拒绝，不迁移<br>10. 编码器健康检查要求端点根路径 2xx 响应 HEAD（无 fallback）<br>11. 活跃场景受 `Capacity`（默认 7，最旧场景被移出 Dream 目标）限制<br>12. `RecordEnd` 头字段 + A/B 头损坏恢复 |
| v1.2.0 | 2026-08-14 | L5 插件层 | 1. L5 动作链 → 插件槽位（PluginSlot + 结构化五段 Manifest：技能 / MCP / 工具 / 提示词 / 服务）<br>2. 仅路径导入 `ImportPlugin`，移除手工写入 Create/Update<br>3. Crystallize 从 L7 轨迹按类型分派插件<br>4. `SearchResult.Crystals` → `Plugins`<br>5. 八层架构（L0–L7）文档 |
| v1.1.0 | 2026-07-27 ~ 08.11 | 架构重构 | 1. `internal` 分层重写（装配层 → sub → repo → core/index/common）<br>2. f16 → f32 单精度向量<br>3. 话题质心向量检索<br>4. 移除 `BatchStore`<br>5. `Dream(ctx)` 签名收窄为 `(bool, error)`<br>6. `.meh` 磁盘格式 `0x0004`，与 v1 数据不兼容<br>7. 集成测试按新 internal API 重建 |
| v1.0.0 | 2026-07-26         | 首个稳定版 | Go 重写，六层认知架构、V2 .meh 存储、BM25+向量+实体 RRF 检索、Dream 巩固管线、L3 超图社区发现。 |
| v0.54–v0.58 | 2026-07-16 ~ 07-23 | Go 重写 | 1. v0.58: 统一 RRF — 加性场景加分、三通道融合、移除 L6、atomic.Pointer<br>2. v0.57: Dream 收窄至 L0+L1+L2、LLM 加固、L5 Write API、SkipDistill<br>3. v0.55: 稳定性 — 移除 IVF、panic→error、崩溃恢复、L5 写入管线<br>4. v0.54: Go 基础 — 四层架构、V2 .meh 存储、仅 2 个依赖、log/slog |
| v0.18–v0.63 | 2026-05-31 ~ 07-10 | Rust | 1. V2 追加写入 `.meh`，支持快照/检查点<br>2. BM25 + IVF 混合检索<br>3. L3 超图 DSL、社区发现（团扩展 + Louvain）、BFS/缓存<br>4. 完整 Dream 管线：L3 蒸馏 → L2 压缩 → L1 衰减 → L0 重建 → L5 结晶<br>5. FFI（cdylib）、MCP Server、gRPC/Unix Socket 编码器 |
| v0.6–v0.17 | 2026-05-20 ~ 05-25 | Rust 早期 | 1. 纯 Rust 单 crate（移除 Python 绑定）<br>2. LMDB → 自定义 `.meh` 存储迁移<br>3. 四层 → 六层认知架构演进<br>4. MCP Server 集成<br>5. HNSW 向量索引（替代暴力搜索） |
| v0.1–v0.5 | 2026-05-19 ~ 05-24 | Python | 1. Hopfield 联想记忆网络<br>2. LMDB 嵌入式存储，`pip install` 一键安装<br>3. O(1) 联想召回 + 置信度评分<br>4. BrainLoop 自循环 Agent 循环<br>5. 验证"活记忆"概念 |

## 链接

| | |
|---|---|
| MeowAgent | [github.com/meowagent/meowagent](https://github.com/meowagent/meowagent) — 即将开源 |
| MemHop | [github.com/qyiun666/MemHop](https://github.com/qyiun666/MemHop) |
| Meowire | [github.com/qyiun666/meowire](https://github.com/qyiun666/meowire) |
| MeowDesk | [github.com/qyiun666/MeowDesk](https://github.com/qyiun666/MeowDesk) — 即将开源 |
| 官网 | [qyiun666.github.io/meowagent.github.io](https://qyiun666.github.io/meowagent.github.io/) |
| 邮箱 | qyiun666@163.com |

<p align="center">⭐️ <a href="https://github.com/qyiun666/MemHop">在 GitHub 上给 MemHop 点个小星星</a> — 你的支持是我们的动力！</p>

## 许可证

MIT OR Apache-2.0
