# MemHop 基准测试集成计划

**日期**: 2026-05-31
**目标**: 覆盖主流记忆评测标准，适配 MemHop 每轮存储模型

---

## 一、基准测试全景

### 1.1 当前已有的

| 基准 | 类型 | 数据 | 适配器 | MemHop 适配状态 |
|------|------|------|--------|----------------|
| **LoCoMo** | 长对话记忆 | ✅ `locomo10.json` | ✅ | ⚠️ 需适配每轮 perceive |
| **LongMemEval-S** | 5 种记忆能力 | ❌ 数据缺失 | ✅ `lme_adapter.py` | ⚠️ 需适配 |
| **DMR** | 对话记忆检索 | ✅ `dmr_questions.json` | ✅ | ⚠️ 需适配 |
| **BEIR nfcorpus** | 英文检索质量 | ✅ 已下载 | ✅ `beir_adapter.py` | ✅ 标准检索 |
| **C-MTEB** | 中文检索质量 | 自动下载 | ✅ `c_mteb_adapter.py` | ✅ 标准检索 |

### 1.2 需要补充的行业标准

| 基准 | 来源 | 测试内容 | 数据获取 |
|------|------|---------|---------|
| **LongMemEval-V2** | ACL 2026 | 静态状态/动态追踪/工作流知识/环境陷阱/前提意识 (451 queries) | HuggingFace `LongMemEval-V2` |
| **LMEB** | arXiv 2025 | 长周期记忆嵌入质量 | 公开下载 |
| **MEMOLAND** | 多智能体 | 智能体在模拟环境中的记忆表现 | 公开 |
| **EverOS 统一框架** | EverMind | LoCoMo + LongMemEval 合并管道 | 方法论参考 |

---

## 二、MemHop 每轮适配要点

MemHop 和传统 RAG 系统的关键区别：

```
传统 RAG:
  index(docs) → 批量存储 → search(query) → 检索

MemHop (v0.12.0):
  for each turn:
    perceive(text, session_id) → 每轮独立存储 + 上下文跟踪
  recall(query, session_id) → 感知检索 + 主动上下文 + 书架附带
```

### 适配要点

**1. 数据注入方式**

```python
# 当前 (批量):
for doc in docs:
    runner.index(doc)  # 一次 store

# MemHop 适配 (每轮):
for doc in docs:  # docs 按会话/时间顺序排列
    output = mcp.perceive(
        text=doc["text"],
        session_id=doc["session_id"],
        turn_id=doc["turn_id"],
        turn_index=doc["turn_index"],
        # 忽略 warmup: warmup_rounds=0
        # 跳过主动上下文缓存
    )
```

**2. Warmup 处理**

Benchmark 应设置 `warmup_rounds=0` 跳过暖场，或保证测试数据量超过 warmup 阈值。

**3. Session 隔离**

MemHop 用 `session_id` 区分不同对话。Benchmark 中每个对话应有唯一 `session_id`。

**4. Recall 参数**

```python
# 当前:
mcp.recall(query, limit=10)

# MemHop 适配:
mcp.recall(
    query,
    session_id=sid,
    limit=10,
    attach_knowledge=False,  # 书架附带关闭
    mode="retrieval",         # 纯检索模式
)
```

**5. Bedrock 评估**

检索结果的 `score` 字段直接用于 NDCG/Recall 计算，和标准检索一致。

---

## 三、基准测试矩阵

| 基准 | 数据状态 | 编码器 | 测试模式 | 指标 | 优先级 |
|------|---------|--------|---------|------|--------|
| **BEIR nfcorpus** | ✅ | bge-small-en/bge-base-en | retrieval | NDCG@10, Recall@5 | P0 |
| **C-MTEB** | 自动下载 | bge-small-zh/bge-base-zh | retrieval | NDCG@10 | P0 |
| **LoCoMo** | ✅ | 全部 | retrieval + associative | LLM-judge accuracy | P0 |
| **LongMemEval-S** | ❌ 需下载 | 全部 | retrieval + associative | 失忆率、幻听率 | P1 |
| **DMR** | ✅ | 全部 | retrieval | 对话检索精度 | P1 |
| **LongMemEval-V2** | ❌ 需下载 | bge-m3 | retrieval | 5 类能力准确率 | P2 |
| **LMEB** | ❌ 需下载 | bge-m3 | retrieval | 嵌入质量 | P2 |

---

## 四、执行计划

### 阶段 1: 修复现有 + 验证核心（P0）

```
1. 下载 LongMemEval-S 数据（找公开源或生成）
2. 修复 lme_adapter 确保兼容 v0.12.0 的 perceive 返回格式
3. 运行 BEIR nfcorpus + C-MTEB 验证编码器质量
4. 全编码器跑 LoCoMo 基准
```

### 阶段 2: 每轮适配 + 自动下载（P1）

```
5. 修改 run_locomo / run_lme_s:
   → 每轮 calls perceive (代替批量 index)
   → 设置 warmup_rounds=0
   → 关闭 attach_knowledge
   → 每个 session 独立
6. 添加 BEIR / C-MTEB 自动下载脚本
7. 验证 LoCoMo 结果可复现
```

### 阶段 3: 扩展新基准（P2）

```
8. 添加 LongMemEval-V2 适配器
9. 添加 LMEB 适配器
10. 全模型对比报告生成
```

---

## 五、和 EverOS 统一框架的对标

EverOS 的评测框架（来自他们的博客）：
- 平台: EverOS, Mem0, MemOS, Zep, MemU
- 数据集: LoCoMo + LongMemEval
- 评估: GPT-4.1-mini 统一评分
- MemHop 定位: 本地嵌入式（vs 云端 API）

MemHop 的差异化优势：
- 纯 Rust，毫秒级延迟（vs 云端秒级）
- 每轮上下文自动管理（vs 手写 prompt 策略）
- Candle 全离线（vs 依赖 LLM API）
- 评测时用 `warmup_rounds=0` 消除暖场影响
