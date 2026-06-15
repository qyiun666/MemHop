# MemHop 新版 API 文档

本文档基于实际代码实现，描述 MemHop 六层认知架构（L0-L5）的所有公开接口。

---

## 目录

- [接口1：创建/打开数据库](#接口1创建打开数据库)
- [接口2：检索记忆](#接口2检索记忆)
- [接口3：更新记忆](#接口3更新记忆)
- [接口4：Dream整合](#接口4dream整合)
- [接口5：查询L0画像](#接口5查询l0画像)
- [接口6：查询L1情节记忆](#接口6查询l1情节记忆)
- [接口7：查询L2主题列表](#接口7查询l2主题列表)
- [接口8：查询L2主题详情](#接口8查询l2主题详情)
- [接口9：查询L3知识列表](#接口9查询l3知识列表)
- [接口10：查询L3知识详情](#接口10查询l3知识详情)
- [接口11：查询L4归档内容](#接口11查询l4归档内容)
- [接口12：查询L5结晶技能列表](#接口12查询l5结晶技能列表)
- [接口13：修改L0画像](#接口13修改l0画像)
- [接口14：修改L2标题](#接口14修改l2标题)
- [接口15：修改L3标题](#接口15修改l3标题)
- [接口16：修改L5标题](#接口16修改l5标题)
- [接口17：关闭与同步数据库](#接口17关闭与同步数据库)
- [接口18：合并L2主题](#接口18合并l2主题)
- [接口19：导入记忆](#接口19导入记忆)
- [接口20：会话管理](#接口20会话管理)
- [接口21：批量存储](#接口21批量存储)

---

## 通用类型

### LLM配置

```rust
#[derive(Debug, Clone)]
pub struct LlmConfig {
    /// API端点URL
    pub api_url: String,
    /// API密钥
    pub api_key: String,
    /// 模型名称
    pub model: String,
    /// API格式（1=OpenAI格式，支持OpenAI、DeepSeek等）
    pub api_format: u8,
}
```

### 配置项

```rust
#[derive(Debug, Clone)]
pub struct MemHopConfig {
    /// .meh 数据库文件路径
    pub db_path: PathBuf,
    /// 向量编码器Unix套接字路径
    pub encoder_socket: PathBuf,
    /// 向量维度（创建时确定，不可更改）
    pub vector_dim: usize,
    /// 结晶化知识存储路径（可选，默认与db_path同目录）
    pub crystal_path: Option<PathBuf>,
}
```

```rust
impl MemHopConfig {
    /// 创建配置，encoder_socket 默认为 /tmp/memhop_encoder.sock
    pub fn new(db_path: PathBuf, vector_dim: usize) -> Self;
}
```

### 通用分页结构

所有列表查询接口使用相同的分页模式：

```rust
pub struct ListResult<T> {
    pub items: Vec<T>,
    pub total: usize,
    pub page: usize,
    pub page_size: usize,
    pub has_more: bool,
}
```

---

## 接口1：创建/打开数据库

### 功能说明

创建或打开一个 MemHop 数据库实例。自动初始化内存映射、B-tree 索引、稀疏索引、激活管理器、会话管理器和编码器。

### 接口签名

```rust
pub fn open(config: MemHopConfig) -> Result<Self>
```

### 参数说明

| 参数 | 类型 | 必需 | 描述 |
|------|------|------|------|
| `db_path` | `PathBuf` | 是 | 数据库文件路径（如 `./data/agent.meh`） |
| `encoder_socket` | `PathBuf` | 是 | 向量模型Unix套接字路径 |
| `vector_dim` | `usize` | 是 | 向量维度（创建时确定，不可更改） |
| `crystal_path` | `Option<PathBuf>` | 否 | 结晶化知识存储路径 |

### 请求示例

```rust
// 基本配置
let config = MemHopConfig::new(PathBuf::from("./data/agent.meh"), 768);
let mut db = MemHop::open(config)?;

// 自定义编码器套接字和结晶路径
let config = MemHopConfig {
    db_path: PathBuf::from("./data/agent.meh"),
    encoder_socket: PathBuf::from("/tmp/custom_encoder.sock"),
    vector_dim: 1024,
    crystal_path: Some(PathBuf::from("./crystals")),
};
let mut db = MemHop::open(config)?;
```

### 错误类型

```rust
pub enum MemHopError {
    Io(io::Error),
    InvalidMagic,
    CrcMismatch,
    InvalidVersion { expected: u16, actual: u16 },
    PageNotFound(u32),
    InvalidPageType,
    Serialization(String),
    VectorDimensionMismatch { expected: usize, actual: usize },
    ConfigError(String),
}
```

---

## 接口2：检索记忆

### 功能说明

根据对话内容检索相关记忆。采用 **L2 中心化扇出检索模型**，使用三重检索（向量相似度 + BM25 + n-gram Jaccard）对 L2 主题进行匹配，然后通过 L1 超边关联到其他 L2 上下文，返回 L0 画像、匹配的 L2 上下文列表、关联的 L3/L4 引用。

### 接口签名

```rust
pub fn search_memory(&mut self, query: SearchQuery) -> Result<SearchResult>
```

### SearchQuery 参数

| 字段 | 类型 | 必需 | 默认值 | 描述 |
|------|------|------|--------|------|
| `dialogue` | `String` | 是 | - | 当前对话内容 |
| `context_id` | `Option<String>` | 否 | `None` | L2主题ID（hex），指定后跳过三重检索，只做 L1 关联 |
| `l3_id` | `Option<String>` | 否 | `None` | L3知识ID（hex），限制只检索包含该 L3 的 L2 |
| `context_limit` | `usize` | 否 | `10` | 返回 L2 上下文数量上限 |
| `llm_enhance` | `Option<LlmConfig>` | 否 | `None` | LLM增强配置 |
| `auto_create` | `u8` | 否 | `0` | 结果为空时自动创建（0=否，1=是） |
| `min_score` | `f32` | 否 | `0.0` | 最小相关性阈值（0.0-1.0） |

### 路由逻辑

| 参数组合 | 行为 |
|----------|------|
| `auto_create=1` | 跳过检索，直接创建新的 L2 上下文 |
| `context_id` 存在且 L2 存在 | 跳过三重检索，只从该 L2 做 L1 关联 |
| `l3_id` 存在 | 限制三重检索到包含该 L3 的 L2 |
| 默认 | 完整三重检索（向量 + BM25 + n-gram） |

### SearchResult 结构

```rust
pub struct SearchResult {
    /// L0 - Agent画像
    pub profile: Option<ProfileResult>,
    /// L2 - 检索匹配的上下文列表
    pub contexts: Vec<ContextResult>,
    /// L2 - 通过L1超边关联的深度1上下文
    pub associated_contexts: Vec<ContextResult>,
    /// L3 - 匹配上下文引用的超图ID列表
    pub l3_ids: Vec<String>,
    /// L4 - 匹配上下文引用的归档引用
    pub archive_refs: Vec<ArchiveRef>,
}
```

### ContextResult 结构

```rust
pub struct ContextResult {
    pub id: String,                         // 上下文唯一ID（hex）
    pub parent_id: Option<String>,          // 父上下文ID，depth-1时为None
    pub depth: u8,                          // 嵌套深度：1=场景，2=子场景，3=轮次组
    pub title: String,                      // 场景名称/标题
    pub summary: Option<String>,            // 压缩摘要（如有）
    pub activation_score: f32,              // 激活分数（检索相关性）
    pub turn_count: u32,                    // 对话轮次数量
    pub l3_refs: Vec<String>,              // 引用的L3超图ID列表
    pub archive_refs: Vec<String>,          // 引用的L4归档ID列表
}
```

### ProfileResult 结构

```rust
pub struct ProfileResult {
    pub id: String,
    pub name: String,
    pub role: String,
    pub personality: String,
    pub worldview: String,
    pub preferences: HashMap<String, String>,
    pub created_at: i64,
    pub updated_at: i64,
}
```

### ArchiveRef 结构

```rust
pub struct ArchiveRef {
    pub id: String,                 // 归档唯一ID（hex）
    pub context_id: String,         // 关联的L2上下文ID
    pub content_type: String,       // 内容类型
    pub created_at: i64,            // 时间戳
}
```

### 请求示例

```rust
let query = SearchQuery {
    dialogue: "我想学习Rust编程语言".to_string(),
    context_id: None,
    l3_id: None,
    context_limit: 10,
    llm_enhance: None,
    auto_create: 0,
    min_score: 0.0,
};
let result = db.search_memory(query)?;

// 使用LLM增强
let query = SearchQuery {
    dialogue: "如何解决内存泄漏".to_string(),
    context_id: None,
    l3_id: None,
    context_limit: 5,
    llm_enhance: Some(LlmConfig {
        api_url: "https://api.deepseek.com/v1/chat/completions".to_string(),
        api_key: "sk-...".to_string(),
        model: "deepseek-chat".to_string(),
        api_format: 1,
    }),
    auto_create: 1,
    min_score: 0.0,
};
let result = db.search_memory(query)?;

println!("Profile: {:?}", result.profile);
for ctx in &result.contexts {
    println!("[{}] {} (depth={}, score={})", ctx.id, ctx.title, ctx.depth, ctx.activation_score);
}
```

### 返回示例

```rust
SearchResult {
    profile: Some(ProfileResult {
        id: "profile_001".to_string(),
        name: "小助手".to_string(),
        role: "AI助手".to_string(),
        personality: "友好、专业".to_string(),
        worldview: "帮助用户解决问题".to_string(),
        preferences: HashMap::new(),
        created_at: 1718304000000,
        updated_at: 1718390400000,
    }),
    contexts: vec![
        ContextResult {
            id: "a1b2c3d4e5f67890".to_string(),
            parent_id: None,
            depth: 1,
            title: "Rust编程学习".to_string(),
            summary: Some("用户学习Rust的过程".to_string()),
            activation_score: 0.85,
            turn_count: 5,
            l3_refs: vec!["knowledge_001".to_string()],
            archive_refs: vec!["archive_001".to_string()],
        },
    ],
    associated_contexts: vec![],
    l3_ids: vec!["knowledge_001".to_string()],
    archive_refs: vec![
        ArchiveRef {
            id: "archive_001".to_string(),
            context_id: "a1b2c3d4e5f67890".to_string(),
            content_type: "text".to_string(),
            created_at: 1718304000000,
        },
    ],
}
```

---

## 接口3：更新记忆

### 功能说明

将当前对话轮次的内容更新到已激活的 L2 上下文中。执行以下操作：
1. 写入 `dialogue_text` 到 L4 ArchiveSlot
2. 写入 `action_chain` 到 L5 ActionChainSlot
3. 追加 L4 archive_id 到 L2 archive_refs 索引
4. 追加 summary 到 L2 上下文摘要

**前置条件**：L2 主题必须已通过 `search_memory()` 激活。

### 接口签名

```rust
pub fn update_memory(&mut self, request: UpdateRequest) -> Result<UpdateResult>
```

### UpdateRequest 参数

| 字段 | 类型 | 必需 | 描述 |
|------|------|------|------|
| `topic_id` | `String` | 是 | 已激活的 L2 主题 ID（由 `search_memory` 返回） |
| `dialogue_text` | `String` | 是 | 当前轮对话原文 |
| `summary` | `Option<String>` | 否 | 当前轮压缩摘要（追加到 L2 上下文摘要） |
| `action_chain` | `Vec<ActionItem>` | 是 | 动作链（写入 L5） |

### ActionItem 结构

```rust
pub struct ActionItem {
    pub title: String,                          // 动作标题
    pub description: String,                    // 动作描述
    pub action_type: ActionType,                // 动作类型
    pub parameters: Option<HashMap<String, String>>, // 动作参数（可选）
}

pub enum ActionType {
    Create,   // 创建
    Read,     // 读取
    Update,   // 更新
    Delete,   // 删除
    Execute,  // 执行
    Query,    // 查询
    Custom,   // 自定义
}
```

### UpdateResult 结构

```rust
pub struct UpdateResult {
    pub topic_id: String,       // L2 主题 ID
    pub archive_id: String,     // 创建的 L4 归档 ID
    pub status: UpdateStatus,   // 更新状态
}

pub enum UpdateStatus {
    Updated,
}
```

### 请求示例

```rust
let request = UpdateRequest {
    topic_id: "a1b2c3d4e5f67890".to_string(),
    dialogue_text: "用户：Rust的借用规则是什么？\n助手：Rust的借用规则...".to_string(),
    summary: Some("用户询问Rust借用规则".to_string()),
    action_chain: vec![
        ActionItem {
            title: "解释借用规则".to_string(),
            description: "向用户解释Rust的借用和引用规则".to_string(),
            action_type: ActionType::Execute,
            parameters: None,
        },
    ],
};
let result = db.update_memory(request)?;
println!("Archive ID: {}", result.archive_id);
```

### 返回示例

```rust
UpdateResult {
    topic_id: "a1b2c3d4e5f67890".to_string(),
    archive_id: "archive_002".to_string(),
    status: UpdateStatus::Updated,
}
```

---

## 接口4：Dream整合

### 功能说明

对当前所有激活的 L2 上下文执行记忆巩固管道：
1. **L2 深度降级**：depth-1 → depth-2（主→次），depth-2 → depth-3（次→次次），depth-3 → 移除
2. **L1 重建**：基于更新后的 L2 重建 L1 超边
3. **L0 画像更新**：从 L1 重新生成 Agent 画像
4. **L5 结晶化**：从所有 ActionChainSlot 提取模式生成技能结晶

### 接口签名

```rust
pub fn dream(&mut self, llm: LlmConfig) -> Result<DreamReport>
```

### 参数说明

| 参数 | 类型 | 必需 | 描述 |
|------|------|------|------|
| `llm` | `LlmConfig` | 是 | LLM配置，用于摘要压缩、模式提取等 |

### DreamReport 结构

```rust
pub struct DreamReport {
    /// 从 depth-1 降级到 depth-2 的上下文（附压缩摘要）
    pub demoted_to_secondary: Vec<DemotionResult>,
    /// 从 depth-2 降级到 depth-3 的上下文ID列表
    pub demoted_to_tertiary: Vec<String>,
    /// 被移除的上下文ID列表（depth-3 → 移除）
    pub removed_contexts: Vec<String>,
    /// 从降级的 depth-1 节点创建的新压缩上下文
    pub new_compressed: Vec<CompressResult>,
    /// 基于L2变化更新的L1节点ID列表
    pub l1_updated: Vec<String>,
    /// L0画像更新信息 (profile_id, updated_fields)
    pub l0_updated: Option<(String, Vec<String>)>,
    /// 从L5结晶化创建的新技能ID列表
    pub new_crystals: Vec<String>,
    /// 被修剪的低质量技能ID列表
    pub pruned_crystals: Vec<String>,
    /// 总执行时间（毫秒）
    pub duration_ms: u64,
}

pub struct DemotionResult {
    pub context_id: String,         // 原始上下文ID
    pub original_title: String,     // 原始标题
    pub compressed_summary: String, // 生成的压缩摘要
    pub new_depth: u8,              // 降级后的深度
}

pub struct CompressResult {
    pub new_context_id: String,     // 新创建的压缩上下文ID
    pub source_context_id: String,  // 来源上下文ID
    pub new_summary: String,        // 压缩摘要
}
```

### 请求示例

```rust
let llm = LlmConfig {
    api_url: "https://api.deepseek.com/v1/chat/completions".to_string(),
    api_key: "sk-...".to_string(),
    model: "deepseek-chat".to_string(),
    api_format: 1,
};
let report = db.dream(llm)?;
println!("执行时间: {}ms", report.duration_ms);
println!("新技能: {:?}", report.new_crystals);
```

### 返回示例

```rust
DreamReport {
    demoted_to_secondary: vec![
        DemotionResult {
            context_id: "ctx-001".to_string(),
            original_title: "Rust编程学习".to_string(),
            compressed_summary: "用户学习Rust语言的基础知识".to_string(),
            new_depth: 2,
        },
    ],
    demoted_to_tertiary: vec!["ctx-002".to_string()],
    removed_contexts: vec!["ctx-003".to_string()],
    new_compressed: vec![],
    l1_updated: vec!["node-001".to_string()],
    l0_updated: Some(("profile_001".to_string(), vec!["personality".to_string()])),
    new_crystals: vec!["crystal-001".to_string()],
    pruned_crystals: vec!["crystal-old".to_string()],
    duration_ms: 1250,
}
```

---

## 接口5：查询L0画像

### 功能说明

获取 Agent 的 L0 画像信息。

### 接口签名

```rust
pub fn get_profile(&self) -> Result<Option<ProfileResult>>
```

### 请求示例

```rust
let profile = db.get_profile()?;
if let Some(p) = profile {
    println!("Agent: {}, Role: {}", p.name, p.role);
}
```

### 返回示例

```rust
Some(ProfileResult {
    id: "profile_001".to_string(),
    name: "MemHop Agent".to_string(),
    role: "AI助手".to_string(),
    personality: "友好、专业、耐心".to_string(),
    worldview: "以用户为中心".to_string(),
    preferences: HashMap::from([
        ("language".to_string(), "中文".to_string()),
    ]),
    created_at: 1718304000000,
    updated_at: 1718390400000,
})
```

---

## 接口6：查询L1情节记忆

### 功能说明

获取 L1 情节记忆（Engram）的详细信息，支持按 ID 查询或批量分页查询。

### 接口签名

```rust
// 按ID查询单个
pub fn get_engram(&self, id: &str) -> Result<Option<EngramResult>>

// 批量查询（支持分页和过滤）
pub fn list_engrams(&self, query: EngramListQuery) -> Result<EngramListResult>
```

### EngramListQuery 参数

| 字段 | 类型 | 必需 | 默认值 | 描述 |
|------|------|------|--------|------|
| `page` | `usize` | 是 | - | 页码（从1开始） |
| `page_size` | `usize` | 是 | - | 每页条数 |
| `state_filter` | `Option<String>` | 否 | `None` | 状态过滤：Active/Latent/Dormant |
| `min_importance` | `Option<f32>` | 否 | `None` | 最小重要性 |
| `keyword` | `Option<String>` | 否 | `None` | 关键词过滤 |

### EngramResult 结构

```rust
pub struct EngramResult {
    pub id: String,                     // 记忆ID
    pub text: String,                   // 原始文本
    pub summary: Option<String>,        // 摘要
    pub keywords: Vec<String>,          // 关键词
    pub memory_state: String,           // 状态：Active/Latent/Dormant
    pub importance: f32,               // 重要性 [0.0, 1.0]
    pub source_type: String,            // 来源类型
    pub created_at: i64,               // 创建时间
    pub updated_at: i64,               // 更新时间
    pub edge_count: usize,              // 关联边数量
    pub associated_topics: Vec<String>, // 关联的L2主题ID
}
```

### 请求示例

```rust
// 查询单个
let engram = db.get_engram("engram_001")?;

// 分页查询
let query = EngramListQuery {
    page: 1,
    page_size: 20,
    state_filter: Some("Active".to_string()),
    min_importance: Some(0.5),
    keyword: None,
};
let result = db.list_engrams(query)?;
println!("共{}条", result.total);
```

---

## 接口7：查询L2主题列表

### 功能说明

获取 L2 主题的列表，支持分页和关键词过滤。

### 接口签名

```rust
pub fn list_topics(&self, query: TopicListQuery) -> Result<TopicListResult>
```

### TopicListQuery 参数

| 字段 | 类型 | 必需 | 默认值 | 描述 |
|------|------|------|--------|------|
| `page` | `usize` | 是 | - | 页码 |
| `page_size` | `usize` | 是 | - | 每页条数 |
| `active_only` | `bool` | 否 | `false` | 仅显示激活主题 |
| `keyword` | `Option<String>` | 否 | `None` | 标题关键词过滤 |

### TopicSummary 结构

```rust
pub struct TopicSummary {
    pub id: String,             // 主题ID
    pub title: String,          // 主题标题
    pub depth: u8,              // 嵌套深度（1=场景，2=子场景，3=轮次组）
    pub archive_count: usize,   // 包含的归档数
    pub turn_count: u32,        // 对话轮次数
    pub is_active: bool,        // 是否激活
    pub updated_at: i64,        // 最后更新时间
}
```

### 请求示例

```rust
let query = TopicListQuery {
    page: 1,
    page_size: 50,
    active_only: true,
    keyword: Some("Rust".to_string()),
};
let result = db.list_topics(query)?;
for topic in &result.items {
    println!("[{}] {} (depth={})", topic.id, topic.title, topic.depth);
}
```

### 返回示例

```rust
TopicListResult {
    items: vec![
        TopicSummary {
            id: "a1b2c3d4e5f67890".to_string(),
            title: "Rust编程学习".to_string(),
            depth: 1,
            archive_count: 15,
            turn_count: 25,
            is_active: true,
            updated_at: 1718390400000,
        },
    ],
    total: 1,
    page: 1,
    page_size: 50,
    has_more: false,
}
```

---

## 接口8：查询L2主题详情

### 功能说明

获取单个 L2 主题的详细信息。

### 接口签名

```rust
pub fn get_topic(&self, id: &str) -> Result<Option<TopicDetail>>
```

### TopicDetail 结构

```rust
pub struct TopicDetail {
    pub id: String,                         // 主题ID
    pub title: String,                      // 主题标题
    pub summary: Option<String>,            // 主题摘要
    pub depth: u8,                          // 嵌套深度
    pub archive_refs: Vec<String>,          // 关联的L4归档ID
    pub l3_refs: Vec<String>,              // 关联的L3知识ID
    pub turn_count: u32,                    // 对话轮次数
    pub parent_id: Option<String>,          // 父主题ID
    pub is_active: bool,                    // 是否激活
    pub importance: f32,                    // 重要性
    pub activation_score: f32,              // 激活分数
    pub activation_state: String,           // 激活状态
    pub created_at: i64,                    // 创建时间
    pub updated_at: i64,                    // 更新时间
}
```

### 请求示例

```rust
let topic = db.get_topic("a1b2c3d4e5f67890")?;
if let Some(t) = topic {
    println!("主题: {}", t.title);
    println!("深度: {}, 轮次: {}", t.depth, t.turn_count);
    println!("关联L3: {:?}", t.l3_refs);
}
```

### 返回示例

```rust
Some(TopicDetail {
    id: "a1b2c3d4e5f67890".to_string(),
    title: "Rust编程学习".to_string(),
    summary: Some("用户学习Rust语言的过程记录".to_string()),
    depth: 1,
    archive_refs: vec!["archive_001".to_string(), "archive_002".to_string()],
    l3_refs: vec!["knowledge_001".to_string()],
    turn_count: 25,
    parent_id: None,
    is_active: true,
    importance: 0.8,
    activation_score: 0.6,
    activation_state: "Active".to_string(),
    created_at: 1718304000000,
    updated_at: 1718390400000,
})
```

---

## 接口9：查询L3知识列表

### 功能说明

获取 L3 超图知识域的列表，支持分页和过滤。

### 接口签名

```rust
pub fn list_knowledge(&self, query: KnowledgeListQuery) -> Result<KnowledgeListResult>
```

### KnowledgeListQuery 参数

| 字段 | 类型 | 必需 | 默认值 | 描述 |
|------|------|------|--------|------|
| `page` | `usize` | 是 | - | 页码 |
| `page_size` | `usize` | 是 | - | 每页条数 |
| `domain_filter` | `Option<String>` | 否 | `None` | 域过滤 |
| `knowledge_type` | `Option<String>` | 否 | `None` | 知识类型：Factual/Procedural/Conceptual/Contextual |
| `keyword` | `Option<String>` | 否 | `None` | 关键词 |

### KnowledgeSummary 结构

```rust
pub struct KnowledgeSummary {
    pub id: String,                 // 知识ID
    pub title: String,              // 知识标题
    pub domain: String,             // 所属域
    pub knowledge_type: String,     // 知识类型
    pub importance: f32,            // 重要性
    pub confidence: f32,            // 置信度
    pub updated_at: i64,            // 更新时间
}
```

### 请求示例

```rust
let query = KnowledgeListQuery {
    page: 1,
    page_size: 20,
    domain_filter: Some("programming".to_string()),
    knowledge_type: Some("Procedural".to_string()),
    keyword: None,
};
let result = db.list_knowledge(query)?;
for k in &result.items {
    println!("[{}] {} ({})", k.id, k.title, k.domain);
}
```

---

## 接口10：查询L3知识详情

### 功能说明

获取单个 L3 超图知识域的详细信息，包括内容、关键词、引用等。

### 接口签名

```rust
pub fn get_knowledge(&self, id: &str) -> Result<Option<KnowledgeDetail>>
```

### KnowledgeDetail 结构

```rust
pub struct KnowledgeDetail {
    pub id: String,                     // 知识ID
    pub title: String,                  // 知识标题
    pub domain: String,                 // 所属域
    pub knowledge_type: String,         // 知识类型
    pub text: String,                   // 知识内容
    pub summary: Option<String>,        // 摘要
    pub keywords: Vec<String>,          // 关键词
    pub edge_ptrs: Vec<String>,         // 关联超边
    pub archive_refs: Vec<String>,      // 关联的L4归档ID
    pub source_ref: Option<String>,     // 来源引用
    pub importance: f32,                // 重要性
    pub confidence: f32,                // 置信度
    pub created_at: i64,                // 创建时间
    pub updated_at: i64,                // 更新时间
}
```

### 请求示例

```rust
let knowledge = db.get_knowledge("knowledge_001")?;
if let Some(k) = knowledge {
    println!("知识: {}", k.title);
    println!("类型: {}", k.knowledge_type);
    println!("内容: {}", k.text);
}
```

### 返回示例

```rust
Some(KnowledgeDetail {
    id: "knowledge_001".to_string(),
    title: "Rust所有权系统".to_string(),
    domain: "programming".to_string(),
    knowledge_type: "Conceptual".to_string(),
    text: "Rust的所有权系统是其内存安全的核心...".to_string(),
    summary: Some("Rust所有权规则和借用检查器".to_string()),
    keywords: vec!["ownership".to_string(), "borrowing".to_string()],
    edge_ptrs: vec![],
    archive_refs: vec!["archive_003".to_string()],
    source_ref: Some("/docs/rust-ownership.md".to_string()),
    importance: 0.9,
    confidence: 0.95,
    created_at: 1718304000000,
    updated_at: 1718390400000,
})
```

---

## 接口11：查询L4归档内容

### 功能说明

获取 L4 归档的原始对话内容，支持多种查询方式和分页。

### 接口签名

```rust
// 按L2主题ID查询
pub fn list_archives_by_topic(&self, topic_id: &str, query: ArchivePageQuery) -> Result<ArchiveListResult>

// 按节点ID列表查询
pub fn list_archives_by_nodes(&self, node_ids: &[String], query: ArchivePageQuery) -> Result<ArchiveListResult>

// 查询全部（分页）
pub fn list_all_archives(&self, query: ArchivePageQuery) -> Result<ArchiveListResult>
```

### ArchivePageQuery 参数

| 字段 | 类型 | 必需 | 默认值 | 描述 |
|------|------|------|--------|------|
| `page` | `usize` | 是 | - | 页码（从1开始） |
| `page_size` | `usize` | 是 | - | 每页条数（默认20，最大100） |
| `start_time` | `Option<i64>` | 否 | `None` | 开始时间戳 |
| `end_time` | `Option<i64>` | 否 | `None` | 结束时间戳 |
| `content_type` | `Option<String>` | 否 | `None` | 内容类型过滤 |

### Archive 结构

```rust
pub struct Archive {
    pub id: String,                     // 归档ID
    pub content: String,                // 原始内容
    pub content_type: String,           // 内容类型
    pub source_ref: Option<String>,     // 来源引用
    pub topic_id: Option<String>,       // 关联的L2主题ID
    pub engram_ids: Vec<String>,        // 关联的Engram节点ID
    pub created_at: i64,                // 创建时间
}
```

### 请求示例

```rust
let query = ArchivePageQuery {
    page: 1,
    page_size: 20,
    start_time: None,
    end_time: None,
    content_type: None,
};

// 按主题查询
let result = db.list_archives_by_topic("a1b2c3d4e5f67890", query)?;

// 按节点ID查询
let node_ids = vec!["engram_001".to_string()];
let result = db.list_archives_by_nodes(&node_ids, query)?;

// 查询全部
let result = db.list_all_archives(query)?;
for archive in &result.items {
    println!("[{}] {}", archive.id, &archive.content[..50.min(archive.content.len())]);
}
```

### 返回示例

```rust
ArchiveListResult {
    items: vec![
        Archive {
            id: "archive_001".to_string(),
            content: "用户：什么是Rust的所有权？\n助手：Rust的所有权系统是...".to_string(),
            content_type: "dialogue".to_string(),
            source_ref: None,
            topic_id: Some("a1b2c3d4e5f67890".to_string()),
            engram_ids: vec!["engram_001".to_string()],
            created_at: 1718304000000,
        },
    ],
    total: 1,
    page: 1,
    page_size: 20,
    has_more: false,
}
```

---

## 接口12：查询L5结晶技能列表

### 功能说明

获取 L5 结晶化技能的列表。

### 接口签名

```rust
pub fn list_crystals(&self, query: CrystalListQuery) -> Result<CrystalListResult>
```

### CrystalListQuery 参数

| 字段 | 类型 | 必需 | 默认值 | 描述 |
|------|------|------|--------|------|
| `page` | `usize` | 是 | - | 页码 |
| `page_size` | `usize` | 是 | - | 每页条数 |
| `status_filter` | `Option<String>` | 否 | `None` | 状态过滤：active/inactive/deprecated |
| `min_trigger_count` | `Option<u32>` | 否 | `None` | 最小触发次数 |
| `keyword` | `Option<String>` | 否 | `None` | 标题关键词 |

### CrystalSummary 结构

```rust
pub struct CrystalSummary {
    pub id: String,                     // 技能ID
    pub title: String,                  // 技能标题
    pub condition: String,              // 触发条件
    pub status: String,                 // 状态：active/inactive/deprecated
    pub trigger_count: u32,             // 触发次数
    pub success_rate: f32,              // 成功率 [0.0, 1.0]
    pub last_triggered: Option<i64>,    // 最后触发时间
    pub created_at: i64,                // 创建时间
}
```

### 请求示例

```rust
let query = CrystalListQuery {
    page: 1,
    page_size: 20,
    status_filter: Some("active".to_string()),
    min_trigger_count: Some(3),
    keyword: Some("开发".to_string()),
};
let result = db.list_crystals(query)?;
for skill in &result.items {
    println!("[{}] {} (触发{}次)", skill.id, skill.title, skill.trigger_count);
}
```

### 返回示例

```rust
CrystalListResult {
    items: vec![
        CrystalSummary {
            id: "crystal-001".to_string(),
            title: "Rust代码开发流程".to_string(),
            condition: "当用户请求编写Rust代码时".to_string(),
            status: "active".to_string(),
            trigger_count: 15,
            success_rate: 0.93,
            last_triggered: Some(1718390400000),
            created_at: 1718304000000,
        },
    ],
    total: 1,
    page: 1,
    page_size: 20,
    has_more: false,
}
```

---

## 接口13：修改L0画像

### 功能说明

修改 Agent 的 L0 画像信息。采用合并策略，只更新提供的字段。

### 接口签名

```rust
pub fn update_profile(&mut self, request: UpdateProfileRequest) -> Result<ProfileResult>
```

### UpdateProfileRequest 参数

| 字段 | 类型 | 必需 | 描述 |
|------|------|------|------|
| `name` | `Option<String>` | 否 | Agent名称 |
| `role` | `Option<String>` | 否 | 角色定义 |
| `personality` | `Option<String>` | 否 | 性格描述 |
| `worldview` | `Option<String>` | 否 | 世界观 |
| `preferences` | `Option<HashMap<String, String>>` | 否 | 偏好设置（合并更新） |

### 请求示例

```rust
let request = UpdateProfileRequest {
    name: Some("小助手".to_string()),
    role: None,
    personality: Some("友好、专业、耐心".to_string()),
    worldview: None,
    preferences: Some(HashMap::from([
        ("language".to_string(), "中文".to_string()),
    ])),
};
let profile = db.update_profile(request)?;
println!("更新后名称: {}", profile.name);
```

---

## 接口14：修改L2标题

### 功能说明

修改 L2 主题的标题，同步更新稀疏索引。

### 接口签名

```rust
pub fn update_topic_title(&mut self, id: &str, new_title: String) -> Result<TopicSummary>
```

### 请求示例

```rust
let topic = db.update_topic_title("a1b2c3d4e5f67890", "Rust编程入门".to_string())?;
println!("更新后标题: {}", topic.title);
```

### 返回示例

```rust
TopicSummary {
    id: "a1b2c3d4e5f67890".to_string(),
    title: "Rust编程入门".to_string(),
    depth: 1,
    archive_count: 15,
    turn_count: 25,
    is_active: true,
    updated_at: 1718390400000,
}
```

---

## 接口15：修改L3标题

### 功能说明

修改 L3 超图知识域的标题。

### 接口签名

```rust
pub fn update_knowledge_title(&mut self, id: &str, new_title: String) -> Result<KnowledgeSummary>
```

### 请求示例

```rust
let k = db.update_knowledge_title("knowledge_001", "Rust高级编程".to_string())?;
println!("更新后标题: {}", k.title);
```

### 返回示例

```rust
KnowledgeSummary {
    id: "knowledge_001".to_string(),
    title: "Rust高级编程".to_string(),
    domain: "programming".to_string(),
    knowledge_type: "Conceptual".to_string(),
    importance: 0.9,
    confidence: 0.95,
    updated_at: 1718390400000,
}
```

---

## 接口16：修改L5标题

### 功能说明

修改 L5 结晶技能的标题。

### 接口签名

```rust
pub fn update_crystal_title(&mut self, id: &str, new_title: String) -> Result<CrystalSummary>
```

### 请求示例

```rust
let skill = db.update_crystal_title("crystal-001", "Rust开发最佳实践".to_string())?;
println!("更新后标题: {}", skill.title);
```

### 返回示例

```rust
CrystalSummary {
    id: "crystal-001".to_string(),
    title: "Rust开发最佳实践".to_string(),
    condition: "当用户请求编写Rust代码时".to_string(),
    status: "active".to_string(),
    trigger_count: 15,
    success_rate: 0.93,
    last_triggered: Some(1718390400000),
    created_at: 1718304000000,
}
```

---

## 接口17：关闭与同步数据库

### 功能说明

关闭数据库连接或手动同步数据到磁盘。

- `close()`: 执行 final checkpoint，清空 journal，标记关闭，防止 Drop 重复 checkpoint
- `sync()`: 仅将 mmap 刷新到磁盘
- `checkpoint()`: 保存 B-tree 和稀疏索引到磁盘，更新 header commit_id

### 接口签名

```rust
pub fn close(mut self) -> Result<()>
pub fn sync(&self) -> Result<()>
pub fn checkpoint(&mut self) -> Result<()>
```

### 请求示例

```rust
let config = MemHopConfig::new(PathBuf::from("./data/agent.meh"), 768);
let mut db = MemHop::open(config)?;
// ... 使用数据库 ...
db.close()?;  // 关闭并同步数据

// 或手动同步
db.sync()?;
```

---

## 接口18：合并L2主题

### 功能说明

将多个 L2 主题合并为一个主主题。合并流程：
1. 验证主L2和所有副L2是否存在
2. 将副L2的 archive_refs 和 l3_refs 合并到主L2（去重）
3. 更新 L1 节点关联指向主L2
4. 删除副L2 主题

### 接口签名

```rust
pub fn merge_topics(&mut self, primary_id: &str, secondary_ids: Vec<String>) -> Result<TopicDetail>
```

### 参数说明

| 参数 | 类型 | 必需 | 描述 |
|------|------|------|------|
| `primary_id` | `&str` | 是 | 主L2主题ID（合并后保留） |
| `secondary_ids` | `Vec<String>` | 是 | 副L2主题ID列表（合并后删除） |

### 请求示例

```rust
let merged = db.merge_topics("topic_001", vec!["topic_002".to_string(), "topic_003".to_string()])?;
println!("合并后主题: {}", merged.title);
println!("归档数: {}", merged.archive_refs.len());
```

---

## 接口19：导入记忆

### 功能说明

将外部记忆数据导入到指定的认知层级（Profile/Topic/Knowledge）。支持批量导入和三种导入模式。

### 接口签名

```rust
pub fn import_memory(&mut self, request: ImportRequest) -> Result<ImportResult>
```

### ImportRequest 参数

| 字段 | 类型 | 必需 | 描述 |
|------|------|------|------|
| `target_layer` | `TargetLayer` | 是 | 目标层级 |
| `data` | `ImportData` | 是 | 导入的数据 |
| `mode` | `ImportMode` | 否 | 导入模式（默认 Merge） |
| `knowledge_title` | `Option<String>` | 否 | 导入Topic时，关联的L3知识域标题 |

### TargetLayer 枚举

```rust
pub enum TargetLayer {
    Profile,   // L0 画像
    Topic,     // L2 主题
    Knowledge, // L3 知识域
}
```

### ImportMode 枚举

```rust
pub enum ImportMode {
    Merge,     // 合并：存在则更新，不存在则创建
    Overwrite, // 覆盖：强制覆盖已有数据
    Skip,      // 跳过：存在则跳过
}
```

### ImportData 枚举

```rust
pub enum ImportData {
    Profile {
        name: Option<String>,
        role: Option<String>,
        personality: Option<String>,
        worldview: Option<String>,
        preferences: Option<HashMap<String, String>>,
    },
    Topics(Vec<TopicImportItem>),
    Knowledge(Vec<KnowledgeImportItem>),
}
```

### TopicImportItem / KnowledgeImportItem

```rust
pub struct TopicImportItem {
    pub title: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub knowledge_domain: Option<String>, // 关联的L3知识域标题
}

pub struct KnowledgeImportItem {
    pub title: String,
    pub domain: String,
    pub knowledge_type: String, // Factual/Procedural/Conceptual/Contextual
    pub text: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub source_ref: Option<String>,
}
```

### ImportResult 结构

```rust
pub struct ImportResult {
    pub status: ImportStatus,           // 导入状态
    pub created_ids: Vec<String>,       // 创建的ID列表
    pub updated_ids: Vec<String>,       // 更新的ID列表
    pub skipped_count: usize,           // 跳过的数量
    pub errors: Vec<ImportError>,       // 错误列表
}

pub enum ImportStatus { Success, PartialSuccess, Failed }

pub struct ImportError {
    pub index: usize,   // 出错的数据索引
    pub message: String, // 错误信息
}
```

### 请求示例

```rust
// 导入L0画像
let result = db.import_memory(ImportRequest {
    target_layer: TargetLayer::Profile,
    data: ImportData::Profile {
        name: Some("AI助手".to_string()),
        role: Some("编程助手".to_string()),
        personality: Some("专业、耐心".to_string()),
        worldview: None,
        preferences: None,
    },
    mode: ImportMode::Merge,
    knowledge_title: None,
})?;

// 批量导入L2主题
let result = db.import_memory(ImportRequest {
    target_layer: TargetLayer::Topic,
    data: ImportData::Topics(vec![
        TopicImportItem {
            title: "Rust所有权系统".to_string(),
            summary: Some("Rust的所有权、借用和生命周期".to_string()),
            keywords: vec!["ownership".to_string()],
            knowledge_domain: Some("编程".to_string()),
        },
    ]),
    mode: ImportMode::Merge,
    knowledge_title: Some("编程".to_string()),
})?;

// 批量导入L3知识
let result = db.import_memory(ImportRequest {
    target_layer: TargetLayer::Knowledge,
    data: ImportData::Knowledge(vec![
        KnowledgeImportItem {
            title: "Rust所有权规则".to_string(),
            domain: "编程".to_string(),
            knowledge_type: "Factual".to_string(),
            text: "每个值都有一个所有者...".to_string(),
            summary: None,
            keywords: vec!["ownership".to_string()],
            source_ref: None,
        },
    ]),
    mode: ImportMode::Merge,
    knowledge_title: None,
})?;
```

### 从文件构建L3超图

```rust
/// 从文件路径读取文件，提取关键词，通过 BM25 搜索关联现有知识节点，
/// 创建 KnowledgeEdge 连接。
pub fn build_l3_hypergraph_from_path(&mut self, path: &std::path::Path) -> Result<ImportResult>
```

```rust
let result = db.build_l3_hypergraph_from_path(Path::new("/docs/rust-book"))?;
println!("创建了{}个超边", result.created_ids.len());
```

---

## 接口20：会话管理

### 功能说明

管理 L2 主题的激活/停用状态，用于控制哪些主题参与 Dream 整合管道。

### 接口签名

```rust
// 激活主题（添加TTL）
pub fn activate_topic(&mut self, topic_id: &str, ttl_ms: Option<i64>)

// 停用主题
pub fn deactivate_topic(&mut self, topic_id: &str)

// 获取所有已激活主题ID
pub fn get_active_topic_ids(&self) -> Vec<String>

// 调整激活TTL（delta × 600,000ms）
pub fn adjust_activation(&mut self, topic_id: &str, delta: f32)
```

### 请求示例

```rust
// 激活主题（使用默认TTL）
db.activate_topic("a1b2c3d4e5f67890", None);

// 激活主题（自定义TTL，10分钟）
db.activate_topic("a1b2c3d4e5f67890", Some(600_000));

// 获取激活的主题列表
let active_ids = db.get_active_topic_ids();
println!("当前激活: {:?}", active_ids);

// 调整激活（增加优先级）
db.adjust_activation("a1b2c3d4e5f67890", 0.5);

// 停用主题
db.deactivate_topic("a1b2c3d4e5f67890");
```

---

## 接口21：批量存储

### 功能说明

使用五阶段管道批量存储多个文档：编码 → 归档 → L1写入 → L2主题更新 → L3超图写入 → 超边创建。
需要编码器支持向量化。如果没有设置编码器，会自动使用 MockEncoder 降级。

### 接口签名

```rust
// 设置自定义编码器
pub fn set_encoder<E: Encoder + Send + Sync + 'static>(&mut self, encoder: E)

// 批量存储
pub fn batch_store(&mut self, batch: StoreBatch) -> Result<BatchReport>
```

### StoreBatch / StoreItem 结构

```rust
pub struct StoreBatch {
    pub items: Vec<StoreItem>,
    pub session_id: Option<String>,
    pub turn_id: Option<String>,
}

pub struct StoreItem {
    pub text: String,
    pub topic_label: Option<String>,
    pub domain_id: Option<String>,
    pub importance: Option<f32>,
    pub valence: Option<f64>,
    pub arousal: Option<f64>,
    pub source: SourceMeta,
    pub is_structural: bool,
    pub source_ref: Option<SourceRef>,
}
```

### BatchReport 结构

```rust
pub struct BatchReport {
    pub l4_docs: u32,          // 归档的文档数
    pub l1_nodes_created: u32, // 新创建的L1节点数
    pub l1_nodes_updated: u32, // 更新的L1节点数
    pub l2_topics_updated: u32,// 更新/创建的L2主题数
    pub l3_nodes: u32,         // L3知识节点数
    pub edges_created: u32,    // 创建的超边数
    pub dedup_skipped: u32,    // 去重跳过的项数
}
```

### 请求示例

```rust
use memhop::{MemHop, MemHopConfig};
use memhop::query::batch::{StoreBatch, StoreItem};
use memhop::util::{SourceMeta, SourceRef};

let mut db = MemHop::open(config)?;

let batch = StoreBatch {
    items: vec![
        StoreItem {
            text: "Rust的所有权系统是其内存安全的核心".to_string(),
            topic_label: Some("Rust编程".to_string()),
            domain_id: Some("编程".to_string()),
            importance: Some(0.8),
            valence: None,
            arousal: None,
            source: SourceMeta::Dialogue,
            is_structural: true,
            source_ref: None,
        },
    ],
    session_id: Some("session_001".to_string()),
    turn_id: Some("turn_001".to_string()),
};
let report = db.batch_store(batch)?;
println!("存储报告: {:?}", report);
```

---

## 接口清单

| 接口 | 方法 | 功能 | 需要LLM |
|------|------|------|----------|
| 接口1 | `MemHop::open()` | 创建/打开数据库 | 否 |
| 接口2 | `search_memory()` | 检索记忆（三重检索 + L1扇出） | 可选 |
| 接口3 | `update_memory()` | 更新记忆（L4+L5写入，L2索引更新） | 否 |
| 接口4 | `dream()` | 记忆巩固管道（L2降级+L1重建+L0更新+L5结晶） | **是** |
| 接口5 | `get_profile()` | 查询L0画像 | 否 |
| 接口6 | `get_engram()` / `list_engrams()` | 查询L1情节记忆 | 否 |
| 接口7 | `list_topics()` | 查询L2主题列表 | 否 |
| 接口8 | `get_topic()` | 查询L2主题详情 | 否 |
| 接口9 | `list_knowledge()` | 查询L3知识列表 | 否 |
| 接口10 | `get_knowledge()` | 查询L3知识详情 | 否 |
| 接口11 | `list_archives_by_*()` / `list_all_archives()` | 查询L4归档内容 | 否 |
| 接口12 | `list_crystals()` | 查询L5结晶技能列表 | 否 |
| 接口13 | `update_profile()` | 修改L0画像 | 否 |
| 接口14 | `update_topic_title()` | 修改L2标题 | 否 |
| 接口15 | `update_knowledge_title()` | 修改L3标题 | 否 |
| 接口16 | `update_crystal_title()` | 修改L5标题 | 否 |
| 接口17 | `close()` / `sync()` / `checkpoint()` | 关闭/同步数据库 | 否 |
| 接口18 | `merge_topics()` | 合并L2主题 | 否 |
| 接口19 | `import_memory()` / `build_l3_hypergraph_from_path()` | 导入记忆 / 从文件构建L3超图 | 否 |
| 接口20 | `activate_topic()` / `deactivate_topic()` / `get_active_topic_ids()` / `adjust_activation()` | 会话管理 | 否 |
| 接口21 | `batch_store()` / `set_encoder()` | 批量存储 | 否 |
