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
| `domain` | 域状态容器 `Context`（Mu/L2Meta/Arch/Traj/Plans/DreamInFlight/OpCtx，持 Engine/LLM/Defaults 注入）+ PlanCache + L2Meta 缓存维护（SyncL2Meta/RemoveTopicsFromIndices/RetargetL2Meta）；`Arch` 是「话题 → 它名下的 L4 归档」的镜像 |
| `scene` | L2 场景读写面：ResolveForRead/Create/FreshID/OpenTurn/SurfaceTopics/ContextTopic/PruneParentChild/DeleteTopics |
| `turn` | 轮次沉淀：Targets 校验、SettleTarget（可沉淀的轮次范围）、PriorArchives（本话题已拥有的归档，走 `ac.Arch`）、WriteArchives、DropRetained、ReadProfile |
| `dream` | 巩固阶段：SceneSet、PruneTrajectoryStage(TrajectoryRetention)、CompressScenes(+组回滚)、StructureStages、L1 各阶段、DistillL0Stage、usage feedback；调参常量随阶段在此 |
| `graph` | L3 导入/查询：`ImportBatch`（一次批次的 mode + result + 三张缓存，方法 ImportNode/ImportRelations/GraphIDs）、NodeFilter.Matches/ResolveSubgraphStart/SubgraphAdjacency/BfsWithinDepth/AllNodesVisited |
| `plan` | L6 计划树机制（一棵树归属于打开它的轮次）：PlanStatus 面、SplitNodePath、EnsureNode/AppendEventLocked/UpdateNode(Locked/SummaryLocked)、BuildTree/RollupTree |
| `trajectory` | L6 键与读取：ParseTopicID（全键的解析与拒零，读写两侧共用）、ReadTurn、TrimByBudget、MaxEventPayload/MaxCrystallizePayload（payload 预算） |

## agentContext（domain.Context）域级锁纪律

1. **先域锁后存储**：所有大方法统一走 `db.lockAgent(agentID)`（内部：
   `contextFor` 取域 + `ac.Mu.Lock()` + 锁内复检 `Deleted` 墓碑），再调小
   方法；引擎自带的锁在内层，顺序不可颠倒。同 agent 串行、跨 agent 并行。
   `contextFor` 对非默认域校验注册表：未注册/已删除的 agentID 直接
   `ErrAgentNotFound`，域永不复活；与删除对撞的陈旧句柄由锁内墓碑复检拒绝。
   L6 族统一走 `db.lockSession(agentID, turnID)`（lockAgent +
   `trajectory.ParseTopicID`，解析失败先解锁）：L6 只有一个键——本轮话题 ID，
   该轮的事件与它开出的计划节点同住，一次 `ReadTrajectory(topic)` 两者齐。
   门面侧的会话准入策略在 `CheckSession`。L3 的方法是唯一例外：走
   `db.lockSharedPool(callerID)`——先 `CheckSession` 校验调用方域活着，再锁
   保留公共域 `core.SharedPoolAgentID`（L3 记录全部住该域，跨 agent
   全局串行；公共域无墓碑、免空闲回收）。锚点校验（`scene.Create`/
   `ResolveForRead`/`UpdateScene`）持调用方锁无锁读公共域记录，由引擎级
   互斥兜底。
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
   `Defaults.AgentIdleTTLMs` 未访问的域（默认域与共享 L3 域豁免），回收前先对域锁
   `TryLock`：锁被占用（在飞操作）或 `dreamInFlight` 非空则跳过，留待下轮。
   回收时不快照任何东西：L2Meta 在下次访问时从记录重建，数据始终在文件里。
5. **DeleteAgent 顺序**：先摘租户映射（断绝新 `contextFor`）→
   `destroyContext`（取消 `ac.OpCtx`）→ `ac.Deleted` 墓碑（`lockAgent` 拿锁后
   复检，与删除对撞的在飞操作被拒）→ `ac.Mu` 屏障等待在飞操作 → 引擎域删除。
6. **planCache 域内索引**：L6 计划聚合缓存 `ac.Plans`（`domain` 包）
   **不内置锁**，完全依赖 `ac.Mu` 串行（区别于自带
   RWMutex 的 `TrajIndex`）。所有计划写路径（节点增删改、事件绑定、
   Dream 清理）必须先取 `ac.Mu` 再同步缓存；`domain.NewContext` 构建，
   idle 重建时一并重建。**一个键算不算一棵活树的判据是「键下还有节点」**——
   `repo.CollectPlanAggregates` 与 `detachIfEmpty` 用同一条，裸轮次事件不引用
   节点因此不成树。任何删记录的路径都要**两份缓存一起镜像**（`ac.Plans` 与
   `ac.Traj`）：只镜像一边的话，事件索引仍命名已删记录，该键之后每次
   `ReadTrajectory`/`Crystallize` 都报 `ErrIO`，要等重启从记录重建索引才自愈。
7. **L6 键全零保留**：`0` 是每条记录未赋键时的值，故 `0000000000000000` 不是
   合法的 L6 键。读写两侧一律经 `trajectory.ParseTopicID` 拒它
   （`AppendTrajectory`/`ReadTrajectory`/`PlanCommit`/`PlanState`/
   `Crystallize`）——只在写侧拒，全零键下就会攒出永远读不出的记录。
8. **计划清理有界**：dream 的 `l6_prune` 只豁免「持非 done 节点 **且** 窗口内
   仍有活动」的计划；宿主中断或放弃而静默超 `TrajectoryRetention` 的计划
   照常清理并级联其绑定事件，否则废弃计划会让 L6 无界增长。

## 数据访问纪律

- 只经 `internal/repo`（及 `repo/core` 导出的 Slot 读写）访问数据；
  **禁止**直接操作帧、文件头、快照结构。
- `StorageEngine` 句柄由装配层 `config.go` 的 `Open(cfg)`
  唯一持有：注入 `DB.engine`，并经 `domain.NewContext` 注入每个域；业务代码
  不得自行打开/关闭引擎。Open 不做任何目录扫描或能力注入——能力卡是宿主
  自有的磁盘文档（目录即能力），库只提供 v4 解析校验导出。
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
  也不得当成"不存在"后继续写（`plan.EnsureNode`、
  `profile.MergeDistill` 都按这条判定，误判会让活节点退回 pending 或画像
  被空值覆盖）。

## 读写路径契约

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
   **重写前先问 `ac.Arch` 该话题已拥有哪些归档（`turn.PriorArchives`），落完
   新归档后把本次没再写出的那些打墓碑（`turn.DropRetained` +
   `repo.DropArchivesL4`，磁盘删成功才摘镜像）**，所以"一轮恰好两条原文"
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
7. **归档靠话题 id 被寻址，镜像必须跟着删**：一条归档的归属是它的
   `ContextID`，话题不列举任何东西；档案 ID 哈希了 `(topic, ts, content)`，
   从话题 id **推不出地址**，所以「这个话题有哪些归档」唯一的来源是域内的
   `ac.Arch`——它是寻址手段，不是加速器。由此得出与 L6 两份镜像同一条纪律：
   任何删归档的路径都必须在**磁盘删成功后**同步摘镜像
   （`repo.DropArchivesL4` / `repo.DeleteTopicArchives` 已内置这一步），漏一处
   就让该话题之后每次读都撞「索引点名已不存在的记录」而硬 `ErrIO`；索引在
   `domain.NewContext` 从记录重建，故重启自愈、运行期不自愈。读回顺序仍由
   `scene.ContextTopic` 经 `sortMessages` 稳定排序——先按档案时间戳，**同毫秒
   再按 Role（`RoleUser` 在 `RoleAgent` 之前）**：索引给出的是创建序，不保证
   谁先说话，会话恢复必须"问在前、答在后"。
8. **L0 画像字段所有权在库内强制**：`UpdateL0` 只写宿主四项
   （Name/Role/Personality/Preferences），`EmotionState`/`MBTI` 一律从库里
   现值继承（只有它们的首次建立走蒸馏路径），`UpdatedAtMs` 由库戳写、不采信
   调用方传值；api 侧的入站映射也不搬运这三项。`MergeDistill` 是反过来只写
   蒸馏项。
9. **L3 的 id 与边身份**：`core.readJSON` 校验帧内记录类型，种类不符即
   `ErrNotFound`（否则 `UpdateL3(节点 id)` 会把节点记录改写成图槽）；
   `CreateEdgeL3` 的 id 含 kind，导入按「排序成员 + kind」的语义键去重，
   故同一对节点可并存多种关系。`ImportL3` 结果带 `GraphIDs`
   （图 id = `hash(Domain)`，没有别的公开调用能渲染它）；`DeleteL3Nodes`
   做节点级删除并级联其超边。全部 L3 记录住保留公共域
   `core.SharedPoolAgentID`（文件级公共池：`contextFor`/空闲回收/租户注册表
   三处豁免，`CreateAgent` 拒撞、`DeleteAgent` 拒删、`Session` 拒绑）。
   `DeleteL3` 两阶段：公共锁内删图，释放后遍历「默认域 + 注册表」逐域
   `lockAgent` 清锚（`detachGraphAnchors`），不嵌套双锁——代价是「删图后、
   清锚前」窗口内同名重导入（图 id = hash(Domain) 同 id）的锚点会被清成
   未锚定，可经 `UpdateScene` 重挂。
10. **L6 事件写入的字段归属**：两条追加路径（`appendTurnEvent` /
   `plan.AppendEventLocked`）把记录强制成裸事件形状——`NodeType`/`PlanID`/
   `ParentID`/`NodePath`/`Status`/`Summary`/`PlanType` 一律清零（`PlanType`
   按记录契约只属于计划节点），`Seq`/会话 id 由库赋值，`Payload` 超
   `trajectory.MaxEventPayload` 即拒绝（不截断：截短的事件读起来和完整的一样）。
   `EventType` 是宿主自定的步骤名，计划绑定事件与裸轮次事件同口径：引擎从不按它
   分支（只有 `ReadTrajectory` 原样回显与结晶 prompt 的一行格式化），唯一约束是
   非空，校验点只有 `trajectory.ValidateEvent` 一处。
   轨迹只按 key 整体寻址：公开面上没有任何
   调用接受单条事件 id，所以写入不返回句柄（加了就是一桩没人消费的新契约）。
11. **`MultiAgentDB.CompactTo`**：core 的 `Compact` 用 `Create`（带
   `O_TRUNC`）在新路径写整理副本，故根层先拒空路径、拒当前库文件
   （`sameFile` 走绝对路径归一）与拒已存在的目标，绝不覆盖任何既有文件。
12. **`PlanCommit` 未填即继承，路径即结构**：`plan.Step` 里空白的
   Title/PlanType/Summary 继承节点现值（空 Status 会被 `StatusToU8` 拒——状态是
   每次必须给的危害字段，不是"不改"），宿主推进一步不必先读旧树；显式传入的值
   仍然覆盖。`nodePath` 自己决定树形：`EnsureNode` 沿点号路径把缺失段一律建成
   pending，所以**打错一段路径会凭空多出一棵树**，而 L6 没有任何删节点入口
   （作废靠换轮次键，旧树由 `l6_prune` 的保留窗回收）——这是选「提交即追加」而
   弃「整树 diff 同步」时付出的代价，写进门面注释与 GUIDE 而不是留给宿主踩。
13. **破坏性写入先验 id**：`MergeScenes` 会删记录，所以主/次每个 id 都必须
   仍是一个场景（`requireScenes` 逐个回读比对），未知 id 报 `ErrNotFound`；
   底层 `DeleteL2(DeleteScenesL2)` 直接按传入 id 批量删，少这一步时一个陈旧
   的 secondary id 就能带走存活主场景自己的记录，而调用还返回成功。
   `DeleteAgent` 也先查注册表（注册表不认识
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

- 关键词提炼无本地兜底：LLM 输出不可解析即 `ErrLLM`（`Update` 那一轮不写），`internal` 根不初始化任何分词器。
- `ImportL3` 的批校验在 composition root 完成（Title/Domain 必填、mode 不接受空值），拒批即一字节不写；`result.Errors` 只表示单条存储失败。
- 宿主面测试覆盖 26 个会话方法 + 8 个 `MultiAgentDB` 方法，按层分文件：`test/api_interface_scene_test.go`（L2 场景生命周期）、`api_interface_plan_test.go`（L6 一轮一键的树、Model A 折叠与节点字段回读、轨迹键与纯提炼、重开后读回）、`api_interface_l5l6_test.go`（轨迹与纯结晶面）、`api_interface_multi_test.go`（租户隔离与 `CompactTo`）。这些用例只使用库铸造并回传给宿主的 id。
