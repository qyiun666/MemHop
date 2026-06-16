# MemHop

> Agent-oriented memory database inspired by human brain cognitive architecture

MemHop is a specialized memory database designed for AI Agents, implementing a six-layer cognitive architecture (L0-L5) with custom .meh binary file format.

## Features

- **Zero-copy mmap retrieval**: Memory-mapped file access for high-performance reads
- **Hybrid search**: Triple retrieval (BM25 + vector cosine + n-gram Jaccard)
- **Hypergraph associative memory**: Multi-hop graph diffusion for related memory retrieval
- **Cognitive layers**: L0-L5 architecture mirroring human memory systems
- **Automatic consolidation**: Dream pipeline for memory pruning and archival
- **Pure Rust**: Zero external dependencies beyond minimal crates
- **Single-process design**: One agent = one process = one .meh file

## Quick Start

### FFI (C/Python/Go/...) — 推荐方式

MemHop 通过 4 个 `extern "C"` 函数提供 JSON-in JSON-out 跨语言接口。从 [GitHub Actions](../../actions) 下载对应平台的预编译二进制即可使用：

```c
#include "memhop.h"

// 1. 打开数据库
void* handle = memhop_open(
    "{\"db_path\":\"/tmp/agent.meh\",\"encoder_socket\":\"/tmp/memhop_encoder.sock\",\"vector_dim\":768}");

// 2. 搜索记忆
char* res = memhop_execute(handle,
    "{\"command\":\"search\",\"dialogue\":\"hello\",\"context_limit\":10,\"min_score\":0.0}");
printf("%s\n", res);
memhop_free_string(res);

// 3. 关闭
memhop_close(handle);
```

完整协议参考 [API_NEW.md](API_NEW.md)（11 个命令 + 4 个 C 函数）。

### Rust SDK

```rust
use memhop::{MemHop, MemHopConfig, SearchQuery, UpdateRequest, LlmConfig};
use std::path::PathBuf;

// Open or create database
let config = MemHopConfig::new(PathBuf::from("agent_memory.meh"), 768);
let mut db = MemHop::open(config)?;

// Search memory
let query = SearchQuery {
    dialogue: "我想学习Rust编程".to_string(),
    context_id: None,
    l3_id: None,
    context_limit: 10,
    llm_enhance: None,
    auto_create: 0,
    min_score: 0.0,
    context_history: None,
};
let results = db.search_memory(query)?;

// Update memory (topic must be activated first)
let request = UpdateRequest {
    topic_id: results.contexts[0].id.clone(),
    dialogue_text: "用户学习Rust所有权系统".to_string(),
    summary: Some("Rust所有权学习记录".to_string()),
    action_chain: vec![],
};
let result = db.update_memory(request)?;

// Run Dream consolidation
let llm = LlmConfig {
    api_url: "https://api.deepseek.com/v1/chat/completions".to_string(),
    api_key: "sk-...".to_string(),
    model: "deepseek-chat".to_string(),
    api_format: 1,
};
let report = db.dream(llm)?;

db.close()?;
```

## Architecture

### Cognitive Layers (L0-L5)

- **L0 (Profile)**: Agent identity and preferences
- **L1 (Episodic)**: Short-term episodic memories
- **L2 (Semantic)**: Compressed topic structures (3-level nesting)
- **L3 (Procedural)**: Skill and pattern memories
- **L4 (Archive)**: Long-term archival storage
- **L5 (Crystal)**: Crystallized programmatic knowledge

### File Format (.meh)

- 4KB page-aligned binary format
- A/B dual header for crash recovery
- Journal transaction log for atomicity
- **Hybrid search**: Triple retrieval (BM25 + Vector + n-gram)
- B-tree indexing for O(log n) lookups
- SIMD-accelerated vector operations (AVX2)

## Core API

### Database Operations

| Method | Description |
|--------|-------------|
| `MemHop::open(config)` | Open or create database |
| `search_memory(query)` | Search memory with L2-centric retrieval |
| `update_memory(request)` | Create/update multi-layer memory |
| `dream(llm)` | Run Dream consolidation pipeline |
| `batch_store(batch)` | Batch store multiple documents |
| `close()` | Close database and sync to disk |

### L0-L5 Query Interfaces

| Method | Description |
|--------|-------------|
| `get_profile()` | Get Agent profile |
| `get_engram(id)` | Get single L1 engram by ID |
| `list_engrams(query)` | List L1 engrams with pagination |
| `get_topic(id)` | Get L2 topic detail |
| `list_topics(query)` | List L2 topics |
| `get_knowledge(id)` | Get L3 knowledge detail |
| `list_knowledge(query)` | List L3 knowledge |
| `list_archives_by_topic(topic_id, query)` | List L4 archives by topic |
| `list_archives_by_nodes(node_ids, query)` | List L4 archives by node IDs |
| `list_all_archives(query)` | List all L4 archives |
| `list_crystals(query)` | List L5 crystallized skills |

### Update Interfaces

| Method | Description |
|--------|-------------|
| `update_profile(request)` | Update Agent profile |
| `update_topic_title(id, new_title)` | Update L2 topic title |
| `update_knowledge_title(id, new_title)` | Update L3 knowledge title |
| `update_crystal_title(id, new_title)` | Update L5 crystal title |
| `merge_topics(primary_id, secondary_ids)` | Merge multiple L2 topics |
| `import_memory(request)` | Import memory to Profile/Topic/Knowledge |

## Performance

- Store throughput: >1000 ops/sec (target)
- Recall latency: p95 < 10ms (target)
- Vector similarity: AVX2 4x faster than scalar

## Version Roadmap

- **v0.30.0 (Foundation)**: .meh format + basic store/recall ✅
- **v0.31.0 (Awakening)**: Activation + Cascade
- **v0.32.0 (Self-Aware)**: L2 session activation + Organize
- **v0.33.0 (Full Mind)**: Batch store + Emotion + Dream pipeline
- **v0.34.0 (Launch Ready)**: Migration + integration tests + benchmarks
- **v0.41.0 (Current)**: L2-centric search/update model + Dream pipeline

## Download

预编译二进制从 [GitHub Actions](../../actions) 的 `build` workflow 下载：

| 平台 | 产物 | CI Job |
|------|------|--------|
| macOS (Intel + Apple Silicon Universal) | `libmemhop-universal.dylib` | `create-universal` |
| macOS Apple Silicon | `libmemhop.dylib` | `build-macos-arm` |
| macOS Intel | `libmemhop.dylib` | `build-macos-x86` |
| Linux x86_64 | `libmemhop.so` | `build-linux` |
| Windows x86_64 | `memhop.dll` | `build-windows` |

验证下载的二进制：
```bash
cp libmemhop.dylib /tmp/memhop-download/
cargo run --example ffi_test  # 动态加载并测试所有 FFI 接口
```

## Development

```bash
# Build
cargo build --release

# Test (217 tests)
cargo test

# FFI binary validation (loads .dylib via libloading)
MEMHOP_DYLIB_PATH=/tmp/memhop-download/libmemhop.dylib cargo run --example ffi_test

# Full test including DeepSeek Dream
MEMHOP_DEEPSEEK_KEY=sk-xxx cargo test -- --include-ignored --nocapture
```

## API Documentation

- **[API_NEW.md](API_NEW.md)** - FFI 协议文档（推荐，JSON-in JSON-out）
- **[API_NEI.md](API_NEI.md)** - Internal implementation details
- **[docs/](docs/)** - Additional documentation

## Version History

| Version Range | Date | Key Highlights |
|---------------|------|----------------|
| **v0.30.x - v0.41.x** | 2026-05-19 ~ 2026-06-14 | 1. **专用记忆数据库** (.meh格式) 2. 六层认知架构 (L0-L5) 3. L2中心化检索/更新模型 4. Dream记忆整合管线 5. BM25+HNSW双通道检索 |
| **v0.23.x - v0.25.x** | 2026-06-14 ~ 2026-06-15 | 1. SDK模式重构 2. 6层仿人脑记忆引擎 3. API优化与标准化 4. CandleEncoder向量模型集成 5. 性能优化与基准测试 |
| **v0.1.x - v0.22.x** | 2026-05-19 ~ 2026-06-13 | 1. Brain架构设计与迭代 2. MCP Server集成 3. HNSW向量索引实现 4. 场景感知与记忆塑性 5. LMDB持久化层 |

For detailed release notes, see [docs/changelogs/](docs/changelogs/) and [docs/plans/](docs/plans/).

## Development Guidelines

This project follows strict development guidelines to ensure code quality, performance, and security. All guidelines are documented in the `.qoder/rules/` directory:

### Core Rules
- **P01 - Code Quality**: Coding standards and best practices
- **P02 - Code Modification**: Guidelines for modifying existing code
- **P07 - Performance Optimization**: Performance optimization techniques
- **P09 - Dependency Management**: Dependency selection and management

### Quick Reference
- See [.qoder/rules/README.md](.qoder/rules/README.md) for the complete index
- All rules are prefixed with 'P' for easy identification
- Rules are designed for MemHop's specific architecture and requirements

## Contributing

We welcome contributions! Please follow these steps:

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

For bug reports and feature requests, please open an issue on GitHub.

## Contact

- **Author**: qyiun666
- **Email**: qyiun666@163.com
- **GitHub**: https://github.com/qyiun666/memhop

## License

MIT OR Apache-2.0

---

**Note**: This is an active project under development. APIs may change between versions. Please refer to the version-specific documentation for the most accurate information.
