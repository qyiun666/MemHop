# MemHop — Agent 专用嵌入式联想记忆数据库 · 系统设计方案

**日期**：2026-05-19
**版本**：v2.0 — Rust 优先，pyo3 绑定；ngram 默认编码器；最终版设计

---

## 📌 TL;DR（执行摘要）

- **定位**："SQLite for associative memory" — 专为 AI Agent 设计的嵌入式联想记忆数据库
- **核心机制**：字符 n-gram 哈希编码 → Modern Hopfield Network 吸引子 → O(1) 单条记忆召回（非 Top-K）
- **编码器**：默认 ngram 哈希（零模型，全语言），可选 BGE-M3（语义增强）
- **技术栈**：Rust + pyo3，LMDB 持久化，单文件零配置
- **当前 MeowAgent 有 27 个独立持久化组件**，其中 11 个可被 MemHop 直接替换，8 个可渐进迁移
- **语言无关**：embedding 层处理多语言，无需中英文分路径
- **关键空白市场**：不存在嵌入式、持久化、单文件、零配置的联想记忆数据库

---

## 🎯 核心结论卡片

| 项目 | 内容 |
|------|------|
| 项目名称 | **MemHop**（Memory + Hopfield） |
| 整体定位 | 嵌入式联想记忆数据库，人脑 O(1) 检索模型 |
| 技术栈 | Rust + pyo3 生产 |
| 存储后端 | LMDB（单文件，mmap，零配置） |
| 核心算法 | Sparse Coding + Modern Hopfield Networks |
| API 接口 | `remember(text, meta)` / `recall(cue)` → 单条完整记忆 |
| 许可 | MIT |

---

## 1. 需求与目标

### 1.1 问题陈述

当前 AI Agent 记忆系统普遍采用 **O(0) 策略**（全量上下文压缩），原因有三：

| 原因 | 说明 |
|------|------|
| 🪙 商业动机 | 压缩 = 更多 token 消耗 = 更多收入 |
| 🔧 工程惯性 | O(1) 精准检索需要专门设计，O(0) 压缩简单粗暴 |
| 🧩 基础设施缺失 | 不存在现成的、嵌入式的、为 Agent 设计的联想记忆数据库 |

### 1.2 设计目标

| 目标 | 指标 |
|------|------|
| 检索复杂度 | **真正 O(1)** — 与记忆总量无关 |
| 召回方式 | 单条完整记忆（非 Top-K 列表） |
| 存储格式 | 单文件，零配置 |
| 内存占用 | < 50MB 运行时（100K 记忆，Rust f16 + mmap 懒加载） |
| 持久化延迟 | < 10ms 写入 |
| 检索延迟 | < 1ms 召回（进程内函数调用，无网络） |
| 嵌入方式 | `import memhop` / `Cargo.toml` 一行依赖，无端口 |
| 默认编码器 | 字符 n-gram 哈希（0MB 模型，全语言，< 0.1ms） |

### 1.3 人脑 O(1) 模型

```
人脑检索模型：
  cue "今天早上吃了什么"
    → 稀疏编码（激活特定神经元集群）
    → Hopfield 吸引子（收敛到最近存储模式）
    → 单条完整回忆（"豆浆油条"）

传统 Agent 检索模型：
  cue "今天早上吃了什么"  
    → embedding 向量化
    → Top-K 余弦相似度排序
    → 返回 K 个候选片段（仍需 LLM 筛选）
    → O(N * log K) 或 O(log N)

MemHop 检索模型：
  cue "今天早上吃了什么"
    → 稀疏编码（局部敏感哈希 → 激活神经元子集）
    → Modern Hopfield 能量函数最小化（一步收敛）
    → 单条完整记忆（"豆浆油条，7:30AM，厨房"）
    → O(1) — 与记忆库大小无关
```

### 1.4 MemHop 与提示词：管什么的，不管什么的

MemHop **不是提示词的替代品**，而是替代提示词里最占 token 的部分——**上下文注入层**。

| 提示词做的事 | MemHop 能代替吗 | 原因 |
|-------------|:---:|------|
| 角色定义（"你是 xx 专家"） | ❌ | 行为指令，不是记忆 |
| 输出格式约束 | ❌ | 规则，不需要检索 |
| 思维链引导 | ❌ | 推理框架 |
| 工具调用规范 | ❌ | 编程接口 |
| RAG 检索结果注入 | ✅ | 事实内容 |
| 对话历史摘要 | ✅ | 情节记忆 |
| 用户偏好/习惯 | ✅ | 语义记忆 |
| 项目上下文/决策记录 | ✅ | 情景记忆 |

**核心区分**：提示词是"怎么做事"的章程（不变），MemHop 是"记得什么"的笔记（海量需检索）。两者互补，不互替。MemHop 的实际价值是把 MeowAgent 当前的 O(0) 全量压缩注入变成 O(1) 精准检索注入——对话 100 轮不再压缩成一坨摘要塞进 prompt，而是精准 recall 3~5 条相关记忆，token 成本从 $$$ 降到几乎为零。

### 1.5 三层记忆模型

MemHop 承载 MeowAgent 的三类记忆，形成 **纠缠图 → 知识树 → 原文索引** 的层次结构：

```
┌──────────────────────────────────────────────────────────────────┐
│                    纠缠图 (Entity Layer)                          │
│  layer="entity"                                                  │
│  ┌──────────┐   relation    ┌──────────┐                        │
│  │ e_001    │────caused_by──▶│ e_002    │                        │
│  │ "支付bug" │               │ "空指针"  │                        │
│  └────┬─────┘               └────┬─────┘                        │
│       │ connections              │ connections                   │
├───────┼──────────────────────────┼──────────────────────────────┤
│       ▼                          ▼                               │
│  知识树 (Knowledge Layer)     layer="knowledge"                  │
│  ┌──────────────────────────────────────────┐                    │
│  │ domain="code"  path="payment.py"         │                    │
│  │ ┌─────────┐    ┌─────────┐               │                    │
│  │ │ k_010   │───▶│ k_011   │───▶ ...       │                    │
│  │ │ 模块结构 │    │ 函数签名 │               │                    │
│  │ └────┬────┘    └────┬────┘               │                    │
│  │      │ parent       │ parent              │                    │
│  └──────┼──────────────┼────────────────────┘                    │
│         ▼              ▼                                         │
├──────────────────────────────────────────────────────────────────┤
│  原文索引 (Episode Layer)     layer="episode"                     │
│  ┌──────────────────┐  ┌──────────────────┐                      │
│  │ turn_042         │  │ turn_043         │                      │
│  │ "这个bug是昨天的  │  │ "改好了,加了个    │                      │
│  │  空指针引起的"    │  │  null check"     │                      │
│  │ session_id="s_7" │  │ session_id="s_7" │                      │
│  └──────────────────┘  └──────────────────┘                      │
└──────────────────────────────────────────────────────────────────┘
```

**层次关系**：
- **纠缠图 entity** 通过 `connections[].to` 指向其他 entity，形成关系网
- **知识树 node** 通过 `parent` 指向父节点，`path` 关联源文件
- **原文 turn** 记录原始对话，`session_id` 关联会话

**三类记忆统一存储，通过 `meta.layer` 字段区分，`search()` 按层过滤。**

### 1.6 衰减与自动清理

MemHop 本身不做复杂的衰减策略（那是 meowagent 应用层的事），但提供基础清理能力防止数据库无限膨胀：

| 能力 | 接口 | 说明 |
|------|------|------|
| 按时间清理 | `purge_before(datetime)` | 删除 `created_at` 早于指定时间的记忆 |
| 保护级别 | `meta.protection` | `"permanent"` 永不删除，`"protected"` 需显式确认，`"normal"` 可自动清理 |
| 休眠标记 | `meta.is_dormant` | 标记为休眠的记忆不参与 `recall()`，但 `search()` 可见 |
| 最大条数 | `memhop.open(max_memories=N)` | 超出上限时触发 FIFO 淘汰（仅删除 `"normal"` 级别） |

**不在 MemHop 范围内的**（由 meowagent 处理）：时间衰减策略、热点检测、语义去重、噪音过滤。

### 1.7 MeowAgent 对接需求

完整需求规格：`deliverables/engineering-assurance/memhop-requirements-from-meowagent-2026-05-19.md`

核心 API 需求（当前实现 vs 需求差异见 ROADMAP.md §Phase 2）：

| 接口 | 需求 | 当前状态 |
|------|------|:--:|
| `remember_batch(items)` | 批量写入 | ❌ |
| `recent(limit)` | 最近 N 条 | ❌ |
| `search(filters, limit)` | 任意 meta 字段过滤 | ⚠️ 仅 tags + text_contains |
| `created_at` 字段 | Memory 含时间戳 | ❌ |
| upsert on same key | 同 key 去重 | ❌ |
| `is_dormant` | 休眠标记 | ❌ |
| `protection` | 保护级别 | ❌ |
| `connections_to` 查询 | 关联引用查询 | ❌ |
| close 后错误 | MemHopClosedError | ❌ |

---

## 2. 高层设计

### 2.1 三层系统架构

```
┌─────────────────────────────────────────────────────────┐
│                     MemHop API                           │
│   remember(text, meta) → id   |   recall(cue) → Memory  │
│   forget(id)                  |   search(tags) → [Mem]  │
├─────────────────────────────────────────────────────────┤
│  第一层：编码层 (Pluggable Encoder)                       │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐     │
│  │ ngram (默认)  │ │ BGE-M3 本地  │ │ 自定义编码器  │     │
│  │ 零模型 <0.1ms │ │ 300MB INT8   │ │ 任意兼容实现  │     │
│  └──────┬───────┘ └──────┬───────┘ └──────┬───────┘     │
│         └────────────────┼────────────────┘             │
│                          ▼                               │
│  接口: encode(text) → (dense[1024d], sparse_vec, multi) │
├─────────────────────────────────────────────────────────┤
│  第二层：存储层 (MHN + LMDB)                              │
│  ┌─────────────────────┐  ┌────────────────────────┐    │
│  │ Modern Hopfield Net │  │ LMDB 持久化              │    │
│  │ E(x)=-lse(β,Xᵀx)+½xᵀx│  │ Patterns│Blobs│Meta     │    │
│  │ x_new = softmax(·)·X │  │ 单文件: memhop.db       │    │
│  └─────────────────────┘  └────────────────────────┘    │
├─────────────────────────────────────────────────────────┤
│  第三层：检索层 (两阶段 O(1))                              │
│  ┌──────────────────┐     ┌────────────────────────┐    │
│  │ 阶段一: 稀疏粗筛   │ ──► │ 阶段二: MHN 精排        │    │
│  │ LSH bucket/Sparse │     │ Hopfield 一步收敛       │    │
│  │ 1M → 500 候选     │     │ → 单条完整记忆+置信度    │    │
│  └──────────────────┘     └────────────────────────┘    │
└─────────────────────────────────────────────────────────┘
```

### 2.2 系统架构（原 v1.0 架构图，保留参考）

```
┌─────────────────────────────────────────────────────┐
│                    MemHop API                        │
│  remember(text, meta) → id                           │
│  recall(cue) → Memory | None                         │
│  forget(id) / update(id, text, meta)                 │
├─────────────────────────────────────────────────────┤
│                  MemHop Engine                       │
│  ┌───────────────┐  ┌──────────────────────────┐    │
│  │ Sparse Encoder│  │ Modern Hopfield Network   │    │
│  │ (LSH + Random │  │ ┌──────────────────────┐ │    │
│  │  Projection)  │  │ │  Energy Landscape     │ │    │
│  │               │  │ │  E(x) = -lse(β, X^T x)│ │    │
│  │  text → bits  │  │ │  + ½x^T x + const     │ │    │
│  └───────┬───────┘  │ │                        │ │    │
│          │          │ │  Update: X_new = softmax│ │    │
│          ▼          │ │  (β X^T x) X           │ │    │
│  ┌───────────────┐  │ └──────────────────────┘ │    │
│  │ Memory Index  │  │   Pattern Storage: X      │    │
│  │ (Bloom Filter │  │   (LMDB-backed matrix)    │    │
│  │  + Bits → ID) │  └──────────────────────────┘    │
│  └───────────────┘                                    │
├─────────────────────────────────────────────────────┤
│                    LMDB Storage                      │
│  ┌──────────┐ ┌──────────┐ ┌──────────────────┐    │
│  │ Patterns │ │  Blobs   │ │  Meta Index       │    │
│  │ (f16    │ │ (zstd   │ │ (timestamp, confidence,) │    │
│  │  matrix) │ │  text)   │ │   timestamp, tags)│    │
│  └──────────┘ └──────────┘ └──────────────────┘    │
│                   single file: memhop.db             │
└─────────────────────────────────────────────────────┘
```

### 2.3 核心算法：Modern Hopfield Network (MHN)

与经典 Hopfield (O(N²) 存储容量) 不同，Modern Hopfield 使用连续状态 + softmax 能量函数：

```
能量函数：E(x) = -lse(β, X^T x) + ½x^T x + C

其中：
  X ∈ R^(d×N)  — 存储的 N 条记忆模式矩阵
  x ∈ R^d      — 查询向量
  β            — 温度参数（控制吸引子锐度）
  lse(β, z)    — log-sum-exp: β⁻¹ log(Σ exp(β·zᵢ))

一步更新规则：
  x_new = softmax(β X^T x) · X

关键性质：
  ✅ 存储容量指数级增长（N ∝ exp(d)）
  ✅ 一步收敛到最近吸引子
  ✅ 每个吸引子 = 一条完整记忆（非 Top-K 混合）
  ✅ 数学保证 O(1) 收敛
```

### 2.4 编码层：可插拔设计（Pluggable Encoder）

编码器与存储引擎解耦。用户根据场景选择，MemHop 只要求实现统一接口：

```rust
// Rust Encoder trait（见 REQUIREMENTS.md §6.1 完整定义）
pub trait Encoder: Send + Sync {
    fn encode(&self, text: &str) -> Result<EncoderOutput, MemHopError>;
}

pub struct EncoderOutput {
    pub dense: Vec<f16>,              // [1024] float16, L2 归一化
    pub sparse: HashMap<String, f32>, // ngram 字面权重 / BGE-M3 lexical weights
}
```

| 模式 | 编码器 | 本地内存 | 延迟 | 适用场景 |
|------|--------|---------|------|---------|
| **ngram（默认）** | 字符 n-gram 哈希 | **0** | < 0.1ms | 零模型，全语言，开箱即用 |
| **bge（可选）** | BGE-M3 ONNX INT8 | **~300MB** | < 5ms | 需要强语义召回时 |
| **自定义** | 任意 Encoder trait 实现 | 不定 | 不定 | 特殊领域需求 |

**默认选择 ngram 哈希**：零模型 = 真正的零配置。MemHop 定位 "SQLite for associative memory"——SQLite 不需要下载模型才能用，MemHop 同理。字符 n-gram 的全语言特性确保外国人跟中国人一样开箱即用。需要更强的语义理解时 `pip install memhop[semantic]` 切换 BGE-M3。

> **关于 BGE-M3（可选增强）**：智源研究院 (BAAI) 开源。目前**唯一**同时支持 dense + sparse + multi-vector 三种检索模式的模型。中文 CMTEB 第 2，ONNX INT8 量化 ~300MB。作为语义增强的可选方案，不需要时完全不用装。

### 2.5 中文短词场景：五层防护方案

MHN 在中文短词上的挑战不在算法本身，而在嵌入向量的输入质量。短词（如 "早餐"）在 1024 维空间中信号极弱，容易收敛到错误吸引子。以下是五层渐进式防护：

| 层 | 方案 | 原理 | 成熟度 |
|---|------|------|:---:|
| 1 | **ngram 字面重叠（默认）** | n=2/3/4 字符片段哈希，短词天然高重叠（"早餐"→"早"/"餐"/"早餐" n-gram 自覆盖） | 🟢 生产可用 |
| 2 | **BGE-M3 混合编码（可选）** | 安装 bge 模式后，dense + sparse + multi-vector 三输出增强语义 | 🟢 生产可用 |
| 3 | **多向量 (ColBERT-style)（可选）** | 每 token 独立向量，短词不丢失逐字信号（需 BGE-M3 multi-vector） | 🟢 生产可用 |
| 4 | **Query Expansion（可选）** | 短词扩展为短语再编码（需 BGE-M3 语义理解） | 🟡 需领域调优 |
| 5 | **两阶段检索** | Sparse 粗筛（ngram 字面匹配 → 500 候选）+ MHN 精排 | 🟢 生产可用 |

**默认启用第 1、5 层**（零模型依赖）。第 2-4 层需安装 `pip install memhop[semantic]` 获得 BGE-M3 支持。

**两阶段检索流程**：
```
recall("早餐")
  → 阶段一 (Sparse): ngram 字面权重匹配 → 1M 记忆 → 500 候选
  → 阶段二 (MHN):   dense 向量 Hopfield 收敛 → 1 条完整记忆
  → confidence < 0.7? → None (安全兜底，防止错误召回)
```

### 2.6 存储方案：LMDB

| 特性 | LMDB | SQLite | RocksDB | FAISS |
|------|------|--------|---------|-------|
| 单文件 | ✅ | ✅ | ❌ (多文件) | ✅ |
| 零配置 | ✅ | ✅ | ❌ | ✅ |
| mmap 读取 | ✅ | ❌ | ❌ | ❌ |
| 写事务 ACID | ✅ | ✅ | ❌ | ❌ |
| 嵌入体积 | ~100KB | ~1MB | ~5MB | ~50MB |
| Python 绑定 | ✅ (lmdb) | ✅ (内置) | ✅ | ✅ |
| Rust 绑定 | ✅ (heed) | ✅ (rusqlite) | ✅ | ✅ |

**选择 LMDB 的理由**：
- mmap 零拷贝读取，Hopfield 模式矩阵直接映射到内存
- 单文件 + ACID，比 FAISS 更可靠
- 极简 API：open / get / put / cursor
- 写入快于 SQLite（无 SQL 解析开销）

### 2.7 API 设计

```python
# Python API
import memhop

db = memhop.open("memhop.db")  # 单文件，自动创建

# 写入记忆
mid = db.remember(
    text="今天早上吃了豆浆油条",
    meta={"time": "2026-04-25T07:30:00", "tags": ["早餐", "食物"]}
)

# 精确召回（O(1)）
memory = db.recall("今天早上吃了什么")
# → Memory(
#     id="m_001",
#     text="今天早上吃了豆浆油条",
#     meta={"time": "2026-04-25T07:30:00", "tags": ["早餐", "食物"]},
#     confidence=0.94  # 吸引子收敛置信度
#   )

# 无匹配时返回 None
result = db.recall("火星上有什么")
# → None  (confidence < threshold)

# 更新 / 删除
db.update(mid, text="...", meta={...})
db.forget(mid)

# 批量操作
db.remember_batch([...])
db.search(tags=["早餐"])  # 元数据精确检索（非联想）
```

```rust
// Rust API
use memhop::MemHop;

let db = MemHop::open("memhop.db")?;
let mid = db.remember("今天早上吃了豆浆油条", json!({"time": "2026-04-25T07:30:00"}))?;
let memory = db.recall("今天早上吃了什么")?;
```

---

## 3. 关键决策记录 (ADR)

### ADR-001: Modern Hopfield 而非经典 Hopfield

| | 经典 Hopfield | Modern Hopfield |
|---|---|---|
| 存储容量 | N ≤ 0.14d | N ∝ exp(d) |
| 收敛步骤 | 多步迭代 | 一步 |
| 状态空间 | 二元 (-1/+1) | 连续 (R^d) |
| 能量函数 | 二次型 | softmax-based |
| 适用维度 | d ≤ 100 | d 可达 1024+ |

**决策**：Modern Hopfield，一步收敛 + 指数级容量。

### ADR-002: LMDB 而非 SQLite

| 考量 | LMDB | SQLite |
|------|------|--------|
| 浮点矩阵存储 | 原生 binary blob | BLOB 列（间接） |
| mmap 零拷贝 | ✅ | ❌ |
| 并发读 | 无锁 | 读锁 |
| Rust 生态 | heed (成熟) | rusqlite (成熟) |

**决策**：LMDB，mmap 对 Hopfield 模式矩阵的零拷贝读取是关键优势。

### ADR-003: 稀疏编码 + LSH 前置，而非全量向量检索

| | 全量向量检索 (Chroma/FAISS) | 稀疏编码 + LSH |
|---|---|---|
| 检索复杂度 | O(log N) ANN | O(1) bucket + O(1) Hopfield |
| 召回形式 | Top-K 列表 | 单条吸引子 |
| 区分度 | 余弦距离连续 | 二进制哈希离散 |
| 新记忆增量 | 需重建索引 | 直接插入 |

**决策**：稀疏编码 + LSH，与 O(1) 目标一致。

### ADR-004: Rust 优先，pyo3 绑定

| 阶段 | 语言 | 目标 |
|------|------|------|
| Phase 1 | Rust (heed + pyo3) | 核心引擎 + Python 绑定，直接生产可用 |
| Phase 2 | Rust feature flags | BGE-M3 语义增强（`semantic` feature） |
| Phase 3 | Rust + C FFI | 语言无关（Go/JS/Flutter 均可调用） |

**决策**：Rust 直接作为主要开发语言。理由：
1. **避免维护两套实现** — Python 原型和 Rust 生产版需要双倍维护，API 差异导致迁移成本高
2. **pyo3 成熟度足够** — v0.23+ 的 pyo3 对 Rust struct/enum 的 Python 暴露已非常流畅
3. **LMDB 的 Rust 生态优于 Python** — heed crate 原生 mmap 零拷贝，Python lmdb 封装有 GIL 开销
4. **ngram 编码器零依赖** — 不涉及复杂数值计算（不像 BGE-M3 需要 Python 的 ONNX 推理栈），Rust 原生实现更干净

### ADR-005: 编码器可插拔，默认字符 n-gram 哈希（零模型）

| 方案 | 本地内存 | 嵌入质量 | 延迟 | 离线 | 语言 |
|------|---------|---------|------|:--:|------|
| **ngram 哈希（默认）** | **0** | ⭐⭐⭐ | < 0.1ms | ✅ | **全语言** |
| BGE-M3 本地 (INT8) | ~300MB | ⭐⭐⭐⭐⭐ | < 5ms | ✅ | 中英为主 |

**决策**：默认字符 n-gram 哈希。理由：
1. **零模型** — 不需要下载 300MB 的 BGE-M3，不需要 API Key。`pip install memhop` 即用
2. **全语言** — 只在字符层面操作，中文、英文、日文、阿拉伯文……任何语言同等对待
3. **外国人开箱即用** — 不存在"下载一个中文 embedding 模型才能用"的门槛
4. **Hopfield 不需要强语义** — MHN 做的是吸引子收敛，相似输入 → 相近向量区域即可。字符 n-gram 的字面重叠性天然满足
5. **两阶段检索保底** — ngram sparse 权重做粗筛，字面匹配兜住短词场景

BGE-M3 降级为可选增强（`pip install memhop[semantic]`），需要更强语义召回时自行安装。

### ADR-006: 两阶段检索以应对中文短词

| | 纯 MHN | 两阶段 (Sparse + MHN) |
|---|---|---|
| 短词准确率 | ~60%（信号弱，易错配） | > 90%（sparse 字面保底） |
| 检索复杂度 | O(1) | O(1) bucket + O(1) MHN |
| 额外依赖 | 无 | BGE-M3 sparse 输出 |

**决策**：两阶段检索。MHN 数学本身在短词场景没有问题——问题在输入向量区分度不够。用 sparse 粗筛解决输入质量问题，MHN 在精排阶段发挥 O(1) 优势。这是最务实的解法：不改 MHN 算法，只在编码层做文章。

---

## 4. MeowAgent 存储组件可替换性矩阵

### 4.1 可直接替换（11 个）

| # | 组件 | 当前格式 | 替换方式 | 收益 |
|---|------|---------|---------|------|
| 1 | SqliteGraphStore | SQLite FTS5 | MemHop 作为纠缠图后端 | O(1) 检索替代 O(N) FTS5 |
| 2 | AdaptiveVectorStore | ChromaDB | MemHop 内置联想检索 | 消除 ChromaDB 依赖 |
| 3 | FAISS Index | 二进制文件 | MemHop 替代 | 消除 FAISS 依赖 |
| 4 | Vector Meta (JSON) | JSON | MemHop meta 字段 | 统一元数据 |
| 5 | FAISS ID Map | JSON | MemHop 内部索引 | 消除 |
| 6 | FAISS Deleted | JSON | MemHop forget() | 消除 |
| 7 | JsonSharedStore | JSON | MemHop namespace: shared | 统一后端 |
| 8 | FocusState | JSON (meowcat) | MemHop namespace: focus | 统一后端 |
| 9 | RoleEmergence | JSON | MemHop namespace: emergence | 统一后端 |
| 10 | KnowledgeTreeStore | 多个 SQLite | MemHop 实体记忆 | 合并 N 个 SQLite 为一个 |
| 11 | ChromaStore | ChromaDB | MemHop 替代 | 消除外部依赖 |

### 4.2 可渐进迁移（8 个）

| # | 组件 | 当前格式 | 迁移策略 | 难度 |
|---|------|---------|---------|------|
| 12 | JsonlL6Store | JSONL | 每条对话 → db.remember() | 中 |
| 13 | SessionStore | JSONL | 每条消息 → db.remember() | 中 |
| 14 | MetricsStore | SQLite | 时序数据不适合联想检索 | 低（保留 SQLite） |
| 15 | Cortex Worldview | 4 个 YAML | namespace: cortex/{layer} | 低 |
| 16 | CatShelf meta | JSON | namespace: cats | 低 |
| 17 | CatConfig | YAML | namespace: config | 低 |
| 18 | HookManager | YAML | namespace: hooks | 低 |
| 19 | UserConfig | JSON | 不适合（启动时需快速读取） | 保留 JSON |

### 4.3 不适合替换（8 个）

| # | 组件 | 原因 |
|---|------|------|
| 20 | Skills (Crystallization) | 文件系统 artifact，非数据库语义 |
| 21 | CatProfile YAML | 用户手写配置，非数据库语义 |
| 22 | Logs | 追加日志流，非记忆检索 |
| 23 | Adapters | Python 模块文件 |
| 24 | InternalSettings | 启动时一次性读取 |
| 25 | 知识树 Changelog | 版本历史，非联想检索 |
| 26 | 知识树 CodeRelations | 结构化图数据，用专用图数据库更好 |
| 27 | Colony legacy cats.json | 已废弃 |

### 4.4 替换后的 MeowAgent 存储架构

```
替换前：27 个组件，5 种格式，3 个 SQLite 数据库
替换后：3 个核心存储

┌─────────────────────┐
│    memhop.db        │  ← 联想记忆（替代 #1-#12, #14-#19）
│  (MemHop / LMDB)    │
├─────────────────────┤
│    metrics.sqlite   │  ← 时序指标（保留）
│  (SQLite)           │
├─────────────────────┤
│    filesystem       │  ← 技能/配置/日志（保留）
│  ~/.meowagent/      │
└─────────────────────┘

从 27 → 3，格式从 5 → 3，依赖从 3 → 2
```

---

## 5. 可运维性

### 5.1 部署模型：嵌入式，非服务

MemHop 是**嵌入式库**，不是独立服务。无端口、无 HTTP、无 gRPC。

```
MeowAgent 使用 MemHop：
  pip install memhop  # 默认 ngram，零模型依赖，< 5MB

  from memhop import MemHop
  db = MemHop.open("memhop.db")  # 直接打开文件，不走网络
  # 开箱即用，MeowAgent 已有服务端口，MemHop 只嵌入

  无需：
    ❌ ChromaDB server
    ❌ FAISS build 工具链
    ❌ SQLite FTS5 扩展
    ❌ 独立端口、独立进程
    ❌ 大模型下载
```

**嵌入式 vs 独立服务对比**：

| | 嵌入式（MemHop） | 独立服务（如 Redis） |
|---|---|---|
| 调用方式 | `db.recall("hello")` 进程内 | `GET /recall?cue=hello` 网络 |
| 延迟 | < 1ms（函数调用） | 1-10ms（网络 + 序列化） |
| 数据访问 | mmap 直接映射，零拷贝 | JSON/protobuf 序列化 |
| 崩溃面 | 同宿主进程生灭 | 网络分区 + 独立进程 |

### 5.2 冷启动 vs 热启动

| 阶段 | Rust 生产版 |
|------|-----------|
| LMDB 打开 | < 1ms（mmap） |
| 模式矩阵 | mmap 懒加载（OS 缺页），f16 精度 |
| 索引加载 | **mmap 直接映射（< 1ms）** — 索引导出到 LMDB 第 4 子库 `i`，启动时零重建 |

每次 `open()` 直接从 LMDB 子库 `i` mmap 加载索引快照，无需扫描重建。模式矩阵按需缺页加载，冷启动 < 2ms。

### 5.3 存储架构（Rust 生产版）：全部 mmap，按需缺页

```
memhop.db（单文件）
┌──────────────────────────────────────────┐
│ Header: 魔数、版本、统计                  │
├──────────────────────────────────────────┤
│ Pattern Region: [f16; 1024] × N          │
│   磁盘占用 = N × 2KB                      │
│   实际 RAM = 仅实际访问的页（缺页加载）     │
├──────────────────────────────────────────┤
│ Blob Region: 原文 + meta，zstd 压缩        │
│   磁盘占用 = ~500MB / 100万条              │
│   实际 RAM = 仅解压时暂存                  │
├──────────────────────────────────────────┤
│ Index Region: 扁平哈希表                  │
│   磁盘占用 = ~30MB / 100万条               │
│   启动时 mmap，零重建，零 RAM 浪费         │
└──────────────────────────────────────────┘
```

**核心设计**：模式矩阵、原文、知识树都在磁盘上 mmap 映射。OS 按 4KB 缺页加载，只有被 `recall()` 实际访问的记忆才会进入 RAM。100K 记忆的稳定态内存 ~100MB（索引 + 热页），远小于全量加载的 ~1GB。

### 5.4 内存占用预估

| 规模 | Rust 生产版 (f16 + mmap 懒加载) |
|------|------------------------------|
| 1K | ~10MB |
| 10K | ~25MB |
| 100K | **~100MB** |
| 100 万 | **~800MB** |

### 5.5 性能预估

| 指标 | 1K | 10K | 100K | 100 万 |
|------|----|-----|------|--------|
| recall() 延迟 | < 0.5ms | < 1ms | < 2ms | < 5ms |
| remember() 延迟 | < 1ms | < 3ms | < 5ms | < 10ms |
| 启动时间（Rust） | < 1ms | < 1ms | < 2ms | < 5ms |
| 磁盘占用 | ~1MB | ~10MB | ~100MB | ~1GB |

### 5.6 故障模式

| 故障 | 影响 | 缓解 |
|------|------|------|
| memhop.db 损坏 | 全部记忆丢失 | LMDB ACID 保证（几乎不可能损坏） |
| OOM（记忆过多） | 进程崩溃 | 自动触发衰减引擎（可配置上限） |
| 编码冲突（哈希碰撞） | 错误召回 | 置信度阈值 + 二次验证 |
| Hopfield 收敛到错误吸引子 | 错误记忆 | β 温度参数调优 + confidence 阈值 |

---

## 6. 测试策略

### 6.1 核心测试用例

| 测试场景 | 预期结果 |
|----------|---------|
| 基本写入召回 | remember → recall 返回相同记忆 |
| 语义相似召回 | "吃了什么" → recall → "豆浆油条" |
| 无匹配 | recall("无关话题") → None |
| 多记忆区分 | 100 条相似记忆 → 每条精确区分 |
| 大规模压力 | 100 万条记忆 → recall < 5ms |
| 并发写入 | 多线程 remember → 无数据损坏 |
| 崩溃恢复 | kill -9 → 重启后数据完整 |
| 中文短词 | "早餐" → recall → 正确记忆 |
| 跨语言 | remember(en) → recall(zh) → 匹配 |
| 遗忘 | forget → recall → None |

### 6.2 对比基准

| 基准 | MeowAgent 当前 (FTS5) | MemHop 目标 |
|------|----------------------|-------------|
| 1K 记忆检索 | ~2ms | < 1ms |
| 10K 记忆检索 | ~15ms | < 2ms |
| 100K 记忆检索 | ~150ms | < 5ms |
| 中文短词准确率 | ~60% | > 90% |
| 单次召回 | Top-K（需 LLM 筛选） | 单条（无需 LLM） |

---

## 7. 文档结构

```
memhop/
├── README.md              # 项目介绍 + 快速开始
├── DESIGN.md              # 本文档（设计原理）
├── API.md                 # API 参考（remember/recall/forget/update/search）
├── ALGORITHM.md           # MHN 数学推导 + 两阶段检索细节
├── ENCODER.md             # 编码器接口 + BGE-M3 部署指南
├── examples/
│   ├── basic_usage.py     # 基础示例
│   ├── agent_integration.py  # Agent 集成示例
│   ├── chinese_short.py   # 中文短词专项测试
│   └── benchmark.py       # 性能测试
├── src/
│   ├── lib.rs             # Rust 核心
│   ├── hopfield.rs        # Modern Hopfield 实现
│   ├── encoder.rs         # 编码器 trait + API/BGE-M3 实现
│   ├── storage.rs         # LMDB 封装
│   └── py_bindings.rs     # pyo3 绑定
├── python/
│   ├── memhop/__init__.py # Python API
│   ├── encoder.py         # 可插拔编码器
│   └── tests/
├── Cargo.toml
└── pyproject.toml
```

---

## 8. 风险与权衡

| 风险 | 等级 | 缓解 |
|------|------|------|
| MHN 在中文短词场景效果待验证 | 🟡 中 | 五层防护（见 §2.5），两阶段检索 + BGE-M3 sparse 保底 |
| LSH 桶冲突率 | 🟡 中 | 多层编码 + residual connection |
| BGE-M3 本地内存 300MB | 🟢 低 | BGE-M3 是可选的 feature flag（`semantic`），默认零模型的 ngram 就够用。300MB 不到 7B LLM 的 1/50 |
| LMDB mmap 在 32 位系统受限 | 🟢 低 | 目标用户均为 64 位 |
| Rust 开发周期长 | 🟡 中 | pyo3 成熟度高，heed + nalgebra 生态稳定。分阶段交付 v0.1.* 逐步迭代 |
| 与现有 ChromaDB/FAISS 的兼容过渡 | 🟡 中 | 提供双写迁移期 adapter |
| Hopfield 收敛到错误吸引子 | 🟡 中 | β 温度参数调优 + confidence 阈值兜底 |
| 市场认知（"又一个新的数据库?"） | 🟢 低 | 定位清晰："SQLite for associative memory" |

---

## 9. 实现路线图

### Phase 1: Rust 核心引擎（配合 pyo3 Python 绑定）

```
□ Rust 项目骨架：Cargo.toml (heed, zstd, serde, bincode, rand, pyo3)
□ NgramEncoder 实现（字符 2/3/4-gram 哈希，零模型依赖）
□ ModernHopfield 实现（nalgebra f16 矩阵，一步 softmax 收敛）
□ LmdbStorage 实现（heed 封装，4 子库 p/b/m/i）
□ SearchIndex 实现（内存倒排索引，索引导出 mmap 快照）
□ MemHopEngine 完整 API：remember / recall / forget / update / search / recent / remember_batch / purge_before / close
□ pyo3 绑定：暴露 memhop.open() 及全部 Python API
□ maturin 构建配置 + pyproject.toml，pip install 可用
□ 基本测试套件（22 验收用例，见 REQUIREMENTS.md §13）
□ 性能基准（vs MeowAgent FTS5）
```

### Phase 2: 语义增强（可选 feature flag）

```
□ BgeEncoder 实现（ort crate + BGE-M3 ONNX INT8, feature = "semantic"）
□ 两阶段检索激活（总记忆 > 500 时 ngram sparse 粗筛 + dense MHN 精排）
□ 中文短词专项验证
```

### Phase 3: MeowAgent 集成

```
□ MemHopGraphStore（替代 SqliteGraphStore）
□ MemHopVectorStore（替代 ChromaDB + FAISS）
□ MemHopSharedStore（替代 JsonSharedStore）
□ Thalamus 路由改造（MemHop.recall 替代 FTS5 搜索）
□ 渐进迁移脚本（从旧格式到 memhop.db）
```

### Phase 4: 生态扩展

```
□ Go binding (cgo)
□ JavaScript/TypeScript binding (napi-rs)
□ Flutter/Dart FFI
□ 可视化工具（Hopfield 能量景观 3D 图）
```

---

## ✅ 行动清单

| # | 行动 | 负责 | 紧急度 | 预期完成 |
|---|------|------|--------|---------|
| 1 | 确认项目命名（MemHop） | Zhen | P0 | 已完成 |
| 2 | 启动 Phase 1 Rust 核心引擎开发 | AI 团队 | P0 | 立即 |
| 3 | 中文短词场景验证（核心风险点） | AI 团队 | P0 | Phase 1 第 1 周 |
| 4 | 设计 MeowAgent 迁移 adapter 接口 | Zhen | P1 | Phase 3 |
| 5 | 准备开源发布材料（README/DESIGN/benchmark） | Zhen | P2 | Phase 2 后 |
| 6 | 评估与 mhn-ai-agent-memory 的差异化竞争定位 | Zhen | P2 | Phase 1 期间 |

---

## ⚠️ 待完善 / 已知局限

- **中文短词场景**（最大风险点）：MHN 数学保证 O(1) 收敛，但短词嵌入区分度低。通过五层防护（§2.5）缓解，Phase 1 第一周优先验证
- LSH 桶冲突率与 Hopfield 温度参数 β 需实验调优
- 100 万+ 规模下的 LMDB mmap 性能需实测
- HEN (Hopfield Encoding Networks) 论文方案评估和潜在集成（Phase 2 后）
- 多语言跨语言召回的 embedding 模型选型（BGE-M3 已覆盖 100+ 语言，需实测跨语言精度）
- 与 MeowAgent 现有海马体噪音过滤 / Jaccard 去重的整合方式
- BGE-M3 本地模式对 ARM64 (Apple Silicon) 的 ONNX 推理优化

---

## 📚 数据来源 & 成员产出索引

- MeowAgent 全部 27 个持久化组件审查：完整清单见第 4 节
- 存储后端竞争分析：LMDB vs SQLite vs RocksDB vs FAISS
- 算法选型：Modern Hopfield vs 经典 Hopfield vs Dense Associative Memory
- 命名方案评估：MemHop（推荐）/ Mneme / Associa / SynapseDB

---

> 本报告由工程保障团队 AI 协作生成，关键决策请由人类工程负责人复核。
