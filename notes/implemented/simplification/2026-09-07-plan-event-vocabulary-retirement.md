# 决策档案: 计划事件词表退役（EventType 归宿主命名）

Status: implemented

## Problem

`AppendTrajectory(key, nodePath, ev)` 的计划绑定分支（`nodePath` 非空）额外要求
`EventType` 落在一份 10 词名单里（`plan_step`、`llm_request`、`llm_output`、
`tool_call`、`tool_result`、`subagent_spawn`、`subagent_done`、`context_inject`、
`ask_user`、`user_reply`），名单外的名字一律 `ErrInvalidQuery`；裸轮次事件（`nodePath`
为空）则接受宿主任意命名。同一批名字在两条路径上一个是法律、一个是惯例。

这份约束不挣自己的饭钱。全仓非测试代码里 `EventType` 的引用只有三处：拒写的那次查表、
`ReadTrajectory` 把记录原样回显、结晶 prompt 里一行 `fmt.Fprintf("[seq=%d type=%s]…")`。
**引擎从不按 `EventType` 分支**——没有按类型过滤的读路径、没有按类型触发的行为、没有按
类型派生的 id。名单唯一的行为就是拒写。

而拒写的代价不对称地全在宿主侧：meowagent 把 L6 轨迹收敛为「一轮一树」单路之后（事件键
恒为轮级 planID），它的沙箱裁决反问事件 `sandbox_ask` 在计划桶被拒，宿主按「轨迹是旁路
观察、不因记录失败中断决策循环」的口径 Warn 丢弃——**一个审计事件因为库从不读的字段静默
消失**。交付面自己也不支持这份严格：MCP 的 `memhop_trajectory_append` 描述写的就是
「event_type 由宿主自定（惯例：…）」，而计划写面根本不在 MCP 工具面上。

## Decision

- **删除 `planEventTypes` 名单与 `plan.ValidateEvent` 包装**（`internal/plan/write.go`）。名单外的 `EventType` 一律接受，原样存回；计划绑定事件与裸轮次事件同口径。
- **校验点回归一处**：`trajectory.ValidateEvent`（非空 `EventType` + `Timestamp > 0` + payload ≤ 4KB）。`AppendTrajectory` 计划分支与 `PlanCommit` 仍在 `EnsureNode` / `UpdateNodeLocked` **之前**调用它，被拒的写零留痕（v1.6.1 修掉的「校验晚于改树」顺序不变量原样保留，`TestPlanCommitRejectedLeavesTreeUntouched` 继续钉）。
- **公开面与格式不变**：`api.Session` 仍 27 + `MultiAgentDB` 8；`FormatVersion` 仍 `0x000C`——`event_type` 是记录内的 JSON 字符串字段，放宽不触及记录布局。MCP 24 工具不变。
- **惯例名留在文档里，降格为「给读者的共享词表」**：`api/session.go` 的 `AppendTrajectory` / `PlanCommit` 注释、`INTEGRATION_GUIDE.md` / `.zh.md` 的 L6 计划面表格、`internal/plan/agent.md`。`internal/repo/core/model.go` 的字段注释本就是惯例清单，不动。

## Alternatives considered

- **把 `sandbox_ask` 加进名单**（宿主 issue 提的 (a)）——落选：把某个宿主的传输层帧名（meowagent 的 WS 挂起帧 kind / checkpoint kind）写进跨宿主的库命名空间，等于开一条按宿主收词的路；下一个宿主的 `deploy_confirm`、`retry_prompt` 都会来要一行。名单本身的问题一个没解。
- **维持拒绝，确认「裁决反问不入轨迹」为库侧语义**（宿主 issue 提的 (b)）——落选：语义上站得住（名单是 v1.4.2 起写明的公开契约），但库为此保住的是什么答不出来；代价是宿主丢掉一个审计事件，而宿主侧唯一自救路径是把裁决反问改名塞进 `ask_user`——那是让宿主用 payload 复刻库已经拥有的字段。
- **保留名单但改为大小写/前缀宽容匹配**——落选：一条只用来拒写的名单，匹配得宽一点不会让它开始保护任何东西，只多出一套匹配规则要测。
- **改成「库内已知类型 + 宿主自定义命名空间」（如 `x_sandbox_ask`）**——落选：为一条不存在的约束发明一套前缀约定，比原名单更接近过度设计。

## Consequences

- **换来**：计划轨迹写入面与裸轮次写入面对称，宿主事件名不再需要库的批准；`sandbox_ask` 这类审计事件直接可入；库少一份需要同步维护的名单（10 词出现在 5 个文件里，任一处漂移就是文档与实现不一致）。
- **放弃**：计划事件的命名纪律不再由写入面强制。同一域内不同宿主（或 MCP 与 Go 混用）可能写下风格不一的事件名，读侧与结晶 prompt 拿到的 `type=` 字段可信度下降。**接受这个代价的判断依据是：库内没有任何逻辑消费这个字段的可预测性**，命名纪律的实际落点是文档惯例与宿主自己的常量表。
- **对宿主**：放宽方向，兼容（原本被拒的写入现在成功），无需改调用点。
- **验证**：`TestPlanEventNamesAreHostOwned`（`internal/l6_test.go`）钉住三件事——宿主自命名被接受、回读名字未被改写、空 `EventType` 仍在建链前被拒（`TotalCount` 不涨）。
