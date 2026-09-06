# 决策档案: 内置说明书卡机制整体删除（能力池只装可触发单元）

Status: implemented

## Problem

用户对 L5 的定位：能力目录——装「LLM 能用的东西，或者能识别的东西」，`plug/` 同理，对标主流 agent 把插件/skill 汇总进一个能力面的做法。对主流设计调研（Claude Code / OpenClaw 的 skill 渐进披露、MCP 工具面与 Tool Search、Manus 上下文工程、OpenAI function calling 实践）得到的共性结论：**能力面只装 LLM 可触发单元**（工具 schema / skill 文件 / 动作链）；「怎么做」的知识是 skill 文档、按需加载；库/API 自身的用法说明走文档检索通道（llms.txt / Context7 类），从不驻留能力池——没有任何主流框架把引擎自身方法的用法说明塞进能力面。

引擎内置卡（同日早些时候刚从 go:embed 改为代码组装，见 [rejected 档案](../../rejected/architecture/2026-09-06-builtin-cards-code-assembly.md)）是「宿主方法说明常驻能力池」的第三种形态：9 张覆盖 32 个库方法用法，约 28KB 文本随每次全量 `ListCapabilities` 流动。它补丁链式地衍生出：宿主整体 skip（meowagent）、ListCapabilities 的 stored-shadow 去重、结晶折回的保留名检查、MCP 宿主「30 工具 + 卡文本」双份暴露、10 个纯管理方法（装/卸插件、删/并场景、删知识图）混进 LLM 视野。只换装配方式没有回答定位问题——说明书不是能力。

## Decision

- **内置说明书卡机制整体删除**：`internal/builtin_capabilities.go`（BuiltinCards 代码组装）、`internal/l5_builtin.go`（SetBuiltinCapabilities / findBuiltinCapability / builtinMatchingList / db.builtinCapabilities 字段）、ListCapabilities 的 stored-shadow 合并段、结晶折回的 `reserved` 谓词（`trajectory.ApplyCandidate` 签名去掉该参数）、`CapabilityOriginBuiltin` 常量（core → internal → api 别名链一并删）、五个测试文件/段落（词表测试、builtin 集合测试、影子卡测试、fresh-DB 测试改钉「空池」）。
- **能力池回归本意**：池里只有宿主导入的卡（`ImportCapability` / `plug/` 注入）、结晶草稿（`Crystallize`）与宿主直写记录；空库 `ListCapabilities` 返回 0 条。`plug/` 自动注入机制原样保留；`Origin` 枚举其余三值（imported/crystallized/host）不变。（该保留决定同日被 [L5 记录层退役](2026-09-06-l5-record-layer-retirement.md) 取代：plug/ 注入与 `Origin` 枚举连同整个记录层一并移除，能力唯一事实源归还宿主目录。）
- **方法说明的归宿**：`go doc api.Session`（宿主唯一文档入口）与 `INTEGRATION_GUIDE.md`——即主流的「文档检索通道」角色；向 LLM 投影工具时先注入一行索引（id + name + summary + trigger），参数详情按需按 id 查。
- **同批落地公开面按使用者分两类**（注释 + 文档层，代码零结构变化）：任务面 22（宿主每轮驱动 + LLM 工具绑定）与组装/管理面 10 + DB 8（会话边界与管理通道，不做成 LLM 工具）；钉在 `api/surface_public_test.go` 分组清单，声明在 `api/session.go` 头注释与 `INTEGRATION_GUIDE.md` §8。MCP 工具面维持 30 个不动（「刻意不收敛」既定决策）。

## Alternatives considered

- **修补：卡砍到 22 个任务面条目**（去管理方法、desc 去 MCP 工具名引用，28KB → 约 18KB）——落选：文档仍伪装成能力，架构别扭保留一半；用户裁决「删掉内置卡（对标主流）」。
- **改造为单张索引卡**：32 方法每行「名字 + 一句话」，删 8 张域卡整段 desc/schema（28KB → 约 2KB），对齐渐进披露的「元数据常驻」模式——落选：仍是「宿主方法说明常驻能力池」，主流无先例的形态没有变；「识别」需求由宿主的工具面与 go doc 承担。
- **维持 9 张卡 32 条目**——落选：28KB 与双份暴露原样；调研结论直接否定该形态。

## Consequences

- 换到：L5 语义 = 纯能力目录（LLM 可触发单元的汇总），与主流同构；MCP 宿主调 `capability_list` 不再拿到 28KB 方法说明；meowagent 的 skip 死代码、同名遮蔽、结晶保留名、管理方法混入视野四类补丁链全部消失；本仓净删约 31KB 组装文件 + 合并/守卫链与五个测试件。
- 代价 1（对消费方 breaking）：meowagent 的 `contracts.CapabilityOriginBuiltin` 别名编译断（`contracts/memhop.go:216`），`toolbox/l5.go:59` 的 `Origin == builtin` skip 成死代码（另 `toolbox/l5_test.go:452`、`memory/handle_test.go:69` 引用）——归属主自己的跟版适配轮次，本仓不改它的文件。
- 代价 2：「不写代码、直接读 L5 拿方法说明书」的裸宿主失去了说明书。现有两类宿主都不需要它：meowagent 直连 Go API 且整体 skip 内置卡，MCP 宿主的 LLM 用工具 desc。若未来出现该形态的宿主，主流做法是给它一份检索式文档（llms.txt 类），而不是把说明书驻留进能力池。
- 代价 3：结晶的 LLM 候选目录少了内置名集合——LLM 理论上可产出与已删说明书同名的卡。名字即身份（`CapabilityID(name)`），与已存储卡撞名走 reuse/merge 既有处置；「保留名」概念随内置卡消失，无残留。
- 升级路径：无磁盘格式变化（内置卡本就不落盘，`FormatVersion` 保持 `0x000B`），无迁移；存量文件里 `origin: "builtin"` 字符串只是不再被代码产出，读取不受影响。
