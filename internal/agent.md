# internal — 业务层契约（模块级 agent 上下文）

本包是 MemHop 的**业务层**。任何 AI agent 或开发者修改本层前必须先读完本
文件，修改后必须同步更新本文件。

## 唯一职责

编排 Search/Update/Dream/L0-L6 业务管线与多 agent 生命周期
（`agentContext`、`CreateAgent`/`ListAgents`/`DeleteAgent`），并以
`Session`（session.go）向 `api` 门面暴露按域绑定的公开操作面。

## agentContext 域级锁纪律

1. **先域锁后存储**：所有公开方法统一走 `db.lockAgent(agentID)`（内部：
   `contextFor` 取域 + `ac.mu.Lock()` + 锁内复检 `deleted` 墓碑），再进入
   存储读写；引擎自带的锁在内层，顺序不可颠倒。同 agent 串行、跨 agent
   并行。`contextFor` 对非默认域校验注册表：未注册/已删除的 agentID 直接
   `ErrAgentNotFound`，域永不复活；与删除对撞的陈旧句柄由锁内墓碑复检拒绝。
   L6 轨迹族统一走 `db.lockSession(agentID, turnID)`（lockAgent + hex 解析，
   解析失败先解锁）：裸事件的键就是该轮话题 ID（`AppendTrajectory` 顺手把
   `TopicID` 写成同一个值），计划绑定事件的键是计划 ID。门面侧的会话准入
   策略在 `CheckSession`。
2. **缓存刷新序**：写记录帧后紧跟 `ac.syncL2Meta`（**存储 -> l2meta**）；
   话题 BM25 索引随检索子系统退役，已无第三层。
   **禁止在域锁内取 `db.agentsMu`**（锁序环：sweep 走 agentsMu -> ac.mu），
   域内簿记（如 `lastDreamAt`）直接写 atomic 字段。
3. **Dream 域化**：`RunDream` 全程持本域锁；后台触发经
   `triggerSceneDream(ac, sceneID)`（调用方持 `ac.mu`），goroutine 运行在
   `ac.opCtx` 下——`Close`/`DeleteAgent`/空闲回收取消它，任何在飞 Dream 在下一
   阶段边界退出，绝不写入已销毁的域。域锁内的前台 LLM 调用（`Update` 的轮次
   提炼）同样挂 `ac.opCtx`，避免生命周期屏障被一次完整往返阻塞。
4. **空闲回收**：无后台定时器；`contextFor` 顺带清扫超
   `Defaults.AgentIdleTTLMs` 未访问的域（默认域豁免），回收前先对域锁
   `TryLock`：锁被占用（在飞操作）或 `dreamInFlight` 非空则跳过，留待下轮。
   回收时不快照任何东西：L2Meta 在下次访问时从记录重建，数据始终在文件里。
5. **DeleteAgent 顺序**：先摘租户映射（断绝新 `contextFor`）→
   `destroyContext`（取消 `ac.opCtx`）→ `ac.deleted` 墓碑（`lockAgent` 拿锁后
   复检，与删除对撞的在飞操作被拒）→ `ac.mu` 屏障等待在飞操作 → 引擎域删除。
6. **planCache 域内索引**：L6 计划聚合缓存 `ac.plans`（`plancache.go`）
**不内置锁**，完全依赖 `ac.mu` 串行（区别于自带
   RWMutex 的 `TrajIndex`）。所有计划写路径（节点增删改、事件绑定、
   `PlanReplace`、`SyncPlanTree`、Dream 清理）必须先取 `ac.mu` 再同步缓存；
   `newAgentContextLocked` 构建，idle 重建时一并重建。`SyncPlanTree` 整树同步
   只改节点结构/字段，**不产生 `plan_step` 事件**、不动事件 Seq 空间。
7. **planID 全零保留**：`AppendTrajectory` 写入的裸轮次事件恒为
   `PlanID=0`，故 `0000000000000000` 不是合法计划。五个计划入口
   （`PlanAppend`/`PlanCommit`/`PlanState`/`PlanReplace`/`SyncPlanTree`）
   一律经 `parsePlanID` 拒绝；绕过它直接调 `repo.DeletePlanRecords(0)`
   会删掉整个域的全部轮次事件。
8. **计划清理有界**：`l6_prune` 只豁免「持非 done 节点 **且** 窗口内仍有
   活动」的计划；宿主中断或放弃而静默超 `trajectoryRetention` 的计划照常
   清理并级联其绑定事件，否则废弃计划会让 L6 无界增长。

## 数据访问纪律

- 只经 `internal/repo`（及 `repo/core` 导出的 Slot 读写）访问数据；
  **禁止**直接操作帧、文件头、快照结构。
- `StorageEngine` 句柄由装配层 `config.go` 的 `Open(cfg, builtins)`
  唯一持有并注入 `DB.engine`；业务代码不得自行打开/关闭引擎。内建能力
  工具箱由 `api` 门面以 `fs.FS` 注入（api 传 `capabilities.FS`），
  `Open` 负责解析并 attach——internal 不得 import `capabilities`。
- **能力下沉**：算法与策略一律在 `internal/cap/<feature>` 能力包
  （engram L1 建边与遗忘衰减、llmops 四类
  LLM 提示契约与解析、capability 能力卡解析校验合并、profile 画像摘要
  与蒸馏生成、knowledge L3 节点合并策略）；本包保留"取数 → 调
  能力 → 落库"的编排，不做算法。LLM 传输策略（截断升级重试）在本包
  `llm_ops.go` 的 `Provider.ChatWithRetry`，prompt 构建属于 llmops。
- 新增能力时：先建 `cap/<feature>` 包（依赖注入 + 窄接口，禁止 import
  internal 根），再由本层组装；共享 DTO 下沉 `repo/core/model_dto.go`
  / `model_distill.go`，本包以恒等别名引用（`models.go`）。
- **会话面（session.go）**：`Session` 是绑定单个 agent 域的唯一对外操作
  入口；`api.Session` 只内嵌本类型做
  纯转发，api 侧禁止出现业务逻辑、格式化或域绑定代码。多 agent 是唯一
  模式：`NewSession(agentID)` 是唯一会话构造器，`DefaultSession` 已删除。
  `exports.go` 是给 api 门面的恒等再导出接缝（Slot 别名、枚举常量、
  Code/CodeOf、FormatAgentID/ParseAgentID）；`api` 包禁止直接 import
  `repo/core` 或 `common`。
- LLM 客户端是 DB 级共享（`db.llm`），由 `New(cfg)` 在装配时构造；任何域
  不自己建客户端。


## 读写路径契约（v1.5.0）

1. **一次 `Search` = 读场景 + 开一轮**：`scene_id` 为空 → `freshSceneID` 铸一个
   未被占用的 ID（`0` 跳过；只有 `ErrNotFound` 才算可用，其他读错误原样上抛）
   并落场景记录（名字一律库生成 `session:<id>`，入参不再有 `SceneName`）；
   非空且不存在 → `ErrNotFound`。同一批调用还经 `repo.OpenSceneTurn` 把场景的
   `TurnSeq` 推到下一轮，返回值 `NewTopicID = hash("turn:" + 场景:TurnSeq)`
   就是本轮要沉淀进去的话题。`Update` 一律拒绝未知场景，库内不再猜场景。
2. **`Search` 零 LLM、零话题写入**：唯一写是那一行场景记录（命中计数 + 轮次
   计数，写失败即报错——轮次号是铸 ID 的依据，不能吞），话题从 `ac.l2Meta` 取
   depth-1；`Scene.TopicCount` 用这批话题现算（该字段不落盘）。开了没沉淀的
   轮次不留任何残渣，读两次只沉淀一次就是跳号。
3. **`Update` 每轮一次提炼且排在写入前**：`ExtractTurnKeywords` 失败或空结果
   直接报错，此时话题/档案/L2Meta 一个字都没动。话题 ID 由宿主从 `Search`
   原样带回（`TopicID`，`0`/非 hex 拒绝），档案 ID 由 `(topic, ts, content)`
   派生，故同 `TopicID` 重放是覆盖而不是叠加。N:N 追加面（`AppendL4Message` /
   `RefineTopicKeywords`）已删除：一轮的原文恒为两条，轮内过程走 L6 轨迹——
   `AppendTrajectory`/`ReadTrajectory`/`Crystallize` 的轮键就是这个话题 id，
   引擎把事件的 `topic_id` 回填成同一个值；计划绑定事件改按计划 id 落键，
   一条事件只有一个归宿，不做双写。
4. **巩固按单场景规模触发**：`consolidateScene` 在 depth-1 话题数超
   `Defaults.SceneDreamTopicThreshold` 时调度该场景 Dream；`activeScenes`/`Capacity`
   窗口已删除，Dream 也不再合并场景（合并只走显式 `MergeScenes`）。
5. **L4 内容类型只在 `Update` 声明**：两侧档案按 `TurnUpdate.UserType` /
   `AgentType` 落类型（零值 `ContentText`，非文本侧存路径/URL），Dream 的融合
   摘要恒为 `text`。`SceneContext` 的 `Messages[].Type` 把它读回。
6. **`SetSceneName` 是 `SceneName` 的唯一写者**：场景记录只被 `OpenSceneTurn`
   读改写（它回填整条记录、只动计数），Dream 从不写场景记录，故改名不会被
   后续读取覆盖；`freshSceneID` 建新场景时才写默认名 `session:<id>`。
7. **`L4Refs` 无对话顺序，读回面自己补**：`UpdateTopicL4RefsL2` 按 id
   `DedupSorted` 存引用，而档案 id 由 `(话题, 时间戳, 内容)` 哈希得来，顺序与
   谁先说话无关。`sceneContextTopic` 因此按档案时间戳稳定排序 `Messages`——
   会话恢复必须"问在前、答在后"，别指望引用顺序。

## 单向依赖

`internal -> internal/cap -> internal/repo -> repo/{core,index}`，
外加 `common`；能力包之间互不 import（需要交互时回到组装根）。
禁止依赖 `api`、`cmd`、`capabilities`。`api` 门面（`MultiAgentDB`/
`Session`）是本层唯一对外出口。

## 修改者义务

改动锁纪律、Dream 阶段划分或域生命周期时，必须同步更新本文件与
`internal/repo/agent.md` 中受影响的条目。
