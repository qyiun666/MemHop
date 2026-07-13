# MemHop API v0.57.0

> Agent-oriented memory database with L0-L6 cognitive architecture. Feature flags: `grpc-encoder` (default), `llm` (default).

## Quick Start

```rust
// Cargo.toml: memhop = "0.57"
use memhop::{MemHop, MemHopConfig, SearchQuery, UpdateRequest};
let mut db = MemHop::open(MemHopConfig::new(PathBuf::from("memory.meh"), 768))?;
let results = db.search(SearchQuery {
    query: "What did we discuss yesterday?".into(),
    layers: vec![0, 2, 5],
    max_results: 20,
    min_score: 0.0,
    include_profile: false,
    filters: None,
    directed_l2_id: None,
    directed_l3_id: None,
    auto_create: None,
})?;
db.update_memory(UpdateRequest {
    id: "topic-1".into(),
    layer: 2,
    fields: std::collections::HashMap::new(),
})?;
db.close()?;
```

## Lifecycle

| Method | Signature | Description |
|--------|-----------|-------------|
| `open` | `fn(config: MemHopConfig) -> Result<Self>` | Open/create .meh database |
| `close` | `fn(self) -> Result<()>` | Checkpoint and close |
| `checkpoint` | `fn(&mut self) -> Result<()>` | Persist in-memory indices |

## Core API

### search — 多层记忆检索 (requires grpc-encoder)
`fn search(&mut self, query: SearchQuery) -> Result<SearchResult>`
- SearchQuery: `query` (必填搜索文本), `layers`, `max_results`, `min_score`, `include_profile`, `filters`, `directed_l2_id`/`directed_l3_id` (定向范围), `auto_create` (自动建场景)
- SearchResult: `profile`, `contexts`, `associated_contexts`, `l3_ids`, `l1_previews`

### update_memory — 记忆写入
`fn update_memory(&mut self, request: UpdateRequest) -> Result<UpdateResult>`
- UpdateRequest: `id`, `layer`, `fields` (通用字段映射, 支持 `dialogue_text`, `summary`, `scene_id`, `user_keywords`, `agent_keywords` 等)
- UpdateResult: `id`, `status` (Created/Updated/Archived)

### batch_store — 批量编码存储
`fn batch_store(&mut self, batch: StoreBatch) -> Result<StoreResult>` (requires grpc-encoder)
- StoreBatch: `items`, `source_info`, `import_mode`
- StoreResult: `stored_count`, `item_ids`

### import_memory — 数据导入
`fn import_memory(&mut self, request: ImportRequest) -> Result<ImportResult>`
- ImportRequest: `target_layer` (Profile/Topic/Knowledge), `data`, `mode` (Merge/Overwrite/Skip)

### build_l3_hypergraph_from_path — L3 超图构建
`fn build_l3_hypergraph_from_path(&mut self, path: &Path) -> Result<ImportResult>`

### dream — 记忆巩固
`fn dream(&mut self, l2_ids: Option<Vec<String>>) -> Result<DreamReport>` (requires llm)

## Cognitive Layer CRUD

### L0 Profile
| Method | Signature |
|--------|----------|
| `get_profile` | `fn(&self) -> Result<Option<ProfileResult>>` |

### L1 Engraph Memory
| Method | Signature |
|--------|----------|
| `get_l1_graph` | `fn(&self, scene_id: Option<&str>) -> Result<L1Graph>` |

L1Graph: `nodes: Vec<L1Node>`, `edges: Vec<L1Edge>`.
L1Node: `id`, `scene_id`, `topic_ids`, `depth`, `importance`, `valence`, `arousal`, **`summary`** (新增, LLM生成的摘要), **`dominant_emotion`** (新增, 主导情感标签), **`keywords`** (新增, 关键词列表), **`recall_score`** (新增, 召回相关度分数), `created_at`, `updated_at`, `edge_ids`

### L2 Context & Scene
| Method | Signature |
|--------|----------|
| `list_l2` | `fn(&self, query: TopicListQuery) -> Result<TopicListResult>` |
| `get_l2` | `fn(&self, id: &str) -> Result<Option<TopicDetail>>` |
| `update_l2` | `fn(&mut self, id: &str, fields: UpdateL2Fields) -> Result<TopicDetail>` |
| `delete_l2` | `fn(&mut self, id: &str) -> Result<()>` |
| `delete_turn` | `fn(&mut self, id: &str, range: Range<usize>) -> Result<()>` |
| `merge_l2` | `fn(&mut self, primary_id: &str, merge_ids: Vec<String>) -> Result<MergeResult>` |
| `list_scene_tree` | `fn(&self, scene_id: &str) -> Result<SceneTreeResult>` |

MergeResult: `primary_id` (主场景ID), `merged_count` (合并的场景数), `new_turn_count` (合并后的总轮次数), `absorbed_topic_ids` (被吸收的场景ID列表)

TopicSummary: `id`, `depth`, `scene_id` (String), `user_keywords`, `agent_keywords`, `fused_keywords`, **`fused_summary`** (新增, LLM融合摘要), **`turn_count`** (新增, 总轮次数), **`is_active`** (新增, 当前是否活跃), `created_at`, `l4_count`, `l3_count`, `updated_at`

### L3 Knowledge Graph
| Method | Signature |
|--------|----------|
| `get_l3` | `fn(&self, id: &str) -> Result<Option<L3Detail>>` |
| `update_l3` | `fn(&mut self, id: &str, fields: UpdateL3Fields) -> Result<L3Detail>` |
| `delete_l3` | `fn(&mut self, id: &str) -> Result<()>` |
| `list_knowledge` | `fn(&self, query: KnowledgeListQuery) -> Result<KnowledgeListResult>` |
| `get_knowledge` | `fn(&self, id: &str) -> Result<Option<KnowledgeDetail>>` |
| `query_knowledge_nodes` | `fn(&self, query: KnowledgeNodeQuery) -> Result<KnowledgeNodesResult>` |
| `graph_query` | `fn(&mut self, graph_id, start_node, max_depth, edge_kinds) -> Result<Subgraph>` |
| `l3_query` | `fn(&mut self, graph_id, query, page) -> Result<QueryResult>` |

KnowledgeNodeQuery: `ByIds { ids, include_text }` | `ByKeyword { graph_id, keyword, limit }` | `ByType { graph_id, node_type, limit }`

### L4 Archive
| Method | Signature |
|--------|----------|
| `query_archives` | `fn(&self, query: ArchiveQuery) -> Result<Vec<Archive>>` |

### L5 Action Chain
| Method | Signature |
|--------|----------|
| `get_l5` | `fn(&self, id: &str) -> Result<Option<CrystalSummary>>` |
| `update_l5` | `fn(&mut self, id: &str, fields: UpdateL5Fields) -> Result<CrystalSummary>` |
| `delete_l5` | `fn(&mut self, id: &str) -> Result<()>` |
| `list_crystals` | `fn(&self, query: CrystalListQuery) -> Result<CrystalListResult>` |

## Session & Diagnostics

| Method | Signature |
|--------|----------|
| `session_status` | `fn(&self) -> SessionStatus` |
| `health_check` | `fn(&self) -> Result<HealthStatus>` |

## Configuration

MemHopConfig: `db_path`, `vector_dim`, `encoder_grpc_addr` (必选, MemHop 要求 gRPC 编码器始终可用), `llm`, `search_weights`, `decay_config`, `session_config`, `llm_preprocess`。详见 `src/config.rs`。

## 模块化导出路径

为了便于 meowAgent SDK 集成, MemHop 提供模块化 re-export 路径:

```rust
// 搜索相关类型
use memhop::search::{SearchQuery, SearchResult, ContextResult, SearchFilters};
// 配置/Profile
use memhop::profile::ProfileResult;
// 更新操作
use memhop::update::{UpdateRequest, UpdateResult, UpdateStatus};
// 批量存储
use memhop::store_mod::{StoreItem, StoreBatch, StoreResult};
// L2 上下文
use memhop::l2::{TopicDetail, TopicListQuery, TopicListResult, TopicSummary, UpdateL2Fields, MergeResult, SceneTreeResult};
// L4 归档
use memhop::l4::{ArchiveQuery, Archive, ArchiveListResult, ArchivePageQuery};
// L5 行动链
use memhop::l5::{CrystalListQuery, CrystalListResult, CrystalSummary, UpdateL5Fields};
// L1 图结构
use memhop::l1::{L1Graph, L1Node, L1Edge};
// 诊断
use memhop::diagnostics::HealthStatus;
// 会话
use memhop::session_mod::SessionStatus;
// 数据导入
use memhop::import::{ImportRequest, ImportResult, ImportData, ImportMode, TargetLayer, TopicImportItem, KnowledgeImportItem};
// L3 超图类型
use memhop::l3_types::{Subgraph, SubgraphNode, SubgraphEdge, EdgeKind, GraphNode, GraphEdge, L3Detail, L3Preview};
```

传统的平铺导出 (`use memhop::{SearchQuery, ...}`) 仍然有效, 与新模块化路径完全兼容。

## Pagination Pattern

所有 List 方法统一模式：请求 `XxxListQuery { page, page_size, ... }` → 响应 `XxxListResult { items, total, has_more }`。

## Errors

`MemHopError` (`#[non_exhaustive]`): `Io`, `ConfigError`, `EncoderError`, `NotFound`, `InvalidQuery`, `Serialization`, `Deserialization`, `VectorDimensionMismatch`, `Corruption` 等。
