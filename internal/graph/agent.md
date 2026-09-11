# internal/graph — L3 知识图小方法

## 职责

- 批量导入：`ImportBatch`（`NewImportBatch` 预载本域已有图槽 name→id），方法
  `ImportNode`（图槽建/复用 + 按 Skip/Merge/Overwrite 处理同名节点）、
  `ImportRelations`（Related 解析建超边，未解析项记进 `result.Errors` 不中断批次）、
  `GraphIDs()`（本批写过的图）。
- 查询步：`NodeFilter.Matches`、`ResolveSubgraphStart`、`SubgraphAdjacency`、
  `BfsWithinDepth`、`AllNodesVisited`。
- 命名闸：`CheckName`——拒掉改到本域另一张图已占用的标签。
- 本包不实现字段合并，只按 mode 选一个注入进来的策略函数；记录读写全经 `repo`。

## 契约

- 一个批次的 mode、result 与三张缓存（domain→图、图→标题集、图→边键）都收在
  `ImportBatch` 里；调用方持域锁创建它，结束后读回 result。
- `ImportNode` 只返回 error：一条 item 落在哪张图、建没建成都进 `result`，不打断整批。
- `ResolveSubgraphStart` 对起点不属于该图返回 `ErrInvalidQuery` 而非 `ErrNotFound`
  ——图是查询的范围，不是被查的对象。

## 陷阱

- 边身份是「排序后的成员 + kind」（`repo.EdgeKeyL3`），不是节点对哈希：同一对节点可以
  并存多种关系，重复导入按这个键去重。
- 一条关系 = 一条 `{source} ∪ Titles` 超边，元数不限。成员集非法（空/自指/重复/不在
  本图/词表外 kind）由 `relationMembers` 拒绝并给出原因，**不建边，也不降级成两两边**。
- 边对批内每个条目都声明，含被 skip 的：边按成员集去重所以幂等——只在节点新落库时
  建边，会丢掉这一批声明到已存在节点上的那些关系。
- 同名标签的两半各管一头，都不可省：`CheckName` 让撞名状态从写入端不可达，
  `preferGraphID` 让读取端对已存在的同名槽路由确定（id 由该名字派生的那张图拥有它，
  全等平手取较小 id）。`NewImportBatch` 播种是 map 迭代顺序，两张同名槽不加裁决就会让
  同一个 domain 每次导入随机进一张——节点 id 是 `hash(graphID:title)`，换图即换节点 id，
  重导幂等性随之失效。
- `graphFor` 用 `repo.EnsureGraphL3` 而不是 `CreateGraphL3`：已存在的槽原样复用，它的
  `Name` 可能已被改过，派生出的 id 不该把那个标签写回去。
