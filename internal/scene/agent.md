# internal/scene — L2 场景小方法

- **职责**：场景读面（`ResolveForRead`/`Create`/`FreshID`/`OpenTurn`/
  `SurfaceTopics`）、场景上下文渲染（`ContextTopic`，内含同毫秒
  Role 决序的 `sortMessages`）、删除步（`PruneParentChild`/`DeleteTopics`/
  `DetachGraph`）。
- **契约**：大方法（Search/SceneContext/DeleteTopic/DeleteScene/
  MergeScenes）在根里持域锁后调用本包；`FreshID` 只有 `ErrNotFound` 才算
  ID 可用；`OpenTurn` 是读路径唯一的写，失败必须使整次读取失败。
- **陷阱**：`ContextTopic` 的消息顺序靠时间戳 + 同毫秒 RoleUser 优先，
  不能依赖 L4Refs 的落盘顺序。

<!-- 2026-09-04 接口去 fallback 与按层闭环修复 -->
- `ResolveForRead` 在 `SceneID` 非空时**拒绝任何 `L3ID`**（报 ErrInvalidQuery）：锚点是创建期字段，过去这里把传进来的锚点整个丢掉，宿主以为挂了项目域实际没有。改锚点走 `UpdateScene`。
- `DetachGraph` 是本包对 `DeleteL3` 的那半边级联（由 `internal/l3.go` 在图记录删成后调用）：`scene.L3ID` 是 L3 图唯一的入边，而锚点两条写路径都要求图存在，所以删图必须清掉命名它的锚点——否则 `ListScenes(deletedGID)` 仍列出该场景，宿主列出一个不存在的项目域，且 `Search`/`SceneContext` 都不报错。只清命中的场景，同域其它图的锚点不动。
