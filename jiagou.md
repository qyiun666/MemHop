# MemHop 架构设计文档 (v0.18.1)

## 1. 系统定位

MemHop 参考真实人脑记忆机制，专为 Agent 设计的仿人脑记忆系统。核心目标：

- **高效检索**：BM25 + HNSW 双通道 RRF 融合，毫秒级定位上下文
- **多场景上下文召回**：通过一次会话，检索出不同场景所需的上下文
- **语义理解**：双编码器路由 (中文 BGE + 英文 BGE)，真正理解语义而非字面匹配

## 2. 系统特性

- **无状态模式**：一台设备只能运行一个 memhop 进程
- **中英双语编码器**：intfloat/multilingual-e5-small (384维, ~118MB)，单模型处理中英双语
- **HNSW 向量索引**：O(log N) 近似搜索，替代全扫描 O(N)
- **语义去重**：写入时自动检测重复记忆 (cosine > 0.95 + ngram Jaccard > 0.8)

## 3. 五层记忆架构

所有层均包含时间戳，统一使用 **13 位毫秒级时间戳**。

```
L0  画像层（Agent Profile）
L1  纠缠图层（Entanglement Graph）  ← HNSW 向量索引 + BM25
L2  上下文层（Context Graph）       ← HNSW centroid 索引 + ngram
L3  知识图层（Knowledge Graph）     ← HNSW 向量索引 + ngram (v0.18.0 优化)
L4  原文层（Raw Archive）           ← HNSW 向量索引 + ngram (v0.18.0 优化)
```

### L0 — 画像层

当前 Agent 的画像，包括：姓名、性格、三观、世界观等。
通过 dream 机制更新。

### L1 — 纠缠图层

- 只有 **1 个超图结构**
- 通过 Dream 机制将多个 L2 记忆关联
- 目标：使每个 Agent 的人格**独一无二**
- v0.18.0：HNSW 向量索引 + BM25 双通道 RRF 融合检索，importance 加权

### L2 — 上下文层

- **多个单图结构**，独立存在
- 将不同场景、不同事件的聊天记录上下文单独串联
- 每个上下文可对应多个 L3，并索引到 L4
- 用户对话与 Agent 回复为一组
- v0.18.0：centroid 向量 + ngram 双通道 RRF 融合

### L3 — 知识图层

- **多个超图结构**，独立存在
- 可根据目标路径或 L2 内容生成不同的图结构
- v0.18.0：新增 HNSW 向量索引，支持 dense cosine 检索通道

### L4 — 原文层

存放聊天记录原文，记录用户与 Agent 的完整对话。
v0.18.0：新增 HNSW 向量索引，支持 dense cosine 检索通道

## 4. 编码器

v0.18.0 使用 `intfloat/multilingual-e5-small` 单一中英双语编码器：

- 架构: MiniLM (12层, 384维)
- 大小: ~118MB
- 单个模型处理中文和英文（无需语言检测路由）
- 统一的 384 维向量，避免跨语言 cosine 偏差
- 默认启用（feature-gated, 可通过 `--no-default-features` 禁用）
- 回退 NgramEncoder（无需模型文件）

## 5. HNSW 向量索引

v0.18.0 使用 usearch crate 实现 HNSW (Hierarchical Navigable Small World) 索引：

- Metric: Cosine (余弦相似度)
- Quantization: f32
- Connectivity: 16
- 搜索复杂度: O(log N)
- 序列化: 自定义格式 [magic][id_map][usearch buffer] 支持 LMDB 持久化

## 6. Dream 功能

由 Agent 层触发，执行 7 阶段巩固管线：

1. NREM — 超边权重时间衰减 + 剪枝
2. REM — 话题合并
3. REM — 话题反思
4. REM — 计划压缩
5. 共现分析 — L2→L1 跨话题超边
6. L0 形成 — 从 L2 提取世界观/价值观
7. 索引重建 — BM25 + HNSW

## 7. 每轮对话处理流程

```
对话进入 memhop
  ├── 有 L3 标识 → 通过 L3 标识找到 L2 → 定位具体内容
  ├── 无 L3 标识 → 检索 L2 → 找到对应内容
  └── 有选中 L2 标识 → 直接找当前 L2 的对应内容
```

## 8. 记忆激活机制

- 返回的 L2 进入**已激活队列**
- 每次对话进入 memhop 时，**优先校验已激活队列**是否存在匹配的上下文
- 若无匹配，按原流程检索
- 已激活的上下文设有**倒计时**，超时自动取消激活
- v0.18.0: Agent 可通过 `memhop_feedback` 调整激活 TTL

## 9. Memory Dedup (v0.18.0)

写入 L1 前自动检测语义重复：

1. 用 HNSW 搜索 top-5 候选
2. 如果 cosine similarity > 0.95 **且** ngram Jaccard > 0.8 → 视为重复
3. 重复时更新已有节点 (version++, keywords 更新)，不创建新节点

## 10. Importance Scoring (v0.18.0)

每条记忆可附带 importance (0.0-1.0，默认 0.5)：

- 检索时 `score *= importance` 加权
- 高重要性记忆在排序中优先
- Agent 层应标记关键事实为高 importance

## 11. Time-aware Retrieval (v0.18.0)

可选的时间衰减：`score *= exp(-λ * hours_since_creation)`

- λ = 0.001 ≈ 42 天半衰期
- λ = 0 → 不衰减 (默认)
- 在所有通道融合后应用，重新排序

## 12. 长文本处理

每轮对话内容过长时：

1. 按段落或内容语义切断 (splitter)
2. 分批编码和检索
3. 合并结果

## 13. 多结果置信度

v0.18.0 改进为三因子模型：

- 双通道一致性 (40%)：top-1 和 top-2 分数差距越小越一致
- 结果数量因子 (30%)：结果越多越有信心
- 最高分绝对值 (30%)：top score 越高越有信心

返回多个 L2 时，由 Agent 层 LLM 进行最终判断。

## 14. 对外接口

| 接口             | 说明                                                                              |
| ---------------- | --------------------------------------------------------------------------------- |
| **对话接口**     | 每轮对话进入，返回 L0 + L2 上下文 + L3 位置。支持 importance + time_decay         |
| **Dream**        | 由 Agent 层触发：7 阶段巩固管线                                                  |
| **Feedback**     | **新增**：Agent 反馈 recall 结果是否相关，调整激活权重                            |
| **Stats**        | **新增**：返回记忆统计、编码器模式、各层节点数                                    |
| **已激活上下文** | 返回当前 L2 层已激活的上下文列表                                                  |
| **L3 路径**      | 返回 L3 知识图路径                                                                |
| **L4 原文**      | 返回 L4 对话原文                                                                  |
| **更新接口**     | Agent 实际操作内容和回复，更新 L2、L3、L4                                         |
| **重新适配**     | Agent 通过 LLM 判断返回的 L2 是否正确，若不正确则 memhop 重新适配                 |

## 15. Agent 层配合清单

除 Dream 外，以下功能需要 Agent 层配合：

| 功能 | Agent 需要做什么 |
|------|-----------------|
| Store | 传入 `llm_keywords`、`llm_compressed_summary`、`topic_label`、`importance` |
| Re-search | LLM 判断 recall 结果不正确后调 re_search |
| Feedback | LLM 判断 recall 结果相关性后调 feedback |
| Confidence | LLM 利用 confidence 分数做最终判断 |
| Profile | 初始化 L0 画像（role, personality, values） |
