# MemHop v0.5.2 — 多猫宇宙：协作、压缩、创新

> **发布日期**: 2026-05-24
> **核心主题**: 单大脑 → 多大脑协作系统，三层压缩漏斗，创新认知模块

---

## 新增模块

### SnapshotLayer — 三层上下文压缩漏斗 (`src/snapshot.rs`)

四种快照策略，控制多只猫协作时的上下文预算：

| 策略 | Token 预算 | 用途 |
|------|-----------|------|
| `Full` | ~200 tok/任务 | LLM 写完整简写 |
| `Diff` | ~30 tok/任务 | 只存与上一帧的差异 |
| `Anchor` | ~15 tok/任务 | 只写 recall 关键词 |
| `Testament` | ~40 tok/任务 | 写给下一个分身猫的经验 |

- `SnapshotLayer::take_snapshot()` 记录快照
- `evict()` 超 token 预算自动踢出旧快照

### CognitiveFingerprint — 记忆冲突检测 (`src/fingerprint.rs`)

- `MemoryFingerprint`: 记忆指纹（内容哈希 + 版本号 + 写入者）
- `check_conflict()`: 检测多猫写入冲突
- `resolve_conflict()`: 通过 Calibrator 仲裁冲突（KeepMine / KeepTheirs / Merge / KeepBoth）

### Swarm — 多猫编排 (`src/swarm/`)

三种协作模式：

| 模式 | 用途 |
|------|------|
| `MasterClone` | 主从分身，并行独立子任务 |
| `DualReview` | 双头互审，执行脑写 + 审查脑审 |
| `HiveMind` | 蜂巢共脑，私有短期 + 共享长期 |

核心结构：
- `Swarm`: 多脑编排器，含 `plan_and_execute()` 和 `dual_review()`
- `CloneCat`: 分身猫，独立 BrainLoop 实例
- `CognitiveLoadBalancer`: 负载均衡，低置信度自动任务重分配

### DreamMode — 空闲时巩固记忆 (`src/dream.rs`)

- 空闲超过 `idle_timeout_secs` 自动触发
- 三阶段循环：`replay_random` → `reinforce_weak` → `apply_decay`
- 巩固弱吸引子、衰减低频记忆
- `DreamReport` 输出每轮统计（replayed / reinforced / decayed）

### MemoryWormhole — 跨脑只读窥探 (`src/wormhole.rs`)

- `WormholeScope` 控制访问权限（按 layer / domain 过滤）
- `peek()` / `peek_topk()` 只读召回
- 设计原则：永远只读，不跨进程

### CollectiveIntuition — 群体直觉共识 (`src/intuition.rs`)

- 多只猫的结论取交集
- 关键词频率 ≥ `agreement_threshold` 视为共识
- 共识话题获得 `confidence_boost`

---

## 版本号

- `Cargo.toml`: 0.5.1 → 0.5.2
- `pyproject.toml`: 0.5.1 → 0.5.2

---

## 未纳入（留到后续版本）

- `MemoryVaccination` (vaccinate.rs): 分身出发前预注入知识 — 未实现
- 新模块的 Python 绑定导出 — 未实现，所有新模块仅在 Rust crate 内部可用
- pyo3 暴露 Swarm / CollaborationMode / SnapshotStrategy 等 Python API — 未实现

---

## 测试

- 271 个 Rust 测试全绿（含新增模块的单元测试）
- 10 个需要 pyo3 GIL 的集成测试标记 `#[ignore]`

---

## 已知限制

- `Swarm::plan_and_execute()` 中 `execute_parallel()` 实际为串行执行（名实不符）
- `Swarm::clone_brain()` 硬编码 `HttpThinker`，未通过 trait object 注入
- Dream / Wormhole 的集成测试需 pyo3 GIL 环境，当前 `#[ignore]`
