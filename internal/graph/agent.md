# internal/graph — L3 知识图小方法

- **职责**：批量导入步（`ImportOneNode`：图槽建/复用 + 按 Skip/Merge/
  Overwrite 处理同名节点；`ImportRelations`：Related 二趟解析建超边，
  未解析项记 result.Errors 不中断批次）；查询步（`QueryNodesByIDs`、
  `NodeMatchesKeyword`、`ResolveSubgraphStart`、`SubgraphAdjacency`、
  `BfsWithinDepth`、`AllNodesVisited`）。
- **契约**：字段合并策略不在本包——在 `cap/knowledge`
  （MergeFields/OverwriteFields）；记录读写经 `repo` L3 层。
- **陷阱**：边 ID 哈希排序后的节点对，重复导入同批次必须幂等；
  `ResolveSubgraphStart` 对起点不属于该图返回 `ErrInvalidQuery` 而非
  `ErrNotFound`。
