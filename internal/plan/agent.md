# internal/plan — L6 计划树小方法

- **职责**：`PlanStatus` 字符串面与 `StatusToU8`/`StatusToString`/
  `IsTerminalStatus`；`ParsePlanID`（全零保留哨兵）/`SplitNodePath`；
  写步 `EnsureNode`（沿路径建 pending 节点链）/`AppendEventLocked`
  （planEventTypes 词表 + 强制裸事件语义）/`UpdateNodeLocked`/
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
