# MemHop v0.18.0 → v0.18.1 全面验证计划

## Context

MemHop 是一个人脑记忆机制启发的 AI 代理记忆系统，当前版本为 v0.18.0。本次验证的目标是：
1. 全面验证功能完整性，识别并修复潜在 bug
2. 将版本升级至 v0.18.1
3. 恢复 CandleEncoder 的使用（当前被禁用）
4. 使用 multilingual-e5-small 模型作为默认编码器
5. 确保与 meowAgent 的完全集成能力
6. 所有质量指标必须达到行业第一梯队水平

## 当前状态分析

### 版本不一致问题
- `memhop/Cargo.toml`: `0.18.0`
- `memhop-mcp-server/Cargo.toml`: `0.18.0`
- `memhop/src/lib.rs` L1 注释: `v0.17.0` ❌
- `memhop/src/brain/mod.rs` L17 注释: `v0.17.0` ❌
- `memhop-mcp-server/src/main.rs` L1 注释: `v0.18.0`
- `memhop-mcp-server/src/main.rs` L11 `VERSION`: `"0.18.0"`
- `AGENT_INTEGRATION.md` 标题: `v0.18.0`

### 技术债务
1. **CandleEncoder 被禁用**: `encoder/mod.rs` L26-30 feature-gated 且标记为暂时禁用
2. **模型依赖**: multilingual-e5-small (1.3GB) 存在但未使用
3. **clippy 注解**: 5 处 `#[allow(clippy::too_many_arguments)]`
4. **dead_code 注解**: 4 处 `#[allow(dead_code)]`

### 测试覆盖
- 集成测试: 12 个测试函数
- MCP API 测试: 15 个测试函数
- 基准测试: 1 个基准文件

## 验证阶段

### Phase 0: 环境准备与基线建立 (0.5 天)

**目标**: 建立验证基线，确保所有工具和环境就绪。

**任务清单**:
1. Rust 工具链验证
   - 确认 Rust 版本 >= 1.85（Cargo.toml 使用 edition 2024）
   - `rustup show` 确认 toolchain 配置
   - `cargo --version` >= 1.85

2. 编译基线建立
   - `cargo check --workspace` 确保无错误
   - `cargo clippy --workspace -- -D warnings` 记录所有现有 warning
   - `cargo test --workspace` 记录通过率和失败详情
   - `cargo build --release` 确保可构建

3. 模型文件审计
   - 计算 `models/` 目录总大小和明细
   - 确认 multilingual-e5-small 模型完整性
   - 验证模型文件格式（config.json, model.safetensors, tokenizer.json）

4. 依赖树审计
   - `cargo tree` 分析依赖关系
   - 确认 heed (LMDB), usearch (HNSW), bincode, serde_json, half 等关键依赖版本
   - 检查 candle 相关依赖是否需要添加

**检查点**: 全部命令成功执行，记录基线数据。

---

### Phase 1: 版本统一和 CandleEncoder 恢复 (1.5 天)

**目标**: 将所有版本号统一到 v0.18.1，恢复 CandleEncoder 功能。

**任务清单**:

#### 1.1 版本号更新 (P0)
- 更新 `memhop/Cargo.toml` L3: `0.18.0` → `0.18.1`
- 更新 `memhop-mcp-server/Cargo.toml` L3: `0.18.0` → `0.18.1`
- 更新 `memhop/src/lib.rs` L1 注释: `v0.17.0` → `v0.18.1`
- 更新 `memhop/src/brain/mod.rs` L17 注释: `v0.17.0` → `v0.18.1`
- 更新 `memhop-mcp-server/src/main.rs` L1 注释: `v0.18.0` → `v0.18.1`
- 更新 `memhop-mcp-server/src/main.rs` L11 `VERSION`: `"0.18.0"` → `"0.18.1"`
- 更新 `memhop-mcp-server/tests/mcp_api_test.rs` L123: `"0.17.0"` → `"0.18.1"`
- 更新 `AGENT_INTEGRATION.md` 标题: `v0.18.0` → `v0.18.1`
- 更新 `README.md` 标题: `v0.18.0` → `v0.18.1`
- 更新 `jiagou.md` 标题: `v0.18.0` → `v0.18.1`

#### 1.2 恢复 CandleEncoder (P0)
- 在 `memhop/Cargo.toml` 中添加 candle 相关依赖:
  ```toml
  [features]
  default = ["candle"]
  candle = ["candle-core", "candle-nn", "candle-transformers", "tokenizers"]
  
  [dependencies]
  candle-core = { version = "0.8", optional = true }
  candle-nn = { version = "0.8", optional = true }
  candle-transformers = { version = "0.8", optional = true }
  tokenizers = { version = "0.21", optional = true }
  ```
- 更新 `memhop/src/encoder/mod.rs`:
  - 移除 L26-30 的注释，启用 CandleEncoder
  - 添加 feature gate: `#[cfg(feature = "candle")]`
- 更新 `memhop/src/brain/mod.rs`:
  - 修改 L48-50 的编码器初始化逻辑
  - 当 `model_path` 存在时，使用 CandleEncoder
  - 当 `model_path` 不存在时，回退到 NgramEncoder
  - 默认 model_path 指向 `models/multilingual-e5-small`
- 测试 CandleEncoder 初始化和编码功能

#### 1.3 文档同步 (P1)
- 更新 `jiagou.md`:
  - L14: 更新编码器描述为 `intfloat/multilingual-e5-small (384维, ~1.3GB)`
  - L62-71: 更新编码器章节，说明 CandleEncoder 为默认，NgramEncoder 为回退
- 更新 `AGENT_INTEGRATION.md`:
  - 添加编码器配置说明
  - 添加 `MEMHOP_MODEL_PATH` 环境变量说明

#### 1.4 clippy 注解审查 (P2)
- 审查 5 处 `#[allow(clippy::too_many_arguments)]`:
  - `hypergraph/graph.rs:64` (add_node, 7 args) - 保留
  - `hypergraph/graph.rs:73` (add_node_with_id, 8 args) - 保留
  - `hypergraph/graph.rs:97` (add_hyperedge, 6 args) - 保留
  - `domain_graph/mod.rs:93` - 检查是否可以重构
  - `raw_archive/mod.rs:57` - 检查是否可以重构
- 审查 4 处 `#[allow(dead_code)]`:
  - `index.rs:29` - 检查是否可以移除
  - `encoder/ngram.rs` 中的 3 处 - 保留（供未来使用）

**检查点**: 所有版本号一致为 0.18.1，CandleEncoder 可正常初始化和编码，编译无新增 warning。

---

### Phase 2: 代码质量全面审查 (1 天)

**目标**: 深入审查代码质量，识别潜在缺陷。

**任务清单**:

#### 2.1 错误处理审查
- 检查所有 `unwrap()` 调用在生产代码中（非测试）
- 审查 `MemHopError` 变体是否足够覆盖所有错误场景
- 确认 LMDB 事务异常处理路径
- 检查 Brain::open() 中对 rebuild 失败的处理

#### 2.2 并发安全审查
- 审查 `BRAIN_CACHE: LazyLock<Mutex<HashMap<...>>>` 的锁定策略
- 确认 `Arc<Mutex<Brain>>` 模式在 tokio 异步上下文中的正确性
- 检查 `ID_SEQ: AtomicU64` 全局计数器是否有溢出风险

#### 2.3 内存安全审查
- 确认 `unsafe` 代码块的使用（如有）
- 审查 LMDB mmap 的大小配置是否合理
- 检查 HNSW 索引重建时的内存峰值

#### 2.4 类型安全性审查
- 检查 `RecallRequest` 的 `spread_depth` 字段同时使用 `Option<usize>` 但实际逻辑在 `Some(0)` 时等同于 `None`
- 确认 `Layer` 枚举的序列化/反序列化一致性

#### 2.5 边界条件测试
- 空字符串查询 (`query.trim().is_empty()`)
- 空 batch 写入 (items.is_empty())
- 超大 batch (1000+ items)
- 不存在的 agent_id
- 不存在的节点 ID / 文档 ID

**检查点**: 形成代码审查报告，列出所有发现和建议。

---

### Phase 3: 5层架构功能完整性验证 (2 天)

**目标**: 逐一验证每层功能的正确性。

**任务清单**:

#### 3.1 L4 原文层验证
- 存储单条/多条对话记录
- 验证 `l4_docs_stored` 计数正确性
- 验证 `get_l4_raw()` 返回正确的文档内容
- 验证长文本 splitter 行为（超过 512 字符的切分）
- 验证 turn_id, session_id 索引

#### 3.2 L3 领域超图层验证
- 通过 shelf/mount 挂载知识库
- 验证 chunker 分块逻辑（1024 字符分块）
- 验证 scanner 文件扫描和过滤
- 验证 domain_id 隔离
- 验证 unmount 后数据移除
- 验证 `list_l3_paths()` 返回正确的路径信息

#### 3.3 L2 话题图层验证
- 验证 topic_label 自动创建话题
- 验证相同 topic_label 的节点归并到同一话题
- 验证 `list_topics()` 返回完整列表
- 验证 `update_topic()` 更新摘要和关键词
- 验证 centroid 向量索引重建

#### 3.4 L1 纠缠超图层验证
- 验证节点去重（cosine > 0.95 + Jaccard > 0.8）
- 验证 `l1_dedup_skipped` 计数
- 验证超边创建和权重
- 验证 BM25 索引更新
- 验证 HNSW 索引更新
- 测试 importance 加权

#### 3.5 L0 画像层验证
- 验证 `set_l0_profile()` / `get_l0_profile()` 往返
- 验证 `set_l0()` 完整版设置
- 验证 L0 快照历史记录
- 验证 Dream L0 Formation 更新画像
- 验证 recall 时附带 L0 profile

**测试用例设计**:
```
TC-L4-01: 单条中英文文本写入与读取
TC-L4-02: 长文本 (6KB) 自动分块
TC-L4-03: 批量 100 条写入验证
TC-L3-01: 代码文件 (Rust) 挂载与检索
TC-L3-02: Markdown 文档挂载与检索
TC-L3-03: 空目录挂载失败处理
TC-L2-01: 相同话题标签归并
TC-L2-02: 不同话题标签隔离
TC-L2-03: 话题摘要更新
TC-L1-01: 完全重复的去重验证
TC-L1-02: 近似重复的去重阈值边界测试
TC-L1-03: 超边链创建和演化
TC-L0-01: 画像完整 CRUD
TC-L0-02: Dream 自动更新画像
TC-L0-03: 画像快照版本历史
```

**检查点**: 所有测试用例通过，5层架构功能完整。

---

### Phase 4: 双通道检索性能基准测试 (1.5 天)

**目标**: 验证检索性能和质量指标达到行业第一梯队。

**任务清单**:

#### 4.1 基准测试运行
- 运行 `cargo bench` 获取完整基准报告
- 记录 batch_store 在不同数据量 (10/50/100/500) 下的延迟
- 记录 recall 在不同结果数 (5/10/50/100) 下的延迟
- 对比 NgramEncoder vs CandleEncoder 性能

#### 4.2 BM25 通道测试
- 纯英文检索精度
- 纯中文检索精度
- 中英混合检索精度
- 关键词命中率

#### 4.3 HNSW 语义通道测试
- CandleEncoder 向量的 cosine 相似度质量
- HNSW 近似搜索质量 vs 暴力搜索结果
- 大规模索引下的搜索延迟（1000/5000/10000 条）

#### 4.4 RRF 融合测试
- 验证动态 RRF k 值逻辑
- 验证跨层排名公平性
- 验证单通道 vs 双通道的结果质量

#### 4.5 质量指标测量
- **记忆率 (Recall@K)**: 目标 R@5 >= 90%
- **精确率 (Precision)**: 目标 >= 85%
- **延迟**: 目标 < 5ms (P99)
- **去重准确率**: 目标 F1 >= 95%

**测试数据集设计**:
- 构造 100 条标记好的中英文对话数据
- 每条标注: 相关话题标签、预期召回排序
- 包含正例和负例

**检查点**: 基准报告包含所有指标数据，全部达标。

---

### Phase 5: MCP 接口完整性验证 (1.5 天)

**目标**: 确保所有 20 个 MCP 接口正常工作。

**接口分级验证**:

| 优先级 | 接口 | 验证方式 |
|--------|------|----------|
| P0 | memhop_batch_store | 集成测试 + 手工 E2E |
| P0 | memhop_recall | 集成测试 + 手工 E2E |
| P0 | memhop_health | 单元测试 + 手工 |
| P1 | memhop_consolidate | 集成测试 + 手工 |
| P1 | memhop_dream | 同 consolidate |
| P1 | memhop_organize | 手工 E2E |
| P1 | memhop_mount_shelf | 集成测试 + 手工 |
| P1 | memhop_unmount_shelf | 集成测试 + 手工 |
| P1 | memhop_list_shelf | 集成测试 + 手工 |
| P1 | memhop_get_profile | 单元测试 |
| P1 | memhop_set_profile | 单元测试 |
| P1 | memhop_set_l0 | 单元测试 |
| P2 | memhop_get_activated | 单元测试 |
| P2 | memhop_activate | 单元测试 |
| P2 | memhop_deactivate | 单元测试 |
| P2 | memhop_feedback | 单元测试 |
| P2 | memhop_get_l4_raw | 单元测试 |
| P2 | memhop_list_l3_paths | 单元测试 |
| P2 | memhop_list_topics | 单元测试 |
| P2 | memhop_re_search | 单元测试 |
| P2 | memhop_update_topic | 单元测试 |
| P2 | memhop_stats | 单元测试 |

**任务清单**:

1. **现有测试审计**
   - 更新 `mcp_api_test.rs` L123 版本号
   - 确保所有 test 通过

2. **Unix Socket 连接测试**
   - 启动 mcp-server，通过 netcat/socat 发送 JSON-RPC 请求验证

3. **错误处理测试**
   - 无效 JSON (`-32700`)
   - 未知方法 (`-32601`)
   - 必填参数缺失 (`-32602`)
   - 内部错误 (`-32000`)

4. **并发测试**
   - 多个客户端同时连接
   - 多个 agent_id 同时操作
   - LMDB 读写锁竞争

**检查点**: 20 个接口全部测试通过，错误码正确。

---

### Phase 6: Dream/Organize 记忆巩固测试 (1 天)

**目标**: 验证 7 阶段巩固管线的正确性和稳定性。

**任务清单**:

#### 6.1 Dream 7 阶段逐阶段验证
- Stage 1 (NREM): 超边权重衰减和剪枝
- Stage 2 (REM-Merge): 话题合并
- Stage 3 (REM-Reflect): 话题反思更新摘要
- Stage 4 (REM-Plan): 计划压缩
- Stage 5 (Co-occurrence): 跨话题超边
- Stage 6 (L0 Formation): 世界观提取
- Stage 7: 索引重建

#### 6.2 Dream 边界条件
- 空数据库上运行 consolidate 不会 panic
- 极端数据量（1000+节点）下的性能
- 连续多次 consolidate 的幂等性

#### 6.3 Organize 功能验证
- `organize_node()` 对指定节点执行归类
- 关键词提取 (extract_keywords) 的中英文混合测试
- 停用词过滤验证
- 共现超边创建

#### 6.4 会话管理验证
- 话题激活/停用
- TTL 过期清理
- activated topic priority 在 recall 中的优先级提升
- 多 session 隔离

**测试用例**:
```
TC-DREAM-01: 完整 7 阶段管线运行
TC-DREAM-02: 空数据库 consolidate
TC-DREAM-03: 连续 3 次 consolidate 的幂等性
TC-DREAM-04: vitality 衰减边界测试
TC-ORG-01: 单节点 organize
TC-ORG-02: 关键词中英文提取
TC-SESSION-01: session 隔离
TC-SESSION-02: TTL 过期清理
TC-SESSION-03: 激活话题优先级检索
```

**检查点**: 所有测试用例通过，Dream 管线稳定可靠。

---

### Phase 7: meowAgent 集成准备与文档 (1.5 天)

**目标**: 完善 AGENT_INTEGRATION.md，确保 meowAgent 可无缝集成。

**任务清单**:

#### 7.1 AGENT_INTEGRATION.md 完善
- 确保所有 20 个接口都有完整参数表、请求示例、响应示例
- P0 接口: 添加详细错误场景和错误码说明
- P1 接口: 补充完整参数说明
- P2 接口: 统一格式，确保一致性
- 添加版本兼容性矩阵
- 添加编码器配置说明

#### 7.2 接口规范补全
需要补全/新增的接口文档:
- `memhop_set_l0` - 详细参数说明
- `memhop_stats` - 完整响应格式
- `memhop_organize` - node_id 参数详细说明
- `memhop_get_activated` - 返回基于 agent_id 过滤
- `memhop_feedback` - weight 调整机制
- `memhop_re_search` - 与 recall 的区别
- `memhop_update_topic` - plan 参数

#### 7.3 CROSS_PROJECT_UPGRADE_GUIDE.md 更新
- 更新版本号至 v0.18.1
- 确认 meowAgent 需要做的任务状态
- 添加 v0.18.1 新增的变化

#### 7.4 接口契约测试 (Contract Test)
- 为每个 P0 接口编写契约测试，验证响应 schema 符合文档
- 为每个 P1 接口编写至少 1 个 E2E 测试

#### 7.5 示例代码验证
- 验证 `examples/basic_usage.py` 可正常运行
- 验证 `memhop/examples/basic_usage.py` 可正常运行
- 更新示例中的版本号和接口调用

**检查点**: AGENT_INTEGRATION.md 完整准确，所有接口文档齐全。

---

### Phase 8: 质量指标达标验证与收尾 (1 天)

**目标**: 全面测量关键指标，形成最终报告。

**质量指标检测表**:

| 指标 | 目标 | 测量方法 | 当前基线 |
|------|------|----------|----------|
| 检索召回率 (R@10) | >= 90% | 标注数据集测试 | 待测量 |
| 检索精确率 | >= 85% | 标注数据集测试 | 待测量 |
| 检索延迟 P99 | < 5ms | benchmark | 待测量 |
| 批量写入延迟 (100条) | < 100ms | benchmark | 待测量 |
| Dream 管线耗时 (1000节点) | < 5s | 手工计时 | 待测量 |
| 测试覆盖率 | >= 80% | cargo-tarpaulin | 待测量 |
| 去重准确率 (F1) | >= 95% | 标注重复对 | 待测量 |

**安全检测**:
- `cargo audit` 检查依赖安全性
- `cargo clippy -- -D warnings` 无新增 warning
- `cargo fmt --check` 格式正确

**交付物清单**:
1. 验证执行报告
2. 代码审查报告
3. 基准测试报告
4. 接口测试报告（20 接口全覆盖）
5. 质量指标报告
6. 更新后的 AGENT_INTEGRATION.md (v0.18.1)
7. 更新后的 CROSS_PROJECT_UPGRADE_GUIDE.md
8. CHANGELOG-v0.18.1.md

**检查点**: 所有质量指标达标，安全审计通过，交付物齐全。

---

## 风险评估和应对措施

| 风险 | 概率 | 影响 | 应对措施 |
|------|------|------|----------|
| CandleEncoder 依赖冲突 | 中 | 高 | 仔细测试 candle 依赖版本兼容性；必要时锁定版本 |
| 模型加载失败 | 低 | 高 | 验证模型文件完整性；提供清晰的错误信息 |
| LMDB 数据兼容性问题 | 低 | 高 | 升级前备份所有 .db 文件；验证 v0.18.0 数据可被 v0.18.1 读取 |
| HNSW 索引重建性能退化 | 中 | 中 | 基准测试对比；如退化则更新 connectivity/ef 参数 |
| meowAgent API 签名不匹配 | 中 | 高 | 确保 AGENT_INTEGRATION.md 100% 准确；运行 Joint Test |
| 并发竞争导致数据损坏 | 低 | 高 | LMDB 本身 ACID 保证；重点测试并发写入场景 |
| 质量指标不达标 | 中 | 高 | 优先修复 P0 指标；P1 指标可延后至 v0.19.0 |

## 关键文件清单

### 需要修改的文件
1. `memhop/Cargo.toml` - 版本号 + candle 依赖
2. `memhop-mcp-server/Cargo.toml` - 版本号
3. `memhop/src/lib.rs` - 版本注释
4. `memhop/src/brain/mod.rs` - 版本注释 + 编码器初始化
5. `memhop/src/encoder/mod.rs` - 启用 CandleEncoder
6. `memhop-mcp-server/src/main.rs` - 版本号
7. `memhop-mcp-server/tests/mcp_api_test.rs` - 版本号
8. `AGENT_INTEGRATION.md` - 版本号 + 接口文档完善
9. `README.md` - 版本号
10. `jiagou.md` - 版本号 + 编码器描述

### 需要创建的文件
1. `CHANGELOG-v0.18.1.md` - 变更日志
2. `plan_memhop_v0181_verification.md` - 本计划文件

## 执行检查清单

```
Phase 0 [ ] cargo check --workspace 通过
         [ ] cargo clippy --workspace 基线记录
         [ ] cargo test --workspace 基线通过
         [ ] 环境版本确认
         [ ] 模型文件完整性验证

Phase 1 [ ] 所有版本号统一为 0.18.1 (10处)
         [ ] CandleEncoder 恢复并可正常初始化
         [ ] jiagou.md 同步真实架构
         [ ] clippy 注解审查

Phase 2 [ ] 错误处理审查
         [ ] 并发安全审查
         [ ] 边界条件测试 added

Phase 3 [ ] L0 测试通过
         [ ] L1 测试通过 (含去重)
         [ ] L2 测试通过
         [ ] L3 测试通过
         [ ] L4 测试通过 (含长文本)

Phase 4 [ ] benchmark 运行
         [ ] 延迟达标 (< 5ms)
         [ ] 召回率达标 (>= 90%)

Phase 5 [ ] 20 个 MCP 接口全量测试
         [ ] 错误码验证
         [ ] 并发测试通过

Phase 6 [ ] Dream 7-stage 测试
         [ ] Organize 测试
         [ ] Session 管理测试

Phase 7 [ ] AGENT_INTEGRATION.md 完善
         [ ] CROSS_PROJECT_UPGRADE_GUIDE 更新
         [ ] 契约测试通过

Phase 8 [ ] 安全审计通过
         [ ] 质量指标全部达标
         [ ] CHANGELOG 撰写
         [ ] 最终报告提交
```

## 时间估算

总计: 约 11 天

- Phase 0: 0.5 天
- Phase 1: 1.5 天
- Phase 2: 1 天
- Phase 3: 2 天
- Phase 4: 1.5 天
- Phase 5: 1.5 天
- Phase 6: 1 天
- Phase 7: 1.5 天
- Phase 8: 1 天
