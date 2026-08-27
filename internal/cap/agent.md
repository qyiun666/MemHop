# internal/cap — 能力层契约（模块级 agent 上下文）

本目录是 MemHop 的**能力层**：一功能一包的身份中立组件。修改任何能力包前
先读本文件。

## 分工（一功能一包）

- `scenefind/`：三通道检索（BM25/f32 向量/实体）→ RRF 融合 → 场景聚合、
  加分与向量地板；附带 L1 扩散激活 `SpreadingActivation`。
- `engram/`：L1 场景超图的共现建边（Jaccard）与遗忘衰减
  （`BuildHyperedges`/`DecayNetwork`/`RebuildFromL2`）。
- `llmops/`：关键词提取/L2 巩固/L1→L0 蒸馏/L6→L5 结晶四类 LLM 调用点；
  prompt 契约 + 输出解析 + 自愈重试预算全在此，传输经注入的
  `Chat` 接口（组合根 Provider 实现）。
- `capability/`：memhop-capability/v3 文档的读取、校验（`Validate`）、
  定义合并（`MergeDefinition`/`FromImport`）与列表过滤（`Matches`）。
- `profile/`：L0 画像摘要渲染（`Brief`）、关键词分布重建（`Generate`）、
  蒸馏信号写入（`Samples`/`MergeDistill`/`SampleRank`）。
- `knowledge/`：L3 图匹配（`MatchGraphs`）与导入节点字段合并策略
  （`MergeFields`/`OverwriteFields`）。

## 纪律

1. **身份中立、依赖注入**：只接收数据与注入的原语（engine/index/encoder/
   Chat）；禁止 new 依赖、禁止 import `internal` 根（会形成反向依赖环）。
2. **依赖方向**：`cap -> {common, repo, repo/core, repo/index}` 单向；
   能力包之间互不 import，需要协作时回到组装根（internal）编排。
3. **窄接口**：只声明自己需要的小接口（≤3 方法），如 `llmops.Chat`、
   `scenefind.Encoder`；宿主实现天然满足（结构化鸭子类型）。
4. **业务 DTO 在 core**：跨包使用的请求/响应结构体定义于
   `repo/core/model_dto.go`、`model_distill.go`（最底层纯数据），
   组合根与门面用恒等别名沿用历史命名。
5. 每个能力包自带同包单元测试；改算法必须带测试并同步更新本文件分工条目。
