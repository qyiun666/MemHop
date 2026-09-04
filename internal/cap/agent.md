# internal/cap — 能力层契约（模块级 agent 上下文）

本目录是 MemHop 的**能力层**：一功能一包的身份中立组件。修改任何能力包前
先读本文件。

## 分工（一功能一包）

- `engram/`：L1 场景超图的共现建边（Jaccard）与遗忘衰减
  （`BuildHyperedges`/`DecayNetwork`/`RebuildFromL2`）。
- `llmops/`：轮次关键词提炼（`ExtractTurnKeywords`，宿主热路径每轮恰好一次）/L2 巩固/L1→L0 蒸馏/L6→L5 结晶四类 LLM 调用点；
  prompt 契约 + 输出解析 + 自愈重试预算全在此，传输经注入的
  `Chat` 接口（组合根 Provider 实现）。
- `capability/`：memhop-capability/v3 文档的读取、校验（`Validate`）、
  定义合并（`MergeDefinition`/`FromImport`）与列表过滤（`Matches`）。
- `profile/`：L0 画像摘要渲染（`Brief`）、关键词分布重建（`Generate`）、
  蒸馏信号写入（`Samples`/`MergeDistill`/`SampleRank`）。
- `knowledge/`：L3 导入节点字段合并策略
  （`MergeFields`/`OverwriteFields`）。

## 纪律

1. **身份中立、依赖注入**：只接收数据与注入的原语（engine/index/
   Chat）；禁止 new 依赖、禁止 import `internal` 根（会形成反向依赖环）。
2. **依赖方向**：`cap -> {common, repo, repo/core, repo/index}` 单向；
   能力包之间互不 import，需要协作时回到组装根（internal）编排。
3. **窄接口**：只声明自己需要的小接口（≤3 方法），如 `llmops.Chat`——
   宿主/组合根的实现以结构化鸭子类型满足，不需要显式适配。
4. **业务 DTO 在 core**：跨包使用的请求/响应结构体定义于
   `repo/core/model_dto.go`、`model_distill.go`（最底层纯数据），
   组合根与门面用恒等别名沿用历史命名。
5. 每个能力包自带同包单元测试；改算法必须带测试并同步更新本文件分工条目。
- engram 的 L1 衰减（`decayRemainingEdges`/`removeEdgeFromNode`）只在记录确实不存在时跳过那条：索引点名而引擎读不动就上报，否则一条边会被当作不存在而永不衰减、且节点可能留着指向已删边的引用。
