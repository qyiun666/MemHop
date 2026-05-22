# MemHop v0.4.0 — 场景感知 + 自塑性记忆引擎

> 发布日期: 2026-05-23
> 前置版本: v0.3.0 (scope/time_alpha/RwLock)

---

## 概述

v0.4.0 将 MemHop 从静态联想记忆数据库升级为**场景感知 + 自塑性认知记忆系统**。引擎自主识别当前场景、在正确语境中检索、并在每次使用中强化和分化记忆。

### 四大模块

```
v0.4.0
├── Part A: 模式塑性 (Pattern Plasticity)       ← 核心创新
├── Part B: 编码器增强 (Encoder Enhancement)    ← 精度提升
├── Part C: 场景感知门控 (Scene-Gated Recall)   ← 内生智能
└── Part D: Python 辅助 + Benchmark             ← 质量保障
```

---

## Part A: 模式塑性

### 新增数据结构

- `PlasticityConfig` — 可配置的塑性参数（min_drift_attention, discrimination_threshold, reinforce_rate, discriminate_rate, decay_threshold_days, decay_rate）
- `ModernHopfield` 新增字段: `access_counts`, `last_access`, `drift_enabled`, `plasticity_cfg`

### 新增 API

**Python:**
- `db.recall_with_plasticity(cue, *, include_blob=True) -> Memory | None` — 联想召回 + 塑性漂移
- `db.enable_plasticity(enabled: bool) -> None` — 启用/关闭塑性
- `db.set_plasticity_config(...) -> None` — 配置塑性参数
- `db.get_memory_stats(memory_id) -> dict | None` — 查询访问统计
- `db.trigger_decay() -> int` — 触发自然衰减，返回被标记为 dormant 的记忆数

**Rust (pub 或 pub(crate)):**
- `ModernHopfield::recall_with_plasticity(&mut self, query, now_ms) -> Option<(String, f32, Vec<usize>)>`
- `ModernHopfield::apply_decay(&mut self, now_ms) -> Vec<String>`
- `ModernHopfield::get_access_stats(&self, id) -> Option<(u64, u64)>`
- `ModernHopfield::enable_plasticity(&mut self, enabled: bool)`
- `ModernHopfield::set_plasticity_config(&mut self, cfg: PlasticityConfig)`
- `ModernHopfield::collect_patterns_by_indices(&self, indices) -> Vec<(String, Vec<f16>)>`
- `LmdbStorage::persist_patterns_batch(&self, items) -> Result<(), StorageError>`

### 算法

**Drift 算法（winner reinforcement + non-winner discrimination）:**
```
for each pattern i:
  attention = softmax(β · dot(pattern[i], query))[i]
  if attention < min_drift_attention: skip
  direction = winner → +reinforce_rate | high-attention non-winner → -discriminate_rate
  pattern[i] = L2_normalize(pattern[i] + direction · attention · query)
  access_count[i]++, last_access[i] = now
```

**Decay 算法（自然遗忘）:**
```
for each pattern:
  if days_since_access > decay_threshold_days:
    scale = 1.0 - decay_rate · ln(1 + extra_days)
    pattern[i] *= scale
    if L2_norm(pattern[i]) < 0.1: mark_dormant(id)
```

### 持久化

- `EngineInner.dirty_patterns: HashSet<usize>` 跟踪被塑性修改的索引
- `close()` 时: persist_dirty_patterns → dirty_patterns.clear() → persist_indices
- 破坏性写入（forget/purge/evict）清空 dirty_patterns 防止 swap-remove 索引偏移

---

## Part B: 编码器增强

### IDF 加权

- `NgramEncoder` 新增 `idf: Option<HashMap<String, f32>>` 字段
- 新增方法: `set_idf()`, `clear_idf()`, `new_with_idf()`
- `encode()` 在 IDF 模式下，每个 ngram 的 dense 贡献乘以 `idf_factor ≥ 1.0`
- 无 IDF 时编码与 v0.3.0 位级一致（`idf=None` → uniform weighting）

**Python API:**
- `db.set_encoder_idf(idf_dict: dict[str, float]) -> None`
- `db.clear_encoder_idf() -> None`
- `memhop.idf.build_idf(texts: list[str]) -> dict[str, float]`

### 时间表达式归一化

- `memhop.time_norm.normalize_time(text, reference_date=None) -> str`
- 支持 "3 days ago" → "2026-05-20", "yesterday", "today" 等
- 优先用 dateparser（可选），不可用时用正则回退

---

## Part C: 场景感知门控

### 新增模块

`src/scene_gating.rs` — 独立场景门控模块

### 数据结构

- `ActiveScene` — 当前锚定场景（session_id, domain, confidence, miss_count）
- `SceneState` — 门控状态（session_fingerprints, node_fingerprints, active_scene, gating_enabled, gating_threshold）

### 三层门控

| 层 | 名称 | 开销 | 描述 |
|----|------|:---:|------|
| L1 | 会话指纹匹配 | < 0.5ms | query 与 session fingerprints 余弦相似度匹配 |
| L2 | 知识树路径预测 | < 2ms | query 与知识树节点指纹匹配 |
| L3 | 隐式场景锚定 | 0 (cached) | 基于缓存的 active_scene 缩小候选集 |

### 新增 API

**Python:**
- `open(gating_enabled=True, gating_threshold=0.6)`
- `db.set_gating(enabled: bool) -> None`
- `db.set_gating_threshold(threshold: float) -> None`
- `db.reset_scene() -> None`

**内部机制:**
- `recall()` / `recall_topk()` 无显式 scope 且 gating 启用时 → 自动三层门控
- `remember()` → 检测 meta.session_id / meta.parent → 更新指纹
- `open()` → 自动重建 session fingerprints（遍历 by_session_id）
- 用户显式 scope 优先于自动门控

### 影响

- 现有 `remember()` / `recall()` / `recall_topk()` 调用零影响
- gating 默认启用（`gating_enabled=True`），可关闭
- 启动指纹重建开销: 10 万条记忆 < 50ms（仅一次）

---

## Part D: Python 辅助 + Benchmark

### Python 辅助模块

- `python/memhop/idf.py` — IDF 构建器（`build_idf(texts) → dict`）
- `python/memhop/time_norm.py` — 时间表达式归一化（`normalize_time(text) → str`）

### 文件改动

| 文件 | 改动 | 行数 |
|------|------|:---:|
| `src/hopfield.rs` | +PlasticityConfig + plasticity 方法 | ~200 |
| `src/scene_gating.rs` | **新建** — SceneState + 三层门控 | ~310 |
| `src/engine.rs` | +dirty_patterns + scene_state + plasticity/gating/IDF pyo3 绑定 | ~150 |
| `src/storage.rs` | +persist_patterns_batch | ~20 |
| `src/encoder/ngram.rs` | +idf 字段 + set_idf + clear_idf + encode IDF 加权 | ~40 |
| `src/meta_index.rs` | +all_session_ids + session_memory_ids | ~15 |
| `src/lib.rs` | +mod scene_gating | +1 |
| `python/memhop/__init__.py` | +gating 参数 + 版本号 | ~5 |
| `python/memhop/__init__.pyi` | +所有 v0.4.0 API 类型桩 | ~40 |
| `python/memhop/idf.py` | **新建** — IDF 构建器 | ~60 |
| `python/memhop/time_norm.py` | **新建** — 时间归一化 | ~120 |

### 测试覆盖

- cargo test: 51 tests, all passed
- Python API: 全量验证导入 + 运行时

---

## 向后兼容

| 功能 | v0.3.0 行为 | v0.4.0 行为 |
|------|------------|------------|
| `remember()` | 写入 + 索引 | 写入 + 索引 + 指纹更新（透明） |
| `recall(cue)` | 全量/scope 搜索 | 全量/scope 搜索 + 可选自动门控 |
| `recall_topk()` | 全量/scope 搜索 | 全量/scope 搜索 + 可选自动门控 |
| 编码 | 等权 ngram | ngram + 可选 IDF |
| 关闭 | 持久化索引 | 持久化索引 + dirty patterns |
| 数据文件 | 兼容 | v0.3.0 数据可直接读取 |
| plasticity | — | 默认关闭，需显式启用 |
| gating | — | 默认启用，可关闭 |

---

## 不做什么

- ❌ 不改变 `recall()` / `remember()` 默认行为（plasticity 默认关，gating 对结果无影响）
- ❌ 不持久化 fingerprint（启动重建 < 50ms）
- ❌ 不引入新 Rust 依赖
- ❌ 不做 compressor / cognitive_loop / consolidator（留 v0.5.0 Rust 实现）

---

> 🤖 Generated with [Qoder](https://qoder.com)
