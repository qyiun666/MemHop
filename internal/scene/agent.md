# internal/scene — L2 场景记录的小方法

## 职责

- 读面解析：`ResolveForRead`（定位本次读取所属的场景，没有则新建）、
  `Create`（铸一个未占用的 id、落记录、按需锚定）、`FreshID`（铸 id）、
  `OpenTurn`（把场景的轮次计数器推到下一轮）。
- 读面渲染：`SurfaceTopics`（一个场景的 depth-1 话题，轮次序）、
  `ContextTopic`（一个话题的关键词轨、子话题数与它名下的原文，`Seq` 升序）。
- 删除步：`PruneParentChild`（从存活父话题的 `ChildrenIDs` 摘掉被删话题，刷新父
  记录与缓存）、`DeleteTopics`（话题 + 它名下的内容 + 它开出的计划树 + 各份缓存）、
  `DetachGraph`（清掉命名某张 L3 图的锚点）。

## 契约

- 全部入口在调用方持有 `ac.Mu` 时被调用。
- `FreshID` 只在 `ErrNotFound` 时认作 id 可用：IO / 关闭 / 损坏必须原样上抛，
  否则会铸出一个与活场景相撞的 id。
- `OpenTurn` 是本包唯一的写，它的失败必须让调用方整次读取失败——轮次号是铸话题
  id 的依据，不能吞。
- `ResolveForRead` 在 `SceneID` 非空时拒绝任何 `L3ID`：锚点是创建期字段，改锚点只
  走 `UpdateScene`；丢掉一个锚点不得让它看起来已被采纳。
- 锚点校验一律经 `repo.ReadSharedGraphL3`，本包不自己解析图 id 再读图槽；命名的图
  必须能解析。

## 陷阱

- 消息顺序只看 `Seq`：`ac.L4.IDs` 给的就是 `Seq` 升序，本包不按时间戳、也不按
  `Role` 打平——事件的 `Role` 未设即 0，正是 `RoleUser`，混进对话读法会把一次工具
  调用显示成用户发言。
- 两种「少内容」判据不同，不得合并：索引点名而引擎读不动的记录是硬 `ErrIO`
  （镜像与磁盘不一致）；话题在而内容为空、或 `Seq` 有空洞，是被回收过的合法终局，
  不报错。
- `ContextTopic` 是本包唯一带出话题 `Name` 与整场原文的渲染口（`SurfaceTopics` 只
  列 depth-1），被下沉的子话题的名字只能从这里出现。
- `DetachGraph` 按域全扫场景：锚点只长在场景上，图槽没有反向清单。
- `FreshID` 跳过 0（id 面的哨兵值）并在占用时重铸；`Create` 的名字一律库生成
  `session:<id>`。
