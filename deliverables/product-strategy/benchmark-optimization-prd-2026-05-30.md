# MemHop Benchmark 体系优化需求文档（v2 — 含双编码器验证方案）

**日期**：2026-05-30
**类型**：PRD — Benchmark 体系重构 + 双编码器架构验证
**参与成员**：方向明（主理人，审阅+编排）
**版本**：v2（基于 v1 的诊断结论，新增双编码器验证方案和修正策略）

---

## 📌 TL;DR（执行摘要）

- **核心问题**：现有 benchmark 体系有 3 套互不兼容的脚本、2 种调用路径（MCP vs Rust 直调）、存储方式混乱（per-session vs per-turn vs chunking），导致跑出来的数据不可比、不可信
- **关键决策**：统一为「MCP 单进程 + 每轮清库 + 只跑 MemHop + 竞品数据引用公开论文」架构
- **双编码器验证**：BGE-M3（2.5GB）可能太重，需验证「1 个中文小模型 + 1 个英文小模型」能否在质量接近的前提下将内存降至 1/9
- **下一步**：Phase 0 跑 BGE-M3 基线 → Phase 1 双小模型验证 → Phase 2 根据数据决定架构

---

## 🎯 核心结论卡片

| 项目 | 内容 |
|------|------|
| 推荐方案 | 统一 MCP 评测框架 + 只跑 MemHop + 竞品引用公开数据 + 双编码器验证 |
| 优先级 | P0（benchmark 数据是产品声明的根基） |
| 预期影响 | 可复现的 benchmark 体系 + 编码器架构决策数据支撑 |
| 资源需求 | ~4-6 天工程量（Python 重写 + 编码器验证 + 数据接入） |
| 风险等级 | 中（双编码器验证可能发现质量差距不可接受，需回退 BGE-M3） |

---

## 🔑 v2 新增：关键策略修正

### 修正 1：不跑竞品，只引用公开数据

| 竞品 | 有公开数据可直接引用 | 来源 |
|------|---------------------|------|
| Mem0 | LoCoMo 92.5, LongMemEval 94.4, BEAM 64.1/48.6 | Mem0 官方博客 2026.04 |
| agentmemory | LongMemEval ~80%（自报） | GitHub README |
| Zep/Graphiti | DMR 94.8% | Zep 论文 |
| FAISS-HNSW | BEIR nfcorpus 0.990 | 公开基准线 |

**结论：benchmark 框架只服务 MemHop 自己。竞品数据引用公开论文/README。**

`competitors/` 目录下的 `faiss_runner.py`、`chroma_runner.py`、`milvus_lite.py` **降级为可选交叉验证工具**，不是主力。

### 修正 2：编码器选择 = embedding model，不是向量库

用户问的"测试出一款适合的向量库"实际是 **embedding model** 选择——用什么模型把文本编码成向量。MemHop 内部已经用 HNSW 做索引（`instant-distance` crate），不需要外挂 FAISS/Milvus。

### 修正 3：双编码器验证方案

BGE-M3 占 2.5GB 内存，普通用户可能吃不消。假设用两个小模型替代：

| 模型 | 磁盘 | 运行时内存 | 维度 | 语言 | C-MTEB |
|------|------|----------|------|------|--------|
| **BGE-M3** | ~2.2GB | ~2.5GB | 1024 | 多语言 | ~0.70 |
| BGE-small-zh-v1.5 | ~90MB | ~150MB | 512 | 中文 | ~0.60 |
| all-MiniLM-L6-v2 | ~80MB | ~130MB | 384 | 英文 | MTEB ~0.59 |
| **双小模型合计** | ~170MB | ~280MB | — | 中+英 | 待验证 |

**两个小模型加起来 = BGE-M3 的 1/9 内存**。如果质量差距可接受（NDCG 降幅 < 0.05），这是压倒性优势。

**硬问题**：两个模型的向量维度不同（512d vs 384d），不能放进同一个 HNSW 索引。解法：**按语言建不同 knowledge tree**，CrossTree 边做跨语言关联。

---

## 1. 现状诊断：乱在哪

### 1.1 三条路，各跑各的

| 脚本 | 调用路径 | 存储方式 | 评估方式 | 能代表真实使用？ |
|------|---------|---------|---------|:---:|
| `run_lme.py` | Rust `quality_bench` 直调 | per-session（整段拼） | session-level | ❌ |
| `run_lme_mcp.py` | MCP 服务器 | per-session（整段拼） | turn-level | ❌ |
| `run_lme_associative.py` | MCP 服务器 | ✅ per-turn | ✅ `aggregated_sessions` | ✅ |
| `run_all.py` | MCP 服务器 | per-turn + chunking | ⚠️ turn-level（应改 session） | ⚠️ |
| `run_longmemeval.py` | Rust `quality_bench` 直调 | per-session | session-level | ❌ |
| `compare_encoders.py` | Rust `quality_bench` 直调 | per-session | session-level | ❌ |
| `quality/run_beir.py` | Rust `quality_bench` 直调 | per-session | turn-level | ❌ |
| `quality/run_c_mteb.py` | Rust `quality_bench` 直调 | per-session | turn-level | ❌ |
| `performance/run_latency.py` | MCP 服务器 | N/A（合成数据） | N/A | ✅ |

**结论**：9 个脚本里只有 2 个走 MCP + per-turn 存储，其余全是 Rust 直调 + per-session 拼接，不能代表真实使用。

### 1.2 核心架构问题

```
当前状态（混乱）：

  Python BGE-M3 编码
       │
       ├──→ quality_bench 二进制（Rust 直调，绕过 MCP）  ← 6个脚本走这条路
       │         ↓
       │    Rust Engine::new() 内部直接调用
       │    不走 MCP，不测 MCP overhead
       │
       └──→ memhop-mcp-server（MCP 进程）              ← 3个脚本走这条路
                 ↓
            JSON-RPC over stdio
            真实 MeowAgent 使用路径

  问题：两条路跑出来的数据不可比！
```

### 1.3 `quality_bench` 已废弃

- **源码已不在仓库**（Cargo.toml 无 `[[bin]]` 定义）
- 6 个脚本仍在调用它，但无法在新环境重建
- 所有调用它的脚本应废弃

---

## 2. 目标架构

### 2.1 核心原则

1. **MCP-only**：所有评测走 MCP 服务器进程，和 MeowAgent 真实使用方式完全一致
2. **每轮清库**：每个测试集完成后，清空 LMDB + 杀掉 MCP 进程，下一轮重新启动
3. **只跑 MemHop**：竞品数据引用公开论文，不自己跑 FAISS/ChromaDB
4. **结果即落盘**：每完成一个测试集，立刻保存 JSON 报告
5. **per-turn 存储**：对话记忆测试必须按 turn 存储（带 session_id / turn_id / turn_index）

### 2.2 评测流程

```
run_benchmark.py
  --encoder bge-m3          # 或 dual-small
  --datasets lme-s,nfcorpus,c-mteb
  --modes retrieval,associative

执行流程（串行，每轮清库）：
  1. 启动 MCP 服务器（BGE-M3 内置 ONNX）
  2. LME-S + Associative 模式  → 存结果 → 清库
  3. LME-S + Retrieval 模式     → 存结果 → 清库
  4. nfcorpus + Retrieval 模式  → 存结果 → 清库
  5. C-MTEB 8 个任务            → 存结果 → 清库
  6. 杀 MCP 服务器

最终报告里：
  - MemHop 实测数据
  - 竞品数据从论文/README 引用，标注来源
```

### 2.3 测试集分组

| 测试组 | 测试集 | 跑 MemHop | 说明 |
|--------|--------|:---------:|------|
| **记忆组** | LongMemEval-S | ✅ | 核心差异化场景 |
| **记忆组** | LoCoMo | ✅ | P0 缺口，通用 Agent 记忆核心榜单 |
| **检索组** | BEIR nfcorpus | ✅ | 纯 IR 对标 |
| **检索组** | C-MTEB T2Retrieval | ✅ | 中文 IR 对标 |
| **延迟组** | 合成数据 1K/10K/100K | ✅ | MemHop 独有 |
| **Dream组** | LME-S 前/后 Dream 对比 | ✅ | MemHop 独有 |

---

## 3. 双编码器验证方案（核心新增）

### 3.1 验证目标

验证「BGE-small-zh + MiniLM-L6」双小模型能否替代 BGE-M3，实现：
- 内存从 2.5GB → ~280MB（降 89%）
- NDCG@10 降幅 < 0.05（可接受）
- Associative 模式下 LME-S R@1 降幅 < 5pp（图扩散补偿）

### 3.2 验证方案

```
Phase 0: BGE-M3 基线（确立标杆）
  ├── LME-S + Associative → R@1=?, R@5=?
  ├── LME-S + Retrieval  → R@1=?, R@5=?
  ├── BEIR nfcorpus       → NDCG@10=?
  └── C-MTEB (8 tasks)   → NDCG@10=?

Phase 1: 双小模型验证（Python 侧编码，不改 Rust）
  ├── 用 sentence-transformers 加载 BGE-small-zh + MiniLM-L6
  ├── Python 端编码 → 传 vector 给 MCP store()
  ├── 同样跑 4 个基准
  └── 对比：质量差距有多大？

Phase 2: 决策
  ├── 如果 LME-S R@1 差距 < 5pp → 改 Rust HybridEncoder 为 DualEncoder
  ├── 如果 NDCG 差距 > 0.10 → 放弃，保留 BGE-M3
  └── 如果 0.05 < 差距 < 0.10 → 可选提供 dual-small feature flag
```

### 3.3 Phase 1 实现细节（Python 侧，零 Rust 改动）

MemHop 的 MCP 客户端 `store()` 已支持传 `vector` 参数（绕过 Rust 侧 ONNX），所以 Phase 1 只需 Python 代码：

```python
# benchmarks/encoders/dual_small.py
from sentence_transformers import SentenceTransformer

class DualSmallEncoder:
    """BGE-small-zh + MiniLM-L6 双编码器"""
    
    def __init__(self):
        self.zh_model = SentenceTransformer("BAAI/bge-small-zh-v1.5")  # 512d
        self.en_model = SentenceTransformer("sentence-transformers/all-MiniLM-L6-v2")  # 384d
    
    def encode(self, text: str) -> tuple[list[float], str]:
        """返回 (vector, tree_name)"""
        if self._is_cjk(text):
            return self.zh_model.encode(text).tolist(), "zh"
        else:
            return self.en_model.encode(text).tolist(), "en"
    
    @staticmethod
    def _is_cjk(text: str) -> bool:
        cjk_count = sum(1 for c in text if '\u4e00' <= c <= '\u9fff')
        return cjk_count / max(len(text), 1) > 0.3

# 在 benchmark 脚本中使用：
encoder = DualSmallEncoder()
for doc in docs:
    vec, tree = encoder.encode(doc["text"])
    mcp.store(doc["text"], vector=vec, tree=tree, ...)
```

### 3.4 Rust 侧 DualEncoder 架构（Phase 2 决策后实现）

```rust
// src/encoder/dual.rs
pub struct DualEncoder {
    zh_session: ort::Session,   // BGE-small-zh ONNX
    en_session: ort::Session,   // MiniLM-L6 ONNX
}

impl DualEncoder {
    pub fn encode(&self, text: &str) -> (Vec<f32>, &str) {
        if is_cjk(text) {
            (self.zh_session.run(...).to_vec(), "zh")
        } else {
            (self.en_session.run(...).to_vec(), "en")
        }
    }
}

fn is_cjk(text: &str) -> bool {
    let cjk = text.chars()
        .filter(|c| ('\u{4E00}'..='\u{9FFF}').contains(c))
        .count();
    cjk as f32 / text.len() as f32 > 0.3
}
```

### 3.5 按语言建 tree 的存储逻辑

```
中文文本 → zh_encoder → 512d → store(text, vector=..., tree="zh")
英文文本 → en_encoder → 384d → store(text, vector=..., tree="en")

recall("类似判例") → is_cjk → zh_encoder → 512d → recall(query_vec, tree="zh")
recall("async await") → !is_cjk → en_encoder → 384d → recall(query_vec, tree="en")

跨语言关联：
  EntangleGraph 的 CrossTree 边连接 zh 和 en 树中的相关记忆
  一次 recall 可以同时查两棵树（先查主树，再通过 CrossTree 扩散到副树）
```

### 3.6 混合语言处理

| 文本 | CJK 占比 | 编码器 | 示例 |
|------|---------|--------|------|
| "今天学了Rust异步" | >30% | zh_encoder | 中英混合但主体中文 |
| "Implement async await" | <30% | en_encoder | 纯英文 |
| "async/await机制" | >30% | zh_encoder | 英文词少但中文多 |

**不完美的边界情况**用 EntangleGraph CrossTree 边弥补——同一概念的中英文表述会通过语义相似度自动关联。

### 3.7 预判结果

| 维度 | BGE-M3 | 双小模型 | 差距 | 判断 |
|------|--------|---------|------|------|
| 内存 | 2.5GB | ~280MB | **-89%** | ✅ 压倒性 |
| 纯 IR（BEIR） | ~0.70 | ~0.58-0.62 | -0.08~-0.12 | ⚠️ 差距大 |
| 关联记忆（LME-S） | 待测 | 待测 | 可能差距更小 | ✅ 图扩散补偿 |
| 速度 | 慢（大模型） | 快（小模型 3-5x） | **更快** | ✅ 额外优势 |

**关键假设**：在 Associative 模式下（HNSW + Hopfield + EntangleGraph），小模型的向量质量差距可能被图扩散补偿。如果 LME-S 的 R@1 差距 < 5pp，那就非常值得做。

---

## 4. 需求池

### P0 — 必须做（地基）

| # | 需求 | 验收标准 | 估算 |
|---|------|---------|------|
| P0-1 | **统一评测框架** `benchmarks/run_benchmark.py` | 单入口、CLI 参数、MCP-only、每轮清库、结果即落盘 | 1.5 天 |
| P0-2 | **删除/标注废弃脚本** | `run_lme.py`、`run_lme_mcp.py`、`run_longmemeval.py`、`compare_encoders.py`、`quality/run_beir.py`、`quality/run_c_mteb.py` 标注 DEPRECATED 或删除 | 0.5 天 |
| P0-3 | **统一报告 schema** | 所有报告 JSON 包含：schema_version、版本号、编码器、测试集、模式、metrics、参数快照、时间戳 | 0.5 天 |
| P0-4 | **跑 BGE-M3 基线** | 用统一框架跑 LME-S (Associative+Retrieval) + BEIR nfcorpus + C-MTEB | 0.5 天 |
| P0-5 | **双小模型验证** | Python 侧 DualSmallEncoder + 同样 4 个基准 + 对比表 | 1 天 |

### P1 — 应该做（补缺口）

| # | 需求 | 验收标准 | 估算 |
|---|------|---------|------|
| P1-1 | **接入 LoCoMo** | 下载 LoCoMo 数据、写 adapter、跑 MemHop（P0 缺口） | 1 天 |
| P1-2 | **接入 DMR benchmark** | 找到 Zep 论文原始数据、写 adapter、跑 MemHop | 1 天 |
| P1-3 | **修复 `run_all.py` 评估方式** | 把 turn-level R@K 改成 `aggregated_sessions` session-level 评估 | 0.5 天 |
| P1-4 | **Dream 效果对比** | LME-S 同一数据集，Dream 前后 R@5/R@10/失忆率对比 | 0.5 天 |

### P2 — 可选（锦上添花）

| # | 需求 | 验收标准 | 估算 |
|---|------|---------|------|
| P2-1 | **接入 BEAM** | 1M/10M token 长上下文测试 | 2 天 |
| P2-2 | **CI 集成** | GitHub Actions 每次发版自动跑 P0 测试集 | 1 天 |
| P2-3 | **可视化报告** | HTML 仪表盘，多编码器多测试集对比 | 1 天 |
| P2-4 | **MemoryAgentBench** | 冲突解决 + 测试时学习 | 1 天 |
| P2-5 | **Rust DualEncoder** | Phase 2 决策后，改 Rust HybridEncoder 为 DualEncoder | 2 天 |

---

## 5. 统一评测框架设计

### 5.1 目录结构（目标）

```
benchmarks/
├── run_benchmark.py          ← 唯一入口
├── mcp_client.py             ← MCP 客户端（保留）
├── encoders/
│   ├── bge_m3.py             ← BGE-M3 编码器（MCP 内置 ONNX，无需 Python 侧）
│   └── dual_small.py         ← 双小模型编码器（新增，Python 侧）
├── adapters/
│   ├── schema.py             ← 统一数据/结果 schema（保留，扩展）
│   ├── beir_adapter.py       ← BEIR 数据加载（保留）
│   ├── lme_adapter.py        ← LongMemEval 适配器（新增）
│   ├── locomo_adapter.py     ← LoCoMo 适配器（新增）
│   └── dmr_adapter.py        ← DMR 适配器（新增）
├── quality/
│   └── metrics.py            ← IR 指标（保留）
├── data/
│   ├── cache/                ← 编码缓存（新增）
│   ├── beir/                 ← BEIR 本地数据（保留）
│   └── lme/                  ← LongMemEval 数据
├── reports/                  ← 结果输出（统一 schema）
└── deprecated/               ← 废弃脚本
    ├── run_lme.py
    ├── run_lme_mcp.py
    ├── run_longmemeval.py
    ├── run_all.py
    ├── compare_encoders.py
    ├── quality/run_beir.py
    ├── quality/run_c_mteb.py
    └── competitors/           ← 竞品 runner 降级为可选工具
```

### 5.2 核心接口：`run_benchmark.py`

```bash
# BGE-M3 基线（Phase 0）
python benchmarks/run_benchmark.py \
  --encoder bge-m3 \
  --datasets lme-s,nfcorpus,c-mteb \
  --modes retrieval,associative

# 双小模型验证（Phase 1）
python benchmarks/run_benchmark.py \
  --encoder dual-small \
  --datasets lme-s,nfcorpus,c-mteb \
  --modes retrieval,associative

# 只跑前 10 个问题（快速验证）
python benchmarks/run_benchmark.py \
  --encoder bge-m3 --datasets lme-s --subset 10

# 跑完出对比表
python benchmarks/run_benchmark.py \
  --compare reports/bge_m3_*.json reports/dual_small_*.json
```

### 5.3 MemHop MCP Runner 核心逻辑

```python
class MemHopMCPRunner:
    """MemHop through MCP server — the only correct way to test."""
    
    def __init__(self, mode="retrieval", dream=True, encoder="bge-m3"):
        self.mode = mode        # "retrieval" | "associative"
        self.dream = dream      # whether to call dream() after storing
        self.encoder = encoder  # "bge-m3" (MCP内置) | "dual-small" (Python侧)
        self.mcp = None
    
    def index(self, docs, vectors=None):
        """Store docs via MCP."""
        self.mcp = MemHopMCPClient(self.binary, self.db_dir)
        self.mcp.start_reader()
        
        for doc in docs:
            vec, tree = self._encode(doc["text"]) if self.encoder == "dual-small" else (None, None)
            self.mcp.store(
                text=doc["text"],
                session_id=doc.get("session_id", "bench"),
                turn_id=doc.get("turn_id", ""),
                turn_index=doc.get("turn_index", 0),
                vector=vec,         # None = MCP 内置编码
                tree=tree,           # None = 默认树
            )
        
        if self.dream:
            self.mcp.dream()
    
    def search(self, query, top_k=10):
        """Recall via MCP."""
        vec, tree = self._encode(query) if self.encoder == "dual-small" else (None, None)
        r = self.mcp.recall(query, limit=top_k, query_vector=vec, tree=tree)
        
        if self.mode == "associative":
            return [s["session_id"] for s in r.get("aggregated_sessions", [])]
        else:
            return [item["id"] for item in r.get("results", [])]
    
    def clear(self):
        """Kill MCP + delete DB."""
        if self.mcp:
            self.mcp.close()
        shutil.rmtree(self.db_dir, ignore_errors=True)
```

### 5.4 报告 schema

```json
{
  "schema_version": "1.0",
  "timestamp": "2026-05-30T22:00:00+08:00",
  "memhop_version": "0.11.0",
  "encoder": {
    "model_id": "BAAI/bge-m3",
    "alt_model_id": null,
    "dim": 1024,
    "device": "cpu",
    "source": "mcp_builtin"
  },
  "dataset": {
    "name": "LongMemEval-S",
    "num_docs": 1234,
    "num_queries": 100,
    "storage_mode": "per-turn",
    "subset": null
  },
  "system": {
    "name": "memhop",
    "mode": "associative",
    "dream": true,
    "dream_result": {"consolidated": 42, "new_edges": 18}
  },
  "metrics": {
    "ndcg_10": {"mean": 0.7234, "std": 0.1821},
    "mrr": {"mean": 0.8102, "std": 0.1654},
    "recall_1": {"mean": 0.7000, "std": 0.4602},
    "recall_5": {"mean": 0.9000, "std": 0.3020},
    "recall_10": {"mean": 0.9500, "std": 0.2190},
    "失忆率": 0.3000,
    "幻听率": 0.1000
  },
  "latency": {
    "avg_recall_us": 8500,
    "avg_store_us": 3200,
    "p95_recall_us": 12000
  },
  "competitor_comparison": {
    "mem0_lme_s": {"source": "Mem0 blog 2026.04", "recall_1": null, "f1": 0.925},
    "agentmemory_lme_s": {"source": "GitHub README (self-reported)", "recall_1": 0.80}
  }
}
```

---

## 6. 需要新增的测试集

### 6.1 P0 缺口（通用类 Agent 记忆核心榜单）

| 测试集 | 数据来源 | 规模 | 评测指标 | 竞品得分 | MemHop 目标 |
|--------|---------|------|---------|---------|------------|
| **LoCoMo** | `LinkedIn-EI/LoCoMo` (HuggingFace) | 1,540 问题 | F1, R@K | Mem0: 92.5, Letta: 74.0 | F1 > 70 |
| **DMR** | Zep/Graphiti 论文 | — | Accuracy | Zep: 94.8%, MemGPT: 93.4% | Accuracy > 85% |

### 6.2 已有测试集的重跑需求

| 测试集 | 当前状态 | 需要做什么 |
|--------|---------|-----------|
| LongMemEval-S | 有 v0.8 数据（R@1=70%），可能用旧代码 | 用统一框架 + 最新 v0.11 代码重跑全量 |
| BEIR nfcorpus | 有 v0.8 数据（NDCG=0.225，Associative 模式） | 补跑 Retrieval 模式（目标 NDCG > 0.90） |
| C-MTEB T2Retrieval | 有 v0.8 数据（NDCG=0.36） | 用统一框架重跑，双编码器对比 |

---

## 7. 实施路线

### Phase 0：清理 + BGE-M3 基线（1-2 天）

1. 移动废弃脚本到 `benchmarks/deprecated/`
2. 写 `run_benchmark.py` 基本框架
3. 跑 BGE-M3 基线：LME-S + nfcorpus + C-MTEB（双模式）
4. 保存基线数据到 `reports/bge_m3_baseline_*.json`

### Phase 1：双小模型验证（1-2 天）

1. 写 `encoders/dual_small.py`
2. 用 Python 侧编码 + MCP store(vector=...) 跑同样 4 个基准
3. 输出 BGE-M3 vs dual-small 对比表
4. **决策点**：差距可接受 → 继续 Phase 2；不可接受 → 放弃双编码器

### Phase 2：补测试集 + Rust 实现（2-3 天）

1. 接入 LoCoMo（P0 缺口）
2. 接入 DMR（P0 缺口）
3. Dream 效果对比
4. 如果 Phase 1 决定采用双编码器：实现 Rust `DualEncoder`
5. 完整重跑所有测试集

---

## ✅ 行动清单

| # | 行动 | 负责方 | 时间窗 |
|---|------|--------|--------|
| 1 | 清理废弃脚本，移入 deprecated/ | 开发 | Day 1 |
| 2 | 写统一 run_benchmark.py 入口 | 开发 | Day 1 |
| 3 | 跑 BGE-M3 基线（LME-S + nfcorpus + C-MTEB） | 开发 | Day 1-2 |
| 4 | 写 dual_small.py 编码器 | 开发 | Day 2 |
| 5 | 跑双小模型验证 + 输出对比表 | 开发 | Day 2-3 |
| 6 | **决策点**：双编码器是否可行 | 产品 | Day 3 |
| 7 | 接入 LoCoMo | 开发 | Day 3-4 |
| 8 | 接入 DMR | 开发 | Day 4-5 |
| 9 | （如果决策通过）实现 Rust DualEncoder | 开发 | Day 5-6 |

---

## ⚠️ 待确认 / 假设 / Non-goals

**待确认：**
- MCP 服务器的 `store()` 是否支持 `tree` 参数（用于双编码器按语言建不同 tree）？如果不支持，需要先加这个参数
- `quality_bench` Rust 二进制的源码是否已从仓库删除？如果是，直接废弃
- agentmemory 那个 ~80% R@1 的数据来源是自报还是第三方评测？
- LoCoMo 的 F1 评测是否需要 LLM 参与？

**假设：**
- 所有测试集都能在本地 CPU 上跑（不需要 GPU）
- MCP 客户端 `store()` 支持传 `vector` 参数（已确认）
- MCP 服务器进程的启动/关闭是可靠的
- BGE-small-zh 和 MiniLM-L6 的 ONNX 模型可以从 HuggingFace 下载

**Non-goals：**
- 不做 CI/CD 集成（P2）
- 不做可视化仪表盘（P2）
- 不接入 BEAM/MemoryAgentBench（P2）
- 不自己跑 FAISS/ChromaDB 作为 baseline（引用公开数据）
- Phase 0-1 不改 Rust 生产代码（只改 Python 脚本）

---

## 📚 数据来源 & 成员产出索引

- 方向明（主理人）：代码审阅、问题诊断、需求文档编写
- 基于 9 个 Python 脚本 + 4 个 competitor runner + MCP client 的完整代码审阅
- v2 新增：双编码器验证方案、竞品数据引用策略修正

---

> 本报告由产品战略团队方向明审阅编写，基于 MemHop benchmark 代码库完整审阅。
> v2 新增双编码器验证方案，核心假设需实测数据验证。
