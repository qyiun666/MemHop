# MemHop API v0.57.0

> Agent-oriented memory database with L0-L6 cognitive architecture. Feature flags: `grpc-encoder` (default), `llm` (default).

## Quick Start

```rust
// Cargo.toml: memhop = "0.57"
use memhop::{MemHop, MemHopConfig, SearchQuery, UpdateRequest};
let mut db = MemHop::open(MemHopConfig::new(PathBuf::from("memory.meh"), 768))?;
let results = db.search_context(SearchQuery {
    dialogue: "What did we discuss yesterday?".into(),
    l2_id: None, l3_id: None, auto_create: false,
})?;
db.update_memory(UpdateRequest {
    topic_id: "topic-1".into(),
    dialogue_text: "We discussed the project roadmap.".into(),
    ..Default::default()
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

### search_context — 多层记忆检索  // requires grpc-encoder
`fn search_context(&mut self, query: SearchQuery) -> Result<SearchResult>`
- SearchQuery: `dialogue` (必填), `l2_id`/`l3_id` (定向范围), `auto_create` (自动建场景)
- SearchResult: `profile`, `contexts`, `associated_contexts`, `l3_ids`, `l1_previews`, `l3_previews`
### update_memory — 记忆写入
`fn update_memory(&mut self, request: UpdateRequest) -> Result<UpdateResult>`
- UpdateRequest: `topic_id`, `dialogue_text`, `summary`, `action_chain`, `instant_distill`, `scene_id`, `user_keywords`, `agent_keywords`, `source`
### batch_store — 批量编码存储
`fn batch_store(&mut self, batch: StoreBatch) -> Result<BatchReport>`  // requires grpc-encoder
### import_memory — 数据导入
`fn import_memory(&mut self, request: ImportRequest) -> Result<ImportResult>`
- ImportRequest: `mode` (Append/Merge/Replace), `data` (Profile/Topics/Knowledge)
### build_l3_hypergraph_from_path — L3 超图构建
`fn build_l3_hypergraph_from_path(&mut self, path: &Path) -> Result<ImportResult>`
### dream — 记忆巩固
`fn dream(&mut self, l2_ids: Option<Vec<String>>) -> Result<DreamReport>`  // requires llm

## Cognitive Layer CRUD

### L0 Profile
| Method | Signature |
|--------|----------|
| `get_profile` | `fn(&self) -> Result<Option<ProfileResult>>` |

### L1 Engraph Memory
| Method | Signature |
|--------|----------|
| `get_l1_graph` | `fn(&self, scene_id: Option<&str>) -> Result<L1Graph>` |
L1Graph: `nodes: Vec<L1Node>`, `edges: Vec<L1Edge>`。

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

### L6 Pathway
| Method | Signature |
|--------|----------|
| `get_l6` | `fn(&self, id: &str) -> Result<Option<PathwayWeightSlot>>` |
| `update_l6` | `fn(&mut self, id: &str, fields: UpdateL6Fields) -> Result<PathwayWeightSlot>` |
| `delete_l6` | `fn(&mut self, id: &str) -> Result<()>` |
| `list_l6` | `fn(&self, filter: Option<L6Filter>) -> Result<Vec<PathwayWeightSlot>>` |
| `add_l6` | `fn(&mut self, slots: Vec<PathwayWeightSlot>) -> Result<usize>` |

## Session & Diagnostics

| Method | Signature |
|--------|----------|
| `session_status` | `fn(&self) -> SessionStatus` |
| `health_check` | `fn(&self) -> Result<HealthStatus>` |

## Configuration

MemHopConfig: `db_path`, `vector_dim`, `encoder_grpc_addr`, `llm`, `search_weights`, `decay_config`, `session_config`, `llm_preprocess`。详见 `src/config.rs`。

## Pagination Pattern

所有 List 方法统一模式：请求 `XxxListQuery { page, page_size, ... }` → 响应 `XxxListResult { items, total, has_more }`。

## Errors

`MemHopError` (`#[non_exhaustive]`): `Io`, `ConfigError`, `EncoderError`, `NotFound`, `InvalidQuery`, `Serialization`, `Deserialization`, `VectorDimensionMismatch`, `Corruption` 等。
