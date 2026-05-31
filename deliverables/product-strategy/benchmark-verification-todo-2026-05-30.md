# Benchmark 重构验证报告 & 待办清单

**日期**：2026-05-30
**基于**：`benchmark-optimization-prd-2026-05-30.md` (v2)
**验证对象**：其他 AI 完成的 benchmark 框架重构

---

## 📌 总体完成度

| 维度 | 完成度 | 说明 |
|------|--------|------|
| P0 代码实现 | **80%** | 核心框架 + 双编码器完成，但旧文件未清理 |
| P0 数据产出 | **0%** | 没有跑出任何新的 benchmark 报告 |
| P1 功能 | **0%** | LoCoMo / DMR adapter 未实现 |

---

## ✅ 已完成（不需改）

| # | 内容 | 验证细节 |
|---|------|---------|
| 1 | **`run_benchmark.py`** 统一入口 | 640 行，CLI 支持 `--encoder bge-m3/dual-small`、`--datasets lme-s,nfcorpus,c-mteb`、`--modes retrieval,associative`、`--subset`、`--compare`、`--dream/--no-dream`。`MemHopMCPRunner` 走 MCP-only 路径 ✅，每轮清库 ✅ |
| 2 | **`encoders/dual_small.py`** 双编码器 | CJK >30% 判断 ✅，分 tree 路由（zh 512d / en 384d）✅，`encode_many` 批量编码 ✅，`info` 属性 ✅ |
| 3 | **`adapters/schema.py`** 统一报告 | `BenchmarkResult` + `EncoderInfo` + `DatasetInfo` + `SystemInfo` + `LatencyInfo`，含递归序列化/反序列化 ✅ |
| 4 | **`mcp_client.py`** 增强 | 新增 `store_knowledge()`、`query_vector`、`tree`、`kind_filter`、`env_extra` ✅ |
| 5 | **`quality/metrics.py`** | `aggregate_metrics` 输出 mean+std ✅，标准 IR 指标 ✅ |
| 6 | **`adapters/c_mteb_adapter.py`** | 完整实现 `load_all_c_mteb_retrieval` ✅ |
| 7 | **`deprecated/`** 目录 | 5 个旧文件已移入 ✅ |

---

## ❌ 待修复 / 待完成

### P0 — 必须做（不完成 = 框架不可用）

#### P0-1：清理根目录旧脚本（冲突源）

**问题**：根目录和 `deprecated/` 同时存在以下文件，使用者会困惑"跑哪个"：

| 需删除的文件（根目录） | 原因 |
|------------------------|------|
| `benchmarks/run_lme.py` | 走 Rust quality_bench 直调，绕过 MCP，结果不可信 |
| `benchmarks/run_lme_mcp.py` | per-session 存储（旧），R@1=16.7% 就是它跑的 |
| `benchmarks/compare_encoders.py` | 旧脚本，功能已由 run_benchmark.py --compare 覆盖 |
| `benchmarks/run_longmemeval.py` | 旧脚本，功能已由 run_benchmark.py --datasets lme-s 覆盖 |
| `benchmarks/run_all.py` | turn-level 评估（应改 aggregated_sessions），且 chunking 逻辑按 200 字符乱切 |
| `benchmarks/run_mcp_models.py` | 60 行，硬编码路径，功能已由 run_benchmark.py 覆盖 |
| `benchmarks/run_multi_model.py` | 83 行，硬编码路径，功能已由 run_benchmark.py 覆盖 |
| `benchmarks/run_bge_m3_baseline.py` | 临时脚本，功能已由 run_benchmark.py 覆盖 |

**操作**：删除以上文件（deprecated/ 里的备份保留即可）。

#### P0-2：清理测试/诊断脚本

| 需移动到 `benchmarks/tools/` 或删除 | 说明 |
|--------------------------------------|------|
| `benchmarks/test_chunking.py` | 开发调试用，非正式 benchmark |
| `benchmarks/test_onnx_startup.py` | 同上 |
| `benchmarks/test_recall_fix.py` | 同上 |
| `benchmarks/test_recall_fix_v2.py` | 同上 |
| `benchmarks/diag_similarity.py` | 诊断工具，非正式 benchmark |

**操作**：移入 `benchmarks/tools/` 目录，或直接删除。

#### P0-3：抽取 LME adapter

**问题**：LME 数据加载逻辑直接内嵌在 `run_benchmark.py` 的 `run_lme_s()` 里（约 70 行），但 PRD 设计是每个数据集一个 adapter。

**操作**：
1. 创建 `benchmarks/adapters/lme_adapter.py`
2. 将 `run_lme_s()` 中的数据加载 + 预处理逻辑移入 adapter
3. `run_benchmark.py` 中调用 adapter 的标准接口
4. 参照 `c_mteb_adapter.py` 的模式

#### P0-4：修复 LME 数据路径硬编码

**问题**：`run_benchmark.py` 第 65 行默认路径硬编码为：
```python
LME_DATA = os.environ.get("LME_DATA", "/Volumes/zt_hd/projects/meow/LongMemEval/data/longmemeval_s_cleaned.json")
```

**操作**：改为相对路径 + 环境变量，例如：
```python
LME_DATA = os.environ.get(
    "LME_DATA",
    os.path.join(os.path.dirname(__file__), "data", "longmemeval_s_cleaned.json")
)
```

#### P0-5：跑 BGE-M3 基线数据

**问题**：reports/ 目录没有新的 BGE-M3 benchmark 报告。框架搭好了但没跑分 = 0 产出。

**操作**：
```bash
# 快速验证（10 题）
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets lme-s --subset 10

# 完整跑
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets lme-s
```

预期输出：`benchmarks/reports/bge_m3_lme-s_*.json`

#### P0-6：跑双小模型验证数据

**操作**：
```bash
# 快速验证（10 题）
python3 benchmarks/run_benchmark.py --encoder dual-small --datasets lme-s --subset 10

# 完整跑
python3 benchmarks/run_benchmark.py --encoder dual-small --datasets lme-s
```

**决策阈值**（来自 PRD v2）：
- LME-S R@1 差距 < 5pp → 采用双编码器
- NDCG 差距 > 0.10 → 放弃，保留 BGE-M3

#### P0-7：双模式对比跑分

**操作**：
```bash
# 两种模式对比
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets lme-s --modes retrieval,associative --compare
```

---

### P1 — 应该做（补测试集 + 补功能）

#### P1-1：接入 LoCoMo（通用类核心榜单）

**背景**：LoCoMo 是 Mem0 跑出 92.5 分的榜单，是"通用类 Agent 记忆"的必争之地。MemHop 目前没有接入。

**操作**：
1. 创建 `benchmarks/adapters/locomo_adapter.py`
2. 数据来源：`pip install datasets && load_dataset('LinkedIn-EI/LoCoMo')`
3. 核心指标：**F1 Score**（不是 NDCG，不是 R@K）
4. 存储方式：per-turn（参照 LME adapter）
5. 评估方式：判断答案是否在 recall 返回的 text 中

**LoCoMo 数据结构**（参考）：
- 1,540 个问题
- 覆盖：单跳推理、多跳推理、开放域、时序记忆
- 每个 problem 包含：conversation_turns + question + answer + evidence

#### P1-2：接入 DMR benchmark

**背景**：Zep/Graphiti 论文用的 benchmark，Zep 94.8%。另一个通用类核心榜单。

**操作**：
1. 找到 DMR 原始数据（Zep/Graphiti 论文附录或 GitHub）
2. 创建 `benchmarks/adapters/dmr_adapter.py`
3. 核心指标：准确率

#### P1-3：BEIR nfcorpus Retrieval 模式补测

**背景**：你图里 MemHop BGE-M3 在 nfcorpus 上 NDCG@10 = 0.225，但那是 Associative 模式。Retrieval 模式（纯 HNSW KNN）应该接近 FAISS 的 0.990。

**操作**：
```bash
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets nfcorpus --modes retrieval
```

预期结果：NDCG@10 > 0.90。如果达不到，说明 HNSW 检索本身有问题。

#### P1-4：Dream 效果对比

**背景**：PRD 要求验证 Dream 巩固对 recall 质量的影响。

**操作**：
```bash
# 有 Dream
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets lme-s --dream

# 无 Dream
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets lme-s --no-dream

# 自动对比
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets lme-s --compare-dream
```

如果 `--compare-dream` 未实现，则分别跑两次手动对比。

---

### P2 — 可选

| # | 内容 | 说明 |
|---|------|------|
| P2-1 | 接入 BEAM (1M/10M tokens) | 第二代 benchmark 硬骨头，Mem0 10M 仅 48.6 |
| P2-2 | 接入 MemoryAgentBench | 冲突解决场景，目前所有系统 <7% |
| P2-3 | `run_benchmark.py` 去掉 numpy 依赖 | 第 32 行 `import numpy as np`，用于 `_make_latency_info`，可用 statistics 模块替代 |

---

## 🏗️ 目标目录结构（清理后）

```
benchmarks/
├── run_benchmark.py          # 唯一入口 ✅
├── mcp_client.py             # MCP 客户端 ✅
├── encoders/
│   ├── __init__.py           ✅
│   └── dual_small.py         ✅
├── adapters/
│   ├── schema.py             ✅
│   ├── beir_adapter.py       ✅
│   ├── c_mteb_adapter.py     ✅
│   ├── lme_adapter.py        ❌ 待抽取 (P0-3)
│   ├── locomo_adapter.py     ❌ 待创建 (P1-1)
│   └── dmr_adapter.py        ❌ 待创建 (P1-2)
├── quality/
│   └── metrics.py            ✅
├── performance/
│   └── run_latency.py        ✅
├── data/                     # 本地数据缓存
├── reports/                  # 跑分结果
├── deprecated/               # 旧脚本备份
└── tools/                    # 诊断/调试工具
    ├── test_chunking.py
    ├── test_onnx_startup.py
    ├── diag_similarity.py
    └── ...
```

---

## 📊 跑分优先级（做完一个做一个）

```
Step 1: BGE-M3 + LME-S (Associative)     → 确立基线
Step 2: BGE-M3 + LME-S (Retrieval)       → 补全双模式数据
Step 3: dual-small + LME-S               → 验证双编码器假设
Step 4: BGE-M3 + nfcorpus (Retrieval)    → 填上 0.225 的坑
Step 5: BGE-M3 + c-mteb                 → 中文基线
Step 6: dual-small + c-mteb             → 中文双编码器验证
Step 7: LoCoMo 接入 + 跑分              → P1-1
Step 8: DMR 接入 + 跑分                  → P1-2
```

每一步跑完都应在 `reports/` 生成 JSON 结果文件。

---

## ⚠️ 特别注意

1. **MCP 服务器必须在本地终端运行**，sandbox 环境下 LMDB 创建会报 `Operation not permitted`
2. **版本号统一**：确认 `Cargo.toml` 版本和 `run_benchmark.py` 中 `memhop_version` 一致
3. **双编码器的分 tree 逻辑**：`dual_small.py` 中 zh 走一个 tree（512d）、en 走另一个 tree（384d），必须确保 `run_benchmark.py` 在 store 时正确传递 `tree` 参数
4. **LoCoMo 的 F1 计算**：和 LME 的 R@K 不同，需要实现 F1 评估逻辑（不能复用 metrics.py 的 R@K）

---

> 本报告由产品战略团队主理人验证生成，供开发 AI 按图施工。
