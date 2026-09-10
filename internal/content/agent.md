# internal/content — 话题内容与键的小方法

- **职责**：`ParseTopicID`（话题键的解析 + 拒零，读写两侧共用一个入口）、
  `ValidateEvent`（事件写入契约）、`AppendEvent`（把一次事件写进该话题的内容轨，
  分配 Seq 并盖上它归属的步骤路径）、`ReadEvents`（经域 `L4` 索引读一个话题的
  事件，按 Seq 升序）、`TrimByBudget`（预算内保最新，至少留一条；升级路径见
  函数注释）、`MaxEventPayload`（单事件载荷上限，裸事件与计划事件共用）/
  `MaxCrystallizePayload`（结晶读侧的事件预算）。
- **为什么叫 content 而不叫 trajectory**：一轮的对话原文与操作事件自 0x000E 起
  同住 L4，只差一个 `Kind`；计划节点挂在同一个话题键上但住在 L6。本包服务的
  是「内容与它的键」，不是七层里的某一层。
- **契约**：一个话题的键就是 Search 为那一轮铸出的话题 ID；该轮的事件与它开出
  的计划树同住这个键下。大方法（AppendTrajectory/ReadTrajectory/PlanCommit/
  PlanState/Crystallize）在根里持域锁后调用本包。Crystallize 只做纯提炼
  （ReadEvents → TrimByBudget → llmops.Crystallize），候选列表原样返回宿主：
  L5 记录层已退役（目录即能力），落盘/去重/激活全归宿主，本包没有结晶写步。
- `AppendEvent` 是事件追加的唯一实现，`AppendTrajectory` 的裸事件分支与
  `PlanCommit` 的步进事件都走它——事件写路径只有一处，宿主伪造的 `Kind`/`Seq`/
  `ContextID`/`Role`/`ContentType` 一律不被采信，`Kind` 恒为 event、
  `ContentType` 恒为 text。
- **Seq 是一个话题内跨 Kind 共享的单一空间**：`seq = max(话题现有 Seq, 2) + 1`。
  下限 2 不是保守，是正确性——宿主在一轮里先 append 事件、事后才 `Update`
  沉淀，而沉淀固定写 `Seq=1/2`；没有这个下限，第一条事件会被用户原文原地覆掉
  （`TestSettledTurnKeepsEventsAppendedBeforeIt` 钉住这条）。
- `ValidateEvent` 的 payload 超预算是**拒绝而不是截断**——被剪短的事件读回来与
  完整事件无法区分。
- `ReadEvents` 返回 `([]slot, error)`：索引点名而引擎读不动的记录是 `ErrIO`，
  不是少几条的转录（对齐仓内「瞬时错误一律上报」）。
