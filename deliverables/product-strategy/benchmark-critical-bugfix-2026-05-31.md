# Benchmark 关键 Bug 修复 + 补跑指令

**日期**：2026-05-31
**优先级**：🔴 P0 — 所有 "BGE-M3" benchmark 数据无效，必须修复后重跑
**严重程度**：CRITICAL — 报告声称用 BGE-M3 编码，实际用的是 NgramEncoder

---

## 🔴 Bug #1：`--encoder bge-m3` 没有真正使用 BGE-M3

### 现象

所有 `--encoder bge-m3` 的测试，MCP 服务器内部实际使用的是 **NgramEncoder**（纯 n-gram 文本匹配，无语义编码），不是 BGE-M3 语义编码。

### 根因

`MemHopMCPRunner.__init__()` 在 `--encoder bge-m3` 时：

1. **没有**传 `env_extra={"MEMHOP_ONNX_MODEL": "..."}` 给 `MemHopMCPClient`
2. MCP 服务器启动时 **没有** `MEMHOP_ONNX_MODEL` 环境变量
3. `BrainConfig::default()` 的 `onnx_model_path: None`
4. Brain 编码链：Candle → ONNX → **NgramEncoder（fallback）**
5. 因为没有模型路径，Candle 和 ONNX 都跳过，最终降级到 NgramEncoder

**但报告元数据写的是 `source: "mcp_builtin", model_id: "BAAI/bge-m3"`** — 这是错的。

### 证据链

```
文件                                    内容
───────────────────────────────────────────────────────────
Cargo.toml (memhop)                     default = ["candle"]  ← 编译了 Candle
Cargo.toml (mcp-server)                 features = ["candle", "onnx"]  ← 编译了两者
brain.rs                                CandleEncoder::from_path(model_path)
                                        ↑ 只在 onnx_model_path 设了才执行
types.rs                                BrainConfig::default()
                                          onnx_model_path: None  ← 默认不加载
main.rs (mcp-server)                   env::var("MEMHOP_ONNX_MODEL")  ← 需要环境变量
run_benchmark.py                        MemHopMCPClient(MCP_BIN, self._db_dir)
                                        ↑ 没传 env_extra！
models/bge-m3/model.safetensors         ← 模型文件存在，但没人告诉 MCP 去加载
models/bge-m3/model.onnx                ← 同上
```

### 修复方案

**在 `MemHopMCPRunner.__init__()` 中，当 `--encoder bge-m3` 时传 `MEMHOP_ONNX_MODEL` 环境变量：**

```python
# run_benchmark.py, MemHopMCPRunner.__init__() 中

def __init__(self, mode="retrieval", dream=True, encoder="bge-m3"):
    self.mode = mode
    self.dream = dream
    self.encoder_name = encoder

    # ── 修复：BGE-M3 需要传模型路径给 MCP 服务器 ──
    env_extra = {}
    if encoder == "bge-m3":
        model_dir = os.environ.get(
            "MEMHOP_ONNX_MODEL",
            os.path.join(os.path.dirname(SCRIPT_DIR), "models", "bge-m3"),
        )
        if os.path.isdir(model_dir):
            env_extra["MEMHOP_ONNX_MODEL"] = model_dir
        else:
            print(f"⚠️ BGE-M3 model dir not found: {model_dir}")
            print(f"   Falling back to NgramEncoder (results will NOT be BGE-M3)")

    self._dual_encoder = None
    if encoder == "dual-small":
        from encoders.dual_small import DualSmallEncoder
        self._dual_encoder = DualSmallEncoder()

    # MCP subprocess
    self._db_dir = os.path.join(TEMP, f"bench_{os.urandom(4).hex()}")
    self._mcp = MemHopMCPClient(
        MCP_BIN, self._db_dir,
        env_extra=env_extra if env_extra else None,  # ← 传环境变量
        recv_timeout=3600,
    )
    self._mcp.start_reader()
```

### 修复后需要验证

启动 MCP 后，stderr 应该输出类似：

```
memhop: loaded Candle encoder from 'models/bge-m3' (dim=1024)
```

或者（如果 Candle 加载失败，回退 ONNX）：

```
memhop: loaded ONNX encoder from 'models/bge-m3' (dim=1024)
```

如果看到的是：

```
memhop: failed to load Candle encoder from 'models/bge-m3': ..., falling back
memhop: failed to load ONNX encoder from 'models/bge-m3': ..., falling back
```

那说明模型加载失败，仍然降级到了 NgramEncoder。

### 同步修复：`_build_encoder_info()`

当前代码在 `--encoder bge-m3` 时报告 `source: "mcp_builtin"`，但这不能区分"真的加载了 BGE-M3"还是"降级到了 NgramEncoder"。

**修复：启动后检查 MCP 的实际编码器状态，写入报告。**

最简方案：在 `MemHopMCPRunner` 初始化后，调一次 `mcp.status()` 或通过 stderr 日志判断实际加载了哪个编码器，然后在 `_build_encoder_info()` 里如实报告。

如果暂时不想改 Rust 代码，至少在 Python 端做：

```python
# 保守做法：如果没设 env_extra，报告里写 ngram 而非 bge-m3
def _build_encoder_info(encoder_name, env_extra=None):
    if encoder_name == "bge-m3":
        if env_extra and "MEMHOP_ONNX_MODEL" in env_extra:
            return EncoderInfo(model_id="BAAI/bge-m3", dim=1024, source="mcp_candle")
        else:
            return EncoderInfo(model_id="ngram-fallback", dim=1024, source="mcp_ngram")
    ...
```

---

## 🔴 Bug #2：LME-S Retrieval 模式的 qrels 不匹配

### 现象

LME-S Retrieval 模式 NDCG@10=0.0、R@1=0、R@5=0。不是真的 0 分，是评测代码的 bug。

### 根因：全链路追踪

```
load_lme_dataset()
  docs:  id = "session1_t0"          ← turn 级别
  qrels: {"q1": {"session1": 1}}     ← session 级别（❌ 不对齐）

runner.index(docs)
  _id_map[engram_id] = "session1_t0" ← 存的是 turn-level doc_id

runner.search("query") → Retrieval 模式
  ranked_ids = ["session1_t0", "session1_t3", ...]

aggregate_metrics(rankings, qrels)
  qrels["q1"] = {"session1": 1}
  检查 "session1_t0" in {"session1": 1} → ❌ 不匹配
  所有指标 = 0  ← 假的
```

**关键：`_id_map` 映射的是 `engram_id → doc_id`（turn 级），不是 `engram_id → session_id`。** 而 qrels 是 session 级的。两个 ID 空间完全不重叠。

### 修复方案（精确代码，只改 `run_lme_s` 一个函数）

在 `run_lme_s()` 里加 10 行，不改 `search()` 不改 `aggregate_metrics()`：

```python
def run_lme_s(runner, docs, queries, qrels):
    """LongMemEval-S benchmark: session-level associative retrieval."""

    # ===== 🔧 FIX: 构建 doc_id → session_id 映射 =====
    # docs 的每个元素都有 {"id": "session1_t0", "session_id": "session1"}
    doc_to_session = {doc["id"]: doc["session_id"] for doc in docs}
    # ===== END FIX =====

    dream_result = runner.index(docs)

    rankings = {}
    latencies = []
    for q in queries:
        t0 = time.time()
        ranked_ids, _ = runner.search(q["text"], top_k=10)
        latencies.append((time.time() - t0) * 1e6)

        # ===== 🔧 FIX: Retrieval 模式把 doc_id 转成 session_id =====
        if runner.mode == "retrieval":
            session_ids = []
            seen = set()
            for did in ranked_ids:
                sid = doc_to_session.get(did)
                # 兜底：如果映射找不到，尝试从 doc_id 格式推断
                # (e.g. "session1_t0" → "session1")
                if not sid and "_t" in did:
                    sid = did.rsplit("_t", 1)[0]
                if sid and sid not in seen:
                    seen.add(sid)
                    session_ids.append(sid)
            ranked_ids = session_ids
        # ===== END FIX =====

        rankings[q["id"]] = ranked_ids[:10]

    metrics = aggregate_metrics(rankings, qrels)
    ...
```

### 为什么这样修

1. **不改 `search()`** — `search()` 方法被 LME-S / nfcorpus / LoCoMo 共用，改它会影响其他数据集
2. **不改 `aggregate_metrics()`** — 这是标准 IR 指标实现，逻辑正确
3. **不改 `lme_adapter.py`** — qrels 用 session_id 是 LME-S 数据集本身的设计（每个问题问"哪个 session 包含答案"）
4. **只加 doc_id → session_id 转换** — 在 `run_lme_s()` 这个 LME-S 专用的评测函数里做转换

### Associative 模式为什么不受影响

`search()` 在 Associative 模式下直接返回 `aggregated_sessions` 里的 `session_id`，天然和 qrels 对齐：

```python
if self.mode == "associative":
    ranked_ids = [s["session_id"] for s in agg_sessions]  # 直接就是 session_id ✅
```

---

## 🟡 Bug #3：DualSmallEncoder 首次加载 38s

### 现象

```
$ python3 benchmarks/run_benchmark.py --encoder dual-small --datasets lme-s --subset 1
Loading models...              ← 此处卡 38 秒
```

### 根因

```python
class DualSmallEncoder:
    def __init__(self, device="cpu"):
        self._zh_model = SentenceTransformer("BAAI/bge-small-zh-v1.5")      # ~90MB ↓
        self._en_model = SentenceTransformer("sentence-transformers/all-MiniLM-L6-v2")  # ~80MB ↓
```

首次运行时 `sentence-transformers` 从 HuggingFace 下载两个模型（合计 ~170MB）。38s 取决于网络。后续运行走 `~/.cache/huggingface/hub/` 缓存，秒级加载。

### 结论

**不是 bug，是预期行为。** 只需加两处文档化：

**A. `__init__` 里加一行注释 + 日志**

```python
def __init__(self, device="cpu"):
    # ⚠️ First run downloads ~170MB from HuggingFace (~30-60s).
    # Subsequent runs use ~/.cache/huggingface/ (instant).
    import time
    t0 = time.time()
    self._zh_model = SentenceTransformer("BAAI/bge-small-zh-v1.5", device=device)
    print(f"[dual-small] zh loaded ({time.time()-t0:.1f}s)", flush=True)
    self._en_model = SentenceTransformer("sentence-transformers/all-MiniLM-L6-v2", device=device)
    print(f"[dual-small] en loaded ({time.time()-t0:.1f}s)", flush=True)
```

**B. 提供预下载命令**

```bash
# 一次性预下载，后续 benchmark 免等待
python3 -c "
from sentence_transformers import SentenceTransformer
SentenceTransformer('BAAI/bge-small-zh-v1.5')
SentenceTransformer('sentence-transformers/all-MiniLM-L6-v2')
print('Models cached ✓')
"
```

---

## 🟡 补跑清单（修复 Bug 后执行）

修复 Bug #1 和 #2 后，必须**全部重跑**，因为之前的 "BGE-M3" 数据实际是 NgramEncoder。

### Phase 0：BGE-M3 真实基线（必须先跑）

```bash
# 1. BGE-M3 + LME-S（两种模式，至少 10 problems）
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets lme-s --modes retrieval,associative --subset 10

# 2. BGE-M3 + LoCoMo（需要基线判断 dual-small 的 0.095）
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets locomo --modes retrieval --subset 50

# 3. BGE-M3 + DMR（至少 10 conversations）
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets dmr --modes retrieval --subset 10

# 4. BGE-M3 + nfcorpus（补 Retrieval 模式数据）
python3 benchmarks/run_benchmark.py --encoder bge-m3 --datasets nfcorpus --modes retrieval
```

**验证 BGE-M3 真正加载：**
- stderr 应输出 `loaded Candle encoder from '...' (dim=1024)` 或 `loaded ONNX encoder from '...'`
- 如果看到 `falling back` 则模型加载失败，结果仍然是 NgramEncoder

### Phase 1：dual-small 对比（有了基线再跑）

```bash
# 5. dual-small + LME-S（至少 10 problems，和 BGE-M3 对齐）
python3 benchmarks/run_benchmark.py --encoder dual-small --datasets lme-s --modes retrieval,associative --subset 10

# 6. dual-small + LoCoMo（和 BGE-M3 对比 F1 差距）
python3 benchmarks/run_benchmark.py --encoder dual-small --datasets locomo --modes retrieval --subset 50

# 7. dual-small + DMR（和 BGE-M3 对比）
python3 benchmarks/run_benchmark.py --encoder dual-small --datasets dmr --modes retrieval --subset 10

# 8. dual-small + nfcorpus
python3 benchmarks/run_benchmark.py --encoder dual-small --datasets nfcorpus --modes retrieval
```

### Phase 2：决策

| 指标 | BGE-M3 | dual-small | 差距 | 决策 |
|------|--------|-----------|------|------|
| LME-S R@1 | ? | ? | ? | < 5pp → 采纳双编码器 |
| LoCoMo F1 | ? | 0.095 | ? | 需基线才能判断 |
| nfcorpus NDCG@10 | ? | ? | ? | > 0.10 差距 → 保留 BGE-M3 |
| 内存 | 2.5GB | 280MB | -89% | 双编码器压倒性优势 |

---

## ⚠️ 特别注意

### 1. MCP 服务器启动日志

修复后每次跑分，**必须检查 MCP 的 stderr 输出**，确认编码器实际加载状态：

```
✅ 正确：memhop: loaded Candle encoder from 'models/bge-m3' (dim=1024)
❌ 错误：memhop: failed to load Candle encoder... falling back
```

如果模型加载失败，结果仍然是 NgramEncoder，报告必须如实标注。

### 2. `models/bge-m3/` 目录有 safetensors + onnx

```
models/bge-m3/
├── model.safetensors   ← Candle 用
├── model.onnx          ← ONNX 用
├── config.json         ← BERT 配置
└── tokenizer.json      ← 分词器
```

Brain 的加载优先级：Candle（找 `model.safetensors`）→ ONNX（找 `model.onnx`）→ NgramEncoder

### 3. `MEMHOP_ONNX_MODEL` 路径

- 可以是绝对路径：`/Volumes/zt_hd/projects/meow/memhop/models/bge-m3`
- 可以是相对路径（相对于 MCP 服务器的工作目录）
- 建议用 `os.path.join(os.path.dirname(SCRIPT_DIR), "models", "bge-m3")` 构造

### 4. 之前所有 "BGE-M3" 报告标记为无效

修复后需要**删除或重命名**之前所有标注 `bge-m3` 但实际是 NgramEncoder 的报告，避免混淆：

```bash
# 重命名旧报告（加 _ngram_fallback 后缀）
cd benchmarks/reports/
for f in *bge_m3*; do
    mv "$f" "${f%.json}_ngram_fallback.json"
done
```

---

## 📋 检查清单

### 🔴 P0（阻塞，必须修）
- [ ] Bug #1：`--encoder bge-m3` 时传 `MEMHOP_ONNX_MODEL` 给 MCP 服务器
- [ ] Bug #1：验证 stderr 输出确认 Candle/ONNX 编码器加载成功
- [ ] Bug #1：`_build_encoder_info()` 按实际加载状态报告编码器
- [ ] Bug #2：LME-S Retrieval 模式 doc_id → session_id 映射（改 `run_lme_s()`）
- [ ] 旧 "BGE-M3" 报告重命名（标记为 ngram_fallback）

### 🟡 P1（优化，修完 P0 后做）
- [ ] Bug #3：DualSmallEncoder `__init__` 加加载时间日志
- [ ] Bug #3：提供模型预下载命令
- [ ] Phase 0 补跑：BGE-M3 真实基线（LME-S + LoCoMo + DMR + nfcorpus）
- [ ] Phase 1 补跑：dual-small 对比（同数据集）
- [ ] Phase 2 决策：根据数据判断双编码器方案是否可行

---

> 本文档由产品战略团队审阅后输出。核心结论：**所有标注 "BGE-M3" 的 benchmark 结果实际都是 NgramEncoder**，必须修复后全部重跑。
