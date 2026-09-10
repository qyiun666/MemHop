# internal/repo — 存储层契约（模块级 agent 上下文）

本目录（含子包 `core`、`index`）是 MemHop 的**存储层**。任何 AI agent 或
开发者修改本层前必须先读完本文件，修改后必须同步更新本文件。

## 唯一职责

按 **agent 域**（`agentID uint64`）提供记录的读、写、遍历与检索原语：

- `core/`：.meh 引擎——记录帧（26 字节：type/flags/length/agent_id/
  id_hash/crc32）、A/B 文件头、快照（0x02 分域）、空间回收、
  `StorageEngine` 索引（`agent -> idHash -> offset` 两级分域）、Slot 数据模型。
  `FormatVersion` 是 `0x000E`：Open 对任何其它版本（更旧**或**更新）都显式拒绝、
  无迁移路径。这次上抬改的仍不是帧布局而是记录含义——一轮的内容自 0x000E 起
  同住 L4（`Kind` 区分原文与事件，id 由 `hash("l4:"+topic+":"+seq)` 派生），
  L6 只剩计划节点、搬到新帧型 `RecL6PlanNode 0x0F`。旧文件把事件存在一个已不存在
  的记录类型里、把归档按正文哈希发号，按新规则哪一条都指不到东西。
  `StorageEngine` 按功能分文件：`engine.go`（索引模型/访问器）、
  `engine_lifecycle.go`（Create/Open/Checkpoint/Close）、`engine_write.go`（追加）、
  `engine_read.go`（索引查找读）、`engine_delete.go`（墓碑删除）、
  `engine_recovery.go`（扫描/撕裂尾帧截断/索引重建）；数据模型分
  `model.go`（Slot 结构）/ `model_enums.go`（枚举）/ `model_dto.go` 与
  `model_distill.go`（跨包使用的请求与响应结构，最底层纯数据）。
- `index/`：索引——L2Meta（场景读回的唯一话题缓存，`rebuild.go` 全量重建）/
  `l4.go`（`L4Index`：一个话题名下有哪些内容槽位，按 Seq 升序、条目带 `Kind`）。
  只依赖 `core`。
- 根目录 `l0layer.go`~`l4layer.go`/`l6layer.go`、`agentlayer.go`
  （L5 层文件已随 L5 记录层退役整体移除）：各层记录读写原语，
  一层一个文件组（单文件超 400 行时按功能拆分，命名
  `<layer>layer_<aspect>.go`：`l1layer_sync.go`、`l2layer_topic.go`），
  所有函数以 `agentID` 为域参数。存在一个保留域
  `core.SharedPoolAgentID`（文件级公共池：L3 知识图）：本层原语对它和普通域无差别
  （`agentID` 只是参数），路由与守卫都在 `internal` 根。L1 建边/遗忘算法已上提至
  `internal/cap/engram`（ DecayNetwork/RebuildFromL2/BuildHyperedges）、
  L0 画像生成/蒸馏合并至 `internal/cap/profile`、L3 匹配与节点合并至
  `internal/cap/knowledge`——本层只保留记录读写原语
  （如 `MutateNodeL3` 以回调接受调用方策略）与索引维护。

## 边界纪律

1. **域隔离**：所有读写必须携带 `agentID`；跨 agent 的联合查询/共享记忆
   不属于本层，禁止引入。同名记录在不同域内互不可见是正确行为。
2. **无业务语义**：本层不做业务判断（何时压缩、何时结晶、容量策略等一律
   由 `internal` 业务层决定），不调用 LLM。
3. **原语必须有活调用方**：本层导出函数不得为"将来可能用到"预留；
   模式类参数必须
   具名或结构化——L4 查询收为 `ArchiveQuery`（填了的字段之间 AND，含
   `Kind`/`Limit`；`Limit` 保 Seq 最高 N 条；`Keyword` 两边 lowercase，与 L3
   节点过滤一致），
   L2 批量删除用 `DeleteScenesL2` / `DeleteTopicsL2`，不再往调用点传裸
   `1/2/3`。
3.1 **一个 id 只对应一种记录**：`core` 的 typed reader 一律带期望的
   `Rec*`（`readJSON` 比对帧内类型，不符即 `ErrNotFound`）。丢掉这个校验
   会让 `UpdateL3(节点 id)` 读到"空名图槽"再把节点记录改写成图槽。
3.2 **L3 超边身份 = 排序成员 + kind**：`CreateEdgeL3` 的 id 含 kind，
   `EdgeKeyL3` 是同一身份的语义键，导入侧按它去重。记录里的
   `Importance`/`Weight`/`Label` 无写入路径，故意不进公开 DTO。
4. **实现不外露**：记录帧布局、快照格式、回收/压缩细节只在 `core` 内部
   流转；`internal` 业务层只能经本目录导出的函数访问数据，不得直接解析
   帧或操作 `StorageEngine` 未导出的状态。
5. **单向依赖**：`repo -> repo/core`、`repo/index -> repo/core`、
   `repo -> common`；禁止反向依赖 `internal`、`api`、`cmd`。
6. **默认域**：`core.DefaultAgentID = 0` 即全零 hex 域，公开 `Session("0000000000000000")` 可绑定；
   注册记录 `RecAgentRegistry (0x10)` 的 `idHash == agentID`，data 为
   agent 名 JSON，Open 时扫描重建 `name -> agentID` 映射。

## 修改者义务

改动本层导出签名、帧/快照格式或域语义时，必须同步更新本文件与
`internal/agent.md` 中受影响的条目，并保证 `go vet ./...` 与
`grep -rn 'L7\|RecL7' --include='*.go'` 零残留。

<!-- 2026-09-04 接口去 fallback 与按层闭环修复 -->
- `EnsureGraphL3`：槽存在就复用其 id、不覆写记录；`CreateGraphL3` 是无条件写槽，只用于确认不存在时。
- L2/L4 读路径的错误策略：只有 `CodeOf(err)==ErrNotFound` 才跳过那一条，其余（IO/关闭/损坏）一律返回 error——宿主分不清「少一条」和「没有这一条」。`ListScenesL2`/`CollectAllScenesL2`/`QueryArchivesL4` 因此都带 error 返回。
- L4 的两种读判据不同：**按 id 读**时不存在的 id 可以跳过（已墓碑的、或本来就不是本域的 id 都只是「选不中」），**按话题索引读**时索引点名却读不到就是镜像与磁盘不一致，必须 `ErrIO` 而不是少给一条对话。「槽位被保留窗回收」不属于任何一种：那次清扫同时摘掉索引条目，读侧看到的是 `Seq` 上的一个空洞，由 `SceneMessage.Seq` 暴露给宿主判别。
- `Kind` 是**条件**而不是模式：`ArchiveQuery.Kind == nil` 表示「两种都要」，非 nil 表示「只要这一种」。它必须在每一条读路径上都生效，包括只给 id 的那条快路径——快路径绕过过滤谓词就是这个条件最容易静默失灵的地方（`TestSearchL4KindCondition` 的 `events, by id` 分支专门盯它）。
- L4 原语的签名带着归属信息：`AppendArchiveL4(engine, agentID, idx, ArchiveContent)` 以 `core.HashContent(TopicID, Seq)` 发号、落盘后同步 `index.L4Index`。写同一个 (话题, Seq) 是**原地覆写**而不是追加第二条——这就是重放一轮能收敛的机制，本层因此没有也不需要「先列出这个话题旧有的归档、再删掉没被重写的那几条」这类原语（第二真相的活形式）。
- 删除只有两条入口，都内置「磁盘删成功后才摘镜像」这一步序：带话题的 `DeleteTopicArchives`（整话题连删带摘）、按保留窗的 `DropExpiredArchives`（先 `ExpiredBefore` 只读地拿 id，删成后逐话题 `RemoveIDs`）。`TopicClosureL2` 只返回话题闭包——内容由闭包里的每个 id 去索引取回。
- L6 只剩计划节点（`l6layer.go`）：一个节点一条记录，键是开出这一轮的话题 id。`CollectPlanNodes` 按它分组，`PlanAggregate` 只带 `{TopicID, Nodes, LastActiveAt, HasNonDone}`——没有事件计数、没有事件清单：事件在 L4，树不拥有它们。`WritePlanNode` 校验 `IDHash == HashPlanNode(TopicID, NodePath)`：节点身份是派生的，本层不接受调用方自备的第二把键。删除有 `DeletePlanNodesByIDs`（Dream 保留窗按 id 批删）与 `DeletePlanNodesByTopicIDs`（删话题/场景时连它的树一起走，靠扫节点桶按 `TopicID` 过滤——树没有内容侧那样的位置键可推）。本层没有「删某分支」的原语：作废发生在键上，不在记录上。
