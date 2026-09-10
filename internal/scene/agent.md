# internal/scene — L2 场景小方法

- **职责**：场景读面（`ResolveForRead`/`Create`/`FreshID`/`OpenTurn`/
  `SurfaceTopics`）、场景上下文渲染（`ContextTopic`）、删除步
  （`PruneParentChild`/`DeleteTopics`/`DetachGraph`）。
- **契约**：大方法（Search/SceneContext/DeleteTopic/DeleteScene/
  MergeScenes）在根里持域锁后调用本包；`FreshID` 只有 `ErrNotFound` 才算
  ID 可用；`OpenTurn` 是读路径唯一的写，失败必须使整次读取失败。
- **陷阱**：消息顺序只看 `Seq`。`ac.L4.IDs(topicID, KindUtterance)` 给的就是
  Seq 升序，而沉淀把用户说的钉在 1、回复钉在 2，所以「谁先说话」由写入侧构造
  保证，读侧不需要时间戳、更不需要拿 `Role` 打平——事件的 `Role` 未设即 0，
  正是 `RoleUser`，混进来读就会把一次工具调用显示成用户发言。
  两种「少内容」判据不同，不得合并处理：索引点名却读不到 = 镜像与磁盘不一致，
  是硬 `ErrIO`；话题在而内容为空、或 `Seq` 有空洞 = 被 7 天保留窗回收的合法
  终局，不报错，靠 `SceneMessage.Seq` 让空洞可判别。
- `ResolveForRead` 在 `SceneID` 非空时**拒绝任何 `L3ID`**（报 ErrInvalidQuery）：锚点是创建期字段，改锚点走 `UpdateScene`。
- `DetachGraph` 是本包对 `DeleteL3` 的那半边级联（由 `internal/l3.go` 在图记录删成后调用）：`scene.L3ID` 是 L3 图唯一的入边，而锚点两条写路径都要求图存在，所以删图必须清掉命名它的锚点——否则 `ListScenes(deletedGID)` 仍列出该场景，宿主列出一个不存在的项目域，且 `Search`/`SceneContext` 都不报错。只清命中的场景，同域其它图的锚点不动。
- 锚点校验（`Create`/`ResolveForRead` 里的 `ReadGraphSlot`）读的是文件级公共 L3 域（`core.SharedPoolAgentID`），不是调用方自己的域：图记录全部住在公共池。`DetachGraph` 本身仍按调用方 agentID 清各自域里的场景锚点。
