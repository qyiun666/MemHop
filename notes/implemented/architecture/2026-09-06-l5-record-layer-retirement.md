# 决策档案: L5 记录层整体退役（目录即能力）

Status: implemented

Superseded by: 2026-09-11-capability-surface-retirement.md

## Problem

v1.6.0 建成的 L5 记录层（[统一卡 + 公共池 + plug/ 注入](2026-09-06-l5-uniform-card-shared-pool.md)）在同版本内被两轮收敛推到终局：内置说明书卡删除（[前一份档案](2026-09-06-remove-builtin-cards.md)）之后，池里存的**已经是 plug/ 文件的副本**。为维持「文件 + 库」双事实源同步，机制清单还在增长：`FileHash` 水位（包级幂等）、同字节重注入零写入、patch 幸存（宿主对状态与定义的修改存活到包内容真正变更）、draft→active→deprecated 状态补丁。每一条机制都不是能力本身，而是两个事实源对齐的代价；宿主侧（meowagent）为消费池还叠了 skip/遮蔽/保留名等补丁链。

用户裁定：**目录即能力**——能力卡的唯一事实源是宿主自有的 `plug/<包>/capability.json` 目录，宿主自扫自装配、变更重启生效、结晶草稿落 `plug/draft/`、activate = 文件转正。库不再是事实源，也就不再需要记录层；它真正的价值只剩两块纯能力：v4 磁盘格式的解析校验（单一来源，免宿主双维护格式）与纯结晶提炼（轨迹 → 候选）。

## Decision

- **L5 记录层整体退役**，引擎不存储任何能力记录：删五个会话方法（`ImportCapability`/`UpdateCapability`/`DeleteCapability`/`ListCapabilities`/`RecordCapabilityUsage`，公开面 32→27：任务面 22→20、管理面 10→7）、八个类型（`Capability`/`CapabilityImportResult`/`CapabilityListQuery`/`CapabilityPatch`/`CrystallizeResult`/`CrystallizeDetail` 与 `CapabilityStatus`/`CapabilityOrigin` 及其常量）、`internal/l5.go`/`l5plug.go`（注入/水位/patch 幸存全链）、`repo/l5layer.go`、core 记录层（`RecL5Capability`、typed 读写器、`CollectAllCapabilities`）。`core.SharedPoolAgentID` 保留（只剩 L3 引用）。
- **格式 0x000B → 0x000C**：`0x0F` 帧型退役；旧文件 Open 时显式拒绝、不迁移（本仓先例）；`SnapshotVersion=0x02` 不变（索引结构无变化）。
- **v4 格式唯一事实源下沉 `internal/cap/capability`**：卡片文档类型（`CapabilityImport`/`CapabilityPackageDoc`/`ResourceRef`）迁入该包，`BuildPackage` 只解析校验、不再盖 Package/FileHash 戳；经 api 导出包级面 `CapabilityFormatV4` + `ParseCapabilityPackage(data, source)`（内走整包校验）+ `ValidateCapabilityCard(card)`——恒等别名模式，不加 api→cap 直连边。
- **`Crystallize(ctx, turnID, existing []CapabilityImport)` 转纯提炼**：ReadTurn → TrimByBudget（128KB 预算留在引擎内）→ llmops.Crystallize，出参即候选列表（`CrystallizeOutput.Capabilities`）；删 ActiveOnly 读取与折回落库循环（`trajectory.ApplyCandidate`/`applyCrystallized`/`findTarget` 整链）。**reuse_id 从 16-hex 记录 id 改为已有卡名**（卡名是 v4 文档的唯一性保证、宿主目录可寻址身份），systemCrystallize 提示词同步改写；未过卡级校验的候选原样返回，过滤职责移交宿主。宿主职责：校验、对照目录去重、把草稿写成 `plug/draft/` 下的 v4 文档、文件转正即激活。
- **`PromptCard` 迁为 `*CapabilityImport` 方法**：删 `id:`（hash 派生宿主无法复刻）/`package:`（目录名复述）/`usage:`（用量统计失去写入方）三行渲染。
- **MCP 面 30 → 24 工具**：六个能力工具（含便捷工具 `memhop_capability_get`）与 `--capability-dir`/`MEMHOP_CAPABILITY_DIR` 删除；`memhop_crystallize` 传 `existing=nil`，描述声明「引擎不落盘、去重落盘归宿主」。锁面只剩 L3 走 `lockSharedPool`，并发契约里的 L5 公共域锁句删除。

### meowagent 跟版断点（只列不执行，适配归属主自己的轮次）

- 消失方法 5：`ImportCapability`/`UpdateCapability`/`DeleteCapability`/`ListCapabilities`/`RecordCapabilityUsage`
- 签名变更 1：`Crystallize(ctx, turnID)` → `Crystallize(ctx, turnID, existing []api.CapabilityImport)`，回执从落库处置（CreatedIDs/Details）变为候选列表
- 消失类型 8：`Capability`/`CapabilityImportResult`/`CapabilityListQuery`/`CapabilityPatch`/`CrystallizeResult`/`CrystallizeDetail`/`CapabilityStatus`(+3 常量)/`CapabilityOrigin`(+3 常量)
- 新增可用：`CapabilityFormatV4`/`ParseCapabilityPackage`/`ValidateCapabilityCard`/`CrystallizeOutput`/`CrystallizeCapability`；MCP 六工具消失、`memhop_crystallize` 出参变形、`--capability-dir` 消失
- `.meh` 随 0x000C 拒绝旧版：宿主升级即换文件，无迁移

## Alternatives considered

- **只删 plug/ 注入、保留记录层**（池 = 宿主导入 + 结晶草稿）——落选：双事实源复杂度全保留（水位/patch/状态机一个不少），只是少一条注入通道；事实源问题原封不动。
- **记录层保留，plug/ 降级为纯导入源**（Open 扫目录调 `ImportCapability`）——落选：与上一案同构，且把「目录 = 事实源」的直觉变成「目录 = 种子」，宿主改文件后必须记得重导、重启不生效——比现状更隐蔽。
- **格式保留 0x000B，读侧跳过 0x0F 未知帧**——落选：违背本仓 bump + 拒绝先例（0x0005/0x0009/0x000A/0x000B 四次全部显式拒绝旧文件）；静默跳帧让旧文件在宿主眼里「能力全丢了却不报错」，坏过显式失败。
- **折中：记录层只存 hash/水位，内容只留目录**——落选：记录层退化为文件的影子索引，双事实源同步代价全保留，而 stored 卡的按条件查询随记录消失——两头不占。

## Consequences

- 换到：引擎零能力记录（Open 零扫描、`DeleteAgent` 少一个保留域例外、并发契约少一层公共锁）；宿主目录成为唯一可读事实源——文件即能力，激活/停用/删除都是文件操作；库的面收敛为「格式 + 提炼」两块纯能力，MCP 工具面 24 个。
- 代价 1（对消费方 breaking）：meowagent 五个调用点、八个类型、六工具与 `--capability-dir` 全部失效，断点清单见上——适配（自扫目录 + 落 draft + 转正）归其自身轮次，本仓不改它的文件。
- 代价 2：文件级共享能力池的语义消失——同一 `.meh` 的多个 agent 不再共享池（L3 仍共享）；要多 agent 共用能力，宿主把目录放共享路径，由宿主决定。
- 代价 3：库内失去 usage 统计（`RecordCapabilityUsage`）与 stored 卡的按条件查询（package/status 过滤）——前者本就没有库内读者，后者的读者是宿主自己的目录扫描。
- 升级路径：0x000C 拒绝 0x000B 及更早文件、不迁移；宿主的能力数据本来就在 plug/ 目录里，不受引擎换版影响。
