# internal — 组合根 + 大方法层（模块级 agent 上下文）

本包是 MemHop 的**组合根 + 大方法层**。任何 AI agent 或开发者修改本层前
必须先读完本文件，修改后必须同步更新本文件。

## 四层分工（本仓库的纵向契约）

```
api/            对外门面：纯透传 + DTO 映射，禁止业务逻辑
internal/ 根    大方法层：接收 api 透传，每个大方法 = 拿域锁 + 组装小方法
                + 组合根装配（Open/DB/Session/agents/exports/models）
internal/{domain,scene,turn,dream,graph,plan,trajectory}
                小方法包：每个小方法只组装功能（repo/core 记录读写、
                cap 纯计算、llmops 提示契约），不自己拿域锁
内部底座        internal/{config,llm} 配置类型与 LLM 传输；
                internal/cap 纯功能；internal/repo(+core) 连数据库内核的功能层
```

- 大方法（`Search`/`Update`/`RunDream`/`Crystallize`/L0-L6 各面/
  `CreateAgent` 等）只做：`db.lockAgent` 取域 → 顺序调小方法 → 组装返回。
  细节逻辑（循环、重试、缓存维护、ID 铸造、回滚）一律在小方法包。
- 小方法包之间互不 import（`plan` 读 `trajectory.MaxEventPayload` 是仅有
  的常量级例外）；需要交互时回到根的大方法组装。
- 依赖方向单向：`根 -> 小方法包 -> {domain, cap, repo, llm} -> repo/core,index
  -> common`，禁止反向。

## 小方法包职责

| 包 | 职责 |
|---|---|
| `domain` | 域状态容器 `Context`（Mu/L2Meta/Traj/Plans/DreamInFlight/OpCtx，持 Engine/LLM/Defaults 注入）+ PlanCache + L2Meta 缓存维护（SyncL2Meta/RemoveTopicsFromIndices/RetargetL2Meta） |
| `scene` | L2 场景读写面：ResolveForRead/Create/FreshID/OpenTurn/SurfaceTopics/ContextTopic/PruneParentChild/DeleteTopics |
| `turn` | 轮次沉淀：Targets 校验、SettleTarget（可沉淀的轮次范围）、PriorL4Refs、WriteArchives、DropRetained、ReadProfile |
| `dream` | 巩固阶段：SceneSet、PruneTrajectoryStage(TrajectoryRetention)、CompressScenes(+组回滚)、StructureStages、L1 各阶段、DistillL0Stage、usage feedback；调参常量随阶段在此 |
| `graph` | L3 导入/查询：`ImportBatch`（一次批次的 mode + result + 三张缓存，方法 ImportNode/ImportRelations/GraphIDs）、NodeFilter.Matches/ResolveSubgraphStart/SubgraphAdjacency/BfsWithinDepth/AllNodesVisited |
| `plan` | L6 计划树机制：PlanStatus 面、MintID/ParsePlanID/SplitNodePath、EnsureNode/AppendEventLocked/UpdateNode(Locked/SummaryLocked)、BuildTree/RollupTree、SyncNodeLocked/CollectPaths/ParentPath |
| `trajectory` | 轨迹/结晶：ReadTurn、TrimByBudget、MaxEventPayload/MaxCrystallizePayload、ApplyCandidate(+apply/find 私有步) |

## agentContext（domain.Context）域级锁纪律

1. **先域锁后存储**：所有大方法统一走 `db.lockAgent(agentID)`（内部：
   `contextFor` 取域 + `ac.Mu.Lock()` + 锁内复检 `Deleted` 墓碑），再调小
   方法；引擎自带的锁在内层，顺序不可颠倒。同 agent 串行、跨 agent 并行。
   `contextFor` 对非默认域校验注册表：未注册/已删除的 agentID 直接
   `ErrAgentNotFound`，域永不复活；与删除对撞的陈旧句柄由锁内墓碑复检拒绝。
   L6 轨迹族统一走 `db.lockSession(agentID, turnID)`（lockAgent + hex 解析，
   解析失败先解锁）：裸事件的键就是该轮话题 ID（`AppendTrajectory` 顺手把
   `TopicID` 写成同一个值），计划绑定事件的键是计划 ID。门面侧的会话准入
   策略在 `CheckSession`。
2. **缓存刷新序**：写记录帧后紧跟 `ac.SyncL2Meta`（**存储 -> l2meta**）。
   **禁止在域锁内取 `db.agentsMu`**（锁序环：sweep 走 agentsMu -> ac.Mu），
   域内簿记（如 `lastDreamAt`）直接写 atomic 字段。
3. **Dream 域化**：`RunDream` 全程持本域锁；后台触发经
   `triggerSceneDream(ac, sceneID)`（调用方持 `ac.Mu`，留在根里因为它管理
   goroutine 生命周期），goroutine 运行在 `ac.OpCtx` 下——
   `Close`/`DeleteAgent`/空闲回收取消它，任何在飞 Dream 在下一阶段边界退出，
   绝不写入已销毁的域。域锁内的前台 LLM 调用（`Update` 的轮次提炼）同样挂
   `ac.OpCtx`，避免生命周期屏障被一次完整往返阻塞。
4. **空闲回收**：无后台定时器；`contextFor` 顺带清扫超
   `Defaults.AgentIdleTTLMs` 未访问的域（默认域豁免），回收前先对域锁
   `TryLock`：锁被占用（在飞操作）或 `dreamInFlight` 非空则跳过，留待下轮。
   回收时不快照任何东西：L2Meta 在下次访问时从记录重建，数据始终在文件里。
5. **DeleteAgent 顺序**：先摘租户映射（断绝新 `contextFor`）→
   `destroyContext`（取消 `ac.OpCtx`）→ `ac.Deleted` 墓碑（`lockAgent` 拿锁后
   复检，与删除对撞的在飞操作被拒）→ `ac.Mu` 屏障等待在飞操作 → 引擎域删除。
6. **planCache 域内索引**：L6 计划聚合缓存 `ac.Plans`（`domain` 包）
   **不内置锁**，完全依赖 `ac.Mu` 串行（区别于自带
   RWMutex 的 `TrajIndex`）。所有计划写路径（节点增删改、事件绑定、
   `PlanReplace`、`SyncPlanTree`、Dream 清理）必须先取 `ac.Mu` 再同步缓存；
   `domain.NewContext` 构建，idle 重建时一并重建。`SyncPlanTree` 整树同步
   只改节点结构/字段，**不产生 `plan_step` 事件**、不动事件 Seq 空间；但删掉
   vanished 分支时必须**两份缓存一起镜像**——`ac.Plans.RemoveNodeBranch` 与
   `ac.Traj.RemoveEvents`（`repo.DeletePlanNodeBranch` 返回它删掉的记录 id）。
   只镜像计划缓存的话，事件索引仍命名已删记录，该 plan key 之后每次
   `ReadTrajectory`/`Crystallize` 都报 `ErrIO`，要等重启从记录重建索引才自愈。
7. **planID 全零保留**：`AppendTrajectory` 写入的裸轮次事件恒为
   `PlanID=0`，故 `0000000000000000` 不是合法计划。计划入口
   （`AppendTrajectory` 带 nodePath 时/`PlanCommit`/`PlanState`/`PlanReplace`/`SyncPlanTree`）
   一律经 `plan.ParsePlanID` 拒绝；绕过它直接删会删掉整个域的全部轮次事件。
8. **计划清理有界**：dream 的 `l6_prune` 只豁免「持非 done 节点 **且** 窗口内
   仍有活动」的计划；宿主中断或放弃而静默超 `TrajectoryRetention` 的计划
   照常清理并级联其绑定事件，否则废弃计划会让 L6 无界增长。

## 数据访问纪律

- 只经 `internal/repo`（及 `repo/core` 导出的 Slot 读写）访问数据；
  **禁止**直接操作帧、文件头、快照结构。
- `StorageEngine` 句柄由装配层 `config.go` 的 `Open(cfg, builtins)`
  唯一持有：注入 `DB.engine`，并经 `domain.NewContext` 注入每个域；业务代码
  不得自行打开/关闭引擎。内建能力工具箱由 `api` 门面以 `fs.FS` 注入
  （api 传 `capabilities.FS`），`Open` 负责解析并 attach——internal 不得
  import `capabilities`。
- **能力下沉**：算法与策略在 `internal/cap/<feature>` 能力包；小方法在
  `internal/{scene,turn,dream,graph,plan,trajectory}`；根只留"取数 → 调
  能力 → 落库"的大方法编排，不做算法。LLM 传输策略（截断升级重试）在
  `internal/llm` 的 `Provider.ChatWithRetry`，prompt 构建属于 `cap/llmops`。
- 新增功能时：先问属于哪一层——记录读写进 `repo`、纯算法进 `cap`、
  带域状态的编排步进小方法包、大方法才进根。共享 DTO 下沉
  `repo/core/model_dto.go`，根以恒等别名引用（`models.go`）；配置类型在
  `internal/config`，同样经 `exports.go` 恒等别名暴露给 api。
- **会话面（session.go）**：`Session` 是绑定单个 agent 域的唯一对外操作
  入口；`api.Session` 内嵌本类型，api 侧禁止出现业务逻辑、
  格式化或域绑定代码。多 agent 是唯一模式：`NewSession(agentID)` 是唯一
  会话构造器。`exports.go` 是给 api 门面的恒等再导出接缝；`api` 包禁止
  直接 import `repo/core` 或 `common`。
- LLM 客户端是 DB 级共享（`db.llm`，`internal/llm.New(cfg)`），由 `Open`
  在装配时构造并注入每个域上下文（`ac.LLM`）；任何域不自己建客户端。

- **错误判定纪律**：区分「记录不存在」与「读不动」。`ErrNotFound` 只代表
  前者；IO / 关闭 / 反序列化失败一律原样上抛，不得改写成 `ErrNotFound`，
  也不得当成"不存在"后继续写（`plan.EnsureNode`、`trajectory.findTarget`、
  `profile.MergeDistill` 都按这条判定，误判会让活节点退回 pending 或画像
  被空值覆盖）。

## 读写路径契约（v1.5.0）

1. **一次 `Search` = 读场景 + 开一轮**：`scene_id` 为空 → `scene.FreshID`
   铸一个未被占用的 ID（`0` 跳过；只有 `ErrNotFound` 才算可用，其他读错误
   原样上抛）并落场景记录（名字一律库生成 `session:<id>`）；非空且不存在 →
   `ErrNotFound`。同一批调用还经 `scene.OpenTurn`（`repo.OpenSceneTurn`）把
   场景的 `TurnSeq` 推到下一轮，返回值 `NewTopicID = hash("turn:" +
   场景:TurnSeq)` 就是本轮要沉淀进去的话题。`Update` 一律拒绝未知场景，
   库内不再猜场景。画像读取失败同样使本次读取失败（仅"画像尚未建立"按空
   画像继续），不再静默返回缺 L0 的上下文。
2. **`Search` 零 LLM、零话题写入**：唯一写是那一行场景记录（命中计数 + 轮次
   计数，写失败即报错——轮次号是铸 ID 的依据，不能吞），话题从 `ac.L2Meta`
   取 depth-1；`Scene.TopicCount` 用这批话题现算（该字段不落盘）。开了没
   沉淀的轮次不留任何残渣，读两次只沉淀一次就是跳号。
3. **`Update` 每轮一次提炼且排在写入前**：`ExtractTurnKeywords` 失败或空
   结果直接报错，此时话题/档案/L2Meta 一个字都没动。话题 ID 由宿主从
   `Search` 原样带回（`TopicID`，`0`/非 hex 拒绝），档案 ID 由
   `(topic, ts, content)` 派生，故同 `TopicID` 重放是覆盖而不是叠加：
   **重写前先读回该话题的旧引用（`turn.PriorL4Refs`），落完新引用后把不再
   被引用的旧档案打墓碑（`turn.DropRetained`）**，所以"一轮恰好两条原文"
   在改写文本的重放下也成立。轮内过程走 L6 轨迹。
   `turn.SettleTarget` 另外钉住可沉淀的范围：`TopicID` 必须是
   `hash("turn:" + 场景:k)` 且 `k <= 场景.TurnSeq`，即该场景真开出过的某一轮
   ——写 Dream 融合节点（同 depth、同场景，但由时间戳派生）、跨场景 id、宿主
   自造 id 都在 LLM 调用之前被拒，零留痕。重放当前轮与"先开两轮再乱序结算"
   仍然合法（`TestUpdateSettlesEachScenesTurnsInOrder`）。
4. **巩固按单场景规模触发**：`consolidateScene` 在 depth-1 话题数超
   `Defaults.SceneDreamTopicThreshold` 时调度该场景 Dream；单个融合组是
   "摘要档案 → 提炼关键词 → 建父话题 → 挂引用 → 下沉子话题"的串写，任一步
   失败都回滚本组已写的记录（`dream.discardFusedGroup`）——要么整体生效，
   要么不留孤儿档案 / 半成品父节点。
5. **L4 内容类型只在 `Update` 声明并在其边界校验**：两侧档案按
   `TurnUpdate.UserType` / `AgentType` 落类型（零值 `ContentText`，非文本
   侧存路径/URL），未定义值以 `ErrInvalidQuery` 拒绝，Dream 的融合摘要恒为
   `text`。
6. **`UpdateScene` 是 `SceneName` 的唯一宿主写者**：场景记录只被 `OpenSceneTurn`
   读改写（它回填整条记录、只动计数），Dream 从不写场景记录，故改名不会被
   后续读取覆盖；`scene.Create` 建新场景时才写默认名 `session:<id>`。
7. **`L4Refs` 无对话顺序，读回面自己补**：`UpdateTopicL4RefsL2` 按 id
   `DedupSorted` 存引用。`scene.ContextTopic` 因此经内部 `sortMessages`
   稳定排序 `Messages`——先按档案时间戳，**同毫秒再按 Role（`RoleUser` 在
   `RoleAgent` 之前）**；会话恢复必须"问在前、答在后"，别指望引用顺序。
8. **L0 画像字段所有权在库内强制**：`UpdateL0` 只写宿主四项
   （Name/Role/Personality/Preferences），`EmotionState`/`MBTI` 一律从库里
   现值继承（只有它们的首次建立走蒸馏路径），`UpdatedAtMs` 由库戳写、不采信
   调用方传值；api 侧的入站映射也不搬运这三项。`MergeDistill` 是反过来只写
   蒸馏项。
9. **L3 的 id 与边身份**：`core.readJSON` 校验帧内记录类型，种类不符即
   `ErrNotFound`（`UpdateL3(节点 id)` 曾把节点记录改写成图槽）；`CreateEdgeL3`
   的 id 含 kind，导入按「排序成员 + kind」的语义键去重，故同一对节点可并存
   多种关系，且对旧边的 pair-only 哈希同样幂等。`ImportL3` 结果带 `GraphIDs`
   （图 id = `hash(Domain)`，没有别的公开调用能渲染它）；`DeleteL3Nodes`
   做节点级删除并级联其超边。
10. **L6 事件写入的字段归属**：两条追加路径（`appendTurnEvent` /
   `plan.AppendEventLocked`）把记录强制成裸事件形状——`NodeType`/`PlanID`/
   `ParentID`/`NodePath`/`Status`/`Summary`/`PlanType` 一律清零（`PlanType`
   按记录契约只属于计划节点），`Seq`/会话 id 由库赋值，`Payload` 超
   `trajectory.MaxEventPayload` 即拒绝（不截断：截短的事件读起来和完整的一样）。
   轨迹只按 key 整体寻址：公开面上没有任何
   调用接受单条事件 id，所以写入不返回句柄（加了就是一桩没人消费的新契约）。
11. **`MultiAgentDB.CompactTo`**：core 的 `Compact` 用 `Create`（带
   `O_TRUNC`）在新路径写整理副本，故根层先拒空路径、拒当前库文件
   （`sameFile` 走绝对路径归一）与拒已存在的目标，绝不覆盖任何既有文件。
12. **`SyncPlanTree` 未填即继承**：快照里空白的 Title/PlanType/Status/Summary
   继承节点现值（空 Status 不再退回 pending、空 Summary 不再清空折叠结论），
   宿主推部分快照不必先读旧树；显式传入的值仍然覆盖。
13. **破坏性写入先验 id**：`MergeScenes` 会删记录，所以主/次每个 id 都必须
   仍是一个场景（`requireScenes` 逐个回读比对），未知 id 报 `ErrNotFound`；
   底层 `DeleteL2(DeleteScenesL2)` 直接按传入 id 批量删，少这一步时一个陈旧
   的 secondary id 就能带走存活主场景自己的记录，而调用还返回成功。
   `DeleteCapability` 同理由先读后删，`DeleteAgent` 也先查注册表（注册表不认识
   的 id 正是 `CheckSession` 拒的那个 id）——至此删除面没有一处把「记录不在」
   当成成功返回。
14. **`SceneContext` 的说话顺序是读出来的语义**：融合父话题的时间戳就是它吞掉
   的第一轮的 `UserTimestamp`，两者必然同值，所以排序在时间戳之后加
   `Depth` 次键（浅的在前）。只按时间戳排时 `slices.SortFunc` 不稳定，一组的
   摘要会随机落到它所总结的原文中间。

## 修改者义务

改动锁纪律、Dream 阶段划分或域生命周期时，必须同步更新本文件与
`internal/repo/agent.md` 中受影响的条目；在小方法包里改动契约时，同步该包
自己的 `agent.md`。

<!-- 2026-09-04 接口去 fallback 与按层闭环修复 -->
- `Open` 不再初始化分词器：关键词提炼的启发式兜底已删除（不可解析即 `ErrLLM`），`index.Tokenize` 失去唯一生产读者，`index/tokenizer.go`、`internal/tuning.go`、`common.TruncateUTF8` 随之下线，直接依赖 5 → 4。
- `ImportL3` 的批校验在 composition root 完成（Title/Domain 必填、mode 不接受空值），拒批即一字节不写；`result.Errors` 从此只表示单条存储失败。
- 宿主面测试补到 34 个会话方法 + 8 个 `MultiAgentDB` 方法全覆盖，按层分文件：`test/api_interface_scene_test.go`（L2 场景生命周期）、`api_interface_plan_test.go`（L6 计划树三形态 + 轨迹双键）、`api_interface_capability_test.go`（L5 生命周期/重导入/内建只读/重启）、`api_interface_multi_test.go`（租户隔离与 `CompactTo`）。这些用例只使用库铸造并回传给宿主的 id——`api_interface_l5l6_test.go` 里手打的轮键已换掉。
