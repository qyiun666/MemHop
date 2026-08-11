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
  <img src="https://img.shields.io/badge/go-1.26+-00ADD8.svg" alt="go">
  <img src="https://img.shields.io/badge/license-MIT%20OR%20Apache--2.0-blue.svg" alt="license">
</p>

<p align="center">
  <strong>当前版本：v1.1.0（架构重构）· 最新稳定 tag：v1.0.1</strong>
</p>

---

MemHop 是一个面向 AI Agent / 大模型（LLM）应用的**嵌入式长期记忆数据库**，纯 Go 实现。它不是一个向量数据库——它是以人脑知识组织方式为蓝本的记忆系统：具备身份认同、情景回忆、语义压缩、知识图谱、归档存储和结晶化技能。一个 Agent，一个 `.meh` 文件，零基础设施。

MemHop 是 **Agent 专用**记忆数据库：每个 Agent 绑定唯一的 `.meh` 文件，文件级排他锁保证同一文件同时只有一个实例（第二次 `Open` 直接报错）。支持 **Linux、macOS、Windows** 全平台，无 cgo，除嵌入/LLM 接口外无任何外部服务。

作为 [MeowAgent](https://github.com/meowagent/meowagent)（即将开源）的大脑记忆模块，MemHop 以内嵌器官而非独立服务的形式运行。无需启动服务器，无需管理配置——打开文件，Agent 便拥有记忆。

> **我们对 Agent 记忆的立场。** 记忆不应该是事后用向量数据库插件外挂上去的附属品，也不该是被塞进上下文窗口的纯文本日志。没有内化记忆的 Agent，不过是一个假装聪明的无状态函数。MemHop 的存在基于一个信念：记忆必须是*认知的*——像人脑一样结构化、压缩、巩固、遗忘——并且是*内嵌的*——活在 Agent 进程内部，而非躲在一次网络调用的背后。一个文件，零基础设施，心智随每次对话成长。

## 核心特性

- **六层认知架构** — L0 画像 → L1 纠缠图 → L2 上下文 → L3 知识 → L4 归档 → L5 结晶，配合 Dream 巩固管线
- **三通道 RRF 检索** — BM25（gse CJK 分词）+ f32 向量 + 实体模糊匹配，通过 Reciprocal Rank Fusion（k=60）融合
- **V2 追加写入存储** — `.meh` 格式（`FormatVersion=0x0004`），A/B 双头 + 记录级 CRC32 + 撕裂尾帧截断恢复，mmap 零拷贝读取，快照/检查点。**与 v1 的 `.meh` 数据文件不兼容**（JSON 序列化切换为原生数字）
- **Dream 巩固管线** — 仅作用于 L0–L2 的五阶段：L2 压缩 → L1 重建 → L1 衰减 → L0 画像 → L0 蒸馏（情绪/MBTI）
- **L3 知识图谱** — 多独立超图，内置团扩展 + Louvain 社区发现、BFS 遍历与邻接缓存
- **设计层面单实例** — 一个 Agent = 一个 `.meh` 文件，全平台文件排他锁强制（linux/darwin/windows）
- **极简依赖、可内嵌** — 4 个直接 Go 依赖（xxhash、gse、ollama、go-openai），`sync.RWMutex` + `atomic.Pointer`，零基础设施

## 快速开始

```go
import (
    "context"
    "os"
    "time"

    memhop "github.com/qyiun666/MemHop/internal"
    "github.com/qyiun666/MemHop/internal/sub"
    "github.com/qyiun666/MemHop/internal/sub/common"
)

db, err := memhop.Open(&sub.MemHopConfig{
    DBPath:      "agent.meh",
    VectorDim:   1024,
    EncoderAddr: "http://127.0.0.1:11434",
    EmbedModel:  "qllama/bge-m3:q4_k_m",
    LLM: sub.LlmConfig{ // 必填：Open 时校验
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
    Defaults: *sub.DefaultMemHopDefaults,
})
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// 检索 —— 三条路由：AutoCreate（跳过检索，直建新场景+话题）、
// DirectedL2ID（定向写入指定场景）、默认三通道检索。
// Timestamp 必填：消息的 Unix 毫秒时间戳。
res, err := db.Search(sub.SearchQuery{
    Text:      "昨天我们讨论了什么？",
    Timestamp: time.Now().UnixMilli(),
})
if err != nil {
    log.Fatal(err)
}

// 将 Agent 回复追加到 Search 创建的话题。
// Update 的 topicID 参数为 hex 字符串（common.FormatHash）。
topicID := common.FormatHash(res.NewTopicID)
_ = db.Update(topicID, "Agent：...", time.Now().UnixMilli())

// Dream 巩固（作用于激活场景，L0-L2）
ok, err := db.Dream(context.Background())
```

前置条件：Go 1.26+，Ollama（`ollama pull qllama/bge-m3:q4_k_m`），OpenAI 兼容的 LLM 接口（`Config.LLM` 必填）

### API 概览

| 分组 | 方法 |
|------|------|
| 核心循环 | `Search` · `Update` · `Dream` · `Checkpoint` · `Close` |
| L0 画像 | `GetL0` · `UpdateL0` |
| L2 上下文 | `ListScenes` · `MergeScenes` |
| L3 知识 | `GetL3` · `ListL3` · `ImportL3` · `UpdateL3` · `DeleteL3` · `QueryL3Nodes` · `QueryL3Subgraph` |
| L4 归档 | `SearchL4` · `GetArchive` |
| L5 结晶 | `CreateL5` · `GetL5` · `UpdateL5` · `DeleteL5` · `ListL5` |

## 架构

```
层级  名称            人脑类比              机制
───── ────────────── ───────────────────  ─────────────────────────────────────────────
 L5    Crystal         肌肉记忆             结晶化的流程与可复用技能
 L4    Archive         长期记忆             原始对话日志与历史记录
 L3    Knowledge       语义记忆             多源超图知识库
 L2    Context         工作记忆             压缩的话题结构（4 级压缩深度）
 L1    Engram          联想超图             连接 L2 上下文的超图骨架
 L0    Profile         身份认同             Agent 人格、偏好与语言习惯
```

### Dream 管线

Dream 周期是一个自动记忆巩固过程，受人脑睡眠中处理经历的机制启发。Dream **仅作用于 L0–L2**（L3 蒸馏与 L5 结晶为设计外），共五个阶段：

1. **L2 压缩** — LLM 归组合并相关话题，每个激活场景一个 goroutine 并行处理，降级陈旧上下文
2. **L1 重建** — 重建连接 L2 上下文的超图骨架（检索索引在同一趟扫盘中重建）
3. **L1 衰减** — 衰减情景重要性，剪枝弱节点/边
4. **L0 画像** — 基于巩固后的记忆重建 Agent 画像
5. **L0 蒸馏** — 蒸馏情绪/MBTI 模式（恒执行；L1 采样为空时自动跳过）

`Dream(ctx) (bool, error)` 整个周期持有写锁，无激活场景时直接返回成功，并在阶段间响应 `ctx` 取消。

### 检索

`Search` 分发到三条路由之一：`AutoCreate`（跳过检索，直建新场景+话题）、`DirectedL2ID`（定向写入指定场景）、默认检索路由（可通过 `DirectedL3ID` 限定范围）。检索路由使用**三通道融合召回**（BM25 + 向量 + 实体）配合 RRF：

| 通道 | 方法 |
|------|------|
| BM25 | 通过倒排索引进行关键词匹配（gse CJK 分词） |
| 向量 | 通过 Ollama HTTP embed 接口进行 f32 单精度语义相似度检索 |
| 实体 | 知识图谱实体模糊名称匹配 |

融合后处理：关键词重合打分 → 活跃/最近场景的加性场景加分 → L1 关联扩展 → L5 结晶匹配 → L0 画像组装。

## 基准测试

### LoCoMo 检索召回（v1.1.0）

基于 [LoCoMo](https://github.com/snap-research/locomo)（ACL 2024）的长期对话记忆召回测试，**仅评估检索层**（不含答案生成）：每个 QA 对已灌入的 `.meh` 记忆发起检索，由 LLM judge 判定返回的上下文单独是否足以回答问题。

| 范围 | 会话数 | 轮数 | QA | 可答率 recall | 实体命中率 |
|------|--------|------|-----|---------------|------------|
| 3 个对话集（conv-26/30/41） | 70 | 1,451 | 497 | 0.531（264/497） | 0.945 |
| 1 个对话集（conv-26） | 19 | 419 | 199 | 0.709（141/199） | 0.883 |

- **可答率**覆盖 LoCoMo 全部五类问题，含 22.5% 对抗陷阱题（其正确行为是拒答，上下文不可答即为正确结果），因此是保守下界；可答类（1-4 类）估算约 0.69。
- **实体命中率**为无模型硬指标：答案关键 token 出现在检索上下文中的 QA 占比。
- `Search` 将上下文返回给宿主（如 MeowAgent）作为生成上下文；检索本身不做答案生成。

复现：

```bash
# 1 个对话集
go test -tags integration ./test/ -run '^$' -bench BenchmarkLocomoRecall -benchtime 1x
# 3 个对话集
MEMHOP_LOCOMO_ITEMS=3 go test -tags integration ./test/ -run '^$' -bench BenchmarkLocomoRecall -benchtime 1x
```

分析与竞品定位：[docs/benchmarks/locomo_recall_analysis.md](docs/benchmarks/locomo_recall_analysis.md)

## 项目结构

```
internal/                     ← 装配层：DB 门面（open、search、update、dream、l0~l5）
internal/sub/                 ← 业务装配层：config / db / defaults / search / update /
                                dream / scenefind / llm_client / llm_ops / encoder
internal/sub/repo/            ← 数据层：open + l0layer~l5layer（记录读写、向量存取）
internal/sub/repo/index/      ← 索引层：sparse（BM25）/ l1_reverse / l2meta / l3_index /
                                entity / rebuild / tokenizer（gse）
internal/sub/repo/core/       ← .meh 引擎：engine / frame / header / snapshot / reclaim /
                                record / model / mmap / filelock
internal/sub/common/          ← 最底层工具：bktree / cosine / enum / errors / hash /
                                sliceutil / strutil / vec
test/                         ← 集成测试（build tag：integration）
benches/fixtures/             ← 基准数据集（locomo10、locomo_smoke、longmemeval_smoke）
```

依赖方向严格单向：`internal → sub → repo → core`，`common` 位于最底层（不引用任何其他 internal 包）。

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
|------|--------------------|------|---------|
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
| MeowDesk | [github.com/qyiun666/MeowDesk](https://github.com/qyiun666/MeowDesk) — 即将开源 |
| 官网 | [qyiun666.github.io/meowagent.github.io](https://qyiun666.github.io/meowagent.github.io/) |
| 邮箱 | qyiun666@163.com |

<p align="center">⭐️ <a href="https://github.com/qyiun666/MemHop">在 GitHub 上给 MemHop 点个小星星</a> — 你的支持是我们的动力！</p>

## 许可证

MIT OR Apache-2.0
