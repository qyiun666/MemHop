# 决策档案: 内置说明书卡从 embed 包改为代码组装

Status: implemented

## Problem

内置 L5 说明书卡住在根包 `capabilities/`（go:embed 的 6 张 JSON）：`internal.Open` 需要 `fs.FS` 注入参数、`loadBuiltinCapabilities` 做文件解析、`Origin=builtin` 的只读守卫散布在 import/update/delete/usage/activate 五个写点，且 6 张卡只覆盖 24 个方法——核心循环（Search/Update/Dream）、轨迹三方法与计划树四方法完全没有说明书条目，拿卡当说明书的宿主看不到引擎的主要能力。宿主裁定：内置卡就是「组装对外接口方法成 L5 格式」+「plug/ 目录注入」，不需要专门机制。

## Decision

- **`capabilities/` 包整删**（go:embed FS + 6 张 JSON + README）：`internal.Open(cfg)` 去掉 `fs.FS` 注入参数，api 门面不再 import 数据包。
- **`internal.BuiltinCards()` 代码组装 9 张卡**（`builtin_capabilities.go`）：guide 总纲 + cycle/profile/scene/knowledge/archive/capability/trajectory/plan，**覆盖全部 32 个会话方法**（原 JSON 内容原样移植，新增 cycle/trajectory/plan 三张，Crystallize 从 capability 卡移入 trajectory 卡）。卡仍为稳定名派生 id（`core.CapabilityID`）、`Status=active`、`Origin=builtin`（宿主按 Origin 过滤的兼容面保留）。
- **守卫删除，语义回归自然**：`UpdateCapability`/`DeleteCapability`/`RecordCapabilityUsage`/`ImportCapability`（保留名拒绝）四处 `findBuiltinCapability` 守卫删除。内置卡**只列不存**——对内置卡 id 的写就是「记录不存在」→ `ErrNotFound`；同名字卡（导入/plug）入库即遮蔽内置卡（stored wins，既有去重逻辑不变），删除影子卡即还原说明书。
- **唯一保留的内置检查点**：Crystallize 折回的 `reserved` 拒绝（`findBuiltinCapability` 收窄为 bool 谓词）——防 LLM 生成的卡撞名说明书、在列表里永久顶掉手册。这是命名空间保护，不是只读机制。
- 卡内容词表由 `api/card_vocabulary_test.go` 继续钉住（改读 `internal.BuiltinCards()`），卡集合由 `internal/builtin_capabilities_test.go` 与 `api/open_test.go` 钉住（6 → 9）。

## Alternatives considered

- **改为目录分发（统一走 plug/ 机制）**：内置卡变成落库存储记录——二进制不再自足（宿主须随发行带卡目录）、卡占 `.meh` 空间、只读保护要走持久化 Origin 字段且随 CompactTo 永久保留；收益只有「运营期可改卡」而卡内容本就应跟二进制版本走。落选（用户裁定方向：代码组装）。
- **维持 embed 包并只补覆盖缺口**：改动最小，但 fs.FS 注入参数与五处散布守卫保留，「内置」仍是需要到处特判的概念，与裁定相悖。落选。
- **反射自动生成卡片描述**：从 go doc 反射方法签名自动产出 desc——描述质量（参数语义、调用顺序、词表）恰是卡的价值所在，生成结果不可用，还引入反射复杂度。落选（用户明示「不需要复杂代码」）。
- **保留导入侧保留名拒绝**：防影子卡的初衷（影子卡永远改不掉删不掉）在守卫删除后不成立——影子卡可更新可删除，删除即还原说明书，导入撞名无须拒绝。落选。

## Consequences

- 换到：内置机制收敛为「代码组装 + plug 目录」两条读路径，无散布守卫；说明书覆盖从 24/34 升到 32/32；`capabilities/` 包消失，api→internal 装配少一个注入参数。
- 代价 1：卡内容进 Go 源码——改卡要过编译；换来词表测试直接钉源码对象，漂移即红。
- 代价 2：内置卡改由每次 Open 重建，`FileHash` 恒空——无文件可哈希，plug 注入的 FileHash 去重逻辑不受影响（那是存储记录的事）。
- 代价 3：导入/plug 同名卡会遮蔽说明书，宿主拿到的是自己的影子卡——这是显式选择（stored wins），靠 `Origin` 字段可分辨；结晶折回仍挡 LLM 撞名。
- 关联：`ActivateCapability` 折叠与本档案同期落地，见 [2026-09-06-fold-activatecapability-planreplace](2026-09-06-fold-activatecapability-planreplace.md)；v1.6.0 档案中「导入撞内置卡显式拒绝」的事实自本档案起由「入库为影子卡」取代。
