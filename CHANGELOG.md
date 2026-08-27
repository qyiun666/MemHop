# Changelog

MemHop 遵循语义化版本。本文件记录每个版本的核心改动；完整历史见
README 的版本表与 git log。

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
