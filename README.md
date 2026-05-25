# MemHop

嵌入式联想记忆引擎。Modern Hopfield Network O(1) 召回，LMDB 单文件存储，零外部模型依赖。

## 安装

**Rust 库**（lib crate）：

```toml
[dependencies]
memhop = "0.6.0"
```

**MCP Server**（二进制）：

```bash
cargo install memhop-mcp-server
# 或从源码构建
cargo build --release --workspace
```

纯 Rust，Rust 2024 edition。依赖：heed (LMDB)、serde、rayon、half、zstd。

## Quick Start

### 作为 Rust 库

```rust
use memhop::{MemHop, StoreOptions};

let mut db = MemHop::open("brain.db")?;

// 写入记忆
let id = db.store("今天吃了豆浆油条", None, &StoreOptions::default())?;

// O(1) 联想召回
let m = db.recall("早餐吃了什么", None)?.unwrap();
println!("{} (confidence: {:.2})", m.text, m.confidence);

// 多域知识树
db.create_tree("work")?;
db.store("GraphRAG 方案评审通过", Some("work"), &StoreOptions::default())?;

db.close()?;
```

### 作为 MCP 工具

配置 Claude Desktop `claude_desktop_config.json`：

```json
{
  "mcpServers": {
    "memhop": {
      "command": "/path/to/memhop-mcp-server",
      "env": { "MEMHOP_DB_PATH": "/path/to/brain.db" }
    }
  }
}
```

支持 12 个 MCP 工具：`memhop_store`、`memhop_recall`、`memhop_recall_topk`、`memhop_search`、`memhop_recent`、`memhop_forget`、`memhop_dream`、`memhop_stats`、`memhop_count`、`memhop_create_tree`、`memhop_list_trees`、`memhop_remove_tree`。

## 核心 API

### 读写

| 方法 | 说明 |
|------|------|
| `MemHop::open(path)` | 打开/创建数据库 |
| `db.store(text, tree?, opts)` | 写入记忆，返回记忆 ID |
| `db.recall(query, tree?)` | O(1) 联想召回，最高置信度匹配 |
| `db.recall_topk(query, k, tree?)` | Top-K 召回 |
| `db.search(filters, limit)` | 按 meta 字段精确过滤 |
| `db.recent(limit, tree?)` | 最近写入的记忆 |
| `db.forget(memory_id)` | 删除 |
| `db.update(memory_id, text?, meta?)` | 更新 |
| `db.close()` | 关闭引擎，持久化所有数据 |

### 知识树

| 方法 | 说明 |
|------|------|
| `db.create_tree(name)` | 创建独立知识域 |
| `db.remove_tree(name)` | 删除知识域 |
| `db.list_trees()` | 列出所有知识域 |

### 记忆巩固

| 方法 | 说明 |
|------|------|
| `db.dream(config?)` | 触发记忆巩固（模式合并/弱化） |
| `db.stats()` | 引擎统计（总数/活跃/域数量） |
| `db.count()` | 记忆总数 |

### StoreOptions

```rust
StoreOptions {
    auto_entangle: true,              // 自动发现关联创建纠缠边
    context_snippet: None,            // 存储时上下文
    manual_links: vec![],             // 手动指定关联记忆 ID
}
```

### DreamConfig

```rust
DreamConfig {
    auto_trigger_interval: 100,       // 每 N 次 store 自动触发
    merge_threshold: 0.95,            // 余弦相似度 > 此值触发合并
    weaken_threshold: 0.3,             // 置信度 < 此值触发弱化
    max_duration_ms: 500,             // 最大持续时间
}
```

## 核心特性

- **O(1) 召回**：Modern Hopfield Network，召回时间与记忆总量无关
- **单文件存储**：LMDB 持久化，`brain.db` 即走即拷
- **零模型依赖**：字符 n-gram 哈希编码 (1024 维 f16)，无需下载任何模型
- **Domain Tree**：独立知识域，每个域有独立的 Hopfield 网络 + 索引 + 存储
- **Auto Entangle**：存入记忆时自动发现关联（top-5 召回，置信度 > 0.5 自动建边）
- **Dream 模式**：纯本地统计的记忆巩固（合并相似模式、弱化低置信度模式）
- **f16 半精度**：向量存储使用 half-precision float，节省 50% 存储
- **场景门控**：三层自动上下文过滤（fingerprint/tree/anchor），v0.6.2 启用
- **MCP 集成**：标准 Model Context Protocol server，可直接接入 Claude Desktop

## 架构

```
memhop (lib crate, 0.6.0)
├── engine/         MemHop 引擎（API 门面 + domain tree 路由）
├── hopfield.rs     Modern Hopfield Network (499 行)
├── encoder/        N-gram 哈希编码器
├── storage.rs      LMDB 持久化层 (546 行)
├── index.rs        稀疏索引 (288 行)
├── meta_index.rs   元数据索引 (153 行)
├── scene_gating.rs 场景门控 (414 行, v0.6.2 启用)
├── dream.rs        记忆巩固 (156 行)
└── filter.rs       过滤器 (161 行)

memhop-mcp-server (binary crate, 0.6.0)
└── main.rs         MCP JSON-RPC server (124 行)
```

## 构建与测试

```bash
# 编译
cargo build --workspace

# 测试
cargo test --workspace

# Clippy
cargo clippy --workspace -- -D warnings
```

## License

All Rights Reserved. Copyright (c) 2026 MemHop Contributors.
