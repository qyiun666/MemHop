# internal — 业务层契约（模块级 agent 上下文）

本包是 MemHop 的**业务层**。任何 AI agent 或开发者修改本层前必须先读完本
文件，修改后必须同步更新本文件。

## 唯一职责

编排 Search/Update/Dream/L0-L6 业务管线与多 agent 生命周期
（`agentContext`、`CreateAgent`/`ListAgents`/`DeleteAgent`）。

## agentContext 域级锁纪律

1. **先域锁后存储**：所有公开方法统一走 `db.lockAgent(agentID)`（内部：
   `contextFor` 取域 + `ac.mu.Lock()` + 锁内复检 `deleted` 墓碑），再进入
   存储读写；引擎自带的锁在内层，顺序不可颠倒。同 agent 串行、跨 agent
   并行。`contextFor` 对非默认域校验注册表：未注册/已删除的 agentID 直接
   `ErrAgentNotFound`，域永不复活；与删除对撞的陈旧句柄由锁内墓碑复检拒绝。
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

## 数据访问纪律

- 只经 `internal/repo`（及 `repo/core` 导出的 Slot 读写）访问数据；
  **禁止**直接操作帧、文件头、快照结构。
- `StorageEngine` 句柄由装配层 `config.go` 的 `Open` 唯一持有并注入
  `DB.engine`；业务代码不得自行打开/关闭引擎。
- LLM/encoder 为 DB 级共享（无租户状态），可在域锁内调用，但不得持存储
  事务等待网络返回。

## 单向依赖

`internal -> internal/repo -> repo/{core,index}`，外加 `common`；
禁止依赖 `api`、`cmd`、`capabilities`。`api` 门面（含 `AgentSession`）
是本层唯一对外出口。

## 修改者义务

改动锁纪律、Dream 阶段划分或域生命周期时，必须同步更新本文件与
`internal/repo/agent.md` 中受影响的条目。
