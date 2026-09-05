# 决策档案: L3 知识图升级为文件级公共池

Status: implemented — 0x000A 落地，`internal/l3shared_test.go` 六组用例与 api 隔离断言全绿

## Problem

设想「一个 `.meh` = 主 agent + 多个子 agent」时，L3 是唯一的缺口：知识图定位是项目级知识/代码图（宿主导入、库只存，见 `l3-code-graph` 定位），却按 agent 域隔离存储——家族里每个子 agent 要么重复导入同一份项目知识，要么互相看不见。MCP 单文件多租户形态同理：每个租户一份 L3 纯属复制。

## Decision

- 保留域机制：新增 `core.SharedL3AgentID`（frame.go，宿主不可见/不可删/`Session` 拒绑/`ListAgents` 不列/空闲回收豁免/`CreateAgent` 拒撞），全部 L3 记录住该域；`internal` 根 8 个 L3 大方法改走 `lockSharedL3(callerID)`（先 `CheckSession` 校验调用方域活着，再锁公共域）。graph/repo 小方法包零改动——agentID 本就是参数。
- 锁语义：L3 操作跨 agent 在公共域锁上全局串行（L3 链路无 LLM、操作短，代价可接受）；锚点校验（`scene.Create`/`ResolveForRead`/`UpdateScene`）持调用方锁无锁读公共域记录，由引擎级互斥兜底，`TestL3PoolConcurrentImportAndAnchor` 供 `-race` 证明。
- `DeleteL3` 两阶段：公共锁内删图 → 释放后遍历「默认域 + 注册表」逐域 `lockAgent` 清锚（`detachGraphAnchors`），不嵌套双锁（锁序环风险归零，也不会把 L3 全局串行顶在一个慢域后面）。遍历对象不用 `engine.IterAgents()`：它含未注册域会让 `lockAgent` 报错，且默认域不在注册表而场景锚点可能落在默认域。
- 格式 0x0009 → 0x000A，旧文件 Open 时显式拒绝、无迁移（沿用 0x0008 拒绝先例）。
- 公开面零变化（34 会话 + 8 DB 方法）。

## Alternatives considered

- **家族级公共池（库内引入主→子层级，公共池挂主域）**：`domain.Context` 加 parent、`CreateAgent` 面要动（v1.6.0 已打 tag，属破坏性或新增方法）。落选：用户拍板文件级即可，层级归宿主（meowagent 的 spawn 模型已建模主/幼/分身），库内不需要知道家族。
- **维持按域 + 宿主自同步**：零改动，但重复存储、跨 agent 合并靠宿主拉通，与「L3 是项目代码图、库只管存」的定位相悖。
- **不升 FormatVersion（旧 L3 记录留原地变孤儿）**：省一个版本号，但旧文件静默「丢」知识（查询读公共域看不到旧记录），且 `hash(Domain)` 同名重导入会产出第二份图。静默数据消失比硬拒绝更差，选升版。

## Consequences

- 换到：一份文件一份项目知识，家族/多租户共享；删档（克隆用完即弃）不误伤公共池。
- 代价：打破「完全隔离」的一条缝——`DeleteL3Nodes`/合并策略是全家共享的，一个 agent 删节点影响所有 agent；MCP 多租户「no data is ever shared」承诺改写为「除 L3 外隔离」。
- 已知窗口：`DeleteL3` 删图后、清锚前若有宿主同名重导入（图 id = hash(Domain) 同 id），该场景锚点会被清成未锚定，可经 `UpdateScene` 重挂——比嵌套双锁划算。
- 升级路径：0x0009 及更早文件不支持原地升级，宿主自行重导 L3（旧文件本就打不开）。
