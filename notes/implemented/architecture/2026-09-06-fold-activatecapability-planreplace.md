# 决策档案: 折叠 ActivateCapability 与 PlanReplace（公开面 34 → 32）

Status: implemented

## Problem

公开面里有两处被证实的能力重复：`ActivateCapability` 的 repo 实现（`repo/l5layer.go`）是「读卡 → 置 active → 写回」三步，而 `UpdateCapability` 已应用 `patch.Status` 且对内置卡同样拒绝——同一状态变更存在两个入口；`PlanReplace` 的清树体（DeletePlanRecords + 两个索引摘除）与 `SyncPlanTree` 的「消失节点删除」语义重合，播种根节点又等价于单节点同步。v1.6.0 已把公开面审到 34 + 8，重复能力与「同一功能只能有一个实现」相悖。

## Decision

- **`ActivateCapability` 删除**（api/internal/repo 三层 + `repo.ActivateCapabilityL5` 整函数）：激活走 `UpdateCapability(id, CapabilityPatch{Status: &core.CapabilityActive})`。并入后同过卡片校验（ValidateCard 要求 trigger/summary 至少一、条目 ≥1）——旧 Activate 不校验，行为差异是显式收紧而非回归。MCP `memhop_capability_activate` 工具删除，激活走 `memhop_capability_update` 的 `status` 参数（工具数 31 → 30）。
- **`PlanReplace` 删除**（api/internal 两层）：`SyncPlanTree(planID, nil)` = 清整树（`repo.DeletePlanRecords` + `Traj.RemoveSession` + `Plans.RemovePlan`，保留 planID，事件 Seq 归 1）；播种下一个任务 = 单节点同步（空 status 落 pending）。`ParsePlanID` 的零 planID 拒绝保持在 nil 判断之前，`0000000000000000` 保留语义不变。`plan.Replace` 语义并入后 `SyncPlanTree` 的非 nil 空 NodePath 仍拒绝。PlanReplace 本就不在 MCP 工具面，工具数不受影响。
- 公开面清单同步 `api/surface_public_test.go`（Session 34 → 32）；版本表新增 v1.7.0 条目。

## Alternatives considered

- **保留窄形态方法作意图明确的捷径**：ActivateCapability 的语义确实更窄（不会误改其它字段），但它是同一存储行为第二入口，宿主可从签名差异里读出根本不存在的语义差——删除优于共存。落选。
- **PlanReplace 保留、SyncPlanTree 不动**：清树是重规划的刚需（新任务不得按路径串台），但它的实现体与 SyncPlanTree 完全共享，留两个入口等于留两份文档要同步。落选。
- **SyncPlanTree 加显式 wipe 参数而非 nil**：多一个入参位表达「空树」，但 nil 根在语义上就是「没有树」，无需新参数。落选。

## Consequences

- 换到：公开面 34 → 32，激活与清树各只剩一条实现路径；MCP 工具 30。
- 代价：下游 meowagent 有两个生产调用点（`facade.go:219` ActivateCapability、`toolbox/plan.go:64` PlanReplace），打 tag 后在宿主侧独立轮次适配（`{"reset":true}` 工具语义改为 nil 同步 + 单节点播种）。go.work 取证（本仓只取证不改）：meowagent `go build ./...` 仍先停在 v1.6.0 的旧断点 `toolbox/l5.go:445`（`cap.Type undefined`），本轮两个断点排在它之后，须宿主按版本顺序一并适配。
- 升级路径：纯 API 面变更，无磁盘格式变化，老文件直接打开。
