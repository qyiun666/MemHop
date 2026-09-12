# internal/graph — L3 知识图小方法

## 职责

- 批量导入：`ImportBatch`（`NewImportBatch` 一次建好三张索引：本域图槽 name→id、
  每图的节点标题集、每图的边键；三者任一条记录读不回就整批拒绝，且拒绝发生在任何写入
  之前），方法
  `ImportNode`（图槽建/复用 + 按 Skip/Merge/Overwrite 处理同名节点）、
  `ImportRelations`（Related 解析建超边，未解析项记进 `result.Errors` 不中断批次）、
  `GraphIDs()`（本批把 domain 解析到的图）、`StampChanged()`（批末给「本批真写过
  内容」的每张图各推进一次槽的 `UpdatedAt`，一次批量导入一个图只写一次）。
- 查询步：`NodeFilter.Matches`、`ResolveSubgraphStart`、`SubgraphAdjacency`、
  `BfsWithinDepth`、`AllNodesVisited`。
- 命名闸：`CheckName`——拒掉改到本域另一张图已占用的标签。
- 本包不实现字段合并，只按 mode 选一个注入进来的策略函数。单条记录的读写全经 `repo`；
  **整池**的枚举走 `core` 的严格 typed 扫描——`repo` 那两份列举是按图过滤后的宽容版，
  批次要的恰恰是「一条都不能漏」。

## 契约

- 一个批次的 mode、result 与状态都收在 `ImportBatch` 里：查表缓存三张
  （domain→图、图→标题集、图→边键）加两份图集（解析到的 / 写过内容的）；调用方
  持域锁创建它，整批跑完后调一次 `StampChanged`，再读回 result。
- `ImportNode` 只返回 error：一条 item 落在哪张图、建没建成都进 `result`，不打断整批。
- `ResolveSubgraphStart` 对起点不属于该图返回 `ErrInvalidQuery` 而非 `ErrNotFound`
  ——图是查询的范围，不是被查的对象。起点记录本身读不回（`ErrNotFound` 以外的任何错误）
  原样上报：「没有这个节点」让宿主换起点，「这个节点坏了」让它知道这张图坏在这里。

## 陷阱

- 边身份是「排序后的成员 + kind」（`repo.EdgeKeyL3`），不是节点对哈希：同一对节点可以
  并存多种关系，重复导入按这个键去重。
- 一条关系 = 一条 `{source} ∪ Titles` 超边，元数不限。成员集非法（空/自指/重复/不在
  本图/词表外 kind）由 `relationMembers` 拒绝并给出原因，**不建边，也不降级成两两边**。
  「哪种边种类算已定义」不在本包判断，也不在两处判断：`core.GraphEdgeKind.Valid` 是
  那一份词表，写入端与子图遍历的过滤器都问它——写侧不收的值在读侧滤不出东西，
  两处各写一套就会一处收紧一处放宽。
- 边对批内每个条目都声明，含被 skip 的：边按成员集去重所以幂等——只在节点新落库时
  建边，会丢掉这一批声明到已存在节点上的那些关系。
- 同名标签的两半各管一头，都不可省：`CheckName` 让撞名状态从写入端不可达，
  `preferGraphID` 让读取端对已存在的同名槽路由确定（id 由该名字派生的那张图拥有它，
  全等平手取较小 id）。`NewImportBatch` 播种是 map 迭代顺序，两张同名槽不加裁决就会让
  同一个 domain 每次导入随机进一张——节点 id 是 `hash(graphID:title)`，换图即换节点 id，
  重导幂等性随之失效。
- 三张索引都走严格那份（`core.CollectAllGraphSlotsStrict` / `CollectAllStrict[Node]` /
  `[Edge]`）：读不回的记录**仍然占着它的标签、它的标题、它的边**。跳过槽就是把「有人占着」
  答成「没人占」，导入那侧会铸出第二张同名图、两半节点集从此各自独立，改名那侧会把名字改到
  别人占着的标签上；跳过节点则是让 Merge 导入答「这个标题没有」，于是按位置式 id 原地覆写
  那条正读不回的记录，报告里还算成新增了一条。
- `graphFor` 用 `repo.EnsureGraphL3` 而不是 `CreateGraphL3`：已存在的槽原样复用，它的
  `Name` 可能已被改过，派生出的 id 不该把那个标签写回去。ensure 只收名字——图槽存的是标签
  与两把时钟，本包不向它声明任何来源。
