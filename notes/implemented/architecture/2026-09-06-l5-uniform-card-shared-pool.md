# 决策档案: L5 统一能力卡 + 文件级公共池 + plug/ 自动注入

Status: implemented

## Problem

L5 能力卡有四型卡片级特判（mcp/skill/api/composite）外加卡片级 `Workflow` 字段：同一动作链存在两份真相源（卡 `Workflow` 与条目 `Config`），而宿主 meowagent 的执行路径只读 Config 里的 `tool` 键步骤、从不读 Workflow——四型特判与双真相源都是纯负重。导入面一次只能进一张卡，主流形态「一个插件包含多张 skill 卡」没有归宿；L5 又按 agent 域隔离存储，家族里每个子 agent 要重复导入同一批能力、互相看不见。

## Decision

- **三层模型，卡片级无类型**：插件包（v4 文档/文件夹）> 卡（Capability）> 功能条目（ResourceRef）。卡 = 名称 + N 个条目，每条目自带启动方式（`type: mcp|skill|api|composite` + `ref`/`config`）、说明（`desc`）、用法（`input` JSON Schema/`output`）。删 `Capability.Type`/`.Workflow`、`Workflow`/`WorkflowStep` 类型、`CapabilityPatch.Type`/`.Workflow`、`CapabilityListQuery.Type`、Validate 四型 switch、api 与 internal 的 `Workflow` 别名；`CapabilityType` 四值枚举保留在条目级（宿主资源级分派不变）。新增 `Capability.Package` 记录来源包名（导入后不可变；结晶卡为空）。
- **动作链唯一归宿**：`composite` 条目的 `config` = `{"steps":[{"tool":"<名>","args":{...}}]}`——键名用 `tool` 与宿主可执行解析器对齐（`ref` 键会被宿主整卡拒绝）。校验规则：JSON 形态的 config 必须合法解析，steps 数组每步须为对象且带非空 `tool` 键；非 JSON 的松散行形态放行（说明文本）。
- **v4 = 插件包文档**：`{"format":"memhop-capability/v4","name":"<包名>","capabilities":[{卡},...]}`，单卡文件 = 一卡包。包名必填、capabilities 1..N、卡名包内唯一且**跨包全局唯一**（CapabilityID 由名字哈希派生，撞名 = 身份冲突显式拒绝）、卡 trigger/summary 至少一、条目 ≥1 且 name 必填、条目 `type` 限定四值枚举（mcp/skill/api/composite）、Input 非空须合法 JSON（是否为合法 schema 由宿主自理）。v3 及更早格式导入显式拒绝。内置说明书卡机制已于 v1.6.1 整体删除（能力池只装 LLM 可触发单元），撞名保护随之消失——卡名即身份（`CapabilityID(name)`），同名卡走 reuse/merge 既有处置；演进链见 [2026-09-06-remove-builtin-cards](2026-09-06-remove-builtin-cards.md)。结晶侧卡级校验按落盘点分闸：create/merge 整卡覆写走前置校验，reuse 命中不落盘免校验（最小 name-only 载荷是钉死契约，见 `TestCrystallizeReuseMinimalPayload`），reuse 未命中降级 create 在落库前过闸、失败折成 skip。
- **L5 文件级公共池**：复用 L3 的保留域机制——`core.SharedL3AgentID` 更名 `core.SharedPoolAgentID`（常量值不变，磁盘 agent_id 不变），`lockSharedL3` 更名 `lockSharedPool`；L5 大方法一律 `CheckSession(callerID)` 后拿共享域锁。FormatVersion 0x000A → 0x000B，旧文件 Open 时显式拒绝、不迁移（L5 记录换域 = 语义布局变更，静默消失不可接受）。
- **plug/ 自动注入**：Open 装配序列在存储就绪后扫 `<dir(mehPath)>/plug/*/capability.json` → v4 包解析 → 逐卡 upsert 共享池（同包重注入按 FileHash 判等，字节级不变零写入——append-only 文件不因重启膨胀）；单个坏包 Warn（路径+原因）后跳过，不阻断 Open；缺目录 no-op，其余读目录失败 Warn。
- **`ImportCapability(path)` 升级为包导入**：返回逐卡处置摘要 `CapabilityImportResult{CreatedIDs, UpdatedIDs, Errors}`（仿 CrystallizeResult 形状）；方法签名不变。`CapabilityListQuery` 加 `Package *string` 过滤。
- 公开方法集数量不变（34 会话 + 8 DB 方法）；MCP 工具数不变（31）。

## Alternatives considered

- **保留四型卡片级特判，包格式只做容器**：宿主适配面最小，但 Workflow/Config 双真相源继续存在，而唯一执行方只读 Config——删除优于共存。落选。
- **L5 维持按域隔离，plug/ 对每个域注入一份**：一个包要写 N 份记录、跨 agent 合并靠宿主拉通，与「所有 agent 都能用」的需求相悖。落选。
- **为 L5 新建第二个保留域（L3、L5 各一）**：锁与快照路径多一套，而两个池的并发特征完全一致（链路无 LLM、操作短、全局串行）——共用一个保留域更少状态，改名即可（域值不变）。落选。
- **不升 FormatVersion（L5 记录留原域）**：省一个版本号，但旧文件的 L5 记录变成共享池读不到的孤儿、`ListCapabilities` 静默丢卡——静默消失比硬拒绝更差（与 L3 升 0x000A 同款决策）。落选。
- **plug/ 坏包直接 Open 失败（fail fast）**：一个手误的 JSON 能让整只猫起不来，坏包与既有能力毫无关系——先例是 meowagent 对 MCP 发现超时的降级：Warn + 跳过，保住「Open 永远成功」。落选。
- **结晶候选不分动作一律前置全量卡级校验**：实现后即破最小 reuse 契约（reuse 命中只定位不写盘，payload 本就无需完整卡形，宿主实测钉死该行为）——校验必须贴着写点走，写哪验哪。落选。

## Consequences

- 换到：一个文档 = 一个插件包（插件/skill/动作链/mcp 四种主流形态都有存储归宿），宿主零注入成本（把包放进 `plug/` 即用），能力全家共享；Workflow/Type 双真相源消灭，`PromptCard` 渲染随之收敛为一段卡文。
- 代价 1：打破按域隔离的第二条缝（继 L3 之后）——`UpdateCapability`/`DeleteCapability` 任何存活会话可操作全池，一个 agent 删卡影响所有 agent；MCP 多租户承诺改写为「除 L3/L5 外按域隔离」。与 L3 同构的既定代价。
- 代价 2：下游 meowagent 断点清单（go.work 取证实测，本仓只取证不改）：生产代码恰一处——`toolbox/l5.go:445` `cap.Type undefined`（kanban 卡片渲染行）；测试侧 `toolbox/l5_test.go:77` 卡片级 `Type` 构造（vet 被生产断点挡住，其余测试断点待宿主修完生产再枚举）；`contracts` 实测无 `Workflow` 别名（计划期的假设不成立，取证修正）；`ImportCapability` 响应形状变化（`[]Capability` → `*CapabilityImportResult`）经 WS `memory_capability_import` handler 流出，属行为适配非编译断点。宿主在打 tag 后的独立轮次适配。
- 代价 3：`FileHash` 是包水位而非内容指纹，`UpdateCapability` 不清除它——未变更包的重导入（含 plug/ 每次 Open）命中跳过守卫，宿主对卡的生命周期（如 deprecated）与定义修改存活到包内容真正变更为止；变更的包整卡覆写并回到 active（包是卡内容的真相源）。
- 升级路径：0x000A 及更早文件不支持原地升级——宿主自行重导 L3 与 L5（旧文件本就打不开）。
