# MemHop 需求规格说明书

> **唯一开发依据**：AI 团队读完本文档即可开始开发。设计原理和决策理由在 `DESIGN.md`。
> **版本**：v2.0 — Rust 优先，纯规格
> **日期**：2026-05-19

---

## 1. 定位

MemHop 是嵌入式联想记忆数据库。`import memhop` 或 `memhop = "0.1"` 即可使用，零配置，单文件。

- 写入：`remember(text, meta)` — 文本 + 元数据 → 编码为稠密向量 → 存储
- 召回：`recall(cue)` — 查询文本 → 编码 → Hopfield 网络 O(1) 收敛 → 返回单条最匹配记忆
- 搜索：`search(filters)` — 元数据字段过滤

**适用场景**：AI Agent 的记忆系统（MeowAgent 的三层记忆：纠缠图 entity / 知识树 node / 原文 turn）。

---

## 2. 技术规格

| 项目 | 规格 |
|------|------|
| **实现语言** | Rust（stable，edition 2024） |
| **Python 绑定** | pyo3 ≥ 0.23，暴露到 `memhop` 包 |
| **存储引擎** | LMDB（heed crate），单文件 `memhop.db` |
| **编码器（默认）** | 字符 n-gram 哈希（2/3/4-gram，零模型依赖） |
| **编码器（可选）** | BGE-M3 ONNX INT8（feature flag `semantic`） |
| **检索算法** | Modern Hopfield Network，一步 softmax 收敛 |
| **模式矩阵精度** | float16（内存比 float32 减半） |
| **并发** | 单写多读（LMDB MVCC） |
| **Python 版本** | ≥ 3.10 |
| **安装体积** | < 3MB（默认 ngram，不含 BGE-M3） |

---

## 3. Rust 项目结构

```
memhop/
├── REQUIREMENTS.md      # 本文档
├── DESIGN.md            # 设计原理
├── ROADMAP.md           # 开发任务
├── README.md
├── Cargo.toml           # Rust 项目
├── pyproject.toml       # Python 包（maturin build）
├── src/
│   ├── lib.rs           # pyo3 入口，暴露 `memhop.open()` 等
│   ├── types.rs         # Memory, EncoderOutput, EncoderMode, Protection, 错误类型
│   ├── engine.rs        # MemHopEngine（完整 API 实现）
│   ├── encoder/
│   │   ├── mod.rs       # Encoder trait
│   │   ├── ngram.rs     # NgramEncoder（默认）
│   │   ├── bge.rs       # BgeEncoder（feature = "semantic"）
│   │   └── api.rs       # ApiEncoder（feature = "api"）
│   ├── hopfield.rs      # ModernHopfield
│   ├── storage.rs       # LmdbStorage（heed 封装，4 子库）
│   └── index.rs         # SearchIndex（内存倒排索引）
├── python/
│   └── memhop/
│       ├── __init__.py  # re-export Rust 模块
│       └── py.typed
└── tests/
    ├── test_core.rs
    ├── test_search.rs
    └── test_python.py   # Python 端集成测试
```

---

## 4. API 完整契约

### 4.1 打开数据库

```rust
// Rust
pub fn open(config: OpenConfig) -> Result<MemHopEngine, MemHopError>
```

```python
# Python
memhop.open(
    path: str = "memhop.db",
    *,
    encoder: str = "ngram",            # "ngram" | "bge" | "mock"
    confidence_threshold: float = 0.7,
    beta: float = 8.0,
    max_memories: int = 1_000_000,
    timezone: str = "UTC",             # "UTC" | "Asia/Shanghai" | "local"
) -> MemHopEngine
```

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `path` | str | `"memhop.db"` | 数据库文件路径 |
| `encoder` | str | `"ngram"` | 编码器：ngram（默认） / bge（需 feature flag）/ mock |
| `confidence_threshold` | float | 0.7 | recall 最低置信度 |
| `beta` | float | 8.0 | Hopfield 温度参数 |
| `max_memories` | int | 1,000,000 | 软上限，超限 FIFO 淘汰 normal 级记忆 |
| `timezone` | str | `"UTC"` | 时区 |

上下文管理器支持：

```python
with memhop.open("test.db") as db:
    db.remember("hello")
# 退出自动 close()
```

---

### 4.2 写入记忆

```rust
// Rust
pub fn remember(&self, text: &str, meta: Option<HashMap<String, Value>>, memory_id: Option<&str>) -> Result<String, MemHopError>
```

```python
# Python
db.remember(text: str, meta: dict | None = None, memory_id: str | None = None) -> str
```

**行为**：
- 自动填充 `meta["created_at"]` 为当前时间（按 `timezone` 格式化为 ISO 8601）
- 内部存储统一用 UTC float 时间戳
- 若 meta 含 `"key"` 字段且已存在同 key 记忆 → upsert（覆盖旧记录）
- 写入流程：encode → 写 LMDB → 更新 MHN 模式矩阵 → 更新搜索索引

**返回**：记忆 ID（格式 `m_<12 hex chars>`）。

---

### 4.3 联想召回

```rust
// Rust
pub fn recall(&self, cue: &str) -> Result<Option<Memory>, MemHopError>
```

```python
# Python
db.recall(cue: str) -> Memory | None
```

**返回**：
- `Memory` — 单条最匹配记忆，含 `confidence` 字段（Hopfield 收敛分数 0-1）
- `None` — 无匹配（confidence < `confidence_threshold`）

**不参与召回**：`meta.is_dormant == True` 的记忆。

**两阶段检索**（总记忆数 > 500 时自动启用）：
1. Sparse 粗筛：ngram sparse 权重过滤 → ≤ 500 候选
2. MHN 精排：dense 向量 Hopfield 一步收敛 → 单条最优

**性能目标**：100K 记忆 < 2ms。

---

### 4.4 Top-K 召回

```rust
// Rust
pub fn recall_topk(&self, cue: &str, k: usize) -> Result<Vec<Memory>, MemHopError>
```

```python
# Python
db.recall_topk(cue: str, k: int = 5) -> list[Memory]
```

按 confidence 降序排列。结果数 ≤ k。

---

### 4.5 删除记忆

```rust
// Rust
pub fn forget(&self, memory_id: &str) -> Result<bool, MemHopError>
```

```python
# Python
db.forget(memory_id: str) -> bool
```

**保护规则**：

| protection 级别 | forget() |
|:---|:---:|
| `"permanent"` | 拒绝 → 返回 False |
| `"protected"` | 允许 |
| `"normal"` | 允许 |

---

### 4.6 更新记忆

```rust
// Rust
pub fn update(&self, memory_id: &str, text: Option<&str>, meta: Option<HashMap<String, Value>>) -> Result<bool, MemHopError>
```

```python
# Python
db.update(memory_id: str, text: str | None = None, meta: dict | None = None) -> bool
```

`text=None` 保持原文本；`meta=None` 保持原 meta；`meta` 非 None 时覆盖。permanent 级记忆仍可更新（只是不能删）。文本变更时重新编码并更新 MHN。

---

### 4.7 元数据搜索

```rust
// Rust
pub fn search(&self, filters: HashMap<String, Value>, limit: usize) -> Result<Vec<Memory>, MemHopError>
```

```python
# Python
db.search(filters: dict, limit: int = 20) -> list[Memory]
```

**过滤语法**：

| 操作符 | 示例 | 含义 |
|--------|------|------|
| 等值 | `{"layer": "entity"}` | `meta.layer == "entity"` |
| 大于 | `{"importance_gt": 0.7}` | `meta.importance > 0.7` |
| 小于 | `{"importance_lt": 0.3}` | `meta.importance < 0.3` |
| 数组包含 | `{"tags_contains": "早餐"}` | `"早餐" in meta.tags` |
| 引用查询 | `{"connections_to": "e_017"}` | `meta.connections` 中含 `{"to":"e_017"}` |

**支持字段**（全部 10 个）：`layer`, `type`, `domain`, `is_dormant`, `protection`, `session_id`, `path`, `parent`, `importance`, `created_at`

无匹配时返回 `[]`。

---

### 4.8 最近记忆

```rust
// Rust
pub fn recent(&self, limit: usize) -> Result<Vec<Memory>, MemHopError>
```

```python
# Python
db.recent(limit: int = 5) -> list[Memory]
```

按 `created_at` 倒序。含 dormant 和所有保护级别。

---

### 4.9 批量写入

```rust
// Rust
pub fn remember_batch(&self, items: Vec<BatchItem>) -> Result<Vec<String>, MemHopError>
```

```python
# Python
db.remember_batch(items: list[dict]) -> list[str]
# items = [{"text": "...", "meta": {...}}, ...]
```

**原子性**：LMDB 单事务保证，全成功或全失败。

---

### 4.10 按时间清理

```rust
// Rust
pub fn purge_before(&self, before: DateTime<Utc>) -> Result<usize, MemHopError>
```

```python
# Python
db.purge_before(before: datetime) -> int
```

删除 `created_at < before` 且 `protection == "normal"` 的记忆。返回删除条数。

---

### 4.11 关闭

```rust
// Rust
pub fn close(self) -> Result<(), MemHopError>
```

```python
# Python
db.close() -> None
```

关闭后调用任何方法 → `MemHopClosedError`。

---

### 4.12 状态查询

```rust
// Rust
pub fn count(&self) -> usize
pub fn stats(&self) -> Stats
```

```python
# Python
db.count -> int             # 总记忆数（含 dormant）
db.stats -> dict            # 运行时统计
```

`Stats` 结构：

```rust
struct Stats {
    total_memories: usize,
    storage_path: String,
    encoder_mode: String,       // "ngram" | "bge" | "mock"
    beta: f64,
    threshold: f64,
    max_memories: usize,
    index_size_bytes: usize,
}
```

---

## 5. 数据模型

### 5.1 Memory

```rust
// Rust
pub struct Memory {
    pub id: String,            // m_<12 hex>
    pub text: String,
    pub meta: serde_json::Value,
    pub confidence: f64,       // recall 时有效，search/recent 时为 0.0
    pub created_at: String,    // ISO 8601 with timezone
}
```

### 5.2 Meta 字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `layer` | str | — | `"entity"` / `"knowledge"` / `"episode"` |
| `type` | str | — | `"topic"` / `"code"` / `"decision"` / `"insight"` / `"workflow"` / `"preference"` |
| `domain` | str | — | `"code"` / `"finance"` / `"film"` / `"legal"` |
| `is_dormant` | bool | false | 休眠标记：不参与 recall，但 search 可见 |
| `protection` | str | `"normal"` | `"permanent"` / `"protected"` / `"normal"` |
| `session_id` | str | — | 会话 ID |
| `path` | str | — | 知识树节点路径 |
| `parent` | str | — | 父节点 ID |
| `importance` | float | 0.5 | 重要性 0-1 |
| `connections` | list[dict] | — | `[{"to":"e_002","relation":"caused_by","confidence":0.8}]` |
| `tags` | list[str] | — | 自由标签 |
| `key` | str | — | 去重键（存在时触发 upsert） |

### 5.3 Protection

| 级别 | forget() | purge_before() | max_memories 淘汰 |
|:---|:---:|:---:|:---:|
| `"permanent"` | ❌ 拒绝 | ❌ 跳过 | ❌ 跳过 |
| `"protected"` | ✅ | ❌ 跳过 | ✅ 最后淘汰 |
| `"normal"` | ✅ | ✅ 允许 | ✅ 最先淘汰 |

### 5.4 Dormant

`is_dormant=True` → `recall()` / `recall_topk()` 不返回；`search()` / `recent()` 仍可见。用于"暂时不用但保留"的记忆。

---

## 6. 编码器

### 6.1 Encoder trait

```rust
pub trait Encoder: Send + Sync {
    fn encode(&self, text: &str) -> Result<EncoderOutput, MemHopError>;
}

pub struct EncoderOutput {
    pub dense: Vec<f16>,              // [1024] float16, L2 归一化
    pub sparse: HashMap<String, f32>, // ngram 字面权重 / BGE-M3 lexical weights
}
```

### 6.2 NgramEncoder（默认）

零模型依赖。文本 → 切分为 2/3/4-gram 字符片段 → 哈希到 1024 维 → 叠加归一化。

```
"hello" → ["he","el","ll","lo", "hel","ell","llo", "hell","ello"]
       → hash each → 1024-dim vector → L2 normalize
```

- 空字符串输入 → 零向量
- 全语言通用（中日韩英阿……），只在字符层面操作
- 延迟 < 0.1ms，零内存开销

### 6.3 BgeEncoder（可选）

Feature flag `semantic`。BGE-M3 ONNX INT8，~300MB 下载，< 5ms 编码。dense + sparse 双输出。

```toml
# Cargo.toml
[features]
semantic = ["ort"]
```

### 6.4 编码器选择

| encoder 参数 | 对应 Encoder | 依赖 |
|-------------|-------------|------|
| `"ngram"` | NgramEncoder | 无 |
| `"bge"` | BgeEncoder | `feature = "semantic"`, BGE-M3 模型 |
| `"mock"` | MockEncoder（SHA256 确定性伪随机） | 无 |

---

## 7. 搜索索引

### 7.1 设计

内存倒排索引。`open()` 时从 LMDB 扫描一次构建，后续读写时增量更新。

Rust 版索引导出到 LMDB 第 4 子库 `i`（Index），后续 `open()` 直接 mmap 加载，不再重建。

### 7.2 索引结构

```rust
struct SearchIndex {
    // 等值索引
    idx_layer: HashMap<String, HashSet<String>>,
    idx_type: HashMap<String, HashSet<String>>,
    idx_domain: HashMap<String, HashSet<String>>,
    idx_session_id: HashMap<String, HashSet<String>>,
    idx_parent: HashMap<String, HashSet<String>>,
    idx_path: HashMap<String, HashSet<String>>,
    idx_protection: HashMap<String, HashSet<String>>,

    // 范围索引（排序列表）
    idx_importance: BTreeMap<OrderedFloat<f64>, HashSet<String>>,
    idx_created_at: BTreeMap<i64, HashSet<String>>,  // UTC timestamp

    // 引用索引
    idx_connections_to: HashMap<String, HashSet<String>>,

    // 休眠索引
    idx_is_dormant_false: HashSet<String>,
    idx_is_dormant_true: HashSet<String>,
}
```

### 7.3 查询处理

```
输入: {"layer": "entity", "importance_gt": 0.7, "is_dormant": false}

1. 等值交集: idx_layer["entity"] ∩ idx_is_dormant_false
2. 范围过滤: 从 idx_importance.range(0.7..) 取 ID 集合，再取交集
3. 数组包含: 遍历剩余候选，检查 meta.tags
4. 引用查询: 直接用 idx_connections_to
5. 截断到 limit
```

### 7.4 索引生命周期

| 事件 | 操作 |
|------|------|
| open()（首次） | 扫描 meta_db → 构建全部索引 → 快照到 LMDB 子库 `i` |
| open()（非首次） | mmap 加载 `i` 子库 → 反序列化索引（< 1ms） |
| remember() | 逐字段插入索引 + 标记 `i` 脏（lazy flush） |
| forget() | 逐字段删除 + 标记 `i` 脏 |
| update() | 删除旧条目 → 插入新条目 + 标记 `i` 脏 |
| close() | flush `i` 子库到 LMDB |

---

## 8. 存储设计

### 8.1 LMDB 子数据库

单文件 `memhop.db`，4 个子库：

| 子库 | 键 | 值 | 说明 |
|------|----|----|------|
| `p` | memory_id (16 bytes) | [f16; 1024] (2048 bytes) | 模式向量 |
| `b` | memory_id | zstd 压缩的 JSON `{"t": text, "m": meta}` | 文本 + 元数据 |
| `m` | memory_id | (timestamp: i64, confidence: f32) 12 bytes | 时间戳 + 置信度 |
| `i` | index_name (str) | bincode 序列化的索引段 | 索引快照 |

### 8.2 配置

```rust
let env = EnvOpenOptions::new()
    .map_size(1_073_741_824)  // 1GB，自动增长
    .max_dbs(4)
    .open(path)?;
```

### 8.3 mmap 内存模型

全部数据通过 mmap 映射。OS 按 4KB 缺页加载，只有被实际访问的记忆才进入 RAM。

| 数据 | 磁盘占用（100K） | 常驻 RAM（稳定态） |
|------|-----------------|-------------------|
| 模式矩阵 (f16) | ~200MB | 仅热记忆页 |
| Blobs (zstd) | ~50MB | 仅解压时暂存 |
| Meta | ~1MB | ~1MB |
| 索引 | ~30MB | ~30MB（全量在内存） |

---

## 9. 三层记忆模型

### 9.1 层次定义

```
Entity Layer（纠缠图）  meta.layer = "entity"
  关系: meta.connections = [{"to":"e_002","relation":"caused_by","confidence":0.8}]
  "支付模块空指针 bug 是由昨天的代码合并引起的"

Knowledge Layer（知识树） meta.layer = "knowledge"
  结构: meta.parent → 父节点 ID, meta.path → 文件路径
  "payment.py 模块结构：validate / process / confirm"

Episode Layer（原文对话） meta.layer = "episode"
  关联: meta.session_id → 会话
  "用户: 支付报错了 | AI: 我来看看错误日志"
```

### 9.2 存储示例

```python
# entity
db.remember("支付模块空指针 bug", meta={
    "layer": "entity", "type": "code", "protection": "permanent",
    "connections": [{"to": "e_002", "relation": "caused_by", "confidence": 0.8}],
})

# knowledge
db.remember("payment.py: validate/process/confirm", meta={
    "layer": "knowledge", "path": "payment.py", "parent": "k_root",
})

# episode
db.remember("用户:支付报错了\nAI:我来看看", meta={
    "layer": "episode", "session_id": "s_007",
})
```

层间关联由应用层通过 `connections` 和共享 meta 字段自行管理。MemHop 不维护层间外键。

---

## 10. 清理与生命周期

### 10.1 FIFO 淘汰

`remember()` 后 `count > max_memories` → 删除最早的非 permanent 记忆，直到 `count ≤ max_memories`。

淘汰顺序：oldest normal → oldest protected。permanent 永不淘汰。

### 10.2 手动清理

`purge_before(datetime)` → 删除 `created_at < datetime` 且 `protection == "normal"` 的记忆。

### 10.3 关闭

`close()` 后：
- 所有方法 → `MemHopClosedError`
- 上下文管理器退出时自动调用
- flush 索引快照到 LMDB

---

## 11. 性能目标

| 指标 | 1K | 10K | 100K | 100 万 |
|------|----|-----|------|--------|
| `recall()` | < 0.5ms | < 1ms | < 2ms | < 5ms |
| `remember()` | < 1ms | < 3ms | < 5ms | < 10ms |
| `search()` | < 1ms | < 5ms | < 10ms | < 50ms |
| `recent()` | < 0.5ms | < 0.5ms | < 1ms | < 2ms |
| `remember_batch(100)` | < 20ms | < 50ms | < 100ms | < 200ms |
| 启动时间 | < 1ms | < 1ms | < 2ms | < 5ms |
| 运行时内存 | ~10MB | ~25MB | ~100MB | ~800MB |
| 磁盘（memhop.db） | ~1MB | ~10MB | ~100MB | ~1GB |

---

## 12. 非功能需求

| 需求 | 规格 |
|------|------|
| **部署模式** | 嵌入式库，非服务。无端口、无 HTTP |
| **Rust 版本** | stable, edition 2024 |
| **Python 版本** | ≥ 3.10 |
| **安装体积** | < 3MB（默认 ngram 模式） |
| **依赖数** | Rust: heed, zstd, serde, bincode, rand, pyo3（6 个核心） |
| **ACID** | LMDB 原生保证 |
| **并发** | 单写多读，与 MeowAgent 多进程架构兼容 |
| **编码器** | 默认 ngram（零模型），bge 可选（feature flag） |
| **时区** | 可配，默认 UTC |
| **构建工具** | maturin |
| **上下文管理器** | 支持 `with` 语句 |

---

## 13. 验收标准

### P0 — 核心通路

| # | 测试 | 标准 |
|---|------|------|
| 1 | 基本召回 | `remember("豆浆油条") → recall("早餐吃什么")` → confidence > 0.7 |
| 2 | 无匹配 | `recall("火星探测")` → None |
| 3 | 多记忆区分 | 100 条相似记忆 → 每条精确召回 |
| 4 | 中文短词 | ngram 两阶段检索 → `recall("早餐")` 正确 |
| 5 | 大规模 | 10K 记忆 → recall < 2ms, remember < 5ms |
| 6 | 关闭错误 | `close() → remember()` → `MemHopClosedError` |
| 7 | 上下文管理器 | `with open(...)` → 退出后 close |
| 8 | 崩溃恢复 | kill -9 → 重启 → 数据完整 |
| 9 | 批量写入 | `remember_batch(100)` → 原子性 |
| 10 | Upsert | 同 key 两次写入 → 只有一条 |

### P1 — Protection & 搜索

| # | 测试 | 标准 |
|---|------|------|
| 11 | permanent | `forget(permanent_id)` → False |
| 12 | protected | `purge_before(now)` → 不删 protected |
| 13 | FIFO | 超 max_memories → oldest normal 淘汰 |
| 14 | dormant | dormant 记忆 → recall 不返回 |
| 15 | 等值过滤 | `search({"layer":"entity"})` → 只返回 entity |
| 16 | 范围过滤 | `search({"importance_gt":0.7})` → 只返回 > 0.7 |
| 17 | 组合过滤 | 多条件交集正确 |
| 18 | 引用查询 | `search({"connections_to":"e_001"})` → 返回引用者 |
| 19 | 空结果 | 无匹配 → `[]` |

### P1 — 三层集成

| # | 测试 | 标准 |
|---|------|------|
| 20 | 三层同存 | entity/knowledge/episode → search 按层分离 |
| 21 | 层间关联 | connections 引用查询返回 |

### P2

| # | 测试 | 标准 |
|---|------|------|
| 22 | recent | `recent(5)` → 时间倒序 |

---

> 📋 **AI 团队开发指引**：本文档是唯一开发依据。按 `ROADMAP.md` 的任务顺序实现，每个任务对应本文档的相关章节。先读 §4 API 契约了解接口，再读 §7-§8 了解索引和存储，最后按 §13 逐条验收。
