# internal/graph — L3 知识图小方法

- **职责**：批量导入 = 一个 `ImportBatch`（`NewImportBatch` 预载本域已有图槽
  name→id，方法 `ImportNode`（图槽建/复用 + 按 Skip/Merge/Overwrite 处理同名
  节点）、`ImportRelations`（Related 二趟解析建超边，未解析项记 result.Errors
  不中断批次）、`GraphIDs()`（本批写过的图，供宿主直接把图挂到场景））；
  查询步（`NodeFilter.Matches`、`ResolveSubgraphStart`、`SubgraphAdjacency`、
  `BfsWithinDepth`、`AllNodesVisited`）。
- **契约**：字段合并策略不在本包——在 `cap/knowledge`
  （MergeFields/OverwriteFields）；记录读写经 `repo` L3 层。批次的 mode、
  result 与三张缓存（domain→图、图→标题集、图→边键）都收在 `ImportBatch` 里，
  调用方（组合根）只负责拿域锁。
- **陷阱**：边身份是「排序后的成员 + kind」`repo.EdgeKeyL3`，不是节点对哈希——
  同一对节点可以并存多种关系；重复导入按这个键去重，因此对旧文件里
  pair-only 哈希写下的边同样幂等（换 id 公式不会让它重复建）。
  `ResolveSubgraphStart` 对起点不属于该图返回 `ErrInvalidQuery` 而非
  `ErrNotFound`。

<!-- 2026-09-04 接口去 fallback 与按层闭环修复 -->
- `ImportBatch.ImportNode` 只返回 error（它的 bool 曾只服务于「skip 的条目不建边」这条门槛）。**边现在对批内每个条目都声明**，含被 skip 的：边按「排序成员 + kind」去重，所以幂等；反过来，只在节点新落库时建边会让「删节点 → Skip 重导」永久丢掉该节点的入边（实测 16 条只剩 1 条）。
- `L3Relation.Titles` 是关系的另一侧全部目标：一条关系 = 一条 `{source} ∪ Titles` 超边，元数不限。成员集非法（空/自指/重复/不在本图/词表外 kind）由 `relationMembers` 拒绝并给出原因，`ImportRelations` 记进 `result.Errors` 后继续其余关系——**不建边，不降级成两两边**。
- `graphFor` 走 `repo.EnsureGraphL3` 而非 `CreateGraphL3`：图 id = `hash(Domain)`，但槽里的 `Name` 是宿主标签（可能被 `UpdateL3` 改过），已存在就原样复用，否则一次重导就把改名悄悄撤销。
- `NewImportBatch` 播种 name→id 时**必须裁决同名**：`graphIDs[g.Name] = g.IDHash` 逐条覆盖 + `IndexByType` 是 map 迭代顺序，两张同名槽就让同一个 `Domain` 每次导入随机进一张（节点 id = `hash(graphID:title)`，于是同标题换图、重导幂等性失效）。规则在 `preferGraphID`：id 由该名字派生的那张图拥有它，全等平手取较小 id——旧文件里已经存在的撞名也因此读取确定。
- `CheckName` 是写入端同一件事的另一半，由 `internal/l3.go` 的 `UpdateL3` 在改名前调用：拒掉「改到别的图已占用的标签」，撞名状态从源头不可达；`preferGraphID` 只负责已经撞名的旧文件。
