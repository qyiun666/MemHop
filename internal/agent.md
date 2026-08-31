# internal — 业务层契约（模块级 agent 上下文）

本包是 MemHop 的**业务层**。任何 AI agent 或开发者修改本层前必须先读完本
文件，修改后必须同步更新本文件。

## 唯一职责

编排 Search/Update/Dream/L0-L6 业务管线与多 agent 生命周期
（`agentContext`、`CreateAgent`/`ListAgents`/`DeleteAgent`），并以
`Session`（session.go）向 `api` 门面暴露按域绑定的公开操作面。

## agentContext 域级锁纪律

1. **先域锁后存储**：所有公开方法统一走 `db.lockAgent(agentID)`（内部：
   `contextFor` 取域 + `ac.mu.Lock()` + 锁内复检 `deleted` 墓碑），再进入
   存储读写；引擎自带的锁在内层，顺序不可颠倒。同 agent 串行、跨 agent
   并行。`contextFor` 对非默认域校验注册表：未注册/已删除的 agentID 直接
   `ErrAgentNotFound`，域永不复活；与删除对撞的陈旧句柄由锁内墓碑复检拒绝。
   L6 会话族统一走 `db.lockSession(agentID, sessionID)`（lockAgent + hex
   解析，解析失败先解锁），门面侧的会话准入策略在 `CheckSession`。
2. **索引锁序**：同一操作内更新索引遵循 **存储 -> l2meta -> sparse**
   （先落记录帧，再 `ac.syncL2Meta`，最后更新稀疏索引）。
   **禁止在域锁内取 `db.agentsMu`**（锁序环：sweep 走 agentsMu -> ac.mu），
   域内簿记（如 `lastDreamAt`）直接写 atomic 字段。
3. **Dream 域化**：`RunDream` 全程持本域锁；后台触发经
   `triggerSceneDream(ac, sceneID)`（调用方持 `ac.mu`），goroutine 运行在
   `ac.dreamCtx` 下——`Close`/`DeleteAgent` 取消它，任何在飞 Dream 在下一
   阶段边界退出，绝不写入已销毁的域。
4. **空闲回收**：无后台定时器；`contextFor` 顺带清扫超
   `Defaults.AgentIdleTTLMs` 未访问的域（默认域豁免），回收前先对域锁
   `TryLock`：锁被占用（在飞操作）或 `dreamInFlight` 非空则跳过，留待下轮。
   回收时快照 sparse blob 进缓存，数据仍在文件，下次访问透明重建。
5. **DeleteAgent 顺序**：先摘租户映射（断绝新 `contextFor`）→
   `destroyContext`（取消 dreamCtx）→ `ac.deleted` 墓碑（`lockAgent` 拿锁后
   复检，与删除对撞的在飞操作被拒）→ `ac.mu` 屏障等待在飞操作 → 引擎域删除。
6. **planCache 域内索引**：L6 计划聚合缓存 `ac.plans`（`plancache.go`）
   **不内置锁**，完全依赖 `ac.mu` 串行（与 `activeScenes` 同模式，区别于自带
   RWMutex 的 `TrajIndex`）。所有计划写路径（节点增删改、事件绑定、
   `PlanReplace`、`SyncPlanTree`、Dream 清理）必须先取 `ac.mu` 再同步缓存；
   `newAgentContextLocked` 构建，idle 重建时一并重建。`SyncPlanTree` 整树同步
   只改节点结构/字段，**不产生 `plan_step` 事件**、不动事件 Seq 空间。
7. **planID 全零保留**：`AppendTrajectory` 写入的裸轮次事件恒为
   `PlanID=0`，故 `0000000000000000` 不是合法计划。五个计划入口
   （`PlanAppend`/`PlanCommit`/`PlanState`/`PlanReplace`/`SyncPlanTree`）
   一律经 `parsePlanID` 拒绝；绕过它直接调 `repo.DeletePlanRecords(0)`
   会删掉整个域的全部轮次事件。
8. **计划清理有界**：`l6_prune` 只豁免「持非 done 节点 **且** 窗口内仍有
   活动」的计划；宿主中断或放弃而静默超 `trajectoryRetention` 的计划照常
   清理并级联其绑定事件，否则废弃计划会让 L6 无界增长。

## 数据访问纪律

- 只经 `internal/repo`（及 `repo/core` 导出的 Slot 读写）访问数据；
  **禁止**直接操作帧、文件头、快照结构。
- `StorageEngine` 句柄由装配层 `config.go` 的 `Open(cfg, enc, builtins)`
  唯一持有并注入 `DB.engine`；业务代码不得自行打开/关闭引擎。内建能力
  工具箱由 `api` 门面以 `fs.FS` 注入（api 传 `capabilities.FS`），
  `Open` 负责解析并 attach——internal 不得 import `capabilities`。
- **能力下沉**：算法与策略一律在 `internal/cap/<feature>` 能力包
  （scenefind 三通道+RRF 评分、engram L1 建边与遗忘衰减、llmops 四类
  LLM 提示契约与解析、capability 能力卡解析校验合并、profile 画像摘要
  与蒸馏生成、knowledge L3 匹配与节点合并策略）；本包保留"取数 → 调
  能力 → 落库"的编排，不做算法。LLM 传输策略（截断升级重试）在本包
  `llm_ops.go` 的 `Provider.ChatWithRetry`，prompt 构建属于 llmops。
- 新增能力时：先建 `cap/<feature>` 包（依赖注入 + 窄接口，禁止 import
  internal 根），再由本层组装；共享 DTO 下沉 `repo/core/model_dto.go`
  / `model_distill.go`，本包以恒等别名引用（`models.go`）。
- **会话面（session.go）**：`Session` 是绑定单个 agent 域的唯一对外操作
  入口（含 ActiveSceneIDs 的外部 hex 渲染）；`api.Session` 只内嵌本类型做
  纯转发，api 侧禁止出现业务逻辑、格式化或域绑定代码。多 agent 是唯一
  模式：`NewSession(agentID)` 是唯一会话构造器，`DefaultSession` 已删除。
  `exports.go` 是给 api 门面的恒等再导出接缝（Slot 别名、枚举常量、
  Code/CodeOf、FormatAgentID/ParseAgentID）；`api` 包禁止直接 import
  `repo/core` 或 `common`。
- LLM/encoder 为 DB 级共享（无租户状态），可在域锁内调用，但不得持存储
  事务等待网络返回。

## 单向依赖

`internal -> internal/cap -> internal/repo -> repo/{core,index}`，
外加 `common`；能力包之间互不 import（需要交互时回到组装根）。
禁止依赖 `api`、`cmd`、`capabilities`。`api` 门面（`MultiAgentDB`/
`Session`）是本层唯一对外出口。

## 修改者义务

改动锁纪律、Dream 阶段划分或域生命周期时，必须同步更新本文件与
`internal/repo/agent.md` 中受影响的条目。
