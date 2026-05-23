# MemHop v0.5.3 — "O(1) 人脑"闭环

> **发布日期**: 2026-05-24
> **核心主题**: 聚焦记忆引擎核心，删除编排层，本地化增强，安全修复

---

## 删除

### 删除 `swarm/` 模块（4 文件）
- `src/swarm/mod.rs` — `Swarm` / `CloneCat` / `CollaborationMode` 等类型
- `src/swarm/swarm.rs` — `plan_and_execute` / `dual_review`
- `src/swarm/clone.rs` — `CloneCat` + `SubTask`
- `src/swarm/load_balancer.rs` — `CognitiveLoadBalancer`

**理由**：Swarm 是 Agent 编排功能，不属于记忆引擎范畴。多脑编排应交给 MeowAgent 或独立的 `meow-orchestrator`。

### 删除 Wormhole（1 文件）
- `src/wormhole.rs` — `Wormhole` + `WormholeScope`

**理由**：与"砍掉跨窝功能"决策一致。

## 重构

### engine.rs God Object 拆分（2265 → 1906 行）
- `src/engine/mod.rs` — MemHopEngine pyclass + pymethods + 持久化帮助函数
- `src/engine/helpers.rs` — 独立辅助函数（generate_memory_id, now_millis, 时间/保护/重要性处理）
- `src/engine/filter.rs` — FilterCriteria + scope 解析 + scene gating

## 安全修复

### API key 不再可通过 Python 读取（G-02 合规）
- `src/http_thinker.rs`: `api_key` 字段从 `#[pyo3(get, set)]` 改为 `#[pyo3(set)]`，Python 用户只能设置不能读取

### 危险输入不再存入记忆
- `src/brain/brain_loop.rs`: `memory_history.push("用户: ...")` 移到 Gate danger detection 之后，被标记为危险的用户输入不会被写入 episode memory

## 增强

### Gate 激进过滤（FastPath）
- `src/brain/gate.rs`: 新增 `GateDecision` 枚举（FastPath / DeepPath / ReasoningPath）
- 新增 `Gate::decide()` 方法，基于检索置信度判断是否跳过 LLM 调用
- `BrainConfig.fast_path_threshold` 可配置（默认 0.85）

### Dream Mode 纯本地化
- `src/dream.rs`: 移除 Calibrator 依赖，Phase 2 `reinforce_weak` 改为纯统计评分（访问频率 + 时间衰减）
- 修复"伪随机"抽样——`replay_random` 现在使用 `rand::choose_multiple` 做真正的随机采样
- Dream Mode 不再发起任何网络请求

### 编译修复
- `src/brain/growth.rs`: `compress()` 方法移除未使用的 `Calibrator` 参数
- `src/lib.rs`: 移除传递给 `BrainLoop::new` 的旧版第 5 参数

## 版本号

- `Cargo.toml`: 0.5.2 → 0.5.3
- `pyproject.toml`: 0.5.2 → 0.5.3

## 测试

- 234 个 Rust 测试全绿
- 0 failure, 1 ignored（需 pyo3 GIL 的集成测试）

## 延续到下个版本的项

- **纠缠图动态扩散**（`Hopfield::spread_activation()`）：需新增 BFS 连接遍历
- **场景路由增强**（`SceneState.recent_turn_summary`）：需新增滚动摘要
- **Rust 异步并发 LLM**（`AsyncHttpThinker`）：需新增 async 变体 + batch 调用
