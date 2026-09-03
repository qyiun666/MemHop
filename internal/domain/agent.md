# internal/domain — 域状态容器

- **职责**：`Context`（原 agentContext）= 单个 agent 域的业务状态：`Mu` 域锁、
  `L2Meta`/`Traj`/`Plans` 缓存、`DreamInFlight`、`OpCtx`/`OpCancel`、
  `LastActiveAt`/`Deleted`，以及构造时注入的 `Engine`/`LLM`/`Defaults`。
  另有 `PlanCache`（无自带锁，靠 `Context.Mu` 串行）与 L2Meta 缓存维护
  （`SyncL2Meta`/`RemoveTopicsFromIndices`/`RetargetL2Meta`）。
- **纪律**：所有字段只在持有 `Mu` 时读写（组合根在大方法入口拿锁）。
  本包不拿引擎以外的资源，不做业务编排——编排是小方法包与根的事。
- **陷阱**：写记录帧后必须紧跟 `SyncL2Meta`（存储 → 缓存序）；
  `NewContext` 是重建点（空闲回收后的域从这里复活，缓存全部从记录重建）。
