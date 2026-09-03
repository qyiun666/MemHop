# internal/scene — L2 场景小方法

- **职责**：场景读面（`ResolveForRead`/`Create`/`FreshID`/`OpenTurn`/
  `SurfaceTopics`）、场景上下文渲染（`ContextTopic`，内含同毫秒
  Role 决序的 `sortMessages`）、删除步（`PruneParentChild`/`DeleteTopics`）。
- **契约**：大方法（Search/SceneContext/DeleteTopic/DeleteScene/
  MergeScenes）在根里持域锁后调用本包；`FreshID` 只有 `ErrNotFound` 才算
  ID 可用；`OpenTurn` 是读路径唯一的写，失败必须使整次读取失败。
- **陷阱**：`ContextTopic` 的消息顺序靠时间戳 + 同毫秒 RoleUser 优先，
  不能依赖 L4Refs 的落盘顺序。
