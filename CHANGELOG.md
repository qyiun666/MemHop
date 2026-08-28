# Changelog

MemHop 遵循语义化版本。本文件记录每个版本的核心改动；完整历史见
README 的版本表与 git log。

## v1.4.1 — 2026-08-28

**类型契约清理**：api 出参 ID 全量 16 位 hex、L0 画像 v2（字段所有权）、库内零往返。

### 变更（含破坏性）

- **api 面（破坏性）**
  - 出参 DTO 改为真实 struct（`api/types.go` + `api/mapping.go` 显式映射）：`TopicSlot`/`SceneSlot`/`SearchResult`/`Hypergraph*`/`ArchiveSlot`/`Capability`/`TrajectorySlot`/`L3Graph`/`L3Subgraph` 全部 ID 字段出参 16 位 hex 字符串；`SearchResult.NewTopicID`、`AppendL4Message` 返回值、`Session.AgentID()` 同步
  - 新增记录级 ID 工具 `api.FormatID` / `api.ParseID`（宿主不再需要自带 hex 格式化）
  - `ProfileSlot` DTO 删除 `IDHash`（UpdateL0 强制覆盖，宿主无感知）
- **L0 画像 v2（存储格式 0x0009，旧文件 Open 即拒绝）**
  - 字段所有权：Name/Role/Preferences 宿主独占（Dream 永不改写）；Personality 宿主播种、Dream 蒸馏演化（蒸馏契约新增 personality 输出，≤160 字符证据归纳）；`EmotionState`/`MBTI` 为 typed 蒸馏信号，替代字符串编码的 emotion_patterns/mbti 混写
  - 删除死字段 `lexicon`/`style_traits` 与关键词投影阶段（Dream 阶段少一级 `l0_profile`）
- **库内零往返**
  - repo 层 ID 入参 uint64 化：Search/Update 每轮 4~5 次 `FormatHash`→`ParseID` 往返清零
  - 质心哈希改 `HashBytes` 字节直算（每次 Search 省一份向量拷贝）；`FormatHash` 改位操作零 fmt
- **LLM 契约健壮性**
  - distill / consolidate 解析失败各补一次格式约束重试（对齐 keywords 自愈模式）
  - distill `per_node` 限 top-20（首轮 2048 token 预算内）；MBTI `type` 改由四维重导出，不再信任 LLM 输出；清理 prompt 死参数（summary/depth）

- **L3 超图激活与 L4 内容类型落地**
  - `L3ImportItem` 增 `source_ref`（位置引用落节点 `SourceRef`，knowledge 合并契约同步增参：Merge 仅非空刷新、Overwrite 全量替换）与 `related`（同图内按标题建边，两阶段解析支持前向引用；边 ID 哈希排序节点对，重导入幂等不重复建边）；`L3ImportResult` 增 `edges_created`；api 导出 `L3Relation` 类型
  - `AppendL4Message` 增 `contentType` 入参（未定义值拒绝），api 导出 `Content*` 七常量；内容约定：text/document/code 的 Content 存原文，image/audio/video 存路径或 URI（mime/size/sha256 走 Metadata）；`L4Query` 增 `Type` 过滤，MCP `memhop_archive_search` 同步 `content_type` 参数

- **L6 轨迹重构与对外面收敛（破坏性）**
  - 每轮一条轨迹：`SessionID` 改为轮键（search 开轮、update 收轮，宿主每轮派生新 16 位 hex）；`TrajectorySlot` 删 `L4Ref`、增 `TopicID`（结晶按同话题聚合跨轮轨迹，payload 上限 128KB）；event_type 为轮内步骤分类（llm_request/llm_output/tool_call/tool_result/subagent_spawn/subagent_done/context_inject/ask_user/user_reply）
  - 对外面只剩追加与查询：`AppendTrajectory` / `ReadTrajectory` / `ListTrajectorySessions`；删除 `TrajectoryStats` / `DeleteTrajectory` / `PruneTrajectory` 与 MCP `memhop_trajectory_stats` / `memhop_trajectory_delete`（33 → 31 工具）
  - 保留期内置：Dream 新增 `l6_prune` 阶段，自动清理 7 天前的事件（`TrajIndex` 支撑 O(1) Seq 分配、轮枚举与按期清理）；注入层删除 `memhop-trajectory` 卡（7 → 6 张），轨迹记录并入宿主自动循环

## v1.4.0 — 2026-08-26

**多 agent 记忆数据库**：一个 `.meh` 文件承载多个完全隔离的 agent 域。

### 新增

- **存储层（`internal/repo/core`）**
  - 记录帧 18 → 26 字节：`type(1) flags(1) length(4) agent_id(8) id_hash(8) crc32(4)`，CRC 覆盖头+数据
  - 引擎索引两级分域：`agent -> idHash -> offset` 与 `byAgentType`；新增 `IterAgents()`（`iter.Seq[uint64]`）、`DeleteAgentRecords(agentID)`
  - 快照格式 0x02：按 agent 序列化稀疏索引（`AGENT_COUNT [agentID blob]...`）
  - 新增 `RecAgentRegistry (0x10)` 注册记录：`crypto/rand` 8 字节 agentID，data 为 agent 名 JSON；Open 时扫描重建 `name -> agentID` 映射（同名复用、不同名永不碰撞，替代无状态哈希）
- **业务层（`internal`）**
  - `agentContext` 每 agent 业务态：域级锁（同 agent 串行、跨 agent 并行）、独立稀疏索引 / L2Meta / 活跃场景 / Dream 簿记 / `dreamCtx`
  - 空闲域内存回收：`Defaults.AgentIdleTTLMs`（默认 60 分钟），无后台定时器，随访问清扫，数据仍在文件
  - `CreateAgent(name)` / `ListAgents()` / `DeleteAgent(agentID)`（域墓碑 + 取消在飞 Dream → 域锁屏障 → 引擎域删除）；删除后的 agentID 永不复活（`contextFor` 校验注册表）
  - `DreamReport` / `DreamStage`（各阶段状态与耗时、`L0Updated`）与 `DistillL0`（独立触发 L0 蒸馏）；L6 新增 `ListTrajectorySessions` / `PruneTrajectory`
- **api 门面**
  - `OpenMulti(cfg) (*MultiAgentDB, error)`、`OpenMultiWithEncoder`
  - `AgentSession`：方法集对齐单 agent `DB`（Search/Update/Dream/L0–L6 全量）
  - `FormatAgentID` / `ParseAgentID`（16 位 hex）
  - 新错误码 `ErrAgentNotFound` (3002)：agentID 未注册或已删除
  - `Open` 保留且宿主零改动（内部映射默认域 `DefaultAgentID = 0`）
- **MCP（`cmd/memhop-mcp`）**
  - registry 共享单个 `MultiAgentDB`：`/mcp/<tenant-id>` → `CreateAgent(tenant)` 稳定 agentID → `Session`；单文件 `<db-dir>/memhop.meh`
  - `os.Root` 锚定 db 目录（替代 `filepath.Dir` 比较），路径穿越防御升级
  - 新增 `memhop_trajectory_sessions`（域内会话清单，发现可结晶/可清理会话），工具总数 32 → 33
- **内置 L5 能力卡（`capabilities/`）**
  - 工具箱重构：19 → 7 张全英文卡（`memhop-guide` 循环分工总纲 + 卡索引；knowledge/scene/archive/profile/trajectory/capability 六张 LLM 可调用说明书；capability 卡合并 crystallize + import）；宿主自动循环（Search/Update/Dream）不再做卡
  - 分层注入契约：默认只注入一行索引（`id + name + summary + trigger`，≈300–500 token）+ guide，参数详情按需 `GetCapability(id)`（仅收 16 位 hex）
- **边界固化**：新增 `internal/agent.md`（业务层契约：域级锁纪律、锁序 存储→l2meta→sparse、Dream 域化）与 `internal/repo/agent.md`（存储层契约：域化原语、实现不外露、单向依赖）

### 变更

- **层编号收敛**：原 L6 Scene Usage 已在 0x0007 并入 SceneSlot，编号空出；轨迹层从 L7 下沉为 **L6**，认知层收敛为 L0–L6 七层（`RecL6Trajectory` 值 0x0E 不变）。文件改名：`api/l6.go`、`internal/l6.go`、`internal/repo/l6layer.go`、`cmd/memhop-mcp/tools_l6.go`；MCP 工具名不变（语义命名，不含层号）
- **去重与转换层消除**：删除 `topicSlotJSON`（core.TopicSlot 镜像）；`topicToL2Meta` 与 `L2MetaFromTopic` 合并为单一 `L2MetaFromTopic(*core.TopicSlot)`；`ReadTopicSlot` 返回 `*TopicSlot`（去掉单元素切片包装）；`CompressTopicsL2`/`MergeScenesL2` 复用 `core.TopicEntry` 单点序列化
- **Go 标准库现代化（零新依赖）**：`L2MetaIndex.Iter` 返回 `iter.Seq2[uint64, *L2Meta]`；`unique.Make` 驻留稀疏索引词项；`os.Root` 租户路径校验
- 版本常量：`cmd/memhop-mcp` → `v1.4.0`

### 破坏性变更

- **格式不兼容**：`FormatVersion = 0x0008`；`<= 0x0007` 的旧 `.meh` 文件在 Open 时被显式拒绝，**无迁移路径**
- `internal` 层全部读写函数签名加 `agentID` 首参（仅影响直接依赖 `internal` 的代码；`api.Open` 宿主不受影响）
- `api.DB` 上提升自 `internal.DB` 的方法随之新增 `agentID` 参数（门面方法签名不变）；`Lock()` 对已关闭的 DB 会 panic（与旧无条件锁同契约）
- 并发契约变更：同 agent 串行、跨 agent 并行由库内域级锁保证，宿主无需自行排队
- `Session.Dream` 返回值 `bool` → `*DreamReport`；`consolidated` 语义收窄（仅当实际巩固了 ≥1 个场景才为 true，no-op 返回 false）
- 内置能力卡集合重定义：删除全部 `agent-*` 原子卡与 `memhop-search/update/dream/refine/crystallize/capability-import` 卡（后两者并入 `memhop-capability`），卡片内容全部英文化

### 测试

- 新增 `TestAgentDomainIsolation`（两 agent 同文件同 idHash 互不可见）、`TestDeleteAgent`（全域清除 + ListAgents 一致 + 默认域拒删）、`TestAgentRegistryStableAcrossRestart`（名称映射跨重启稳定）
- 回归：默认域下三通道检索、Dream 五阶段、`go test -race` 全绿
