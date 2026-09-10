# internal/content — 话题内容与键的小方法

- **职责**：`ParseTopicID`（话题键的解析 + 拒零，读写两侧共用一个入口）、
  `ValidateAppend`（内容写入契约，两种 Kind 各管各的轴）、`Append`（把一条记录写进
  该话题的内容轨，必要时分配 Seq）、`Read`（经域 `L4` 索引按 Kind 读一个话题的内容，
  Seq 升序）、`RenderForDistill`（把一个话题的原文渲染成提炼要读的转录）、
  `TrimByBudget`（预算内保最新，至少留一条；升级路径见函数注释）、
  `MaxEventPayload`（单事件 4 KiB）/`MaxUtterancePayload`（单条原文 64 KiB）/
  `MaxCrystallizePayload`（结晶读侧的 128 KiB 预算）。
- **为什么叫 content 而不叫 trajectory**：一轮的对话原文与操作事件同住 L4，只差一个
  `Kind`；计划节点挂在同一个话题键上但住在 L5。本包服务的是「内容与它的键」，
  不是六层里的某一层。
- **契约**：一个话题的键就是 Search 为那一轮铸出的话题 ID；该轮的原文、事件与它开出
  的计划树同住这个键下。`Update` 不写内容，所以 `Append` 是一条记录进入话题的
  唯一途径。大方法（AppendArchive/SearchL4/PlanSet/PlanState/Crystallize）在根里
  持域锁后调用本包。Crystallize 只做纯提炼（`Read(event)` → `TrimByBudget` →
  `llmops.Crystallize`），候选列表原样返回宿主：能力面没有记录层（目录即能力），
  落盘/去重/激活全归宿主，本包没有结晶写步。
- **字段归属**：`Kind` 决定采信哪一组。原文侧照收宿主的 `Role`/`ContentType`；
  事件侧 `Kind` 恒为 event、`ContentType` 恒为 text、`Role` 恒为 0——说了发生了什么
  的东西没有说话者，也没有媒介。`IDHash` 与 `TopicID` 两侧都不采信：前者由
  (话题, Seq) 派生，后者就是调用键本身。宿主可伪装的只有被丢弃的字段，
  所以不必为它增设校验分支（`TestAppendEventCannotForgeContentFields` 钉住）。
  反过来，越界的轴是**拒**的：原文带 `EventType` 或 `NodePath`、或自称 `RoleDream`
  （融合摘要的标记，库自己盖），都是 `ErrInvalidQuery`。
- **Seq 是一个话题内跨 Kind 共享的单一空间**：显式 `Seq=0` 才分配，且
  `seq = max(话题现有 Seq, 2) + 1`。下限 2 是给对话预留的位置：无论宿主先记事件
  还是先补原文，自动分配都不会踩到 1/2。写一个已被占用的 Seq 是**覆写**而非报错，
  跨 Kind 也覆写——重放因此收敛（`TestAppendArchiveOverwritesAcrossKinds`）。
- 超预算是**拒绝而不是截断**——被剪短的记录读回来与完整的无法区分；原文的上限
  另有理由：提炼按 2000 rune 分片、每片一次 LLM 调用，无上限的原文就是无上限的
  锁内停留。
- `Read` 返回 `([]slot, error)`：索引点名而引擎读不动的记录是 `ErrIO`，
  不是少几条的转录（对齐仓内「瞬时错误一律上报」）。
- `RenderForDistill` 按传入顺序逐行渲染 `<说话者>: <内容>`，而传入的就是 `Read` 的
  Seq 序——一个话题只有一个序，蒸馏读到的与宿主读回来的必须是同一份。说话者标签
  不是装饰：没有标签，一轮的两侧塌成一坨，提炼就丢了是谁主张的。
