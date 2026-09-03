# internal/turn — 轮次沉淀小方法

- **职责**：`Targets`（Update 入参校验 + 场景/话题 ID 解析）、
  `PriorL4Refs`（重放前读旧引用）、`WriteArchives`（一轮两条原文）、
  `DropRetained`（旧档案墓碑差集）、`ReadProfile`（Search 的 L0 读面，
  未建立按空画像）。
- **契约**：提炼（llmops）在根的大方法里做且排在任何写入之前；本包只做
  校验与记录读写。同 `TopicID` 重放 = 覆盖（先读旧引用 → 写新引用 →
  墓碑差集），保证"一轮恰好两条原文"在改写文本的重放下成立。
- **陷阱**：`PriorL4Refs` 对不存在的话题返回 nil（首次沉淀），但瞬时读
  错误必须上抛，不得当成"无旧引用"。
