# MemHop 内部接口文档

本文档描述每个外部接口内部需要调用的模块、函数和数据结构。

---

## 目录

- [外部接口1：创建/打开数据库](#外部接口1创建打开数据库)
- [外部接口2：检索记忆](#外部接口2检索记忆)
- [外部接口3：更新记忆](#外部接口3更新记忆)
- [外部接口4：Dream整合](#外部接口4dream整合)
- [外部接口5：查询L0画像](#外部接口5查询l0画像)
- [外部接口6：查询L1情节记忆](#外部接口6查询l1情节记忆)
- [外部接口7：查询L2主题列表](#外部接口7查询l2主题列表)
- [外部接口8：查询L2主题详情](#外部接口8查询l2主题详情)
- [外部接口9：查询L3知识域列表](#外部接口9查询l3知识域列表)
- [外部接口10：查询L3知识域详情](#外部接口10查询l3知识域详情)
- [外部接口11：查询L4归档内容](#外部接口11查询l4归档内容)
- [外部接口12：查询L5技能列表](#外部接口12查询l5技能列表)
- [外部接口13：修改L0画像](#外部接口13修改l0画像)
- [外部接口14：修改L2标题](#外部接口14修改l2标题)
- [外部接口15：修改L3标题](#外部接口15修改l3标题)
- [外部接口16：修改L5标题](#外部接口16修改l5标题)
- [外部接口18：合并L2主题](#外部接口18合并l2主题)
- [外部接口19：导入记忆](#外部接口19导入记忆)
- [外部接口17：关闭数据库](#外部接口17关闭数据库)
- [内部模块总览](#内部模块总览)

---

## 外部接口1：创建/打开数据库

### 外部接口签名

```rust
pub fn open(config: MemHopConfig) -> Result<MemHop>
```

### 内部调用链

```
MemHop::open(config)
├── 1. 文件操作 (file/mod.rs)
│   ├── File::open() / OpenOptions::new().create(true).open()
│   └── file.set_len(500 * 4096)  // 初始500页
│
├── 2. 内存映射 (memmap2)
│   ├── MmapMut::map_mut(&file)   // 读写映射
│   └── Mmap::map(&file)          // 只读映射（用于读取header）
│
├── 3. 文件头管理 (file/header.rs)
│   ├── read_headers(&mmap_readonly)      // 读取A/B双头
│   ├── select_valid_header(&header_a, &header_b)  // 选择有效头
│   └── FileHeader::new(vector_dim)       // 新建header（首次创建）
│
├── 4. 空闲列表初始化 (file/free_list.rs)
│   ├── init_free_list(&mut header)       // 初始化空闲列表
│   └── free_page(&mut mmap, &mut header, page_id)  // 添加页面到空闲列表
│
├── 5. 日志回放 (file/journal.rs)
│   └── replay_journal(&mmap_readonly, &header)  // 回放未完成的事务
│
├── 6. 索引加载 (index/)
│   ├── BTree::deserialize(data)          // 加载B树索引 (index/btree.rs)
│   └── SparseIndex::deserialize(data)    // 加载稀疏索引 (index/sparse.rs)
│
├── 7. 会话管理器 (session/mod.rs)
│   └── SessionManager::new()
│
├── 8. 编码器初始化 (encoder/ipc.rs)
│   ├── IpcEncoder::new()  # 尝试连接Unix套接字
│   └── MockEncoder::new() # 降级方案
│
└── 9. 返回 MemHop 实例
```

### 内部接口详情

#### 3.1 FileHeader (file/header.rs)

```rust
pub struct FileHeader {
    pub magic: [u8; 4],           // "MEH!" 魔数
    pub version: u16,             // 版本号
    pub vector_dim: u16,          // 向量维度
    pub page_count: u32,          // 总页面数
    pub free_list_head: u32,      // 空闲列表头
    pub commit_id: u64,           // 提交ID
    pub journal_start: u32,       // 日志起始页
    pub journal_len: u32,         // 日志长度
    pub layer_roots: [u32; 16],   // 各层根页面
    pub reserved: [u8; 32],       // 保留字段
    pub tail_magic: [u8; 4],      // 尾部魔数
    pub crc32: u32,               // CRC校验
}
```

**关键函数**：

- `FileHeader::new(vector_dim: u16) -> Self`
- `FileHeader::to_bytes(&self) -> Vec<u8>`
- `FileHeader::from_bytes(data: &[u8]) -> Result<Self>`
- `read_headers(mmap: &Mmap) -> Result<(FileHeader, FileHeader)>`
- `select_valid_header(a: &FileHeader, b: &FileHeader) -> Result<FileHeader>`

#### 3.2 MemHopConfig 结构体

```rust
pub struct MemHopConfig {
    /// 数据库文件路径
    pub db_path: PathBuf,

    /// 向量模型Unix套接字路径
    pub encoder_socket: PathBuf,

    /// 向量维度（创建时确定，不可更改）
    pub vector_dim: usize,

    /// 结晶化知识存储路径（可选，默认: 与db_path同目录）
    pub crystal_path: Option<PathBuf>,
}
```

**说明**：

- `crystal_path` 用于存储L5结晶化知识（Crystal节点）
- 如果未指定，默认使用 `db_path` 所在目录
- 结晶路径在数据库创建时确定，后续不可更改

#### 3.3 空闲列表 (file/free_list.rs)

```rust
pub fn init_free_list(header: &mut FileHeader) -> Result<()>
pub fn allocate_from_free_list(mmap: &mut MmapMut, header: &mut FileHeader) -> Result<u32>
pub fn free_page(mmap: &mut MmapMut, header: &mut FileHeader, page_id: u32) -> Result<()>
```

#### 3.4 页面管理 (file/page.rs)

```rust
pub fn read_page_header(data: &[u8]) -> PageHeader
pub fn write_page_header(mmap: &mut MmapMut, page_id: u32, header: &PageHeader) -> Result<()>
pub fn read_page_data(mmap: &Mmap, page_id: u32) -> Result<&[u8]>
pub fn write_page_data(mmap: &mut MmapMut, page_id: u32, data: &[u8]) -> Result<()>
pub fn decode_page_ref(page_ref: u64) -> (u32, u16)  // (page_id, slot_index)
```

---

## 外部接口2：检索记忆

### 外部接口签名（新增）

```rust
pub fn search_memory(&mut self, query: SearchQuery) -> SearchResult
```

### 检索逻辑说明

**核心检索流程**：

1. **主要检索L2主题**：通过BM25+向量检索找到相关L2主题
2. **L1通过L2关联带出**：通过L2的`node_ids`获取关联的L1情节记忆
3. **L3通过L2关联带出**：通过L2的`l3_refs`获取关联的L3知识域
4. **L4通过L2关联带出**：通过L2的`l4_refs`获取关联的L4归档

**检索层次关系**：

```
L2 (主要检索目标)
├── L1 (通过node_ids关联)
├── L3 (通过l3_refs关联)
└── L4 (通过l4_refs关联)
```

### 内部调用链

```
search_memory(query)
│
├── [快速路径] auto_create=1（直接创建模式）
│   ├── 跳过检索流程
│   ├── 创建新L2主题
│   │   ├── 生成唯一ID（dialogue + 时间戳 + 计数器）
│   │   ├── 使用dialogue作为标题（截取前50字符）
│   │   ├── 分配新页面并写入TopicSlot
│   │   ├── 更新B树索引和稀疏索引
│   │   └── 将新创建的L2加入结果集
│   └── 直接跳到步骤 5（层级扇出）
│
├── [正常路径] auto_create=0（检索模式）
│   ├── 0. LLM优化查询（如果配置llm_enhance）
│   │   ├── 调用LLM优化查询内容
│   │   ├── 提取关键词、扩展同义词、理解用户意图
│   │   └── 返回优化后的查询文本
│   │
│   ├── 1. L2主题检索
│   │   ├── BM25文本检索 (index/sparse.rs)
│   │   │   ├── SparseIndex::tokenize(&query.dialogue)
│   │   │   └── sparse_index.search(&terms, top_k)
│   │   └── 向量检索 (index/vector.rs)
│   │       ├── encode_text(&query.dialogue) -> Vec<f16>
│   │       └── cosine_similarity(query_vec, topic_centroid)
│   │
│   ├── 2. L2结果过滤
│   │   ├── 按l2_id精确过滤（如果指定）
│   │   └── 按l3_id精确过滤（如果指定）
│   │
│   └── 继续步骤 5
│
├── 5. L1关联查询
│   ├── 遍历每个L2主题
│   ├── 读取TopicSlot.node_ids
│   ├── 通过B树查找L1 EngramSlot
│   └── 计算L1与查询的相似度
│
├── 6. L3关联查询
│   ├── 遍历每个L2主题
│   ├── 读取TopicSlot.l3_refs
│   ├── 通过B树查找L3 KnowledgeSlot
│   └── 按l3_id精确过滤（如果指定）
│
├── 7. L4关联查询
│   ├── 遍历每个L2主题
│   ├── 读取TopicSlot.l4_refs
│   └── 通过B树查找L4 ArchiveSlot
│
├── 8. L0画像查询
│   └── 读取ProfileSlot（始终返回）
│
├── 9. 激活更新
│   ├── 更新L2.activation_score
│   ├── 设置L2.is_active = true
│   ├── 更新L1.memory_state = Active
│   └── 将L2 ID加入session_manager
│
└── 10. 返回SearchResult
```

### 内部接口详情

#### 2.1 稀疏索引 - BM25检索 (index/sparse.rs)

```rust
pub struct SparseIndex {
    inverted_index: HashMap<String, Vec<(u64, f32)>>,  // term -> [(id_hash, tf)]
    doc_lengths: HashMap<u64, usize>,                   // id_hash -> doc_length
    avg_doc_length: f32,
    total_docs: usize,
}

impl SparseIndex {
    pub fn new() -> Self
    pub fn add_document(&mut self, id_hash: u64, terms: Vec<String>, doc_length: usize)
    pub fn search(&self, terms: &[String], top_k: usize) -> Vec<(u64, f32)>
    pub fn tokenize(text: &str) -> Vec<String>
    pub fn serialize(&self) -> Result<Vec<u8>>
    pub fn deserialize(data: &[u8]) -> Result<Self>
}
```

#### 2.2 向量索引 (index/vector.rs)

```rust
pub struct VectorPage {
    pub dim: u16,
    pub count: u16,
    pub slot_size: u16,
    // ... 其他字段
}

pub fn read_vector(mmap: &Mmap, page_id: u32, slot_index: u16, dim: usize) -> Result<Vec<f16>>
pub fn write_vector(mmap: &mut MmapMut, page_id: u32, slot_index: u16, id_hash: u64, vector: &[f16], dim: usize) -> Result<()>
pub fn cosine_similarity(a: &[f16], b: &[f16]) -> f32  // SIMD加速
```

#### 2.3 B树索引 (index/btree.rs)

```rust
pub struct BTreeIndex {
    tree: BTreeMap<u64, u64>,  // id_hash -> page_ref
}

impl BTreeIndex {
    pub fn new() -> Self
    pub fn insert(&mut self, id_hash: u64, page_ref: u64)
    pub fn search(&self, id_hash: u64) -> Option<u64>
    pub fn remove(&mut self, id_hash: u64) -> bool
    pub fn iter(&self) -> impl Iterator<Item = (&u64, &u64)>
    pub fn serialize(&self) -> Result<Vec<u8>>
    pub fn deserialize(data: &[u8]) -> Result<Self>
}
```

#### 2.4 记忆槽位 (slot/engram.rs)

```rust
pub struct EngramSlot {
    pub id_hash: u64,
    pub text: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub edge_count: u16,
    pub doc_len: u16,
    pub vector_page_ref: u64,
    pub is_structural: bool,
    pub source_type: u8,
    pub memory_state: u8,
    pub emotion_type: u8,
    pub valence: f32,
    pub arousal: f32,
    pub importance: f32,
    pub edge_ptrs: [u64; 8],
}

impl EngramSlot {
    pub fn serialize(&self) -> io::Result<Vec<u8>>
    pub fn deserialize(data: &[u8]) -> io::Result<Self>
    pub fn slot_size(&self) -> usize
}
```

#### 2.5 主题槽位 (slot/topic.rs)

```rust
pub struct TopicSlot {
    pub id_hash: u64,
    pub title: String,
    pub summary: Option<String>,
    pub node_ids: Vec<u64>,           // 关联的L1 Engram IDs
    pub l3_refs: Vec<u64>,            // 关联的L3 Knowledge IDs
    pub l4_refs: Vec<u64>,            // 关联的L4 Archive IDs
    pub parent_id: Option<u64>,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub importance: f32,
    pub activation_score: f32,
    pub is_active: bool,
    pub centroid_vector: Option<Vec<f16>>,
    pub domain_weights: Vec<(u64, f32)>,
    pub dialogue_range: (i64, i64),
    pub reserved: [u8; 16],
}
```

#### 2.6 知识槽位 (slot/knowledge.rs)

```rust
pub struct KnowledgeSlot {
    pub id_hash: u64,
    pub title: String,
    pub domain: String,
    pub knowledge_type: KnowledgeType,
    pub text: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub edge_count: u16,
    pub edge_ptrs: [u64; 8],
    pub archive_refs: Vec<u64>,
    pub source_ref: Option<String>,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub importance: f32,
    pub confidence: f32,
}
```

#### 2.6 结果融合 (query/fusion.rs) [已移除 — 融合逻辑内置在 search.rs]

#### 2.7 会话管理 (session/mod.rs)

```rust
pub struct SessionManager {
    // 内部状态
}

impl SessionManager {
    pub fn new() -> Self
    pub fn activate_topic(&mut self, topic_id: u64, ttl_ms: Option<i64>)
    pub fn deactivate_topic(&mut self, topic_id: u64)
    pub fn get_active_topic_ids(&self) -> HashSet<u64>
    pub fn adjust_activation(&mut self, topic_id: u64, delta: f32)
}
```

---

## 外部接口3：更新记忆

### 外部接口签名（新增）

```rust
pub fn update_memory(&mut self, request: UpdateRequest) -> UpdateResult
```

### 更新逻辑说明

**L2中心化更新模型**：

1. **查找或创建L2主题**：通过`l2_id`查找已有L2主题，如果不存在则创建新的
2. **创建L1情节记忆**：为当前轮对话创建L1 Engram
3. **创建L4原文归档**：存储当前轮对话原文到L4 Archive
4. **更新L2主题**：追加L1节点引用、L4归档引用，更新摘要
5. **创建超边关联**：建立L1->L2和L2->L4的关联边
6. **存储动作链**：将动作链存储到L5 Crystal

### 内部调用链

```
update_memory(request)
├── 1. 查找或创建L2主题
│   ├── 如果request.l2_id存在
│   │   ├── btree.search(hash_id(l2_id)) -> Option<u64>
│   │   └── 如果不存在返回错误
│   └── 如果request.l2_id为空
│       ├── hash_id(dialogue_text) -> l2_id_hash
│       └── allocate_and_write_l2_topic() -> 创建新TopicSlot
│
├── 2. 创建L1 Engram (query/update.rs)
│   ├── hash_id("{}-{}", l2_id_hash, now_ms) -> l1_id_hash
│   ├── allocate_and_write_l1_engram()
│   │   ├── allocate_from_free_list(mmap, header) -> page_id
│   │   ├── 创建EngramSlot
│   │   ├── 设置text、keywords、timestamps
│   │   ├── EngramSlot::serialize() -> 写入页面
│   │   └── btree.insert(id_hash, page_ref)
│   └── 返回l1_page_ref
│
├── 3. 创建L4 Archive (query/update.rs)
│   ├── hash_id("{}-{}", l2_id_hash, now_ms) -> l4_id_hash
│   ├── allocate_and_write_l4_archive()
│   │   ├── allocate_from_free_list(mmap, header) -> page_id
│   │   ├── 创建ArchiveSlot
│   │   ├── 设置content、topic_id、timestamps
│   │   ├── ArchiveSlot::serialize() -> 写入页面
│   │   └── btree.insert(id_hash, page_ref)
│   └── 返回l4_page_ref
│
├── 4. 更新L2主题 (query/update.rs)
│   ├── update_l2_with_new_data()
│   │   ├── btree.search(l2_id_hash) -> page_ref
│   │   ├── TopicSlot::deserialize(&mmap[offset..])
│   │   ├── 添加l1_id到node_ids（如果不存在）
│   │   ├── 添加l4_id到l4_refs（如果不存在）
│   │   ├── 更新summary（如果提供）
│   │   ├── TopicSlot::serialize() -> 写回mmap
│   │   └── 更新稀疏索引
│   └── sparse_index.add_document(id_hash, terms, doc_len)
│
├── 5. 创建超边关联 (query/update.rs)
│   ├── create_association_edges_l2_centric()
│   │   ├── 创建L1->L2边
│   │   │   ├── hash_id("edge-{:016x}-{:016x}", l1_id, l2_id)
│   │   │   ├── HyperedgeSlot { kind: Association, node_ptrs: [l1_id, l2_id] }
│   │   │   └── btree.insert(edge_hash, page_ref)
│   │   └── 创建L2->L4边
│   │       ├── hash_id("edge-{:016x}-{:016x}", l2_id, l4_id)
│   │       ├── HyperedgeSlot { kind: Hierarchical, node_ptrs: [l2_id, l4_id] }
│   │       └── btree.insert(edge_hash, page_ref)
│   └── 返回Ok(())
│
├── 6. 存储动作链到L5 Crystal (query/update.rs)
│   ├── 遍历request.action_chain
│   ├── 为每个动作创建CrystalSlot
│   │   ├── hash_id("{}-{:?}-{}", l2_id_hash, action_type, now_ms)
│   │   ├── allocate_and_write_l5_crystal()
│   │   │   ├── allocate_from_free_list(mmap, header) -> page_id
│   │   │   ├── 创建CrystalSlot
│   │   │   ├── 设置title、condition、action
│   │   │   ├── CrystalSlot::serialize() -> 写入页面
│   │   │   └── btree.insert(id_hash, page_ref)
│   │   └── 返回crystal_page_ref
│   └── 收集crystal_ids
│
└── 7. 返回 UpdateResult
    ├── memory_id: format!("{:016x}", l2_id_hash)
    ├── l1_engram_id: format!("{:016x}", l1_id_hash)
    ├── l2_topic_id: format!("{:016x}", l2_id_hash)
    ├── l3_knowledge_id: String::new() // 此模型中为空
    ├── l4_archive_id: format!("{:016x}", l4_id_hash)
    ├── l5_crystal_ids: crystal_ids
    └── status: if is_new_l2 { Created } else { Updated }
```

### 内部接口详情

#### 3.1 归档槽位 (slot/archive.rs)

```rust
pub struct ArchiveSlot {
    pub id_hash: u64,
    pub content: String,           // 原始对话文本
    pub content_type: u8,          // 内容类型（文本、文件路径等）
    pub source_ref: Option<String>, // 来源引用
    pub created_at: i64,
    pub version: u32,
}
```

#### 3.2 超边槽位 (slot/hyperedge.rs)

```rust
pub struct HyperedgeSlot {
    pub id_hash: u64,
    pub source_id: u64,            // 源节点ID
    pub target_id: u64,            // 目标节点ID
    pub edge_type: u8,             // 边类型
    pub weight: f32,               // 边权重
    pub created_at: i64,
    pub metadata: Option<String>,  // 元数据
}
```

#### 3.3 页面分配流程

```
allocate_from_free_list(mmap, header)
├── 读取free_list_head页面
├── 获取第一个空闲页面ID
├── 更新free_list_head指向下一个空闲页面
├── 更新header.free_list_head
└── 返回分配的page_id
```

#### 3.4 索引更新流程

```
更新索引
├── B树索引更新
│   ├── 计算id_hash = hash_id(original_id)
│   ├── 计算page_ref = (page_id << 16) | slot_index
│   └── btree.insert(id_hash, page_ref)
│
└── 稀疏索引更新
    ├── 提取关键词 terms = SparseIndex::tokenize(&text)
    ├── 计算文档长度 doc_len = text.len()
    └── sparse_index.add_document(id_hash, terms, doc_len)
```

---

## 外部接口4：Dream整合

### 外部接口签名

```rust
pub fn dream(&mut self, llm: LlmConfig) -> Result<DreamReport>
```

### 内部调用链

```
dream(llm)
├── 阶段1: L2压缩/深度降级 (dream/compress_stage.rs)
│   ├── 对激活的L2上下文进行深度降级
│   │   ├── depth-1 → depth-2: 压缩摘要后降级为子场景
│   │   ├── depth-2 → depth-3: 降级为轮次组
│   │   └── depth-3 → 移除: 释放页面
│   └── 返回 (demoted_sec, compressed, removed, demoted_ter)
│
├── 阶段2: L1重建 (dream/mod.rs; rebuild_l1_from_l2)
│   ├── 扫描所有L1 ContextNode
│   ├── 检查其 context_id 指向的 L2 ContextSlot 是否仍存在
│   └── 删除指向已移除L2的L1节点
│
├── 阶段3: L0更新 (dream/l0_form_stage.rs)
│   ├── 通过topic keywords分析知识分布
│   └── 更新ProfileSlot (personality, preferences)
│
├── 阶段4: L3蒸馏 (dream/l3_distill_stage.rs)
│   ├── 遍历激活的depth-1 L2上下文
│   ├── 调用LLM提取概念和关系 (JSON)
│   ├── 创建HypergraphSlot/Node/Edge
│   └── 更新L2.l3_refs
│
├── 阶段5: L5结晶 (dream/crystallize_stage.rs)
│   ├── 扫描所有ActionChainSlot
│   ├── 调用LLM提取模式生成Crystal
│   └── 修剪低质量Crystal
│
└── 返回 DreamReport
```

### 内部接口详情

#### 4.1 DreamReport (dream/prune.rs)

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
    /// 从L3蒸馏创建的新知识节点ID列表
    pub new_l3_nodes: Vec<String>,
    /// 从L5结晶化创建的新技能ID列表
    pub new_crystals: Vec<String>,
    /// 被修剪的低质量技能ID列表
    pub pruned_crystals: Vec<String>,
    /// 总执行时间（毫秒）
    pub duration_ms: u64,
}

pub struct DemotionResult {
    pub context_id: String,
    pub original_title: String,
    pub compressed_summary: String,
    pub new_depth: u8,
}

pub struct CompressResult {
    pub new_context_id: String,
    pub source_context_id: String,
    pub new_summary: String,
}
```

#### 4.2 LLM接口 (dream/llm.rs)

```rust
pub trait LlmProvider: Send + Sync {
    fn summarize(&self, texts: &[String]) -> Result<String>;
    fn extract_patterns(&self, memories: &[MemorySummary]) -> Result<Vec<Pattern>>;
    fn generate_crystal(&self, pattern: &Pattern) -> Result<CrystalDef>;
    fn fallback_summarize(&self, texts: &[String]) -> String;
    fn fallback_extract_patterns(&self, memories: &[MemorySummary]) -> Vec<Pattern>;
    fn fallback_generate_crystal(&self, pattern: &Pattern) -> CrystalDef;
}

pub struct MemorySummary {
    pub text: String,
    pub keywords: Vec<String>,
    pub timestamp: i64,
}

pub struct Pattern {
    pub description: String,
    pub frequency: u32,
    pub confidence: f32,
}

pub struct CrystalDef {
    pub condition: String,
    pub action: String,
    pub confidence: f32,
}
```

#### 4.3 各阶段详情

**阶段1: L2压缩/深度降级 (dream/compress_stage.rs)**

```rust
pub fn compress_active_contexts(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    llm: &dyn LlmProvider,
    session_topics: &HashSet<u64>,
) -> Result<(Vec<DemotionResult>, Vec<CompressResult>, Vec<String>, Vec<String>)>
```

**阶段2: L1重建 (dream/mod.rs — 内联函数)**

```rust
fn rebuild_l1_from_l2(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    session_topic_ids: &HashSet<u64>,
) -> Result<Vec<String>>
```

**阶段3: L0更新 (dream/l0_form_stage.rs)**

```rust
pub fn generate_profile(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
) -> Result<()>
```

**阶段4: L3蒸馏 (dream/l3_distill_stage.rs)**

```rust
pub fn distill_l3_knowledge(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &mut SparseIndex,
    llm: &dyn LlmProvider,
    active_topic_ids: &HashSet<u64>,
) -> Result<Vec<String>>
```

**阶段5: L5结晶 (dream/crystallize_stage.rs)**

```rust
pub fn crystallize_patterns(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    llm: &dyn LlmProvider,
) -> Result<Vec<String>>

pub fn prune_low_quality_crystals(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    page_count: u32,
) -> Result<Vec<String>>
```

---

## 外部接口5：查询L0画像

### 外部接口签名

```rust
pub fn get_l0_profile() -> Result<Option<L0Profile>>
```

### 内部调用链

```
get_l0_profile()
├── 1. 读取L0 ProfileSlot
│   ├── btree.search(hash_id("profile")) -> page_ref
│   ├── decode_page_ref(page_ref) -> (page_id, slot_index)
│   └── ProfileSlot::deserialize(&mmap[offset..])
│
├── 2. 转换为L0Profile
│   └── L0Profile {
│       id: format!("{:016x}", profile.id_hash),
│       name: profile.name,
│       role: profile.role,
│       ...
│   }
│
└── 3. 返回结果
```

---

## 外部接口6：查询L1情节记忆

### 外部接口签名

```rust
// 按ID查询单个L1
pub fn get_l1_engram(id: &str) -> Result<Option<L1Engram>>

// 批量查询L1（支持分页）
pub fn list_l1_engrams(query: L1ListQuery) -> Result<L1ListResult>
```

### 内部调用链

```
get_l1_engram(id)
├── 1. 查找L1 Engram
│   ├── id_hash = hash_id(id)
│   ├── btree.search(id_hash) -> page_ref
│   └── EngramSlot::deserialize(&mmap[offset..])
│
├── 2. 转换为L1Engram
│   └── L1Engram {
│       id: format!("{:016x}", engram.id_hash),
│       text: engram.text,
│       summary: engram.summary,
│       ...
│   }
│
└── 3. 返回结果

list_l1_engrams(query)
├── 1. 遍历B树索引
│   └── btree.iter() -> Iterator<Item = (&u64, &u64)>
│
├── 2. 过滤L1节点
│   ├── 检查page_ref对应的页面类型是否为Engram
│   ├── 应用state_filter（Active/Latent/Dormant）
│   ├── 应用min_importance过滤
│   └── 应用keyword过滤（BM25匹配）
│
├── 3. 分页处理
│   ├── 计算total
│   ├── 跳过 (page-1) * page_size 条
│   └── 取 page_size 条
│
└── 4. 返回L1ListResult
```

---

## 外部接口7：查询L2主题列表

### 外部接口签名

```rust
pub fn list_l2_topics(query: L2ListQuery) -> Result<L2ListResult>
```

### 内部调用链

```
list_l2_topics(query)
├── 1. 遍历B树索引
│   └── btree.iter() -> Iterator<Item = (&u64, &u64)>
│
├── 2. 过滤L2节点
│   ├── 检查page_ref对应的页面类型是否为Topic
│   ├── 应用active_only过滤（is_active == true）
│   └── 应用keyword过滤（标题匹配）
│
├── 3. 分页处理
│   ├── 计算total
│   ├── 跳过 (page-1) * page_size 条
│   └── 取 page_size 条
│
└── 4. 返回L2ListResult
```

---

## 外部接口8：查询L2主题详情

### 外部接口签名

```rust
pub fn get_l2_topic(id: &str) -> Result<Option<L2TopicDetail>>
```

### 内部调用链

```
get_l2_topic(id)
├── 1. 查找L2主题
│   ├── id_hash = hash_id(id)
│   ├── btree.search(id_hash) -> page_ref
│   └── TopicSlot::deserialize(&mmap[offset..])
│
├── 2. 转换为L2TopicDetail
│   └── L2TopicDetail {
│       id: format!("{:016x}", topic.id_hash),
│       title: topic.title,
│       summary: topic.summary,
│       node_ids: topic.node_ids.iter().map(|id| format!("{:016x}", id)).collect(),
│       l3_refs: topic.l3_refs.iter().map(|id| format!("{:016x}", id)).collect(),
│       l4_refs: topic.l4_refs.iter().map(|id| format!("{:016x}", id)).collect(),
│       ...
│   }
│
└── 3. 返回结果
```

---

## 外部接口9：查询L3知识域列表

### 外部接口签名

```rust
pub fn list_l3_domains(query: L3ListQuery) -> Result<L3ListResult>
```

### 内部调用链

```
list_l3_domains(query)
├── 1. 遍历B树索引
│   └── btree.iter() -> Iterator<Item = (&u64, &u64)>
│
├── 2. 过滤L3节点
│   ├── 检查page_ref对应的页面类型是否为Knowledge
│   ├── 应用domain_filter过滤
│   ├── 应用knowledge_type过滤
│   └── 应用keyword过滤（标题或文本匹配）
│
├── 3. 分页处理
│   ├── 计算total
│   ├── 跳过 (page-1) * page_size 条
│   └── 取 page_size 条
│
└── 4. 返回L3ListResult
```

---

## 外部接口10：查询L3知识域详情

### 外部接口签名

```rust
pub fn get_l3_domain(id: &str) -> Result<Option<L3DomainDetail>>
```

### 内部调用链

```
get_l3_domain(id)
├── 1. 查找L3知识域
│   ├── id_hash = hash_id(id)
│   ├── btree.search(id_hash) -> page_ref
│   └── KnowledgeSlot::deserialize(&mmap[offset..])
│
├── 2. 转换为L3DomainDetail
│   └── L3DomainDetail {
│       id: format!("{:016x}", knowledge.id_hash),
│       title: knowledge.title,
│       domain: knowledge.domain,
│       text: knowledge.text,
│       ...
│   }
│
└── 3. 返回结果
```

---

## 外部接口11：查询L4归档内容

### 外部接口签名

```rust
// 按L2主题ID查询
pub fn list_l4_by_topic(topic_id: &str, query: L4PageQuery) -> Result<L4ListResult>

// 按节点ID列表查询
pub fn list_l4_by_nodes(node_ids: &[String], query: L4PageQuery) -> Result<L4ListResult>

// 查询全部（分页）
pub fn list_l4_all(query: L4PageQuery) -> Result<L4ListResult>
```

### 内部调用链

```
list_l4_by_topic(topic_id, query)
├── 1. 查找L2主题
│   ├── id_hash = hash_id(topic_id)
│   ├── btree.search(id_hash) -> page_ref
│   └── TopicSlot::deserialize(&mmap[offset..])
│
├── 2. 获取关联的L4归档
│   └── topic.l4_refs -> Vec<u64>
│
├── 3. 读取L4归档
│   ├── 遍历l4_refs
│   ├── btree.search(l4_ref) -> page_ref
│   └── ArchiveSlot::deserialize(&mmap[offset..])
│
├── 4. 应用过滤
│   ├── 时间范围过滤（start_time, end_time）
│   └── 内容类型过滤（content_type）
│
├── 5. 分页处理
│   └── 返回L4ListResult
│
└── 6. 返回结果

list_l4_by_nodes(node_ids, query)
├── 1. 遍历node_ids
│   ├── id_hash = hash_id(node_id)
│   ├── btree.search(id_hash) -> page_ref
│   └── EngramSlot::deserialize(&mmap[offset..])
│
├── 2. 获取关联的L4归档
│   └── 通过L1关联的L2，再获取L4（需要图遍历）
│
├── 3. 应用过滤和分页
│
└── 4. 返回L4ListResult

list_l4_all(query)
├── 1. 遍历B树索引
│   └── btree.iter()
│
├── 2. 过滤L4节点
│   ├── 检查page_ref对应的页面类型是否为Archive
│   ├── 应用时间范围过滤
│   └── 应用内容类型过滤
│
├── 3. 分页处理
│
└── 4. 返回L4ListResult
```

---

## 外部接口12：查询L5技能列表

### 外部接口签名

```rust
pub fn list_l5_skills(query: L5ListQuery) -> Result<L5ListResult>
```

### 内部调用链

```
list_l5_skills(query)
├── 1. 遍历B树索引
│   └── btree.iter()
│
├── 2. 过滤L5节点
│   ├── 检查page_ref对应的页面类型是否为Crystal
│   ├── 应用status_filter过滤（active/inactive/deprecated）
│   ├── 应用min_trigger_count过滤
│   └── 应用keyword过滤（标题匹配）
│
├── 3. 分页处理
│   ├── 计算total
│   ├── 跳过 (page-1) * page_size 条
│   └── 取 page_size 条
│
└── 4. 返回L5ListResult
```

---

## 内部模块总览

### 模块依赖关系

```
┌──────────────────────────────────────────────────────────────────┐
│                     外部接口 (lib.rs)                             │
│  open / search_memory / update_memory / dream / close / sync      │
│  get_profile / list_engrams / list_topics / list_knowledge / ...  │
│  update_profile / update_topic_title / update_knowledge_title     │
│  merge_topics / import_memory / batch_store / activate_topic      │
└──────────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┬──────────────────┐
        ▼                     ▼                     ▼                  ▼
┌───────────────┐     ┌───────────────┐     ┌───────────────┐  ┌──────────────┐
│  查询层       │     │  梦境层       │     │  L3引擎       │  │  会话管理    │
│  query/       │     │  dream/       │     │  l3/          │  │  session/    │
├───────────────┤     ├───────────────┤     ├───────────────┤  ├──────────────┤
│ search.rs     │     │ mod.rs        │     │ mod.rs        │  │ mod.rs       │
│ update.rs     │     │ compress_stage│     │ store.rs      │  └──────────────┘
│ import.rs     │     │ crystallize_..│     │ index.rs      │
│ list.rs       │     │ l0_form_stage │     │ view.rs       │
│ merge.rs      │     │ l3_distill_.. │     └───────────────┘
│ update_title  │     │ llm.rs        │             │
│ batch.rs      │     │ prune.rs      │             │
│ l0_crud.rs    │     │ emotion.rs    │             ▼
│ slot_io.rs    │     └───────────────┘     ┌───────────────┐
│ common.rs     │             │             │  槽位层       │
│ types.rs      │             │             │  slot/        │
└───────────────┘             ▼             ├───────────────┤
        │                     │             │ profile.rs    │(L0)
        └─────────────────────┼─────────────│ context_node  │(L1)
                              │             │ hyperedge.rs  │(L1)
                              ▼             │ context.rs    │(L2)
┌─────────────────────────────────────┐     │ hypergraph.rs │(L3)
│             索引层                  │     │ archive.rs    │(L4)
│  index/                             │     │ action_chain  │(L5)
├─────────────────────────────────────┤     └───────────────┘
│ btree.rs  (BTreeIndex: id_hash→page)│            │
│ sparse.rs (BM25 + n-gram倒排索引)    │            │
│ vector.rs (f16向量存储 + cosine相似度)│            │
└─────────────────────────────────────┘            │
        │                                          │
        └──────────────────────────────────────────┘
                        │
                        ▼
        ┌───────────────────────────────┐
        │  文件层          │  工具层    │
        │  file/           │  util/     │
        ├──────────────────┼────────────┤
        │ free_list.rs     │ hash.rs    │
        │ header.rs        │ io_helper  │
        │ journal.rs       │ mod.rs     │
        │ page.rs          │            │
        └──────────────────┴────────────┘
```

### 关键数据流

#### 存储流程

```
BatchStore / UpdateRequest
  ↓
update_memory() / batch_store()
  ├── hash_id() → id_hash
  ├── allocate_from_free_list() → page_id
  ├── ContextSlot/ArchiveSlot/ActionChainSlot::serialize() → 写入页面
  ├── write_vector() → 写入向量页 (ContextSlot.centroid_page_ref)
  ├── btree.insert(id_hash, page_ref)
  └── sparse_index.add_document()
  ↓
UpdateResult / BatchReport
```

#### 检索流程

```
SearchQuery
  ↓
search_memory()
  ├── 路由:
  │   ├── auto_create=1 → 创建新L2 ContextSlot
  │   ├── context_id设置 → 加载指定L2
  │   └── 默认 → 三重检索
  ├── 三重检索:
  │   ├── n-gram检索 (SparseIndex.tokenize_ngram → search)
  │   ├── BM25检索 (split_whitespace → SparseIndex.search)
  │   └── 向量检索 (encoder.encode → cosine_similarity)
  ├── 融合排序 (MergeConfig: ngram 0.2 + BM25 0.5 + vector 0.3)
  ├── L1扇出 (get_l1_associated_depth1)
  ├── L0画像 (read_profile)
  ├── L3/L4引用收集 (collect_l3_ids, collect_archive_refs)
  └── 激活更新 (update_activation_scores)
  ↓
SearchResult
```

#### Dream流程

```
LlmConfig
  ↓
dream_pipeline()
  ├── Stage 1: compress_stage → L2深度降级 (depth-1→2→3→移除)
  ├── Stage 2: rebuild_l1_from_l2 → L1重建 (删除失效节点)
  ├── Stage 3: l0_form_stage → L0画像更新
  ├── Stage 4: l3_distill_stage → L3知识蒸馏 (LLM)
  ├── Stage 5: crystallize_stage → L5结晶 + 修剪
  ↓
DreamReport
```

---

## 页面布局

### 页面类型

| 页面类型       | 描述         | 结构                            |
| -------------- | ------------ | ------------------------------- |
| Header         | 文件头       | 32字节头 + 数据                 |
| FreeList       | 空闲列表     | 32字节头 + 空闲页面ID列表       |
| ContextNode    | L1图节点     | 32字节头 + ContextNode (指向L2) |
| Hyperedge      | L1超边       | 32字节头 + HyperedgeSlot        |
| Vector         | 向量页       | 32字节头 + 向量数据 (f16)       |
| SparseIndex    | 稀疏索引     | 32字节头 + 倒排表               |
| Context        | L2场景上下文 | 32字节头 + ContextSlot          |
| HypergraphSlot | L3容器       | 32字节头 + HypergraphSlot       |
| HypergraphNode | L3节点       | 32字节头 + HypergraphNode       |
| HypergraphEdge | L3边         | 32字节头 + HypergraphEdge       |
| Archive        | L4归档       | 32字节头 + ArchiveSlot          |
| ActionChain    | L5动作链     | 32字节头 + ActionChainSlot      |
| Profile        | L0画像       | 32字节头 + ProfileSlot          |
| BTreeNode      | B树内部节点  | 32字节头 + 树节点               |
| Free           | 空闲页面     | 32字节头                        |
| Overflow       | 溢出页面     | 32字节头 + 溢出数据             |

### 页面偏移计算

```rust
// 页面起始偏移
let page_offset = (page_id as usize) * PAGE_SIZE;

// 槽位数据偏移（跳过32字节头）
let data_offset = page_offset + 32;

// page_ref 编码
let page_ref = ((page_id as u64) << 16) | (slot_index as u64);

// page_ref 解码
let (page_id, slot_index) = decode_page_ref(page_ref);
```

---

## 错误处理

### MemHopError 类型

```rust
pub enum MemHopError {
    Io(io::Error),                    // IO错误
    InvalidMagic,                     // 无效魔数
    CrcMismatch,                      // CRC校验失败
    InvalidVersion { expected, actual }, // 版本不匹配
    PageNotFound(u32),                // 页面未找到
    InvalidPageType,                  // 无效页面类型
    Serialization(String),            // 序列化错误
    VectorDimensionMismatch { expected, actual }, // 向量维度不匹配
    ConfigError(String),              // 配置错误
}
```

### 错误处理策略

1. **可恢复错误**：返回默认值或空结果
   - 索引加载失败 → 使用空索引
   - 晶体匹配失败 → 跳过匹配

2. **不可恢复错误**：返回错误
   - IO错误
   - CRC校验失败
   - 版本不匹配

3. **警告错误**：打印警告但继续执行
   - 激活状态更新失败
   - 晶体触发计数更新失败

---

## 外部接口13：修改L0画像

### 外部接口签名

```rust
pub fn update_l0_profile(request: UpdateL0Request) -> Result<L0Profile>
```

### 内部调用链

```
update_l0_profile(request)
├── 1. 读取L0 ProfileSlot
│   └── ProfileSlot::deserialize(&data[page_offset..])
│
├── 2. 更新字段（仅更新非None字段）
│   ├── if request.name.is_some() → profile.name = request.name
│   ├── if request.role.is_some() → profile.role = request.role
│   ├── if request.personality.is_some() → profile.personality = request.personality
│   ├── if request.worldview.is_some() → profile.worldview = request.worldview
│   └── if request.preferences.is_some() → profile.preferences.merge(request.preferences)
│
├── 3. 更新时间戳
│   └── profile.updated_at = current_timestamp()
│
├── 4. 序列化并写回
│   └── ProfileSlot::serialize() -> 写回mmap
│
└── 5. 返回更新后的L0Profile
```

---

## 外部接口14：修改L2标题

### 外部接口签名

```rust
pub fn update_l2_title(id: &str, new_title: String) -> Result<L2TopicSummary>
```

### 内部调用链

```
update_l2_title(id, new_title)
├── 1. 查找L2主题
│   ├── id_hash = hash_id(id)
│   ├── btree.search(id_hash) -> page_ref
│   └── TopicSlot::deserialize(&data[page_offset..])
│
├── 2. 更新标题
│   ├── topic.title = new_title
│   └── topic.updated_at = current_timestamp()
│
├── 3. 更新稀疏索引
│   └── sparse_index.update_terms(id_hash, new_terms)
│
├── 4. 序列化并写回
│   └── TopicSlot::serialize() -> 写回mmap
│
└── 5. 返回L2TopicSummary
```

---

## 外部接口15：修改L3标题

### 外部接口签名

```rust
pub fn update_l3_title(id: &str, new_title: String) -> Result<L3DomainSummary>
```

### 内部调用链

```
update_l3_title(id, new_title)
├── 1. 查找L3知识域
│   ├── id_hash = hash_id(id)
│   ├── btree.search(id_hash) -> page_ref
│   └── KnowledgeSlot::deserialize(&data[page_offset..])
│
├── 2. 更新标题
│   ├── knowledge.title = new_title
│   └── knowledge.updated_at = current_timestamp()
│
├── 3. 更新稀疏索引
│   └── sparse_index.update_terms(id_hash, new_terms)
│
├── 4. 序列化并写回
│   └── KnowledgeSlot::serialize() -> 写回mmap
│
└── 5. 返回L3DomainSummary
```

---

## 外部接口16：修改L5标题

### 外部接口签名

```rust
pub fn update_l5_title(id: &str, new_title: String) -> Result<L5SkillSummary>
```

### 内部调用链

```
update_l5_title(id, new_title)
├── 1. 查找L5技能
│   ├── id_hash = hash_id(id)
│   ├── btree.search(id_hash) -> page_ref
│   └── CrystalSlot::deserialize(&data[page_offset..])
│
├── 2. 更新标题
│   ├── crystal.title = new_title
│   └── crystal.updated_at = current_timestamp()
│
├── 3. 序列化并写回
│   └── CrystalSlot::serialize() -> 写回mmap
│
└── 4. 返回L5SkillSummary
```

---

## 外部接口18：合并L2主题

### 外部接口签名

```rust
pub fn merge_l2_topics(primary_id: &str, secondary_ids: Vec<String>) -> Result<L2TopicDetail>
```

### 内部调用链

```
merge_l2_topics(primary_id, secondary_ids)
├── 1. 验证主题存在
│   ├── primary_hash = hash_id(primary_id)
│   ├── btree.search(primary_hash) -> primary_page_ref
│   ├── TopicSlot::deserialize(&mmap[primary_offset..])
│   ├── 遍历 secondary_ids
│   │   ├── secondary_hash = hash_id(secondary_id)
│   │   ├── btree.search(secondary_hash) -> secondary_page_ref
│   │   └── TopicSlot::deserialize(&mmap[secondary_offset..])
│   └── 如果任一主题不存在，返回错误
│
├── 2. 合并L1节点 (node_ids)
│   ├── primary_topic.node_ids.extend(secondary_topic.node_ids)
│   └── 去重处理
│
├── 3. 合并L3引用 (l3_refs)
│   ├── primary_topic.l3_refs.extend(secondary_topic.l3_refs)
│   └── 去重处理
│
├── 4. 合并L4引用 (l4_refs)
│   ├── primary_topic.l4_refs.extend(secondary_topic.l4_refs)
│   └── 去重处理
│
├── 5. 更新L1关联
│   ├── 遍历从副主题合并过来的L1节点
│   ├── EngramSlot::deserialize(&mmap[l1_offset..])
│   ├── 更新L1的关联边（如果有指向副L2的边，改为指向主L2）
│   └── EngramSlot::serialize() -> 写回mmap
│
├── 6. 更新主L2摘要
│   ├── 方法1: 使用LLM生成新摘要（如果有LLM配置）
│   │   └── llm.summarize(combined_text)
│   └── 方法2: 使用关键词提取（降级策略）
│       └── 提取所有L1的keywords，合并去重
│
├── 7. 更新主L2时间戳
│   ├── primary_topic.updated_at = current_timestamp()
│   └── 更新dialogue_range（扩展时间范围）
│
├── 8. 序列化并写回主L2
│   └── TopicSlot::serialize() -> 写回mmap
│
├── 9. 更新稀疏索引
│   ├── 更新主L2的索引条目
│   └── 删除副L2的索引条目
│
├── 10. 删除副L2主题
│   ├── 遍历 secondary_ids
│   ├── btree.remove(secondary_hash)
│   └── 释放页面到空闲列表（可选）
│
├── 11. 更新B树索引
│   └── btree.serialize() -> 写入页面
│
└── 12. 返回合并后的L2TopicDetail
```

### 合并策略说明

**L1节点处理**：

- 所有副L2关联的L1节点将转移到主L2
- L1节点的`edge_ptrs`中指向副L2的边需要更新为主L2

**摘要生成策略**：

- 如果有LLM配置：调用`llm.summarize()`生成新的综合摘要
- 如果没有LLM：简单合并所有L1的keywords作为新摘要

**去重逻辑**：

- `node_ids`、`l3_refs`、`l4_refs`在合并时需要去重
- 使用HashSet进行去重处理

**时间范围更新**：

- `dialogue_range`扩展为包含所有合并主题的时间范围

---

## 外部接口19：导入记忆

### 外部接口签名

```rust
pub fn import_memory(request: ImportRequest) -> Result<ImportResult>
```

### 内部调用链

```
import_memory(request)
├── 1. 解析目标层级
│   └── match request.target_layer {
│       L0 => import_l0_profile(...)
│       L2 => import_l2_topics(...)
│       L3 => import_l3_knowledge(...)
│   }
│
├── 2. L0画像导入 (import_l0_profile)
│   ├── 读取现有L0 ProfileSlot
│   │   ├── btree.search(hash_id("profile")) -> page_ref
│   │   └── ProfileSlot::deserialize(&mmap[offset..])
│   │
│   ├── 根据ImportMode处理
│   │   ├── Merge: 合并更新（仅更新非None字段）
│   │   ├── Overwrite: 强制覆盖所有字段
│   │   └── Skip: 如果存在则跳过
│   │
│   ├── 更新时间戳
│   │   └── profile.updated_at = current_timestamp()
│   │
│   └── 序列化并写回
│       └── ProfileSlot::serialize() -> 写回mmap
│
├── 3. L2主题导入 (import_l2_topics)
│   ├── 遍历 request.data.L2Topics
│   │   ├── 计算id_hash = hash_id(title)
│   │   ├── btree.search(id_hash) -> Option<page_ref>
│   │   │
│   │   ├── 根据ImportMode处理
│   │   │   ├── Merge:
│   │   │   │   ├── 如果存在：更新TopicSlot
│   │   │   │   └── 如果不存在：创建新TopicSlot
│   │   │   ├── Overwrite:
│   │   │   │   └── 强制创建/覆盖TopicSlot
│   │   │   └── Skip:
│   │   │       └── 如果存在则跳过，返回skipped_count++
│   │   │
│   │   ├── 关联L3知识域
│   │   │   ├── 查找l3_domain对应的KnowledgeSlot
│   │   │   ├── 如果存在：添加l3_refs引用
│   │   │   └── 如果不存在：可选创建新L3
│   │   │
│   │   ├── 分配页面
│   │   │   └── allocate_from_free_list(mmap, header)
│   │   │
│   │   ├── 序列化并写入
│   │   │   └── TopicSlot::serialize() -> 写入新页面
│   │   │
│   │   ├── 更新B树索引
│   │   │   └── btree.insert(id_hash, page_ref)
│   │   │
│   │   └── 更新稀疏索引
│   │       ├── terms = SparseIndex::tokenize(title)
│   │       └── sparse_index.add_document(id_hash, terms, title.len())
│   │
│   └── 收集created_ids/updated_ids
│
├── 4. L3知识域导入 (import_l3_knowledge)
│   ├── 遍历 request.data.L3Knowledge
│   │   ├── 计算id_hash = hash_id(title + domain)
│   │   ├── btree.search(id_hash) -> Option<page_ref>
│   │   │
│   │   ├── 根据ImportMode处理
│   │   │   ├── Merge:
│   │   │   │   ├── 如果存在：更新KnowledgeSlot
│   │   │   │   └── 如果不存在：创建新KnowledgeSlot
│   │   │   ├── Overwrite:
│   │   │   │   └── 强制创建/覆盖KnowledgeSlot
│   │   │   └── Skip:
│   │   │       └── 如果存在则跳过
│   │   │
│   │   ├── 创建KnowledgeSlot
│   │   │   ├── KnowledgeSlot {
│   │   │   │     id_hash,
│   │   │   │     title: item.title,
│   │   │   │     domain: item.domain,
│   │   │   │     knowledge_type: parse_type(item.knowledge_type),
│   │   │   │     text: item.text,
│   │   │   │     summary: item.summary,
│   │   │   │     keywords: item.keywords,
│   │   │   │     source_ref: item.source_ref,
│   │   │   │     ...
│   │   │   │   }
│   │   │
│   │   ├── 分配页面
│   │   │   └── allocate_from_free_list(mmap, header)
│   │   │
│   │   ├── 序列化并写入
│   │   │   └── KnowledgeSlot::serialize() -> 写入新页面
│   │   │
│   │   ├── 更新B树索引
│   │   │   └── btree.insert(id_hash, page_ref)
│   │   │
│   │   └── 更新稀疏索引
│   │       ├── terms = SparseIndex::tokenize(title + " " + text)
│   │       └── sparse_index.add_document(id_hash, terms, doc_len)
│   │
│   └── 收集created_ids/updated_ids
│
├── 5. 汇总结果
│   └── ImportResult {
│       status: if errors.is_empty() { Success } else { PartialSuccess },
│       created_ids,
│       updated_ids,
│       skipped_count,
│       errors,
│   }
│
└── 6. 返回ImportResult
```

### 导入模式说明

**Merge模式**：

- 如果目标已存在，更新非空字段
- 如果目标不存在，创建新条目
- 适用于增量导入场景

**Overwrite模式**：

- 强制覆盖已有数据
- 如果不存在则创建
- 适用于全量导入场景

**Skip模式**：

- 如果目标已存在，跳过不处理
- 如果不存在，创建新条目
- 适用于首次导入场景

### L2与L3关联处理

导入L2主题时，如果指定了`l3_title`：

1. 查找对应的L3 KnowledgeSlot
2. 如果找到：添加`l3_refs`引用
3. 如果未找到：
   - 可选：自动创建新的L3知识域
   - 或：返回警告，不建立关联

---

## 外部接口17：关闭数据库

### 外部接口签名

```rust
pub fn close(self) -> Result<()>
```

### 内部调用链

```
close(self)
├── 1. checkpoint()
│   ├── 保存BTree索引到磁盘
│   ├── 保存SparseIndex到磁盘
│   └── header.commit_id += 1
│
├── 2. 清空Journal
│   ├── header.journal_start = 0
│   └── header.journal_len = 0
│
├── 3. 写入文件头 (A/B双头 crash safety)
│   ├── 写入B（备份）→ flush
│   └── 写入A（主）→ flush
│
└── 4. 标记closed=true (防止Drop重复checkpoint)
```

---

## 各层级存储内容

| 层级 | 存储内容                     | 数据结构                    |
| ---- | ---------------------------- | --------------------------- |
| L0   | Agent标识、角色、人格        | ProfileSlot                 |
| L1   | 图节点（指向L2 ContextSlot） | ContextNode + HyperedgeSlot |
| L2   | 场景上下文、摘要、激活状态   | ContextSlot                 |
| L3   | 通用超图（节点+边）          | HypergraphSlot/Node/Edge    |
| L4   | 对话原文、文件引用           | ArchiveSlot                 |
| L5   | 动作序列、触发条件           | ActionChainSlot             |

---

## 激活机制实现

L2 上下文的激活通过 `session/mod.rs` 的 SessionManager 管理。

### 激活流程

1. **L2主题激活**
   - 更新 `activation_score`
   - 设置 `is_active = true`
   - 将L2主题ID加入 `session_manager`

2. **L1图节点**
   - `search_memory()` 中通过 `update_activation_scores()` 更新

### 相关模块

- `session/mod.rs` - SessionManager
- `query/search.rs` - update_activation_scores()

---

## Dream管道详细步骤

详细步骤已在 [外部接口4：Dream整合](#外部接口4dream整合) 中描述。当前管道包含5个阶段：

| 阶段 | 模块                         | 功能                                     | 需要LLM |
| ---- | ---------------------------- | ---------------------------------------- | ------- |
| 1    | `compress_stage`             | L2深度降级 (depth-1→2→3→移除)            | 是      |
| 2    | `mod.rs::rebuild_l1_from_l2` | L1重建 (删除指向已移除L2的节点)          | 否      |
| 3    | `l0_form_stage`              | L0画像更新 (从主题关键词生成personality) | 否      |
| 4    | `l3_distill_stage`           | L3超图知识蒸馏 (LLM提取概念+关系)        | **是**  |
| 5    | `crystallize_stage`          | L5结晶化 + 低质量修剪                    | 是      |

各阶段详细信息请参考对应模块的源代码注释。

---

## L0-L5 数据结构

### L0 - Profile（Agent画像）

```rust
pub struct ProfileSlot {
    pub id_hash: u64,
    pub name: String,
    pub role: String,
    pub personality: String,
    pub worldview: String,
    pub preferences: HashMap<String, String>,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}
```

### L1 - ContextNode（图节点）

```rust
pub struct ContextNode {
    pub id_hash: u64,
    pub context_id: u64,        // 指向L2 ContextSlot
    pub vector_page_ref: u64,   // 向量页面引用
    pub importance: f32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub edge_ptrs: Vec<u64>,   // 关联超边
}
```

### L1 - Hyperedge（超边）

```rust
pub struct HyperedgeSlot {
    pub id_hash: u64,
    pub kind: HyperedgeKind,    // Association/Temporal/Hierarchical/CoOccurrence
    pub node_ptrs: Vec<u64>,    // 连接的节点列表
    pub weight: f32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}
```

### L2 - ContextSlot（场景上下文）

```rust
pub struct ContextSlot {
    pub id_hash: u64,
    pub parent_id: Option<u64>,
    pub depth: u8,              // 1=场景, 2=子场景, 3=轮次组
    pub title: String,
    pub summary: Option<String>,
    pub archive_refs: Vec<u64>, // 关联的L4归档
    pub l3_refs: Vec<u64>,      // 关联的L3超图
    pub turn_count: u32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub importance: f32,
    pub activation_score: f32,
    pub is_active: bool,
    pub activation_state: ActivationState,
    pub centroid_page_ref: u64, // 质心向量页面
    pub dialogue_range: (i64, i64),
}
```

### L3 - HypergraphSlot（超图容器）

```rust
pub struct HypergraphSlot {
    pub id_hash: u64,
    pub name: String,
    pub source: HypergraphSource,  // Manual/Path/Url
    pub node_count: u32,
    pub edge_count: u32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}
```

### L3 - HypergraphNode（超图节点）

```rust
pub struct HypergraphNode {
    pub id_hash: u64,
    pub graph_id: u64,
    pub title: String,
    pub node_type: String,
    pub content: String,
    pub keywords: Vec<String>,
    pub source_ref: Option<String>,
    pub importance: f32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}
```

### L3 - HypergraphEdge（超图边）

```rust
pub struct HypergraphEdge {
    pub id_hash: u64,
    pub graph_id: u64,
    pub kind: GraphEdgeKind,
    pub node_ids: Vec<u64>,
    pub weight: f32,
    pub label: Option<String>,
    pub created_at: i64,
}
```

### L4 - ArchiveSlot（原文归档）

```rust
pub struct ArchiveSlot {
    pub id_hash: u64,
    pub content_type: ContentType,  // Text/Image/Document
    pub role: u8,                   // 0=user, 1=assistant
    pub context_id: u64,
    pub created_at: i64,
    pub content: String,
    pub metadata: Option<String>,
}
```

### L5 - ActionChainSlot（动作链）

```rust
pub struct ActionChainSlot {
    pub id_hash: u64,
    pub title: String,
    pub trigger: String,
    pub status: ChainStatus,        // Active/Deprecated/Draft
    pub confidence: f32,
    pub success_rate: f32,
    pub trigger_count: u32,
    pub last_triggered: i64,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}
```

---

## 性能关键路径

### 1. 向量相似度计算

- 使用SIMD指令（AVX2/NEON）
- 批量处理多个向量
- 缓存友好的内存布局

### 2. BM25检索

- 倒排索引预计算
- 文档长度归一化
- 批量评分

### 3. 页面分配

- 空闲列表缓存
- 批量分配减少锁竞争

### 4. 内存映射

- 零拷贝读取
- 延迟写入（mmap flush）
- 双头备份提高容错性
