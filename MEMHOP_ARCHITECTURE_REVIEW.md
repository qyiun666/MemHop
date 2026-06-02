# MemHop v0.13 Benchmark 开发规格书

> 本文档供 AI 开发人员根据 v0.13 spec 重新设计 benchmark 系统。

---

## 一、v0.13 核心变化总览

### Breaking 变更

| 变化 | 影响 |
|------|------|
| `agent_path` → `agent_id` | benchmark `mcp_client.py` 和 `run_benchmark.py` 中所有 `agent_path` 改为 `agent_id` |
| MCP schema 全量更新 | store/recall/dream 参数签名全部更新 |

### 新增 MCP 参数

`memhop_store`:

```python
agent_id: str                    # 必填，替代 agent_path
text: str                        # 必填
session_id: str                  # 必填
auto_create_tree: bool = True    # 是否自动建树
match_threshold: float = 0.75    # 上下文匹配阈值
context_half_life: float = 12.0  # 时间衰减半衰期(小时)
auto_compress: bool = True       # 是否自动压缩上下文
llm_compressed_summary: str = "" # 应用层 LLM 压缩的摘要
llm_keywords: list[str] = []     # 应用层 LLM 提取的关键词
# 原有参数保留: valence, arousal, tree_id, kind, agent_response
```

`memhop_recall`:

```python
agent_id: str                    # 必填
query: str                       # 必填
context_id: str = ""             # 指定上下文范围 → 测失憶率
use_reranker: bool = True        # 开启 Cross-Encoder → 测幻听率
use_worldview_filter: bool = True # 开启世界观过滤 → 测幻听率
llm_conflict_check: str = ""     # 应用层 LLM 冲突检测
# 原有参数保留: session_id, limit, mode, kind_filter, tree, query_vector
```

`memhop_dream`:

```python
agent_id: str                    # 必填
context_compress: bool = True    # 是否压缩待压缩的上下文
llm_patterns: list = []          # 应用层 LLM 提供的模式
llm_contradictions: list = []    # 应用层 LLM 发现的矛盾
```

### 新增 MCP 响应字段

`memhop_store` 响应新增:
```json
{
  "context_id": "ctx_1717200000",
  "context_summary": "关于Rust所有权的讨论",
  "context_turn_count": 5,
  "auto_tree_id": "tree_xxx"
}
```

`memhop_recall` 响应新增:
```json
{
  "context_id": "ctx_1717200000",
  "recall_quality": {
    "scope": "context_filtered",
    "context_hit_count": 5,
    "total_candidates": 80
  }
}
```

`memhop_dream` 响应新增:
```json
{
  "contexts_compressed": 2,
  "new_auto_trees": [{"tree_id": "...", "name": "..."}],
  "new_entanglements": [{"id": "...", "context": "...", "strength": 0.6}],
  "new_worldviews": [{"pattern": "...", "stability": 0.31}],
  "dormant_moved": 3,
  "archived": 1
}
```

---

## 二、Benchmark 适配方案

### 2.1 mcp_client.py 改动

```python
class MemHopMCPClient:
    def __init__(self, binary_path: str, env_extra=None, recv_timeout=3600):
        # 去掉 db_path 参数 — v0.13 由 agent_id 内部管理路径
        ...

    def store(self, text, agent_id, session_id, ...,
              auto_create_tree=True, match_threshold=0.75,
              context_half_life=12.0, auto_compress=True,
              llm_compressed_summary=None, llm_keywords=None):
        args = {
            "agent_id": agent_id,
            "text": text,
            "session_id": session_id,
            "auto_create_tree": auto_create_tree,
            "match_threshold": match_threshold,
            "context_half_life": context_half_life,
            "auto_compress": auto_compress,
        }
        if llm_compressed_summary:
            args["llm_compressed_summary"] = llm_compressed_summary
        if llm_keywords:
            args["llm_keywords"] = llm_keywords
        # ... 原有参数透传 ...
        return self._tool_call("memhop_store", args)

    def recall(self, query, agent_id, ...,
               context_id=None, use_reranker=True, use_worldview_filter=True):
        args = {
            "agent_id": agent_id,
            "query": query,
            "use_reranker": use_reranker,
            "use_worldview_filter": use_worldview_filter,
        }
        if context_id:
            args["context_id"] = context_id
        # ...
        return self._tool_call("memhop_recall", args)

    def dream(self, agent_id, context_compress=True, ...):
        args = {"agent_id": agent_id, "context_compress": context_compress}
        # ...
        return self._tool_call("memhop_dream", args)
```

### 2.2 run_benchmark.py 改动

**MemHopMCPRunner 重构**：

```python
class MemHopMCPRunner:
    def __init__(self, mode="retrieval", encoder="bge-m3"):
        self.mode = mode
        self.encoder_name = encoder
        self._mcp = None
        self._id_map = {}

    def ensure_mcp(self):
        """启动 MCP 子进程（全局只需一次）"""
        if self._mcp is not None:
            return
        # v0.13: 不再需要 db_path，agent_id 内部管理
        ...

    def perceive(self, doc: dict, agent_id: str) -> dict:
        """单轮 perceive，传入 agent_id 替代 agent_path"""
        text = doc.get("text", "")
        if not text:
            return {}
        result = self._mcp.store(
            text,
            agent_id=agent_id,
            session_id=doc.get("session_id", "bench"),
            ...
        )
        eid = result.get("engram_id")
        doc_id = doc.get("id", "")
        if eid and doc_id:
            self._id_map[eid] = doc_id
        return result

    def search(self, query, agent_id, top_k=10,
               context_id=None, use_reranker=True, use_worldview_filter=True):
        """召回时传入 agent_id，支持 context_id 过滤"""
        raw = self._mcp.recall(
            query,
            agent_id=agent_id,
            limit=top_k,
            context_id=context_id,
            use_reranker=use_reranker,
            use_worldview_filter=use_worldview_filter,
        )
        # ... ranked_ids 映射逻辑 ...
        return ranked_ids, raw

    def dream(self, agent_id, context_compress=True):
        return self._mcp.dream(agent_id=agent_id, context_compress=context_compress)

    def clear(self):
        """关闭 MCP 进程"""
        if self._mcp is not None:
            self._mcp.close()
            self._mcp = None
```

**主循环**：

```python
runner = MemHopMCPRunner(encoder=args.encoder)
runner.ensure_mcp()

for ds_name in datasets:
    agent_id = f"bench_{ds_name}"  # v0.13: agent_id 是逻辑标识
    runner._id_map = {}

    for mode in modes:
        runner.mode = mode
        if ds_name == "nfcorpus":
            results = run_nfcorpus(runner, args.subset or 500, agent_id, mode, args.dream_interval)
        elif ...

# 清理：v0.13 不需要手动删 DB 路径，但需清理 agent 数据
# memhop 侧需要提供清理接口，或 benchmark 直接复用 agent_id（覆盖旧数据）
runner.clear()
```

### 2.3 agents_path 相关代码移除

```python
# 删除以下函数/变量：
def _make_agent_path(ds_name): ...        # 不再需要
@staticmethod
def reset_db(agent_path): ...             # 改为 reset_agent(agent_id)
TEMP = "/tmp/memhop_bench"                # 不再需要
```

---

## 三、测试集设计（完美匹配 v0.13）

### 3.1 基础检索测试（与竞品对标）

| 测试集 | 来源 | 测什么 | 对应 MemHop 能力 | 指标 |
|--------|------|--------|-----------------|------|
| **BEIR nfcorpus** | BEIR @ NeurIPS 2021 | 文档检索 | HNSW 基础搜索 | NDCG@10, R@5 |
| **C-MTEB (8任务)** | MTEB @ EMNLP 2022 | 中文检索 | HNSW 中文编码 | NDCG@10 |
| **LongMemEval-S** | LongMemEval ICLR 2025 | 会话记忆 | perceive + 逐轮存储 | Session R@1, R@5 |
| **LoCoMo** | Snap Research | 长对话 | perceive + dream + recall | LLM-judge Accuracy |
| **DMR** | MemGPT arXiv 2023 | 多会话一致性 | perceive + dream + 跨会话 recall | LLM-judge Accuracy |

### 3.2 v0.13 差异化能力测试（新增）

| 测试维度 | 测试方法 | 对应 v0.13 特性 | 量化指标 |
|---------|---------|----------------|---------|
| **上下文激活** | store 1000 随机话题 → 检查活跃上下文 ≤5 | 三阶生命周期 | active_count ≤5, dormant_count ≤1000 |
| **上下文过滤召回** | 存 ctx_A(5条) + ctx_B(5条) → recall(context_id=ctx_A) | context_id 过滤 | 失憶率 <5% |
| **Reranker 降幻听** | 存"Rust"+ "Python" → recall("所有权") | use_reranker | 幻听率 <5% |
| **Worldview 过滤** | 建 worldview → recall 冲突内容 | use_worldview_filter | 冲突内容降权率 |
| **自动建树** | 同一话题 5+ 轮 → 检查 auto_tree 被创建 | auto_create_tree | tree_id != null |
| **Dream 巩固** | dream 前/后对比 recall NDCG | dream + 上下文压缩 | NDCG 提升率 |
| **纠缠图扩散** | mount_tree → perceive 对话 → dream → 跨树 recall | EntangleGraph | 跨树召回率 |
| **时间衰减** | 同一话题间隔不同时间 store → 检查上下文切换 | context_half_life | 衰减曲线匹配度 |

### 3.3 测试数据来源

所有数据集**必须预下载，测试过程不联网**（已实现 `download_data.sh`，需同步更新）：

| 数据集 | 下载方式 |
|--------|---------|
| BEIR nfcorpus | `beir` 库预缓存 |
| C-MTEB | `mteb` 库预缓存，`HF_DATASETS_OFFLINE=1` |
| LoCoMo | `data/locomo/locomo10.json` |
| LME-S | `data/lme/longmemeval_s_cleaned.json` |
| DMR (MSC) | `datasets` 库预缓存 |
| 知识树素材 | 准备示例代码目录、PDF、Book 用于 mount_tree 测试 |

---

## 四、质量门禁指标与 Benchmark 对应

### v0.13 spec 的三个核心指标

| 指标 | 目标 | Benchmark 怎么测 | 测试数据集 |
|------|------|----------------|-----------|
| **上下文不爆炸** | 活跃≤5, 休眠≤1000 | store 1000 随机话题，检查 | 合成数据 |
| **失憶率 <5%** | 应召回的内容未召回 <5% | context_id 过滤召回，检查命中率 | 合成数据 + DMR |
| **幻听率 <5%** | 召回中不相关内容 <5% | reranker/worldview 过滤，检查不相关内容比例 | 合成数据 |

### G-05 回归阈值对标

| G-05 指标 | Benchmark 覆盖 | 说明 |
|-----------|--------------|------|
| Recall p99 | ✅ BEIR/C-MTEB 延迟 | HNSW 检索延迟 |
| Store p99 | ✅ latency benchmark | perceive 存储延迟 |
| Search throughput | ✅ latency benchmark | 每秒查询数 |
| Memory usage | ✅ stats 检查 | dream 前后内存对比 |
| 编译时间 | ⚠️ `cargo bench` 缺失 | 需要加 Rust 基准 |

---

## 五、竞品对比策略

### 5.1 竞品公开数据

文件: `benchmarks/competitors_published.json`（已创建，需同步更新 v0.13 指标）

| 竞品 | 可对比的数据集 | 指标 |
|------|--------------|------|
| **Mem0 2026** | LongMemEval (94.4%), LoCoMo (92.5%) | QA Accuracy |
| **EverOS** | LongMemEval (83.0%), LoCoMo (93.05%) | QA Accuracy |
| **Zep** | LongMemEval (71.2%), DMR (94.8%) | QA Accuracy |
| **AgentMemory** | LongMemEval-S R@5 (95.2%) | Retrieval R@5 |
| **Hindsight** | LongMemEval (91.4%), LoCoMo (89.6%) | QA Accuracy |
| **FAISS HNSW** | BEIR nfcorpus NDCG@10 (0.352) | NDCG@10 |

注意区分：竞品报告的是**端到端 QA 准确率**，AgentMemory 报告的是**检索召回率**，需用不同 dataset key 区分。

### 5.2 差异对比报告格式

每个测试集输出以下格式的报告：

```
=== BEIR-nfcorpus ===
System               | NDCG@10  | R@5
---------------------|----------|--------
BGE-base (published) | 0.352    | —
BM25 (published)     | 0.325    | —
FAISS HNSW (pub.)   | 0.352    | —
---------------------|----------|--------
→ MemHop v0.13      | XX.XX    | XX.XX

=== LongMemEval-S ===
System               | R@5      | Source
---------------------|----------|----------------------
MemPalace            | 96.60%   | GitHub
AgentMemory          | 95.20%   | GitHub
---------------------|----------|----------------------
→ MemHop v0.13      | XX.XX%   | this run
```

---

## 六、3 个 benchmark 运行模式

### 模式 A: 竞品对标（快速，面向发布）
```bash
python benchmarks/run_benchmark.py \
  --encoder bge-m3 \
  --datasets nfcorpus,lme-s,locomo,dmr \
  --modes retrieval \
  --dream-interval 50
```
- 只跑 retrieval mode
- 和竞品公开数据同条件对比
- 产出：各数据集 NDCG@10, R@5

### 模式 B: 全量质量门禁（CI，面向开发）
```bash
python benchmarks/run_benchmark.py \
  --all \
  --modes retrieval,associative \
  --dream-interval 50 \
  --dream-quality \
  --check-regression
```
- 全部数据集 + 全部 mode
- 包含 Dream 质量 benchmark
- 自动对比基线，检测回归

### 模式 C: 差异化能力展示（面向发布/营销）
```bash
python benchmarks/run_benchmark.py \
  --encoder bge-m3 \
  --context-benchmark \
  --worldview-benchmark \
  --entangle-benchmark \
  --dream-interval 5
```
- 上下文激活/失憶率/幻听率专项测试
- 知识树 mount + 纠缠图扩散测试
- 产出：差异化能力报告

---

## 七、开发顺序与依赖

```
第1步：mcp_client.py 适配 v0.13
  └── agent_id 替换 agent_path
  └── store/recall/dream 新参数
  └── 依赖：v0.13 子任务1 完成

第2步：run_benchmark.py 适配 v0.13
  └── MemHopMCPRunner 全部 agent_id
  └── 移除 _make_agent_path / reset_db
  └── 依赖：第1步完成

第3步：验证基础检索仍可运行
  └── nfcorpus smoke test（--subset 10）
  └── 对比 v0.12 结果是否退化
  └── 依赖：第2步完成

第4步：差异化能力 benchmark 场景
  └── 上下文激活测试（store 1000 话题）
  └── context_id 过滤测试
  └── reranker/worldview 测试
  └── 自动建树测试
  └── 依赖：v0.13 子任务3-6 完成

第5步：Dream 质量 + 纠缠图 benchmark
  └── mount_tree 测试数据准备
  └── dream 前后 recall 对比
  └── 跨树召回测试
  └── 依赖：v0.13 全部完成

第6步：竞品数据 + 报告生成
  └── competitors_published.json 更新
  └── compare_reports() 增强
  └── 依赖：第3步、第5步完成
```

---

## 八、关键文件清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `benchmarks/mcp_client.py` | **重写** | agent_id + 新 MCP 参数 |
| `benchmarks/run_benchmark.py` | **重写** | agent_id + context_id + 差异化场景 |
| `benchmarks/adapters/schema.py` | 修改 | LatencyInfo / BenchmarkResult 适配新响应 |
| `benchmarks/config.py` | 修改 | 移除 TEMP, 添加 agent 配置 |
| `benchmarks/competitors_published.json` | 修改 | 同步更新指标/来源 |
| `benchmarks/download_data.sh` | 修改 | 知识树 mount 测试数据 |
| `benchmarks/quality/metrics.py` | 增量 | 失憶率/幻听率 评估函数 |
| `benchmarks/data/mount_demo/` | **新建** | mount_tree 测试用示例代码/文档 |

### 不变的文件

| 文件 | 说明 |
|------|------|
| `benchmarks/adapters/beir_adapter.py` | 仅加载数据，不改 |
| `benchmarks/adapters/c_mteb_adapter.py` | 同上 |
| `benchmarks/adapters/lme_adapter.py` | 同上 |
| `benchmarks/adapters/locomo_adapter.py` | 同上 |
| `benchmarks/adapters/dmr_adapter.py` | 同上 |
| `benchmarks/utils/llm_client.py` | DeepSeek API 不变 |
| `benchmarks/competitors/` | 保留但不引用 |

---

## 九、验证方案

### 第1步验证
```bash
# 编译新 MCP 服务器
cargo build --release -p memhop-mcp-server

# import 测试
python3 -c "from benchmarks.mcp_client import MemHopMCPClient"
```

### 第2步验证
```bash
# smoke test
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets nfcorpus --subset 10 --dream-interval 0
# 检查 reports/ 下 JSON 指标非零
```

### 第3步验证
```bash
# 对比 v0.12 -> v0.13 无退化
python3 benchmarks/run_benchmark.py --compare reports/*.json
```

### 第4步验证
```bash
# context_id 过滤测试
python3 benchmarks/run_benchmark.py --context-benchmark
# 输出应显示: 失憶率=2.3%, 幻听率=1.8%
```

### 完成验证
```bash
# 全部模式运行
python3 benchmarks/run_benchmark.py --all --modes retrieval,associative
# 竞品对比
python3 benchmarks/run_benchmark.py --compare reports/*.json
# 差异化能力报告
python3 benchmarks/run_benchmark.py --diff-benchmark
```
