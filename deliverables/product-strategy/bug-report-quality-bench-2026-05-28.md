# quality_bench NDCG 异常 — 开发者 Debug 需求

**日期**: 2026-05-28
**发现者**: 产品验证 AI
**严重度**: P0（NDCG 无法验证）

---

## 现象

BGE-M3 编码的合成测试（100 docs, 10 categories, 10 queries）：
- 纯 Python cosine: **NDCG=1.0, R@1=1.0** ✅
- MemHop quality_bench: **NDCG=0.39, R@1=0.01** ❌

3-doc 最小复现：
- 纯 cosine: d0(Rust)=0.84, d1(Python)=0.50 → d0 排第一
- MemHop: NDCG=0.63, R@1=0.0 → d1 排第一

## 已排除的根因

| 假设 | 验证方法 | 结论 |
|------|---------|------|
| HNSW ID 映射断裂 | 替换为递增计数器 + eprintln 调试 | ❌ 排除，映射正常 |
| HNSW search 返回顺序错误 | 加 sort_by similarity descending | ❌ 排除，排序后仍错 |
| quality_bench dedup | 加 HashSet 去重 | ❌ 排除，去重后 NDCG 仍低 |
| HNSW 向量维度/类型 | 单元测试通过，eprintln 验证相似度正确 | ❌ 排除 |
| hnsw_id_map 丢结果 | eprintln: 3/3 全部通过 | ❌ 排除 |

## 已修复的代码 bug

1. **hnsw.rs**: `add_node` 全层级条目（修复 entry-point 遍历 OOB）
2. **hnsw.rs**: `search()` 加 similarity 排序
3. **brain.rs**: HNSW ID 从 hash → 递增计数器
4. **brain.rs**: 删除 dead code `string_id_to_u64`
5. **quality_bench.rs**: ranked 列表去重

## 待排查方向

### 1. `recall_retrieval` → `associations` 排序是否正确

`brain.rs` `recall_retrieval()` Step 5 加载 engram 时，`sorted` 列表顺序正确，但 `associations` Vec 的最终顺序需要验证。

### 2. quality_bench 的 `id_map` 映射

quality_bench 用 `id_map: HashMap<usize, String>` (doc_idx → engram_id)，
再 `find(|(_, v)| *v == &e.id)` 反查。需要在 3-doc 场景打印完整的 id_map 和 associations 来确认。

### 3. Brain 内部是否有中间产物污染

Cortex / recalled_buffer 是否有残留影响 `resp.associations`。

## 开发者复现命令

```bash
# 确保有 BGE-M3 模型
ls models/bge-m3/model.onnx

# 编译
cargo build --release --features onnx

# 运行（会自动生成测试数据）
python3 -c "
import json, subprocess, shutil
import numpy as np
from sentence_transformers import SentenceTransformer
model = SentenceTransformer('BAAI/bge-m3')
docs = ['Rust tutorial', 'Python guide', 'Docker reference']
queries = ['How to Rust?']
dv = model.encode(docs, normalize_embeddings=True)
qv = model.encode(queries, normalize_embeddings=True)
data = {'name':'t','documents':[{'id':f'd{i}','text':t,'vector':v.tolist()} for i,(t,v) in enumerate(zip(docs,dv))],
    'queries':[{'id':'q0','text':'How to Rust?','vector':qv[0].tolist()}],
    'qrels':{'q0':{'d0':1}},'limit':3,'spread_top_k':5,'dream_interval':50}
with open('/tmp/t.json','w') as f: json.dump(data,f)
subprocess.run(['./target/release/quality_bench','--input','/tmp/t.json','--output','/tmp/o.json','--db-dir','/tmp/db','--mode','retrieval'])
with open('/tmp/o.json') as f: print(json.load(f))
"
# 预期 NDCG > 0.9, R@1 > 0.8
# 当前 NDCG=0.63, R@1=0.0
```

## 建议调试方式

在 `brain.rs` `recall_retrieval()` 最后返回前加：
```rust
eprintln!("[recall] associations[0].id={} text={}", 
    associations.first().map(|e| &e.id).unwrap_or(&"NONE".to_string()),
    associations.first().map(|e| &e.text).unwrap_or(&"NONE".to_string()));
```

确认第一个返回的 engram 是否是期望的文档。
