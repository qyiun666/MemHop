# internal/domain — 域状态容器

- **职责**：`Context`（原 agentContext）= 单个 agent 域的业务状态：`Mu` 域锁、
  `L2Meta`/`Arch`/`Traj`/`Plans` 缓存、`DreamInFlight`、`OpCtx`/`OpCancel`、
  `LastActiveAt`/`Deleted`，以及构造时注入的 `Engine`/`LLM`/`Defaults`。
  另有 `PlanCache`（无自带锁，靠 `Context.Mu` 串行；键是**开出该计划的那一轮**的
  话题 ID，一个聚合存在当且仅当该键下还有节点）。面只有
  `Aggregate`/`UpsertNode`/`UpsertEvent`/`RemovePlanIDs` 四个：树的作废不走缓存删除
  通道（换轮次键即换树，旧树由 Dream 保留窗经 `RemovePlanIDs` 回收）。另有
  L2Meta 缓存维护（`SyncL2Meta`/`RemoveTopicsFromIndices`/`RetargetL2Meta`）。
- **纪律**：所有字段只在持有 `Mu` 时读写（组合根在大方法入口拿锁）。
  本包不拿引擎以外的资源，不做业务编排——编排是小方法包与根的事。
- **陷阱**：写记录帧后必须紧跟 `SyncL2Meta`（存储 → 缓存序）；
  `NewContext` 是重建点（空闲回收后的域从这里复活，缓存全部从记录重建）。
  `Arch` 不是加速器而是**唯一的寻址手段**：归档 id 哈希了自己的文本，从话题 id
  推不出地址，所以「这个话题说过什么」只能问它。它也只在 `NewContext` 重建，
  运行期不自愈——任何删归档的路径都必须等磁盘删成功后再摘镜像
  （`repo.DropArchivesL4` / `repo.DeleteTopicArchives` 已内置这一步序），漏一处
  就让该话题之后每次读都撞「索引点名已不存在的记录」而硬错。
