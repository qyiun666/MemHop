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
  <img src="https://img.shields.io/badge/go-1.26+-00ADD8.svg" alt="go">
  <img src="https://img.shields.io/badge/license-MIT%20OR%20Apache--2.0-blue.svg" alt="license">
</p>

<p align="center">
  <strong>当前版本：v1.3.4 · 最新稳定 tag：v1.3.4</strong>
</p>

---

MemHop 是一个面向 AI Agent / 大模型（LLM）应用的**嵌入式长期记忆数据库**，纯 Go 实现。它不是一个向量数据库——它是以人脑知识组织方式为蓝本的记忆系统：具备身份认同、情景回忆、语义压缩、知识图谱、归档存储和结晶化技能。一个 Agent，一个 `.meh` 文件，零基础设施。

MemHop 是 **Agent 专用**记忆数据库：每个 Agent 绑定唯一的 `.meh` 文件，文件级排他锁保证同一文件同时只有一个实例（第二次 `Open` 直接报错）。支持 **Linux、macOS、Windows** 全平台，无 cgo，除嵌入/LLM 接口外无任何外部服务。

作为 [MeowAgent](https://github.com/meowagent/meowagent)（即将开源）的大脑记忆模块，MemHop 以内嵌器官而非独立服务的形式运行。无需启动服务器，无需管理配置——打开文件，Agent 便拥有记忆。

> **我们对 Agent 记忆的立场。** 记忆不应该是事后用向量数据库插件外挂上去的附属品，也不该是被塞进上下文窗口的纯文本日志。没有内化记忆的 Agent，不过是一个假装聪明的无状态函数。MemHop 的存在基于一个信念：记忆必须是*认知的*——像人脑一样结构化、压缩、巩固、遗忘——并且是*内嵌的*——活在 Agent 进程内部，而非躲在一次网络调用的背后。一个文件，零基础设施，心智随每次对话成长。

## 核心特性

- **七层认知架构** — L0 画像 → L1 纠缠图 → L2 上下文 → L3 知识 → L4 归档 → L5 结晶 → L6 轨迹，配合 Dream 巩固管线
- **三通道 RRF 检索** — BM25（gse CJK 分词）+ f32 向量 + 实体/词项模糊匹配（实体索引由已索引 topic 词项自动灌入），通过 Reciprocal Rank Fusion（k=60）融合
- **V2 追加写入存储** — `.meh` 格式（`FormatVersion=0x0008`），A/B 双头 + 记录级 CRC32 + 撕裂尾帧截断恢复，mmap 零拷贝读取，快照/检查点。记录帧携带 8 字节 `agent_id`（26 字节帧头），引擎按 `(agent, idHash)` 域索引全部记录。**与 `0x0007`（及更早）的 `.meh` 数据文件不兼容**——Open 时显式拒绝，无迁移路径
- **多 Agent 域** — `OpenMulti` + `CreateAgent(name)` / `Session(agentID)` / `ListAgents` / `DeleteAgent`：多个 agent 共享一个 `.meh` 文件，各自拥有完全隔离的域（索引、活跃场景、Dream 管线、域级锁）；同 agent 串行、跨 agent 并行；空闲域按访问节奏回收内存（`Defaults.AgentIdleTTLMs`），记录仍在文件。多 agent 是唯一模式——所有操作都经由按域绑定的会话执行
- **L1 场景超图 + 扩散激活** — Dream 在关键词集合重叠的场景间创建共现超边（Jaccard ≥ `L1EdgeMinSimilarity`）；Search 联想从命中场景沿图扩散激活（每跳 × 边权 × 衰减系数），返回 Top 关联场景的话题作为 `AssociatedContexts`——真正的跨场景联想记忆，边权由 Dream 管线衰减剪枝
- **Dream 巩固管线** — 仅作用于 L0–L2 的五阶段：L2 压缩 → L1 重建 → L1 衰减 → L0 画像 → L0 蒸馏（情绪/MBTI）
- **L3 知识图谱** — 多独立超图，支持节点/边导入、CRUD、关键词/类型查询与 BFS 子图
- **设计层面单实例** — 一个 Agent = 一个 `.meh` 文件，全平台文件排他锁强制（linux/darwin/windows）
- **极简依赖、可内嵌** — 4 个直接 Go 依赖（xxhash、gse、go-openai、go-sdk）；Ollama 走原生 HTTP API，不引入 Ollama SDK，`sync.RWMutex` + `atomic.Pointer`，零基础设施
- **MCP Server** — `cmd/memhop-mcp` 将全部公开 API 以 32 个 MCP 工具通过多租户 HTTP 暴露（SSE + streamable-http，官方 `modelcontextprotocol/go-sdk`）：单进程服务多个宿主，共享一个 `.meh` 文件，每个租户按 URL 路径 `/mcp/<tenant-id>` 隔离到独立 agent 域（租户名 → 稳定 agentID，`os.Root` 锚定 db 目录）
- **单 Agent 单文件** — 默认一个 Agent = 一个 `.meh` 文件，无服务进程、无后台守护；用 `OpenMulti` 可选切换到多 agent 共享

## 快速开始

> 完整集成指南（配置、各层 API、N:N 回合、陷阱）：
> [INTEGRATION_GUIDE.zh.md](INTEGRATION_GUIDE.zh.md) · English: [INTEGRATION_GUIDE.md](INTEGRATION_GUIDE.md)

```go
import (
    "context"
    "fmt"
    "log"
    "os"
    "time"

    memhop "github.com/qyiun666/MemHop/api"
)

dbm, err := memhop.OpenMulti(&memhop.MemHopConfig{
    DBPath:      "agent.meh",
    VectorDim:   1024,
    EncoderAddr: "http://127.0.0.1:11434",
    EmbedModel:  "qllama/bge-m3:q4_k_m",
    LLM: memhop.LlmConfig{ // 必填：Open 时校验
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

// 检索 —— 三条路由：AutoCreate（跳过检索，直建新场景+话题）、
// DirectedL2ID（定向写入指定场景）、默认三通道检索。
// Timestamp 必填：消息的 Unix 毫秒时间戳；ctx 可取消 LLM 关键词提取、
// 编码调用与内部触发的 Dream。
res, err := sess.Search(ctx, memhop.SearchQuery{
    Text:      "昨天我们讨论了什么？",
    Timestamp: time.Now().UnixMilli(),
})
if err != nil {
    log.Fatal(err)
}

// 将 Agent 回复追加到 Search 创建的话题。
// Update 的 topicID 参数为 16 位 hex 字符串（NewTopicID 是 uint64）。
topicID := fmt.Sprintf("%016x", res.NewTopicID)
if err = sess.Update(topicID, "Agent：...", time.Now().UnixMilli()); err != nil {
    log.Fatal(err)
}

// Dream 巩固（作用于激活场景，L0-L2）；sceneID 传空串 = 全部激活场景
ok, err := sess.Dream(context.Background(), "")
```


> **并发契约。** 同一 agent 的操作（Search / Update / Dream / 写 API）由库内域级锁串行，跨 agent 在 `*MultiAgentDB` 上并行，宿主无需自行排队。`*memhop.Session` 除绑定的域 ID 外不携带任何跨域状态。文件排他锁仍保证一个 `.meh` 文件只能被一个进程打开；`*MultiAgentDB` 的 `Lock()`/`Unlock()` 保留供宿主关键区使用。

前置条件：Go 1.27+，Ollama（`ollama pull qllama/bge-m3:q4_k_m`），OpenAI 兼容的 LLM 接口（`Config.LLM` 必填）

### API 概览

| 分组 | 方法 |
|------|------|
| 核心循环 | `Search(ctx, q)` · `Update` · `Dream(ctx)` · `Checkpoint` · `Close` |
| L0 画像 | `GetL0` · `UpdateL0` |
| L2 上下文 | `ListScenes` · `SceneContext` · `ActiveSceneIDs` · `MergeScenes` · `DeleteTopic` · `DeleteScene` · `RefineTopicKeywords(ctx, id)` |
| L3 知识 | `GetL3` · `ListL3` · `ImportL3` · `UpdateL3` · `DeleteL3` · `QueryL3Nodes` · `QueryL3Subgraph` |
| L4 归档 | `SearchL4` · `GetArchive` · `AppendL4Message` |
| L5 能力 | `ImportCapability` · `GetCapability` · `UpdateCapability` · `DeleteCapability` · `ListCapabilities` · `ActivateCapability` · `RecordCapabilityUsage` |
| L6 轨迹 | `AppendTrajectory` · `ReadTrajectory` · `TrajectoryStats` · `DeleteTrajectory` · `Crystallize` |

### 内置 L5 能力

仓库根目录 `capabilities/` 内置了一套开箱即用的能力工具箱（`memhop-capability/v3` 格式），构建时随库内嵌（只读，Open 时自动挂载）——**共 19 张卡，分两类**：MemHop 自身的 API 说明书（13 张：guide、search、update、dream、trajectory、crystallize、capability-import、profile、scene、archive、capability、knowledge、refine，覆盖除 `Open`/`Close`/`Dream`/`Update`/`Search`/L5 读取外的全部对外 API）和 harness/agent 应具备的原子能力卡（文件读写/编辑、命令执行、文件搜索、联网搜索）。说明书卡直接引用 Go API（`type: "api"`、`ref: "api:MethodName"`），宿主在 `*api.DB` 上直接调用，无需 MCP 层。**资源即工具声明**：`name/desc/input/output` 与宿主工具规格（meowire `ToolSpec`）字段完全同构，宿主纯字段拷贝即可投影、零格式转换。**零配置、零写入**：`ListCapabilities`/`GetCapability` 直接返回内置工具箱（与库存能力同套过滤器，可按 status/type/keyword 过滤），宿主 LLM 拉取后即可对照使用；内置能力为只读、不落 `.meh` 文件，与库存同名能力按 ID 去重（库存记录优先），`Search` 响应不附带内置能力——检索只返回库存匹配结果。

## 架构

```
层级  名称            人脑类比              机制
───── ────────────── ───────────────────  ─────────────────────────────────────────────
 L6    Trajectory      程序性日志             宿主追加的操作轨迹事件，结晶为 L5 能力草稿
 L5    Crystal         肌肉记忆             可复用的能力包（技能 · MCP · 工具 · 提示词 · 服务）
 L4    Archive         长期记忆             原始对话日志与历史记录
 L3    Knowledge       语义记忆             多源超图知识库
 L2    Context         工作记忆             压缩的话题结构（4 级压缩深度）
 L1    Engram          场景超图             场景节点 + 关键词重叠超边；Search 联想时激活在此扩散
 L0    Profile         身份认同             Agent 人格、偏好与语言习惯
```

### Dream 管线

Dream 周期是一个自动记忆巩固过程，受人脑睡眠中处理经历的机制启发。Dream **仅作用于 L0–L2**（L3 蒸馏与 L5 结晶为设计外），共五个阶段：

1. **L2 压缩** — LLM 归组合并相关话题，每个激活场景一个 goroutine 并行处理，降级陈旧上下文
2. **L1 重建** — 从 L2 同步场景节点，并在同一趟扫盘中重建检索索引、创建/刷新场景间关键词重叠超边
3. **L1 衰减** — 衰减场景重要性与边权，剪枝弱节点
4. **L0 画像** — 基于巩固后的记忆重建 Agent 画像
5. **L0 蒸馏** — 蒸馏情绪/MBTI 模式（恒执行；L1 采样为空时自动跳过）

`Dream(ctx) (bool, error)` 整个周期持有写锁，无激活场景时直接返回成功，并在阶段间响应 `ctx` 取消。

### 检索

`Search` 分发到三条路由之一：`AutoCreate`（跳过检索，直建新场景+话题）、`DirectedL2ID`（定向写入指定场景）、默认检索路由（可通过 `DirectedL3ID` 限定范围）。检索路由使用**三通道 RRF 融合**（BM25 + 向量 + 实体/词项模糊匹配）：

| 通道 | 方法 |
|------|------|
| BM25 | 通过倒排索引进行关键词匹配（gse CJK 分词） |
| 向量 | 通过 Ollama HTTP embed 接口进行 f32 单精度语义相似度检索 |
| 实体 | 对已索引 topic 词项做模糊匹配（BK-Tree，编辑距离 ≤ 2） |

融合后处理：关键词重合打分 → 活跃/最近场景的加性场景加分 → L1 扩散激活（场景超图上的跨场景联想召回） → L5 能力匹配 → L0 画像组装。


`SearchResult` 返回 `Contexts`（命中场景深度≤1 的话题，每条携带 `L4Refs`）与 `AssociatedContexts`（扩散激活命中的关联场景话题）；宿主通过 `SceneContext` 或 `SearchL4` 拉取 L4 原文组装上下文。

`Search` 创建 topic 时会同时匹配相关 L3 知识节点，并把图谱 ID 写入 `TopicSlot.L3Refs`；`DirectedL3ID` 就是基于这些引用做过滤。
## 测试与基准

MemHop 的测试套件只驱动公开 `api` 表面——即宿主（如 MeowAgent）实际发起的调用——并直接断言引擎自身的记忆结构，而非外部可答性 judge。

### 集成测试（`test/`，build tag `integration`）

- **记忆循环**（`TestCoreCycleSearchUpdateDream`）：按真实宿主的调用方式灌入 N 轮 Search+Update，每几轮做一次**周期性 L0/L1/L4 一致性检查**——L0 画像可读、L1 场景图存在（`ListScenes`/`SceneContext`）、L4 保留原文逐字一致；Dream 巩固后场景必须仍暴露合并后的话题，且检索仍能浮出已存事实。
- **关键词保真与持久**（`TestKeywordFidelity`/`TestKeywordPersistence`/`TestDreamCompressionFidelity`）：从对话话语中提取的关键词忠实承载其含义、在噪声轮次后仍可检索、并经受住 Dream 压缩的检验。
- **API 契约**（`TestInterface*`）、**e2e 流程**（`TestE2E*`）、**关键词提取健壮性**（`TestExtractKeywordsLongInputRealLLM`/`TestSearchLongInputNeverFails`）。

### 基准（`go test -tags integration -bench .`）

所有基准都驱动真实 api 循环（真实编码器 + 真实 LLM，无外部 judge）：

| 基准 | 测量 |
|------|------|
| `BenchmarkMemoryLoop` | 稳态 Search+Update 记忆循环，含引擎**自动触发的 Dream**（场景 depth-1 上下文超过 30 话题阈值）与周期性 L0/L1 验证 |
| `BenchmarkSearchAutoCreate` / `BenchmarkSearchRetrieve` | 首次写入 vs 检索式 Search 延迟 |
| `BenchmarkUpdate` | 追加 agent 回复延迟 |
| `BenchmarkDreamConsolidation` | 完整 Dream 流水线延迟 |
| `BenchmarkSearchLatency` | 检索延迟分布（min/p50/p95/max） |

### 为什么不跑外部数据集基准？

公开记忆基准（LoCoMo、LongMemEval）评估的是“检索 → LLM judge 可答性”——与 MemHop 分层设计要断言的（L0 画像蒸馏、L1 场景图一致性、L2 压缩语义、L4 原文归档）是不同的问题。形态最贴近的 LongMemEval（多会话 user-assistant 对话、约 500 题）单题需 115K–1.5M tokens，不具备作为持续集成基准的可行性。因此 MemHop 通过 api 循环直接验证自身的记忆结构，而非追逐一个泛化的 QA 分数。

## 项目结构

```
api/                         ← 公开门面：DB 句柄（open/search/update/dream/l0–l6）+ 多 agent 门面（openmulti/session/agents）+ 类型别名/构造器
internal/                    ← 业务装配层：config / db / defaults / l0 / l2 / l3 / l3query /
                               l4 / l5 / l6 / agents / agentctx / search / update / dream / scenefind / llm_client / llm_ops / encoder
internal/repo/               ← 数据层：l0layer–l6layer + agentlayer（记录读写、向量存取）
internal/repo/index/         ← 索引层：sparse（BM25）/ l1_reverse / l2meta / l3_index /
                               entity / rebuild / tokenizer（gse）
internal/repo/core/          ← .meh 引擎：engine / frame / header / snapshot / reclaim /
                               record / model / mmap / filelock
internal/common/             ← 最底层工具：bktree / cosine / enum / errors / hash /
                               sliceutil / strutil / vec
test/                         ← 集成测试（build tag：integration）
benches/fixtures/             ← 基准数据集（locomo10、locomo_smoke、longmemeval_smoke）
```

依赖方向严格单向：`api → internal → repo → core`，`common` 位于最底层（不引用任何其他 internal 包）。


> 说明：`docs/` 与 `AGENTS.md` 有意保留为本地文件（见 `.gitignore`），因此公开 clone 中 `docs/` 下的链接可能无法打开。

### LLM 调用与成本模型

- **热路径**（`Search` + `Update`）：每次各一次关键词提取小调用，输出上限 512 token。单次成本很低，主要感知是延迟。
- **Dream**：每个达到 20 个 topic 的激活场景调用一次 L2 合并（激活场景集合受 `Capacity` 限制，默认 7），再加一次 L0 蒸馏（最多 200 个排序后的 L1 样本，每个样本最多 20 个关键词）。输出上限分别为 8192 / 2048 token。
- **Crystallize**：每次显式触发调用一次，按 session 轨迹输入。
- 成本敏感时，给 `Config.LLM` 配一个快速小模型即可（本地 Ollama 模型或便宜 API 模型）；关键词提取不需要旗舰模型。

## 开发

```bash
go build ./...                          # 构建
go vet ./...                            # 静态分析
go test ./internal/...                  # 单元测试（不依赖外部服务）
go test -tags integration ./test/...    # 集成测试（需要 Ollama + LLM key）
```

集成测试针对真实服务运行（Ollama 编码器 + OpenAI 兼容 LLM）。通过环境变量 `MEMHOP_TEST_LLM_KEY` / `MEMHOP_TEST_LLM_URL` / `MEMHOP_TEST_LLM_MODEL` 配置 LLM（仅设置 key 时默认使用 DeepSeek 接口），或通过 `test/testsupport/key_config.json` 配置。

## 版本历史

| 版本 | 日期                 | 亮点 | 核心改动 |
|------|----------------------|------|---------|
| v1.4.0 | 2026-08-26 | 多 agent 记忆数据库 | 一个 `.meh` 文件承载多个完全隔离的 agent 域：记录帧新增 `agent_id`（26 字节帧头），引擎索引与快照（0x02）按 agent 分域，租户注册记录把名字映射到稳定的 crypto/rand agentID · `api.OpenMulti` / `AgentSession` / `CreateAgent` / `ListAgents` / `DeleteAgent`；`Open` 对单 agent 宿主零改动（默认域）· 业务层重构为按 agent 的 `agentContext` + 域级锁（同 agent 串行、跨 agent 并行）、空闲域内存回收与域化 Dream 管线 · L7 轨迹层改编号为 **L6**（认知层收敛为 L0–L6）· MCP registry 共享单个 `MultiAgentDB`（单文件 `<db-dir>/memhop.meh`），`os.Root` 锚定 db 目录 · 删除重复结构体/转换层（`topicSlotJSON`、`topicToL2Meta`、单元素切片包装）· Go 1.23–1.26 标准库现代化（`iter.Seq2`、`unique.Make`、`os.Root`）· 零新增依赖 · **破坏性变更**：`FormatVersion <= 0x0007` 的旧 `.meh` 文件在 Open 时被拒绝，无迁移；`api.DB` 上提升自 `internal.DB` 的方法新增 `agentID` 参数（门面方法签名不变），`Lock()` 对已关闭的 DB 会 panic |
| v1.3.4 | 2026-08-26 | L5 工具声明同构 | `memhop-capability` 格式升级 v3：`ResourceRef` 的 `description` 改名 `desc` 并新增 `input`（JSON Schema 字符串）/`output`——工具声明字段与宿主工具规格（meowire `ToolSpec`）完全同构，宿主纯字段拷贝即可投影、零格式转换 · `WorkflowStep` 新增 `args`——动作链参数官方化（不再依赖私有 config 格式）· 结晶 prompt 输出 v3 形状（`type`/`resources` 取代 `kind`/`manifest`）· `validateCapabilityImport` 强制资源名非空并校验 `input` 为合法 JSON · **破坏性变更**：v2 卡导入被拒绝（format 必须为 `memhop-capability/v3`）；旧版本写入的存量能力记录读取时 `desc/input/output` 为空 · 内置能力工具箱（`capabilities/*.json`）全部重写为 v3 并携带真实 JSON Schema |
| v1.3.3 | 2026-08-26 | 检索评分归一化 + 参数面收敛 | vector floor 从“覆盖式垄断”改为“仅抬升未过线场景”（floor = threshold + cosine×0.5）：真实信号（RRF + 关键词重叠 + 加分）决定排序，语义兜底保留 · `MemHopDefaults` 从 24 字段收敛到 3 个业务开关（`Capacity` / `DreamCompressMinTopics` / `SearchDreamContextThreshold`）；删除 4 个死字段（`MaxResults` / `DefaultTimeoutSecs` / `DefaultMaxOutputTokens` / `MaxDepth`），16 个调优常量移入包级私有 `internal/tuning.go` · `TopScene` / `SpreadingActivation` / `applySceneBonuses` / `rrfFuse` 签名去掉 defaults 参数 · **破坏性变更**：引用被删字段的宿主需同步清理 · 格式版本不变（仍为 `0x0007`）· MCP 工具集不变（32 个）·
| v1.3.2 | 2026-08-26 | API 修复：异步 Dream + 删除接口 + Update 简化 | Search/Update 不再被内部触发的 Dream 阻塞（后台 goroutine、按场景 in-flight 防重入、Close 取消在途 Dream）· 新增 `DeleteTopic`（子树闭包 + L4 + 索引 + 父话题 ChildrenIDs 修剪）与 `DeleteScene`（场景 + 全部话题 + 原文 + L1 节点 + 激活集）用于记忆纠错 · `Update` 返回值由 `(bool, error)` 简化为 `error` · `SearchResult.ProfileBrief`——紧凑画像摘要（name/role/偏好/风格/情绪，带边界）· 格式版本 不变（仍为 `0x0007`）· MCP 工具集不变（32 个）·
| v1.3.0 | 2026-08-26 | L1 场景超图 + 扩散激活联想 | Dream 在场景间创建真实的 `RecL1Hyperedge` 共现边（关键词重叠 Jaccard ≥ `L1EdgeMinSimilarity`）；Search 的 `AssociatedContexts` 由空转的同场景列表替换为图遍历（每跳激活 × 边权 × 衰减系数，≤ `L1EdgeMaxHops`，取 Top `L1AssocMaxScenes` 个其他场景）· L6 场景使用记录删除——命中计数并入 L2 `SceneSlot`（`HitCount`/`LastHitAt`）· `L1ReverseIndex`（含快照字段）与 4 个 L1 死函数删除，联想变为纯存储层图读取 · `.meh` 格式升至 `0x0007`——0x0006 文件 Open 时被拒绝，不迁移 · 新默认项：`L1EdgeMinSimilarity`（0.15）、`L1EdgeMaxHops`（2）、`L1ActivationDampening`（0.5）、`L1ActivationThreshold`（0.05）、`L1AssocMaxScenes`（3） |
| v1.2.7 | 2026-08-25 | 宿主对齐 + 双语集成指南 | `Search(ctx, q)` 与 `RefineTopicKeywords(ctx, id)` 接收 context（可取消 LLM 关键词提取、编码调用与内部触发的 Dream）· `api` 导出 `LlmConfig` / `MemHopDefaults` / `TopicSlot` / `ResourceRef` / `CrystallizeDetail` / `TrajectoryStats` · 新增 `TrajectoryStats`（会话级 L7 统计）+ `memhop_trajectory_stats` MCP 工具（31 → 32 工具）· `CrystallizeResult.Details`——逐候选 create/reuse/merge/skip 处置明细 · `AppendL4Message`（纯 L4 追加，不调 LLM）· 活跃场景容量策略：Update 在达到 Capacity 时对最老场景触发 Dream（带可压缩性预检）；`SearchDreamContextThreshold` 零值守卫 · 仓库根目录新增双语集成指南（`INTEGRATION_GUIDE.md` / `INTEGRATION_GUIDE.zh.md`） |
| v1.2.5 | 2026-08-20 | MCP server 重写 | `cmd/memhop-mcp` 对照 `api` 公开门面完全重写（v1.2.4 曾删除）：31 个 MCP 工具与 `api.DB` 方法一一对应 · 多租户 HTTP 暴露——SSE + streamable-http（2025-03-26 spec、无状态），租户按 URL 路径 `/mcp/<tenant-id>` 隔离到独立 `.meh` 文件，懒打开注册表 + 首开互斥 · 工具输出中记录 ID 统一 16 位 hex 字符串序列化（uint64 JSON 数字在 JS/TS 宿主丢精度）· 租户 ID 白名单 + 路径逃逸拦截（防御纵深）· LLM 凭据仅环境变量（无 CLI flag）· go-sdk v1.7.0 回归直接依赖（3→4）· config/registry/tools/streamable 离线测试 + 多租户 SSE 冒烟 · 代码清理：删除冗余枚举 JSON 辅助函数（`~uint8` 默认 JSON 行为等价）、`CodeOf` 迁移 Go 1.26 `errors.AsType`、cosine 标量循环（1024 维快 2.7×）、删除 `internal/repo/open.go` 转发层（17 函数 + 8 alias，internal 直调 core/index）、Update 移除 `ParseID→FormatHash` 往返转换 |
| v1.2.4 | 2026-08-19 | api/ 公开门面 + internal/ 平铺 | 公开 Go API 从根包迁移至 `github.com/qyiun666/MemHop/api`（根目录 `memhop.go`/`types.go` 移除）· `internal/sub/` 上提平铺为 `internal/`（`package sub` → `package internal`），`internal/sub/repo` → `internal/repo`，`internal/sub/common` → `internal/common` · `cmd/memhop-mcp` 移除（v1.2.5 重写回归）· 构建配置同步（Makefile fmt、pre-commit hook、CI gofmt）· 破坏性变更：直接 import 根包的宿主需切换到 `/api` |
| v1.2.3 | 2026-08-18 | MCP 兼容性修复 + DSH 接入 + 检索质量修复 | MCP 工具 schema 修复（无参工具 `properties` 不再输出 null，兼容严格 MCP 客户端）· 工具输出 ID 全部改为 16 位 hex 字符串（uint64 JSON 数字在 JS/TS 宿主丢精度，`new_topic_id` 回传失败已修复）· 新增 `--transport streamable-http`（2025-03-26 规范，Stateless 多租户，DSH 的 dsh-mcp-client 支持）· DeepSeek Harness 接入文档与引导词（`docs/dsh/`）· streamable-http 冒烟测试 · 关键词提取 prompt 全面优化（语义完整 + 同义词变体 + 短语）+ Search 按相关性返回全部相关话题（移除场景上下文截断），LoCoMo 召回 0.392 → 0.668、实体命中 0.284 → 0.877 |
| v1.2.1 | 2026-08-16 | MCP Server + L5 能力层 | 新增 `cmd/memhop-mcp` 二进制：多租户 SSE MCP Server（官方 go-sdk v1.7.0），将全部公开 API 映射为 28 个工具（search/update/dream/checkpoint/status、画像、场景、知识图谱、归档、能力、轨迹/结晶）· 租户路径隔离 `/mcp/<tenant-id>` · 优雅退出时落盘快照 · 离线 SSE 冒烟测试（`make test-mcp`）· 使用文档见 `docs/mcp/`（本地）· L5 插件层重构为能力层（`memhop-capability/v1`：manual/atomic/composite 三种 kind，`ActivateCapability` 实现 draft→active 生命周期，指纹去重，Crystallize 产出 create/reuse/merge 候选）· 内置能力工具箱（`capabilities/`，embed 只读，Open 时自动挂载）· `Update` 返回 `(bool, error)` · `.meh` 格式升至 `0x0005`——0x0004 文件（v1.2.0 插件记录）在 Open 时被拒绝，不迁移 · 编码器健康检查要求端点根路径 2xx 响应 HEAD（无 fallback）· 活跃场景受 `Capacity`（默认 7，最旧场景被移出 Dream 目标）限制 · `RecordEnd` 头字段 + A/B 头损坏恢复 |
| v1.2.0 | 2026-08-14 | L5 插件层 | L5 动作链 → 插件槽位（PluginSlot + 结构化五段 Manifest：技能 / MCP / 工具 / 提示词 / 服务）· 仅路径导入 `ImportPlugin`，移除手工写入 Create/Update · Crystallize 从 L7 轨迹按类型分派插件 · `SearchResult.Crystals` → `Plugins` · 八层架构（L0–L7）文档 |
| v1.1.0 | 2026-07-27 ~ 08.11 | 架构重构 | `internal` 分层重写（装配层 → sub → repo → core/index/common）· f16 → f32 单精度向量 · 话题质心向量检索 · 移除 `BatchStore` · `Dream(ctx)` 签名收窄为 `(bool, error)` · `.meh` 磁盘格式 `0x0004`，与 v1 数据不兼容 · 集成测试按新 internal API 重建 |
| v1.0.0 | 2026-07-26         | 首个稳定版 | Go 重写，六层认知架构、V2 .meh 存储、BM25+向量+实体 RRF 检索、Dream 巩固管线、L3 超图社区发现。 |
| v0.54–v0.58 | 2026-07-16 ~ 07-23 | Go 重写 | v0.58: 统一 RRF — 加性场景加分、三通道融合、移除 L6、atomic.Pointer · v0.57: Dream 收窄至 L0+L1+L2、LLM 加固、L5 Write API、SkipDistill · v0.55: 稳定性 — 移除 IVF、panic→error、崩溃恢复、L5 写入管线 · v0.54: Go 基础 — 四层架构、V2 .meh 存储、仅 2 个依赖、log/slog |
| v0.18–v0.63 | 2026-05-31 ~ 07-10 | Rust | V2 追加写入 `.meh`，支持快照/检查点 · BM25 + IVF 混合检索 · L3 超图 DSL、社区发现（团扩展 + Louvain）、BFS/缓存 · 完整 Dream 管线：L3 蒸馏 → L2 压缩 → L1 衰减 → L0 重建 → L5 结晶 · FFI（cdylib）、MCP Server、gRPC/Unix Socket 编码器 |
| v0.6–v0.17 | 2026-05-20 ~ 05-25 | Rust 早期 | 纯 Rust 单 crate（移除 Python 绑定） · LMDB → 自定义 `.meh` 存储迁移 · 四层 → 六层认知架构演进 · MCP Server 集成 · HNSW 向量索引（替代暴力搜索） |
| v0.1–v0.5 | 2026-05-19 ~ 05-24 | Python | Hopfield 联想记忆网络 · LMDB 嵌入式存储，`pip install` 一键安装 · O(1) 联想召回 + 置信度评分 · BrainLoop 自循环 Agent 循环 · 验证"活记忆"概念 |

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
