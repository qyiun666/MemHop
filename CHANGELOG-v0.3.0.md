# MemHop v0.3.0 — MeowAgent 更新说明

> **版本**: v0.2.0 → v0.3.0  
> **升级方式**: `pip install --upgrade memhop`  
> **兼容性**: 完全向后兼容，所有现有代码无需修改

---

## 一句话总结

**限定范围、硬过滤时间、软偏置时间、并发读写、打通链路。现在你可以告诉 MemHop "只在这棵树里找，只看昨天的"，"最近发生的优先"，"多个 recall 同时跑不排队"，"顺着 entity 找回原文"。**

---

## MemHop 定位（再确认）

MemHop 是纯记忆存储与检索引擎，**不含任何提示词、LLM 调用、对话逻辑**。

```
用户对话
  → MeowAgent (LLM + prompt + 工具调用)
    → MemHop.remember()  ← 存记忆
    → MemHop.recall()    ← 检索记忆（出数据）
    → MemHop.search()    ← 元数据过滤
    → LLM (用检索结果 + prompt 组织回复)
```

MemHop 只负责把相关的记忆数据给你，怎么用是你的事。

---

## 零改动可用（向后兼容）

所有现有 API 签名不变。升级后代码不动，直接受益：
- 启动速度提升（close→reopen 几乎瞬间）
- recall 延迟降低（多核并行）
- 内存分配减少

---

## 核心新增

### 1. recall scope — 限定检索范围

应用层可视化了多个知识树，用户选中一个发起对话 → MemHop 只在该范围内找。同时支持**时间范围硬过滤**——用户问"昨天早上吃了啥"时，只找昨天的记忆，不碰三个月前的。

```python
# 限定在一个 domain
db.recall("支付bug", scope={"domain": "code_project_alpha"})

# 限定在一个知识树
db.recall("模块结构", scope={"knowledge_tree": "k_root"})

# 限定层
db.recall("bug", scope={"layer": "entity"})

# 多选
db.recall("bug", scope={"domains": ["project_a", "project_b"]})

# ── 时间范围硬过滤 ──

# 最近 48 小时内
db.recall("吃了什么", scope={"time_range": {"hours": 48}})

# 昨天到今天
db.recall("吃了什么", scope={"time_range": {"days": 1}})

# 精确时间范围
db.recall("吃了什么", scope={
    "time_range": {
        "after": "2026-05-19T00:00:00",
        "before": "2026-05-20T00:00:00"
    }
})

# 组合: 昨天、code domain、entity 层
db.recall("支付bug", scope={
    "domain": "code",
    "layer": "entity",
    "time_range": {"hours": 24}
})

# 不传 scope = 全局（旧行为，默认）
db.recall("bug")  # 等价于 scope=None
```

`time_range` 和 `time_alpha` 的区别：
| 机制 | 作用 | 场景 |
|------|------|------|
| `scope.time_range` | **硬过滤**：只参与某段时间内的记忆 | "昨天吃了啥" — 精准时间限定 |
| `time_alpha`（见下文） | **软偏置**：所有记忆可参与，近的多加分 | "吃了啥" — 没明说时间但最近优先 |

两者独立可组合。

### 2. 时间感知召回（软偏置）

recall 不仅看语义，还看时间——最近发生的事更容易被想起来。这是**软偏置**（不影响候选范围），与上面的 `scope.time_range` 硬过滤独立。

```python
# 最近发生的优先（time_alpha 控制时间权重）
db.recall("早餐吃了什么", time_alpha=0.05)

# time_alpha=0 = 不看时间（旧行为，默认）
db.recall("那个bug怎么修的", time_alpha=0.0)

# 硬过滤 + 软偏置组合（"昨天吃了啥"的完整解法）
db.recall("吃了什么",
    scope={"time_range": {"hours": 48}},  # 硬过滤：只看48h内
    time_alpha=0.05                        # 软偏置：近的优先
)
```

`time_alpha=0.05` 时：昨天的记忆比一年前的多加 18 分（360 天 × 0.05），在 Hopfield 竞争中天然优先。

### 3. 重要性加权召回

```python
# 重要记忆更容易被召回
db.recall("bug", importance_alpha=0.3)
```

`scope` + `time_alpha` + `importance_alpha` 可以任意组合：
```python
db.recall("支付bug", 
    scope={"domain": "code"},
    time_alpha=0.05,
    importance_alpha=0.3
)
```

### 4. 跨层链路 — 人脑管道

```python
# entity 指向关联的 episode/knowledge
db.links_to("e_001")     # → [Memory(id="ep_042", ...), ...]

# 哪些记忆指向了这条
db.links_of("ep_042")    # → [Memory(id="e_001", ...), ...]
```

### 5. 多线索融合

```python
db.recall_fuse(["支付", "空指针", "昨天"], weights=[1.0, 0.8, 0.5])
# 三条线索融合成一个 query vector，一次 recall
```

### 6. 重要性自动衰减

```python
db.remember("今天吃的豆浆油条", meta={
    "importance": 0.9,
    "importance_decay_rate": 0.97  # 30天后 importance 降至 40%
})
```

旧记忆自动降温，让 Hopfield 吸引子不被"过期信息"污染。

### 7. RwLock 并发读写

引擎内部锁从 `Mutex` 升级为 `RwLock`：多个 `recall()` 可以**同时执行**，不再串行排队。Python GIL 在 Hopfield 密集计算期间主动释放，避免阻塞其他线程。

```
# v0.2.0 (Mutex):
线程A: recall() → 等锁 → 计算(30ms) → 释放
线程B: recall() → 等锁... 等锁... 等锁... → 计算 → 释放  ← 白等

# v0.3.0 (RwLock):
线程A: recall() → 读锁 → 计算(30ms, GIL释放中) → 释放  ← 并发
线程B: recall() → 读锁 → 计算(30ms, GIL释放中) → 释放  ← 同时
线程C: remember() → 写锁(等读完) → 写入 → 释放  ← 合理排队
```

多线程 Agent 场景：background 存记忆不阻塞 frontend recall，所有读操作零锁竞争。无需应用层修改代码。

---

## 新 API 一览

```python
# ── 限定范围 ──
db.recall("bug", scope={"domain": "code"})

# ── 时间范围硬过滤 ──
db.recall("吃了什么", scope={"time_range": {"hours": 48}})
db.recall("吃了什么", scope={"time_range": {"days": 1}})

# ── 时间软偏置 ──
db.recall("吃了什么", time_alpha=0.05)

# ── 综合召回 ──
db.recall("支付bug", 
    scope={"domain": "code", "time_range": {"hours": 24}}, 
    time_alpha=0.05, 
    importance_alpha=0.3
)

# ── 多线索 ──
db.recall_fuse(["支付", "空指针", "昨天"])

# ── 跨层链路 ──
db.links_to("e_001")
db.links_of("ep_042")

# ── 可视化接口（给应用层造 UI） ──
db.entity_graph("e_001", depth=2)         # entity 子图
db.knowledge_tree("k_root")               # knowledge 子树
db.episode_thread("s_007")                # 对话线程（按时间排列）
db.memories_by_layer("entity", limit=20)  # 按层分页

# ── 增强统计 ──
db.stats
# → {
#     "total_memories": 2600,
#     "layer_counts": {"entity": 100, "knowledge": 500, "episode": 2000},
#     "dormant_count": 1500,
#     "protected_count": 50,
#     "permanent_count": 20,
#     "age_distribution": {"<1d": 10, "1d-7d": 200, ...},
#     "avg_importance": 0.62,
#     ...
#   }
```

---

## 推荐使用模式（人脑管道）

```python
# 1. 收到对话 → 存原文 (episode)
ep_id = db.remember(
    "用户: 支付报错\nAI: 看看...发现是空指针",
    meta={
        "layer": "episode",
        "session_id": "s_007",
        "importance": 0.3,              # 原文优先级低
        "importance_decay_rate": 0.97    # 30天后降至40%
    }
)

# 2. LLM 提取关键信息 → 存 entity
bug_id = db.remember(
    "支付模块空指针bug",
    meta={
        "layer": "entity", "type": "bug", "domain": "code",
        "importance": 0.9,              # 关键信息优先级高
        "links": [{"to": ep_id, "relation": "extracted_from"}]
    }
)

# 3. 日常查询：命中 entity（限定范围+时间硬过滤+时间软偏置）
mem = db.recall("支付bug", 
    scope={"domain": "code", "time_range": {"days": 30}},
    time_alpha=0.05
)
# → Memory("支付模块空指针bug", confidence=0.92)

# 4. 需要详细上下文：沿 links 回溯原文
context = db.links_to(mem.id)
# → [Memory("用户: 支付报错\nAI: 看看...发现是空指针", ...)]
```

---

## 关于编码器

MemHop 唯一编码器为 **ngram 哈希（字符 n-gram）**：
- 零模型，0MB 内存，<0.1ms 编码
- 中文 ngram 覆盖率 90%+（"早餐"/"早饭"/"早点" 天然共享 ngram）
- 不需要下载任何模型

**不引入 BGE-M3 或其他 embedding 模型** —— 中文场景 ngram 已足够好用，300MB 模型上线会让 MacBook 8GB 用户崩溃（大模型 + MemHop + embedding = 内存不够）。

---

## 性能提升

| 指标 | v0.2.0 | v0.3.0 | 提升 |
|------|--------|--------|:---:|
| 启动时间 (100K) | ~1-3s | < 100ms | 10-30× |
| recall 延迟 (10K) | ~30ms | ~8ms | 3-4× |
| recall 延迟 (100K) | ~100ms | ~25ms | 4× |
| Python 内存分配 (per recall) | ~8KB | ~4KB | 2× |
| 并发 recall | 串行 (Mutex) | 并发 (RwLock) | 读零等待 |

*测试环境: macOS Apple Silicon M系列*

---

## 三层记忆 — 概念对齐

| 概念 | 对应用法 | v0.3.0 关键增强 |
|------|---------|----------------|
| **纠缠图** (entity) | 概念、事物、bug —— 记忆的节点 | `scope` 限定域+时间、`links_to` 打通回溯、`entity_graph()` 可视化、RwLock 并发读 |
| **知识树** (knowledge) | 代码结构、场景上下文 —— 选中后限定检索范围 | `scope={"knowledge_tree": "k_root"}`、`knowledge_tree()` 可视化 |
| **原文** (episode) | 对话原文 —— 低优先级存储，必要时回溯 | `importance_decay_rate` 自然淡忘、`links_of()` 反向查找 |

管道: episode (低优先级) → LLM 提取 → entity/knowledge → (scope 限定域+时间硬过滤 + time_alpha 时间软偏置 recall，RwLock 下多条并发检索) → links 回溯 → episode 原文

---

## 升级注意事项

1. **无破坏性变更**: 所有代码无需修改，升级后直接跑
2. **LMDB 兼容**: v0.2.0 的数据库文件直接可用
3. **首次启动**: 首次启动自动重建索引快照（仅一次），后续秒开
4. **原始 ngram**: 唯一编码器，无需额外安装，零依赖