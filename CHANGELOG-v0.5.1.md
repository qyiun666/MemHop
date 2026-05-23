# MemHop v0.5.1 — 双模型架构：Calibrator 校准层

> 发布日期: 2026-05-23
> 前置版本: v0.5.0 (BrainLoop + Thinker trait + 5 器官)

---

## 概述

v0.5.0 只有一个 `Thinker` trait（主模型），所有任务——深度推理、记忆校准——全部走同一个大模型。

v0.5.1 引入第二个可选模型 **Calibrator**，专门处理记忆校准任务（importance 标注、语义去重、链接验证）。主模型专注深度推理，校准模型做简单判断——小模型更快、更便宜。

### 架构变化

```
v0.5.0: BrainLoop → thinker (单模型，什么都做)
v0.5.1: BrainLoop → ModelRouter
                     ├── thinker (必选)：深度推理、流式输出、gate 审查
                     └── calibrator (可选)：importance 标注、语义去重、链接验证
```

| 维度 | v0.5.0 | v0.5.1 |
|------|--------|--------|
| 模型数量 | 1 (Thinker) | 2 (Thinker + 可选 Calibrator) |
| 校准模型 | 无（用 Thinker 兼任） | 可选专用小模型 |
| 校准成本 | 高（大模型做小事） | 低（小模型做小事） |
| 路由 | 直接调 thinker | ModelRouter 按任务类型分配 |
| 向后兼容 | — | calibrator 不传则 ThinkerBackedCalibrator 回退 |

---

## 新增: Calibrator trait

`src/calibrator.rs` — 校准模型 trait，专门处理记忆维护任务：

| 方法 | 说明 |
|------|------|
| `cal_importance(text, context) → f32` | 标注单条记忆的重要性 (0.0~1.0) |
| `cal_dedup(text_a, text_b) → DedupResult` | 判断两条记忆是否语义重复 |
| `cal_link(from, to, relation) → LinkValidation` | 验证链接关系是否语义有效 |
| `cal_batch_importance(items) → Vec<f32>` | 批量标注（默认逐条调用，可重写） |

### 回退实现: ThinkerBackedCalibrator

当用户没配 calibrator 时，自动用主模型（Thinker）做校准。通过 prompt 模板将校准任务转化为 text 请求。

### Result 类型

| 类型 | 字段 |
|------|------|
| `DedupResult` | `is_duplicate`, `confidence`, `merge_suggestion` |
| `LinkValidation` | `is_valid`, `confidence` |
| `CalibrationContext` | `domain`, `layer`, `recent_count` |

---

## 新增: HttpCalibrator

`src/http_calibrator.rs` — `reqwest` 实现的 Calibrator，支持任意 OpenAI-compatible API：

- `HttpCalibrator::new(endpoint, api_key, model)` — 构造
- 支持 Ollama（api_key 不传则不发送 Authorization header）
- 与 HttpThinker 共享相同请求模式
- pyo3 绑定，可从 Python 直接构造

---

## 新增: ModelRouter — 任务路由

`src/router.rs` — 根据任务类型分配模型：

| 方法 | 路由目标 |
|------|---------|
| `route_reasoning(prompt, route)` | Thinker（Fast→think_fast, Deep/Reasoning→think_deep） |
| `route_stream(prompt, on_chunk)` | Thinker（think_stream） |
| `route_calibrate_importance(text, ctx)` | Calibrator |
| `route_calibrate_batch(items)` | Calibrator |
| `route_calibrate_dedup(a, b)` | Calibrator |
| `route_calibrate_link(from, to, rel)` | Calibrator |

### 路由规则

| 任务 | 目标模型 | 理由 |
|------|---------|------|
| Gate 路由判决 | thinker | 需要理解用户意图 |
| 大脑主推理 | thinker | 核心推理能力 |
| Gate 结果审查 | thinker | 需要判断推理质量 |
| importance 标注 | calibrator | 简单判断，小模型足够 |
| 语义去重 | calibrator | 简单判断，小模型足够 |
| 链接验证 | calibrator | 简单判断，小模型足够 |

---

## 新增: CalibrationEngine — 校准任务

`src/calibration.rs` — 记忆维护任务引擎：

| 方法 | 说明 |
|------|------|
| `run_importance_scoring(engine, router, threshold)` | 扫描低 importance 记忆，用 calibrator 重新标注 |
| `run_semantic_dedup(engine, router, max_check)` | 检查最近 N 条记忆的语义重复，重复则标记 dormant |
| `run_link_validation(engine, router, max_check)` | 验证链接关系有效性，移除无效链接 |

触发机制：每 `calibrate_threshold` 轮在 `BrainLoop::finalize()` 中自动触发。默认阈值 20。

---

## 新增类型

| 类型 | 说明 |
|------|------|
| `ModelSlot` | 模型配置槽位，位置决定角色（[0]=thinker, [1]=calibrator） |
| `Calibrator` | 校准模型 trait |
| `ModelRouter` | 任务路由器 |
| `CalibrationEngine` | 校准任务执行引擎 |
| `DedupResult` | 语义去重结果 |
| `LinkValidation` | 链接验证结果 |
| `CalibrationContext` | 校准上下文 |

---

## 文件改动

### 新增文件

```
src/calibrator.rs        # Calibrator trait + ThinkerBackedCalibrator + 单元测试
src/http_calibrator.rs   # HttpCalibrator (reqwest 实现) + 单元测试
src/router.rs            # ModelRouter + 单元测试
src/calibration.rs       # CalibrationEngine + 集成测试
CHANGELOG-v0.5.1.md      # 本文件
```

### 修改文件

| 文件 | 改动 |
|------|------|
| `Cargo.toml` | version → 0.5.1 |
| `pyproject.toml` | version → 0.5.1 |
| `src/lib.rs` | 注册 calibrator/router/calibration/http_calibrator 模块 + PyBrainLoop 双模型构造 + pyo3 导出 |
| `src/types.rs` | BrainError 新增 CalibratorFailed/ParseError 变体 + BrainConfig 新增 calibrate_threshold + ModelSlot 类型 |
| `src/brain/brain_loop.rs` | BrainLoop 接入 ModelRouter + finalize() 集成校准触发器 |
| `src/brain/growth.rs` | compress 签名从 Thinker 改为 Calibrator |
| `python/memhop/__init__.py` | 导出 HttpCalibrator + ModelSlot, version → 0.5.1 |
| `python/memhop/driver.py` | MeowAgentDriver 新增双模型构造路径 |
| `tests/test_acceptance.py` | version → 0.5.1 |
| `tests/test_brain.py` | version → 0.5.1 |

---

## 测试覆盖

| 范围 | 结果 |
|------|------|
| Rust unit tests (calibrator.rs) | 12 tests, all passed |
| Rust unit tests (http_calibrator.rs) | 8 tests, all passed |
| Rust unit tests (router.rs) | 8 tests, all passed |
| Rust unit tests (calibration.rs) | 4 tests, all passed |
| Rust 全量测试 | 153 tests, all passed |

---

## 向后兼容

| 功能 | v0.5.0 调用方式 | v0.5.1 行为 |
|------|----------------|------------|
| `BrainLoop::new(thinker, cerebellum, config)` | 单模型 | 兼容，不传 calibrator 则回退 |
| `MeowAgentDriver(llm_endpoint, api_key, model)` | 单模型 | 兼容，保持 v0.5.0 签名 |
| `BrainConfig` | 全部字段 | 兼容，新增 calibrate_threshold |

---

## 不做什么

- ❌ 不做 Calibrator 的训练/微调（只做推理调用）
- ❌ 不做校准结果的人工反馈循环（v0.6.0+）
- ❌ 不做校准模型的自动选择（用户手动配，或用默认回退）
- ❌ 不改 Thinker trait 签名（v0.5.0 API 完全兼容）
- ❌ 不做校准任务的持久化队列（校准是 best-effort）

---

## 推荐的校准模型

| 模型 | 参数量 | 推理速度 | 中文 | 推荐部署 |
|------|--------|---------|------|---------|
| **Qwen2.5-0.5B** | 0.5B | ~500 tok/s (GPU) / ~15 tok/s (CPU) | 好 | `ollama pull qwen2.5:0.5b` |
| **Qwen2.5-1.5B** | 1.5B | ~300 tok/s (GPU) / ~8 tok/s (CPU) | 好 | Ollama 本地 |
| **TinyLlama-1.1B** | 1.1B | ~400 tok/s (GPU) | 一般 | Ollama 本地 |

---

> 2026-05-23
> 核心思路：**思考用大模型，校准用小模型，没有小模型就用大模型**
> 向后兼容：v0.5.0 的 BrainLoop 调用方式完全不变
> 🤖 Generated with [Qoder](https://qoder.com)
