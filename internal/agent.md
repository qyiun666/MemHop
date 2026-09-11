# internal — 组合根 + 大方法层（模块级 agent 上下文）

本包是 MemHop 的**组合根 + 大方法层**。任何 AI agent 或开发者修改本层前
必须先读完本文件，修改后必须同步更新本文件。

## 四层分工（本仓库的纵向契约）

```
api/            对外门面：纯透传 + DTO 映射，禁止业务逻辑
internal/ 根    大方法层：接收 api 透传，每个大方法 = 拿域锁 + 组装小方法
                + 组合根装配（Open/DB/Session/agents/exports/models）
internal/{domain,scene,turn,dream,graph,plan,content}
                小方法包：每个小方法只组装功能（repo/core 记录读写、
                cap 纯计算、llmops 提示契约），不自己拿域锁
内部底座        internal/{config,llm} 配置类型与 LLM 传输；
                internal/cap 纯功能；internal/repo(+core) 连数据库内核的功能层
```

- 大方法（`Search`/`Update`/`RunDream`/L0-L5 各面/
  `CreateAgent` 等）只做：`db.lockAgent` 取域 → 顺序调小方法 → 组装返回。
  细节逻辑（循环、重试、缓存维护、ID 铸造、回滚）一律在小方法包。
- 小方法包之间互不 import，一条都不例外（事件载荷预算原先由 `plan` 读
  `trajectory` 的常量，现在事件写入整体归 `content`，那个例外随之消失）；
  需要交互时回到根的大方法组装。
- 依赖方向单向：`根 -> 小方法包 -> {domain, cap, repo, llm} -> repo/core,index
  -> common`，禁止反向。

## 小方法包职责

| 包 | 职责 |
|---|---|
| `domain` | 域状态容器 `Context`（Mu/L2Meta/L4/Plans/DreamInFlight/OpCtx，持 Engine/LLM/Defaults 注入）+ PlanCache + L2Meta 缓存维护（SyncL2Meta/RemoveTopicsFromIndices/RetargetL2Meta）；`L4` 是「话题 → 它名下的内容槽位（原文 + 事件）」的镜像 |
| `scene` | L2 场景读写面：ResolveForRead/Create/FreshID/OpenTurn/SurfaceTopics/ContextTopic/PruneParentChild/DeleteTopics |
| `turn` | 轮次归属：Targets（解析 Update 的两个 hex 入参）、SettleTarget（可沉淀的轮次范围）、ReadProfile（Search 的 L0 读面）；本包不碰内容 |
| `dream` | 巩固阶段：SceneSet、PruneContentStage(`l4_prune`) 与 PrunePlanStage(`l5_prune`)（共用 `ContentRetention` 窗口、各读自己的时间戳）、CompressScenes(+组回滚)、StructureStages、L1 各阶段、DistillL0Stage；调参常量随阶段在此 |
| `graph` | L3 导入/查询：`ImportBatch`（一次批次的 mode + result + 三张缓存，方法 ImportNode/ImportRelations/GraphIDs）、NodeFilter.Matches/ResolveSubgraphStart/SubgraphAdjacency/BfsWithinDepth/AllNodesVisited |
| `plan` | L5 计划树机制（一棵树归属于打开它的轮次；L5 只剩节点记录）：PlanStatus 面（单张词表、双向都查它）、NodeSpec/Step 两个入参形状、CreateNode/UpdateNode/UpdateNodeSummaryLocked、BuildTree/Forest/ToNodeView/RollupTree |
| `content` | 话题内容与键：ParseTopicID（键的解析与拒零，读写两侧共用）、ValidateAppend（两种 Kind 各自的写入契约）、Append（写一条内容的唯一实现，必要时跨 Kind 分配 Seq）、Read（按 Kind 读回）、RenderForDistill（把一个话题的原文渲染成提炼读的转录）、MaxEventPayload/MaxUtterancePayload |

## agentContext（domain.Context）域级锁纪律

1. **先域锁后存储**：所有大方法统一走 `db.lockAgent(agentID)`（内部：
   `contextFor` 取域 + `ac.Mu.Lock()` + 锁内复检库未关），再调小
   方法；引擎自带的锁在内层，顺序不可颠倒。同 agent 串行、跨 agent 并行。
   `contextFor` 对非默认域校验注册表：未注册的 agentID 直接
   `ErrAgentNotFound`。
   L5 族统一走 `db.lockSession(agentID, turnID)`（lockAgent +
   `content.ParseTopicID`，解析失败先解锁）：一个话题键同时寻址两样东西——
   它的内容（L4 的原文与事件，`SearchL4{TopicID, Kind}` 按 Kind 取）与它开出的
   计划树（L5 节点，`PlanState(topic)` 给树）。
   门面侧的会话准入策略在 `CheckSession`。L3 的方法是唯一例外：走
   `db.lockSharedPool(callerID)`——先 `CheckSession` 校验调用方域活着，再锁
   保留公共域 `core.SharedPoolAgentID`（L3 记录全部住该域，跨 agent
   全局串行；公共域免空闲回收）。锚点校验（`scene.Create`/
   `ResolveForRead`/`UpdateScene`）持调用方锁无锁读公共域记录，由引擎级
   互斥兜底。
2. **缓存刷新序**：写记录帧后紧跟 `ac.SyncL2Meta`（**存储 -> l2meta**）。
   **禁止在域锁内取 `db.agentsMu`**（锁序环：sweep 走 agentsMu -> ac.Mu），
   域内簿记（如 `lastDreamAt`）直接写 atomic 字段。
3. **Dream 域化**：`RunDream` 全程持本域锁；后台触发经
   `triggerSceneDream(ac, sceneID)`（调用方持 `ac.Mu`，留在根里因为它管理
   goroutine 生命周期），goroutine 运行在 `ac.OpCtx` 下——
   `Close` 与空闲回收取消它，任何在飞 Dream 在下一阶段边界退出，
   不会把生命周期屏障堵在一次完整 LLM 往返上。域锁内的前台 LLM 调用（`Update` 的轮次提炼）同样挂
   `ac.OpCtx`，避免生命周期屏障被一次完整往返阻塞。
4. **空闲回收**：无后台定时器；`contextFor` 顺带清扫超
   `Defaults.AgentIdleTTLMs` 未访问的域（默认域与共享 L3 域豁免），回收前先对域锁
   `TryLock`：锁被占用（在飞操作）或 `dreamInFlight` 非空则跳过，留待下轮。
   回收时不快照任何东西：L2Meta 在下次访问时从记录重建，数据始终在文件里。
5. **planCache 域内索引**：L5 计划聚合缓存 `ac.Plans`（`domain` 包）
   **不内置锁**，完全依赖 `ac.Mu` 串行（区别于自带 RWMutex 的 `L4Index`）。
   所有计划写路径（节点增删改、Dream 清理）必须先取 `ac.Mu` 再同步缓存；
   `domain.NewContext` 构建，idle 重建时一并重建。**一个键算不算一棵活树的
   判据是「键下还有节点」**——`repo.CollectPlanNodes` 与 `PlanCache` 用同一条，
   事件不再进这张缓存。每份镜像各有一个属主，不要交叉补写：`ac.Plans` 由
   `RemoveTopicsFromIndices` 与 `l5_prune` 摘，`ac.L4` 由删内容的那条路径
   （`DeleteTopicArchives` / `DropExpiredArchives`）在**磁盘删成功后**摘。
   漏摘 `Plans` 与漏摘 `L4` 的代价不对称：后者让该话题每次读都报 `ErrIO`
   直到重启重建索引，前者留下一条陈旧的 `LastActiveAt` 让死树长期豁免清扫。
6. **L5 键全零保留**：`0` 是每条记录未赋键时的值，故 `0000000000000000` 不是
   合法的 L5 键。读写两侧一律经 `content.ParseTopicID` 拒它
   （`AppendArchive`/`PlanCreate`/`PlanNodeAdd`/`PlanNodeUpdate`/`PlanState`）
   ——只在写侧拒，
   全零键下就会攒出永远读不出的记录。
7. **计划清理有界**：dream 的 `l5_prune` 只豁免「持非 done 节点 **且** 窗口内
   仍有节点活动」的计划，其中活动只看节点自己的 `UpdatedAt`；宿主中断或放弃而
   静默超 `ContentRetention` 的计划照常清理。豁免保住的是**整棵活树**（含早已
   不更新的 done 父节点），不是「有事件在写所以树还活着」——事件住在 L4，
   不参与这个判断，也不再被节点的清扫带走。

## 数据访问纪律

- 只经 `internal/repo`（及 `repo/core` 导出的 Slot 读写）访问数据；
  **禁止**直接操作帧、文件头、快照结构。
- `StorageEngine` 句柄由装配层 `config.go` 唯一持有：注入 `DB.engine`，并经
  `domain.NewContext` 注入每个域；业务代码不得自行打开/关闭引擎。
  **唯一入口是 `OpenDB(path, llm, defaults, primary)`**：先用 `openEngine` 做三态
  判定（只在自己带了主域画像时才允许建文件），再 `assemble` 装配，最后按主域画像
  在不在落定 a/b/e 三条规则。**先校验后建文件**——被拒的打开不在宿主的路径上留
  任何东西。**建文件是带截断的**，所以 `openEngine` 只在
  `errors.Is(err, os.ErrNotExist)` 时才走创建分支，其它 stat 失败一律上报；路径是
  目录时显式拒绝，否则 `core.Open` 会回一句误导的「文件太小放不下双头」。
- **域身份两个入口**：`Primary()` 返回零号域（一个文件恰好一个主域，无需扫描），
  `SubAgent(llm, profile)` 按 `profile.Name` 幂等建/取一个注册域并挂上它自己的
  LLM 端点。`SubAgent` 的顺序是硬约束：注册（`agentsMu`）→ 挂端点（`agentsMu`）
  → 取会话句柄（`CheckSession` 读注册表，`agentsMu`）→ **最后**才 `lockAgent`
  拿域锁写画像。反过来就是 `ac.Mu` 之下取 `agentsMu`，正是本文件域锁纪律第 2 条
  禁止的那个环。写画像用 ensure 语义（已有就不动），所以注册记录写完、画像没写完
  就崩的情况下，同名再调一次会把画像补上。
- **能力下沉**：算法与策略在 `internal/cap/<feature>` 能力包；小方法在
  `internal/{scene,turn,dream,graph,plan,content}`；根只留"取数 → 调
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
- LLM 客户端由装配函数 `assemble` 用 `internal/llm.New(cfg.LLM)` 构造（`db.llm`，
  两个入口共用），经 `domain.NewContext` 注入每个域上下文的 `ac.LLM`；域不自己建
  客户端。一个域可以有自己的端点：`db.llmByAgent` 是覆盖表，`db.providers` 按
  `LlmConfig` 值去重（上百个租户共用一个端点时只有一个 http.Client）。**两张表都
  刻意比域上下文活得久**——空闲回收丢掉上下文后 `contextFor` 会重建它，覆盖若挂在
  上下文上，一个闲置超过 TTL 的域会静默退回库级端点。
  **所有 LLM 调用点一律读 `ac.LLM`，不读 `db.llm`**——库级那一个只是注入的默认
  值，绕过注入位的调用点在「每个域同一个端点」时看不出问题，只在某个域带自己的
  端点时才显形，而且显形成同一个域的两类调用打到两个端点。

- **错误判定纪律**：区分「记录不存在」与「读不动」。`ErrNotFound` 只代表
  前者；IO / 关闭 / 反序列化失败一律原样上抛，不得改写成 `ErrNotFound`，
  也不得当成"不存在"后继续写（`content.Read`、`profile.MergeDistill` 都按这条判定，
  误判会让一次读不动被报成「这条记录没有」，或画像被空值覆盖）。

## 读写路径契约

1. **一次 `Search` = 读场景 + 开一轮**：`scene_id` 为空 → `scene.FreshID`
   铸一个未被占用的 ID（`0` 跳过；只有 `ErrNotFound` 才算可用，其他读错误
   原样上抛）并落场景记录（名字一律库生成 `session:<id>`）；非空且不存在 →
   `ErrNotFound`。同一批调用还经 `scene.OpenTurn`（`repo.OpenSceneTurn`）把
   场景的 `TurnSeq` 推到下一轮，返回值 `NewTopicID = hash("turn:" +
   场景:TurnSeq)` 就是本轮要沉淀进去的话题。`Update` 一律拒绝未知场景，
   库内不再猜场景。画像读取失败同样使本次读取失败（仅"画像尚未建立"按空
   画像继续），不再静默返回缺 L0 的上下文。
2. **`Search` 零 LLM、零话题写入**：唯一写是那一行场景记录（轮次计数，
   写失败即报错——轮次号是铸 ID 的依据，不能吞），话题从 `ac.L2Meta` 取
   depth-1。开了没沉淀的轮次不留任何残渣，读两次只沉淀一次就是跳号。
3. **`Update` 每轮一次提炼，且排在话题写入前**：它读该话题已有的
   `Kind=utterance` 记录（几种都行，一轮不再恒两条），渲染成带说话者标签的转录
   交 `llmops.ExtractKeywords`；提炼失败或空结果直接报错，此时话题与 L2Meta
   一个字都没动，而**宿主先前 append 的内容原样留着**——Update 不拥有它，
   失败回滚它就要把「本轮写了哪几条」再记一份。一条内容都没读到
   （被保留窗裁光）时以 `ErrInvalidQuery` 拒绝且**不碰 LLM**：给一个说不出
   话的轮次写空关键词轨，读回来像是真提炼过。
   覆写的边界在 append 这一侧：写一个已被占用的 Seq 是原地覆写而非报错，
   本轮没写到的槽位也不会被回收，所以改一轮的文本可能让一句已撤回的回复继续
   出现在转录里，直到话题被删或过保留窗（这条写进 `AppendArchive` 的门面注释）。
   `turn.SettleTarget` 另外钉住可沉淀的范围：`TopicID` 必须是
   `hash("turn:" + 场景:k)` 且 `k <= 场景.TurnSeq`，即该场景真开出过的某一轮
   ——写 Dream 融合节点（同 depth、同场景，但由时间戳派生）、跨场景 id、宿主
   自造 id 都在 LLM 调用之前被拒，零留痕。重放当前轮与"先开两轮再乱序结算"
   仍然合法（`TestUpdateSettlesEachScenesTurnsInOrder`）。
4. **巩固按单场景规模触发**：`consolidateScene` 在 depth-1 话题数超
   `Defaults.SceneDreamTopicThreshold` 时调度该场景 Dream；单个融合组是
   "摘要内容 → 提炼关键词 → 建父话题 → 下沉子话题"的串写，任一步
   失败都回滚本组已写的记录（`dream.discardFusedGroup` 按父话题键整删它名下的
   内容与缓存，不需要携带任何 id 才能撤销一次写）——要么整体生效，
   要么不留孤儿记录 / 半成品父节点。
5. **内容类型与说话者在 `AppendArchive` 逐条声明并在其边界校验**：原文侧照收
   `Role`（user/agent/system）与 `ContentType`（零值 `ContentText`，非文本侧存
   路径/URL），未定义值以 `ErrInvalidQuery` 拒绝；`RoleDream` 是库给融合摘要
   自己盖的标记，公开常量里没有它、append 也拒它，否则宿主能伪造巩固产物。
   事件侧不接受这两项：`content.Append` 一律写 `Kind=event` + `ContentText` +
   `Role=0`，宿主在事件上给的 `Role`/`ContentType`/`TopicID`/`IDHash`
   一律不被采信（`TestAppendEventCannotForgeContentFields`）。
6. **`UpdateScene` 是 `SceneName` 的唯一宿主写者**：场景记录只被 `OpenSceneTurn`
   读改写（它回填整条记录、只动计数），Dream 从不写场景记录，故改名不会被
   后续读取覆盖；`scene.Create` 建新场景时才写默认名 `session:<id>`。
7. **内容由 (话题, Seq) 寻址，枚举仍靠镜像**：一条内容的地址就是
   `hash("content:"+话题+":"+seq)`，`TopicID` 是它归属的话题；单条能推出来，
   「这个话题一共有哪几条」推不出来，唯一的来源还是域内的 `ac.L4`——它是枚举
   手段，不是加速器。由此得出镜像纪律：任何删内容的路径都必须在**磁盘删
   成功后**同步摘镜像（`repo.DeleteTopicArchives` / `repo.DropExpiredArchives`
   已内置这一步），漏一处就让该话题之后每次读都撞「索引点名已不存在的记录」而
   硬 `ErrIO`；索引在 `domain.NewContext` 从记录重建，故重启自愈、运行期不
   自愈。读回顺序只看 `Seq`：按惯例用户说的占 1、回复占 2，所以「问在前、
   答在后」由写入侧选的槽位保证，不需要时间戳、更不需要拿 `Role` 打平（事件的
   `Role` 未设即 0，正是 `RoleUser`，一旦混进对话读法就会把一次工具调用显示成
   用户发言）。`SceneMessage.Seq` 因此是**契约字段**：空洞就是被保留窗裁过的
   证据，宿主据此把「裁掉了」与「没说过」分开。
8. **L0 画像字段所有权在库内强制**：`UpdateL0` 只写宿主四项
   （Name/Role/Personality/Preferences），`EmotionState`/`MBTI` 一律从库里
   现值继承（只有它们的首次建立走蒸馏路径），`UpdatedAtMs` 由库戳写、不采信
   调用方传值；api 侧的入站映射也不搬运这三项。`MergeDistill` 是反过来只写
   蒸馏项。
9. **L3 的 id 与边身份**：`core.readJSON` 校验帧内记录类型，种类不符即
   `ErrNotFound`（否则 `UpdateL3(节点 id)` 会把节点记录改写成图槽）；
   `CreateEdgeL3` 的 id 含 kind，导入按「排序成员 + kind」的语义键去重，
   故同一对节点可并存多种关系。`ImportL3` 结果带 `GraphIDs`
   （图 id = `hash(Domain)`，没有别的公开调用能渲染它）。全部 L3 记录住保留公共域
   `core.SharedPoolAgentID`（文件级公共池：`contextFor`/空闲回收/租户注册表
   三处豁免，`CreateAgent` 拒撞、`Session` 拒绑）。
   `DeleteL3` 两阶段：公共锁内删图，释放后遍历「默认域 + 注册表」逐域
   `lockAgent` 清锚（`detachGraphAnchors`），不嵌套双锁——代价是「删图后、
   清锚前」窗口内同名重导入（图 id = hash(Domain) 同 id）的锚点会被清成
   未锚定，可经 `UpdateScene` 重挂。
10. **内容只有一个写入口**：`content.Append` 是唯一写路径，`AppendArchive` 是它
   唯一的调用者（对话原文与事件都走这一条，`NodeSeq` 就写在记录上）。计划写面
   不碰内容：三个写口只动树，一步做过什么永远是宿主自己 append 的那些记录。
   `content.ValidateAppend` 是唯一的校验点，且**排在任何落盘之前**——被拒的写入
   一条记录也不留。事件若绑了 `NodeSeq`，本话题的树上必须已有那一步
   （`ac.Plans.HasSeq`）：一个序号指向计划里没有的步骤，是宿主的计划与它的记录
   对不上，报出来比顺手长出一棵树诚实。这道检查也排在落盘之前。
   两种 Kind 各自的字段归属、
   4 KiB/64 KiB 预算与跨 Kind 的 Seq 覆写语义记在
   `internal/content/agent.md` 与门面注释里，根不复述。
   `EventType` 是宿主自定的步骤名，计划绑定事件与裸事件同口径：引擎从不按它
   分支（只有读回时原样回显与结晶 prompt 的一行格式化），唯一约束是非空。
   内容只按话题键整体寻址：公开面上没有任何调用接受单条记录的 id 去写，
   所以写入不返回句柄（加了就是一桩没人消费的新契约）。
11. **`MultiAgentDB.CompactTo`**：core 的 `Compact` 用 `Create`（带
   `O_TRUNC`）在新路径写整理副本，故根层先拒空路径、拒当前库文件
   （`sameFile` 走绝对路径归一）与拒已存在的目标，绝不覆盖任何既有文件。
12. **节点只由创建口带出来，重述口只改字段**：`PlanCreate`/`PlanNodeAdd` 是唯一
   能让一个步骤存在的两个入口，序号由 `PlanCache.NextSeq` 在该轮的树上从 1 起顺序
   发号，宿主只回传、不自造。`parentSeq` 指向树上没有的一步是 `ErrNotFound`：一步
   的父是谁只有宿主知道，为它补出一个父节点是猜，猜错就长出一枝没人计划过的树。
   `PlanNodeUpdate` 只改已存在的这一步——`Status` 每次必须给（留空会被
   `StatusToU8` 拒掉——它没有"不改"这种写法），`Title`/`Summary` 留空继承现值，
   被重述回进行中的步骤清掉 `FinishedAt`。新建的步骤零值即 `in_progress`，所以
   创建口不要求宿主先给状态。校验与父序号判定都排在任何节点读写之前，一次被拒的
   写零留痕。**没有节点删除口，也不需要一个**：树跟着开它的那一轮走，宿主放弃
   一步的手段就是不在此后的轮里再创建它，旧树由 `l5_prune` 的保留窗回收。
13. **破坏性写入先验 id**：`MergeScenes` 会删记录，所以主/次每个 id 都必须
   仍是一个场景（`requireScenes` 逐个回读比对），未知 id 报 `ErrNotFound`；
   底层 `DeleteL2(DeleteScenesL2)` 直接按传入 id 批量删，少这一步时一个陈旧
   的 secondary id 就能带走存活主场景自己的记录，而调用还返回成功。
   删除面其余各口同此：`DeleteScene`/`DeleteTopic`/`DeleteL3` 都先回读确认目标
   存在（不认识的 id 正是 `CheckSession` 拒的那类 id）——没有一处把「记录不在」
   当成成功返回。
14. **`SceneContext` 的说话顺序是读出来的语义**：融合父话题的时间戳就是它吞掉
   的第一轮的 `UserTimestamp`，两者必然同值，所以排序在时间戳之后加
   `Depth` 次键（浅的在前）。只按时间戳排时 `slices.SortFunc` 不稳定，一组的
   摘要会随机落到它所总结的原文中间。

## 修改者义务

改动锁纪律、Dream 阶段划分或域生命周期时，必须同步更新本文件与
`internal/repo/agent.md` 中受影响的条目；在小方法包里改动契约时，同步该包
自己的 `agent.md`。

- 关键词提炼无本地兜底：LLM 输出不可解析即 `ErrLLM`（这一轮不产生话题），`internal` 根不初始化任何分词器。一轮的提炼与 Dream 的融合提炼共用 `llmops.ExtractKeywords`——它只吃一段文本，不认识记录结构。
- `ImportL3` 的批校验在 composition root 完成（Title/Domain 必填、mode 不接受空值），拒批即一字节不写；`result.Errors` 只表示单条存储失败。
- 宿主面测试覆盖 26 个会话方法 + 7 个 `MultiAgentDB` 方法，按层分文件：`test/api_interface_scene_test.go`（L2 场景生命周期）、`api_interface_plan_test.go`（L5 按步骤逐个建的树、Model A 折叠与节点字段回读、事件键到自己那一轮、重开后读回）、`api_interface_turn_test.go`（一轮之下原文与事件各归各的读法）、`api_interface_multi_test.go`（租户隔离与 `CompactTo`）。这些用例只使用库铸造并回传给宿主的 id。
