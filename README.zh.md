<p align="center">
  <h1 align="center">MemHop</h1>
  <p align="center">
    <strong>AI Agent 的长期记忆数据库 —— 七层认知架构，单文件嵌入式，纯 Go 实现，零基础设施。</strong>
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
  <strong>当前版本：v1.5.0 · 最新稳定 tag：v1.5.0</strong>
</p>

---

MemHop 是一个面向 AI Agent / 大模型（LLM）应用的**嵌入式长期记忆数据库**，纯 Go 实现。它不是一个向量数据库——它是以人脑知识组织方式为蓝本的记忆系统：具备身份认同、情景回忆、语义压缩、知识图谱、归档存储和结晶化技能。一个 Agent，一个 `.meh` 文件，零基础设施。

MemHop 是 **Agent 专用**记忆数据库：每个 Agent 绑定唯一的 `.meh` 文件，文件级排他锁保证同一文件同时只有一个实例（第二次 `Open` 直接报错）。支持 **Linux、macOS、Windows** 全平台，无 cgo，除 LLM 接口外无任何外部服务。

作为 [MeowAgent](https://github.com/meowagent/meowagent)（即将开源）的大脑记忆模块，MemHop 以内嵌器官而非独立服务的形式运行。无需启动服务器，无需管理配置——打开文件，Agent 便拥有记忆。

> **我们对 Agent 记忆的立场。** 记忆不应该是事后用向量数据库插件外挂上去的附属品，也不该是被塞进上下文窗口的纯文本日志。没有内化记忆的 Agent，不过是一个假装聪明的无状态函数。MemHop 的存在基于一个信念：记忆必须是*认知的*——像人脑一样结构化、压缩、巩固、遗忘——并且是*内嵌的*——活在 Agent 进程内部，而非躲在一次网络调用的背后。一个文件，零基础设施，心智随每次对话成长。

## 核心特性

- **七层认知架构** — L0 画像 → L1 纠缠图 → L2 上下文 → L3 知识 → L4 归档 → L5 结晶 → L6 轨迹，配合 Dream 巩固管线
- **场景即会话的记忆循环** — 一个 L2 场景 = 宿主的一个会话。`Search` 按场景 id 直取该会话的 depth-1 话题集（纯内存读，零 LLM、零 embedding），`Update` 一次沉淀整轮（用户原文 + Agent 原文 + 双时间戳 → 一次提炼出话题关键词），话题的 `FusedKeywords` 集合就是宿主每轮注入的上下文
- **V2 追加写入存储** — `.meh` 格式（`FormatVersion=0x0009`），A/B 双头 + 记录级 CRC32 + 撕裂尾帧截断恢复，mmap 零拷贝读取，快照/检查点。记录帧携带 8 字节 `agent_id`（26 字节帧头），引擎按 `(agent, idHash)` 域索引全部记录。**与 `0x0008`（及更早）的 `.meh` 数据文件不兼容**——Open 时显式拒绝，无迁移路径。v1.5.0 的话题单轨化不 bump 格式版本：旧库的 `user_keywords`/`agent_keywords` 在解码点归一进 `fused_keywords`
- **多 Agent 域** — `OpenMulti` + `CreateAgent(name)` / `Session(agentID)` / `ListAgents` / `DeleteAgent`：多个 agent 共享一个 `.meh` 文件，各自拥有完全隔离的域（话题缓存、Dream 管线、域级锁）；同 agent 串行、跨 agent 并行；空闲域按访问节奏回收内存（`Defaults.AgentIdleTTLMs`），记录仍在文件。多 agent 是唯一模式——所有操作都经由按域绑定的会话执行
- **L1 场景超图** — Dream 在关键词集合重叠的场景间创建共现超边（Jaccard ≥ `L1EdgeMinSimilarity`）并按时间衰减剪枝；查询期扩散联想已随检索子系统退役，L1 当前由 Dream 维护、供显式图查询与后续关联消费
- **Dream 巩固管线** — 作用于 L0–L2 及 L6 保留期清理：L2 压缩 → L2Meta 缓存重建 → L1 节点/超边重建 → L1 衰减 → L0 蒸馏（情绪/MBTI）→ L6 清理（自动丢弃 7 天前轨迹事件）；某场景 depth-1 话题数超过 `Defaults.SceneDreamTopicThreshold` 时由 `Update` 后台调度该场景巩固，返回逐阶段 `DreamReport`
- **L3 知识图谱** — 多独立超图，节点导入支持位置引用（source_ref）与关系边（related），CRUD、关键词/类型查询与 BFS 子图
- **设计层面单实例** — 一个 `.meh` 文件只有一个持有者：全平台文件排他锁强制（linux/darwin/windows），第二次 `Open` 直接失败；内嵌形态无服务进程、无后台守护
- **极简依赖、可内嵌** — 5 个直接 Go 依赖（xxhash、gse、go-openai、go-sdk、golang.org/x/sys）；gse 只剩一个读者——关键词提炼失败时的启发式分词兜底；**引擎不再联系任何 embedding / 向量服务**，配置里也没有维度要声明，`sync.RWMutex` + `atomic.Pointer`，零基础设施
- **MCP Server** — `cmd/memhop-mcp` 将 42 个公开会话方法中的 30 个以 MCP 工具通过多租户 HTTP 暴露（SSE + streamable-http，官方 `modelcontextprotocol/go-sdk`）：单进程服务多个宿主，共享一个 `.meh` 文件，每个租户按 URL 路径 `/mcp/<tenant-id>` 隔离到独立 agent 域（租户名 → 稳定 agentID，`os.Root` 锚定 db 目录）。刻意只留在 Go 侧：L6 计划面（`PlanAppend`/`PlanCommit`/`PlanState`/`PlanReplace`/`SyncPlanTree`/`ListPlans`）、记忆纠错（`DeleteTopic`/`DeleteScene`）、轮内 `AppendL4Message`/`RefineTopicKeywords`、`SetSceneL3ID`、`ListScenesByL3`、`DistillL0`——这些要由持有会话状态的宿主来调

## 快速开始

> 完整集成指南（配置、各层 API、N:N 回合、陷阱）：
> [INTEGRATION_GUIDE.zh.md](INTEGRATION_GUIDE.zh.md) · English: [INTEGRATION_GUIDE.md](INTEGRATION_GUIDE.md)

```go
import (
    "context"
    "log"
    "os"
    "time"

    memhop "github.com/qyiun666/MemHop/api"
)

dbm, err := memhop.OpenMulti(&memhop.MemHopConfig{
    DBPath: "agent.meh", // 整个数据库就是这一个文件；无需服务，也不声明维度
    LLM: memhop.LlmConfig{ // 必填：Open 时校验（Update 的一轮提炼用它）
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
    Defaults: *memhop.DefaultMemHopDefaults,
})
if err != nil {
    log.Fatal(err)
}
defer dbm.Close()

// 一个 .meh 文件承载多个隔离域。CreateAgent 返回稳定的 16 位 hex ID；
// Session 把每次调用绑定到该域。
agentID, err := dbm.CreateAgent("my-agent")
if err != nil {
    log.Fatal(err)
}
sess, err := dbm.Session(agentID)
if err != nil {
    log.Fatal(err)
}

// 读记忆 = 读一个场景（场景就是宿主的一个会话）。
// SceneID 为空 → 库新建场景并返回其 id（L3ID 可选：挂到某个 L3 项目域）；
// SceneID 非空 → 该场景必须已存在，否则 ErrNotFound。
// 纯内存读：不调 LLM、不做向量编码、不打分。
res, err := sess.Search(memhop.SearchQuery{SceneName: "chat-42"})
if err != nil {
    log.Fatal(err)
}
sceneID := res.Scene.SceneID
for _, topic := range res.Topics { // 该会话的 depth-1 话题集 = 本轮上下文
    _ = topic.FusedKeywords
}

// 一轮结束：整轮一次沉淀（用户原文 + Agent 原文 + 各自时间戳），
// 库内一次提炼出该轮话题的关键词，返回新话题 id。
topicID, err := sess.Update(memhop.TurnUpdate{
    SceneID:   sceneID,
    UserText:  "昨天我们讨论了什么？",
    UserTS:    time.Now().UnixMilli(),
    AgentText: "Agent：...",
    AgentTS:   time.Now().UnixMilli(),
})
if err != nil {
    log.Fatal(err)
}

// 本轮的中间消息（工具输出等）可继续挂到同一话题，不调 LLM。
_, err = sess.AppendL4Message(topicID, "工具输出：...", time.Now().UnixMilli(),
    memhop.RoleAgent, memhop.ContentText)

// Dream 巩固（L0-L2）；sceneID 传空串 = 遍历域内全部场景。
// 场景话题数超阈值时 Update 已会自行后台调度，通常无需手动调用。
report, err := sess.Dream(context.Background(), "")
```



> **并发契约。** 同一 agent 的操作（Search / Update / Dream / 写 API）由库内域级锁串行，跨 agent 在 `*MultiAgentDB` 上并行，宿主无需自行排队。`*memhop.Session` 除绑定的域 ID 外不携带任何跨域状态。文件排他锁仍保证一个 `.meh` 文件只能被一个进程打开；`*MultiAgentDB` 的 `Lock()`/`Unlock()` 保留供宿主关键区使用。

前置条件：Go 1.27+，OpenAI 兼容的 LLM 接口（`Config.LLM` 必填）；无需任何 embedding / 向量服务

### API 概览

| 分组 | 方法 |
|------|------|
| 核心循环 | `Search(q)` · `Update(TurnUpdate) → topicID` · `Dream(ctx)` · `Checkpoint` · `Close` |
| L0 画像 | `GetL0` · `UpdateL0` |
| L2 上下文 | `ListScenes` · `ListScenesByL3` · `SetSceneL3ID` · `SceneContext` · `MergeScenes` · `DeleteTopic` · `DeleteScene` · `RefineTopicKeywords(ctx, id)` |
| L3 知识 | `GetL3` · `ListL3` · `ImportL3` · `UpdateL3` · `DeleteL3` · `QueryL3Nodes` · `QueryL3Subgraph` |
| L4 归档 | `SearchL4` · `GetArchive` · `AppendL4Message` |
| L5 能力 | `ImportCapability` · `GetCapability` · `UpdateCapability` · `DeleteCapability` · `ListCapabilities` · `ActivateCapability` · `RecordCapabilityUsage` |
| L6 轨迹 | `AppendTrajectory` · `ReadTrajectory` · `ListTrajectorySessions` · `Crystallize`（保留期 7 天自动清理，无删除接口） |
| L6 计划树 | `PlanAppend` · `PlanCommit` · `PlanState` · `PlanReplace` · `SyncPlanTree` · `ListPlans`（仅 Go module 暴露，MCP 工具集未接入） |

### 内置 L5 能力

仓库根目录 `capabilities/` 内置 **6 张能力卡**（`memhop-capability/v3` 格式，构建时随库内嵌，英文）：`memhop-guide`（循环分工总纲——Search/Update/Dream 与轨迹记录由宿主自动执行、LLM 勿手动调用——外加其余五张卡的索引）+ 五张 LLM 可调用说明书（knowledge、scene、archive、profile、capability）。卡描述 Go API 调用契约（`type: "api"`、`ref: "api:MethodName"`），宿主直接调用，无需 MCP 层。**资源即工具声明**：`name/desc/input/output` 与宿主工具规格（meowire `ToolSpec`）字段完全同构，宿主纯字段拷贝即可投影、零格式转换。**分层注入**：`ListCapabilities`/`GetCapability` 只读返回内置卡（与库存能力同套过滤器，不落 `.meh` 文件，同名能力按 ID 去重库存优先，`Search` 响应不附带）；默认只投影一行一卡索引（`id + name + summary + trigger`）+ guide 卡，参数详情按需 `GetCapability(id)` 获取。

## 架构

```
层级  名称            人脑类比              机制
───── ────────────── ───────────────────  ─────────────────────────────────────────────
 L6    Trajectory      程序性日志             宿主追加的操作轨迹事件，结晶为 L5 能力草稿
 L5    Crystal         肌肉记忆             可复用的能力包（技能 · MCP · 工具 · 提示词 · 服务）
 L4    Archive         长期记忆             原始对话日志与历史记录
 L3    Knowledge       语义记忆             多源超图知识库
 L2    Context         工作记忆             压缩的话题结构（4 级压缩深度）
 L1    Engram          场景超图             场景节点 + 关键词重叠超边；由 Dream 维护，供显式图查询
 L0    Profile         身份认同             Agent 人格、偏好与语言习惯
```

### Dream 管线

Dream 周期是一个自动记忆巩固过程，受人脑睡眠中处理经历的机制启发。Dream **仅作用于 L0–L2**（L3 蒸馏与 L5 结晶为设计外）加 L6 保留期清理，共五个阶段：

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
| `Search(SearchQuery{SceneID, L3ID, SceneName})` | 空 `SceneID` → 新建场景并返回其 id；非空 → 返回该场景的 depth-1 话题集（按用户消息时间升序）+ L0 画像 | 纯内存读（L2Meta 缓存），零 LLM、零 embedding、零打分；唯一写是场景命中计数 |
| `Update(TurnUpdate{...})` | 整轮一次沉淀：双原文各写一条 L4 档案，一次 LLM 提炼出该轮话题的 `FusedKeywords`，返回话题 id | 每轮恰好 1 次 LLM 调用；提炼失败即报错且零写入 |
| `AppendL4Message` / `RefineTopicKeywords` | 本轮中间消息续写 / 按全部原文重算关键词 | 前者零 LLM；后者每次 1 次 LLM |

宿主注入的上下文就是该场景 depth-1 话题的关键词集合；要看某轮原文，按话题的 `L4Refs` 走 `GetArchive`/`SearchL4` 或 `SceneContext`。上下文规模由 Dream 保证有界（`Consolidate` 要求压缩后每场景话题数 ≤ 20）。

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
api/                         ← 公开门面：openmulti（入口 + 租户管理）/ session（唯一业务句柄，hex ID 面）
                               / types / mapping / ids / errors / exports
internal/                    ← 业务装配层：config / db / session / defaults / tuning /
                               l0 / l2 / l3 / l3query / l4 / l5 / l6 / l6_plan / agents / agentctx /
                               search / update / dream / plancache / llm_client / llm_ops / models / exports
internal/repo/               ← 数据层：l0layer–l6layer + agentlayer（记录读写）
internal/repo/index/         ← 索引层：l2meta（场景读回的唯一支撑）/ rebuild（单遍重建）/
                               traj（L6 轨迹形状）/ tokenizer（gse，关键词兜底）
internal/repo/core/          ← .meh 引擎：engine / frame / header / snapshot / reclaim /
                               record / model / mmap / filelock
internal/common/             ← 最底层工具：enum / errors / hash /
                               sliceutil / strutil / timeutil
test/                         ← 集成测试（build tag：integration）
benches/fixtures/             ← 基准数据集（locomo10、locomo_smoke、longmemeval_smoke）
```

依赖方向严格单向：`api → internal → repo → core`，`common` 位于最底层（不引用任何其他 internal 包）。


> 说明：`docs/` 与 `AGENTS.md` 有意保留为本地文件（见 `.gitignore`），因此公开 clone 中 `docs/` 下的链接可能无法打开。

### LLM 调用与成本模型

- **读路径**（`Search`）：**零 LLM、零 embedding**，只走 L2Meta 内存缓存。
- **写路径**（`Update`）：每轮恰好一次关键词提炼（用户原文 + Agent 原文一起喂），输出上限 512 token 起、截断时逐级升预算，解析失败自降级为本地分词。
- **Dream**：每次巩固对达到话题数下限（`DreamCompressMinTopics`，默认 20）的场景各调一次 L2 合并，再加一次 L0 蒸馏（最多 200 个排序后的 L1 样本，每个样本最多 20 个关键词）。输出上限分别为 8192 / 2048 token。
- **Crystallize**：每次显式触发调用一次，按 session 轨迹输入。
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
| v1.5.0 | 2026-09-01 | L2 换轨：场景 = 宿主会话 | 1. **Search 重写为场景内直取**：入参只剩 `{scene_id, l3_id, scene_name}`，空 id = 新建场景，非空未知 id = `ErrNotFound`；返回 `{profile, profile_brief, scene, topics}`（该场景 depth-1 话题集），**删除** `contexts`/`associated_contexts`/`new_topic_id`/`auto_create`/`directed_l2_id`/`directed_l3_id` 与 `ctx` 参数——零 LLM、零 embedding、零打分<br>2. **Update 一次沉淀整轮**：入参 `TurnUpdate{scene_id, user_text, user_ts, agent_text, agent_ts}`，一次 LLM 提炼出该轮关键词并返回话题 id；提炼排在所有写入之前，失败零留痕<br>3. **话题单轨**：删除 `user_keywords`/`agent_keywords`/`centroid_page_ref`/`l3_refs`，只留 `fused_keywords`；Dream 压缩、L1 超边、宿主注入共用同一轨，不设摘要字段（原文在 L4）<br>4. **场景 ID 归宿主**：`NewSceneSlot(sceneID, name)` 不再哈希名字，`CreateSceneL2WithID` 幂等复用；`ensureSceneForTopic` 的 `timestamp:文本` 命名场景路径消失<br>5. **检索子系统整体删除**：`internal/cap/scenefind`（BM25+向量+实体三通道、RRF、场景加分、L1 扩散）、话题质心、`RecVecCentroid`、`Encoder`/`HttpEncoder`/`OpenMultiWithEncoder` 与 encoder 配置全部移除——**引擎不再联系任何 embedding 服务**；agent 级 BM25 失去读者，不再写入与落快照（失去读者的 BM25 / 实体模糊 / L3 索引岛一并删除——L3 节点查询本就是记录扫描）<br>6. **巩固触发改轴**：`activeScenes`/`Capacity` 窗口与 `ActiveSceneIDs`/`HasActiveScenes` 删除，`Update` 在场景 depth-1 话题数超 `SceneDreamTopicThreshold`（默认 24）时后台调度该场景 Dream；`Dream(ctx,"")` 遍历域内全部场景<br>7. `RefineTopicKeywords` 去掉幂等守卫，改为按 `L4Refs` 全量原文无条件重算<br>8. **不 bump 格式版本**：`TopicSlot.UnmarshalJSON` 在解码点把旧库两轨归一进 `fused_keywords`，老 `.meh` 直接打开不丢词<br>9. 轮次话题 ID 走 `"turn:"` 命名空间（与 Dream 融合节点分域）；删除零调用的 `ComputeTopicIDForText` 与死字段 `SceneNode.VectorPageRef`<br>10. 场景归并不再发生于 Dream（会删掉宿主正持有的 sceneID），`MergeScenes` 保留为显式接口；MCP `search`/`update` 入参换轨、`status` 改报 `scene_count`、删除 `memhop_scene_active_list`（31 → 30），DSH 插件循环与面板同步适配 |
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
