# MemHop Benchmark 准确率分析报告

> 生成时间: 2026-06-01 23:45
> MemHop v0.13.0 | BGE-M3 (CPU) | DeepSeek Judge
> 当前状态: 全量 benchmark 运行中（已完成: LoCoMo ×2）

---

## 一、总览

| 数据集 | 已完成结果 | 预期正常范围 | 当前偏差 |
|--------|:----------:|:-----------:|:--------:|
| LoCoMo retrieval | **accuracy 6.9%** | 30-70% | ❌ 严重偏低 |
| LoCoMo associative | **accuracy 6.4%** | 30-70% | ❌ 严重偏低 |
| BEIR-nfcorpus | **NDCG@10=0.584** (subset=10) | 0.30-0.60 | ✅ 正常 |
| LME-S | 未出 | 0.40-0.70 | ⏳ |
| DMR | 未出 | 40-80% | ⏳ |
| C-MTEB ×8 | 未出 | 依任务各异 | ⏳ |

---

## 二、已确认的根因

### 1. LoCoMo — 会话日期元数据未存入文本（严重）

**根因：** `locomo_adapter.py` 只存了对话文本，没有把 `session_N_date_time` 元数据拼入文本。

**示例：**

```
Session_1_date_time: "1:56 pm on 8 May, 2023"
turn[2]: "[Caroline] I went to a LGBTQ support group yesterday..."

问题: "When did Caroline go to the LGBTQ support group?"
答案: "7 May 2023"  ← 需要推理: "yesterday" + "8 May 2023" = "7 May 2023"
```

存储的文本 `"[Caroline] I went to a LGBTQ support group yesterday..."` 中不包含日期信息。recall 虽能匹配到该 turn，但 LLM judge 收到的 context 没有日期上下文，**无法确认答案 → 判 0 分**。

**影响定量：**

| 类别 | 占比 | 答案是否在文本中 | 当前状态 |
|:---:|:---:|:---|:---|
| fact（事实） | 14.2% | 直接出现 ✅ | 基本正确 |
| temporal（时间） | **16.2%** | 需日期推理 ❌ | **全部失败** |
| reasoning（推理） | 4.8% | 需多轮推理 | 部分失败 |
| multi-hop（多跳） | **42.3%** | 分散在多轮 | 大部分失败 |
| cross-session（跨会话） | **22.5%** | 跨 session | 大部分失败 |

**修复方案：** 在 `load_locomo_dataset()` 中将 session 日期前缀拼入每个 turn 文本：

```python
# 当前
text = f"[{speaker}] {turn_text}"
# 修复
date_str = conv.get(f"{sk}_date_time", "")
text = f"[{date_str}] [{speaker}] {turn_text}"
```

**预期收益：** temporal 类从 ~0% → ~50%+，总体 accuracy 从 6-7% → **15-25%**。

**局限：** multi-hop（42.3%）和 cross-session（22.5%）需要跨多轮推理，即使有日期前缀，单轮 recall 仍然不够。这部分需要 Dream 压缩后的知识树、context 激活等 MemHop 特色机制来改善。

### 2. LoCoMo — 多轮推理的固有局限

即使修复了日期问题，LoCoMo 仍有 67% 的查询（reasoning + multi-hop + cross-session）需要理解跨多轮对话的信息。

**当前 benchmark 的 recall 模式**：对每个 query 做一次向量检索，返回 top-5 最相似的独立 turn。这种"一轮召回"方式对需要综合多轮信息的查询无能为力。

**可能的改善方向：**
- Dream consolidation 后提取的知识应能帮助 multi-hop 问题
- Context activation 会将相关上下文同时激活
- 但当前 benchmark 的 evaluation 流程没有专门测试这些特性

### 3. BEIR-nfcorpus — 已有正常结果

subset=10 的测试显示 NDCG@10=0.584，在 10 个文档的子集上合理。全量 benchmark 的结果尚未产生。

**无已知问题。**

---

## 三、待验证的潜在问题

以下数据集尚未完成，基于代码分析列出的潜在风险：

### 4. LME-S（LongMemEval-S）

**评估方式：** IR 指标（NDCG@10, MRR, Recall@k），使用 `aggregate_metrics()`。

**潜在风险：**

| 风险点 | 严重度 | 说明 |
|--------|:-----:|------|
| `_id_map` 是否完善 | 高 | 如果 `perceive()` 返回的 engram_id 没有被正确映射到 doc_id，搜索结果会被过滤掉 |
| qrels 模式匹配 | 高 | 同 LoCoMo，qrels 包含 session 级和 turn 级两种，使用错误则全零 |
| 数据预加载 | 中 | LME 数据由 `download_data.sh` 下载，文件结构需确认 |
| Dream 间隔 | 中 | dream_interval=50，文档数少时 Dream 次数不足 |

**需在报告产生后核验：** 确认 qrels 匹配正确，`_id_map` 非空。

### 5. DMR（Deep Memory Retrieval / MSC）

**评估方式：** LLM judge accuracy，使用 `DeepSeekJudge.evaluate_answer()`。

**潜在风险：**

| 风险点 | 严重度 | 说明 |
|--------|:-----:|------|
| **LLM judge 调用的高耗时** | 高 | DMR 用 LLM judge 评估每个查询（类似 LoCoMo），会大幅延长测试时间 |
| DeepSeek API 限频/超时 | 中 | run_benchmark.py 第 343 行有 try/except，失败则 score=0 |
| 上下文裁剪 | 中 | 只传 top-5 recalled texts 作为 context，多跳问题 context 不足 |
| 问题缓存存在性 | 中 | DMR 适配器可能生成/缓存问题，需确认离线缓存是否有有效数据 |

**注意：** DMR 是 5 个数据集中最复杂的，涉及多轮对话、人物画像、长程记忆。即使 recall 正常，LLM judge 也需要能理解时间线。

### 6. C-MTEB（中文多任务 Embedding Benchmark）

**评估方式：** 8 个子任务（T2Retrieval, MMarcoRetrieval 等），每个任务 IR 指标。

**潜在风险：**

| 风险点 | 严重度 | 说明 |
|--------|:-----:|------|
| 中文 embed 质量 | 高 | BGE-M3 支持中文，但 Candle CPU 推理的量化精度可能影响检索质量 |
| 数据量极大 | 高 | C-MTEB 每个子任务可能有数万到数十万文档，全量跑需数小时 |
| 跳过 associative 模式 | 低 | 已正确处理（`_dataset_modes()` 中为 nfcorpus/c-mteb 只跑 retrieval） |
| 跨任务 agent_id 隔离 | 中 | 每个子任务用 `{agent_id_base}_{task_name}`，确认 `_id_map` 在每个子任务前重置 |

---

## 四、跨数据集的系统性问题

### 7. BGE-M3 CPU 推理延时的连锁影响

所有 5 个数据集都受 BGE-M3 CPU 推理延时影响：

| 操作 | 平均耗时 | 全量 benchmark 影响 |
|------|:-------:|:------------------:|
| 单次 encode（store） | ~250ms | locomo 5882 文档 = 24 分钟 |
| 单次 encode（recall） | ~175ms | locomo 1986 查询 = 6 分钟 |
| 全部 5 数据集合计 | — | **预计 5-8 小时** |

**这与准确率无关**，但意味着整套 benchmark 产出效率低，多次迭代调试的成本高。

### 8. LLM Judge 作为评估手段的局限性

LoCoMo 和 DMR 都使用 LLM Judge（DeepSeek）作为评估手段：

```
prompt: "Does the context contain enough information to produce the expected answer?"
response: "YES" → score=1.0 | "NO" → score=0.0
```

**问题：**
- 二值评估（0/1）过于严苛，部分正确的情况也得 0 分
- LLM API 调用失败（网络/限频）静默返回 0 分
- 调用 1986 次 API 的高延时 + 失败风险

**改善建议：** 增加重试机制 + 使用 F1 作为 fallback（已有 `aggregate_locomo_f1`）时调低阈值。

### 9. 单轮 Recall 的固有局限

当前 benchmark 对所有数据集都使用单一 `runner.search()` 调用，没有充分利用 MemHop 的多轮 / 上下文激活 / Dream 特性：

| MemHop 特性 | benchmark 是否使用 | 说明 |
|:-----------|:----------------:|:----|
| 单轮 store + recall | ✅ | 基础流程，全部使用 |
| **Dream consolidation** | ✅ 但不充分 | dream_interval=50，对 eval 结果影响未被检验 |
| **Context activation** | ❌ | `search()` 未传 `context_id` |
| **knowledge_memories** | ❌ | 日志显示 always 0，因为知识需要 Dream 后才有 |
| **worldview filter** | ❌ | 未调用 |
| **tree_id / tree_path** | ❌ | 未使用 |

这意味着当前 benchmark 主要测试的是 MemHop 的**基础向量检索能力**，而非其核心差异化的**联想记忆 / 纠缠图 / 知识树**能力。

---

## 五、Dream 触发策略

### 当前实现

- `--dream-interval N`：每存储 N 个文档触发一次 Dream（默认 50）
- `--dream-timeout N`：每 N 秒触发一次 Dream（新增，默认 0=禁用）
- 两种模式可同时使用（任一条件满足即触发）

### 使用建议

| 场景 | 推荐配置 | 理由 |
|:----|:--------|:-----|
| 批量导入历史数据 | `--dream-timeout 300` | 时间维度更可控 |
| 多数据集对比 | `--dream-interval 50` | 统一按数据量触发 |
| 生产环境 | `--dream-timeout 300` | 用户无感知，后台定期整理 |

---

## 六、NDCG 结果解读

### BEIR-nfcorpus subset=10：NDCG@10=0.584

这个结果**正常**，原因：

1. **subset=10 意味着只有 10 个文档、14 个查询**
2. 在小样本下 NDCG 波动大，0.584 是合理值
3. BEIR-nfcorpus 官方 SOTA 使用 BGE 模型约 **NDCG@10=0.33**
4. 全量跑（3633 文档、323 查询）时数值会下降并稳定

需要等全量 nfcorpus 报告出来后再判断是否需要优化。

### 要提升 NDCG 可尝试的方向

| 方向 | 说明 | 预期提升 |
|:----|:-----|:-------:|
| 开启 Cross-Encoder reranker | 需要 ONNX reranker 模型文件 | +5-10% |
| 降低 limit (5→3) | top-3 比 top-10 更精确 | +2-5% |
| 使用 dual-encoder 多编码器融合 | BGE-M3 + miniLM 双编码 | +3-8% |
| 增加 Dream 频率 | 知识树更完善后辅助检索 | 不确定 |

---

## 七、核心问题：这些只是 benchmark 适配问题吗？

**不是。** 低分是三个层面的问题叠加：

### 层面 1：Benchmark 适配 bug ✅ 可修复

**LoCoMo 日期缺失** — 数据预处理没把 session 日期拼入文本。这是明确的适配 bug，改一行代码就能修。修复后 temporal 类查询（16.2%）会大幅改善。

### 层面 2：Benchmark 设计缺口 ⚠️ 需扩展

当前 benchmark 只测试了 MemHop 的**基础向量检索**能力。MemHop 的核心差异化能力——联想记忆（EntangleGraph）、知识树、上下文激活、Worldview 过滤——**完全没有被测试到**。

这好比用"能不能播放 CD"来评估一辆特斯拉。向量检索只是 MemHop 最基础的 L0 能力，不是它的核心卖点。

**Missing items:**
- Dream 后 knowledge_memories 的检索评估
- context_id 过滤是否能减少噪声
- 纠缠图能否提升跨主题 recall
- Worldview 冲突检测的质量

### 层面 3：实际的检索质量 🔧 可优化

即使修复了所有适配问题，当前 BGE-M3 CPU 的基础向量检索质量受限于：
- Candle CPU 推理的量化精度（与 ONNX GPU 对比，可能有 ~2-5% 退化）
- 单编码器（BGE-M3 1024 维）未使用 Cross-Encoder 精排
- 缺乏 LLM 重排

### 结论

| 问题 | 占比影响 | 类型 | 修复难度 |
|:----|:-------:|:---|:-------:|
| LoCoMo 日期缺失 | 16% 查询全错 | 适配 bug | 1 行 |
| 多跳推理支撑不足 | 67% 查询受限 | 设计缺口 | 需扩展 benchmark |
| BGE-M3 CPU 量化 | ~2-5% 精度损失 | 实际质量 | 可选优化 |
| 基础向量检索局限 | 所有查询 | 实际质量 | 工程迭代 |
| 未测 MemHop 特色 | — | 设计缺口 | 需新增测试 |
