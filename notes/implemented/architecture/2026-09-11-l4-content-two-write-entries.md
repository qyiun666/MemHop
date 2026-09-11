# 决策档案: L4 内容有两个写入口，融合摘要不走宿主的写入契约

Status: implemented

## Problem

分包审查轮提议给巩固摘要单开一个 `content.AppendFusedSummary`，让「一轮内容写进 L4」
只剩一个入口，并顺带把宿主那套 payload 预算校验套到这条记录上。理由当时成立：`Append`
的文档声称自己是唯一写路径，而它不是。

## Decision

两个写入口是结构所迫，不是待补缺口：

- 宿主侧经 `content.Append`——它校验 `Kind`/`Role`/`Seq`/`ContentType` 的组合与预算。
- 引擎侧的融合摘要经 `repo.AppendArchiveL4` 直接落一条 `Seq=SeqUser` +
  `Role=RoleDream` 的原文，因为这条记录的说话者就是库自己。

`dream` 与 `content` 同属第 3 层小方法包，包之间禁止互相 import，所以 `dream` 调不到
`content` 的任何函数。声称「唯一写入口」的那句话因此收回到 `content` 包内（它是本包
唯一写路径），系统级的「两个入口」只在 `internal/agent.md` 讲一次。

## Alternatives considered

- **`content.AppendFusedSummary`，dream 改调它**：违反小方法包互不 import，除非把
  `content` 升格为同层可引用——那是为一个调用的便利改动整层依赖规则。
- **把预算常量与校验下沉到 `core`**：磁盘层开始携带宿主契约（64 KiB 是对宿主的承诺），
  先破 `repo` 的「本层无业务语义」。
- **让摘要也过 `ValidateAppend`**：它按设计拒宿主给 `RoleDream`，套用等于把库自有的
  标记改成宿主可申请的例外。
- **删掉 `Append` 的「唯一」二字了事**：形式上不再失真，但把「为什么有第二个入口」这个
  真问题留在原地，下一轮还会被同一处注释引出来。

## Consequences

融合摘要不受 64 KiB 校验，实际上限是 LLM 输出天花板（`LlmConfig.MaxOutputTokens`，
默认 8192）。宿主把该值调到极大时这条记录会比宿主侧同类更大、读回更重，且没有任何
静默截断。接受它是因为摘要体量随组内话题数增长而非随对话长度增长，而代价一旦出现只是
一次较重的读，不是一次说不清的写。

一条记录的字段归属在 `content` 与 `dream` 两处各自声明一次，这是「包内注释只服务本包」
的必然结果；跨包的那份事实唯一住在 `internal/agent.md` 的巩固条目里。
