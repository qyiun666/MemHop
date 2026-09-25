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
  <strong>当前版本：v1.6.5</strong>
</p>

---

MemHop 是一个面向 AI Agent / 大模型（LLM）应用的**嵌入式长期记忆数据库**，纯 Go 实现。它不是一个向量数据库——它是以人脑知识组织方式为蓝本的记忆系统：具备身份认同、情景回忆、语义压缩、知识图谱和归档存储。一个 Agent，一个 `.meh` 文件，零基础设施。

MemHop 是 **Agent 专用**记忆数据库：每个 Agent 绑定唯一的 `.meh` 文件，文件级排他锁保证同一文件同时只有一个实例（第二次 `Open` 直接报错）。支持 **Linux、macOS、Windows** 全平台，无 cgo，除 LLM 接口外无任何外部服务。

作为 [MeowAgent](https://github.com/meowagent/meowagent)（即将开源）的大脑记忆模块，MemHop 以内嵌器官而非独立服务的形式运行。无需启动服务器，无需管理配置——打开文件，Agent 便拥有记忆。

> **我们对 Agent 记忆的立场。** 记忆不应该是事后用向量数据库插件外挂上去的附属品，也不该是被塞进上下文窗口的纯文本日志。没有内化记忆的 Agent，不过是一个假装聪明的无状态函数。MemHop 的存在基于一个信念：记忆必须是*认知的*——像人脑一样结构化、压缩、巩固、遗忘——并且是*内嵌的*——活在 Agent 进程内部，而非躲在一次网络调用的背后。一个文件，零基础设施，心智随每次对话成长。

## 核心特性

- **六层认知架构** — L0 画像 → L1 纠缠图 → L2 上下文 → L3 知识 → L4 归档 → L5 计划，配合 Dream 巩固管线
- **场景即会话的记忆循环** — 一个 L2 场景 = 宿主的一个会话。`Search` 直取该域当前会话的 depth-1 话题集（纯内存读，零 LLM、零 embedding），并**顺手开启本轮**——此后场景与轮次这两个 id 都由库自持，写入侧不点名任何 id。宿主自己记录这一轮：`AppendArchive` 写下轮中做了什么（对话原文与操作事件同为 L4 内容、只差一个 `Kind`），`Update` 在轮末记下这一轮怎么开、怎么收，并把该轮原文一次提炼成该话题的关键词收口。一轮拥有的东西全在这个 id 下，该轮开出的任务树才是 L5。话题的 `FusedKeywords` 集合就是宿主每轮注入的上下文
- **V2 追加写入存储** — `.meh` 格式（`FormatVersion=0x0012`），A/B 双头 + 记录级 CRC32 + 撕裂尾帧截断恢复，mmap 零拷贝读取，快照/检查点。记录帧携带 8 字节 `agent_id`（26 字节帧头），引擎按 `(agent, idHash)` 域索引全部记录。L3 知识图记录驻留文件级保留公共域，该域不再承载别的东西。**仅认 `0x0012`**——`0x0011` 及更早的 `.meh` 数据文件 Open 时显式拒绝、无迁移路径：那批文件的画像上没有 `agent_type`，解码回来每个域都读作主 agent——错的不是某一个值而是每个域同时错，而当前规则要求一个文件恰好一个主
- **多 Agent 域** — `Open(path, llm, defaults, profile)` 返回库句柄，域一律以句柄形式取、不以 id 取：`Primary()` 是文件被打开所依据的那个域，`SubAgent(llm, profile)` 是按名字建/取的子域。多个 agent 共享一个 `.meh` 文件，各自拥有完全隔离的域（话题缓存、Dream 管线、域级锁）；同 agent 串行、跨 agent 并行；空闲域按访问节奏回收内存（`Defaults.AgentIdleTTLMs`），记录仍在文件。例外是 L3（见下）：知识图是文件级公共池
- **L1 场景超图** — Dream 在关键词集合重叠的场景间创建共现超边（Jaccard ≥ 0.15，这个下限由引擎固定，不是可调项）并按时间衰减剪枝；一条边以建边时那个相似度定重，此后只淡出——只有某一端名下的轮次清单变了才会重新加权（少一轮与多一轮同算），把两份清单都没变的场景再测一遍（哪怕同一批轮次被重新蒸馏成了别的措辞）不算重新经历。L1 由 Dream 维护，供显式图查询与后续关联消费——读取路径不打分、不扩散
- **Dream 巩固管线** — 作用于 L0–L2，另对内容与计划树各做一次保留期清理：`l4_prune`（丢弃超出保留窗的话题内容——默认 7 天，`Defaults.ContentRetentionMs`）与 `l5_prune`（丢弃超窗的计划节点，仍在途的树豁免）排在最前，随后 L2 压缩 → L2Meta 缓存重建 → L1 节点/超边重建 → L1 衰减 → L0 蒸馏（情绪/MBTI）；某场景 depth-1 话题数超过 `Defaults.SceneDreamTopicThreshold` 时由 `Update` 后台调度该场景巩固，返回逐阶段 `DreamReport`
- **L3 知识图谱** — 多独立超图，节点导入支持位置引用（source_ref）与关系边（related；边的身份是「成员节点 + kind」，同一对节点可并存多种关系），整图删除，关键词/类型/ID 条件按 AND 组合，BFS 子图查询。图池是**文件级**的：文件内所有 agent 域共享一份 L3（项目知识导一次全家可见），公共池的寿命跟文件走、不跟任何单个域走
- **设计层面单实例** — 一个 `.meh` 文件只有一个持有者：全平台文件排他锁强制（linux/darwin/windows），第二次 `Open` 直接失败；内嵌形态无服务进程、无后台守护
- **极简依赖、可内嵌** — 3 个直接 Go 依赖（xxhash、go-openai、golang.org/x/sys）；关键词提炼没有本地兜底，LLM 返回不可解析就直接报错；**引擎不联系任何 embedding / 向量服务**，配置里也没有维度要声明，`sync.RWMutex` + `atomic.Pointer`，零基础设施

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
// SceneID 留空即续用该域当前的场景（重开文件后从记录里恢复「轮次计数器跑得最远」
// 的那个），所以一个 agent 对一个库的宿主根本不点名场景；NewScene: true 才是另开
// 一条会话。点名某个 SceneID 的读只对那个场景生效，且它必须已存在，否则
// ErrNotFound（L3ID 只由「会建场景」的那次读取受理）。纯内存读：不调 LLM、不做向量
// 编码、不打分；NewTopicID = 本轮要落进去的话题。
res, err := sess.Search(memhop.SearchQuery{})
if err != nil {
    log.Fatal(err)
}
for _, topic := range res.Topics { // 该会话的 depth-1 话题集 = 本轮上下文
    _ = topic.FusedKeywords
}

// 一轮进行中：宿主把轮中做过的事写进 Search 开着的这一轮——是哪一轮由库记着，这个
// 调用不点名 id，域上没有开着的轮时它直接被拒。对话与事件是同一类记录，只差一个
// Kind；每次调用返回这条记录占用的槽位。
_, _ = sess.AppendArchive(memhop.ArchiveInput{
    Kind:      memhop.KindEvent,
    EventType: "tool_call",
    Content:   `{"tool":"grep"}`,
    CreatedAt: time.Now().UnixMilli(),
})

// 一轮结束：Input 与 Output 落在读者会去找的那两个对话槽（Seq 1 / Seq 2），所以重收
// 同一轮是原地覆写这两行而不是叠加版本；Outcome 是宿主自己的话，说这一轮从哪条路收
// 的，按调用次数追加成一条 turn_outcome 事件（引擎从不按它分支）。随后把该轮原文一次
// 提炼成它的关键词轨，随话题一起返回。
topic, err := sess.Update(memhop.TurnEnd{
    Input:     "昨天我们讨论了什么？",
    Output:    "Agent：...",
    Outcome:   "resolved",
    CreatedAt: time.Now().UnixMilli(),
})
if err != nil {
    log.Fatal(err)
}
_ = topic.FusedKeywords

// Dream 巩固（L0-L2）；sceneID 传空串 = 遍历域内全部场景。
// 场景话题数超阈值时 Update 已会自行后台调度，通常无需手动调用。
report, err := sess.Dream(context.Background(), "")
```



> **并发契约。** 同一 agent 的操作（Search / Update / Dream / 写 API）由库内域级锁串行，跨 agent 在 `*DB` 上并行，宿主无需自行排队。`*memhop.Session` 除绑定的域外不携带任何跨域状态，开着的场景与轮次同样由域自持、不在调用间携带。文件排他锁仍保证一个 `.meh` 文件只能被一个进程打开；`*DB` 不暴露任何锁接口——域锁是库的，宿主自己的临界区请自行加锁。开着的那一轮没有一处是进程级的，所以多一个 agent 就是多一次 `Open`——哪怕是在另一条尚未收束的轮次中途打开，两边的键与内容也各归各的。

前置条件：Go 1.27+，OpenAI 兼容的 LLM 接口（经 `api.LlmConfig` 配置，由 `api.Open` 收取）；无需任何 embedding / 向量服务

### API 概览

| 分组 | 方法 |
|------|------|
| 核心循环 | `Search(q) → 开启本轮` · `PlanNodeAdd(parentSeq, title) → seq`（parentSeq 0 即开出本轮的树）/ `PlanNodeUpdate(PlanStep{Seq, Status, …})`（计划先于步骤，一次一步） · `AppendArchive(ArchiveInput{...}) → seq` · `Update(TurnEnd{Input, Output, Outcome, CreatedAt}) → topic` · `Dream(ctx, sceneID)` —— 轮中这几个写入都不点名场景 id 与轮次 id：它们落在 `Search` 开着的这一轮上 |
| L0 画像 | `GetL0` · `UpdateL0` |
| L1 纠缠图（只读） | `ListL1() → []SceneNodeView` —— 本域全部场景节点，顺序稳定、id 为 hex。节点与它们之间的共现边都由 Dream 建立，Dream 是唯一写入方，所以没有 L1 写接口。`Importance` / `Valence` / `Arousal` 是巩固算出来的值，`EmotionSet` 标记有没有哪一趟真的盖过那两个信号（0 是合法读数，光看值分不出）；`EdgeIDs` 本身没有读取口——两个节点共享同一个 id 就意味着 Dream 判定它们相关 |
| L2 上下文 | `ListScenes([l3ID])` · `UpdateScene(id, {Name, L3ID, Force})` · `RenameTopic(topicID, name)` · `SceneContext(sceneID，传 "" 即读该域当前会话)` · `MergeScenes` · `DeleteTopic` · `DeleteScene` |
| L3 知识 | `GetL3` · `ListL3` · `ImportL3`（报出本批把每个 domain 解析进了哪张图，含什么都没新写的那张） · `UpdateL3` · `DeleteL3` · `QueryL3Nodes` · `QueryL3Subgraph`（`edgeKinds` 收窄走的边，未定义的边种类是拒绝，不是回一个空子图） |
| L4 归档 | `AppendArchive(ArchiveInput{Kind, Seq, Role, ContentType, EventType, NodeSeq, Content, CreatedAt}) → seq` 是一条记录进入话题的唯一途径：它写的就是 `Search` 开着的这一轮——是哪一轮由库记着，这个调用不点名 id，因此拼错或自造的键根本传不进来，而域上没有开着的轮时直接 `ErrInvalidQuery`，`Seq: 0` 由库分配、占到的槽位随调用返回，写一个已被占用的槽位就是覆写。事件的 `NodeSeq` 必须指向本轮计划里已创建的那一步（`0` 即不绑任何步骤）。`SearchL4(q)` 是唯一读取面，两类内容都在里面；关键词（忽略大小写）/ 时间段 / id / 话题 / `Kind`（原文 or 事件）/ `NodeSeq`（**某一步及其全部子步**归因的记录，步骤只在它那一轮内成立；`0` 即不加这条约束）/ 内容类型都是条件而不是模式，`Kind` 不填即两种都要，`Limit` 保留该次读取排序后的末尾 N 条（单话题按槽位序，跨话题按记录自己的时间序） |
| 轮内事件（L4 的 `Kind=event`） | 一轮一个键：Search 为该轮开出的话题 id；事件本身住在 L4（`Kind=event`），超出保留窗自动清理（默认 7 天，可配）、无删除接口，读它用 `SearchL4(L4Query{TopicID, Kind: &KindEvent})`，写它用 `AppendArchive`。一个话题的首条事件是 `Seq=3`，因为槽位 1 与 2 属于对话 |
| L5 计划树 | `PlanNodeAdd(parentSeq, title) → seq` · `PlanNodeUpdate(PlanStep{Seq, Status, Title, Summary})` · `PlanState()` —— 计划树是 L5 唯一自己的记录：一节点一条，一个步骤由「开出它的那一轮 + 该轮内库顺序发号的序号」说清（`ParentSeq` 指它挂在谁下面，0 即根、也即开出本轮的树；序号发在「仍然指着某个序号的那些东西」之上——这一轮现存的步骤，**加上**这一轮仍然活着、绑到某一步的事件里被点名的最高序号（一步与指着它的事件共用同一个地址，两者却各自老化）。于是一轮的编号可以出现空洞，宿主不得从序号本身读出「第几步做过什么」；它仍不保证永久唯一——保留窗扫掉某一步、且指着它的那条事件也到期之后，那个序号才重新腾出来；回到一个被扫空的旧轮次时，手里的旧序号要按新步骤的地址对待），所以 `PlanState()` 与 `SearchL4{TopicID, Kind}` 是同一个键、两层存储，而 `SearchL4{TopicID, NodeSeq}` 能单独读回某一步及其全部子步做过的事。节点**只因被创建而存在**：`parentSeq` 指向树上没有的步骤是拒绝，而不是顺手补出一个父节点（因此打错一个序号不会长出第二棵树）。`PlanNodeUpdate` 只重述已在树上的那一步——`Status` 每次必须给（没有「保持不变」这种写法），`Title`/`Summary` 留空继承现值。新建的步骤就是 `in_progress`，模型里没有「已计划未开始」这一档；撤回一步没有接口，也不需要一个：手段就是不在此后的轮里再创建它。这三个计划写入口都不点名轮次 id——`Search` 开着的这一轮就是它们的键——也不落任何内容：一步的轨迹是宿主自己 append 的 L4 记录 |
| DB 句柄 | `Open(path, llm, defaults, profile)` · `Primary()` · `SubAgent(llm, profile)` · `Checkpoint` · `CompactTo(newPath)`（写出整理后的副本，目的地路径由调用方自行约束） · `Stats`（文件字节数与全文件可达记录数——判断是否该压缩的两个读数） · `Close` · `IsClosed` |

## 架构

```
层级  名称            人脑类比              机制
───── ────────────── ───────────────────  ─────────────────────────────────────────────
 L5    Plan            任务树                一节点一步，键是开出它的那一轮；过期的树由 Dream 清扫
 L4    Archive         轮内内容             一轮的对话原文与操作事件（Kind），按 (话题, Seq) 寻址；保留窗（默认 7 天）——越过它的只有关键词轨存活
 L3    Knowledge       语义记忆             多源超图知识库
 L2    Context         工作记忆             场景表浅话题 + 被 Dream 折进融合组的那一层（一条话题至多下沉一次）
 L1    Engram          场景超图             场景节点 + 关键词重叠超边；由 Dream 维护，供显式图查询
 L0    Profile         身份认同             Agent 人格、偏好与语言习惯
```

### Dream 管线

Dream 周期是一个自动记忆巩固过程，受人脑睡眠中处理经历的机制启发。Dream **仅作用于 L0–L2**（L3 蒸馏为设计外）另对 L4 内容与 L5 计划节点各做一次保留期清理，共四个阶段：

1. **L2 压缩** — LLM 归组合并相关话题，每个目标场景一个 goroutine，但并发有界（整趟都持着域锁，场景数不等于并发数），把被合并的话题下沉为 depth-1 融合节点（子节点降级为历史）
2. **L1 重建** — 从 L2 同步场景节点，并在同一趟扫盘中重建 L2Meta 话题缓存、创建/刷新场景间关键词重叠超边
3. **L1 衰减** — 衰减场景重要性与边权，剪枝弱节点
4. **L0 蒸馏** — 从排序后的 L1 样本蒸馏情绪/MBTI 与一份人格摘要，合入存量画像：`Name`、`Role`、`Preferences` 原样保留，而 `Personality` 是宿主写过、Dream 也会重写的唯一一项，所以一次省略它的宿主写入会清掉蒸出的那句，直到下一趟重新演化。同一份回包带回的逐节点情感回填到**从没被盖过章**的 L1 节点上——已经带着读数的节点原样保留，合法的 (0,0) 也算已定读数；没有样本可读时整段跳过

触发方式：某场景的 depth-1 话题数超过 `Defaults.SceneDreamTopicThreshold`（默认 24）时，`Update` 在后台调度该场景的 Dream；宿主也可显式调用。`Dream(ctx, sceneID) (*DreamReport, error)` 整个周期持有域锁，`sceneID` 传空 = 遍历域内全部场景（话题数不足 `DreamCompressMinTopics` 的场景自动跳过），并在阶段间响应 `ctx` 取消。

### 读取与写入路径

**没有打分检索。** 场景 = 宿主的会话，所以引擎不再猜"这条消息属于哪个场景"：

| 路径 | 做什么 | 代价 |
|------|--------|------|
| `Search(SearchQuery{SceneID, L3ID, NewScene})` | 空 `SceneID` → 续用该域当前的场景（重开文件后从记录里恢复「轮次计数器跑得最远」的那个），域内一个都没有时才新建（名字由库生成）；`NewScene: true` → 另开一条会话，这是同一个域上开第二场会话的唯一路子；点名 `SceneID` → 返回该场景的 depth-1 话题集（按用户消息时间升序）+ L0 画像，外加 `NewTopicID`：本次读取为即将进行的这一轮开出的话题 | 纯内存读（L2Meta 缓存），零 LLM、零 embedding、零打分；唯一写是场景记录（轮次计数） |
| `AppendArchive(ArchiveInput{Kind, ...}) → seq` | 一轮内容的唯一写入面，写的就是 `Search` 开着的这一轮——是哪一轮由库记着，这个调用不点名 id，因此拼错或自造的话题 id 传不进来；域上没有开着的轮时它被拒（`ErrInvalidQuery`），而不是把内容写进一个任何读取都列不出的孤儿键。原文声明谁说的、是什么媒介；事件自己命名，并可挂在某个计划步骤上。`Seq: 0` 在两个对话槽之上分配，占到的槽位随调用返回 | 零 LLM；被拒的记录一字节不留（含顺路要建的节点）。事件整条 4 KiB（名字与正文合计）、原文 64 KiB，超预算是拒写不是截断 |
| `Update(TurnEnd{Input, Output, Outcome, CreatedAt}) → topic` | 收口那一轮：`Input` 与 `Output` 落到该话题的两个对话槽（Seq 1 / Seq 2），所以重收同一轮是原地覆写这两行而不是叠加版本；`Outcome` 是宿主自己的说法——它说这一轮是从哪条路收的，引擎从不按它分支——按调用次数追加成一条 `turn_outcome` 事件（一次挂起加一次恢复是两条事实，不是一行写两遍）。随后把该话题已有的原文蒸馏成它的关键词轨，并随落盘后的话题一起返回——蒸出的 `FusedKeywords` 就在里面 | 每轮恰好 1 次 LLM 调用，且排在该轮话题落盘之前，失败不留半成品话题。内容已被保留窗裁光的轮次直接 `ErrInvalidQuery`，一次 LLM 也不调用；域上没有开着的轮时同样 `ErrInvalidQuery`（消息含 `no turn is open`） |

宿主注入的上下文就是该场景 depth-1 话题的关键词集合；要看某轮原文，用那一轮的话题 id 去寻址 L4——`SearchL4(L4Query{TopicID})`——或直接用已经带回消息的 `SceneContext`。注入规模靠 Dream 向 `DreamCompressMinTopics`（默认 20）收敛来控住——那是巩固这一趟瞄准的目标数，不是场景被牢牢按住的上限，所以让自动巩固照常开着，才是让注入不至于无界增长的那件事。

随检索一并移除的：三通道 RRF 打分、L1 扩散激活（`AssociatedContexts`）、话题向量质心与 embedding 依赖、`AutoCreate` / `DirectedL2ID` / `DirectedL3ID` 三条路由，以及话题级 `L3Refs`（L2↔L3 关系现只由场景锚点 `SceneSlot.L3ID` 承载）。
## 测试与基准

MemHop 的测试套件只驱动公开 `api` 表面——即宿主（如 MeowAgent）实际发起的调用——并直接断言引擎自身的记忆结构，而非外部可答性 judge。

### 集成测试（`test/`，build tag `integration`）

- **记忆循环**（`TestCoreCycleUpdateDream`）：按真实宿主的调用方式把 N 轮灌进一个场景，每几轮做一次**周期性 L0/L2/L4 一致性检查**——L0 画像可读、场景读回非空、L4 保留原文逐字一致；Dream 巩固后场景读回的 depth-1 话题集必须收缩，且事实仍能从 L4 原文取回。
- **关键词保真与持久**（`TestKeywordFidelity`/`TestKeywordPersistence`/`TestDreamCompressionFidelity`）：一轮提炼出的关键词忠实承载该轮含义、在噪声轮次后仍在场景读回里、并经受住 Dream 压缩。
- **API 契约**（`TestInterface*`：读路径零 LLM、写路径每轮恰好一次提炼、未知场景拒绝、检查点跨重启）、**e2e 流程**（`TestE2E*`）、**长输入健壮性**（`TestExtractKeywordsLongInputRealLLM`/`TestUpdateLongTurnSettles`）。

### 基准（`go test -tags integration -bench .`）

所有基准都驱动真实 api 循环（真实 LLM，无外部 judge）：

| 基准 | 测量 |
|------|------|
| `BenchmarkMemoryLoop` | 稳态 Search+Update 记忆循环，含引擎**自动调度的 Dream**（场景 depth-1 话题数超过阈值）与周期性 L0/L2 验证 |
| `BenchmarkUpdateTurn` | 走完一轮：开轮那次读，再加收束（两条对话写入、一次提炼、话题落盘） |
| `BenchmarkSceneRead` / `BenchmarkSceneReadLatency` | 场景读回吞吐与延迟分布（min/p50/p95/max） |
| `BenchmarkDreamConsolidation` | 完整 Dream 流水线延迟 |

### 为什么不跑外部数据集基准？

公开记忆基准（LoCoMo、LongMemEval）评估的是“检索 → LLM judge 可答性”——与 MemHop 分层设计要断言的（L0 画像蒸馏、L1 场景图一致性、L2 压缩语义、L4 原文归档）是不同的问题。形态最贴近的 LongMemEval（多会话 user-assistant 对话、约 500 题）单题需 115K–1.5M tokens，不具备作为持续集成基准的可行性。因此 MemHop 通过 api 循环直接验证自身的记忆结构，而非追逐一个泛化的 QA 分数。

## 项目结构

```
api/                         ← 对外门面：open（唯一入口）/ session（唯一的业务句柄，hex id 面）/
                               types / mapping / errors / exports
internal/ 根                 ← 大方法 + 组合根：config / db / session / models / exports +
                               agents / l0…l5 / l3query / search / update / dream
internal/scene|turn|dream|graph|content|plan
                             ← 第 3 层小方法包，一个认知面一个包：都不自己拿域锁，也互不 import
internal/domain              ← 域状态容器：Context（域锁、三份缓存、OpCtx）、计划缓存、
                               L2Meta 镜像维护
internal/config              ← 配置类型：宿主给的 LLM 端点与调参默认值，
                               以及装配层据它们拼出的那一份
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
- 成本敏感时，给这个端点配一个快速小模型即可（便宜 API 模型或本地兼容端点）；关键词提取不需要旗舰模型。
- **账按收束次数算，不按轮次算。** 同一轮收两次就是两次提炼、话题仍是一个——对话槽只留最后一次说的那一对问答，两次结局各自留在事件轨上。内核「挂起等输入 → 恢复」的那条轮每次调用各读一次、各收一次，所以是两轮，不是同一轮被收两次。

## 开发

```bash
go build ./...                          # 构建
go vet ./...                            # 静态分析
go test ./internal/...                  # 单元测试（不依赖外部服务）
go test -tags integration ./test/...    # 集成测试（需要 LLM key）
```

集成测试针对真实 LLM 运行（引擎侧不再需要任何 embedding 服务）。通过环境变量 `MEMHOP_TEST_LLM_KEY` / `MEMHOP_TEST_LLM_URL` / `MEMHOP_TEST_LLM_MODEL` 配置 LLM（仅设置 key 时默认使用 DeepSeek 接口），或通过 `test/testsupport/key_config.json` 配置。

## 版本历史

每行只保留至多五条重点；完整逐条记录见 [CHANGELOG.md](CHANGELOG.md)。

| 版本 | 日期                 | 亮点 | 核心改动 |
|------|----------------------|------|---------|
| v1.6.6 | 2026-09-23 | 一轮只剩一次收束：域自持场景与轮次，写入形状不再收归属字段 |1. **一次 `Update(TurnEnd{Input,Output,Outcome,CreatedAt})` 收束一轮**——`Settle` 退役；Input/Output 原地覆写该轮话题的 Seq 1/2，Outcome 按调用次数追加成 `turn_outcome` 事件。场景与轮次键由域自持：`Search` 续用当前场景并开出这一轮，`AppendArchive` 的写入形状（`ArchiveInput`）不带地址——写到宿主没在做的轮这条路被形状关死。<br>2. **一个域只需存一件标识符**：`Session.AgentID` 读回该域库发号的 id，`DB.Agent(llm, id)` 按它取回同一个域（不建任何东西——未知或非 hex 一律拒），`DB.Agents()` 列出文件里的每个域。<br>3. **父步骤折出的摘要是派生值**：计划节点带 `summary_folded` 标记，分支变了派生摘要跟着重折，宿主写的文本任何汇总都不改写。<br>4. **修复真缺陷：合并吞掉对话的项目归属**——没锚的场景并掉锚定的那条后，整段对话从项目列举消失；现在幸存者没锚就接手被吞场景的锚，锚冲突整次拒且在任何删除之前。<br>5. **修复 Linux CI 抓出的真缺陷：同一毫秒连开两轮时，重开回到哪段会话由 id 哈希决定**——域内 `last_used_at` 戳改为严格递增。|
| v1.6.5 | 2026-09-22 | MCP 面整体退役，Go module 是唯一对外形态 |1. **`cmd/memhop-mcp` 整包删除**：25 个工具、多租户 HTTP（SSE + streamable-http）、租户注册表与限制域数量的 `--tenants` 一并消失。2. **直接依赖 4 → 3**：它是唯一读 `modelcontextprotocol/go-sdk` 的地方，`go mod tidy` 连带清掉它带入的 7 个间接依赖。3. **Go 面一字不动**：`Session` 25 + `DB` 7 原样，`api.CodeOf` 保留为宿主读错误码的门面口。4. `make build-mcp`/`test-mcp` 删除，CI 与本地门禁的包清单去掉 `cmd`。5. 所有「仅 Go 侧」的说法改成对调用方的约束（`CompactTo` 入参是任意写路径，目的地由宿主限定），并纠正一处归因：`SceneContextTopic.messages` 在 JSON 里缺键是 `omitempty` 的性质，不是某个工具的选择。磁盘格式 `0x0012` 不变|
| v1.6.4 | 2026-09-11 | 公开面围绕 `Open` 重做：域以句柄交回，id 不再越过边界；随后的分包审查把方法归位、删掉没人读的东西 |1. **入口换成 `Open(path, llm, defaults, profile)` → `*api.DB`**，成败由文件里有什么决定（两条拒绝都排在碰文件系统之前），域以句柄交回：`Primary()` / `SubAgent(llm, profile)`——宿主不再持有或回传域 id。<br>2. **L0 新增 `AgentType`**（0=主 / 1=子），建域时盖章、改画像动不了；格式 `0x0011` → `0x0012`，更老文件 Open 即拒、不迁移。<br>3. **整体退役**：能力面（`Crystallize`、`ParseCapabilityPackage` / `ValidateCapabilityCard`）、`ListTrajectorySessions`、`DeleteAgent` 及其整域删除链——公开面 27 + 8 → 26 + 6。<br>4. **公开面收敛（breaking）**：话题不再存子话题清单（`children_ids` 删除、闭包由 `parent_id` 现算）、画像写入换 `api.ProfileInput`（宿主四项、`Name` 必填）、两个纯 `len` 键删除、图槽 `updated_at` 改为内容变化钟。<br>5. **随后的多轮审查扫过列举定序、记忆质量与内核销毁路径**——锁回答之前执行的 `O_TRUNC` 清文件等真缺陷都在其中；逐条见 CHANGELOG。|
| v1.6.3 | 2026-09-10 | 内容只住在 L4：一轮的对话与事件同层，L5 只剩计划树，`Update` 只蒸馏 |1. **记录并入 L4**：`ArchiveSlot` 加 `Kind`/`Seq`/`EventType`/`NodeSeq`，归档 id 改为位置式（由该轮话题与槽位序号派生），同槽位重写即原地覆写——重放差分与墓碑机制删除。<br>2. **L6 只剩计划节点**：`core.PlanNode`（帧值 `0x0F`），`TrajectorySlot`/`NodeType*`/`PlanNodeRef` 删除；`0x000E` 及更早文件 Open 即拒、不迁移。<br>3. **`AppendArchive` 是内容的唯一写入面**（校验排在任何写入之前、超预算拒写不截断），**`Update` 不写内容**——渲染该话题原文并蒸馏一次，提炼失败时宿主已写的记录原样留着。<br>4. **公开面 26 → 25**：删 `AppendTrajectory` 与 `ReadTrajectory`（读取由 `SearchL4{TopicID, Kind}` 覆盖），新增 `AppendArchive`。<br>5. **计划层重做**：一轮的树一步一步写（`PlanCreate`/`PlanNodeAdd`/`PlanNodeUpdate`），步骤以轮内序号寻址，事件指向树上没有的步骤即拒、不长枝。|
| v1.6.2 | 2026-09-07 | 计划事件 `EventType` 归宿主命名（词表退役） |1. **`planEventTypes` 名单与 `plan.ValidateEvent` 删除**：计划绑定事件（`nodePath` 非空）的 `EventType` 与裸轮次事件同口径——任意非空宿主命名即接受、原样存回。判据是这条约束不挣自己的饭钱：引擎从不按 `EventType` 分支（全仓非测试引用只有那次查表、`ReadTrajectory` 的字段回显、结晶 prompt 的一行格式化），名单唯一的行为就是拒写；代价全在宿主侧——事件名被拒即轨迹静默少一条（沙箱裁决反问类审计事件正属此类）<br>2. **校验点回归一处**：`trajectory.ValidateEvent`（非空 `EventType` + `Timestamp > 0` + payload ≤ 4KB）。`AppendTrajectory` 的计划分支与 `PlanCommit` 仍在 `EnsureNode` / `UpdateNodeLocked` **之前**调用它，被拒的写零留痕（「校验晚于改树」那条顺序不变量原样保留）<br>3. **交付面口径统一**：MCP 的 `memhop_trajectory_append` 本就声明 `event_type` 由宿主自定，而计划写面不在 MCP 工具面上——本轮把 Go 面对齐到交付面已有的口径，24 个工具不变<br>4. **那一轮不增删方法、不触及记录布局**：`event_type` 是记录内的 JSON 字符串字段，收紧与放宽都不改变帧结构（方法数与格式版本在随后未发布的轮次里再次变动，见 v1.6.3 行）；**对宿主是放宽方向**，无需改调用点即可受益<br>5. 测试：`TestPlanEventVocabularyRejectsUnknown` 改写为 `TestPlanEventNamesAreHostOwned`（钉住「宿主命名被接受 + 名字未被改写 + 空 `EventType` 仍在建链前被拒」），`api`/`test` 面三处拒写断言改用空 `EventType` 触发；决策档案 `notes/implemented/simplification/2026-09-07-plan-event-vocabulary-retirement.md`|
| v1.6.1 | 2026-09-06 | 公开面再收敛（34→32）；内置说明书卡删除；公开面按使用者分两类；L5 记录层退役（目录即能力） |1. **L5 记录层退役（目录即能力）**：引擎不再存能力卡，宿主自有的 `plug/<包>/capability.json` 是唯一事实源；五个会话方法与八个类型随之删除（公开面 32 → 27）。<br>2. **`ActivateCapability` 删除**（激活并入 `UpdateCapability`）、**`PlanReplace` 删除**（`SyncPlanTree(planID, nil)` 清整树）——同一能力只留一个实现。<br>3. **内置说明书卡删除**：九张「库自身方法怎么调」的卡只会被宿主 skip 或与工具描述重复，能力池只装 LLM 可触发单元。<br>4. **公开面按使用者分两类**（任务面 / 组装与管理面）——这套分组沿用至今。<br>5. **格式 `0x000B` → `0x000C`**：更老文件 Open 即拒、不迁移。|
| v1.6.0 | 2026-09-04 | 接口去 fallback；L3/L5 文件级公共池；L5 统一卡 + `plug/` 自动注入 |1. **v1.5.0 tag 打在 API 面完成之前**，本版本交付补齐后的完整线，ID 一律库内发号（`api.DefaultAgentID` 指隐式域、`api.NewPlanID(name)` 铸计划 id）。<br>2. **公开面合并**：`SetSceneName`+`SetSceneL3ID` → `UpdateScene(id, ScenePatch)`、`PlanAppend` → `AppendTrajectory`、`ListScenesByL3` → `ListScenes(l3ID)`；九个一次性方法删除（34 + 8）。<br>3. **接口不允许 fallback**：LLM 关键词输出不可解析即 `ErrLLM` 且该轮零留痕（gse/分词兜底删除，直接依赖 5 → 4）；超预算 payload 拒绝而非截断。<br>4. **L3 知识图升级为文件级公共池**：所有 agent 域共享一张图，记录住保留共享域，场景锚点校验读公共池。<br>5. **L5 统一卡 + 能力文件级公共池 + `plug/` 自动注入**；格式 0x0009 → 0x000B，更老文件 Open 即拒。|
| v1.5.0 | 2026-09-01 | L2 换轨：场景 = 宿主会话，轮次 ID 归库管 |1. **`Search` 读场景 + 开一轮**：入参只剩 `{scene_id, l3_id}`（都可空），返回场景本体、depth-1 话题与为这一轮铸出的话题 id；读路径上全部 LLM/embedding/打分调用删除。<br>2. **`Update` 把整轮沉淀进那个 id**：一次提炼排在所有写入之前，LLM 失败零留痕；同 id 重放原地覆盖这一轮。<br>3. **N:N 追加面删除**（`AppendL4Message`、`RefineTopicKeywords`）：一轮的 L4 原文恒为两条，两条之间的事归本轮轨迹。<br>4. **话题单轨**：只留 `fused_keywords`，Dream 压缩、L1 超边、宿主注入共用同一轨。<br>5. **检索子系统整体删除**（三通道 scenefind、RRF、场景加分、质心、`Encoder`）：引擎不再联系任何 embedding 服务。|
| v1.4.2 | 2026-08-31 | L6 计划树 + L2 目录归属 |1. L6 承载任务树：`TrajectorySlot.NodeType` 区分轮次事件与计划节点，节点 ID 由 `HashPlanNode(planID, nodePath)` 在 `plan:` 命名空间下稳定派生，事件经 `PlanNodeRef` 挂节点<br>2. 三形态 `PlanAppend` / `PlanCommit` / `PlanState`，另加 `PlanReplace`（重规划、保留 planID）、`SyncPlanTree`（整树快照对齐，不产生 `plan_step`）、`ListPlans`（重启恢复）<br>3. **Model A 显式折叠**：父节点仅由宿主显式 commit 为 done；每次 commit 后把已 done 子节点摘要按 `NodePath` 数值序自底向上汇总进父摘要，不覆盖宿主写的父摘要<br>4. L2 场景 → L3 目录域（N:1）：`SceneSlot.L3ID`、可选 `SearchQuery.L3ID` 前置筛选并在命中时回填、`ListScenesByL3`、`SetSceneL3ID(sceneID, l3ID, force)`（默认写一次，force 纠错、空值清除）<br>5. 加固：`0000000000000000` 为裸事件 `PlanID` 保留值，五个计划入口一律拒绝（此前 `PlanReplace` 传全零会删掉全域轨迹事件）；Dream 的计划豁免收窄为「7 天窗口内仍活动」，被放弃的计划不再无限堆积|
| v1.4.1 | 2026-08-28 | 类型契约清理：hex 出参 DTO、L0 画像 v2、L3 超图激活 |1. api 出参 DTO 改为真实 struct——所有 ID 字段以 16 位 hex 字符串出参（含 `SearchResult.NewTopicID` / `AppendL4Message` 返回值 / `AgentID()`），新增 `api.FormatID` / `api.ParseID`<br>2. L0 画像 v2（`FormatVersion 0x0009`）：字段所有权（Name/Role/Preferences 宿主独占，Personality 宿主播种 + Dream 蒸馏演化）、typed `EmotionState`/`MBTI` 蒸馏信号、删除死字段 lexicon/style_traits<br>3. L3 导入新增 `source_ref`（位置引用）与 `related`（同图内按标题建超边，两阶段解析支持前向引用、重导入幂等；结果含 `edges_created`，导出 `L3Relation` 类型）<br>4. L6 每轮一条轨迹：SessionID 改为轮键（search 开轮、update 收轮），事件带 `TopicID` 支撑跨轮结晶，对外面收敛为追加+查询（删除 `TrajectoryStats` / `DeleteTrajectory` / `PruneTrajectory`，33 → 31 工具），Dream 新增 `l6_prune` 自动清理 7 天前事件<br>5. **破坏性变更**：`FormatVersion != 0x0009`（即 ≤ 0x0008）的 `.meh` 文件在 Open 时被拒绝，无迁移|
| v1.4.0 | 2026-08-26 | 多 agent 记忆数据库 |1. 一个 `.meh` 文件承载多个完全隔离的 agent 域：记录帧新增 `agent_id`（26 字节帧头），引擎索引与快照（0x02）按 agent 分域，租户注册记录把名字映射到稳定的 crypto/rand agentID<br>2. `api.OpenMulti` / `AgentSession` / `CreateAgent` / `ListAgents` / `DeleteAgent`；`Open` 对单 agent 宿主零改动（默认域）<br>3. 业务层重构为按 agent 的 `agentContext` + 域级锁（同 agent 串行、跨 agent 并行）、空闲域内存回收与域化 Dream 管线<br>4. L7 轨迹层改编号为 **L6**（认知层收敛为 L0–L6）<br>5. **破坏性变更**：`FormatVersion <= 0x0007` 的旧 `.meh` 文件在 Open 时被拒绝，无迁移；`api.DB` 上提升自 `internal.DB` 的方法新增 `agentID` 参数（门面方法签名不变），`Lock()` 对已关闭的 DB 会 panic|
| v1.3.4 | 2026-08-26 | L5 工具声明同构 |1. `memhop-capability` 格式升级 v3：`ResourceRef` 的 `description` 改名 `desc` 并新增 `input`（JSON Schema 字符串）/`output`——工具声明字段与宿主工具规格（meowire `ToolSpec`）完全同构，宿主纯字段拷贝即可投影、零格式转换<br>2. `WorkflowStep` 新增 `args`——动作链参数官方化（不再依赖私有 config 格式）<br>3. **破坏性变更**：v2 卡导入被拒绝（format 必须为 `memhop-capability/v3`）；旧版本写入的存量能力记录读取时 `desc/input/output` 为空|
| v1.3.3 | 2026-08-26 | 检索评分归一化 + 参数面收敛 |1. vector floor 从“覆盖式垄断”改为“仅抬升未过线场景”（floor = threshold + cosine×0.5）：真实信号（RRF + 关键词重叠 + 加分）决定排序，语义兜底保留<br>2. `MemHopDefaults` 从 24 字段收敛到 3 个业务开关（`Capacity` / `DreamCompressMinTopics` / `SearchDreamContextThreshold`）；删除 4 个死字段（`MaxResults` / `DefaultTimeoutSecs` / `DefaultMaxOutputTokens` / `MaxDepth`），16 个调优常量移入包级私有 `internal/tuning.go`<br>3. **破坏性变更**：引用被删字段的宿主需同步清理|
| v1.3.2 | 2026-08-26 | API 修复：异步 Dream + 删除接口 + Update 简化 |1. Search/Update 不再被内部触发的 Dream 阻塞（后台 goroutine、按场景 in-flight 防重入、Close 取消在途 Dream）<br>2. 新增 `DeleteTopic`（子树闭包 + L4 + 索引 + 父话题 ChildrenIDs 修剪）与 `DeleteScene`（场景 + 全部话题 + 原文 + L1 节点 + 激活集）用于记忆纠错<br>3. `Update` 返回值由 `(bool, error)` 简化为 `error`<br>4. `SearchResult.ProfileBrief`——紧凑画像摘要（name/role/偏好/风格/情绪，带边界）|
| v1.3.0 | 2026-08-26 | L1 场景超图 + 扩散激活联想 | 1. Dream 在场景间创建真实的 `RecL1Hyperedge` 共现边（关键词重叠 Jaccard ≥ `L1EdgeMinSimilarity`）；Search 的 `AssociatedContexts` 由空转的同场景列表替换为图遍历（每跳激活 × 边权 × 衰减系数，≤ `L1EdgeMaxHops`，取 Top `L1AssocMaxScenes` 个其他场景）<br>2. L6 场景使用记录删除——命中计数并入 L2 `SceneSlot`（`HitCount`/`LastHitAt`）<br>3. `L1ReverseIndex`（含快照字段）与 4 个 L1 死函数删除，联想变为纯存储层图读取<br>4. `.meh` 格式升至 `0x0007`——0x0006 文件 Open 时被拒绝，不迁移<br>5. 新默认项：`L1EdgeMinSimilarity`（0.15）、`L1EdgeMaxHops`（2）、`L1ActivationDampening`（0.5）、`L1ActivationThreshold`（0.05）、`L1AssocMaxScenes`（3） |
| v1.2.7 | 2026-08-25 | 宿主对齐 + 双语集成指南 |1. `Search(ctx, q)` 与 `RefineTopicKeywords(ctx, id)` 接收 context（可取消 LLM 关键词提取、编码调用与内部触发的 Dream）<br>2. `api` 导出 `LlmConfig` / `MemHopDefaults` / `TopicSlot` / `ResourceRef` / `CrystallizeDetail` / `TrajectoryStats`<br>3. `AppendL4Message`（纯 L4 追加，不调 LLM）<br>4. 活跃场景容量策略：Update 在达到 Capacity 时对最老场景触发 Dream（带可压缩性预检）；`SearchDreamContextThreshold` 零值守卫<br>5. 仓库根目录新增双语集成指南（`INTEGRATION_GUIDE.md` / `INTEGRATION_GUIDE.zh.md`）|
| v1.2.5 | 2026-08-20 | MCP server 重写 |1. `cmd/memhop-mcp` 对照 `api` 公开门面完全重写（v1.2.4 曾删除）：31 个 MCP 工具与 `api.DB` 方法一一对应<br>2. 多租户 HTTP 暴露——SSE + streamable-http（2025-03-26 spec、无状态），租户按 URL 路径 `/mcp/<tenant-id>` 隔离到独立 `.meh` 文件，懒打开注册表 + 首开互斥<br>3. 工具输出中记录 ID 统一 16 位 hex 字符串序列化（uint64 JSON 数字在 JS/TS 宿主丢精度）<br>4. go-sdk v1.7.0 回归直接依赖（3→4）|
| v1.2.4 | 2026-08-19 | api/ 公开门面 + internal/ 平铺 | 1. 公开 Go API 从根包迁移至 `github.com/qyiun666/MemHop/api`（根目录 `memhop.go`/`types.go` 移除）<br>2. `internal/sub/` 上提平铺为 `internal/`（`package sub` → `package internal`），`internal/sub/repo` → `internal/repo`，`internal/sub/common` → `internal/common`<br>3. `cmd/memhop-mcp` 移除（v1.2.5 重写回归）<br>4. 构建配置同步（Makefile fmt、pre-commit hook、CI gofmt）<br>5. 破坏性变更：直接 import 根包的宿主需切换到 `/api` |
| v1.2.3 | 2026-08-18 | MCP 兼容性修复 + DSH 接入 + 检索质量修复 |1. MCP 工具 schema 修复（无参工具 `properties` 不再输出 null，兼容严格 MCP 客户端）<br>2. 工具输出 ID 全部改为 16 位 hex 字符串（uint64 JSON 数字在 JS/TS 宿主丢精度，`new_topic_id` 回传失败已修复）<br>3. 新增 `--transport streamable-http`（2025-03-26 规范，Stateless 多租户，DSH 的 dsh-mcp-client 支持）<br>4. 关键词提取 prompt 全面优化（语义完整 + 同义词变体 + 短语）+ Search 按相关性返回全部相关话题（移除场景上下文截断），LoCoMo 召回 0.392 → 0.668、实体命中 0.284 → 0.877|
| v1.2.1 | 2026-08-16 | MCP Server + L5 能力层 |1. 新增 `cmd/memhop-mcp` 二进制：多租户 SSE MCP Server（官方 go-sdk v1.7.0），将全部公开 API 映射为 28 个工具（search/update/dream/checkpoint/status、画像、场景、知识图谱、归档、能力、轨迹/结晶）<br>2. 租户路径隔离 `/mcp/<tenant-id>`<br>3. L5 插件层重构为能力层（`memhop-capability/v1`：manual/atomic/composite 三种 kind，`ActivateCapability` 实现 draft→active 生命周期，指纹去重，Crystallize 产出 create/reuse/merge 候选）<br>4. `.meh` 格式升至 `0x0005`——0x0004 文件（v1.2.0 插件记录）在 Open 时被拒绝，不迁移<br>5. `RecordEnd` 头字段 + A/B 头损坏恢复|
| v1.2.0 | 2026-08-14 | L5 插件层 | 1. L5 动作链 → 插件槽位（PluginSlot + 结构化五段 Manifest：技能 / MCP / 工具 / 提示词 / 服务）<br>2. 仅路径导入 `ImportPlugin`，移除手工写入 Create/Update<br>3. Crystallize 从 L7 轨迹按类型分派插件<br>4. `SearchResult.Crystals` → `Plugins`<br>5. 八层架构（L0–L7）文档 |
| v1.1.0 | 2026-07-27 ~ 08.11 | 架构重构 |1. `internal` 分层重写（装配层 → sub → repo → core/index/common）<br>2. f16 → f32 单精度向量<br>3. 话题质心向量检索<br>4. `.meh` 磁盘格式 `0x0004`，与 v1 数据不兼容|
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
