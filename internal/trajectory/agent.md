# internal/trajectory — L6 键与轨迹小方法

- **职责**：`ParseTopicID`（L6 唯一键的解析 + 拒零，读写两侧共用一个入口）、
  `ReadTurn`（经域 `Traj` 索引读一键下的事件，坏记录跳过）、
  `TrimByBudget`（预算内保最新，至少留一条；升级路径见函数注
  释）、`MaxEventPayload`（单事件载荷上限，裸事件与计划事件共用）/
  `MaxCrystallizePayload`（结晶读侧的轨迹预算）。
- **契约**：L6 只有一个键——本轮话题 ID，该轮的事件与它开出的计划节点同住；
  大方法（AppendTrajectory/ReadTrajectory/PlanCommit/PlanState/Crystallize）在根里
  持域锁后调用本包，记录的 `SessionID` 由大方法强制等于该键。
  Crystallize 只做纯提炼（ReadTurn → TrimByBudget → llmops.Crystallize），
  候选列表原样返回宿主：L5 记录层已退役（目录即能力），落盘/去重/激活
  全归宿主，本包没有结晶写步。

- `ValidateEvent` 集中了 L6 写入的事件契约（EventType/Timestamp 必填、payload 不得超 `MaxEventPayload`）。**超预算是拒绝而不是截断**——被剪短的事件读回来与完整事件无法区分。
- `ReadTurn` 返回 `([]slot, error)`：索引点名而引擎读不动的记录是错误，不是少几条的转录（对齐仓内「瞬时错误一律上报」）。
