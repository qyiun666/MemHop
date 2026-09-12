# internal/domain — 域状态容器

## 职责

- `Context`：单个 agent 域的状态——`Mu` 域锁、`L2Meta`/`L4`/`Plans` 三份缓存、
  `DreamInFlight`、`OpCtx`/`OpCancel`、`LastActiveAt`、`Reclaimed`，以及构造时注入的
  `Engine`/`LLM`/`Defaults`。
- `NewContext`：把一个域的全部缓存从记录重建出来——空闲回收后的域从这里复活。
- `PlanCache`：按话题键聚合的计划树镜像，面只有
  `Aggregate`/`HasSeq`/`Subtree`/`NextSeq`/`UpsertNode`/`RemoveNodes`/`RemoveTopic`。
- L2Meta 缓存维护：`SyncL2Meta`/`RemoveTopicsFromIndices`/`RetargetL2Meta`。
- 本包不拿引擎以外的资源，也不做编排：锁由调用方持有，编排在调用方。

## 契约

- 除 `LastActiveAt` 与 `Reclaimed` 两份原子量外，每个字段只在持有 `Mu` 时被读写；
  `PlanCache` 不内置锁，靠同一条串行。两份原子量刻意能在锁外读——回收的判定与打标
  正发生在调用方还没拿到 `Mu` 的时候。
- 写记录帧后必须紧跟 `SyncL2Meta`（存储 → 缓存序），交出去的就是刚写出去那条 slot：
  镜像不回读，所以它没有自己的失败分支。
- `HasSeq` 只回答「树上有没有这一步」，拒不拒写由问它的人决定，缓存不因此知道任何
  别的东西；`Subtree` 给调用方当过滤集合，`NextSeq` 给调用方当发号器。

## 陷阱

- `L4` 不是加速器而是**唯一的枚举手段**：单条内容的地址能由 (话题, `Seq`) 派生，但
  「这个话题一共有哪几条内容」只有这里有。它也只在 `NewContext` 重建，运行期不自愈
  ——本包不替谁补写它漏掉的条目。
- 反向不成立：过期清扫先问索引要 id（`ExpiredBefore` 只读不改），删盘失败时镜像仍
  完整，下一次清扫还会看到它们。
- `RemoveTopicsFromIndices` 摘 L2Meta 与 `Plans`，**不摘 L4**。在这里重复摘一次不会
  出错，但会把「谁负责哪份镜像」搅浑。反过来的漏配是真 bug：删话题不摘 `Plans` 会留
  下一条陈旧的 `LastActiveAt`，而豁免判定正读它——一棵死树能凭此长期豁免清扫。
- `Plans` 的一个键存在当且仅当该键下还有节点；`Subtree` 对未知的根只返回它自己。
  序号没有前缀形状可匹配，树的形状只有这里知道。
- 三份缓存都从**读得回来的那部分记录**重建：一条读不回的记录在镜像里缺席，与它被保留
  窗裁掉是同一个形状。`L2Meta` 与 `L4` 的这份缺席只影响读，留得住；`Plans` 的缺席还会
  决定写向哪里——序号是从幸存节点算出来的，而记录 id 由 (键, 序号) 派生，缺席的那条仍占着
  自己那个地址，所以这个形状由 `plan.CreateNode` 在落记录前读一次地址兜住。
