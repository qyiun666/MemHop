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
├── 7. 激活管理器 (activation/mod.rs)
│   └── ActivationManager::new(ActivationConfig::default())
│
├── 8. 会话管理器 (session/mod.rs)
│   └── SessionManager::new()
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

#### 2.7 结果融合 (query/fusion.rs)

```rust
pub fn fuse_scores(
    bm25_scores: &HashMap<u64, f32>,
    vector_scores: &HashMap<u64, f32>,
    bm25_weight: f32,
    vector_weight: f32,
) -> Vec<(u64, f32)>

pub fn apply_time_decay(score: f32, hours_since_update: f32) -> f32
pub fn apply_emotion_boost(score: f32, emotion_type: u8, valence: f32) -> f32
```

#### 2.8 激活管理 (activation/mod.rs)

```rust
pub struct ActivationManager {
    config: ActivationConfig,
}

impl ActivationManager {
    pub fn new(config: ActivationConfig) -> Self
    pub fn calculate_score(&self, importance: f32, hours_since_last_access: f32) -> f32
    pub fn apply_recall_bonus(&self, score: f32) -> f32
    pub fn should_transition(&self, score: f32, importance: f32) -> MemoryState
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
pub fn dream(&mut self, llm: LlmConfig, config: DreamConfig) -> Result<DreamReport>
```

### 内部调用链

```
dream(config)
├── 阶段1: L5结晶 (dream/crystallize_stage.rs)
│   ├── 扫描所有L5 CrystalSlot
│   ├── 按动作类型分组
│   ├── 相同类型合并为Skill
│   └── 更新CrystalSlot
│
├── 阶段2: L2压缩 (dream/compress_stage.rs)
│   ├── 获取激活状态的TopicSlot列表
│   ├── 判断是否需要压缩（相似度>阈值）
│   ├── 合并相似主题
│   └── 更新node_ids和summary
│
├── 阶段3: L1更新 (dream/merge_stage.rs)
│   ├── 通过L2获取关联的L1 Engram
│   ├── 计算重要性分数
│   ├── 调整importance值
│   └── 更新EngramSlot
│
├── 阶段4: L0更新 (dream/l0_form_stage.rs)
│   ├── 通过L1分析用户偏好
│   ├── 提取关键特征
│   └── 更新L0 Profile
│
├── 辅助阶段
│   ├── decay_stage: 衰减低激活记忆
│   ├── temporal_stage: 创建时间边
│   ├── cooccurrence_stage: 创建共现边
│   └── reflect_stage: 主题反思
│
└── 返回 DreamReport
```

### 内部接口详情

#### 4.1 Dream配置 (dream/prune.rs)

```rust
pub struct DreamConfig {
    pub compress_l2: bool,         // 是否压缩L2
    pub distill_l3: bool,          // 是否蒸馏L3
    pub crystallize_l5: bool,      // 是否结晶L5
    pub prune_threshold: f32,      // 修剪阈值
    pub time_window: (i64, i64),   // 时间窗口
    pub deactivate_ids: Vec<String>, // 指定要停用的主题ID（可选）
}

pub struct DreamReport {
    /// L5结晶结果
    pub l5_crystallized: Vec<CrystallizeResult>,
    
    /// L2压缩结果
    pub l2_compressed: Vec<CompressResult>,
    
    /// L1更新结果
    pub l1_updated: Vec<UpdateResult>,
    
    /// L0更新结果
    pub l0_updated: Option<L0UpdateResult>,
    
    /// 修剪的文档
    pub pruned: Vec<String>,
    
    /// 执行统计
    pub stats: DreamStats,
}

pub struct CrystallizeResult {
    pub skill_id: String,
    pub skill_title: String,
    pub merged_crystal_ids: Vec<String>,
    pub action_count: usize,
}

pub struct CompressResult {
    pub merged_topic_id: String,
    pub absorbed_topic_ids: Vec<String>,
    pub new_summary: String,
}

pub struct UpdateResult {
    pub engram_id: String,
    pub old_state: String,
    pub new_state: String,
    pub reason: String,
}

pub struct L0UpdateResult {
    pub profile_id: String,
    pub updated_fields: Vec<String>,
    pub new_personality: String,
}

pub struct DreamStats {
    pub duration_ms: u64,
    pub l5_processed: usize,
    pub l2_processed: usize,
    pub l1_processed: usize,
    pub l0_updated: bool,
}
```

#### 4.2 LLM接口 (dream/llm.rs)

```rust
pub trait LlmProvider {
    fn summarize(&self, text: &str) -> Result<String>;
    fn extract_patterns(&self, texts: &[String]) -> Result<Vec<Pattern>>;
    fn generate_crystal(&self, steps: &[String]) -> Result<CrystalDef>;
}

pub struct Pattern {
    pub name: String,
    pub description: String,
    pub frequency: usize,
}

pub struct CrystalDef {
    pub condition: String,
    pub action: String,
    pub raw_steps: String,
}
```

#### 4.3 各阶段详情

**阶段1: L5结晶 (dream/crystallize_stage.rs)**
```rust
pub fn crystallize_memories(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &BTreeIndex,
    llm: &dyn LlmProvider,
) -> Result<Vec<String>>
```

**阶段2: L2压缩 (dream/compress_stage.rs)**
```rust
pub fn compress_l1_to_l2(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &SparseIndex,
    llm: &dyn LlmProvider,
    time_window: (i64, i64),
) -> Result<Vec<String>>
```

**阶段3: L1更新 (dream/merge_stage.rs)**
```rust
pub fn merge_similar_topics(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    threshold: f32,
) -> Result<Vec<(String, String)>>
```

**阶段4: L0更新 (dream/l0_form_stage.rs)**
```rust
pub fn form_l0_profile(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &BTreeIndex,
    llm: &dyn LlmProvider,
) -> Result<String>
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
┌─────────────────────────────────────────────────────────────┐
│                     外部接口 (lib.rs)                        │
│  open / search_memory / update_memory / dream / close       │
│  update_l0_profile / update_l2_title / update_l3_title      │
│  update_l5_title                                            │
└─────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        ▼                     ▼                     ▼
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│  查询层       │     │  梦境层       │     │  激活层       │
│  query/       │     │  dream/       │     │  activation/  │
├───────────────┤     ├───────────────┤     ├───────────────┤
│ store.rs      │     │ mod.rs        │     │ mod.rs        │
│ recall.rs     │     │ compress.rs   │     │ decay.rs      │
│ recall_more.rs│     │ crystallize.rs│     └───────────────┘
│ cascade.rs    │     │ merge.rs      │
│ fusion.rs     │     │ reflect.rs    │
│ batch.rs      │     │ temporal.rs   │
│ crystal_match │     │ l0_form.rs    │
└───────────────┘     └───────────────┘
        │                     │
        └─────────────────────┘
                │
        ┌───────┴───────┐
        ▼               ▼
┌───────────────┐ ┌───────────────┐
│  索引层       │ │  槽位层       │
│  index/       │ │  slot/        │
├───────────────┤ ├───────────────┤
│ btree.rs      │ │ engram.rs     │
│ sparse.rs     │ │ topic.rs      │
│ vector.rs     │ │ knowledge.rs  │
└───────────────┘ │ crystal.rs    │
        │         │ archive.rs    │
        │         │ hyperedge.rs  │
        │         └───────────────┘
        │               │
        └───────────────┘
                │
        ┌───────┴───────┐
        ▼               ▼
┌───────────────┐ ┌───────────────┐
│  文件层       │ │  工具层       │
│  file/        │ │  util/        │
├───────────────┤ ├───────────────┤
│ free_list.rs  │ │ f16.rs        │
│ header.rs     │ │ hash.rs       │
│ journal.rs    │ │ io_helpers.rs │
│ page.rs       │ │ mod.rs        │
└───────────────┘ └───────────────┘
```

### 关键数据流

#### 存储流程
```
StoreDoc
  ↓
store_document()
  ├── hash_id() → id_hash
  ├── allocate_from_free_list() → page_id
  ├── EngramSlot::serialize() → 写入页面
  ├── write_vector() → 写入向量页
  ├── btree.insert(id_hash, page_ref)
  └── sparse_index.add_document()
  ↓
StoreResult { id, page_ref }
```

#### 检索流程
```
RecallQuery
  ↓
recall_documents()
  ├── SparseIndex::search() → BM25结果
  ├── cosine_similarity() → 向量结果
  ├── fuse_scores() → 融合结果
  ├── EngramSlot::deserialize() → 读取记忆
  └── apply_time_decay() → 时间衰减
  ↓
Vec<RecallResult>
```

#### Dream流程
```
DreamConfig
  ↓
dream_pipeline()
  ├── Stage 1: decay_stage → 降级低激活记忆
  ├── Stage 2: temporal_stage → 创建时间边
  ├── Stage 3: merge_stage → 合并相似主题
  ├── Stage 4: reflect_stage → 主题反思
  ├── Stage 5: cooccurrence_stage → 创建共现边
  ├── Stage 6: compress_stage → L1→L2压缩
  ├── Stage 7: distill_stage → L1→L3蒸馏
  └── Stage 8: l0_form_stage → L0形成
  ↓
DreamReport
```

---

## 页面布局

### 页面类型

| 页面类型 | 描述 | 结构 |
|---------|------|------|
| Header | 文件头 | 32字节头 + 数据 |
| FreeList | 空闲列表 | 32字节头 + 空闲页面ID列表 |
| Engram | L1情节记忆 | 32字节头 + EngramSlot |
| Topic | L2语义主题 | 32字节头 + TopicSlot |
| Knowledge | L3知识域 | 32字节头 + KnowledgeSlot |
| Archive | L4归档 | 32字节头 + ArchiveSlot |
| Crystal | L5结晶 | 32字节头 + CrystalSlot |
| Hyperedge | 超边 | 32字节头 + HyperedgeSlot |
| Vector | 向量页 | 32字节头 + 向量数据 |
| BTree | B树节点 | 32字节头 + 树节点 |
| SparseIndex | 稀疏索引 | 32字节头 + 倒排表 |

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
├── 1. 同步索引到磁盘
│   ├── btree.serialize() -> 写入页面
│   └── sparse_index.serialize() -> 写入页面
│
├── 2. 写入文件头
│   ├── header.commit_id += 1
│   ├── header.to_bytes() -> 写入A/B双头
│   └── header.crc32 = crc32(header_bytes)
│
├── 3. 刷新内存映射
│   └── mmap.flush()
│
├── 4. 关闭文件
│   └── file.sync_all()
│
└── 5. 释放资源
    ├── drop(encoder)
    ├── drop(session_manager)
    └── drop(activation_manager)
```

---

## 各层级存储内容

| 层级 | 存储内容 | 数据结构 |
|------|----------|----------|
| L1 | 对话摘要、关键词、向量 | EngramSlot |
| L2 | 主题标题、摘要、关联L1/L3/L4 | TopicSlot |
| L3 | 知识域标题、文本、类型 | KnowledgeSlot |
| L4 | 对话原文、时间戳 | ArchiveSlot |
| L5 | 动作链、标题、类型 | CrystalSlot |

---

## 激活机制实现

### 激活流程

1. **L2主题激活**
   - 更新 `activation_score`
   - 设置 `is_active = true`
   - 将L2主题ID加入 `session_manager`

2. **L1情节记忆**
   - 更新 `memory_state` 为 `Active`
   - 增加 `importance`

3. **L3知识域**
   - 更新 `confidence` 和 `importance`

### 相关模块

- `activation/mod.rs` - ActivationManager
- `session/mod.rs` - SessionManager

---

## Dream管道详细步骤

### 第一步：更新L5（动作链结晶）

**目标**：将相同类型的动作链合并为可复用的Skill（技能）

**流程**：
1. 扫描所有L5 Crystal节点
2. 按动作类型（Create、Read、Update、Delete、Execute、Query）分组
3. 合并相同类型的动作，生成Skill模板
4. 创建新的Crystal节点存储Skill

**示例**：
```rust
// 输入：多个L5动作链
ActionChain: [
    { title: "创建文件", type: Create },
    { title: "编写代码", type: Create },
    { title: "运行测试", type: Execute },
]

// 输出：合并后的Skill
Skill: {
    title: "代码开发流程",
    steps: [
        "1. 创建文件",
        "2. 编写代码",
        "3. 运行测试",
    ],
    action_type: Create,
}
```

### 第二步：更新L2（主题压缩）

**目标**：压缩激活状态的L2主题，合并相似主题

**流程**：
1. 获取所有激活状态的L2主题（`is_active = true`）
2. 计算主题间的相似度（基于centroid_vector）
3. 相似度超过阈值（如0.8）的主题合并
4. 更新L2的关联关系（node_ids、l3_refs、l4_refs）

**判断是否需要压缩的条件**：
- 主题相似度 >= 0.8
- 主题激活时间相近（如24小时内）
- 主题属于同一L3域

### 第三步：更新L1（情节记忆调整）

**目标**：通过L2判断是否需要更新L1

**流程**：
1. 通过L2获取关联的L1列表（node_ids）
2. 分析L1的重要性（importance）和激活状态
3. 重要性低的L1降级为Dormant或删除
4. 重要性高的L1保持或提升状态

**判断条件**：
- `importance < 0.3` → 降级为Dormant
- `memory_state == Dormant` 且 `updated_at` 超过30天 → 删除
- `importance >= 0.8` → 保持Active状态

### 第四步：更新L0（画像更新）

**目标**：通过L1分析行为模式，更新L0画像

**流程**：
1. 通过L1分析用户的行为模式
2. 提取性格特征、偏好、世界观
3. 更新L0 Profile节点

**更新内容**：
- 性格特征（personality）
- 偏好设置（preferences）
- 世界观（worldview）
- 角色定位（role）

---

## 六层架构数据结构

### L0 - Profile（画像）

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
}
```

### L1 - Engram（情节记忆）

```rust
pub struct EngramSlot {
    pub id_hash: u64,
    pub text: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub vector_page_ref: u64,
    pub memory_state: u8,  // Active/Latent/Dormant
    pub importance: f32,
    pub edge_ptrs: [u64; 8],
    // ... 其他字段
}
```

### L2 - Topic（语义主题）

```rust
pub struct TopicSlot {
    pub id_hash: u64,
    pub title: String,
    pub summary: Option<String>,
    pub node_ids: Vec<u64>,      // 关联L1列表
    pub l3_refs: Vec<u64>,       // 关联L3列表
    pub l4_refs: Vec<u64>,       // 关联L4列表
    pub parent_id: Option<u64>,
    pub activation_score: f32,
    pub is_active: bool,
    pub centroid_vector: Option<Vec<f16>>,
    // ... 其他字段
}
```

### L3 - Knowledge（知识域）

```rust
pub struct KnowledgeSlot {
    pub id_hash: u64,
    pub title: String,
    pub domain: String,
    pub knowledge_type: KnowledgeType,
    pub text: String,
    pub summary: Option<String>,
    pub keywords: Vec<String>,
    pub edge_ptrs: [u64; 8],
    pub archive_refs: Vec<u64>,
    pub importance: f32,
    pub confidence: f32,
    // ... 其他字段
}
```

### L4 - Archive（原文归档）

```rust
pub struct ArchiveSlot {
    pub id_hash: u64,
    pub topic_id: u64,
    pub content: String,
    pub timestamp: i64,
    pub file_path: Option<String>,
    pub file_type: Option<String>,
}
```

### L5 - Crystal（结晶化知识）

```rust
pub struct CrystalSlot {
    pub id_hash: u64,
    pub title: String,
    pub action_chain: Vec<ActionItem>,
    pub skill_template: Option<String>,
    pub trigger_count: u32,
    pub success_rate: f32,
    pub created_at: i64,
    pub updated_at: i64,
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
