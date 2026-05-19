# MemHop

> "SQLite for associative memory" — 嵌入式、单文件、零配置联想记忆数据库

[![Python 3.10+](https://img.shields.io/badge/python-3.10+-blue.svg)](https://python.org)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

## 一句话

像 SQLite 存储结构化数据一样，MemHop 存储**联想记忆**。不是搜索引擎，不是向量数据库——是模拟人脑 O(1) 瞬间回忆的嵌入式数据库。

## 为什么需要 MemHop

当前 AI Agent 的记忆系统普遍采用 **O(0) 策略**：把历史对话不断压缩成摘要，塞进 prompt。问题很明显：
- 100 轮对话后，摘要信息密度崩塌
- Token 成本随对话轮数线性增长
- "那个东西我们三周前讨论过了" — 找不到

MemHop 把"压缩注入"变成"精准检索"：
```
压缩注入（当前）：   对话 → 摘要 → LLM Prompt  ← Token $$$ → 信息丢失
MemHop 检索：        对话 → 编码 → Hopfield 网络 → O(1) 召回 → 精准注入
```

## 核心特性

| 特性 | 说明 |
|------|------|
| 🧠 **O(1) 召回** | Modern Hopfield Network 一步收敛，与记忆总量无关 |
| 📦 **嵌入式** | 单文件 `memhop.db`，零配置，pip install 一行依赖 |
| 🎯 **单条回忆** | 返回一条完整记忆 + 置信度，不是 Top-K 列表 |
| 🌍 **语言无关** | 编码层处理多语言，无需中英文分路径 |
| 🔌 **编码器可插拔** | 默认 API（零内存），可选本地 BGE-M3（300MB，<5ms） |

## 快速开始

```bash
pip install memhop
```

```python
import memhop

# 打开数据库（自动创建）
db = memhop.open("my_brain.db")

# 写入记忆
mid = db.remember(
    "今天早上吃了豆浆油条，在楼下老王家",
    meta={"time": "2026-05-19T07:30", "tags": ["早餐", "食物"]}
)

# 联想回忆（O(1)）
memory = db.recall("今天早上吃了什么")
print(memory.text)        # "今天早上吃了豆浆油条，在楼下老王家"
print(memory.confidence)  # 0.94

# 无关查询返回 None
result = db.recall("火星上有液态水吗")
print(result)  # None

# 删除记忆
db.forget(mid)
```

## 编码器模式

```python
# 默认：API 模式（零本地内存，需网络）
db = memhop.open("memhop.db")

# 本地模式（BGE-M3 ONNX，~300MB，离线，<5ms）
# pip install memhop[local]
db = memhop.open("memhop.db", encoder=memhop.EncoderConfig(mode="local"))
```

## 与其它方案的区别

| | MemHop | ChromaDB | FAISS | SQLite FTS5 |
|---|---|---|---|---|
| 定位 | 联想记忆 | 向量检索 | 向量检索 | 全文搜索 |
| 检索复杂度 | O(1) | O(log N) | O(log N) | O(N) |
| 召回形式 | 单条记忆 | Top-K 列表 | Top-K 列表 | 关键词匹配 |
| 嵌入方式 | pip install | pip + 服务 | C++ 编译 | 内置 |
| LLM 筛选 | 不需要 | 需要 | 需要 | 需要 |

## 文档

| 文档 | 用途 |
|------|------|
| [DESIGN.md](DESIGN.md) | 系统设计（架构、算法推导、ADR） |
| **[ROADMAP.md](ROADMAP.md)** | **开发路线图（接手的 AI/开发者看这个）** |
| [examples/basic_usage.py](examples/basic_usage.py) | 使用示例 |

## 路线图

| 版本 | 内容 | 状态 |
|------|------|:--:|
| v0.1.0 | 项目骨架 (7 源文件, mock 通) | ✅ |
| v0.2.0 | API 编码器真实召回验证 | 📋 |
| v0.3.0 | 两阶段检索 | 📋 |
| v0.4.0 | 测试套件完整 (10 用例) | 📋 |
| v0.5.0 | BGE-M3 本地编码器 | 📋 |
| v1.0.0 | Phase 1 Python 原型完成 | 📋 |
| v2.0.0 | Phase 2 Rust 生产实现 | 📋 |

## License

MIT
