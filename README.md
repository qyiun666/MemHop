# MemHop

嵌入式联想记忆引擎。O(1) 召回，单文件存储，零配置。

## 安装

```bash
pip install memhop
```

Python 3.10+，预编译 wheel，无需本地编译。

## Quick Start

```python
import memhop

# 打开数据库
with memhop.open("brain.db") as db:
    # 写入记忆
    db.remember("今天吃了豆浆油条", meta={"tags": ["早餐"]})

    # 联想召回（O(1)）
    m = db.recall("早餐吃了什么")
    print(m.text)       # "今天吃了豆浆油条"
    print(m.confidence) # 0.94
```

## API

| 方法 | 说明 |
|------|------|
| `db.remember(text, meta?)` | 写入记忆 |
| `db.recall(cue)` | 联想召回最佳匹配 |
| `db.recall_topk(cue, k=5)` | Top-K 召回 |
| `db.search(filters)` | 按 meta 过滤 |
| `db.recent(limit=5)` | 最近记忆 |
| `db.forget(id)` | 删除 |
| `db.update(id, text?, meta?)` | 更新 |
| `db.remember_batch(items)` | 批量写入 |
| `db.purge_before(datetime)` | 清理旧记忆 |

## 核心特性

- **O(1) 召回**：Modern Hopfield Network，与记忆总量无关
- **单文件**：LMDB 持久化，`brain.db` 即走即拷
- **零模型默认**：字符 n-gram 哈希，无需下载模型
- **三层记忆**：entity（纠缠图）/ knowledge（知识树）/ episode（原文）

## License

All Rights Reserved. Copyright (c) 2026 MemHop Contributors.
