# MemHop v0.11.0 统一记忆架构 — 路线图评估

**版本**：v1.0
**日期**：2026-05-29
**作者**：路径（Roadie）· 路线图规划师
**上游依赖**：PRD v1.0（析客）、用户研究（瑞思）、竞品分析（竞析）、性能评估（数析）

---

## 目录

1. [里程碑验证](#1-里程碑验证)
2. [工作量估算](#2-工作量估算)
3. [风险排序](#3-风险排序)
4. [Now / Next / Later 路线图](#4-now--next--later-路线图)
5. [MeowAgent 接入指南概要](#5-meowagent-接入指南概要)

---

## 1. 里程碑验证

### 1.1 PRD 现有拆分总览

析客在 PRD §10 中将 v0.11.0 拆为 5 个两周里程碑：

| 里程碑 | 周数 | 主题 | 核心交付 |
|--------|------|------|---------|
| M1 | W1-2 | 核心数据结构变更 | `EngramKind::Knowledge`、LMDB v2 schema、`EngramMeta` 扩展 |
| M2 | W3-4 | 存储层统一 | store() ADD-only、mount→batch store、unmount→forget_batch、HNSW delete API |
| M3 | W5-6 | recall 统一 + EntangleGraph | RecallResult 格式、跨类型边、recall filter、graph_associations |
| M4 | W7-8 | Dream + Hopfield 扩展 | Knowledge vitality 衰减曲线、Hopfield 权重参数、跨类型关联发现、knowledge_search 废弃映射 |
| M5 | W9-10 | MCP 适配 + 测试 | MCP 工具更新、集成测试、性能回归、MeowAgent 联调 |

### 1.2 独立性检查

逐里程碑验证：能否独立交付可验证的增量？

#### M1：核心数据结构变更 ✅ 可独立交付

- **交付物**：编译通过的 `EngramKind::Knowledge` 枚举、`KnowledgeMeta` struct、新的 LMDB schema（空库可创建）
- **可验证**：`cargo build` + 单元测试 + 新 LMDB 创建 → 写入一条 Knowledge engram → 读出验证
- **独立价值**：数据模型就位，为后续所有里程碑奠定基础
- **依赖**：无前序依赖

#### M2：存储层统一 ✅ 可独立交付

- **交付物**：store() 去重逻辑、mount_shelf 走批量 store、unmount_shelf 走 forget_batch、HNSW delete 实现
- **可验证**：mount_shelf(path) → 检查 LMDB 中有 Knowledge engram → recall 可检索到 → unmount → 确认删除
- **独立价值**：端到端 Knowledge 生命周期（mount → 存储 → recall → unmount）可跑通
- **依赖**：M1（数据结构）
- ⚠️ **关键风险**：依赖 HNSW delete API（当前 `brain.rs:1282` 跳过），若未就绪则 unmount 只能软删除

#### M3：recall 统一 + EntangleGraph ✅ 可独立交付

- **交付物**：RecallResult 格式、跨类型 EntangleGraph 边、filter 支持、graph_associations
- **可验证**：混合召回测试（Episode + Knowledge 同一查询返回）、filter 功能测试、图扩散返回非空 associations
- **独立价值**：统一 recall 体验完整——一次调用返回所有类型结果
- **依赖**：M2（Knowledge engram 已在 LMDB 中）

#### M4：Dream + Hopfield 扩展 ⚠️ 部分依赖 M3

- **交付物**：Knowledge vitality 独立衰减、Hopfield 权重参数、跨类型关联发现、knowledge_search 废弃
- **可验证**：Dream 运行后 knowledge_processed > 0、vitality 衰减速度验证、graph_associations 中出现 Knowledge↔Episode 边
- **独立价值**：记忆巩固覆盖全类型
- **依赖**：M2（Knowledge 在 LMDB 中）即可开始，但 "跨类型关联发现" 依赖 M3 的 EntangleGraph 跨类型边能力
- **建议**：M4 的 vitality 衰减 + Hopfield 权重部分可与 M3 并行开发；跨类型关联发现作为 M4 后段，等 M3 完成后接入

#### M5：MCP 适配 + 测试 ⚠️ 严格依赖 M1-M4

- **交付物**：MCP 工具清单更新、集成测试、性能回归、MeowAgent 联调
- **可验证**：完整链路 (mount → store → recall → dream → unmount) 通过
- **独立价值**：对外交付的完整产品
- **依赖**：全部前序里程碑
- ⚠️ **M5 不可并行**——必须等 M1-M4 全部完成

### 1.3 依赖图

```
M1 (数据结构)
 │
 ├──→ M2 (存储层统一) ──→ M3 (recall + 图) ──→ M5 (MCP + 测试)
 │                         │                    ↑
 │                         └──→ M4 (Dream) ─────┘
 │                              (vitality 部分可与 M3 并行)
```

### 1.4 发现的遗漏依赖

| # | 遗漏项 | 影响阶段 | 严重性 | 建议 |
|---|--------|---------|--------|------|
| D1 | **Encoder 批量编码 API** | M2 (mount_shelf) | 中 | mount 需要批量 embedding。当前 encoder 可能只支持单条。需在 M2 前确认或实现 `encode_batch(chunks) → Vec<Vector>` |
| D2 | **LMDB 批量写入事务** | M2 (mount_shelf) | 中 | 1000 chunks 逐个 LMDB 事务写入会很慢。需要设计批量写事务（单次 LMDB write transaction 内写多条 engram） |
| D3 | **EngramCache 去重范围设计** | M2 (store) | 低 | 析客建议"最近 1000 条"做去重检查。需确认这 1000 条是全局 FIFO 还是按 kind 分桶。建议全局 FIFO（简化实现），v0.11.1 优化 |
| D4 | **EntangleGraph 节点 ID 稳定** | M3 | 低 | Knowledge engram 通过 store() 写入时生成 engram_id。EntangleGraph 引用此 ID。需确保 ID 在 HNSW delete + re-store 场景下稳定 |
| D5 | **Hopfield max_patterns 驱逐策略** | M4 | 低 | 当 pattern 数超 `max_patterns` 时，驱逐哪些？建议按 vitality 升序驱逐（低 vitality 先出），与 Dream 衰减策略一致 |

### 1.5 M4 并行可行性

M4 的以下部分**不依赖 M3**，可在 M2 完成后立即开始：

- Knowledge vitality 独立衰减曲线实现
- Hopfield `knowledge_pattern_weight` 参数和条件分支
- DreamResult 扩展字段（`knowledge_processed`、`new_associations`）

M4 的以下部分**依赖 M3**：

- Dream 阶段跨类型关联发现（需要 M3 的 EntangleGraph CoShelf/Semantic 边）

**建议**：M4 与 M3 并行启动，先做 vitality + Hopfield 部分，等 M3 的 EntangleGraph 跨类型边就位后接入关联发现。

---

## 2. 工作量估算

### 2.1 估算方法

基于以下因素综合判断：
- 代码库规模：~23,000 行 Rust，模块化良好（shelf/、engine/、encoder/ 等子模块）
- LMDB + HNSW + Hopfield + EntangleGraph 四大核心已就绪
- PRD 改动集中在 Brain API 层、EngramKind 扩展、ShelfManager 重构
- 不兼容旧版，不需要迁移脚本（大幅降低工作量）

### 2.2 逐里程碑估算

#### M1：核心数据结构变更（W1-2）

| 任务 | 文件 | 估点 | 说明 |
|------|------|------|------|
| `EngramKind::Knowledge` 枚举变体 | `engram.rs` | 2 | 加变体 + 匹配分支 |
| `KnowledgeMeta` struct 定义 | `engram.rs` (或新文件) | 3 | 含 ShelfDomain、Confidence 枚举 |
| `EngramMeta` 扩展（通用字段） | `engram.rs` | 2 | created_at、last_activated、activation_count |
| LMDB schema v2 定义 | `storage.rs` | 3 | 新 sub-database 或 schema version byte |
| LMDB 版本检测 + 不兼容提示 | `storage.rs` | 2 | 检测旧库 → 明确错误信息 |
| `ShelfDomain` + `Confidence` 枚举 | `engram.rs` | 1 | 简单枚举 |
| 单元测试 | 各文件 | 3 | 序列化/反序列化、schema 创建/打开 |
| **小计** | | **16 点** | |

#### M2：存储层统一（W3-4）

| 任务 | 文件 | 估点 | 说明 |
|------|------|------|------|
| store() ADD-only 语义重构 | `engine/store.rs` | 5 | 去重逻辑 + 新 StoreResult 枚举 |
| 语义去重检查（EngramCache 最近 1000 条） | `engine/store.rs` | 3 | cosine 阈值判断 |
| mount_shelf → store 批量写入 | `shelf/mod.rs` + `engine/store.rs` | 5 | scanner → chunker → encoder → batch store 管线 |
| unmount_shelf → forget_batch | `shelf/mod.rs` + `engine/mod.rs` | 3 | 过滤器匹配 + 批量删除 |
| **HNSW delete API 实现** | `engine/tree.rs` 或 `index.rs` | **8** | 🔴 工作量最大的单项任务 |
| ShelfManager 重构（退化为元数据管理） | `shelf/mod.rs` | 5 | 移除 ShelfTree/HashMap，保留元数据 |
| 移除 ShelfTree 模块 | `shelf/tree.rs` | 1 | 删除文件 + 清理引用 |
| LMDB 批量写事务优化 | `storage.rs` | 3 | 单事务多 engram 写入 |
| Encoder 批量编码支持 | `encoder/mod.rs` | 2 | 若当前不支持 batch |
| 集成测试（mount → recall → unmount） | tests/ | 5 | |
| **小计** | | **40 点** | |

#### M3：recall 统一 + EntangleGraph（W5-6）

| 任务 | 文件 | 估点 | 说明 |
|------|------|------|------|
| RecallResult + RankedEngram + ScoreBreakdown 结构 | `engine/recall.rs` | 3 | 新返回类型 |
| recall 管线适配（不区分 kind） | `engine/recall.rs` | 5 | 单 HNSW 查询 → 排序 → 填充 shelf_context |
| recall filter 实现 (kind/shelf_id/domain) | `engine/recall.rs` 或新 `filter.rs` | 5 | HNSW 后过滤 + 重排 |
| EntangleGraph CoShelf 边自动创建 | `entangle_graph.rs` | 3 | mount 时同一 shelf 的 chunk 间建边 |
| EntangleGraph Semantic 边（跨类型） | `entangle_graph.rs` | 5 | 已有 Episode-Episode Semantic 边，扩展到 Knowledge |
| graph_associations 计算 + 人类可读描述 | `entangle_graph.rs` | 5 | 图扩散 + 描述生成 |
| ShelfContext 聚合去重 | `engine/recall.rs` | 2 | 顶层 shelf_contexts 去重摘要 |
| RecallMeta 统计填充 | `engine/recall.rs` | 2 | knowledge_hit_count 等 |
| 集成测试 | tests/ | 5 | |
| **小计** | | **35 点** | |

#### M4：Dream + Hopfield 扩展（W7-8）

| 任务 | 文件 | 估点 | 说明 |
|------|------|------|------|
| Knowledge vitality 独立衰减曲线 | `vitality.rs` | 3 | `knowledge_decay_rate = 0.015` |
| VitalityConfig 结构 + 可配置参数 | `vitality.rs` | 2 | episode_decay_rate, knowledge_decay_rate 等 |
| Hopfield knowledge_pattern_weight | `hopfield.rs` | 3 | 权重参数 + 条件分支 |
| Hopfield max_patterns 上限 + 驱逐 | `hopfield.rs` | 3 | 按 vitality 驱逐 |
| Dream 管线适配 Knowledge | `engine/mod.rs` 或 `cortex.rs` | 5 | dream() 遍历含 Knowledge engram |
| Dream 跨类型关联发现 | `cortex.rs` + `entangle_graph.rs` | 5 | Knowledge↔Episode 新关联 |
| DreamResult 扩展字段 | `engine/mod.rs` | 1 | knowledge_processed、new_associations |
| knowledge_search → recall 映射 + deprecation warning | MCP server 层 | 2 | |
| 单元测试 + 集成测试 | tests/ | 4 | |
| **小计** | | **28 点** | |

#### M5：MCP 适配 + 测试（W9-10）

| 任务 | 文件 | 估点 | 说明 |
|------|------|------|------|
| MCP 工具清单更新（7 个工具语义变更） | `memhop-mcp-server/` | 5 | store/recall/dream/mount/unmount/shelf_status/knowledge_search |
| RecallResult → MCP JSON 序列化 | MCP server | 3 | 嵌套结构序列化 |
| 集成测试（完整链路） | tests/ | 5 | mount → store → recall → dream → unmount |
| 性能回归测试（vs v0.9.0 基线） | benchmarks/ | 3 | recall p50/p99、store p99、mount 耗时 |
| MeowAgent 适配 + 联调 | — | 3 | 对接 MeowAgent 侧修改 |
| 错误处理 + 边界情况 | 各文件 | 3 | 空 Shelf、超大文件、HNSW 满等 |
| 文档更新 | docs/ | 2 | |
| **小计** | | **24 点** | |

### 2.3 总估点与分布

| 里程碑 | 估点 | 占比 | 周数 |
|--------|------|------|------|
| M1 | 16 | 11% | W1-2 |
| M2 | 40 | 28% | W3-4 |
| M3 | 35 | 24% | W5-6 |
| M4 | 28 | 20% | W7-8 |
| M5 | 24 | 17% | W9-10 |
| **总计** | **143 点** | **100%** | **10 周** |

### 2.4 关键路径分析

```
M1 (16pts, W1-2) → M2 (40pts, W3-4) → M3 (35pts, W5-6) → M5 (24pts, W9-10)
                                         ↘ M4 (28pts, W7-8) ↗
                                            (M4 vitality 部分可与 M3 并行)
```

关键路径：**M1 → M2 → M3 → M5**，总计 115 点（占 80%）。

**瓶颈在 M2**（40 点），其中 HNSW delete API 独占 8 点且不确定性最高。

### 2.5 单人力周产出评估

假设一个熟练 Rust 开发者全职投入（40h/周），每点 ≈ 2-4 小时（含测试），则：

- M1 (16pts)：~48h → 1.2 周 ✅
- M2 (40pts)：~120h → 3 周 ⚠️ **超出 2 周预算**
- M3 (35pts)：~105h → 2.6 周 ⚠️ **超出 2 周预算**
- M4 (28pts)：~84h → 2.1 周 ✅
- M5 (24pts)：~72h → 1.8 周 ✅

**结论**：M2 和 M3 有超时风险。建议：
- M2 的 HNSW delete API 若实现复杂度超预期，先用软删除（标记 + 过滤层）作为 fallback，降低 M2 工程量
- M3 的 EntangleGraph 跨类型边若复杂，首版只做 CoShelf 边（mount 时自动创建），Semantic 边推迟到 M4 Dream 阶段

---

## 3. 风险排序

### 3.1 风险矩阵（重排序）

基于 PRD §7.4 的 7 个风险和工程视角的补充分析，按**阻塞程度**重新排序：

| 排名 | 风险 | 概率 | 影响 | 阻塞什么 | 优先级 |
|------|------|------|------|---------|--------|
| 🔴 R1 | **HNSW delete API 实现复杂度高** | 中 | **极高** — unmount 无法完整清理 | M2 (unmount_shelf)、M5 (集成测试) | P0 |
| 🔴 R2 | **mount_shelf 批量写入性能不达标** | 中 | **高** — 1000+ chunks mount 超时 | M2 (mount_shelf)、M5 (SLA 验收) | P0 |
| 🟠 R3 | **Hopfield 内存随 Knowledge 线性增长** | 高 | 中 — RSS 可能超 1GB | M4 (Dream)、长期运行稳定性 | P1 |
| 🟠 R4 | **EntangleGraph 跨类型边工程质量** | 中 | 中 — CoShelf 边数量爆炸或遗漏 | M3 (recall 关联质量) | P1 |
| 🟡 R5 | **Knowledge engram text 过长导致 LMDB 膨胀** | 低 | 中 | M2 (存储)、长期磁盘占用 | P2 |
| 🟡 R6 | **单 HNSW 200K+ 节点延迟可感知** | 低 | 中 | M3 (recall p99)、用户体验 | P2 |
| 🟢 R7 | **unmount 批量删除耗时过长** | 低 | 低 — 可异步 | M2 (unmount)、用户体验 | P3 |
| 🟢 R8 | **store 去重检查增加写入延迟** | 低 | 低 — 已缓存在 EngramCache | M2 (store p99) | P3 |

### 3.2 真正的阻塞风险详解

#### 🔴 R1：HNSW delete API（阻塞 unmount_shelf）

**为什么是 #1 风险**：
- 当前 `brain.rs:1282` 注释明确：`forget()` 跳过 HNSW 删除
- HNSW 图结构删除需要维护连通性——删除节点后，其邻居的图层级可能断裂
- 没有 delete，unmount_shelf 只能做软删除（LMDB 标记 deleted + recall 过滤），但 HNSW 索引持续膨胀
- 这是真正需要从零实现的算法工作，不是简单 CRUD

**两阶段缓解策略**：
```
v0.11.0 (首版):
  → 软删除：LMDB 中标记 tombstone，recall 过滤层跳过
  → HNSW 节点保留但不可达（通过 filter 屏蔽）
  → 接受 HNSW 索引膨胀（unmount 不释放 HNSW 空间）

v0.11.1 (补丁):
  → 实现 HNSW delete（参考 hnswlib 的 mark_delete + rebuild 策略）
  → 或引入定期 compact：重建 HNSW（过滤掉 tombstone 节点）
```

**决策建议**：析客已将 HNSW delete 标记为 P0（开放问题 Q2）。我建议**首版走软删除**以降低 v0.11.0 交付风险，v0.11.1 补硬删除。

#### 🔴 R2：mount_shelf 批量写入性能（新增风险，PRD 未独立列出）

**为什么是 #2 风险**：
- PRD §8 SLA：mount_shelf(100 chunks) < 5s
- 但实际场景：一本书可能 1000+ chunks × (embedding + LMDB write + HNSW insert + Hopfield + EntangleGraph)
- 当前 store() 是同步的（约 1ms），1000 chunks = 1s 仅是嵌入时间，实际含 embedding 可能 5-10s
- 数析的 SLA 明确告警线是 15s

**缓解**：
- M2 必须实现 LMDB 批量写事务（单事务内写多条 engram）
- Encoder 批量编码（一次模型调用处理多个 chunk）
- mount_shelf 返回 progress 而非阻塞等待

### 3.3 不确定性漏斗

```
高不确定性 ───────────────────────────── 低不确定性
    │                                       │
  HNSW delete    EntangleGraph    Dream     MCP 适配
  mount 批量      跨类型边      vitality    LMDB schema
  (算法工作)    (图算法 tuning)  (参数调优)  (数据结构)
```

---

## 4. Now / Next / Later 路线图

### 4.1 Now：v0.11.0 — 统一存储重构（本次，10 周）

**一句话**：裂脑合拢。Shelf 融入主存储，一次 recall 返回一切。

#### 交付物清单

| 模块 | 交付 | 优先级 |
|------|------|--------|
| **数据模型** | `EngramKind::Knowledge`、`KnowledgeMeta`、`ShelfDomain`、`Confidence` | P0 |
| **存储** | LMDB v2 schema、store() ADD-only + 去重、Knowledge engram 持久化 | P0 |
| **Shelf** | mount_shelf → store 批量写入、unmount_shelf → forget_batch、ShelfManager 退化为元数据管理 | P0 |
| **HNSW** | HNSW delete API（或软删除 fallback）| P0 |
| **recall** | 统一 RecallResult 格式、recall filter (kind/shelf_id/domain)、Knowledge↔Episode 混合排序 | P0 |
| **EntangleGraph** | CoShelf 边自动创建、Semantic 跨类型边（首版至少 CoShelf）、graph_associations 返回 | P0 |
| **Dream** | Knowledge vitality 独立衰减曲线、Hopfield knowledge_pattern_weight、DreamResult 扩展 | P1 |
| **MCP** | 7 个工具语义更新、knowledge_search → recall 废弃映射（保留 deprecation warning）| P0 |
| **质量** | 完整链路集成测试、性能回归测试、MeowAgent 联调 | P0 |

#### 里程碑时间线

```
Week 1-2  │ M1: 数据结构        ████████░░░░░░░░░░  16pts
Week 3-4  │ M2: 存储层统一       ░░░░░░░░████████░░  40pts ⚠️ 密集
Week 5-6  │ M3: recall + 图      ░░░░░░░░░░░░░░████  35pts ⚠️ 密集
Week 7-8  │ M4: Dream + Hopfield ░░░░░░░░░░░░░░░░░░  28pts (前段与 M3 并行)
Week 9-10 │ M5: MCP + 测试       ░░░░░░░░░░░░░░░░░░  24pts
```

#### 关键决策点

| 时间 | 决策 | 选项 |
|------|------|------|
| W2 末 | HNSW delete 策略 | A) 硬删除（多 1 周） B) 软删除 + v0.11.1 补硬删除 |
| W4 末 | M2 完成度评审 | Go/No-Go：mount→recall→unmount 链路是否可跑通 |
| W6 末 | EntangleGraph 跨类型边范围 | 首版 CoShelf only vs CoShelf + Semantic |
| W8 末 | M4 完成度评审 | Dream Knowledge 覆盖是否完整 |
| W10 末 | 发布评审 | 全部验收标准 + 性能回归通过 |

---

### 4.2 Next：v0.11.1 — 修补与增强（后续，4-6 周）

**一句话**：打磨。硬删除、性能调优、垂类框架、上下文工具。

| 模块 | 交付 | 来源 | 优先级 |
|------|------|------|--------|
| **HNSW** | HNSW 硬删除 API（若 v0.11.0 只做了软删除）| 数析 R5 | P0 |
| **性能** | Knowledge vitality 曲线基于实际数据校准 | PRD Q1 | P0 |
| **性能** | Hopfield knowledge_pattern_weight 基于 recall 质量基准校准 | PRD Q7 | P1 |
| **MCP** | `memhop_context` 一站式上下文工具（类比 codegraph_context）| 竞析 §6.1 | P1 |
| **Shelf** | ShelfDomainTrait 框架 + Law/Medical 内置示例 | 瑞思 §2.4 | P1 |
| **Shelf** | 文件变更增量更新（不重做全量 mount）| 竞析 §6.5 | P1 |
| **存储** | Knowledge engram 冷热分层存储 | 数析 §4.2 | P2 |
| **索引** | SQLite FTS5 并行全文索引 | 竞析 §6.5 | P2 |
| **recall** | 三路融合：语义 + BM25 + EntangleGraph 扩散 | 竞析 §6.4 | P2 |
| **Dream** | 社区检测 + 主题摘要生成（Schema engram）| GraphRAG 启发 | P2 |
| **MCP** | `memhop_associations` 涌现关联发现工具 | 竞析 §6.1 | P2 |
| **Shelf** | `mount_shelf --watch` 文件监听模式 | Graphify 启发 | P2 |
| **meta** | `confidence` 字段自动推理（领域 extractor）| PRD Q5 | P2 |

---

### 4.3 Later：v0.11.2 — 垂类深化（远期，6-8 周）

**一句话**：领域定制。完整 ShelfDomain trait 生态 + Code 领域深度集成。

| 模块 | 交付 | 来源 |
|------|------|------|
| **Shelf** | ShelfDomainTrait 完整实现（chunk/rank/meta 全可定制）| PRD |
| **法律** | Law domain：法院层级加权、法条关联、案例时效 | 瑞思 §2.2 |
| **医学** | Medical domain：证据等级门控、疾病分类、指南时效 | 瑞思 §2.2 |
| **学术** | Academic domain：引用链追踪、研究方向聚类 | 瑞思 §2.2 |
| **金融** | Finance domain：时效敏感性、标的关联、券商对比 | 瑞思 §2.2 |
| **代码** | Code domain：与 CodeGraph 协同的代码记忆策略 | 竞析 §2.1 |
| **MCP** | recall 双模：`recall()` 精度 + `recall_global()` 宏观 | GraphRAG 启发 |

---

### 4.4 v1.0.0 及以后（不在本次路线图范围内）

- 文件级 LMDB 同步 (iCloud/Git)
- 团队轻量共享（只读 token + 手动同步）
- Node2Vec 图嵌入探索
- 多设备冲突解决

---

### 4.5 路线图总览

```
v0.11.0 (10w)         v0.11.1 (4-6w)         v0.11.2 (6-8w)        v1.0.0
─────────────────────────────────────────────────────────────────────────────
统一存储重构            修补与增强               垂类深化                协作
                       
■ EngramKind::Knowledge ■ HNSW 硬删除           ■ Law/Medical/Academic  ■ 多设备同步
■ LMDB v2 schema        ■ memhop_context        ■ Code domain          ■ 团队共享
■ store() ADD-only      ■ ShelfDomain trait      ■ recall_global()      ■ Node2Vec
■ mount → batch store   ■ Law/Medical 示例       ■ 完整垂类生态
■ unmount → batch del   ■ 增量更新
■ HNSW delete (软)      ■ 冷热分层
■ 统一 recall           ■ FTS5 全文索引
■ EntangleGraph CoShelf ■ 三路融合检索
■ Dream Knowledge       ■ 社区检测摘要
■ MCP 工具更新          ■ 参数校准
■ MeowAgent 适配        

裂脑合拢                体验打磨                 领域深化                网络效应
─────────────────────────────────────────────────────────────────────────────
```

---

## 5. MeowAgent 接入指南概要

### 5.1 变更性质

**破坏性变更 — 不兼容旧版本。**

v0.11.0 的 LMDB schema 与 v0.9.0/v0.10.0 完全不兼容。打开旧数据库会收到明确的版本错误（不会静默失败）。MeowAgent 是 MemHop 的唯一下游，必须同步升级。

### 5.2 需要改什么

#### 5.2.1 数据层面：重新挂载所有 Shelf

```
旧行为:
  mount_shelf("/books/rust-async.pdf")
  → Shelf 存在内存 HashMap（重启丢失）

v0.11.0 行为:
  旧的 Shelf 数据全部失效（LMDB v2 不兼容）
  → 需要重新 mount_shelf() 所有文档
  → 之后 Knowledge engram 持久化在 LMDB 中，重启可用
```

**行动**：MeowAgent 启动时检测 v0.11.0 MemHop → 提示用户重新挂载文档（可提供"重新挂载上次的 Shelf 列表"快捷操作）。

#### 5.2.2 API 层面：MCP 调用变更

| 旧调用 | 新调用 | 变更类型 |
|--------|--------|---------|
| `knowledge_search(query, shelf_id)` | `recall(query, filter: {kind: ["knowledge"], shelf_id: [shelf_id]})` | 废弃映射（v0.11.0 兼容，有 deprecation warning） |
| `knowledge_search(query, shelf_id)` + `recall(query)` 两次调用 + 手动合并 | `recall(query)` 一次调用 | **推荐迁移路径** |
| `store(text)` — 可能覆盖 | `store(text)` — ADD-only，返回 `"duplicate"` 或 `"stored"` | 语义变更 |
| `recall(query)` → `Vec<Engram>` | `recall(query)` → `RecallResult { results, shelf_contexts, graph_associations, meta }` | 返回格式变更 |
| `mount_shelf(path)` — 同步阻塞 | `mount_shelf(path, domain)` — 新增 domain 参数 + 返回 `MountResult` | 参数和返回值变更 |
| `unmount_shelf(shelf_id)` — 清内存 | `unmount_shelf(shelf_id)` — 批量删除 Knowledge engram + 返回 `UnmountResult` | 语义变更 |
| `dream()` — 仅处理 Episode | `dream()` — 覆盖 Knowledge，返回扩展的 `DreamResult` | 返回格式变更 |

#### 5.2.3 代码层面：关键修改点

**1. 移除 knowledge_search → recall 双调用合并逻辑**

```python
# 旧代码（需删除）
episode_results = memhop.recall(query)
knowledge_results = memhop.knowledge_search(query, shelf_id)
merged = merge_and_dedup(episode_results, knowledge_results)

# 新代码（替换为）
result = memhop.recall(query)
# result.results 已包含 Episode + Knowledge 混合排序
# result.shelf_contexts 提供来源追溯
# result.graph_associations 提供跨类型关联
```

**2. 适配 RecallResult 新格式**

```python
# 旧格式
for engram in recall_result:  # List[Engram]
    text = engram.text
    score = engram.score

# 新格式
for item in recall_result.results:  # List[RankedEngram]
    text = item.engram.text
    kind = item.engram.kind        # "episode" | "knowledge" | ...
    score = item.score
    score_detail = item.score_breakdown  # {semantic, keyword, hopfield, graph_boost}
    if item.shelf_context:               # 仅 Knowledge engram 有
        source = item.shelf_context.source_path
        textunit = item.shelf_context.source_textunit
```

**3. 处理 store() 的 duplicate 响应**

```python
# 旧代码
result = memhop.store(text="...")
engram_id = result.engram_id

# 新代码
result = memhop.store(text="...", kind="episode")
if result.status == "duplicate":
    log.debug(f"跳过重复记忆，已有: {result.duplicate_of}")
elif result.status == "stored":
    engram_id = result.engram_id
```

**4. mount_shelf 新增 domain 参数**

```python
# 旧代码
memhop.mount_shelf("/path/to/books/")

# 新代码
result = memhop.mount_shelf("/path/to/books/", domain="book")
# result: {shelf_id, chunk_count, domain, source_path, warnings}
```

### 5.3 什么时候改

| 阶段 | 时间窗口 | 内容 |
|------|---------|------|
| **联调期** | v0.11.0 W9-10 | MeowAgent 与 MemHop 联调，验证所有 MCP 工具的新语义 |
| **发布前** | v0.11.0 发布前 1 周 | MeowAgent 侧代码冻结，通过集成测试 |
| **发布日** | v0.11.0 发布 | MemHop + MeowAgent 同步发布。旧版本 LMDB 数据库提示用户删除或备份 |

**不存在渐进迁移路径**——v0.11.0 是不兼容升级，旧 LMDB 数据库无法被新版本打开。用户需要：
1. 备份旧数据库（如需要保留旧记忆）
2. 删除旧 `db/` 目录
3. 启动 v0.11.0（自动创建新 LMDB v2）
4. 通过 MeowAgent 重新挂载 Shelf 文档

### 5.4 MeowAgent 侧 checklist

- [ ] 移除所有 `knowledge_search()` 调用，替换为 `recall()` + filter
- [ ] 移除双调用结果合并/去重/重排逻辑
- [ ] 适配 `RecallResult` 新结构（`.results`、`.shelf_contexts`、`.graph_associations`）
- [ ] 适配 `store()` 的 `status: "stored" | "duplicate"` 返回值
- [ ] `mount_shelf()` 增加 `domain` 参数
- [ ] 处理 `unmount_shelf()` 的新返回格式（`deleted_count`）
- [ ] 处理 `dream()` 的扩展返回格式（`knowledge_processed`、`new_associations`）
- [ ] 在 UI 中利用 `shelf_context` 展示知识来源（source_path + source_textunit）
- [ ] 在 UI 中利用 `graph_associations` 展示跨类型关联（"这段知识和你之前的经验有关"）
- [ ] 首次启动检测 v0.11.0 → 提示用户重新挂载 Shelf
- [ ] 更新 MeowAgent 内部文档和 prompt 中引用的 MCP 工具名

### 5.5 向后兼容承诺

- **knowledge_search MCP 工具**在 v0.11.0 保留（带 deprecation warning），v0.11.1 移除
- **recall 旧格式**不兼容——所有调用方必须适配 `RecallResult`
- **store 旧语义**不兼容——调用方必须处理 `duplicate` 状态
- **LMDB 数据库**不兼容——用户数据不迁移，需要重建

---

## 附录 A：与 PRD 差异说明

| PRD 内容 | 路线图评估 | 差异 |
|---------|-----------|------|
| M2 (W3-4) 含 HNSW delete | 建议软删除作为首版 fallback，硬删除推迟到 v0.11.1 | 降低 M2 风险 |
| M4 (W7-8) 严格后于 M3 | M4 vitality 部分可与 M3 并行 | 缩短关键路径 |
| 未提及 Encoder 批量 API | 新增为遗漏依赖 D1 | 补充 |
| 未提及 LMDB 批量写事务 | 新增为遗漏依赖 D2 | 补充 |
| 风险 R1-R7 排序 | 重排序为 R1(delete) → R2(mount batch) → R3(Hopfield) → ... | 工程视角修正 |
| v0.11.1/v0.11.2 拆分 | 细化为修补(v0.11.1) + 垂类(v0.11.2) | 更好的节奏控制 |

## 附录 B：关键架构决策记录

| 决策 | 内容 | 决策者 | 状态 |
|------|------|--------|------|
| HNSW delete 策略 | 首版软删除，v0.11.1 硬删除 | 路径（建议）/ 方向明（审定） | 待审定 |
| Knowledge Hopfield 权重 | 默认 0.5，可配置 | 析客 | 已决策 |
| store 去重范围 | 全局 FIFO 最近 1000 条 | 路径（建议）| 待确认 |
| M3/M4 并行策略 | vitality 部分与 M3 并行 | 路径（建议）| 待确认 |
| mount_shelf 同步 vs 异步 | 同步返回，分批写入 | 析客（PRD Q6）| 已决策 |

---

> **本路线图由路径（Roadie）基于 PRD v1.0 + 三方上游报告 + 代码库状态综合产出。关键决策（HNSW delete 策略、里程碑节奏、并行策略）需方向明（Fang）最终审定。**
