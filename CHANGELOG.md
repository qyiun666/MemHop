# Changelog

MemHop 遵循语义化版本。本文件记录每个版本的核心改动；完整历史见
README 的版本表与 git log。

## v1.5.0 — 2026-09-01

**L2 换轨：场景 = 宿主会话，轮次归库管。** `Search` 一次调用同时完成"读这个场景"和"开启这一轮"——返回该场景的 depth-1 话题集（宿主本轮该注入的上下文）与为这一轮铸出的话题 id；`Update` 把整轮沉淀进那个 id 并做全轮唯一一次提炼；L6 轨迹以同一个 id 为键。三通道打分检索、L1 扩散激活、话题向量质心与 embedding 依赖、以及 N:N 追加面（`AppendL4Message` / `RefineTopicKeywords`）整体退役。

### 破坏性变更

- **`Search` 语义重写：读场景 + 开一轮**：**无文本、无打分、零 LLM、零 embedding**，入参只剩 `{scene_id, l3_id}`，两者都可空——`scene_id` 为空 = 请库铸一个新场景（名字先由库生成 `session:<id>`，`scene_name` 入参删除——场景名改由 `SetSceneName` 单独写，MCP 侧即 `memhop_scene_rename`）；非空但场景不存在 = `ErrNotFound`（库内不再有"检索未命中自动聚类出新场景"）；`l3_id` 只在新建场景时生效。返回体 `{profile, profile_brief, scene, topics, new_topic_id}`，`topics` 是该场景的 depth-1 话题集（按用户消息时间升序）。**删除** `contexts` / `associated_contexts` / `auto_create` / `directed_l2_id` / `directed_l3_id`，门面签名同步去掉 `ctx`（读路径没有可取消的 LLM 调用）。
- **`SearchResult` 新增 `new_topic_id`**：本次读取为即将进行的这一轮开出的话题 ID，取 `hash("turn:" + 场景:轮次)`。为此场景记录新增 `turn_seq` 计数（老库解码缺省 0，首次读取即 1），格式版本仍 `0x0009`、不跑迁移。**`Search` 的场景写从 best-effort 变为必须成功**——轮次计数是铸 ID 的依据，写失败时读直接报错，不再降级返回一个可能重复的 ID；这也是整条读路径唯一的一次写。
- **`Update` 一次沉淀整轮到指定话题**：入参 `TurnUpdate{scene_id, topic_id, user_text, user_ts, user_type, agent_text, agent_ts, agent_type}`，返回该 `topic_id`。`Update` 不再自己派生话题 ID，只写 `Search` 铸出的那一个；`topic_id` 空 / 非 hex / 全零 → `ErrInvalidQuery`。一次 LLM 提炼出该轮关键词，且提炼排在所有写入之前——失败即报错且零留痕（不再有"半轮记忆"）。未知场景拒绝。同一 `topic_id` 重放是覆盖而非叠加，超时重试安全性不变；双时间戳退为纯时序字段，不再参与身份派生。
- **N:N 追加面整体删除**：`AppendL4Message`（"多对一"：把多条消息追加进已沉淀的话题）与 `RefineTopicKeywords`（"一对多"：按话题全量原文重算关键词）下线。一轮的 L4 原文恒为用户 + agent 两条；两条之间发生的事（工具调用、中间输出、子 agent 结果）是执行过程而非对话，归本轮 L6 轨迹。`Update` 因此是 L4 的唯一写入口，内容类型也在那里声明（见下条）。
- **L4 内容类型有写入口了 + 场景改名接口**：`TurnUpdate` 的 `user_type` / `agent_type` 声明两侧档案类型，零值即 `ContentText`，不填的宿主行为与上一版逐字节相同；非 `text` 侧把媒体路径或 URL 写在对应的 `*_text` 字段里（关键词仍从该字段提炼）。读回侧 `SceneMessage` 新增 `type`，场景上下文里能分辨一段散文与一个媒体引用。`SetSceneName(scene_id, name)` 是宿主给场景起人类可读标题的唯一入口（空名 `ErrInvalidQuery`、未知场景 `ErrNotFound`）：`Search` 每次读都在同一条场景记录上读改写命中计数与轮次计数，改名与之不冲突且不会被覆盖。
- **L6 轨迹按话题 id 绑定**：`AppendTrajectory` / `ReadTrajectory` / `Crystallize` 的轮键就是该轮话题 id（`Search` 给出），引擎同时把事件的 `topic_id` 写成同一个值，宿主侧自派生的轮键取消。计划绑定事件继续以计划 id 为键（落点二选一，不双写）。
- **`Crystallize` 不再折叠"同话题兄弟轮"**：新键模型下同轮事件本就在一条轨迹里，聚合逻辑与 `TrajIndex` 的话题桶（`TopicEvents`）一并删除，跨轮聚合单位回归为"一个计划 id"。
- **`TopicSlot` 单轨化**：删除 `user_keywords` / `agent_keywords` / `centroid_page_ref` / `l3_refs`，只留 `fused_keywords`（磁盘字段名不变）。Dream 压缩、L1 超边、宿主注入共用这一轨。不设摘要字段——原文始终在 L4。
- **场景 ID 由库铸造**：`NewSceneSlot(sceneID, name)` 不再由名字哈希派生 ID；`repo.CreateSceneL2` 换成 `CreateSceneL2WithID`（已存在即幂等复用、不改名），`freshSceneID` 循环取未占用的 8 字节 id（`0` 跳过）。`timestamp:文本` 自动命名场景的路径消失。老场景记录照旧可读。
- **检索子系统删除**：`internal/cap/scenefind`（三通道 BM25/向量/实体 + RRF 融合 + 场景加分 + L1 扩散）整包删除；话题质心、`RecVecCentroid` 记录类型、`Encoder`/`HttpEncoder` 与 `encoder_addr`/`embed_model`/`encoder_timeout_secs` 配置一并删除 —— **MemHop 不再需要 embedding 服务**。`gse` 只剩一个读者：`llmops` 关键词提炼失败时的启发式分词兜底。
- **`VectorDim` 从配置面删除**：既然没有任何向量读者，声明维度就成了纯粹的负担——宿主必须填一个无意义的数，填错还会让 `Open` 拒绝一个完全可读的旧库。`MemHopConfig.VectorDim`、`CheckVectorDim`、`ErrVectorDimMismatch`（码 1002，编号不复用）与 MCP `--vector-dim` 全部删除；文件头偏移 6 的两字节改为保留位（新库写 0，旧库原值保留），格式版本仍 `0x0009`。
- **死索引岛整体删除**：检索退役后 `index/sparse.go`（BM25）、`index/entity.go`（BK-tree 模糊匹配）、`index/l3_index.go` 与 `common/bktree.go` 在生产路径上零调用者——`QueryL3Nodes` 一直是记录扫描 + 子串匹配，从未走过这些索引。连带删除 `strutil` 的两个 Levenshtein 实现与零调用的 `common.FormatIDs`（约 800 行生产码 + 700 行测试）。**行为无变化**，只是不再让人误以为 L3 图检索有 BM25 排序。
- **巩固触发改轴**：`activeScenes` 窗口与 `capacity` 旋钮废弃（`Session.ActiveSceneIDs` / `HasActiveScenes` 同步删除）；`Update` 在该场景 depth-1 话题数超过新旋钮 `scene_dream_topic_threshold`（默认 24）时调度该场景 Dream。`RunDream(sceneID=0)` 改为遍历域内全部场景。
- **DSH 插件面随本版本退役**：`dsh/`（mcp 插件的 lib、面板源码、bundle 与安装脚本）与 `dsh-adapter/` 设计文档整树删除。Go 码、Makefile 与 CI 都不读这两棵树，`cmd/memhop-mcp` 原样保留并继续提供同样的三种 transport；本文件其余涉及 DSH 的条目描述的是删除前在那棵树里做的工作。
- **交付面**：MCP `memhop_search` 去掉 `scene_name`、出参带 `new_topic_id`，`memhop_update` 新增必填 `topic_id` 与可选 `user_type` / `agent_type`，轨迹类工具的 `session_id` 即该话题 id；新增 `memhop_scene_rename`；`memhop_status` 改报 `scene_count`；**删除 `memhop_scene_active_list`**（31 → 30 → 31 工具）；DSH 插件把铸出的 id 与本轮原文一起 pending（缺 id 就不沉淀），面板与循环同步适配。
- **工具面覆盖澄清**：31 个 MCP 工具投影 43 个公开会话方法中的 31 个；没有工具入口的 12 个是 L6 计划面全部 6 个（`PlanAppend`/`PlanCommit`/`PlanState`/`PlanReplace`/`SyncPlanTree`/`ListPlans`）、记忆纠错 2 个（`DeleteTopic`/`DeleteScene`）、场景管理 2 个（`SetSceneL3ID`/`ListScenesByL3`）、`DistillL0` 与 `AgentID`。此前文档写"全部公开 API"是不准确的，且把 `MergeScenes` / `SceneContext` 误列为 Go-only——它们分别有 `memhop_scene_merge` / `memhop_scene_topics`。

### 内部

- `repo.TouchSceneUsage` → `repo.OpenSceneTurn`：命中计数与轮次计数一次读改写，并把更新后的记录返回给调用方（读回自己刚分配的 seq，而不是旧快照）。
- `core.ComputeTurnTopicID(sceneID, seq)` 取代 `(sceneID, userTS, agentTS)` 派生。
- 随删除面一并清掉的孤儿码：`agentContext.loadTopicForWrite`、`repo.RefineTopicKeywordsL2`、`internal TrajectorySlot` 折叠用的 `sortTrajectory` / `trajectoryForCrystallize`、`TrajIndex` 的话题桶、`api/mapping.go:parsePtr`、`core.ReadHypergraphEdge`（本版发布前逐符号实测零调用后删除）。

### 修复

- **`SearchResult.Scene.TopicCount` 恒为 0**：该字段是派生值（只有 `ListScenes` 现算），场景记录里从不落盘，于是文档承诺的"读回时带话题数"实际一直是 0。现在 `Search` 用同一批已加载的 depth-1 话题直接填，与 `ListScenes` 报同一个数（回归测试 `TestSearchReportsSceneTopicCount`）。
- **DSH 插件无法起 server**：插件仍向 `memhop-mcp` 传已删除的 `-embed-model` / `-encoder-addr`，Go flag 解析器会以 "flag provided but not defined" 直接拒绝启动。embedder 相关配置从插件的 spawn 参数、wrapper 模板、配置读写与面板表单里全部删除；同时摘掉 `package.json` 指向不存在的 `scripts/install.mjs` 的 lifecycle 脚本。
- **新场景 ID 分配不再吞掉真实故障**：`freshSceneID` 以前把"读场景报任何错"都当作"该 id 未占用"，引擎关闭或 IO 故障时会铸出一个可能与既有场景冲突的 ID（两个宿主会话静默合并）。现在只有 `ErrNotFound` 算可用，其他错误原样上抛。
- **独立安装脚本的 wrapper 同样坏着**：`dsh/scripts/install.mjs` 生成的 launchd wrapper 仍带 `-embed-model` / `-encoder-addr`（它与插件那份 wrapper 是两处独立实现，第一次只修了插件那份）——npm lifecycle 脚本摘掉后这个文件仍可手动执行，一跑起服务就退出。现收敛为与插件相同的三参数形态（`-db-dir` / `-transport` / `-listen`）。
- **MCP server 不再自报旧版本**：`initialize` 返回的 `serverInfo.version` 取自 `cmd/memhop-mcp` 的 `version` 常量，还停在 `v1.4.2`——据此判断兼容性的宿主会判错。现为 `v1.5.0`。
- **DSH 侧"每租户一个 `.meh`"是假的**：插件头注释、`dsh/README` 部署模型与 installer 注入 `cordis.patch.yml` 的注释都写"独立 `.meh`（`dbDir/<session-id>.meh`）"，而 server 只在 `-db-dir` 下建**一个**共享 `memhop.meh`，租户是文件内的独立 agent 域（`cmd/memhop-mcp/registry.go:126`）。后果不止文档错：面板 `db:` 指向一个磁盘上永不存在的文件，turn 计数侧车也从这条假路径派生。现统一为"共享文件 + agent 域"表述，侧车改为直接按 tenant 键落盘（文件名与旧实现逐字节相同，无需迁移），`memhop__session` 增报 `tenant`。
- **`memhop__session` 的 `topicId` 有了真来源**：检索退役那一版起 `search` 一度不再返回话题 id，该字段只剩初值 `null`；现在由 `search` 的 `new_topic_id` 直接填充，宿主的面板轮次视图与引擎落笔的话题从此同源。
- **交付文档的数字与死链**：`capabilities/README` 的"MCP 31 工具"先改 30（与当时 `smoke_test.go:36` 的断言一致，`memhop_scene_rename` 加入后为 31，两处同步）；`dsh/README` 的 server 启动示例换成二进制真正接受的 flag（旧示例照抄必然起不来——它传的 `--embed-model` / `--encoder-addr` 已随 embedding 一起删除）；其首段指向 `docs/dsh-memhop-integration-plan.md` 的链接改为纯文本路径（`docs/` 有意不入库，公开 clone 里是死链）。
- **公开会话方法计数纠正**：文档写"30 个工具 / 42 个公开会话方法"，按 `api` + `internal` 导出方法集实测，在 `AppendL4Message` 与 `RefineTopicKeywords` 还在时是 44 个。这两个方法删除后为 42，与文档一致；本版加入 `SetSceneName` 后为 43。
- **`memhop_profile_update` 会抹掉 Dream 蒸馏出的画像**：`UpdateL0` 是全量覆盖，而该工具只暴露名称/角色/个性/偏好四项、没有任何回填通道——LLM 改一次名字就会把该域的情绪状态与 MBTI 倾向清零。现在工具先 `GetL0` 读回现画像，只替换它有权改的四项，其余按库内现值写回。
- **`memhop_profile_get` 描述在说谎**：仍列 v1.4.1 就删掉的"词表、风格与情绪模式"，说明书卡已改而工具描述漏改。现按 `ProfileSlot` 实际字段（含 Dream 蒸馏的情绪状态与 MBTI）表述。
- **MCP 工具计数的注释与实际不符**：`registerTools` 注释写"33 tools"，实际注册 30 个（现 31）。计数唯一的机器校验在 `smoke_test.go` 的 `tools/list` 断言里，注释同步为实测值。
- **`SceneContext` 的消息可能「答在问前」**：话题的 `L4Refs` 按 id `DedupSorted` 存储，而档案 id 由 `(话题, 时间戳, 内容)` 哈希得来——引用顺序与说话顺序毫无关系，场景 id 又每次新建都走 `crypto/rand`，于是同一份测试在不同库上会拿到不同顺序的消息。现在 `sceneContextTopic` 按档案时间戳稳定排序，会话恢复读回恒为「问在前」（回归测试 `TestUpdateStoresDeclaredContentTypes`）。
- **`MultiAgentDB.Lock/Unlock` 的文档像并发锁**：原文"serializes the default agent domain against host-side writes"极易被读成"宿主调用前要自己加锁"。事实是每个业务方法已在所属域内串行、跨域本就可并发，这对方法只用于宿主在库外碰同一个 `.meh`（备份/复制），且只冻结默认域。实现不动，注释改为写清用途与不锁其他域的后果（多租户文件要备份请 `Close` → 复制 → `Open`）。

### 兼容

- **不 bump `FormatVersion`**：`TopicSlot.UnmarshalJSON` 在解码点把 v1.4.x 记录的 `user_keywords`/`agent_keywords` 归并进 `fused_keywords`，`turn_seq` 是增量字段，老 `.meh` 直接打开且不丢既有关键词；下一次写回自然收敛为单轨。
- 话题 ID 走 `"turn:"` 命名空间（`ComputeTurnTopicID`），与 Dream 融合节点的 `ComputeTopicID` 分域，两者不会相撞；删除生产零调用的 `ComputeTopicIDForText` 与死字段 `SceneNode.VectorPageRef`。
- 场景归并不再发生在 Dream 内（会连带删掉宿主正持有的 sceneID），合并场景仍是宿主显式调用的对外接口 `MergeScenes`。
- L1 层保留（节点同步 / 关键词 Jaccard 建边 / 衰减 / 重要性反馈，全部改吃单轨关键词），但删除扩散后库内暂无读者。

### 实测基线

热路径 LLM 调用从每轮 2 次（Search 提炼 + Update 提炼）降到每轮 1 次且只在写路径；`Search` 变为一次场景记录读改写 + 一次 L2Meta 内存读，不联系 LLM 与 embedding 服务。检索质量类断言随之重定位：`test/` 黑盒不再测"跨会话召回"（该能力已按设计移除），改测整轮沉淀、场景读回与巩固后不丢事实。

宿主可依赖的不变量测试：`TestSearchOpensOneTurnPerRead`（一次读开一轮，且开轮不建任何话题记录）、`TestUpdateSettlesEachScenesTurnsInOrder`（各场景轮次互不串台、按序读回）、`TestUpdateReplayIsIdempotent`（同 `topic_id` 重放只覆盖不叠加：1 个话题 + 2 条档案）、`TestUpdateStoresDeclaredContentTypes`（两侧各按声明类型归档，未声明的一侧保持 `text`，场景上下文按时间戳读回「问在前 + 类型」）、`TestSearchReportsSceneTopicCount`（读回的 `TopicCount` 与 `ListScenes` 一致）、`TestSetSceneNameSurvivesLaterTurns`（改名后被 `Search` 读改写命中/轮次计数覆盖不掉，且空名与未知场景各按码拒绝）、`TestTurnRunsOnOneTopicID` 与 `TestCrystallizeReadsOneTurnTopic`（轨迹追加/读取/结晶三条路径同键）、`TestSurfaceTurnFlow`（门面侧一轮闭环）、`TestSSETurnFlow`（MCP over SSE 端到端：`search` 铸 id → `trajectory_append` → `update` → 复读看到该话题，缺 `topic_id` 的 `update` 被拒）。

## v1.4.2 — 2026-08-31

**L6 计划树 + L2 目录归属**：轨迹层承载可折叠的任务树（三形态写入 + 整树同步），场景固定挂到 L3 项目域。

### 新增

- **L6 计划树（三形态）**：`TrajectorySlot` 用 `NodeType` 区分轨迹事件与计划节点，节点 ID 由 `HashPlanNode(planID, nodePath)` 稳定派生（`plan:` 前缀命名空间，不与事件 `hash(sessionID:seq)` 相撞），事件经 `PlanNodeRef` 挂节点
  - `PlanAppend(planID, nodePath, ev)` 只追加一步不推进计划；`PlanCommit(..., status, summary)` 推进状态并追加一步；`PlanState(planID)` 读树。节点缺失时按路径逐级补建为 pending，宿主只管理 `NodePath`（`"1"` / `"1.2.1"`）
  - **Model A 显式折叠**：父节点只由宿主显式 commit 为 `done`，库内不因"子节点全 done"自动提升；每次 commit 后自底向上把已 `done` 子节点的 `Summary` 以 `; ` 汇总进父节点（`NodePath` 数值段稳定排序，`1.10` 排在 `1.9` 后），且保留宿主自己的父摘要
  - `PlanTree.Roots` 是**森林**：flat 步骤列表每个顶层步骤各为一个根，`DoneCount/TotalCount` 覆盖全部根；父记录缺失的节点提升为根而不丢弃子树
- **计划重规划与整树同步**：`PlanReplace(planID, rootTitle)` 清空一个计划的节点与绑定事件、保留 planID（非空 `rootTitle` 播一个带标题的 pending 根）；`SyncPlanTree(planID, *PlanNode)` 以宿主快照为准整树增删改（按路径对齐、消失的分支连同绑定事件级联删除），**不产生 `plan_step` 事件**、不动事件 Seq 空间；`ListPlans()` 输出域内每个计划的足迹（planID / 节点数 / done 比 / 首末活动时间 / 是否仍活跃），供宿主重启后恢复树
- **L2 场景 ↔ L3 目录域（N:1）**：`SceneSlot.L3ID` 为场景固定归属；`SearchQuery.L3ID` 可选——有值时候选场景先按项目域筛选，命中无锚点的场景时回填；`ListScenesByL3(l3ID)` 按项目列场景；`SetSceneL3ID(sceneID, l3ID, force)` 正常路由为写一次，`force=true` 纠正错挂、空 `l3ID` 清除锚点
- **`planCache` 域内索引**（`internal/plancache.go`）：按域缓存每个计划的节点与绑定事件计数，`PlanState`/`ListPlans`/rollup 不再每次全扫引擎；随 `agentContext` 构建、idle 回收时一并重建；不内置锁，完全依赖域锁 `ac.mu` 串行
- **api 常量导出**：`Role*`、`NodeType*`、数值 `Status*`（只用于读 `TrajectorySlot.Status`）与字符串 `PlanStatus*`（`PlanCommit` 入参 / `PlanState` 出参），并导出 `api.PlanStatus` 类型；`StatusRunning`（`running`）为第五个计划状态

### 变更

- **计划写入面收敛为权威语义**：`AppendTrajectory` / `PlanAppend` / `PlanCommit` 一律强制改写记录的 `NodeType/PlanID/ParentID/NodePath/Status/Summary`（及 `Seq`），宿主在这些字段上传值会被忽略——事件不能伪装成节点、节点树不会被注入脏记录；`PlanAppend`/`PlanCommit` 的事件 `EventType` 限定在既有 9 类加 `plan_step`
- **`planID` 全零保留**：`0000000000000000` 是裸轮次事件的 `PlanID` 哨兵，五个计划入口一律以 `ErrInvalidQuery` 拒绝（此前 `PlanReplace` 传全零会删掉整个域的全部轨迹事件）
- **`l6_prune` 计划豁免改为"活动期内"**：只豁免既持非 `done` 节点、又在 7 天窗口内有过活动的计划；宿主中断/放弃而静默超窗的计划按常规清理（连同绑定事件级联），保证 L6 有界
- **`Search` 每轮必建新话题**：话题稀疏索引写入不再以"本轮新建"为条件（三条路由都建话题）
- **无格式变更**（仍 `FormatVersion 0x0009` / `SnapshotVersion 0x0002`）：计划字段与 `L3ID` 都是 JSON 增量字段，v1.4.1 的 `.meh` 文件直接打开，无需迁移
- **交付面**：计划树与 L3 场景锚定本次**只在 Go module 暴露**，`cmd/memhop-mcp` 的 31 个工具未接入，经 MCP 接入的宿主（DSH 插件）暂时拿不到这些能力

## v1.4.1 — 2026-08-28

**类型契约清理**：api 出参 ID 全量 16 位 hex、L0 画像 v2（字段所有权）、库内零往返。

### 变更（含破坏性）

- **api 面（破坏性）**
  - 出参 DTO 改为真实 struct（`api/types.go` + `api/mapping.go` 显式映射）：`TopicSlot`/`SceneSlot`/`SearchResult`/`Hypergraph*`/`ArchiveSlot`/`Capability`/`TrajectorySlot`/`L3Graph`/`L3Subgraph` 全部 ID 字段出参 16 位 hex 字符串；`SearchResult.NewTopicID`、`AppendL4Message` 返回值、`Session.AgentID()` 同步
  - 新增记录级 ID 工具 `api.FormatID` / `api.ParseID`（宿主不再需要自带 hex 格式化）
  - `ProfileSlot` DTO 删除 `IDHash`（UpdateL0 强制覆盖，宿主无感知）
- **L0 画像 v2（存储格式 0x0009，旧文件 Open 即拒绝）**
  - 字段所有权：Name/Role/Preferences 宿主独占（Dream 永不改写）；Personality 宿主播种、Dream 蒸馏演化（蒸馏契约新增 personality 输出，≤160 字符证据归纳）；`EmotionState`/`MBTI` 为 typed 蒸馏信号，替代字符串编码的 emotion_patterns/mbti 混写
  - 删除死字段 `lexicon`/`style_traits` 与关键词投影阶段（Dream 阶段少一级 `l0_profile`）
- **库内零往返**
  - repo 层 ID 入参 uint64 化：Search/Update 每轮 4~5 次 `FormatHash`→`ParseID` 往返清零
  - 质心哈希改 `HashBytes` 字节直算（每次 Search 省一份向量拷贝）；`FormatHash` 改位操作零 fmt
- **LLM 契约健壮性**
  - distill / consolidate 解析失败各补一次格式约束重试（对齐 keywords 自愈模式）
  - distill `per_node` 限 top-20（首轮 2048 token 预算内）；MBTI `type` 改由四维重导出，不再信任 LLM 输出；清理 prompt 死参数（summary/depth）

- **L3 超图激活与 L4 内容类型落地**
  - `L3ImportItem` 增 `source_ref`（位置引用落节点 `SourceRef`，knowledge 合并契约同步增参：Merge 仅非空刷新、Overwrite 全量替换）与 `related`（同图内按标题建边，两阶段解析支持前向引用；边 ID 哈希排序节点对，重导入幂等不重复建边）；`L3ImportResult` 增 `edges_created`；api 导出 `L3Relation` 类型
  - `AppendL4Message` 增 `contentType` 入参（未定义值拒绝），api 导出 `Content*` 七常量；内容约定：text/document/code 的 Content 存原文，image/audio/video 存路径或 URI（mime/size/sha256 走 Metadata）；`L4Query` 增 `Type` 过滤，MCP `memhop_archive_search` 同步 `content_type` 参数

- **L6 轨迹重构与对外面收敛（破坏性）**
  - 每轮一条轨迹：`SessionID` 改为轮键（search 开轮、update 收轮，宿主每轮派生新 16 位 hex）；`TrajectorySlot` 删 `L4Ref`、增 `TopicID`（结晶按同话题聚合跨轮轨迹，payload 上限 128KB）；event_type 为轮内步骤分类（llm_request/llm_output/tool_call/tool_result/subagent_spawn/subagent_done/context_inject/ask_user/user_reply）
  - 对外面只剩追加与查询：`AppendTrajectory` / `ReadTrajectory` / `ListTrajectorySessions`；删除 `TrajectoryStats` / `DeleteTrajectory` / `PruneTrajectory` 与 MCP `memhop_trajectory_stats` / `memhop_trajectory_delete`（33 → 31 工具）
  - 保留期内置：Dream 新增 `l6_prune` 阶段，自动清理 7 天前的事件（`TrajIndex` 支撑 O(1) Seq 分配、轮枚举与按期清理）；注入层删除 `memhop-trajectory` 卡（7 → 6 张），轨迹记录并入宿主自动循环

## v1.4.0 — 2026-08-26

**多 agent 记忆数据库**：一个 `.meh` 文件承载多个完全隔离的 agent 域。

### 新增

- **存储层（`internal/repo/core`）**
  - 记录帧 18 → 26 字节：`type(1) flags(1) length(4) agent_id(8) id_hash(8) crc32(4)`，CRC 覆盖头+数据
  - 引擎索引两级分域：`agent -> idHash -> offset` 与 `byAgentType`；新增 `IterAgents()`（`iter.Seq[uint64]`）、`DeleteAgentRecords(agentID)`
  - 快照格式 0x02：按 agent 序列化稀疏索引（`AGENT_COUNT [agentID blob]...`）
  - 新增 `RecAgentRegistry (0x10)` 注册记录：`crypto/rand` 8 字节 agentID，data 为 agent 名 JSON；Open 时扫描重建 `name -> agentID` 映射（同名复用、不同名永不碰撞，替代无状态哈希）
- **业务层（`internal`）**
  - `agentContext` 每 agent 业务态：域级锁（同 agent 串行、跨 agent 并行）、独立稀疏索引 / L2Meta / 活跃场景 / Dream 簿记 / `dreamCtx`
  - 空闲域内存回收：`Defaults.AgentIdleTTLMs`（默认 60 分钟），无后台定时器，随访问清扫，数据仍在文件
  - `CreateAgent(name)` / `ListAgents()` / `DeleteAgent(agentID)`（域墓碑 + 取消在飞 Dream → 域锁屏障 → 引擎域删除）；删除后的 agentID 永不复活（`contextFor` 校验注册表）
  - `DreamReport` / `DreamStage`（各阶段状态与耗时、`L0Updated`）与 `DistillL0`（独立触发 L0 蒸馏）；L6 新增 `ListTrajectorySessions` / `PruneTrajectory`
- **api 门面**
  - `OpenMulti(cfg) (*MultiAgentDB, error)`、`OpenMultiWithEncoder`
  - `AgentSession`：方法集对齐单 agent `DB`（Search/Update/Dream/L0–L6 全量）
  - `FormatAgentID` / `ParseAgentID`（16 位 hex）
  - 新错误码 `ErrAgentNotFound` (3002)：agentID 未注册或已删除
  - `Open` 保留且宿主零改动（内部映射默认域 `DefaultAgentID = 0`）
- **MCP（`cmd/memhop-mcp`）**
  - registry 共享单个 `MultiAgentDB`：`/mcp/<tenant-id>` → `CreateAgent(tenant)` 稳定 agentID → `Session`；单文件 `<db-dir>/memhop.meh`
  - `os.Root` 锚定 db 目录（替代 `filepath.Dir` 比较），路径穿越防御升级
  - 新增 `memhop_trajectory_sessions`（域内会话清单，发现可结晶/可清理会话），工具总数 32 → 33
- **内置 L5 能力卡（`capabilities/`）**
  - 工具箱重构：19 → 7 张全英文卡（`memhop-guide` 循环分工总纲 + 卡索引；knowledge/scene/archive/profile/trajectory/capability 六张 LLM 可调用说明书；capability 卡合并 crystallize + import）；宿主自动循环（Search/Update/Dream）不再做卡
  - 分层注入契约：默认只注入一行索引（`id + name + summary + trigger`，≈300–500 token）+ guide，参数详情按需 `GetCapability(id)`（仅收 16 位 hex）
- **边界固化**：新增 `internal/agent.md`（业务层契约：域级锁纪律、锁序 存储→l2meta→sparse、Dream 域化）与 `internal/repo/agent.md`（存储层契约：域化原语、实现不外露、单向依赖）

### 变更

- **层编号收敛**：原 L6 Scene Usage 已在 0x0007 并入 SceneSlot，编号空出；轨迹层从 L7 下沉为 **L6**，认知层收敛为 L0–L6 七层（`RecL6Trajectory` 值 0x0E 不变）。文件改名：`api/l6.go`、`internal/l6.go`、`internal/repo/l6layer.go`、`cmd/memhop-mcp/tools_l6.go`；MCP 工具名不变（语义命名，不含层号）
- **去重与转换层消除**：删除 `topicSlotJSON`（core.TopicSlot 镜像）；`topicToL2Meta` 与 `L2MetaFromTopic` 合并为单一 `L2MetaFromTopic(*core.TopicSlot)`；`ReadTopicSlot` 返回 `*TopicSlot`（去掉单元素切片包装）；`CompressTopicsL2`/`MergeScenesL2` 复用 `core.TopicEntry` 单点序列化
- **Go 标准库现代化（零新依赖）**：`L2MetaIndex.Iter` 返回 `iter.Seq2[uint64, *L2Meta]`；`unique.Make` 驻留稀疏索引词项；`os.Root` 租户路径校验
- 版本常量：`cmd/memhop-mcp` → `v1.4.0`

### 破坏性变更

- **格式不兼容**：`FormatVersion = 0x0008`；`<= 0x0007` 的旧 `.meh` 文件在 Open 时被显式拒绝，**无迁移路径**
- `internal` 层全部读写函数签名加 `agentID` 首参（仅影响直接依赖 `internal` 的代码；`api.Open` 宿主不受影响）
- `api.DB` 上提升自 `internal.DB` 的方法随之新增 `agentID` 参数（门面方法签名不变）；`Lock()` 对已关闭的 DB 会 panic（与旧无条件锁同契约）
- 并发契约变更：同 agent 串行、跨 agent 并行由库内域级锁保证，宿主无需自行排队
- `Session.Dream` 返回值 `bool` → `*DreamReport`；`consolidated` 语义收窄（仅当实际巩固了 ≥1 个场景才为 true，no-op 返回 false）
- 内置能力卡集合重定义：删除全部 `agent-*` 原子卡与 `memhop-search/update/dream/refine/crystallize/capability-import` 卡（后两者并入 `memhop-capability`），卡片内容全部英文化

### 测试

- 新增 `TestAgentDomainIsolation`（两 agent 同文件同 idHash 互不可见）、`TestDeleteAgent`（全域清除 + ListAgents 一致 + 默认域拒删）、`TestAgentRegistryStableAcrossRestart`（名称映射跨重启稳定）
- 回归：默认域下三通道检索、Dream 五阶段、`go test -race` 全绿
