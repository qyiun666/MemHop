# internal/dream — 巩固阶段小方法

- **职责**：Dream 流水线的阶段实现：`SceneSet`、两个保留窗阶段
  （`PruneContentStage` 报 `l4_prune`、`PrunePlanStage` 报 `l5_prune`，共用
  `ContentRetention` 这一个 7 天窗口但各读自己的时间戳）、
  `CompressScenes`（每场景一 goroutine；融合组串写——摘要是父话题名下
  `Seq=1`、`Kind=utterance`、`Role=dream` 的一条 L4 内容，`RoleDream` 只由本层
  戳写（宿主传什么都不会被采信为融合正文），话题上不存在引用清单可回填，任一步失败经
  `discardFusedGroup` 按话题键回滚）、
  `StructureStages`（L2Meta 重建 → L1 各阶段 → 装回缓存 →
  L0 蒸馏）、`DistillL0Stage`（只由 `RunDream` 调，根上不再有独立的蒸馏入口）、
  阶段报告（`AppendStage`/`StageCancelled`）。L1 衰减/建边/相似度的调参
  常量随阶段在本包。
- **契约**：所有阶段都在调用方已持域锁的前提下运行（根的 `RunDream`）；
  LLM 经 `ac.LLM`，取消挂 `ctx`（即 `ac.OpCtx`）。两个清扫阶段都是 best-effort：
  失败记 `slog.Warn` 并进报告，绝不中断 Dream。`l4_prune`/`l5_prune` 排在
  「有没有场景可压缩」的判断之前，所以无事可巩固的域照样按时清理。
- **陷阱**：`l2_compress` 全场景失败要上抛错误；L2Meta 新缓存只在 L1 阶段
  成功后装回（L0 蒸馏失败不推翻重建）。
- **清扫不再跨层**：一个计划节点过期只带走它自己。事件与原文同住 L4、按自己的
  `CreatedAt`  aging，所以过期树上绑着的新事件**必须存活**（钉在
  `TestDreamPrunePlanNodesAndContent`）。旧的「节点级联删事件 + 双份镜像同步」
  之所以存在，唯一理由是节点与事件同住一个索引；两层分开后那份耦合就没了。
- **在途豁免读的是节点时钟**：`HasNonDone && LastActiveAt >= cutoff`，其中
  `LastActiveAt` 是该树节点 `UpdatedAt` 的最大值。它的作用是保住一棵活树的**全部**
  节点（含早已不更新的 done 父节点），而不是让高频写事件的死树续命——事件不再
  参与这个判断，这一点与合并前相反，是刻意取舍：一棵树该不该活着，只有对它做过的
  提交能回答。
- **L1 重要性只有两个来源**：同步新建时的 `1.0`（`repo/l1layer_sync.go`）与时间衰减。没有「被读过就活得更久」这类反馈，场景记录也不挂读侧计数器——「被读过」与「最近有写入」在这里不可分辨，是刻意取舍：±0.05 量级的慢游走左右不了节点存活判定，而为一个阶段留两个热路径写入字段不值。
- `applyOneGroup` 返回 error（含「模型提了组但 merged_summary 是空的」这类），`applyGroups` 分报 applied/rejected，rejected 计入 `failures`——「没什么可压缩」和「组没法应用」是两件事。
- 计划清扫没有独立的计划登记表：可清扫单位就是 `repo.CollectPlanNodes` 给的按键聚合（键 = 开出该计划的轮次话题 id，键下无节点即不成聚合）。它读引擎而不读 `ac.Plans`：Dream 是磁盘维护者，不是热路径，清扫不该被一份可能滞后的缓存塑形。
