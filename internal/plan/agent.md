# internal/plan — L6 计划树小方法

- **职责**：`PlanStatus` 字符串面与 `StatusToU8`/`StatusToString`/
  `IsTerminalStatus`；`ParsePlanID`（全零保留哨兵）/`SplitNodePath`；
  写步 `EnsureNode`（沿路径建 pending 节点链，`NodePath` 点号分隔）/
  `AppendEventLocked`（planEventTypes 词表 + 强制裸事件语义，清零含
  `PlanType` 在内的全部节点字段）/`UpdateNodeLocked`/
  `UpdateNodeSummaryLocked`；树构建 `BuildTree`/`Forest`/`ToNodeView`/
  `CountForest`/`Summarize`；`RollupTree`（只回填空 Summary，永不改
  Status）；整树同步 `SyncNodeLocked`（未填即继承）/`CollectPaths`/
  `ParentPath`。
- **契约**：所有 `*Locked` 都要求调用方持 `ac.Mu`；缓存同步经
  `ac.Plans`（无自带锁）。`PlanStatus`/`PlanTree`/`PlanNode`/
  `PlanNodeView`/`PlanSummary` 的唯一定义处，根经 `models.go` 恒等别名。
- **陷阱**：节点更新不得触碰事件 TrajIndex（节点不占事件 Seq 空间，
  否则深浅提交互相覆盖）；`RollupTree` 在非 done 父节点上什么都不做
  （Model A：父节点完成只由宿主显式提交）。

<!-- 2026-09-04 接口去 fallback 与按层闭环修复 -->
- `ValidateEvent(ev)` 是 plan 事件的唯一校验点，`AppendEventLocked` 与 `PlanCommit` 共用它——**校验必须发生在 `EnsureNode`/`UpdateNodeLocked` 之前**，否则被拒的 commit 会留下已推进的节点状态（实测过这个 bug）。
- `AppendEventLocked` 现在收 `nodePath` 并由**库**把它盖到事件记录上：`PlanNodeRef` 是 `HashPlanNode` 的产物、公开面没有任何途径反查，事件不带上路径就无法归位到某一步。宿主传入的 NodePath 依旧不被采信（api 入参里已无此字段）。
