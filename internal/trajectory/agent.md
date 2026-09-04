# internal/trajectory — 轨迹/结晶小方法

- **职责**：`ReadTurn`（经域 `Traj` 索引读一轮事件，坏记录跳过）、
  `TrimByBudget`（预算内保最新，至少留一条；升级路径见函数注
  释）、`MaxEventPayload`（单事件载荷上限，裸事件与计划事件共用）/
  `MaxCrystallizePayload`；结晶写步 `ApplyCandidate`（create 走完整
  校验，reuse/merge 按名/ReuseID 定位）与私有 `applyCrystallized`/
  `findTarget`。
- **契约**：大方法（AppendTrajectory/ReadTrajectory/Crystallize）在根里
  持域锁后调用本包；事件的 `topic_id`/`SessionID` 语义由大方法强制。
- **陷阱**：`findTarget` 只有真正的"无此记录"才返回 found=false；瞬时读
  失败上抛（否则重建已有能力卡、丢使用计数）；畸形 ReuseID 忽略不致命。

<!-- 2026-09-04 接口去 fallback 与按层闭环修复 -->
- `ValidateEvent` 集中了 L6 写入的事件契约（EventType/Timestamp 必填、payload 不得超 `MaxEventPayload`）。**超预算是拒绝而不是截断**——被剪短的事件读回来与完整事件无法区分。
- `ReadTurn` 返回 `([]slot, error)`：索引点名而引擎读不动的记录是错误，不是少几条的转录（对齐仓内「瞬时错误一律上报」）。
