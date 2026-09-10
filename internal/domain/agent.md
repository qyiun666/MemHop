# internal/domain — 域状态容器

- **职责**：`Context`（原 agentContext）= 单个 agent 域的业务状态：`Mu` 域锁、
  `L2Meta`/`L4`/`Plans` 缓存、`DreamInFlight`、`OpCtx`/`OpCancel`、
  `LastActiveAt`/`Deleted`，以及构造时注入的 `Engine`/`LLM`/`Defaults`。
  另有 `PlanCache`（无自带锁，靠 `Context.Mu` 串行；键是**开出该计划的那一轮**的
  话题 ID，一个聚合存在当且仅当该键下还有节点）。面只有
  `Aggregate`/`UpsertNode`/`RemoveNodes`/`RemoveTopic` 四个：树不接收事件——
  事件是 L4 内容，不进这张缓存。另有 L2Meta 缓存维护
  （`SyncL2Meta`/`RemoveTopicsFromIndices`/`RetargetL2Meta`）。
- **纪律**：所有字段只在持有 `Mu` 时读写（组合根在大方法入口拿锁）。
  本包不拿引擎以外的资源，不做业务编排——编排是小方法包与根的事。
- **陷阱**：写记录帧后必须紧跟 `SyncL2Meta`（存储 → 缓存序）；
  `NewContext` 是重建点（空闲回收后的域从这里复活，缓存全部从记录重建）。
  `L4` 不是加速器而是**唯一的枚举手段**：单条内容的地址能由 (话题, Seq) 派生，
  但「这个话题一共有哪几条内容」只有这里有。它也只在 `NewContext` 重建，
  运行期不自愈——任何删内容的路径都必须等磁盘删成功后再摘镜像
  （`repo.DeleteTopicArchives` / `repo.DropExpiredArchives` 已内置这一步序），
  漏一处就让该话题之后每次读都撞「索引点名已不存在的记录」而硬错。
  反向不成立：过期清扫先问索引要 id（`ExpiredBefore` 只读不改），删盘失败时
  镜像仍完整，下一次清扫还会看到它们。
- `RemoveTopicsFromIndices` 摘的是 L2Meta 与 `Plans`，**不摘 L4**：内容记录由
  调用方自己删（`scene.DeleteTopics` 走 `repo.DeleteTopicArchives`），那一步已经
  按「盘成功→再摘镜像」的序处理了内容缓存。在这里重复摘一次不会出错，但会把
  「谁负责哪份镜像」搅浑。反过来的漏配是真 bug：删话题不摘 `Plans` 会留下一条
  陈旧的 `LastActiveAt`，而保留窗的在途豁免正读它——一棵死树能凭此长期豁免清扫。
