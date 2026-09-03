# internal/dream — 巩固阶段小方法

- **职责**：Dream 流水线的阶段实现：`SceneSet`、`PruneTrajectoryStage`
  （`TrajectoryRetention` 保留窗 + 计划节点清理，豁免条件＝持非 done 节点
  且窗口内活跃）、`CompressScenes`（每场景一 goroutine；融合组串写，
  任一步失败经 `discardFusedGroup` 回滚）、`StructureStages`（L2Meta 重建
  → usage feedback → L1 各阶段 → 装回缓存 → L0 蒸馏）、`DistillL0Stage`
  （也供根的 `DistillL0` 直接调）、阶段报告（`AppendStage`/
  `StageCancelled`）。L1 衰减/建边/相似度/反馈窗的调参常量随阶段在本包。
- **契约**：所有阶段都在调用方已持域锁的前提下运行（根的 `RunDream`）；
  LLM 经 `ac.LLM`，取消挂 `ctx`（即 `ac.OpCtx`）。
- **陷阱**：`l2_compress` 全场景失败要上抛错误；L2Meta 新缓存只在 L1 阶段
  成功后装回（L0 蒸馏失败不推翻重建）。
