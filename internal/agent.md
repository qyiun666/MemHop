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
                cap 功能包、llmops 提示契约），不自己拿域锁
内部底座        internal/{config,llm} 配置类型与 LLM 传输；
                internal/cap 能力包（engram/profile 携记录读写，只接收注入的 engine/index）；internal/repo(+core) 连数据库内核的功能层
```

- 大方法（`Search`/`Update`/`RunDream`/L0-L5 各面/
  `SubAgent` 等）只做：`db.lockAgent` 取域 → 顺序调小方法 → 组装返回。
  细节逻辑（循环、重试、缓存维护、ID 铸造、回滚）一律在小方法包。
- 小方法包之间互不 import，一条都不例外；需要交互时回到根的大方法组装。
- 依赖方向单向：`根 -> 小方法包 -> {domain, cap, repo, llm} -> repo/core,index
  -> common`，禁止反向。

## 小方法包职责

| 包 | 职责 |
|---|---|
| `domain` | 域状态容器 `Context`（Mu/L2Meta/L4/Plans/DreamInFlight/OpCtx，持 Engine/LLM/Defaults 注入）+ PlanCache + L2Meta 缓存维护（SyncL2Meta/RemoveTopicsFromIndices/RetargetL2Meta）；`L4` 是「话题 → 它名下的内容槽位（原文 + 事件）」的镜像。另有两个**自持字段** `Scene`/`Turn`（该域当前的场景与开着的那一轮），宿主因此不必持有也不必回传这两个 id；它们只在域锁内读写，维护口是 `ForgetScene`/`MoveScene`/`ForgetTurn`，Dream 各阶段不在其列——宿主把巩固挂在定时器上撞进开着的一轮，不丢轮——窗口**内**的记录也不被扫，挂着的收束照样落下（`TestDreamLeavesTheOpenTurnCloseable`）；但清扫量的是钟不是轮的状态：开着的一轮里钟已过窗的记录与普通记录一样被扫走，Dream 只是不接管那一轮（`TestDreamSweepsAnExpiredMidRoundRecordAndStillClosesTheTurn`）|
| `scene` | L2 场景读写面：Create（没有场景可读时就在内部新建）/ResolveExisting（宿主点名一个场景时定位它）/CurrentScene（该域没有自持场景时，从记录里恢复轮次计数器跑得最远的那个）/SurfaceTopics/ContextTopic/DeleteCascade/DetachGraph；交回的都是场景 id，入参只收解好的数值 id |
| `turn` | 轮次归属：SettleTarget（可沉淀的轮次范围）、ReadProfile（Search 的 L0 读面）；进来的 hex 键已在根上解析完，本包不碰内容 |
| `dream` | 巩固阶段：SceneSet、PruneContentStage(`l4_prune`) 与 PrunePlanStage(`l5_prune`)（共用 `ContentRetention` 窗口、各读自己的时间戳）、CompressScenes(+组回滚)、StructureStages、L1 各阶段、distillL0Stage（私有）；调参常量随阶段在此 |
| `graph` | L3 导入/查询：`ImportBatch`（一次批次的 mode + result + 缓存：domain→图、图→标题集、图→边键，外加两份图集「访问过」/「写过内容」；方法 ImportNode/ImportRelations/GraphIDs/StampChanged）、NodeFilter.Matches/CheckSubgraphStart/SubgraphAdjacency/BfsWithinDepth/AllNodesVisited |
| `plan` | L5 计划树机制（一棵树归属于打开它的轮次；L5 只剩节点记录）：PlanStatus 面（单张词表、双向都查它）、NodeSpec/Step 两个入参形状、CreateNode/UpdateNode/UpdateNodeSummaryLocked、BuildTree/Forest/ToNodeView/RollupTree |
| `content` | 话题内容与键：ParseTopicID（轮次键的解析与拒零，读写两侧共用）、ValidateAppend（两种 Kind 各自的写入契约）、Append（宿主侧写一条内容的唯一入口，必要时跨 Kind 分配 Seq）、Read（按 Kind 读回）、RenderForDistill（把一个话题的原文渲染成提炼读的转录）、MaxEventPayload/MaxUtterancePayload |

## agentContext（domain.Context）域级锁纪律

1. **先域锁后存储**：所有大方法统一走 `db.lockAgent(agentID)`（内部：`contextFor`
   取域 + `ac.Mu.Lock()` + 锁内复检库未关、且该上下文未被空闲回收——见第 4 条的
   标记），再调小方法；引擎自带的锁在内层，顺序不可颠倒。同 agent 串行、跨 agent 并行。
   `contextFor` 对非默认域校验注册表：未注册的 agentID 直接
   `ErrAgentNotFound`。3002 只有这两处产出：`contextFor` 的复检，与门面 `DB.Agent(llm, id)` 的准入——后者是宿主唯一
   把一个 agent id 交回来的地方，认不出即拒，不顺手建一个空域顶上去。
   开着一轮的写面（`AppendArchive`、计划族、`Update`）统一走
   `db.lockTurn(agentID)`：lockAgent，然后**该域没有开着轮就先拒**
   （`ErrInvalidQuery`，消息含 "no turn is open"）——为它猜一个（当前场景的下一轮）
   等于把收束的话写到一个没人开过的轮上。轮次键由 `Search` 铸、由 `ac.Turn` 自持，
   所以这一族里根本没有一个宿主给的键要解析：一轮的内容（L4 的原文与事件）与它开出的
   计划树（L5 节点）共用同一个键，而这个键只有一个来源。
   仍按宿主给的键工作的只有读侧与改侧：`SearchL4` 的话题条件可选、
   `RenameTopic`/`DeleteTopic` 点名一个话题，它们各自经 `content.ParseTopicID`。
   门面侧的会话准入策略在 `CheckSession`。L3 的方法是唯一例外：走
   `db.lockSharedPool(callerID)`——先 `CheckSession` 校验调用方域活着，再锁
   保留公共域 `core.SharedPoolAgentID`（L3 记录全部住该域，跨 agent
   全局串行；公共域免空闲回收）。锚点校验（`scene` 新建时的 `create`、
   `UpdateScene` 的重挂）持调用方锁无锁读公共域记录，由引擎级
   互斥兜底。
2. **缓存刷新序**：写记录帧后紧跟 `ac.SyncL2Meta`（**存储 -> l2meta**），交出去的是
   刚写出去那条 slot——镜像不回读它刚写的那条记录：一次瞬时读失败除了把这条轮次从两份
   场景读里摘掉之外没有别的回答方式，而那次摘除会藏起宿主已经被告知沉淀成功的一轮。
   唯一的例外是 Dream 的 L2 压缩：它改写话题的深度与父子链而不逐条 `SyncL2Meta`，
   对账靠 `dream.StructureStages` 的整表重建——所以那份重建一算出来就要装回 `ac.L2Meta`，
   不得被其后任何阶段的失败丢弃（L1 各阶段只写 L1 记录，丢不掉它的正确性）。
   这份对账**不覆盖回滚自己也失败**的那种状态：`RestoreSunkTopicsL2` 只 warn 之后，那几个
   成员在盘上仍是 depth 2、挂着一个已被抹掉的父，而镜像还把它们列在表浅一层——**正是这份
   滞后让那一轮还读得到**。下一次走通压缩的整表重建会照盘上的深度认下来，那一轮从此不在
   任何读路径上出现（它的原文此时还在保留窗内）。这条极限只在双重失败时到达；别把它当成
   「全场景失败就早退、没对账」的缺陷去修——把重建提前到那条路径上，只是让这次丢失来得更早。
   **禁止在域锁内取 `db.agentsMu`**（锁序环：sweep 走 agentsMu -> ac.Mu），
   域内簿记里不受本域锁保护的那两个（`ac.LastActiveAt`、`ac.Reclaimed`）是 atomic 字段。
3. **Dream 域化**：`RunDream` 全程持本域锁；后台触发经
   `triggerSceneDream(ac, sceneID)`（调用方持 `ac.Mu`，留在根里因为它管理
   goroutine 生命周期），goroutine 运行在 `ac.OpCtx` 下——
   `Close` 与空闲回收取消它，任何在飞 Dream 在下一阶段边界退出，
   不会把生命周期屏障堵在一次完整 LLM 往返上。域锁内的前台 LLM 调用（`Update` 的轮次提炼）同样挂
   `ac.OpCtx`，避免生命周期屏障被一次完整往返阻塞。一次提炼要串多少次往返不在本契约里限定：长输入
   在 `llmops` 里按块走，块数随这一轮转录的长度增长，所以退出点是一块而不是一整轮，而一轮能在锁内
   串起任意多块——内容侧那两条上限量的是单条记录，不量一轮。
   引擎文件没了之后整份面只答一个码：`errDBClosed`（`ErrClosed`）在建/取域锁的每一处判出，措辞与判据
   只有一份；`Close` 自己也在其列（第二次 `Close` 同样答 `ErrClosed`，因为它没关掉任何东西），只有不读
   引擎状态的两个句柄口（`Session.AgentID`、`DB.IsClosed`）照常回答。这条整面契约由 `api` 侧的
   `TestEveryCallAnswersErrClosedAfterClose` 逐方法走一遍钉住。
4. **空闲回收**：无后台定时器；`contextFor` 顺带清扫超
   `Defaults.AgentIdleTTLMs` 未访问的域（宿主留 0 已在装配处折成默认值，这里见到 `<= 0` 就是宿主显式
   关掉的；默认域与共享 L3 域豁免），回收前先对域锁
   `TryLock`：锁被占用（在飞操作）或 `ac.DreamInFlight` 非空则跳过，留待下轮。
   摘除与 `ac.Reclaimed` 打标在**同一个持锁区间内**完成：调用方可能已经取到上下文、
   却在取锁前被调度出去，超过 TTL 后回收就会插进这两步之间——没有这个标记，那次操作
   会写在一份已作废的缓存上，而 `lockAgent` 复检到标记就重取域。回收时不快照任何东西：
   L2Meta 在下次访问时从记录重建，数据始终在文件里。两个自持字段里只有 `ac.Scene` 可重建
   （`scene.CurrentScene` 取最近开过一轮的那条，只有全都没盖过戳的旧记录才退回计数器）；**`ac.Turn` 不可重建**——开着的那一轮
   在盘上没有任何痕迹。于是回收跨过一轮的中途时，那一轮剩下的写入与收束一律被拒到宿主
   重开一轮为止（`TestIdleReclaimRefusesTheDroppedRound`）：不会静默写到新轮上，代价是那之前
   已写的记录留在一个从未沉淀的话题下，读面不再点名它。
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
6. **轮次键全零保留**：`0` 是每条记录未赋键时的值，故 `0000000000000000` 不是
   合法的轮次键。宿主能递进来的那几处一律经 `content.ParseTopicID` 拒它：
   `SearchL4` 的话题过滤、`RenameTopic`、`DeleteTopic`——放过它，一个根本没拿到键的
   查询会收到空清单，与一轮真的没内容分不清。同一判据也管**跨界**的键：`SearchL4` 的话题条件
   收到一个场景 id 即拒（两个键同出一次 `Search`，换着递是最容易犯的错，而空清单会被读成
   「这一轮没记东西」）；不指名任何记录的 id 仍走空答案——开着的那一轮先有内容、后才有话题记录。写侧的键不再由宿主递，于是同一档保留值
   换了两个落点：`ac.Turn == 0` 是「没有开着轮」的哨兵（`lockTurn` 据此拒），
   而 `Search` 铸出的键若真撞上 0 就报 `ErrCorruption`——那一轮既无法自持也无法
   被读出，静默收下就是让宿主接下来写到别人的轮上。
7. **计划清理有界**：dream 的 `l5_prune` 只豁免「持非 done 节点 **且** 窗口内
   仍有节点活动」的计划，其中活动只看节点自己的 `UpdatedAt`；宿主中断或放弃而
   静默超 `ContentRetention` 的计划照常清理。豁免保住的是**整棵活树**（含早已
   不更新的 done 父节点），不是「有事件在写所以树还活着」——事件住在 L4，
   不参与这个判断，也不再被节点的清扫带走。

## 数据访问纪律

- 只经 `internal/repo`（及 `repo/core` 导出的 Slot 读写）访问数据；
  **禁止**直接操作帧、文件头、快照结构。
- **L1 只有一个写者**：L1 节点与边的全部写入都发生在 Dream 的 `StructureStages`
  （`repo.SyncL1NodesFromL2` 同步节点并把**它这一轮写过的节点 id** 交给 `cap/engram`
  建边，边的权重只在这些端点的证据真的动过时才上升；重建与衰减同在 `cap/engram`）；
  `Search`/`Update` 与各写 API 这些热路径不写 L1，所以 L1 与 L2 之间允许差一个 Dream 周期。
- `StorageEngine` 句柄由装配层 `config.go` 唯一持有：注入 `DB.engine`，并经
  `domain.NewContext` 注入每个域；业务代码不得自行打开/关闭引擎。
  **唯一入口是 `OpenDB(path, llm, defaults, primary)`**：先用 `openEngine` 做三态
  判定（只在自己带了主域画像时才允许建文件），再 `assemble` 装配，最后按主域画像
  在不在落定 a/b/e 三条规则。**先校验后建文件**——被拒的打开不在宿主的路径上留
  任何东西：排在 `openEngine` 之前的三判各管各的入参（路径非空、`LlmConfig.Validate`、
  `MemHopDefaults.Validate`）。第三判管保留窗：写负数或超出可表示上限的宿主表达的是
  「别扫我的记录」，折回默认再让清扫照那个窗口删，等于把一次配置笔误变成一次数据损失。
  **建文件是带截断的**，所以 `openEngine` 只在
  `errors.Is(err, os.ErrNotExist)` 时才走创建分支，其它 stat 失败一律上报；路径是
  目录时显式拒绝，否则 `core.Open` 会回一句误导的「文件太小放不下双头」。
- **域身份三个入口**：`Primary()` 返回零号域（一个文件恰好一个主域，无需扫描），
  `SubAgent(llm, profile)` 按 `profile.Name` 幂等建/取一个注册域并挂上它自己的
  LLM 端点，`Agent(llm, agentID)` 按 `Session.AgentID` 交出的那个 id 取回同一个域——它只走
  `CheckSession` 的准入（注册表认不出即 `ErrAgentNotFound`，不许顺手建一个空域顶上去），
  因此既不注册也不写画像，剩下的锁序与 `SubAgent` 那一条相同。两个带端点的入口都先过
  `LlmConfig.Validate`，与 `OpenDB` 那一判同一份规则——一份装不进 duration 的超时必须在第一次调用
  把域锁挂死之前就被拒。`Agents()` 是反向的发现口：读磁盘上的
  注册记录（不是内存里那份表）列出本文件的所有域，键读不出来就带着那个原因停下——一份更短的列表是一次
  错答。域 id 的作用域**是一个文件**：
  每个文件的主域都是那个隐式零号域，所以同样的 16 个 0 在另一个文件里指另一份记忆；宿主按
  「一个 agent 一个文件」部署时跨文件的键是路径（`TestAnAgentIDAddressesADomainInsideOneFile`
  两头各钉一条）。`SubAgent` 的顺序是硬约束：注册（`agentsMu`）→ 挂端点（`agentsMu`）
  → 取会话句柄（`CheckSession` 读注册表，`agentsMu`）→ **最后**才 `lockAgent`
  拿域锁，在锁内把这次的端点装到域上下文上、再写画像。反过来就是 `ac.Mu` 之下取
  `agentsMu`，正是本文件域锁纪律第 2 条禁止的那个环。写画像用 ensure 语义（已有就不动），所以注册记录写完、画像没写完
  就崩的情况下，同名再调一次会把画像补上。
  建**新**域的前提是这个名字空闲，而「空闲」的判据是整个注册表里没有一条解不出名字的
  键：一条读不回的注册记录仍然占着一个域，此时另发一个同名域等于把宿主引到一个空域，
  真域从此既列不出也删不掉。所以损坏只挡住「建」（`ensureRegistered` 现场再扫一次注册
  表并拒掉，拒因带出那个域的 id），已解析出的名字照旧——`Open` 不因它拒绝，主域与其余
  域都还能用。
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
  `LlmConfig` 值去重（上百个域共用一个端点时只有一个 http.Client）。**两张表都
  刻意比域上下文活得久**——空闲回收丢掉上下文后 `contextFor` 会重建它，覆盖若挂在
  上下文上，一个闲置超过 TTL 的域会静默退回库级端点。表只管**未来**的重建：同名再调
  `SubAgent` 换端点时，宿主手里正是一个活域，所以新 transport 由 `SubAgent` 在域锁内
  赋给 `ac.LLM`——只写表就是让重连的宿主每一轮继续打旧端点，旧 key 一旦失效就是每轮
  `ErrLLM`、那一轮永不沉淀。
  **所有 LLM 调用点一律读 `ac.LLM`，不读 `db.llm`**——库级那一个只是注入的默认
  值，绕过注入位的调用点在「每个域同一个端点」时看不出问题，只在某个域带自己的
  端点时才显形，而且显形成同一个域的两类调用打到两个端点。

- **错误判定纪律**：区分「记录不存在」与「读不动」。`ErrNotFound` 只代表
  前者；IO / 关闭 / 反序列化失败一律原样上抛，不得改写成 `ErrNotFound`，
  也不得当成"不存在"后继续写（`repo.GetProfileL0` 带出记录自身的错误码，`content.Read`、
  `profile.MergeDistill`、`UpdateL0` 的继承判断都靠这条分界落地；
  误判会让一次读不动被报成「这条记录没有」，或画像被空值覆盖）。
  **同一条分界管到整桶扫描**：一个枚举是拿去决定下一次的写的——删谁、覆写谁、
  还是要不要新建哪一条（保留窗清扫、话题与整图级联删除、L1 节点同步与重建、图导入
  批次对整池标签/标题/边键的播种），就必须整个读得回来——读不回的那一条不是
  「不在」，少它一份就让被删的是幸存那几条、被覆写的正是读不回的那一个。只有「从幸存
  记录重建一份视图或缓存」的扫描才允许跳过它，代价是那一条在这份缓存里缺席，而这个
  缺席与它被保留窗裁掉时形状相同——**前提是这份缺席只影响读**。缓存一旦还决定下一次写
  落到哪里（计划树的序号由幸存节点与幸存事件点名的序号两处取高，而记录 id 由 (键, 序号) 派生），缺席就不再
  是「少看见一条」而是「把别人的地址当成空槽」，这类写入要在落记录前定点读那一个地址。第三类同样容错：只作为一次算法输入的那份读（衰减遍历
  边、蒸馏取样、交给模型的列举），跳过一条的代价是这一轮少算它，盘上不会多出说不清的
  东西——这类扫描不去要求读得回来。
  **顺序是这条分界的一半**：决定删谁的枚举排在任何墓碑之前，被拒的一次才真的
  什么都没做；反过来（先落墓碑再枚举）就是让一次拒绝留下盘上说不清的半成品，
  而那半成品既不在「已删」也不在「未删」的状态里。撤销自己刚写的记录按那个 id
  定点删，它不需要枚举，也就不能被域里另一条读不回的记录挡住。
  **另一半是每个上抛的错误都带着自己的码**：`api.CodeOf` 对不是 `*common.Error`
  的错误返回 0，而 0 是「成功」那一档，一次不带码的拒绝等于没有结论。两处由此
  收口：帧解码器的 `io.EOF` 只对逐帧扫描有意义（「扫到这里为止」），按 id 读时
  它说的是索引点名了一条记录区里并不存在的记录，于是翻译成 `ErrCorruption`；
  Dream 的「一个场景都没巩固成」带 `ErrLLM`，上下文已经取消时如实说取消，而**引擎自己
  拒掉的那一组原样上抛它那一档**（`ErrIO` 就是写失败）——
  把一次取消或一次写失败报成模型失败，宿主就会去查一个从没拒绝过它的端点。**取消有自己的一档
  `ErrCancelled`（5008）**：Dream 的每个检查点、LLM 传输里被调用方撤掉的等待
  （请求在途与退避等待两条都算）都报它，cause 留着 `ctx.Err()`，
  `errors.Is(err, context.Canceled)` 照旧成立。

## 读写路径契约

1. **一次 `Search` = 定场景 + 开一轮**：`SceneID` 为空 → 续用该域自持的 `ac.Scene`；
   自持为空（首次访问、或空闲回收后重建、或当前场景被删）时经 `scene.CurrentScene`
   从记录里恢复「最近开过一轮」的那条（`LastUsedAt`），都没盖过戳的旧记录才退回「`TurnSeq` 跑得最远」、再平手取 id 小的，所以同一份记录两次恢复
   得到同一个场景（这一步与下面的纯读共用 `ensureScene`，两条读不会各自演化「当前场景」
   的判法）；一个场景都没有才新建——`scene` 在自己内部铸一个未被占用的 ID
   （`0` 跳过；只有 `ErrNotFound` 才算可用，其他读错误原样上抛）并落一条场景记录
   （名字一律库生成 `session:<id>`，锚点与它同批写入）。`NewScene:true` 跳过这一切
   直接新建，那是宿主唯一「另开一条会话」的写法。`L3ID` 只被**这一读会新建场景**的两条
   路径采纳（`NewScene:true`、该域还没有场景的第一读）；续用一条会话时递来锚点一律拒，
   点名的那条还要在消息里带上那个 id——锚点是创建期字段，改锚只走 `UpdateScene`，
   静默丢弃会让一次没生效的锚定看起来生效了。两道拒绝都只看入参、不看那张图在不在，
   否则一张已删的图会把「场景已在这里」报成「记录不存在」（宿主去找一条自己从没要读的
   记录）。`SceneID` 非空且不存在 → `ErrNotFound`；自持的那条不再重复审存在性——紧接着
   `repo.OpenSceneTurn` 读的就是同一条记录，它同时把场景的 `TurnSeq` 推到下一轮，`NewTopicID = hash("turn:" +
   场景:TurnSeq)` 就是本轮的话题，根把它与场景一起装进 `ac.Scene`/`ac.Turn`。
   铸出的键撞上保留的 0 时报 `ErrCorruption` 而不是收下（见第 6 条）。
   画像读取失败同样使本次读取失败（仅"画像尚未建立"按空
   画像继续），不再静默返回缺 L0 的上下文。
2. **`Search` 零 LLM、零话题写入**：唯一写是那一行场景记录（轮次计数，
   写失败即报错——轮次号是铸 ID 的依据，不能吞），话题从 `ac.L2Meta` 取
   depth-1。**`Search` 自己这一侧不留残渣**：开了没沉淀的轮次没有话题记录，读两次只沉淀
   一次就是跳号。宿主在那把键下自己写下的内容与计划树另算——它们是真实的记录，只是没有
   话题记录可依附，`DeleteTopic` 认不到，收走它们的只有 Dream 的保留窗清扫。
3. **`Update` 先收束这一轮，再每轮一次提炼，且提炼排在话题写入前**：它把入参里非空的
   那几条记录落到开着的那一轮上（三条全空即拒——收束总得留下点什么）——`Input` 占
   `Seq=SeqUser`、`Output` 占 `Seq=SeqAgent`
   （对话主干的两个预留格，所以重关一轮是原地覆写而不是累积两份收束）、`Outcome` 作一条
   `turn_outcome` 事件追加（一次挂起加一次恢复是两条事实，故按调用次数累积）——
   这几条都在任何落盘之前整批过 `content.ValidateAppend`，一条被拒就一条都不写。
   然后读该话题已有的 `Kind=utterance` 记录（几种都行，一轮不再恒两条），渲染成带
   说话者标签的转录交 `llmops.ExtractKeywords`；提炼失败或空结果直接报错，此时话题与
   L2Meta 一个字都没动，而**已落下的那几条与宿主先前 append 的内容原样留着**——回滚它们
   要把「本轮写了哪几条」再记一份，而重放一次 `Update` 会自己把那两个槽覆写回新值。同一轮被收
   两次走的就是这条覆写：两次结局各留一条事件，对话槽只留最后一次说的那一对——内核每次调用各
   开一轮、各收一次，正常接法不会撞到这个形状。
   一条内容都没读到（被保留窗裁光）时以 `ErrInvalidQuery` 拒绝且**不碰 LLM**：给一个
   说不出话的轮次写空关键词轨，读回来像是真提炼过。
   覆写的边界在 append 这一侧：写一个已被占用的 Seq 是原地覆写而非报错，
   本轮没写到的槽位也不会被回收，所以改一轮的文本可能让一句已撤回的回复继续
   出现在转录里，直到话题被删或过保留窗（这条写进 `AppendArchive` 的门面注释）。
   `turn.SettleTarget` 另外钉住可沉淀的范围：自持的 `Turn` 必须是
   `hash("turn:" + 场景:k)` 且 `k <= 场景.TurnSeq`，即该场景真开出过的某一轮
   ——写 Dream 融合节点（同 depth、同场景，但由时间戳派生）与跨场景的旧键都在 LLM
   调用之前被拒，零留痕。这道闸挡的是**自持字段过期**那一类：三条会毁掉一轮的写口各自
   摘掉自持字段（见第 13 条），而一次在飞的 `Update` 若跨过了别的写口留下的窗口，
   这里就是最后一道。宿主自造 id 那一类
   已经不存在——它不再有机会递一个键进来。这道闸不拦「往别的话题写内容」——内容记录
   只被它的**话题**寻址，而话题里推不出场景，那是键本身的语义，不是归属闸的活。
   重放当前轮与"先开两轮再乱序结算"
   仍然合法（`TestUpdateSettlesEachScenesTurnsInOrder`）；重放重写的是引擎那半
   （关键词轨、两个时间界、场景归属——那两个界取的是该轮**原文**的 min/max，轮中事件各按自己的时刻存在、
   不撑宽它们（`TestTurnTopicBoundsIgnoreMidRoundEvents`）），而宿主给的 `name` 与 Dream 已经给过的位置
   （`depth`、`parent_id`）都从存量记录里带过来——否则把一轮重述一次就把它悄悄改了
   名（`TestCreateTurnTopicL2ReplayKeepsHostName`），或让一个已沉入融合组的轮次带着
   自己的原文回到 surface，与取代它的那份组摘要并排出现、组还少一个子
   （`TestCreateTurnTopicL2ReplayKeepsSunkPosition`）。
4. **巩固按单场景规模触发**：`consolidateScene` 在 depth-1 话题数超
   `Defaults.SceneDreamTopicThreshold` 时调度该场景 Dream（同一份表在装配处归一过，故
   「没填」= 默认 24，`t <= 0` 只剩「宿主显式写了负数」这一种来路）。被拒的那趟不改表层计数，于是下一次收轮仍然超阈、再问一次，而本库不记「上次拒过」——那要新造一份会出错的持久真相；这条花费的上界是每场景每趟一次巩固调用（`DreamInFlight` 吸收在飞期间的那几次收轮，`TestDeclinedConsolidationAsksOncePerScenePerPass` 数着调用次数钉住），而 `DreamCompressMinTopics` 判在问模型之前，默认 20 低于触发 24，所以它对已被触发的场景不构成第二道闸。单个融合组是
   "摘要内容 → 提炼关键词 → 建父话题 → 下沉子话题"的串写，任一步
   失败都回滚本组已写的记录（`dream.discardFusedGroup` 按父话题键整删它名下的
   内容与缓存——本组写过的东西全在父键底下，不必去数域里别的记录；一次下沉的
   撤回则按本组自己点名的成员做，见下文）——要么整体生效，
   要么不留孤儿记录 / 半成品父节点。一个场景落地的各组**成员互斥**：模型按对话线程
   分组，相邻两条线程可以都点名同一轮，而两组都应用等于把那一轮沉两次——第二次改父
   指向，第一个组的摘要于是管着一个不再应答它的子，那一轮也落到最深的读路径之下，
   所以后到的重叠组按「提出但未应用」计入 rejected（`TestApplyGroupsRejectsOverlappingGroups`）。
   父话题 id 把成员集合一并算进派生式，所以两个不相交的组共用一对时间界也各得一个父——
   时间界单独作键时，后到的那一组永久压不掉，而每一趟巩固都会再为它花一次模型调用
   （`TestApplyGroupsLandsDisjointGroupsWithOneBoundsPair`）。占用校验照旧在：同一成员集合被
   再提一次就是落在已占用的地址上，仍在写任何记录之前拒掉、无回滚；撞上一个不是话题的记录
   也拒而不覆写（一个 id 只对应一种记录）（`TestApplyGroupsRefusesReplayedMemberSet`）。
   下沉是一次批写，而**批写不是全有或全无**：尾界推进之后才失败（`Sync`/remap）时，
   已经写到的成员就是活了，父链接挂在它们身上。让整组保持原子的是回滚那一步——
   `dream.applyOneGroup` 拿本组自己点名的成员调 `repo.RestoreSunkTopicsL2`，只把
   此刻正挂在这个父下的条目收回 depth-1（没被批写触及的成员不挂这个父，因而不被动），
   且**先撤子再删父**：反过来若在两步之间再失败，留下的是挂在已删父下面的 depth-2
   轮次，而 `Search` 只列 depth-1、下一趟 Dream 的挑组也只列 depth ≤ 1，那一轮从此
   谁也合并不到它。成员读不动（`ErrNotFound`
   以外的任何错误，或索引点名却不是话题记录）就整组不动、把错误交回上面回滚，
   只有「已经不在」的成员从组里退出——吞掉一次读失败会留下父摘要与那一条自己的
   原文同时在场景里，正是一个半成品父节点（`TestCompressTopicsL2RefusesUnreadableMember`、
   `TestRestoreSunkTopicsL2BringsBackOnlyThisGroupsMembers`）。
5. **内容类型与说话者在 `AppendArchive` 逐条声明并在其边界校验**（`Update` 自己那三条
   走同一道 `content.ValidateAppend`，它不给自己开后门）：原文侧照收
   `Role`（user/agent/system）与 `ContentType`（零值 `ContentText`，非文本侧存
   路径/URL），未定义值以 `ErrInvalidQuery` 拒绝；`RoleDream` 是库给融合摘要
   自己盖的标记：名字出得去（读侧得能认出哪一句是库写的），写侧拒收，否则宿主能伪造巩固产物。
   事件侧不接受这两项：`content.Append` 一律写 `Kind=event` + `ContentText` +
   `Role=0`，宿主在事件上给的 `Role`/`ContentType`/`TopicID`/`IDHash`
   一律不被采信（`TestAppendEventCannotForgeContentFields`）。
   同一写法管到读侧的过滤条件：`SearchL4` 在进数据层之前把 `Kind`/`Type` 的未定义值、`NodeSeq`
   没带话题键、以及两条时间界的单位各拒一次（界用 `content.CheckQueryBound`，与写入同一个判断），
   因为一个错单位的界答出来的行集不是宿主能核对的东西。「取多少」这一类入参则共享一条读法：
   `L3NodeQuery.Limit`、`L4Query.Limit` 与 `QueryL3Subgraph` 的 `maxDepth` **非正即不设界**，判据住在
   `graph.BfsWithinDepth` 自己身上而不是调用方——钳在调用方会让同一个 0 在两条读上说着相反的话，而
   `limit` 与 `max_depth` 都是宿主绑成工具后由模型亲手填的键。
6. **`UpdateScene` 是 `SceneName` 的唯一宿主写者**：落笔前先比现值，**patch 没改到任何东西就不追加**
   （空 patch 是宿主文档里的「确认一次锚点而不列举域」那条读，重试同一份 patch 也是常事，而文件是纯追加的，
   每一次等值写都要占字节）；场景记录只被 `OpenSceneTurn`
   读改写（它回填整条记录、只动计数），Dream 从不写场景记录，故改名不会被
   后续读取覆盖；建新场景时才写默认名 `session:<id>`（`scene` 私有的 `create`）。
7. **内容由 (话题, Seq) 寻址，枚举仍靠镜像**：一条内容的地址就是
   `hash("content:"+话题+":"+seq)`，`TopicID` 是它归属的话题；单条能推出来，
   「这个话题一共有哪几条」推不出来，唯一的来源还是域内的 `ac.L4`——它是枚举
   手段，不是加速器。由此得出镜像纪律：任何删内容的路径都必须在**磁盘删
   成功后**同步摘镜像（`repo.DeleteTopicArchives` / `repo.DropExpiredArchives`
   已内置这一步），漏一处就让该话题之后每次读都撞「索引点名已不存在的记录」而
   硬 `ErrIO`；索引在 `domain.NewContext` 从记录重建，故重启自愈、运行期不
   自愈。镜像的同一处容错还管到**分配**：`Seq=0` 那一格是本包替宿主选的，而它数的是幸存
   记录，解不开的那条虽不在镜像里、槽位号仍在派生 id 上——所以分配的写入落笔前定点读那一格
   （`content.Append`），除「不存在」以外不写；宿主点名 `Seq` 的覆写照旧不查，那是它自己
   选的重放语义。一个话题内读回顺序只看 `Seq`：按惯例用户说的占 1、回复占 2，所以「问在前、
   答在后」由写入侧选的槽位保证，不需要时间戳、更不需要拿 `Role` 打平（事件的
   `Role` 未设即 0，正是 `RoleUser`，一旦混进对话读法就会把一次工具调用显示成
   用户发言）。`SceneMessage.Seq` 因此是**契约字段**：它说的只是「这一格没有内容」。
   空洞有**两种成因且读侧分不出**：保留窗把那一格收了（连镜像条目一起摘，`Dream` 的清扫
   就是这么报的），或宿主自己没写那一格——点名 `Seq` 的写入是合法的重放语义，跳号写就是
   留一个空着的位置。把空洞一律读成「被裁掉了」，等于让宿主把自己少写的那一行当成丢数据。**两种宽度两种顺序**：一个话题内
   `Seq` 就是顺序；跨话题的读（不带 `TopicID` 的 `SearchL4`）没有共同的 `Seq` 可看
   ——那是轮内序号——按记录自己的时间排、以记录 id 收尾，`Limit` 说的才是「最近的
   N 条」，同一个查询两次给出同一个子集。
8. **L0 画像字段所有权在库内强制**：`UpdateL0` 只写宿主四项
   （Name/Role/Personality/Preferences），`EmotionState`/`MBTI` 一律从库里
   现值继承（只有它们的首次建立走蒸馏路径），`UpdatedAtMs` 由库戳写。门面不再靠
   入站映射丢弃那三项——宿主能传的 `api.ProfileInput` 就只有那四项，库自有的三项
   不是「传了不采信」而是没有位置可传。`MergeDistill` 是反过来只写蒸馏项，但
   `Personality` 是例外：那一项有**两个写者**（已裁定「都写」），且两边不对称——蒸馏
   只在真的有话要说时盖掉它（空串保留现值），`UpdateL0` 照宿主交来的写、空串也算一个值，
   于是留空的宿主会抹掉上一次蒸馏出的那句，下一次 Dream 再写回来。
   同一条读法管到 `Role` 与 `Preferences`：**宿主那半整次替换、不按 key 合并**——只写一条偏好就只剩那一条，
   nil 表等于空表，省略 `Role` 即清空它。不合并有理由：按 key 合并会让一条偏好永远删不掉，所以改一项的姿势是
   `GetL0` 拿表、改完整张写回（`TestUpdateL0WritesTheHostHalfWhole` 两头都钉）。
   `Name` 是三个写画像的入口（`Open` 播种主域、`SubAgent` 建/取注册域、
   `UpdateL0` 改）共同的必填项：域就靠它被称呼，`UpdateL0` 是唯一能把它清掉的
   口，所以空白名一律 `ErrInvalidQuery` 拒绝，而不是存下一个无从指认的画像。
   子域的那个 `Name` 同时是 `SubAgent` 开门用的租户键（住在注册表记录里），本条写口
   只写得到画像里的副本，所以两者不符一律 `ErrInvalidQuery` 拒掉而不是改一半——留下的是
   「旧名开得出这段记忆、新名开出一个空域」这种不报错的失忆。主域不靠名字被寻址
   （`Primary()` 不收名字），它的 `Name` 才是自由文本。

9. **L3 的 id 与边身份**：`core.readJSON` 校验帧内记录类型，种类不符即
   `ErrNotFound`（否则 `UpdateL3(节点 id)` 会把节点记录改写成图槽）；
   `CreateEdgeL3` 的 id 含 kind，导入按「排序成员 + kind」的语义键去重，
   故同一对节点可并存多种关系。标签就是这张图的地址：宿主拿 `Domain` 找图，图 id
   又由它派生，所以两道写口都不接受空标签——导入条目缺 `Domain` 即拒，`UpdateL3`
   改名给空串也拒，改到别的图已占用的标签同样拒；而**改成它自己已带的那个标签什么都不写**（不追加记录、
   也不盖钟），因为那口钟的意义就在下一句。
   `ImportL3` 结果带 `GraphIDs`
   （图 id = `hash(Domain)`，没有别的公开调用能渲染它）。图槽的 `UpdatedAt` 是**内容变化钟**：
   一次批次只给它真写过节点或边的图盖一次钟，`GraphIDs` 报的是「解析到的图」，与这份
   「改过的图」是两个集合；读口一概不盖，改标签算一次变化（`TestGraphSlotClockAnswersChangeNotAccess`
   四个方向各钉一条）。全部 L3 记录住保留公共域
   `core.SharedPoolAgentID`（文件级公共池：`contextFor`/空闲回收/域注册表
   三处豁免，域发号时跳过它与默认域，宿主因此拿不到也绑不上这个 id）。
   `DeleteL3` 两阶段：公共锁内删图，释放后遍历「默认域 + 注册表」逐域
   `lockAgent` 清锚（`detachGraphAnchors`），不嵌套双锁——代价是「删图后、
   清锚前」窗口内同名重导入（图 id = hash(Domain) 同 id）的锚点会被清成
   未锚定，可经 `UpdateScene` 重挂。`scene.L3ID` 是 L3 图唯一的入边（图槽上没有
   反向清单），所以清锚只能按域扫场景；漏清锚的可见后果是 `ListScenes(l3ID)`
   继续列出解不开的会话。遍历的对象必须是**注册表**而不是当前活上下文映射——被空闲回收的域记录仍在盘上，
   锚同样得清；而注册表在 Open 时由记录重建、也不随回收删项，「回收过的域不会被漏」这件事才成立
   （`TestDeleteL3DetachesAnchorsInReclaimedDomains`；把它变异成只遍历活上下文，另一条只测活域的用例仍全绿）。图槽的 `UpdatedAt` 是**这张图内容的变化钟**：一批导入
   结束时，由根对「本批真写过东西」的每张图各推进一次（`batch.StampChanged`，
   一个图一次写而不是每条记录一次），改名走同一个偏更新原语（`name=nil`），
   只读到没写过的图不动它——skip 模式重导同一批节点因此不会让这张图看起来变新。
10. **内容只有两个写入口，且都落到同一个原语**：宿主侧的内容一律经
   `content.Append`（调用者两个：`AppendArchive` 逐条写，`Update` 写它自己那三条
   收束记录——对话原文与事件都走这一条，`NodeSeq` 就写在记录上）；巩固组那份摘要是唯一不经该边界的写入——
   `dream.applyOneGroup` 直接落 `repo.AppendArchiveL4`，因为它要占 `Seq=SeqUser`
   那一格与库自有的 `RoleDream`，而 `ValidateAppend` 恰恰拒调用方给 `RoleDream`
   （否则宿主能伪造巩固产物）。`RoleDream` 只由巩固那一处戳写，两入口之下的原语只有
   一个。`content.ValidateAppend` 是宿主侧写入的唯一校验点，且**排在任何落盘之前**——
   被拒的写入一条记录也不留；巩固侧对应的判据是空摘要不成组，它同样排在组落盘之前。
   计划写面不碰内容：三个写口只动树，一步做过什么永远是宿主自己 append 的那些记录。
   事件若绑了 `NodeSeq`，本话题的树上必须已有那一步
   （`ac.Plans.HasSeq`）：一个序号指向计划里没有的步骤，是宿主的计划与它的记录
   对不上，报出来比顺手长出一棵树诚实。这道检查也排在落盘之前。
   两种 Kind 各自的字段归属、预算（事件的 4 KiB 量的是**整条记录**——
   `EventType` 与 `Content` 合起来算，分开各量一道就等于给批量写入留一个免检的口袋）
   与跨 Kind 的 Seq 覆写语义记在
   `internal/content/agent.md` 与门面注释里，根不复述。
   `EventType` 是宿主自定的步骤名，计划绑定事件与裸事件同口径：引擎从不按它
   分支（读回时原样回显那个名字），唯一约束是非空。
   内容只按话题键整体寻址：公开面上没有任何调用接受单条记录的 id 去写，
   所以写入不返回句柄（加了就是一桩没人消费的新契约）。
11. **`DB.CompactTo`**：core 的 `Compact` 用 `Create`（带
   `O_TRUNC`）在新路径写整理副本，故根层先拒空路径、拒当前库文件
   （`sameFile` 走绝对路径归一）与拒已存在的目标，绝不覆盖任何既有文件。
12. **节点只由创建口带出来，重述口只改字段**：`PlanNodeAdd` 是唯一
   能让一个步骤存在的入口（parentSeq 0 即开树），序号由 `PlanCache.NextSeq` 发号，宿主只回传、不自造。
   发号取两处的高：该轮幸存节点的最高序号，**与**这一轮幸存事件里被点名的最高序号——一步
   与指着它的那条事件共用同一个 (轮, 序号) 地址，而两者各自老化，所以被裁掉的步骤留下的
   事件仍占着那个序号；代价是一轮的序号可能出现空洞，那是比把别人的工作算到新步上更便宜的结果。
   `parentSeq` 指向树上没有的一步是 `ErrNotFound`：一步
   的父是谁只有宿主知道，为它补出一个父节点是猜，猜错就长出一枝没人计划过的树。
   `PlanNodeUpdate` 只改已存在的这一步——`Status` 每次必须给（留空会被
   `StatusToU8` 拒掉——它没有"不改"这种写法），`Title`/`Summary` 留空继承现值，
   被重述回进行中的步骤清掉 `FinishedAt`。新建的步骤零值即 `in_progress`，所以
   创建口不要求宿主先给状态。三态的取值就是那三个小写串，`L3ImportMode` 的三个值同样小写——宿主从模型那里拿到一个词就能直接 `L3ImportMode(word)` 递进来，不必替它改大小写；这套词表走法由 `api/surface_enums_test.go` 与两份指南 §7.6 相互核。校验与父序号判定都排在任何节点读写之前，一次被拒的
   写零留痕。**没有节点删除口，也不需要一个**：树跟着开它的那一轮走，宿主放弃
   一步的手段就是不在此后的轮里再创建它，旧树由 `l5_prune` 的保留窗回收。
   发号点名到的地址读不回来时（镜像跳过解不开的记录，而地址由 (轮, 序号) 派生，看着仍像空的），
   创建**拒且持久**：一次不记、二次不绕，第二次仍交回那次读自己的码——自愈式的「删掉再发号」
   会把一个步骤的工作悄悄接给下一个序号（`TestPlanNodeAddRefusesAnAddressItCannotRead`）。
13. **破坏性写入先验 id**：`MergeScenes` 会删记录，所以主/次每个 id 都必须
   仍是一个场景（`requireScenes` 逐个回读比对），未知 id 报 `ErrNotFound`；
   底层 `repo.DeleteL2Records` 只照给定的 id 落墓碑、不认 id 是什么，少这一步时一个陈旧
   的 secondary id 就能带走存活主场景自己的记录，而调用还返回成功。
   同一条清单还要说得出「谁」：一个 secondary 出现两次即 `ErrInvalidQuery` 拒（改挂与落墓碑会照单
   跑第二遍，而调用方此时已经数不清自己要并掉哪几段）。
   删除面其余各口同此：`DeleteScene`/`DeleteTopic`/`DeleteL3` 都先回读确认目标
   存在（不认识的 id 正是 `CheckSession` 拒的那类 id）——没有一处把「记录不在」
   当成成功返回。这三条路径还各自带走域自持的那一半：`DeleteScene` 清 `ac.Scene`/`ac.Turn`
   （`ForgetScene`），`DeleteTopic` 沿**整条话题闭包** `ForgetTurn`（被删的是融合父时，它吞掉
   的那一轮也在闭包里），`MergeScenes` 经 `MoveScene` 把自持场景改挂到主场景上、轮次清空（L3 锚按 `mergedAnchor` 定：主场景已锚则以它为准，未锚就接手次场景那个，次场景的锚彼此不一致即整次拒且在任何删除之前——锚是一段对话出现在项目列举里的唯一凭据，丢掉它等于悄悄把这段对话搬出项目视图）
   （轮次 id 由场景派生，跨合并活不下来）。漏一处的后果与漏验 id 不是一个量级：下一次
   `Update` 会把收束写进一条已毁的轮，那批记录在读路径上谁也看不见，只能等保留窗收走。
   **一个场景结束它的两种写法都要带走它的 L1 节点**：`DeleteScene` 删完记录就
   `repo.DeleteSceneNodeL1`，`MergeScenes` 在验完 id 之后、写之前先删掉每个次场景的
   节点（先删才谈得上可重试：合并被拒时场景还活着，下一次 Dream 会把节点建回来）。
   合并只把话题改挂到主场景上，而 `engram.RebuildFromL2` 判陈旧看的是节点自己的
   `TopicIDs`——那些话题条条读得回来，于是留下来的节点永远顶着一批不再属于它的轮次，
   继续参与共现建边与 L0 蒸馏。它的共现边由下一次 Dream 的衰减剪掉：`decayOneEdge`
   按「本域还持有这个节点吗」过滤成员，不再只看本轮刚删掉的那几个。
14. **`SceneContext` 的说话顺序是读出来的语义**：融合父话题的时间戳就是它吞掉
   的第一轮的 `UserTimestamp`，两者必然同值，所以排序在时间戳之后加
   `Depth` 次键（浅的在前）。只按时间戳排时 `slices.SortFunc` 不稳定，一组的
   摘要会随机落到它所总结的原文中间。同一条读**只渲染 utterance**（`content.Read(..., KindUtterance)`），
   一轮的两句原文又由同一次 `Update` 盖同一个钟，所以保留窗扫过之后 `Messages` 只会整对消失（读回是空的，
   不是「有洞」）；会留下洞的是事件轨，而两条读都不重排幸存记录的 `Seq`（`TestSweepKeepsEverySurvivorAtItsOwnSeq`）。
15. **纯读也能不点名**：`SceneContext` 收到空的 `sceneID` 时读的是该域自持的那条会话
    （`readScene` → `ensureScene`），并且**不新建**——新建场景是开轮那条读的义务，一个承诺
    零写入的调用不能顺手留下一条会话。但「从没说过话」是这条读**答得出来**的一件事，不是一次失败：
    它交出空转录且 `SceneName` 为空，不报错。留给错误码只会逼每个宿主自己补一次特判（端口一报错
    内核就终止整次调用，第一次召回反而打死这只猫）。点名而不存在的场景仍是 `ErrNotFound`。
    这条是给
    决策循环的召回用的：一轮里「问模型之前」的读可以发生很多次，只该有一次 `Search` 真的
    开轮，其余都走这一条，否则轮次计数会替没发生过的轮往前空跳。
    这条读的另一半是**它列不出还没收束的那一轮**：话题那一行由 `Update` 收束时建，开轮只铸 id
    不建行，所以召回读到的永远是已收束的轮——一轮里召回几次都不会把自己写了一半的这一轮喂回模型
    （`TestOpenTurnIsAbsentUntilItSettles`）。轮中写的东西并不是看不见：按那个 id 走 `SearchL4`
    当场就读得到，收束之后成为这一轮自己的那几条消息。
    被下一次开轮**弃掉**的那一本同样不是空手：它 append 过的记录留在日志里，只是没有话题行，而那个 id 再没有
    读会点名，要等保留窗扫掉。所以「开了没沉淀的轮不留残渣」这句是错的——放弃一本记了东西的轮花的是空间，
    不是零（`TestAbandonedRoundKeepsItsRecords`）。

16. **两口时钟，分工写死**：宿主写的记录（`AppendArchive` 的每一条、`Update` 收的那一轮）带的是**调用方**
    的毫秒戳，留空（0 或负）与秒级/微秒级带内取值都在写边界被拒——**库不替谁补一个表**，因为保留窗量的就是
    这个值，替宿主盖钟等于替宿主决定一份转录何时作废。库自己派生的时间戳（计划节点的三个、画像的
    `UpdatedAtMs`、图槽的内容钟）才由库盖，同一刻度。两个写口共用 `content.checkTimestamp` 一道闸，所以
    「一个入口补表、另一个拒」这种分叉不可能出现（`TestAppendArchiveRefusesAnAbsentOrWrongUnitTimestamp`、
    `TestUpdateRefusesAnAbsentOrWrongUnitTimestamp`；负例＝让它在 `<= 0` 时盖当前毫秒，两格当场红）。

## 修改者义务

改动锁纪律、Dream 阶段划分或域生命周期时，必须同步更新本文件与
`internal/repo/agent.md` 中受影响的条目；在小方法包里改动契约时，同步该包
自己的 `agent.md`。

- 关键词提炼无本地兜底：LLM 输出不可解析即 `ErrLLM`（这一轮不产生话题），`internal` 根不初始化任何分词器。一轮的提炼与 Dream 的融合提炼共用 `llmops.ExtractKeywords`——它只吃一段文本，不认识记录结构。
- L0 蒸馏同样无兜底，且**「答非所问」与「答得少」分开判**：回包里 `emotion`/`mbti` 缺整块即 `ErrLLM`（这一轮不动画像），解码出的零值不去盖库里已蒸馏的那半；`per_node` 只认本次样本集里的 id，认不出的行在 `llmops` 内丢掉——带下去只会让一次抄错的 hex 被 L1 回填报成「一条记录读不回」，从此每次 Dream 都停在最后一步。三类调用（关键词、巩固、蒸馏）要多少输出都不越 `LlmConfig.MaxOutputTokens` 声明的端点上限（越过去是一次被端点直接拒掉的请求），截断升级花的是「端点上限减去 `llmops.ConsolidationMaxTokens`」那一段——宿主没抬过 `MaxOutputTokens` 时那一段是零，此时一条装不下的融合摘要就是该场景这次巩固的 `ErrLLM`（上限与升级路径写在 `llmops` 那一条上）；巩固 prompt 里的目标条数就是 `Defaults.DreamCompressMinTopics`，并且任何数字都排在「不同主题禁并」这条规则之后。
- `ImportL3` 的批校验：Title/Domain 必填在 composition root 判，导入模式在批次构造处判（`core.L3ImportMode.Valid()` 是唯一列词表的地方）——两者都排在任何读与写之前，拒批即一字节不写。批次创建时又把整池的三张索引一次读全（图槽 name→id、每图标题集、每图边键）——三者中任何一条记录读不回都让**整批**在第一个写入之前被拒、错误带着那条记录的 id。一个派生地址已被别的记录占着的条目（标签正好拼出某节点 id 那一类）是**单条**拒绝，走 `result.Errors`，同批其余照旧落地。
- 宿主面测试覆盖 25 个会话方法 + 7 个 `DB` 方法，按层分文件：`test/api_interface_scene_test.go`（L2 场景生命周期）、`api_interface_plan_test.go`（L5 按步骤逐个建的树、Model A 折叠与节点字段回读、事件键到自己那一轮、重开后读回）、`api_interface_turn_test.go`（一轮之下原文与事件各归各的读法）、`api_interface_multi_test.go`（租户隔离与 `CompactTo`）。这些用例里轮次与场景一律不回传——写入开着的轮靠的是 `Search` 之后库的自持字段，
只有读侧与改侧（`SearchL4{TopicID}`、`RenameTopic`、`DeleteTopic`、`SceneContext`）
还拿着库铸给宿主的 id。
