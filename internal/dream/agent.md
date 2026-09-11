# internal/dream — 巩固阶段小方法

## 职责

- `SceneSet`：一次流水线的目标场景集——指定一个（存在性是答案的一部分），或全域。
- 两个保留窗阶段：`PruneContentStage`（报 `l4_prune`）与 `PrunePlanStage`（报
  `l5_prune`），共用 `ContentRetention` 这一个 7 天窗口，但各读自己的时间戳。
- `CompressScenes`：每场景一 goroutine，取回融合组并逐组应用。一组的摘要是父话题
  名下 `Seq=SeqUser`、`Kind=KindUtterance`、`Role=RoleDream` 的一条 L4 内容，
  `RoleDream` 只由本包戳写；空摘要在组落下第一条记录之前就被拒，其后任一步失败才经
  `discardFusedGroup` 按话题键回滚整组（话题上没有引用清单可回填）。
- `StructureStages`：L2Meta 重建并即刻装回 → L1 同步/建边/重建/衰减（用装回那份）→ L0 蒸馏。
- `DistillL0Stage`：L0 蒸馏，只被本包的 `StructureStages` 调用。
- 阶段报告：`AppendStage`/`StageCancelled`/`stageStatus`。
- L1 衰减与建边的调参常量随阶段在本包。

## 契约

- 全部阶段在调用方已持 `ac.Mu` 的前提下运行；LLM 经 `ac.LLM`，取消挂传入的 `ctx`。
- 两个清扫阶段都是 best-effort：失败记 `slog.Warn` 并进报告，绝不中断流水线。
- `l4_prune`/`l5_prune` 排在「有没有场景可压缩」的判断之前，所以无事可巩固的域
  照样按时清理。
- 每组要么整体生效，要么不留任何东西：`applyOneGroup` 的任一步失败都先回滚本组已
  写的记录再报原因。回滚失败只 warn——子话题仍停在 depth-1，下一次流水线会重新
  挑中这组。

## 陷阱

- `l2_compress` 全场景失败要上抛错误。L2Meta 的重建**一算出来就装回**，不等 L1 阶段：
  L1 各阶段只写 L1 记录，一次 L1 失败不会让一份按当前 L2 记录算出的缓存失效；反过来
  延后装回会让本域继续读到本次已沉下去的压缩之前的深度与子链。本包的压缩改写话题深度
  时不带任何增量镜像步，这次整表重建就是它唯一的对账点。
- 计划节点的清扫只带走它自己：节点按自己的 `UpdatedAt` aging，所以一棵过期树上
  绑着的新事件必须存活。
- 在途豁免读的是节点时钟：`HasNonDone && LastActiveAt >= cutoff`，其中
  `LastActiveAt` 是该树节点 `UpdatedAt` 的最大值。它保住的是**整棵活树**（含早已
  不更新的 done 父节点），而不是让高频写事件的树续命——一棵树该不该活着，只有对它
  做过的提交能回答。
- 可清扫单位就是 `repo.CollectPlanNodes` 给的按键聚合（键下无节点即不成聚合），没有
  独立的计划登记表。这一步读引擎而不读 `ac.Plans`：一次清扫不该被一份可能滞后的
  缓存塑形。
- `applyGroups` 分报 applied/rejected，rejected 计入 `failures`——「没什么可压缩」
  和「组没法应用」是两件事；空 `merged_summary` 属于后者，它会让子话题沉到一个
  什么都带不动的父节点下面。
- 本包的衰减参数不引入读侧反馈：节点重要性只有同步新建时的 `1.0` 与时间衰减两个
  来源。这是刻意取舍——±0.05 量级的慢游走左右不了存活判定，为一个阶段加两个热路径
  写入字段不值。
