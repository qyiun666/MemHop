# MemHop 开发路线图

> **适用对象**：接手 MemHop 开发的 AI 或开发者。本文档是"照着做"的执行手册。

---

## 📌 项目现状 (v0.1.0 骨架)

```
已完成：
✅ types.py      — Memory, EncoderOutput, EncoderConfig 数据类型
✅ hopfield.py   — Modern Hopfield Network 核心 (one-step recall)
✅ storage.py    — LMDB 三子库持久化 (patterns/blobs/meta)
✅ encoder.py    — 可插拔编码器接口 (ApiEncoder / LocalEncoder / MockEncoder)
✅ engine.py     — MemHopEngine 编排层 (remember/recall/forget/search)
✅ pyproject.toml — 项目配置，pip install -e 可用
✅ __init__.py   — memhop.open() 入口

待完成：
⚠️ Mock 编码器是随机向量 — 无法做语义召回验证
⚠️ 两阶段检索 (sparse + MHN) 是 TODO 空壳
⚠️ BGE-M3 本地编码器未验证 (缺 onnxruntime 依赖)
⚠️ 0 个测试
⚠️ 0 个性能基准
⚠️ 中文短词验证未跑
```

## 📖 关键文档

| 文档 | 路径 | 用途 |
|------|------|------|
| 系统设计 | `DESIGN.md` | 架构决策、算法推导、ADR |
| 本文档 | `ROADMAP.md` | 开发任务清单 |
| 项目介绍 | `README.md` | 快速开始 |

**开发原则**：先读 `DESIGN.md` 理解"为什么"，再按 `ROADMAP.md` 执行"做什么"。

---

## Phase 1: Python 原型（当前阶段）

### Task 1.1 — 编码器验证：API 模式真实调用

**目标**：验证 ApiEncoder 能正确调用 DeepSeek Embedding API，返回语义有意义的密集向量。

**当前状态**：代码已写 (`encoder.py:ApiEncoder`)，Mock 编码器是随机向量。

**执行步骤**：
1. 确保 `DEEPSEEK_API_KEY` 环境变量已设置
2. 创建 `tests/test_encoder.py`，编写测试：

```python
# 测试用例
def test_api_encoder_basic():
    enc = ApiEncoder()
    out = enc.encode("今天天气真好")
    assert out.dense.shape == (1024,)
    assert -1.0 <= out.dense[0] <= 1.0

def test_api_encoder_empty():
    enc = ApiEncoder()
    out = enc.encode("")
    assert out.dense.shape == (1024,)
    assert np.allclose(out.dense, np.zeros(1024))

def test_semantic_similarity():
    enc = ApiEncoder()
    a = enc.encode("今天吃了豆浆油条").dense
    b = enc.encode("今天早餐吃了什么").dense
    c = enc.encode("火星探测任务").dense
    sim_ab = float(a @ b)  # 归一化后 dot = cosine
    sim_ac = float(a @ c)
    assert sim_ab > sim_ac, f"语义相似度异常: ab={sim_ab:.3f} ac={sim_ac:.3f}"
```

3. 运行：`pytest tests/test_encoder.py -v`
4. **验收标准**：`sim_ab > 0.7`，`sim_ac < 0.3`

**涉及文件**：`tests/test_encoder.py`（新建）

**依赖**：`DEEPSEEK_API_KEY` 环境变量

---

### Task 1.2 — 语义召回端到端验证

**目标**：接入真实 API 编码器后，验证 `remember → recall` 全链路语义召回正确。

**执行步骤**：
1. 创建 `tests/test_e2e.py`
2. 记住 10 条不同主题的记忆，用语义相近的 cue 召回，验证返回正确记忆：

```python
def test_e2e_semantic_recall():
    db = memhop.open(":temp:", encoder=EncoderConfig(mode="api"))
    
    db.remember("今天早上吃了豆浆油条", meta={"tags": ["早餐"]})
    db.remember("昨天下午开了三小时架构评审会", meta={"tags": ["工作"]})
    db.remember("周末去了海边游泳", meta={"tags": ["休闲"]})
    
    r = db.recall("今天吃了什么早餐")
    assert r is not None
    assert "豆浆油条" in r.text
    assert r.confidence > 0.7

def test_no_match_returns_none():
    db = memhop.open(":temp:", encoder=EncoderConfig(mode="api"))
    db.remember("今天天气很好")
    
    r = db.recall("量子力学的基本原理是什么")
    assert r is None
```

3. `pytest tests/test_e2e.py -v`
4. **验收标准**：语义相近的 cue 召回正确记忆，无关内容返回 None

**涉及文件**：`tests/test_e2e.py`（新建）

**依赖**：Task 1.1

---

### Task 1.3 — Mock 编码器改名为确定性伪语义编码器

**目标**：让 Mock 编码器不依赖 API Key 也能做基本的语义区分，便于本地快速迭代测试。

**执行步骤**：
1. 重写 `MockEncoder.encode()` — 不再用随机数，改为字符级嵌入：

```python
# 思路：取 text 字符的 Unicode 码点，投影到 1024 维空间
# 并非真正语义编码，但相同文本相同向量，相似文本有较高余弦相似度
class MockEncoder(Encoder):
    def encode(self, text: str) -> EncoderOutput:
        import hashlib
        # 用 rolling hash 生成 deterministic vector
        h = hashlib.sha256(text.encode()).digest()
        vec = np.zeros(VECTOR_DIM, dtype=np.float32)
        for i in range(32):
            idx = (i * 32) % VECTOR_DIM
            vec[idx] = (h[i] - 127.5) / 127.5  # [-1, 1]
        # 平滑: 对相邻维度插值
        from scipy.ndimage import gaussian_filter1d
        vec = gaussian_filter1d(vec, sigma=2.0).astype(np.float32)
        vec /= np.linalg.norm(vec) + 1e-8
        return EncoderOutput(dense=vec)
```

2. 验证短词的确定性：`encode("豆浆油条")` 每次都返回相同向量
3. 验证 recall 通路：mock 模式下 `recall("今天吃了什么")` 不再总是返回 None

**涉及文件**：`src/memhop/encoder.py`，`tests/test_encoder.py`
**依赖**：无

---

### Task 1.4 — 两阶段检索实现

**目标**：实现 Sparse 粗筛 → MHN 精排的两阶段管线，解决中文短词场景。

**当前状态**：`engine.py:recall()` 中 sparse 粗筛是 TODO 空壳。

**执行步骤**：
1. 在 `engine.py` 中实现 SparsScreener 类：

```python
class SparseScreener:
    """用 sparse 词汇向量做粗筛，桶内候选 → MHN 精排"""
    
    def screen(
        self,
        query_sparse: dict[str, float],
        mhn: ModernHopfield,
        max_candidates: int = 500,
    ) -> list[str]:
        # 1. 对每个记忆的 sparse 向量计算 Jaccard 或加权重叠
        # 2. 取 top max_candidates
        # 3. 返回候选 memory_id 列表
        ...
```

2. 修改 `MemHopEngine.recall()`：当 `output.sparse` 存在且记忆数 > 500 时，先粗筛再精排
3. 改造 `ModernHopfield` 支持子集召回：新增 `recall_subset(query, candidate_indices)`
4. 写测试验证两阶段逻辑

**验收标准**：
- 中文短词 "早餐" 召回正确率 > 90%（需真实向量）
- recall 延迟保持 < 5ms

**涉及文件**：`src/memhop/engine.py`，`src/memhop/hopfield.py`，`tests/test_two_stage.py`

---

### Task 1.5 — 核心测试套件（10 用例）

**目标**：覆盖 DESIGN.md §6.1 的 10 个核心测试用例。

**新建文件**：`tests/test_core.py`

| # | 测试场景 | 预期结果 |
|---|---------|---------|
| 1 | 基本写入召回 | `remember → recall` 返回相同记忆 |
| 2 | 语义相似召回 | "吃了什么" → "豆浆油条" |
| 3 | 无匹配 | `recall("无关话题") → None` |
| 4 | 多记忆区分 | 100 条相似记忆 → 每条精确区分 |
| 5 | 大规模压力 | 1000 条记忆 → `recall < 5ms` |
| 6 | 并发写入 | 多线程 `remember` → 无数据损坏 |
| 7 | 崩溃恢复 | kill -9 → 重启后数据完整 |
| 8 | 中文短词 | "早餐" → 正确记忆 |
| 9 | 遗忘 | `forget → recall → None` |
| 10 | 更新 | `update` 文本 → `recall` 返回新内容 |

**涉及文件**：`tests/test_core.py`（新建）
**依赖**：Task 1.1 (需要真实向量做 #2/#4/#8)

---

### Task 1.6 — 性能基准

**目标**：实现 DESIGN.md §6.2 的对比基准。

**命令**：`python examples/benchmark.py`

| 基准 | MeowAgent FTS5 (当前) | MemHop 目标 |
|------|----------------------|-------------|
| 1K 记忆 | ~2ms | < 1ms |
| 10K 记忆 | ~15ms | < 2ms |
| 100K 记忆 | ~150ms | < 5ms |

**涉及文件**：`examples/benchmark.py`（新建），`tests/test_perf.py`（新建）

---

### Task 1.7 — BGE-M3 本地编码器验证

**目标**：验证 LocalEncoder 在 macOS ARM64 上正常工作。

**执行步骤**：
1. `pip install memhop[local]` 安装 onnxruntime + FlagEmbedding
2. 下载 BGE-M3 模型 (~2.2GB 原始, ~600MB fp16)
3. 写 `tests/test_encoder_local.py`，验证：
   - `encode()` 返回 dense(1024) + sparse(dict) + multi(N, 1024)
   - 编码延迟 < 10ms
   - 中文语义相似度与 API 模式可比

**涉及文件**：`tests/test_encoder_local.py`（新建）
**依赖**：Task 1.1

---

### Task 1.8 — 边界场景与错误处理

**目标**：处理异常路径，让库更健壮。

**执行步骤**：
1. API 调用失败 → 重试 + 兜底错误
2. 磁盘满 → 捕获 LMDB MDB_MAP_FULL 异常
3. 空数据库 `recall()` → 返回 None
4. 超大文本 (>100KB) → 截断警告
5. 重复 ID `remember()` → 覆盖或报错

**涉及文件**：`src/memhop/engine.py`，`src/memhop/storage.py`，`tests/test_edge_cases.py`

---

## 任务依赖图

```
Task 1.1 (API编码器验证)
  ├── Task 1.2 (端到端语义召回)
  ├── Task 1.5 (核心测试 #2,#4,#8)
  │     └── Task 1.6 (性能基准)
  └── Task 1.7 (BGE-M3本地编码器)

Task 1.3 (Mock改造) — 独立，无依赖

Task 1.4 (两阶段检索) — 依赖 Task 1.1 或 Task 1.3

Task 1.8 (边界错误处理) — 依赖 Task 1.2
```

---

## 可并行执行

- **组 A**：Task 1.1 → 1.2 → 1.5
- **组 B**：Task 1.3 → 1.4
- **组 C**：Task 1.7（安装 BGE-M3 后独立测）

组 A / B / C 之间无依赖，可并行。

---

## 快速启动

```bash
cd /Volumes/zt_hd/projects/meow/memhop

# 安装
pip install -e ".[dev]"

# 跑现有代码
python -c "
import memhop
db = memhop.open('test.db', encoder=memhop.EncoderConfig(mode='mock'))
db.remember('hello world')
print(db.stats)
db.close()
"

# 开发流程
# 1. 读相关源码 (src/memhop/)
# 2. 读 DESIGN.md 对应章节
# 3. 按上面 Task 执行
# 4. pytest tests/ -v 验证
```

---

## 版本发布计划

| 版本 | 内容 | 发布条件 |
|------|------|---------|
| **v0.1.0** ✅ | 项目骨架 | 代码结构就绪 |
| **v0.2.0** | API 编码器可用 | Task 1.1 + 1.2 通过 |
| **v0.3.0** | 两阶段检索 | Task 1.4 通过 |
| **v0.4.0** | 测试套件完整 | Task 1.5 全部通过 |
| **v0.5.0** | BGE-M3 本地可用 | Task 1.7 通过 |
| **v1.0.0** | Phase 1 完成 | 全部 8 个 Task 通过 + 性能达标 |

---

> 🐱 文档维护：Zhen · 工程督导 | 最后更新 2026-05-19
