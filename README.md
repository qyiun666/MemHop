# MemHop

> **SQLite for associative memory** — 嵌入式、单文件、零配置联想记忆引擎

[![Python 3.10+](https://img.shields.io/badge/python-3.10+-blue.svg)](https://python.org)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Rust](https://img.shields.io/badge/powered%20by-Rust-orange.svg)](https://www.rust-lang.org/)

MemHop 是一个嵌入式联想记忆数据库，模拟人脑的 **O(1) 瞬间回忆**能力。不是搜索引擎，不是向量数据库——是专为 AI Agent 设计的精准记忆系统。

## 特性

- **O(1) 召回** — Modern Hopfield Network 一步收敛，与记忆总量无关
- **LMDB 持久化** — 单文件 `memhop.db`，mmap 零配置，ACID 事务
- **零模型默认** — 字符 n-gram 哈希编码，无需下载模型，全语言支持
- **Python API** — `pip install memhop`，Rust 核心加速，类型提示完整
- **两阶段检索** — 稀疏粗筛 + Hopfield 精排，短文本召回更鲁棒
- **批量写入** — 单事务批量 `remember_batch`，百万级记忆秒级灌入

## 安装

```bash
pip install memhop
```

需要 Python 3.10+，wheel 内含预编译 Rust 核心，无需本地编译。

## Quick Start

```python
import memhop

# 打开数据库（自动创建）
db = memhop.open("my_brain.db")

# 写入记忆
mid = db.remember(
    "今天早上吃了豆浆油条，在楼下老王家",
    meta={"time": "2026-05-19T07:30", "tags": ["早餐", "食物"]},
)

# 联想回忆（O(1)）
memory = db.recall("今天早上吃了什么")
print(memory.text)        # "今天早上吃了豆浆油条，在楼下老王家"
print(memory.confidence)  # 0.94

# 无关查询返回 None
result = db.recall("火星上有液态水吗")
print(result)  # None

# Top-K 回忆
top3 = db.recall_topk("早餐", k=3)

# 按元数据搜索
results = db.search({"tags_contains": "食物"})

# 最近记忆
recent = db.recent(limit=5)

# 批量写入
ids = db.remember_batch([
    {"text": "下午开会讨论了产品路线图", "meta": {"tags": ["会议"]}},
    {"text": "晚上跑了5公里", "meta": {"tags": ["运动"]}},
])

# 更新记忆
db.update(mid, text="今天早上吃了豆浆油条和鸡蛋")

# 删除记忆
db.forget(mid)

# 清理旧记忆
deleted = db.purge_before("2026-01-01T00:00:00Z")

# 上下文管理器（自动关闭）
with memhop.open("my_brain.db") as db:
    db.remember("这条记忆会自动管理生命周期")

# 查看统计
print(db.count)  # 记忆总数
print(db.stats)  # {"count": 42, "dim": 1024, "beta": 8.0, "threshold": 0.7}
```

## API 概览

| 方法 | 说明 | 返回 |
|------|------|------|
| `memhop.open(path, ...)` | 打开/创建数据库 | `MemHopEngine` |
| `db.remember(text, meta?, id?)` | 写入一条记忆 | `str` (memory ID) |
| `db.recall(cue)` | 联想回忆最佳匹配 | `Memory \| None` |
| `db.recall_topk(cue, k=5)` | Top-K 联想回忆 | `list[Memory]` |
| `db.forget(memory_id)` | 删除记忆 | `bool` |
| `db.update(memory_id, text?, meta?)` | 更新记忆内容/元数据 | `bool` |
| `db.search(filters, limit=20)` | 按元数据精确搜索 | `list[Memory]` |
| `db.recent(limit=5)` | 最近 N 条记忆 | `list[Memory]` |
| `db.remember_batch(items)` | 批量写入 | `list[str]` |
| `db.purge_before(datetime)` | 清理指定时间前的记忆 | `int` (删除数) |
| `db.count` | 记忆总数 | `int` |
| `db.stats` | 运行时统计 | `dict` |
| `db.close()` | 关闭数据库 | `None` |

### Memory 对象

| 属性 | 类型 | 说明 |
|------|------|------|
| `id` | `str` | 记忆 ID（自动生成或指定） |
| `text` | `str` | 记忆文本 |
| `meta` | `dict` | 用户自定义元数据 |
| `confidence` | `float` | 召回置信度 (0.0–1.0) |
| `created_at` | `str` | ISO 8601 创建时间 |

### search 过滤器

`db.search()` 支持的过滤键：

| 键 | 类型 | 说明 |
|------|------|------|
| `tags_contains` | `str` | 标签包含 |
| `layer` / `type` / `domain` | `str` | 分类过滤 |
| `protection` | `str` | `"normal"` / `"protected"` / `"permanent"` |
| `is_dormant` | `bool` | 是否休眠 |
| `importance_gt` / `importance_lt` | `float` | 重要性范围 |
| `session_id` / `path` / `parent` | `str` | 上下文过滤 |
| `connections_to` | `str` | 关联记忆 ID |

## 性能

| 指标 | 值 |
|------|-----|
| 召回复杂度 | O(1)（Hopfield 一步收敛） |
| 编码延迟 | <1ms（n-gram 哈希，零模型） |
| 存储引擎 | LMDB（mmap，ACID） |
| 向量维度 | 1024 (f16 压缩存储) |
| 百万条记忆占用 | ~2GB |

## License

MIT
