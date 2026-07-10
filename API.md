# MemHop API v0.57.0

> `memhop` — Agent-oriented memory database with L0-L6 cognitive architecture.
> All methods return `Result<T, MemHopError>`. Feature flags: `grpc-encoder` (default), `llm` (default).

## Quick Start

```toml
memhop = "0.57"
```

```rust
use memhop::{MemHop, MemHopConfig, SearchQuery, UpdateRequest};
use std::path::PathBuf;

let config = MemHopConfig::new(PathBuf::from("memory.meh"), 768);
let mut db = MemHop::open(config)?;

// Search memory
let results = db.search_context(SearchQuery {
    dialogue: "What did we discuss yesterday?".into(),
    l2_id: None, l3_id: None, auto_create: false,
})?;

// Store new memory
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
| `open` | `fn(config: MemHopConfig) -> Result<Self>` | Open/create .meh database, auto-connect encoder |
| `close` | `fn(self) -> Result<()>` | Checkpoint and close |
| `checkpoint` | `fn(&mut self) -> Result<()>` | Persist in-memory indices to snapshot |

## Core API

### search_context — 多层记忆检索

```rust
fn search_context(&mut self, query: SearchQuery) -> Result<SearchResult>
```

**SearchQuery**（4 个字段）:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `dialogue` | `String` | ✅ | 当前用户对话内容 |
| `l2_id` | `Option<String>` | | 定向 L2 上下文 ID，存在则跳过检索直接关联 |
| `l3_id` | `Option<String>` | | 定向 L3 超图 ID，限制检索范围 |
| `auto_create` | `bool` | | 无匹配时自动创建新 L2 场景 |

**SearchResult**: `profile: Option<ProfileResult>`, `contexts: Vec<ContextResult>`, `associated_contexts: Vec<ContextResult>`, `l3_ids: Vec<String>`, `l1_previews: Vec<L1Preview>`, `l3_previews: Vec<L3Preview>`

### update_memory — 记忆写入

```rust
fn update_memory(&mut self, request: UpdateRequest) -> Result<UpdateResult>
```

UpdateRequest 核心字段: `topic_id`, `dialogue_text`, `summary`, `action_chain`, `instant_distill`, `scene_id`, `user_keywords`, `agent_keywords`, `source`

### batch_store — 批量编码存储

```rust
fn batch_store(&mut self, batch: StoreBatch) -> Result<BatchReport>  // requires grpc-encoder
```

### import_memory — 数据导入

```rust
fn import_memory(&mut self, request: ImportRequest) -> Result<ImportResult>
```

ImportRequest: `mode: ImportMode` (Append/Merge/Replace), `data: ImportData` (Profile/Topics/Knowledge)

### dream — 记忆巩固

```rust
fn dream(&mut self, l2_ids: Option<Vec<String>>) -> Result<DreamReport>  // requires llm
```

## Cognitive Layer CRUD

### L0 Profile
| Method | Signature |
|--------|-----------|
| `get_profile` | `fn(&self) -> Result<Option<ProfileResult>>` |

### L1 Engraph Memory
| Method | Signature | Description |
|--------|-----------|-------------|
| `get_l1_graph` | `fn(&self, scene_id: Option<&str>) -> Result<L1Graph>` | 获取 L1 完整节点+边图结构 |

L1Graph: `nodes: Vec<L1Node>`, `edges: Vec<L1Edge>`. L1Node 包含 id/scene_id/topic_ids/depth/importance/valence/arousal/edge_ids。L1Edge 包含 id/kind/node_ids/weight。

### L2 Context & Scene
| Method | Signature |
|--------|-----------|
| `list_l2` | `fn(&self, query: TopicListQuery) -> Result<TopicListResult>` |
| `get_l2` | `fn(&self, id: &str) -> Result<Option<TopicDetail>>` |
| `update_l2` | `fn(&mut self, id: &str, fields: UpdateL2Fields) -> Result<TopicDetail>` |
| `delete_l2` | `fn(&mut self, id: &str) -> Result<()>` |
| `delete_turn` | `fn(&mut self, id: &str, range: Range<usize>) -> Result<()>` |
| `merge_l2` | `fn(&mut self, primary_id: &str, merge_ids: Vec<String>) -> Result<MergeResult>` |
| `list_scene_tree` | `fn(&self, scene_id: &str) -> Result<SceneTreeResult>` |

### L3 Knowledge Graph
| Method | Signature |
|--------|-----------|
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
|--------|-----------|
| `query_archives` | `fn(&self, query: ArchiveQuery) -> Result<Vec<Archive>>` |

ArchiveQuery: `topic_id?: String`, `keyword?: String`, `time_range?: (i64, i64)`, `page: usize`, `page_size: usize`

### L5 Action Chain
| Method | Signature |
|--------|-----------|
| `get_l5` | `fn(&self, id: &str) -> Result<Option<CrystalSummary>>` |
| `update_l5` | `fn(&mut self, id: &str, fields: UpdateL5Fields) -> Result<CrystalSummary>` |
| `delete_l5` | `fn(&mut self, id: &str) -> Result<()>` |
| `list_crystals` | `fn(&self, query: CrystalListQuery) -> Result<CrystalListResult>` |

### L6 Pathway
| Method | Signature |
|--------|-----------|
| `get_l6` | `fn(&self, id: &str) -> Result<Option<PathwayWeightSlot>>` |
| `update_l6` | `fn(&mut self, id: &str, fields: UpdateL6Fields) -> Result<PathwayWeightSlot>` |
| `delete_l6` | `fn(&mut self, id: &str) -> Result<()>` |
| `list_l6` | `fn(&self, filter: Option<L6Filter>) -> Result<Vec<PathwayWeightSlot>>` |
| `add_l6` | `fn(&mut self, slots: Vec<PathwayWeightSlot>) -> Result<usize>` |

UpdateL6Fields 包含 `weight_delta: Option<f32>` 用于增量调整权重。

## Session & Diagnostics

| Method | Signature | Description |
|--------|-----------|-------------|
| `session_status` | `fn(&self) -> SessionStatus` | 获取活跃话题 ID 列表、数量、是否为空 |
| `health_check` | `fn(&self) -> Result<HealthStatus>` | 检查实例健康状态 |

SessionStatus: `active_topic_ids: Vec<String>`, `count: usize`, `is_empty: bool`

## Configuration

MemHopConfig 核心字段: `db_path: PathBuf`, `vector_dim: usize`, `encoder_grpc_addr: Option<String>`, `llm: LlmConfig`, `search_weights: Option<SearchWeights>`, `decay_config: Option<DecayConfig>`, `session_config: Option<SessionConfig>`, `llm_preprocess: LlmPreprocessConfig`

详细字段定义参见 `src/config.rs` 源码注释。

## Pagination Pattern

所有 List 方法遵循统一模式：
- 请求: `XxxListQuery { page: usize, page_size: usize, ...filters }`
- 响应: `XxxListResult { items: Vec<T>, total: usize, has_more: bool }`

## Errors

`MemHopError` (`#[non_exhaustive]`): `Io`, `ConfigError`, `EncoderError`, `NotFound`, `InvalidQuery`, `Serialization`, `Deserialization`, `VectorDimensionMismatch`, `Corruption`, 等。
