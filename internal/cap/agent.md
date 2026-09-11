# internal/cap — 能力层契约（模块级 agent 上下文）

本目录下的每个子包是一个功能：身份中立组件，只声明自己需要的接口与参数。
修改任何能力包前先读本文件；改完同步该包自己的注释。

## 分工（一功能一包）

- `engram/`：L1 场景超图的共现建边（Jaccard）与遗忘衰减
  （`BuildHyperedges`/`DecayNetwork`/`RebuildFromL2`）。
- `llmops/`：三类 LLM 调用点的 prompt 契约、输出解析与自愈重试预算
  （`ExtractKeywords`/`Consolidate`/`Distill`）；传输经注入的 `Chat` 接口。
- `profile/`：L0 画像的初始值（`Default`）、紧凑摘要（`Brief`）、蒸馏样本与排名
  （`Samples`/`SampleRank`）、蒸馏结果写回（`MergeDistill`）。
- `knowledge/`：L3 导入节点的字段合并策略（`MergeFields`/`OverwriteFields`）。

## 纪律

1. **身份中立、依赖注入**：只接收数据与注入的原语（engine / index / `Chat`）；
   禁止 new 依赖、禁止 import `internal` 根（会形成反向依赖环）。
2. **依赖方向**：`cap -> {common, repo, repo/core, repo/index}` 单向；能力包之间
   互不 import，需要协作时回到组装根编排。
3. **窄接口**：只声明自己需要的小接口（≤3 方法），如 `llmops.Chat`——注入方以
   结构化鸭子类型满足它，不需要显式适配。
4. **不认识编排，预算各归各包**：本层不因触发阈值而运行、不决定一次调用改完的记录
   归谁镜像、也不负责失败后的回滚——这些都是调用方的事。收到的东西并不统一：
   `llmops` 收渲染好的文本，`profile`/`engram`/`knowledge` 收注入的 engine/index
   原语，按各自算法需要读自己那一层的邻域；裁剪上限同样住在各自包里（`profile` 的
   样本数与每样本关键词数、`llmops` 的重试阶梯）。

5. **读失败不当成「已消失」**：本层凡以一次读的结果决定一条记录的去留（L1 衰减的
   两处级联、depth-3 的保留规则），一律按错误码分道——`ErrNotFound` 才是没了，
   `ErrIO` / `ErrDeserialization` 上抛、让调用方停下这一轮。把后者改写成「没了」，
   后果是一次瞬时读失败删掉一条记忆，而删掉的这条不会再自己长回来。

每个能力包自带同包单元测试；改算法必须带测试并同步更新本文件的分工条目。
