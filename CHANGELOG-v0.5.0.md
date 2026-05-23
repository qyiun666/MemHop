# MemHop v0.5.0 — BrainLoop 自循环 Agent 大脑

> 发布日期: 2026-05-23
> 前置版本: v0.4.0 (plasticity + IDF + scene_gating + benchmark)

---

## 概述

v0.5.0 将 MemHop 从 Rust 记忆引擎升级为**完整的自循环 Agent 大脑内核**。新增 5 大脑器官、Thinker trait 注入、流式 LLM 推理和自生长系统。MeowAgent 退化为 ~50 行 Python 身体驱动层。

### 架构翻转

```
v0.4.0: MeowHop = Rust 记忆引擎 (remember/recall/plasticity/IDF)
v0.5.0: MeowHop = Rust 完整大脑 (5 器官 + 3 自生长 + Thinker trait)
        MeowAgent = Python 身体层 (~50 行)
```

| 维度 | v0.4.0 | v0.5.0 |
|------|--------|--------|
| 定位 | Rust 记忆引擎 | **Rust 完整大脑** |
| 循环控制 | 无 | **BrainLoop 状态机** |
| LLM 推理 | 外部 Python 调用 | **Thinker trait 注入 (Rust reqwest)** |
| 自生长 | 无 | **compress + consolidate** |
| 世界观 | 无 | **cortex CRUD** |
| 输出 | Memory 对象 | **BrainAction (Streaming/NeedBody/Done)** |

---

## 新增: BrainLoop — 11 步认知循环

`src/brain/brain_loop.rs` — 完整状态机，11 步认知管道：

```
Step 1:  额叶热点更新
Step 2:  小脑本能反射 → 命中则直接 Done
Step 3:  Gate 路由判决 (Fast/Deep/Reasoning)
Step 4:  海马体 O(1) 召回 (recall_with_plasticity)
Step 5:  Gate 置信度过滤 + 风险检测
Step 6:  皮层世界观注入
Step 7:  Prompt 组装
Step 8:  大脑 LLM 思考循环 (最多 max_attempts 次)
Step 9:  Gate 结果再审查 + 路由升级
Step 10: 工具/追问检测 → NeedBody
Step 11: finalize() → 写入记忆 + 压缩 + 巩固 → Done
```

### 三个入口点

| 方法 | 说明 |
|------|------|
| `process(user_input)` | 非流式处理，返回 BrainAction |
| `process_streaming(user_input, on_chunk)` | 流式处理，每 token 通过回调推送 |
| `feed_body_result(results)` | 身体结果反馈，继续推理 |

### BrainAction 三种变体

| 变体 | 场景 |
|------|------|
| `Streaming { chunk }` | 流式 token，直接推给用户 |
| `NeedBody { actions, context }` | 需要身体动作（工具/追问/确认） |
| `Done { for_user, notifications }` | 循环完成 |

---

## 新增: Gate — 路由 + 审查

`src/brain/gate.rs` — 丘脑（路由）+ 杏仁核（安全）合并模块：

| 方法 | 说明 |
|------|------|
| `decide_route(input) → Route` | 路由判决 (Fast/Deep/Reasoning) |
| `filter_by_confidence(memories, threshold)` | 置信度过滤 |
| `detect_danger(input) → Option<Warning>` | 风险检测（注入/破坏/有害内容） |
| `validate_result(result, memories)` | 结果验证（长度/重复/关键词重叠） |
| `upgrade_route(route)` | 升级策略 (Fast → Deep → Reasoning) |
| `needs_clarification(result)` | 是否需要追问 |
| `block_chunk(chunk)` | 流式 token 安全过滤 |
| `avg_confidence() → f32` | 平均置信度 |

---

## 新增: Cortex — 世界观 CRUD

`src/brain/cortex.rs` — 世界观以 `layer="cortex"` 的特殊记忆存储：

| 方法 | 说明 |
|------|------|
| `remember_belief(key, value)` | 写入世界观条目 |
| `recall_belief(key) → Option<String>` | 读取世界观 |
| `current_beliefs() → Vec<String>` | 获取当前所有活跃信念 |
| `get_relevant_beliefs(input) → Vec<String>` | 根据输入召回相关信念 |

---

## 新增: Prompt — 模板填充

`src/brain/prompt.rs` — Prompt 组装 + 输出格式化：

| 方法 | 说明 |
|------|------|
| `assemble(input, route, memories, worldview) → String` | 组装完整 prompt |
| `refine(prompt, reason) → String` | Gate 退回后 refined prompt |
| `format_output(result) → String` | 输出格式化 |

---

## 新增: Growth — 自生长

`src/brain/growth.rs` — 两个确定性自生长能力：

| 方法 | 说明 |
|------|------|
| `compress(engine, thinker) → u64` | 子句匹配去重 + 可选 LLM summarize |
| `consolidate(engine) → u32` | n-gram 聚类 → knowledge 节点提取 |

自动触发：每 `compress_threshold` 轮在 `finalize()` 中触发 compress + consolidate。

---

## 新增: Thinker + Cerebellum traits

`src/thinker.rs` — 可注入的 LLM 推理和本能反射 trait：

```rust
pub trait Thinker: Send + Sync {
    fn think_deep(&self, prompt: &str) -> Result<String, BrainError>;
    fn think_fast(&self, prompt: &str) -> Result<String, BrainError>;
    fn think_stream(&self, prompt: &str, on_chunk: &mut dyn FnMut(&str)) -> Result<String, BrainError>;
}

pub trait Cerebellum: Send + Sync {
    fn reflex(&self, input: &str) -> Option<String>;
}
```

---

## 新增: HttpThinker + FastReflex

`src/http_thinker.rs` — reqwest 实现的 Thinker，支持任意 OpenAI-compatible API：

| 方法 | 说明 |
|------|------|
| `think_deep(prompt)` | 大模型推理 (gpt-4o) |
| `think_fast(prompt)` | 小模型快速响应 (gpt-4o-mini) |
| `think_stream(prompt, callback)` | 流式推理，SSE 解析 |

`src/fast_reflex.rs` — 规则匹配实现的 Cerebellum：

- 内置问候/致谢/状态查询规则
- `add_rule(pattern, response)` — Python 可扩展
- 子串匹配，首胜返回

---

## 新增: MeowAgentDriver (~50 行 Python)

`python/memhop/driver.py` — 纯 Python 身体驱动层：

```python
driver = MeowAgentDriver(llm_endpoint=..., api_key=..., model=...)

# 非流式
response = driver.handle_message("Hello!")

# 流式
response = driver.handle_message_streaming("Tell me a story", on_chunk)
```

---

## 新增类型

| 类型 | 说明 |
|------|------|
| `BrainAction` | 三种输出变体 (Streaming/NeedBody/Done) |
| `BodyAction` | 四种身体动作 (Tool/AskUser/HearMore/ReadFile) |
| `BrainNotifications` | 元通知 (new_knowledge_count/compression_triggered) |
| `CognitionHealth` | 认知健康指标 (llm_calls/tokens_used/avg_confidence) |
| `BrainConfig` | BrainLoop 配置 (max_attempts/confidence_threshold/...) |
| `BrainError` | 错误类型 (ThinkerFailed/GateRejected/MaxAttemptsExceeded) |
| `Route` | 路由类型 (Fast/Deep/Reasoning) |
| `StrategyHint` | 策略提示 (SwitchToFastModel/SwitchToDeepModel/...) |
| `BodyResult` | 身体执行结果反馈结构 |

---

## 文件改动

### 新增文件

```
src/brain/
├── mod.rs                # 模块入口 + 公共类型导出
├── brain_loop.rs         # 状态机主循环 + 额叶热点
├── gate.rs               # 路由判决 + 置信度过滤 + 风险检测
├── cortex.rs             # 世界观 CRUD
├── prompt.rs             # Prompt 组装 + 输出格式化
└── growth.rs             # compress + consolidate

src/thinker.rs            # Thinker + Cerebellum trait 定义
src/http_thinker.rs       # HttpThinker (reqwest 实现)
src/fast_reflex.rs        # 规则反射

python/memhop/driver.py   # MeowAgentDriver (~100 行)

tests/test_brain.py       # 脑循环集成测试 (30+ 测试用例)
CHANGELOG-v0.5.0.md       # 本文件
```

### 修改文件

| 文件 | 改动 |
|------|------|
| `src/lib.rs` | 注册 brain/thinker/http_thinker/fast_reflex 模块 + PyBrainLoop pyo3 绑定 |
| `src/types.rs` | 新增 BrainAction/BodyAction/BrainNotifications/CognitionHealth/BrainConfig/BrainError/Route/StrategyHint 类型 |
| `Cargo.toml` | version → 0.5.0, 新增 reqwest 依赖 |
| `pyproject.toml` | version → 0.5.0 |
| `python/memhop/__init__.py` | 新增 BrainLoop/BrainConfig/BrainAction 等导出 |
| `python/memhop/__init__.pyi` | 新增 v0.5.0 类型桩 |
| `tests/test_acceptance.py` | 版本号更新为 0.5.0 |

---

## 依赖变更

| 依赖 | 变更 | 说明 |
|------|------|------|
| reqwest | 新增 v0.12 | blocking + json features, HTTP client for LLM API |

---

## 测试覆盖

| 范围 | 结果 |
|------|------|
| Rust unit tests (brain_loop.rs) | 28 tests, all passed |
| Rust unit tests (gate.rs) | 16 tests, all passed |
| Python integration tests (test_brain.py) | 30+ tests covering BrainLoop/reflex/danger/streaming/feed_body/compat |
| v0.4.0 backward compat tests | 22 acceptance tests still pass |

---

## 向后兼容

| 功能 | v0.4.0 行为 | v0.5.0 行为 |
|------|------------|------------|
| `MemHopEngine` | 全部 API | 完全保留，零改动 |
| `memhop.open()` | 打开引擎 | 不变 |
| `remember()`/`recall()` | 核心操作 | 不变 |
| Python package | `_core` module | 新增 BrainLoop 等，旧 API 不受影响 |

---

## 不做什么

- ❌ 不做世界观演化（信念冲突检测 + 反思更新）— 留 v0.6.0+
- ❌ 不做用户画像更新 — 留 v0.6.0+
- ❌ 不做角色涌现 — 留 v0.6.0+
- ❌ 不做 BrainLoop 内部 async — 状态机同步，外部驱动 async
- ❌ 不修改 v0.4.0 的 plasticity/gating/IDF 核心逻辑

---

> 🤖 Generated with [Qoder](https://qoder.com)
