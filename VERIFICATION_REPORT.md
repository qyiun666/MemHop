# MemHop 验收报告

> **交付版本**：v0.2.0  
> **验收日期**：2026-05-19  
> **对照基准**：`REQUIREMENTS.md` v2.0（唯一开发依据）

---

## 1. 测试执行结果

| 测试层 | 数量 | 通过 | 失败 | 
|--------|------|------|------|
| Rust 单元测试 (cargo test) | 38 | 38 | 0 |
| Python 验收测试 (pytest) | 51 | 51 | 0 |

### 验收用例逐项对照（REQUIREMENTS.md §13）

#### P0 — 核心通路 (10/10 ✅)

| # | 用例 | 结果 |
|---|------|:--:|
| 1 | 基本召回 — overlapping ngram → confidence > 0.7 | ✅ |
| 2 | 无匹配 — 无关文本 → None | ✅ |
| 3 | 多记忆区分 — 10 条各异话题 → 精确召回目标 | ✅ |
| 4 | 中文短词 — "北京" → 找到含"北京"的记忆 | ✅ |
| 5 | 大规模 — 10K 记忆 recall < 100ms（Python FFI 含） | ✅ |
| 6 | 关闭错误 — close() → 所有操作 → MemHopClosedError | ✅ |
| 7 | 上下文管理器 — with open() → 退出后 close | ✅ |
| 8 | 崩溃恢复 — write → close → reopen → 数据完整 | ✅ |
| 9 | 批量写入 — remember_batch(100) → 原子性 + 唯一 ID | ✅ |
| 10 | Upsert — 同 key 两次写入 → 只保留最新 | ✅ |

#### P1 — Protection & 搜索 (9/9 ✅)

| # | 用例 | 结果 |
|---|------|:--:|
| 11 | permanent forget → False | ✅ |
| 12 | purge_before → 不删 protected/permanent | ✅ |
| 13 | FIFO 淘汰 → oldest normal 先淘汰 | ✅ |
| 14 | dormant → recall 不返回 | ✅ |
| 15 | 等值过滤 → layer=entity 只返回 entity | ✅ |
| 16 | 范围过滤 → importance_gt > 0.7 严格 | ✅ |
| 17 | 组合过滤 → 多条件交集 | ✅ |
| 18 | 引用查询 → connections_to | ✅ |
| 19 | 空结果 → 无匹配返回 [] | ✅ |

#### P1 — 三层集成 (2/2 ✅)

| # | 用例 | 结果 |
|---|------|:--:|
| 20 | 三层同存 → search 按层分离，无污染 | ✅ |
| 21 | 层间关联 → connections 引用查询正确 | ✅ |

#### P2 (1/1 ✅)

| # | 用例 | 结果 |
|---|------|:--:|
| 22 | recent → 时间倒序 | ✅ |

---

## 2. 对照 REQUIREMENTS.md 的偏差清单

### 🔴 P0 — 必须修

| # | 文件 | 偏差 | 规格要求 |
|---|------|------|----------|
| **1** | `python/memhop/__init__.py:19` + `src/engine.rs:392` | **`open()` 缺少 `encoder` 和 `timezone` 参数**。当前只有 `path / confidence_threshold / beta / max_memories` | §4.1: `encoder: str = "ngram"` 和 `timezone: str = "UTC"` |

**影响**：MeowAgent 集成时无法切换编码器、无法配置时区。

---

### 🟡 P1 — 内存浪费

| # | 文件 | 偏差 | 规格要求 |
|---|------|------|----------|
| **2** | `src/encoder/mod.rs:7` | `EncoderOutput.dense: Vec<f32>` — 全 f32 存储 1024 维 = 4KB/条 | §6.1: `Vec<f16>` — f16 存 1024 维 = 2KB/条 |
| **3** | `src/types.rs:103` | `MemoryRecord.dense_vector: Vec<f32>` | 同上 |
| **4** | `src/engine.rs:926-935` | `stats` getter 只返回 `count/dim/beta/threshold`，缺少：`storage_path`, `encoder_mode`, `max_memories`, `index_size_bytes` | §4.12 Stats 结构 |

**影响**：100K 记忆 ≈ 400MB (f32) vs 200MB (f16)，内存翻倍。stats 信息不完整影响运维。

---

### 🟠 P2 — 优化项

| # | 文件 | 偏差 | 规格要求 |
|---|------|------|----------|
| **5** | `src/engine.rs:864-906` | `search()` 是 **线性 O(n) 全量扫描**。`SparseIndex`（index.rs）已实现但只用于 recall 粗筛，未集成到 search 过滤。每次 search 都遍历 `all_ids()` 然后逐条 `matches_filters()` | §7.3 查询流程：等值交集 O(1) + 范围过滤 + 截断 |
| **6** | `Cargo.toml:4` | `edition = "2021"` | §2: `edition 2024` |
| **7** | `src/storage.rs:96-99` | LMDB 子库名 `patterns/blobs/meta/index` | §8.1: `p/b/m/i` |

**影响**：search 在 100K+ 记忆时性能差，但不影响功能正确性。

---

### 🟢 P3 — 设计差异

| # | 文件 | 差异 | 说明 |
|---|------|------|------|
| **8** | `src/storage.rs` | 无独立 `m` 置信度子库 | §8.1 要求 `m` 库独立存 (timestamp, confidence)，当前 timestamp 混在 `meta` 库。不阻塞功能。 |

---

## 3. 总评

| 维度 | 评分 | 说明 |
|------|:--:|------|
| 功能完整度 | ⭐⭐⭐⭐☆ | 22/22 验收通过，核心 API 全齐 |
| API 契约对齐 | ⭐⭐⭐☆☆ | 缺 encoder/timezone 参数 |
| 存储效率 | ⭐⭐⭐☆☆ | 全 f32 而非规格的 f16 |
| 性能 | ⭐⭐⭐⭐☆ | 10K recall 在 Python FFI 下 < 100ms |
| 代码质量 | ⭐⭐⭐⭐☆ | 模块清晰，自测覆盖充分 |

**结论**：可集成，但建议修完 2 个 P0 问题后再进 MeowAgent。

---

## 4. 修复建议

### 必须修（P0）

1. **`open()` 加 `encoder` 和 `timezone` 参数**
   - Rust `MemHopEngine::new` 新增 `encoder: &str`、`timezone: &str`
   - Python `__init__.py:open()` 透传这两个参数
   - 当前只有 ngram encoder，`encoder="ngram"` 直接默认，`encoder="bge"` 返回 NotImplemented

### 建议修（P1）

2. **f32 → f16 存储**：使用 `half::f16` crate（已在 Cargo.toml 依赖中），EncoderOutput/MemoryRecord 的 dense 字段改为 `Vec<f16>`
3. **stats 补字段**：Rust `stats()` 返回完整 Stats 结构
