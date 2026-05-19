# MemHop 开发路线图

> **适用对象**：接手 MemHop 开发的 AI 或开发者。本文档是"照着做"的执行手册。

---

## 📌 版本方案

```
v0.1.0  ← 当前 (项目骨架)
v0.1.1  ← 下一个
v0.1.2
 ...
v0.1.N  ← Phase 1 完成前都在 0.1.* 递增
v1.0.0  ← Phase 1 全部完成
```

---

## 📖 关键文档

| 文档 | 路径 | 用途 |
|------|------|------|
| 需求规格 | `REQUIREMENTS.md` | **完整 API 契约、数据模型、验收标准（先读这个）** |
| 系统设计 | `DESIGN.md` | 架构决策、算法推导、ADR |
| 本文档 | `ROADMAP.md` | 开发任务清单 |
| MeowAgent 需求 | `../meowagent/deliverables/engineering-assurance/memhop-requirements-from-meowagent-2026-05-19.md` | 对接需求原文 |
| 项目介绍 | `README.md` | 快速开始 |

---

## 📌 项目现状 (v0.1.0 骨架)

> ⚠️ **当前骨架是 Python 原型**（算法验证用）。目标产出是 **Rust + pyo3** 生产版（见 REQUIREMENTS.md §3 Rust 项目结构）。Phase 1 任务在 Python 骨架上验证核心算法正确性，验证通过后按 REQUIREMENTS.md 的 Rust 项目结构重写。

```
✅ types.py       — Memory, EncoderOutput, EncoderConfig
✅ hopfield.py    — Modern Hopfield Network 核心 (one-step recall)
✅ storage.py     — LMDB 三子库 (patterns/blobs/meta)
✅ encoder.py     — ApiEncoder / LocalEncoder / MockEncoder
✅ engine.py      — MemHopEngine (remember/recall/forget/search)
✅ pyproject.toml — pip install -e 可用
✅ __init__.py    — memhop.open(path, encoder, ...) ← path 可配

⚠️ Mock 编码器是随机向量 — 语义 recall 未验证
⚠️ 两阶段检索是 TODO 空壳
⚠️ search() 仅支持 tags + text_contains — 缺少 meta 字段索引
⚠️ Memory 缺 created_at 字段
⚠️ 缺少 recent() / remember_batch() / purge_before()
⚠️ 缺少 is_dormant / protection / upsert / connections_to
⚠️ 0 个测试
⚠️ 0 个性能基准
```

---

## Phase 1: 核心能力（当前阶段）

### Task 1.1 — 编码器验证：ngram 哈希模式

**目标**：验证 NgramEncoder 的字符 n-gram 哈希向量有效，相似文本向量相近。

**涉及文件**：`tests/test_encoder.py`（新建）
**依赖**：无（零模型依赖）

**验收标准**：
- `sim("今天吃了豆浆油条", "今天早餐吃了什么") > 0.7`
- `sim("今天吃了豆浆油条", "火星探测任务") < 0.3`

---

### Task 1.2 — 语义召回端到端验证

**目标**：`remember → recall` 全链路语义召回正确。

**涉及文件**：`tests/test_e2e.py`（新建）
**依赖**：Task 1.1

**验收标准**：
- cue "今天吃了什么早餐" → 召回到 "豆浆油条"，confidence > 0.7
- cue "量子力学" → None

---

### Task 1.3 — Mock 编码器改为确定性伪语义

**目标**：不依赖 API Key 也能做基本区分，加速本地迭代。

**涉及文件**：`src/memhop/encoder.py`
**依赖**：无

**验收标准**：相同文本相同向量，recall 通路不总是返回 None

---

### Task 1.4 — 两阶段检索

**目标**：Sparse 粗筛 → MHN 精排，解决中文短词。

**涉及文件**：`src/memhop/engine.py`，`src/memhop/hopfield.py`
**依赖**：Task 1.1 或 Task 1.3

**验收标准**：中文短词 "早餐" 召回正确率 > 90%

---

### Task 1.5 — 核心测试套件（10 用例）

覆盖 DESIGN.md §6.1 全部 10 个核心用例。

| # | 场景 | 预期 |
|---|------|------|
| 1 | 基本写入召回 | `remember → recall` 一致 |
| 2 | 语义相似 | "吃了什么" → "豆浆油条" |
| 3 | 无匹配 | `recall("无关")` → None |
| 4 | 多记忆区分 | 100 条 → 精确区分 |
| 5 | 大规模压力 | 1000 条 → recall < 5ms |
| 6 | 并发写入 | 多线程无损坏 |
| 7 | 崩溃恢复 | kill -9 重启完整 |
| 8 | 中文短词 | "早餐" → 正确 |
| 9 | 遗忘 | `forget → recall` → None |
| 10 | 更新 | `update` → 新内容 |

**涉及文件**：`tests/test_core.py`（新建）
**依赖**：Task 1.1

---

### Task 1.6 — 性能基准

实现 DESIGN.md §6.2 对比基准。

| 规模 | MeowAgent FTS5 | MemHop 目标 |
|------|---------------|-------------|
| 1K | ~2ms | < 1ms |
| 10K | ~15ms | < 2ms |
| 100K | ~150ms | < 5ms |

**涉及文件**：`examples/benchmark.py`，`tests/test_perf.py`
**依赖**：Task 1.5

---

### Task 1.7 — BGE-M3 编码器（可选语义增强）

**目标**：验证 BgeM3Encoder 可工作，提供语义增强能力。

**涉及文件**：`tests/test_encoder_bge.py`
**依赖**：`pip install memhop[semantic]`（自动下载 BGE-M3 ONNX 模型）

---

### Task 1.8 — 边界错误处理

**涉及文件**：`src/memhop/engine.py`，`src/memhop/storage.py`
**依赖**：Task 1.2

---

## Phase 2: MeowAgent 需求对齐（新增）

> **来源**：`memhop-requirements-from-meowagent-2026-05-19.md`

### Task 2.1 — Memory 数据模型补全

**目标**：Memory 增加 `created_at` 字段，`remember()` 自动填充 ISO 8601 时间戳。

**涉及文件**：`src/memhop/types.py`，`src/memhop/engine.py`

**验收标准**：
```python
mid = db.remember("test")
m = db.recall("test")
assert m.created_at  # "2026-05-19T18:30:00"
```

---

### Task 2.2 — `recent()` 和 `remember_batch()` 接口

**目标**：补齐缺失的两个 API。

```python
def recent(self, limit: int = 5) -> list[Memory]:
    """最近写入的记忆，按 created_at 倒序"""

def remember_batch(self, items: list[dict]) -> list[str]:
    """批量写入。items = [{"text": "...", "meta": {...}}, ...]"""
```

**涉及文件**：`src/memhop/engine.py`

**验收标准**：
- `recent(3)` 返回最近 3 条
- `remember_batch` 原子写入，全成功或全失败

---

### Task 2.3 — `search()` 支持任意 meta 字段过滤

**目标**：当前 `search()` 只支持 `tags` 和 `text_contains`。改为支持任意 meta key。

```python
db.search({"layer": "entity", "domain": "code", "importance_gt": 0.7})
db.search({"is_dormant": False, "protection": "permanent"})
```

**支持的比较操作符**：
- 等值：`"key": "value"`
- 大于/小于：`"key_gt": 0.7` / `"key_lt": 0.3`
- 包含：`"tags_contains": "早餐"` (列表字段)

**涉及文件**：`src/memhop/engine.py`

**验收标准**：所有 10 个 meta 字段均可过滤

---

### Task 2.4 — upsert 同 key 去重

**目标**：同 `text` + 同 `meta.key` 时自动覆盖。

```python
db.remember("买咖啡", meta={"key": "daily_001"})
db.remember("买咖啡和面包", meta={"key": "daily_001"})
# → 第二条覆盖第一条，memhop.db 只有一条记录
```

**涉及文件**：`src/memhop/engine.py`，`src/memhop/storage.py`

---

### Task 2.5 — `is_dormant` 休眠标记

**目标**：`meta.is_dormant=True` 的记忆不参与 `recall()`，但 `search()` 可见。

**涉及文件**：`src/memhop/engine.py`，`src/memhop/hopfield.py`

**实现方式**：在 MHN 中维护 dormant mask，recall 时排除；search 时包含。

---

### Task 2.6 — `protection` 保护级别

**目标**：三级保护。

| 级别 | 行为 |
|------|------|
| `"permanent"` | 永不删除，`forget()` 和 `purge_before()` 均无效 |
| `"protected"` | `purge_before()` 跳过，`forget()` 有效 |
| `"normal"` | 正常参与所有操作 |

**涉及文件**：`src/memhop/engine.py`

---

### Task 2.7 — `connections_to` 关联引用查询

**目标**：支持查询"哪些 entity 指向了某 entity"。

```python
db.search({"connections_to": "e_017"})
# → 返回所有 connections 中包含 {"to": "e_017"} 的 entity
```

**涉及文件**：`src/memhop/engine.py`

---

### Task 2.8 — 衰减清理：`purge_before()` + `max_memories` 淘汰

**目标**：防止数据库无限膨胀。

```python
# 清理 N 天前的 normal 级别记忆
db.purge_before(datetime(2026, 4, 1))

# 超出上限时 FIFO 淘汰 normal 级别
db = memhop.open(max_memories=100000)
```

**淘汰优先级**：oldest normal → oldest protected → (never permanent)

**涉及文件**：`src/memhop/engine.py`，`src/memhop/storage.py`

---

### Task 2.9 — `close()` 后操作抛 `MemHopClosedError`

**目标**：`close()` 后调用任何方法应抛 `MemHopClosedError`，不是奇怪的底层错误。

**涉及文件**：`src/memhop/engine.py`，`src/memhop/types.py`

---

### Task 2.10 — 三层记忆模型集成测试

**目标**：模拟 MeowAgent 真实场景：纠缠图 entity + 知识树 node + 原文 turn 并存于同一 memhop.db。

```python
# entity
db.remember("支付模块空指针bug", meta={
    "layer": "entity", "type": "code", "domain": "code",
    "connections": [{"to": "e_002", "relation": "caused_by"}],
})

# knowledge
db.remember("payment.py 模块结构", meta={
    "layer": "knowledge", "domain": "code",
    "path": "payment.py", "parent": "k_root",
})

# episode
db.remember("用户: 支付报错了\nAI: 我来看看", meta={
    "layer": "episode", "session_id": "s_007",
})
```

**涉及文件**：`tests/test_three_layer.py`（新建）
**依赖**：Task 2.1 ~ 2.9

---

## 任务依赖图

```
Phase 1 (核心能力):
Task 1.1 (ngram编码器) ─┬─ Task 1.2 (端到端召回) ─┬─ Task 1.5 (核心测试) ── Task 1.6 (性能基准)
                         │                         └─ Task 1.8 (错误处理)
                         └─ Task 1.4 (两阶段检索)

Task 1.3 (Mock改造) ── 独立
Task 1.7 (BGE-M3编码器) ── 可选增强

Phase 2 (MeowAgent 对齐):
Task 2.1 (created_at) ── Task 2.2 (recent+batch) ── Task 2.3 (search增强)
                                                    └─ Task 2.4 (upsert)
Task 2.5 (dormant) ── 独立
Task 2.6 (protection) ── Task 2.8 (衰减清理)
Task 2.7 (connections_to) ── Task 2.3 后
Task 2.9 (close error) ── 独立
Task 2.10 (三层集成测试) ── Phase 2 全部

Phase 1 和 Phase 2 之间无硬依赖，可并行推进。
```

---

## 可并行执行

| 组 | 任务 | 依赖 |
|----|------|------|
| **A** (编码器) | 1.1 → 1.2 → 1.5 → 1.6 | 无 |
| **B** (检索) | 1.3 → 1.4 | 无 |
| **C** (BGE-M3可选) | 1.7 | BGE-M3 模型 |
| **D** (错误处理) | 1.8 | 1.2 |
| **E** (数据模型) | 2.1 → 2.2 → 2.3 → 2.4 | 无 |
| **F** (生命周期) | 2.5 → 2.6 → 2.8 | 无 |
| **G** (引用查询) | 2.7 | 2.3 |
| **H** (close错误) | 2.9 | 无 |

A~D 和 E~H 之间完全独立，可并行。

---

## 版本发布计划

| 版本 | 内容 | 发布条件 |
|------|------|---------|
| **v0.1.0** ✅ | 项目骨架 | 代码结构就绪，mock 通路跑通 |
| **v0.1.1** | ngram 召回可用 | Task 1.1 + 1.2 通过（ngram 语义召回） |
| **v0.1.2** | 两阶段检索 | Task 1.4 通过 |
| **v0.1.3** | 数据模型完整 | Task 2.1 + 2.2 通过 |
| **v0.1.4** | 搜索增强 | Task 2.3 + 2.4 通过 |
| **v0.1.5** | 生命周期 | Task 2.5 + 2.6 + 2.8 通过 |
| **v0.1.6** | 引用查询 | Task 2.7 + 2.9 通过 |
| **v0.1.7** | BGE-M3 可选 | Task 1.7 通过（可选） |
| **v0.1.8** | 核心测试全绿 | Task 1.5 + 1.6 通过 |
| **v0.1.9** | 三层集成验证 | Task 2.10 通过 |
| **v1.0.0** | Phase 1+2 完成 | 全部 Task 通过 + 性能达标 |

---

## 快速启动

```bash
cd /Volumes/zt_hd/projects/meow/memhop
pip install -e ".[dev]"

# 验证骨架
python -c "
import memhop
db = memhop.open('test.db', encoder=memhop.EncoderConfig(mode='mock'))
db.remember('hello', meta={'layer': 'entity'})
print(db.stats)
db.close()
"

# 开发流程
# 1. 读 DESIGN.md 相关章节
# 2. 按上面 Task 执行
# 3. pytest tests/ -v 验证
```

---

> 🐱 文档维护：Zhen · 工程督导 | 最后更新 2026-05-19
