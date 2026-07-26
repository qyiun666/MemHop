<p align="center">
  <h1 align="center">MemHop</h1>
  <p align="center">
    <strong>你的 Agent 拥有类人记忆 —— 六层认知架构，单文件嵌入式记忆数据库。</strong>
  </p>
  <p align="center">
    <a href="README.md">English</a>
    &middot;
    <a href="https://qyiun666.github.io/meowagent.github.io/">官方网站</a>
    &middot;
    <a href="https://github.com/meowagent/meowagent">MeowAgent</a>
  </p>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/license-MIT%20OR%20Apache--2.0-blue.svg" alt="license">
  <img src="https://img.shields.io/badge/go-1.26+-00ADD8.svg" alt="go">
  <img src="https://img.shields.io/badge/test-passing-brightgreen.svg" alt="test">
</p>

---

MemHop 不是一个向量数据库。它是一个以人脑知识组织方式为蓝本的记忆系统——具备身份认同、情景回忆、语义压缩、技能习得、归档存储和结晶化专长。一个 Agent，一个 `.meh` 文件，零基础设施。

MemHop 是 **Agent 专用**记忆数据库：每个 Agent 绑定唯一的 `.meh` 文件，文件级排他锁保证同一文件同时只有一个实例（第二次 `Open` 直接报错）。支持 **Linux、macOS、Windows** 全平台，无 cgo，除嵌入/LLM 接口外无任何外部服务。

作为 [MeowAgent](https://github.com/meowagent/meowagent) 的大脑记忆模块，MemHop 以内嵌器官而非独立服务的形式运行。无需启动服务器，无需管理配置——打开文件，Agent 便拥有记忆。

> **我们对 Agent 记忆的立场。** 记忆不应该是事后用向量数据库插件外挂上去的附属品，也不该是被塞进上下文窗口的纯文本日志。没有内化记忆的 Agent，不过是一个假装聪明的无状态函数。MemHop 的存在基于一个信念：记忆必须是*认知的*——像人脑一样结构化、压缩、巩固、遗忘——并且是*内嵌的*——活在 Agent 进程内部，而非躲在一次网络调用的背后。一个文件，零基础设施，心智随每次对话成长。

## 核心特性

- **六层认知架构** — L0 画像 → L1 纠缠图 → L2 上下文 → L3 知识 → L4 归档 → L5 结晶，配合 Dream 巩固管线
- **三通道 RRF 检索** — BM25（gse CJK 分词）+ f16 向量 + 实体模糊匹配，通过 Reciprocal Rank Fusion（k=60）融合
- **V2 追加写入存储** — `.meh` 格式（MEH2 魔数），A/B 双头 + 记录级 CRC32 + 撕裂尾帧截断恢复，mmap 零拷贝读取，快照/检查点
- **Dream 巩固管线** — 仅作用于 L0–L2 的五阶段：L2 压缩 → L1 重建 → L1 衰减 → L0 画像 → L0 蒸馏（情绪/MBTI）
- **L3 知识图谱** — 多独立超图，内置团扩展 + Louvain 社区发现、BFS 遍历与邻接缓存
- **设计层面单实例** — 一个 Agent = 一个 `.meh` 文件，全平台文件排他锁强制（linux/darwin/windows）
- **极简依赖、可内嵌** — 4 个直接 Go 依赖（xxhash、gse、ollama、go-openai），`sync.RWMutex` + `atomic.Pointer`，零基础设施

## 快速开始

```go
import (
    "context"
    "time"

    memhop "github.com/qyiun666/MemHop/api"
)

db, err := memhop.Open(&memhop.Config{
    DBPath:      "agent.meh",
    VectorDim:   768,
    EncoderAddr: "http://127.0.0.1:11434",
    EmbedModel:  "qllama/bge-m3:q4_k_m",
    LLM: memhop.LlmConfig{ // 必填：Open 时校验
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
})
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// 检索（Timestamp 必填：消息的 Unix 毫秒时间戳）
results, _ := db.Search(memhop.SearchQuery{
    Text:       "昨天我们讨论了什么？",
    Timestamp:  time.Now().UnixMilli(),
    MaxResults: 10,
})

// 将 Agent 回复追加到 Search 创建的话题
_ = db.Update(results.NewTopicID, "Agent：...", time.Now().UnixMilli())

// 批量存储（每个 item 的 Keywords 必填）
db.BatchStore(memhop.StoreBatch{Items: []memhop.StoreItem{{
    Content:  "用户：...\nAgent：...",
    Keywords: []string{"项目", "截止日期"},
}}})

// Dream 巩固（L0-L2）
report, _ := db.Dream(context.Background(), nil)
```

前置条件：Go 1.26+，Ollama（`ollama pull qllama/bge-m3:q4_k_m`），OpenAI 兼容的 LLM 接口（`Config.LLM` 必填）

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

1. **L2 压缩** — LLM 归组合并相关话题，降级陈旧上下文
2. **L1 重建** — 重建连接 L2 上下文的超图骨架
3. **L1 衰减** — 衰减情景重要性，剪枝弱节点/边
4. **L0 画像** — 基于巩固后的记忆重建 Agent 画像
5. **L0 蒸馏** — 蒸馏情绪/MBTI 模式（可选，`SkipDistill`）

每次 Dream 调用最多发起 3 次 LLM 请求。`Dream(ctx, opts)` 串行化并发调用（第二个调用直接报错），并在阶段间响应 `ctx` 取消。

### 检索

MemHop 使用**三通道融合召回**（BM25 + 向量 + 实体）配合 RRF：

| 通道 | 方法 |
|------|------|
| BM25 | 通过倒排索引进行关键词匹配（gse CJK 分词） |
| 向量 | 通过 Ollama HTTP `/api/embed` 进行 f16 半精度语义相似度检索 |
| 实体 | 知识图谱实体模糊名称匹配 |

融合后处理：活跃/最近会话的加性场景加分 → L1 关联扩展 → L5 结晶匹配 → L0 画像组装。

## 基准测试

基于 [LOCOMO10](https://github.com/snap-research/LOCOMO)（ACL 2024）数据集 — 419 轮对话存储，199 条 QA 查询覆盖 5 个类别（Single/Multi/Open/Temporal/Abs 全部 100%）：

| 指标 | 结果 |
|------|------|
| Recall@1 | **100.0%**（199/199） |
| Recall@3 | **100.0%**（199/199） |
| Recall@5 | **100.0%**（199/199） |
| P50 / P95 延迟 | 1.76s / 3.97s ¹ |
| 引擎侧检索延迟 | P50 ≈ 15ms（离线 MockEncoder 基准） |

¹ 端到端延迟由嵌入编码主导（Apple M2，Ollama bge-m3 纯 CPU 运行）；引擎本身 BM25+向量+实体三通道检索仅占 毫秒级。

本地复现（需 Ollama + `test/` 下的 LOCOMO10 数据集）：

```bash
go test -tags integration ./test/ -run TestLocomo10Recall -v
```

### 竞品对比（2026 记忆系统）

| 系统 | GitHub Stars | LOCOMO | LongMemEval | Recall@5 | P95 延迟 | 部署 | 语言 |
|------|-------------|--------|-------------|----------|----------|------|------|
| **MemHop** | — | — | — | **100%** ² | 3.97s ¹ | 嵌入式 .meh | **Go** |
| ZeroMemory | ~200 | 96.1% | — | — | — | Embedded | — |
| MemoryLake | ~500 | 94.03% | — | — | — | SaaS/OSS | Python |
| Zep/Graphiti | ~5k | 94.7%\* | 90.2% | — | 0.63s | Go core | Go/Python |
| Mem0 2026 | ~51k | 92.5% | 93.4% | — | 1.44s | SaaS/OSS | Python |
| Hindsight | ~800 | 92.0% | 94.6% | — | — | OSS/MCP | Python |
| EverMemOS | ~300 | 92.32% | — | — | — | OSS | Python |
| ByteRover | ~100 | 92.2% | 92.8% | — | 1.6s | SaaS | — |
| Dakera | ~500 | 87.8% | — | — | — | Self-host | Rust+Go SDK |
| MemMachine | ~1.5k | 84.87% | — | — | — | OSS | Python |
| Cognee | ~28k | 80.3% | — | — | — | OSS | Python |
| Letta | ~13k | — | — | — | — | OSS | Python |
| agentmemory | ~20k | — | — | 95.2% | — | Embedded TS | TypeScript |
| MemPalace | ~41k\* | — | — | 96.6% | — | Local | JS/TS |
| engram | ~150 | — | — | — | — | Embedded Go | Go |
| OMEGA | ~300 | — | — | — | <50ms | Local MCP | Python |
| LangMem | ~500 | 58.1% | — | — | — | Embedded | Python |

² LOCOMO10 子集检索召回（retrieval-only），与端到端 QA Accuracy（LOCOMO 列）不可直接比较 · \* Zep LOCOMO 为自报数据；MemPalace stars 存在机器人刷量争议

## 项目结构

```
api/                              ← 公开 API（Open, Search, BatchStore, Dream, L0-L5）
internal/
├── common/
│   ├── config/                   ← 配置
│   ├── hash/                     ← xxhash
│   ├── mherrors/                 ← 错误类型
│   ├── numeric/                  ← f16, cosine
│   ├── strutil/                  ← 字符串工具
│   └── timeutil/                 ← 时间工具
├── core/
│   ├── index/                    ← L1 倒排, L2 元数据, L3, 稀疏, 实体, 分词器, 向量哈希
│   ├── model/                    ← profile, hypergraph, scene_node, archive, enums
│   ├── record/                   ← L0, L4, L5, graph, topic
│   └── storage/                  ← V2 .meh 引擎（header, mmap, compact, snapshot）
└── query/
    ├── crud/                     ← L0-L5 CRUD
    ├── dream/                    ← Dream 管线（compress, emotion, l0_distill, l0_form, l1_decay, l1_rebuild, llm, pipeline）
    ├── encoder/                  ← Ollama HTTP 嵌入客户端
    ├── graph/                    ← L3 图（bfs, community, dsl, mutate, store, subgraph）
    ├── health/                   ← 编码器健康检查
    ├── importx/                  ← 文档导入
    ├── search/                   ← RRF 搜索（orchestrator, pipeline, rrf, search）
    ├── session/                  ← 会话管理
    └── write/                    ← 批量存储 + 更新
```

## 开发

```bash
go build ./api/... ./internal/...          # 构建
go test ./api/... ./internal/...           # 单元测试
go test ./test/...                         # 集成测试（需要 Ollama）
go vet ./...                               # 静态分析
```

## 版本历史

| 版本 | 日期 | 亮点 | 核心改动 |
|------|------|------|---------|
| v0.54–v0.58 | 2026-07-16 ~ 07-23 | Go 重写 | v0.58: 统一 RRF — 加性场景加分、三通道融合、移除 L6、atomic.Pointer · v0.57: Dream 收窄至 L0+L1+L2、LLM 加固、L5 Write API、SkipDistill · v0.55: 稳定性 — 移除 IVF、panic→error、崩溃恢复、L5 写入管线 · v0.54: Go 基础 — 四层架构、V2 .meh 存储、仅 2 个依赖、log/slog |
| v0.18–v0.63 | 2026-05-31 ~ 07-10 | Rust | V2 追加写入 `.meh`，支持快照/检查点 · BM25 + IVF 混合检索 · L3 超图 DSL、社区发现（团扩展 + Louvain）、BFS/缓存 · 完整 Dream 管线：L3 蒸馏 → L2 压缩 → L1 衰减 → L0 重建 → L5 结晶 · FFI（cdylib）、MCP Server、gRPC/Unix Socket 编码器 |
| v0.6–v0.17 | 2026-05-20 ~ 05-25 | Rust 早期 | 纯 Rust 单 crate（移除 Python 绑定） · LMDB → 自定义 `.meh` 存储迁移 · 四层 → 六层认知架构演进 · MCP Server 集成 · HNSW 向量索引（替代暴力搜索） |
| v0.1–v0.5 | 2026-05-19 ~ 05-24 | Python | Hopfield 联想记忆网络 · LMDB 嵌入式存储，`pip install` 一键安装 · O(1) 联想召回 + 置信度评分 · BrainLoop 自循环 Agent 循环 · 验证"活记忆"概念 |

## 链接

| | |
|---|---|
| MeowAgent | [github.com/meowagent/meowagent](https://github.com/meowagent/meowagent) |
| MemHop | [github.com/qyiun666/MemHop](https://github.com/qyiun666/MemHop) |
| MeowDesk | [github.com/qyiun666/MeowDesk](https://github.com/qyiun666/MeowDesk) |
| 官网 | [qyiun666.github.io/meowagent.github.io](https://qyiun666.github.io/meowagent.github.io/) |
| 邮箱 | qyiun666@163.com |

## 许可证

MIT OR Apache-2.0
