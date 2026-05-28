# MemHop v0.7.0 性能优化方案

**日期**：2026-05-27
**类型**：性能优化 / 架构改进
**基准**：`full_benchmark` @ 1K 条目，BGE-M3 INT8 ONNX (544MB)

---

## 📌 TL;DR

- **Dream O(N²)**：`nrem_vitality_decay` 对每条记忆做全库 Hopfield recall，1K 条目耗时 1-5 秒，100K 条目小时级
- **召回质量**：R@5=10% 是最大短板，Chroma/FAISS 是 90%
- **策略**：Dream 批量化 + 采样降复杂度，召回走双线并行（ONNX 语义 + Ngram 关键词），冲刺目标 R@5 > 80%、perceive < 1ms、Dream < 100ms @ 1K

---

## 🎯 核心结论卡片

| 项目 | 内容 |
|------|------|
| 推荐方案 | 双编码管线 + SparseIndex 粗筛 + Dream O(N)→O(N·k) |
| 优先级 | P0 |
| 预期影响 | R@5: 10% → 80%+, perceive: 15ms → 1ms, Dream: O(N²)→O(N) |
| 资源需求 | 改 4 个文件 (brain.rs, index.rs, onnx.rs, Cargo.toml) |
| 风险等级 | 低（多数为已有代码解禁/重构，无新架构） |

---

## 一、Dream 性能优化

### 根因分析

`brain.rs:844-940` 的 `nrem_vitality_decay` 是唯一瓶颈：

```rust
// 对每条记忆 (N=1000):
for (id, engram) in entries {
    // ① 全库 Hopfield recall — O(N·d)，1000条=1000次全库扫描
    let neighbors = self.hopfield.recall_topk(&query_f32, 10);
    // ② 单独 LMDB 写事务 — 一条一个 commit
    let mut txn = self.storage.begin_write()?;
    self.storage.put_hippocampus(&mut txn, &id, &engram)?;
    txn.commit()?;
}
```

| 规模 | Hopfield 调用 | 单次复杂度 | LMDB commit 次数 | 估算耗时 |
|------|--------------|----------|-----------------|---------|
| 1K | 1,000 | O(1K·1024) | 1,000 | 1-5 秒 |
| 10K | 10,000 | O(10K·1024) | 10,000 | 50-200 秒 |
| 100K | 100,000 | O(100K·1024) | 100,000 | 小时级 |

**复杂度 O(N²)**，且每次 commit 触发磁盘 fsync。

### 方案

#### P0-1: 批量 LMDB 事务（改 1 处，风险低）

```rust
// 改前：每条单独 commit
// 改后：一个事务包住所有 vitality 更新
let mut txn = self.storage.begin_write()?;
for (id, engram) in entries {
    self.storage.put_hippocampus(&mut txn, id, &engram)?;
}
txn.commit()?;
```

预期：1K 场景 Dream 磁盘部分 1000 次 commit → 1 次，**快 50-100x**。
影响文件：`brain.rs` `nrem_vitality_decay`

#### P0-2: 采样替代全量 Hopfield（改 1 处，风险低）

Vitality decay 需要"近邻相似度"来算干扰。不需要对每条精确算：

```rust
// 改前：全库 Hopfield recall_topk(q, 10) — O(N·d)
// 改后：随机采样 k=50 条近邻 — O(k·d)，k 固定
let sampled = entries.choose_multiple(&mut rng, 50);
let neighbors = sampled.map(|(_, e)| cosine(&query, &e.vector)).collect();
```

预期：Dream 计算部分 O(N²) → O(N·k)，k=50，**1K 场景快 20x，100K 场景快 2000x**。
影响文件：`brain.rs` `nrem_vitality_decay`

#### P1: Dream 异步化（改 1-2 处，风险中）

```rust
// 当前: perceive → 同步触发 dream → 阻塞
// 改后: perceive → spawn dream job to background thread
if self.store_count >= self.config.dream_interval {
    self.store_count = 0;
    let engine = self.dream_engine.clone(); // Arc<Mutex<...>>
    std::thread::spawn(move || {
        engine.lock().unwrap().dream().ok();
    });
}
```

预期：Dream 彻底不阻塞 perceive。并发安全通过 `Arc<Mutex<>>` 保证。
风险：Dream 期间的 LMDB 读写可能与 perceive 竞争。需确认 heed 的 `EnvOpenOptions` 允许多读单写。
影响文件：`brain.rs` perceive + dream

---

## 二、ONNX 策略：双线并行

### 为什么不能二选一

| 场景 | ONNX 语义编码 | Ngram 字符编码 | 赢家 |
|------|-------------|-------------|------|
| 低竞争（5 条独立文本） | R@5 **100%** | R@5 50% | ONNX |
| 高竞争 + 关键词（1K, 10 条/主题） | 命中率 40% | 命中率 **100%** | Ngram |
| 同义改写（"早餐吃什么" vs "豆浆油条"） | ✅ 可召回 | ❌ 无法召回 | ONNX |
| 精确关键词（"rate limiting"） | 🟡 语义竞争 | ✅ 字符匹配 | Ngram |

**丢掉任何一个都是自断一臂。** 正确做法是两条管线并行，结果合并。

### 架构

```
                    输入文本
                       │
           ┌───────────┼───────────┐
           │                       │
     ONNX 语义编码             Ngram 稀疏编码
     (BGE-M3 INT8)            (字符 n-gram)
           │                       │
           ▼                       ▼
      Hopfield 召回           SparseIndex 粗筛
      (全库精排)              (缩小到 ~50 条)
           │                       │
           └───────────┬───────────┘
                       │
               合并 + 去重 + 最终排序
                       │
                     结果
```

### 实现

**存储**（已有，不需改）：
- `HybridEncoder` producing vector = ngram 0.3 + ONNX 0.7
- SparseIndex 存储 ngram 稀疏哈希（`index.rs` 已实现，待解禁）

**召回**（需改 `brain.rs` recall flow）：
```rust
fn recall(&self, text: &str, req: &RecallRequest) -> Vec<(String, f32)> {
    let query_emb = self.encoder.encode(text);
    
    // 路径 A: SparseIndex 粗筛 → Hopfield 精排
    let sparse_candidates = self.index.candidates(text, 50);
    let path_a = self.hopfield.recall_among(&query_emb, &sparse_candidates, req.limit);
    
    // 路径 B: Hopfield 全库召回（ONNX 语义）
    let path_b = self.hopfield.recall_topk(&query_emb, req.limit);
    
    // 合并去重，按分数重排
    merge_and_rerank(path_a, path_b, req.limit)
}
```

预期：
- Path A 提供关键词精确匹配（解决 1K 同簇竞争问题）
- Path B 提供语义泛化能力（解决改写/同义表达）
- 合并后 R@5 预期从 10% → 80%+

---

## 三、perceive 延迟优化

### 当前瓶颈

`brain.rs:280-350` 的 perceive 方法中，~10 次独立的 LMDB 操作：
1. Engram 写入
2. Hopfield 模式添加（内存，快）
3. Temporal edges 写入（batch_entries 读 + add_edge 写 × 2）
4. PlanGate 向量运算（内存，快）
5. PlanIndex 更新（内存，快）
6. Dialogue 写入
7. **LMDB 事务提交（慢，每次 fsync）**

### 方案：合并为单次 LMDB 事务

```rust
// 改前：
let mut txn1 = self.storage.begin_write()?;  // engram
// ... write ...
txn1.commit()?;

let (before, after) = { /* read batch_entries */ };
let mut txn2 = self.storage.begin_write()?;  // temporal edge
// ... write ...
txn2.commit()?;

let mut txn3 = self.storage.begin_write()?;  // dialogue
// ... write ...
txn3.commit()?;

// 改后：一个事务
let mut txn = self.storage.begin_write()?;
self.storage.put_hippocampus(&mut txn, &id, &engram)?;
self.graph.add_edge(&mut txn, ...)?;
self.storage.put_dialogue(&mut txn, &turn)?;
txn.commit()?;
```

预期：perceive 15ms → ~1-3ms。
影响文件：`brain.rs` perceive 方法

---

## 四、ONNX 编码批量化

### 当前瓶颈

1000 条文本逐条编码，每次 ONNX session.run 独立调用，冷启动开销 × 1000。

### 方案

`onnx.rs` 增加 `encode_batch()` 方法：
```rust
pub fn encode_batch(&self, texts: &[&str]) -> Vec<Vec<f16>> {
    // 按 batch_size=32 分组
    // 每次 session.run 处理一组
    // ORT 原生支持 batch 维度
}
```

预期：编码 93ms/条 → ~5ms/条（batch_size=32）。
影响文件：`onnx.rs`

---

## 五、冲第一梯队总路线图

```
          现在                              中期 (1-2周)                    目标
      ┌─────────┐                    ┌──────────────────┐           ┌──────────┐
      │ R@5  10%│ ─── SparseIndex ──→│ R@5  60-80%      │── 调参 ──→│ R@5  >85%│
      │ perc 15ms│── 批量 LMDB     ──→│ perc  1-3ms      │           │ perc <1ms │
      │ dream  龟│── 批量+采样     ──→│ dream <100ms @1K │           │ dream 异步│
      └─────────┘                    └──────────────────┘           └──────────┘
```

| 指标 | 当前 | 目标（第一梯队） | 依赖 |
|------|------|---------------|------|
| R@5 | 10% | > 85% | SparseIndex + 双线合并 |
| perceive P95 | 25ms | < 3ms | 批量 LMDB |
| recall P50 | 1.9ms | < 2ms | ✅ 已达标 |
| Dream @ 1K | 1-5s | < 100ms | 批量 + 采样 |
| encode @ 1K | 93s | < 5s | ONNX batch |

---

## ✅ 行动清单

| # | 行动 | 影响文件 | 预期收益 | 风险 | 时间窗 |
|---|------|---------|---------|------|--------|
| 1 | Dream 批量 LMDB 事务 | `brain.rs` | 磁盘 1000→1 次 commit | 低 | 30min |
| 2 | Dream 采样替代全量 Hopfield | `brain.rs` | O(N²)→O(N·k), 20x+ | 低 | 1h |
| 3 | perceive 批量 LMDB 事务 | `brain.rs` | 15ms → 1-3ms | 低 | 1h |
| 4 | SparseIndex 解禁 + 接线 | `index.rs`, `brain.rs` | R@5 10% → 60%+ | 低 | 2h |
| 5 | 双线 recall 合并 | `brain.rs` recall | R@5 60% → 80%+ | 低 | 2h |
| 6 | ONNX batch 编码 | `onnx.rs` | encode 93ms → 5ms | 低 | 1h |
| 7 | Dream 异步化 | `brain.rs` | 不阻塞 perceive | 中 | 3h |
| 8 | 跑 full_benchmark 回归 | `full_benchmark.rs` | 验证全部指标 | — | 30min |

---

## ⚠️ 待确认 / 假设 / Non-goals

- **假设**：heed/LMDB 的 `EnvOpenOptions` 支持多读单写，Dream 异步化时不会死锁
- **假设**：SparseIndex 的 FNV-1a 哈希空间与 HybridEncoder 的 ngram 一致（需代码验证）
- **Non-goal**：不引入新依赖（FAISS/Milvus），MemHop 保持零外部依赖
- **Non-goal**：不改变公开 API（`store/recall/dream` 签名不变）

---

## 六、专业 Benchmark 设计（替代旧 `full_benchmark`）

### 旧 benchmark 的问题

| 问题 | 影响 |
|------|------|
| 100 条文本重复 10 次凑 1000 | 数据分布不真实 |
| 查询包含目标文档的原关键词 | 测的是关键词匹配，非语义检索 |
| 仅 10 条查询 | R@1 从 10%→20% 只需多命中一条，零统计信度 |
| 参考对比是别家在不同数据集上的分数 | 灾难级对比错误 |
| Dream 被关闭（interval=100000） | 测的是半成品 |
| 所有 10 条同主题文本都算"正确" | 评估太宽松 |

### 新 `professional_benchmark` 设计

**文件**：`memhop/src/bin/professional_benchmark.rs`
**运行**：`cargo run --release --features onnx --bin professional_benchmark`

| 维度 | 设计 |
|------|------|
| 语料 | 500 条独特中文文档，**25 个类别**，覆盖 AI/数据库/安全/教育/医疗/金融/法律等 |
| 查询 | 50 条语义查询，**零关键词重叠**（如"机器如何从大量数据中自动发现规律"对应 AI 类别） |
| 查询-文档关系 | 每条查询对应一个类别中的 20 条文档，其余 480 条为干扰 |
| 指标 | **NDCG@10** ± 标准差、R@1、R@5、R@10、P@10、MRR |
| Dream | **启用**，每 50 次 perceive 触发一次 |
| 多尺度 | 100 / 300 / 500 文档三个规模 |
| 对比基线 | **ONNX+BGE-M3** vs **Ngram** vs **Brute-force Cosine**（同数据、同向量、天花板） |
| 复现性 | 自包含（无外部下载），纯 Rust 实现 |

### 如何正确解读

```
ONNX vs Brute-force  = Hopfield 架构的召回损失
ONNX vs Ngram         = 语义编码的价值
Brute-force           = 理论天花板（给定同等 embedding 质量）
```

**不再引用任何外部系统的分数做假对比。**

### 验证命令

```bash
# 编译
cargo build --release --features onnx --bin professional_benchmark

# 运行（需 BGE-M3 ONNX 模型在 models/bge-m3/）
cargo run --release --features onnx --bin professional_benchmark

# 建议跑 3 次取平均以消除 Dream 时序抖动
for i in 1 2 3; do
  cargo run --release --features onnx --bin professional_benchmark
done
```

---

## 📚 数据来源 & 基准

- Benchmark 数据：`professional_benchmark` @ 100/300/500 docs, 50 语义查询
- BGE-M3 INT8 ONNX：`models/bge-m3/model.onnx` (544MB, MahradHosseini/bge-m3-onnx-int8)
- 旧 `full_benchmark` 定性不可靠，**仅保留作延迟指标参考，不采信其召回质量数据**
- 代码基准：v0.7.0 当前 HEAD

---

> 本报告由产品战略团队 AI 协作生成，重要决策请由产品负责人审定。
