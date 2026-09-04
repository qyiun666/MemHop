# Changelog

MemHop 遵循语义化版本。本文件记录每个版本的核心改动；完整历史见
README 的版本表与 git log。

## v1.6.0 — 2026-09-04 — 接口去 fallback 与按层闭环修复（实测驱动）

按层审查公开面（用 `ImportL3` 把本仓 24 个包 / 78 条依赖边真实导进 L3 超级图，79 条断言逐条实测），据结果修如下一轮；本轮的共同原则是**接口不允许任何 fallback：有问题就返回 error，被拒的写入一字节都不留**。

- **P0｜`PlanCommit` 不再部分生效**：事件校验原先在 `AppendEventLocked` 里，而它跑在 `EnsureNode` + `UpdateNodeLocked` **之后**——一个缺 Timestamp / 词表外 `EventType` 的 commit 会报错，却已把节点推进到 done/failed 并写好摘要，且跳过 rollup（实测 `done 0/2 → 1/2` 伴随一条 error）。校验提到改树之前（新 `plan.ValidateEvent` / `trajectory.ValidateEvent`），实测四类被拒 commit 之后树与事件都零变化（`TestPlanCommitRejectedLeavesTreeUntouched`）
- **P1｜Skip 重导不再吃边**：`ImportL3` 过去只给「节点本次落库」的条目建边，删过节点再 Skip 重导就只能恢复该节点自己的出边（实测 16 条入边只剩 1 条，且不报错）。边早按「排序成员 + kind」去重，于是对每个条目都建边（实测 24 包图恢复后 incident=16/16）
- **P1｜`UpdateL3` 改名不再被撤销**：图 id = `hash(Domain)`，而 `ImportL3` 过去无条件重写槽记录，用原 domain 再导一次就把宿主改好的名字静默改回、`CreatedAt` 一并重置。新增 `repo.EnsureGraphL3`：槽存在就原样复用
- **P1｜`QueryL3Nodes` 口径统一**：对已删图过去静默返回空（`GetL3`/`DeleteL3Nodes` 都报 not found），现在一律先校验图存在；请求里无法解析的节点 id 过去被丢弃，现在 `ErrInvalidQuery`
- **P0｜删掉关键词提炼的启发式兜底**：LLM 输出不可解析时过去降级为 gse 本地分词、把假关键词写进话题且返回 nil error；现在格式约束重试仍失败即 `ErrLLM`，分块路径任何一块不可解析也报错（`Update` 那一轮不落库）。**连带效果**：`index.Tokenize` 失去唯一生产读者 → 删除 `internal/repo/index/tokenizer.go` 与 `internal/tuning.go`，直接依赖 5 → 4（gse 出局），`common.TruncateUTF8` 同批删除
- **P2｜`Search` 不再静默丢弃 `L3ID`**：读一个已存在的场景时传锚点过去被整个忽略，现在视为请求冲突报错（改锚点用 `UpdateScene`），锚点指向不存在的图也报错
- **P2｜`ImportL3` 批校验诚实**：`Title` 为空的条目过去被 `continue` 静默丢弃（不进 CreatedIDs / SkippedCount / Errors），`Domain` 为空则造出一张 `hash("")` 的无名图；空 mode 过去默认成破坏性最强的 Overwrite。现在整批先校验、拒掉就一字节不写
- **P2｜L6 读面不再有死字段**：实测读回一条绑到步骤 `1.1` 的事件，`NodeType/ParentID/Status/Summary/PlanType` 恒为零（每条写路径强制清零，而计划节点记录不经任何公开读面）——这五个字段连同 `api.Status*` / `api.NodeType*` 两组常量一并从公开面删除；`api.TrajectorySlot` 收缩成读写共用的一份真形状
- **P1｜计划事件可归位**：`PlanNodeRef` 是库内 hash，宿主没有任何公开途径反查，`NodePath` 又恒空 → 「这条事件属于哪一步」读不出来。现在写库时由**库**给事件盖上所属节点的 `NodePath`（伪造依旧不可能：入参里已经没有这个字段可传）
- **`AppendTrajectory` 超预算 payload 改为拒绝**（原截断到 4 KiB），`trajectory.ReadTurn` 不再静默跳过读不动的记录（瞬时错误一律上报，对齐仓内错误策略）
- **P1｜L3 的「超级图」接通了写入端**：一条 `L3Relation` 现在声明它的全部另一侧 `Titles []string`，成员集合 = 本条目 ∪ Titles，所以「A、B、C、D 属于同一组」是一条 4 元边而不是 6 条两两边。存储层本来就支持任意元（边 id 哈希排序后的成员集 + kind、BFS 把成员两两连通、`DeleteL3Nodes` 按任意成员命中级联）——只有导入面把它压成了二元。实测把本仓 24 包 78 边导进图后，再加一条 4 元 `part_of`，成员宽度直方图从 `map[2:78]` 变成 `map[2:78 4:1}`，一跳 BFS 从任一成员到达另外三个，重导不复制，删掉任一成员整条超边级联消失（`TestImportL3NaryHyperedge` / `TestImportL3HyperedgeStaysOneEdge`）。关系成员非法（无目标 / 空标题 / 自指 / 重复 / 目标不在本图）逐条记进 `result.Errors` 且不建边；MCP `memhop_knowledge_import` 的 schema 与 `memhop-knowledge` 说明书卡同步
- **P1｜L2/L4/L6 读路径不再静默少条**：`repo.ListScenesL2` / `repo.CollectAllScenesL2` / `repo.QueryArchivesL4`(按 id 快路径) / `scene.ContextTopic` 遇到读不动的记录一律上报，只有「记录确实不存在」才跳过（`Update` 重放会合法 retiring 被替换那一轮的旧 id，这条边界由 `TestContextTopicRetiredRefIsNotAnError` 钉住）。同批：L1 衰减扫描 `decayRemainingEdges` 与级联摘边 `removeEdgeFromNode` 也不再吞读错误
- **P1｜Dream 的 usage-feedback 不再"尽力而为"**：它过去注释里就写着 best-effort、失败只 warn，而随后的 L1 重建/衰减正是按这些 importance 走的——静默跳过会让报告声称做过了其实没做。现在它单独成阶段（`DreamStage` 名增 `usage_feedback`）并把错误上抛
- **P1｜关键词提炼的两条路径统一硬度**：单趟路径有「三档 token 预算 + 一次格式约束重试」，分块路径每块却只有一次机会——越长越容易跑成自然语言摘要的输入反而更软，实测 DeepSeek 就在一个分块上吐非 JSON 导致 `Update` 失败。抽出 `extractOne` 让每个分块走同一阶梯（仍无任何启发式兜底）
- **测试自身的 bug**：`TestCoreCycleUpdateDream` 用 `want[:18]` 取关键词探针，按**字节**切进 3 字节汉字里，非法 UTF-8 永远匹配不上 Content——看起来像"Dream 之后 L4 丢了 4 条原文"，实际 10 条里恰好是那 4 条切在 rune 中间（逐条核对 24 个事实的字节边界，失配集合与报告的丢失集合 1:1）。改成按 rune 取前 12 字
- **接口测试补强**：新增 `api/surface_closed_loop_test.go`（9 个用例：畸形批 / 全字段读回 / 三模式幂等 / 删点重导补边 / 改名存活 / 已删图三口径一致 / 锚点与图面一致 / 被拒 commit 零副作用 / LLM 不可解析时 Update 不写），并重写 `internal/llm_ops_test.go` 为「不可解析即 ErrLLM」
- **P1｜删除面最后两处静默成功**：沿「宿主重复调用能不能分辨做了与没做」把删除面扫完，剩两处返回 nil 而什么都没删——`DeleteCapability` 删一张不存在的卡（`DeleteScene`/`DeleteTopic`/`DeleteL3Nodes` 都报，唯独它不报），以及 `DeleteAgent` 删一个注册表不认识的 id（同一个 id 拿去 `Session` 会被 `ErrAgentNotFound` 拒，删除却报成功、而它确实没有记录可删）。现在两处都先查再删，未知即 `ErrNotFound` / `ErrAgentNotFound`；顺带删掉 `DeleteAgent` 失败回滚路径里因前置校验而永假的 `name != ""` 分支（`TestInterfaceCapabilityLifecycle` / `TestInterfaceAgentDomainsAreIsolated`）
- **P0｜`SyncPlanTree` 删分支不再留下指向已删记录的索引**：宿主每轮推部分快照，LLM 缩步骤是常态路径。vanished 分支的删除原先只镜像到计划缓存（`ac.Plans`），`ac.Traj` 仍命名被删掉的事件记录——于是该计划之后**每一次** `ReadTrajectory` / `Crystallize` 都报 `ErrIO: record not found`，且要到进程重启（索引从记录重建）才自愈。现在 `repo.DeletePlanNodeBranch` 返回它删掉的记录 id，新增 `TrajIndex.RemoveEvents` 把它从事件索引里镜像掉，`SyncPlanTree` 两份缓存一起同步（`TestInterfaceSyncPlanTree` / `TestTrajIndexRemoveEvents`）
- **P0｜`MergeScenes` 校验它点名的每个 id**：过去只校验 id 能否解析，而底层 `DeleteL2(DeleteScenesL2)` 直接按传入 id 批量删。传一个宿主已经删掉的 secondary id 时调用返回成功却什么都没合并，更糟的是那串 id 里若命中主场景自己就把存活场景的记录一并删掉。新增 `requireScenes`（主 + 次逐个回读，必须是现存场景，否则 `ErrNotFound`；读不动原样上抛）（`TestInterfaceMergeScenes`）
- **P1｜Dream 融合后的场景转录顺序确定**：融合父话题的时间戳等于它吞掉的第一轮的 `UserTimestamp`（`ComputeTopicID(sceneID, minTS, maxTS)`），两者必然同值；`repo.ListTopicSlots` 只按时间戳单键排序，而 `slices.SortFunc` 不稳定——一组的摘要（`RoleDream` 那条）会随机落到被它总结的原文中间，同一份数据两次读出的顺序可以不同。加 `Depth` 次键，浅的在前，摘要总在它总结的原文之前（`TestInterfaceSceneContextReadsThroughFusion`）
- **宿主面接口测试补到全覆盖**：34 个会话方法 + 8 个 `MultiAgentDB` 方法此前各有 9 / 3 个在 `test/` 里零可达（整个 L6 计划树面、L5 生命周期、`DeleteL3Nodes`、租户管理与 `CompactTo`）。新增按层四个用例文件——`api_interface_scene_test.go`（L2 改名/锚点/`Force`/转录/合并/删除）、`api_interface_plan_test.go`（计划 id 可复现、整树同步与"未填即继承"、commit rollup 与被拒零副作用、轨迹双键与 Seq 空间、`Crystallize(planID)`、`PlanReplace`）、`api_interface_capability_test.go`（导入→改定义→用量回流→弃用/激活→重导入两条路径、内建卡四类写全拒、重启后按名回到同一域）、`api_interface_multi_test.go`（租户隔离、注册表、`DeleteAgent` 后陈旧句柄报错、`CompactTo` 三条拒绝与副本可开）；`api_interface_l3_test.go` 补 `DeleteL3Nodes` 级联。**同时清掉测试自身的伪证**：`api_interface_l5l6_test.go` 的轨迹键原本是手打的 `"0000000000000001"`（宿主造不出也拿不到这种 id），现改为 `Search` 铸出、`Update` 沉淀过的真实轮次 id；`api_interface_test.go` 里伪造 id 的负面用例改成"拿另一个真实会话的轮次 id 去写"。离线套件 `go test -count=5 ./test/...`、真实 LLM 套件 `go test -tags integration ./test/...` 均全绿
- **P1｜L3 图改名不再撞开路由歧义**：`UpdateL3` 不查名字占用，把图 A 改名成既有图 B 的 domain 后，两张槽同名；而 `graph.NewImportBatch` 用 `graphIDs[g.Name] = g.IDHash` 逐条覆盖播种、`core.IndexByType` 又是 map 迭代顺序——**同一个 `Domain` 下一次导入进 A、下一次进 B**。节点 id = `hash(graphID:title)`，于是同一标题在同一 domain 下随机落到不同图，重导幂等性随之失效。两处一起修：`graph.CheckName` 在写入端拒掉撞名改名（`ErrInvalidQuery`，被拒的调用不动槽记录），`preferGraphID` 让读取端对**已经带着撞名的旧文件**也确定——id 由该名字派生的那张图拥有这个名字，全等平手取较小 id。实测撞名后连导同一 domain 20 次，修复前 4 次进错图、修复后 30 次全进对图（`TestUpdateL3RejectsNameCollision` / `TestImportL3NameCollisionRoutesByDerivation` / `TestSurfaceL3GraphLabelIsUnique`）
- **P1｜`DeleteL3` 级联清掉场景锚点**：锚点两条写路径（`Search{L3ID}` / `UpdateScene{L3ID}`）都校验「图必须存在」（实测各报 3001），删除侧却不管——图删掉后 `scene.L3ID` 仍指向它，`ListScenes(deletedGID)` 照样列出该场景，宿主按项目域列举就列出一个不存在的项目域，而 `SceneContext`/`Search` 都不报错，是静默不一致不是崩溃。`scene.L3ID` 经全仓核查是 L3 图唯一的入边（`L3ID` 在生产代码里只出现在 l2/scene/repo 三处），所以级联面就这一处：新增 `scene.DetachGraph`，图记录删成后把命名它的场景锚点清零，同域其它图的锚点不动（`TestDeleteL3ClearsSceneAnchors` / `TestSurfaceL3DeleteDropsSceneAnchor`）

## v1.5.0 — 2026-09-01

**L2 换轨：场景 = 宿主会话，轮次归库管。** `Search` 一次调用同时完成"读这个场景"和"开启这一轮"——返回该场景的 depth-1 话题集（宿主本轮该注入的上下文）与为这一轮铸出的话题 id；`Update` 把整轮沉淀进那个 id 并做全轮唯一一次提炼；L6 轨迹以同一个 id 为键。三通道打分检索、L1 扩散激活、话题向量质心与 embedding 依赖、以及 N:N 追加面（`AppendL4Message` / `RefineTopicKeywords`）整体退役。

### 破坏性变更

- **`Search` 语义重写：读场景 + 开一轮**：**无文本、无打分、零 LLM、零 embedding**，入参只剩 `{scene_id, l3_id}`，两者都可空——`scene_id` 为空 = 请库铸一个新场景（名字先由库生成 `session:<id>`，`scene_name` 入参删除——场景名改由 `UpdateScene` 单独写，MCP 侧即 `memhop_scene_rename`）；非空但场景不存在 = `ErrNotFound`（库内不再有"检索未命中自动聚类出新场景"）；`l3_id` 只在新建场景时生效。返回体 `{profile, profile_brief, scene, topics, new_topic_id}`，`topics` 是该场景的 depth-1 话题集（按用户消息时间升序）。**删除** `contexts` / `associated_contexts` / `auto_create` / `directed_l2_id` / `directed_l3_id`，门面签名同步去掉 `ctx`（读路径没有可取消的 LLM 调用）。
- **`SearchResult` 新增 `new_topic_id`**：本次读取为即将进行的这一轮开出的话题 ID，取 `hash("turn:" + 场景:轮次)`。为此场景记录新增 `turn_seq` 计数（老库解码缺省 0，首次读取即 1），格式版本仍 `0x0009`、不跑迁移。**`Search` 的场景写从 best-effort 变为必须成功**——轮次计数是铸 ID 的依据，写失败时读直接报错，不再降级返回一个可能重复的 ID；这也是整条读路径唯一的一次写。
- **`Update` 一次沉淀整轮到指定话题**：入参 `TurnUpdate{scene_id, topic_id, user_text, user_ts, user_type, agent_text, agent_ts, agent_type}`，返回该 `topic_id`。`Update` 不再自己派生话题 ID，只写 `Search` 铸出的那一个；`topic_id` 空 / 非 hex / 全零 → `ErrInvalidQuery`。一次 LLM 提炼出该轮关键词，且提炼排在所有写入之前——失败即报错且零留痕（不再有"半轮记忆"）。未知场景拒绝。同一 `topic_id` 重放是覆盖而非叠加，超时重试安全性不变；双时间戳退为纯时序字段，不再参与身份派生。
- **N:N 追加面整体删除**：`AppendL4Message`（"多对一"：把多条消息追加进已沉淀的话题）与 `RefineTopicKeywords`（"一对多"：按话题全量原文重算关键词）下线。一轮的 L4 原文恒为用户 + agent 两条；两条之间发生的事（工具调用、中间输出、子 agent 结果）是执行过程而非对话，归本轮 L6 轨迹。`Update` 因此是 L4 的唯一写入口，内容类型也在那里声明（见下条）。
- **L4 内容类型有写入口了 + 场景改名接口**：`TurnUpdate` 的 `user_type` / `agent_type` 声明两侧档案类型，零值即 `ContentText`，不填的宿主行为与上一版逐字节相同；非 `text` 侧把媒体路径或 URL 写在对应的 `*_text` 字段里（关键词仍从该字段提炼）。读回侧 `SceneMessage` 新增 `type`，场景上下文里能分辨一段散文与一个媒体引用。`UpdateScene(scene_id, ScenePatch{Name: &name})` 是宿主给场景起人类可读标题的唯一入口（空名 `ErrInvalidQuery`、未知场景 `ErrNotFound`）：`Search` 每次读都在同一条场景记录上读改写命中计数与轮次计数，改名与之不冲突且不会被覆盖。
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
- **工具面覆盖澄清**：31 个 MCP 工具投影 34 个公开会话方法中的 27 个；没有工具入口的 7 个是 L6 计划写读面 4 个（`PlanCommit`/`PlanState`/`PlanReplace`/`SyncPlanTree`）与记忆纠错 3 个（`DeleteTopic`/`DeleteScene`/`DeleteL3Nodes`）——它们要由持有会话状态的宿主来调；`MultiAgentDB.CompactTo` 同理留在 Go 侧（入参就是一个输出路径，等于给模型一个任意文件写入口）。此前文档写"全部公开 API"是不准确的，且把 `MergeScenes` / `SceneContext` 误列为 Go-only——它们分别有 `memhop_scene_merge` / `memhop_scene_topics`。
- **公开面按"宿主是否真的用得着"重排（Session 43 → 33，MultiAgentDB 9 → 7）**：审计只以 `api/` 为标尺，MCP 有工具不再是保留一个方法的理由。
  - 删除并连实现链一起拆：`Lock`/`Unlock`（把默认域的互斥锁递给宿主，零调用者且是误用面）、`Session.Checkpoint`/`IsClosed`（与 DB 句点重复）、`Session.AgentID`、`DistillL0`（Dream 的一个阶段，不是入口）、`ListPlans`（连同 `plan.Summarize` / `PlanCache.All`——恢复计划树是"拿着 id 读 `PlanState`"）、`GetArchive`（= `SearchL4{IDs}`）、`GetCapability`（= `ListCapabilities{IDs}`，`CapabilityListQuery` 因此新增 `IDs` 条件）、以及没有任何公开方法接受的 `api.CapabilityImport` 别名
  - 合并：`ListScenes` + `ListScenesByL3` → `ListScenes(l3ID)`；`SetSceneName` + `SetSceneL3ID` → `UpdateScene(sceneID, ScenePatch{Name, L3ID, Force})`（一次读改写）；`AppendTrajectory` + `PlanAppend` → `AppendTrajectory(key, nodePath, ev)`，`nodePath` 空即裸轮次事件
  - **ID 一律库内发号**：`api/ids.go` 的 `FormatID`/`ParseID`/`FormatAgentID`/`ParseAgentID` 四个桥全部删除（宿主不再需要把整数拼成 hex），改为 `api.DefaultAgentID` 常量与 `api.NewPlanID(name)`（在 `plan:` 命名空间下由名字铸出稳定的 16 位 hex，重启按同名即找回同一棵树）；`internal` 侧 `FormatAgentID`/`ParseAgentID` 与 `FormatID`/`ParseID` 本就是同一实现，合并为一对
- **第四处静默失败由门面守卫挡掉**：`UpdateScene` 合并后没在 `api` 重写，`Session` 的嵌入把它直接抬给宿主，于是返回的是 `core.SceneSlot`——`scene_id` / `l3_id` 是 uint64 而不是 16 位 hex。现在门面补上映射，并把返回值改成**写入后的场景**（宿主核对锚点不再需要 `ListScenes` 扫全域）；`api/surface_public_test.go` 新增反射守卫，遍历公开面每个方法的参数与返回值，任何可达结构体里出现 uint64 的 id 字段即失败。`api.NewPlanID` 同时补上借位：名字恰好哈希成 0 时写 1，保证库发出的每个 id 自己都认。

- **三处"静默失败"修掉**：`SearchL4` 只填 `TopicID` 或只填 `Type` 时走 `default` 分支返回空集（L4 选择器从"三选一模式"改为"填了就 AND"，一轮原文一次调用即可取回，`repo.ArchiveQuery` 随之结构化）；场景改挂到另一个项目域时回 `nil` 却什么都不改（现在 `ErrInvalidQuery`，需 `Force`；且锚定目标必须是已存在的 L3 域——`Search` 给新场景设锚点时同样校验）；`Dream` 传不存在的 scene id 返回零值报告且不报错（`dream.SceneSet` 现在校验存在性，缺失即 `ErrNotFound`）

### 内部

- `repo.TouchSceneUsage` → `repo.OpenSceneTurn`：命中计数与轮次计数一次读改写，并把更新后的记录返回给调用方（读回自己刚分配的 seq，而不是旧快照）。
- `core.ComputeTurnTopicID(sceneID, seq)` 取代 `(sceneID, userTS, agentTS)` 派生。
- 随删除面一并清掉的孤儿码：`agentContext.loadTopicForWrite`、`repo.RefineTopicKeywordsL2`、`internal TrajectorySlot` 折叠用的 `sortTrajectory` / `trajectoryForCrystallize`、`TrajIndex` 的话题桶、`api/mapping.go:parsePtr`、`core.ReadHypergraphEdge`（本版发布前逐符号实测零调用后删除）。
- **第二轮零调用清理**：检索退役后仍留在数据层的原语逐个实测后删除——`repo.UpdateChildrenL2`（`ChildrenIDs` 自 v1.5.0 只由 Dream 建组时写）、`repo.CapabilityIDsFromNames`、`core.AgentRecordCount`、`index.L2MetaIndex.IsEmpty`、`common.SetToSlice`、`agentContext.lastDreamAt`（写了没人读）、`index.TokenizeWords`（实体索引专用的免停用词分词，删后 `runPipeline`/`processSegments` 的 `filterStop` 参数一并消失）。连带删掉整条**场景恢复孤岛** `repo.RecoverDeletedScenesL2` → `core.ScanDeletedPayloads` → `scanDeletedFrames`：全仓库无任何调用方（含测试与 `cmd/`），却每次调用全文件扫描并给每个记录类型驻留一份 payload 拷贝——留着等于给一个没人走的分支付 O(文件大小) 的成本。
- **模式类参数具名**：`QueryArchiveL4` 的 `num` 与 `DeleteL2` 的 `num` 收为 `ArchiveByKeyword` / `ArchiveByTime` / `ArchiveByID` 与 `DeleteScenesL2` / `DeleteTopicsL2`，调用点不再传裸 `1/2/3`（此前 `internal/l4.go` 三处与 L2 删除四处都靠注释说明那个数是什么）。
- **公开方法集钉住**：新增 `api/surface_public_test.go`，按反射列出 `Session`（现 34 个）与 `MultiAgentDB`（现 8 个）的方法集并与清单比对。`api.Session` 靠内嵌 `*internal.Session` 提升方法，因此 internal 新增一个公开方法就等于给宿主新增一个可调 API——这条边界此前只在文档里，现在有测试把关。

### 修复

- **`SearchResult.Scene.TopicCount` 恒为 0**：该字段是派生值（只有 `ListScenes` 现算），场景记录里从不落盘，于是文档承诺的"读回时带话题数"实际一直是 0。现在 `Search` 用同一批已加载的 depth-1 话题直接填，与 `ListScenes` 报同一个数（回归测试 `TestSearchReportsSceneTopicCount`）。
- **DSH 插件无法起 server**：插件仍向 `memhop-mcp` 传已删除的 `-embed-model` / `-encoder-addr`，Go flag 解析器会以 "flag provided but not defined" 直接拒绝启动。embedder 相关配置从插件的 spawn 参数、wrapper 模板、配置读写与面板表单里全部删除；同时摘掉 `package.json` 指向不存在的 `scripts/install.mjs` 的 lifecycle 脚本。
- **新场景 ID 分配不再吞掉真实故障**：`freshSceneID` 以前把"读场景报任何错"都当作"该 id 未占用"，引擎关闭或 IO 故障时会铸出一个可能与既有场景冲突的 ID（两个宿主会话静默合并）。现在只有 `ErrNotFound` 算可用，其他错误原样上抛。
- **独立安装脚本的 wrapper 同样坏着**：`dsh/scripts/install.mjs` 生成的 launchd wrapper 仍带 `-embed-model` / `-encoder-addr`（它与插件那份 wrapper 是两处独立实现，第一次只修了插件那份）——npm lifecycle 脚本摘掉后这个文件仍可手动执行，一跑起服务就退出。现收敛为与插件相同的三参数形态（`-db-dir` / `-transport` / `-listen`）。
- **MCP server 不再自报旧版本**：`initialize` 返回的 `serverInfo.version` 取自 `cmd/memhop-mcp` 的 `version` 常量，还停在 `v1.4.2`——据此判断兼容性的宿主会判错。现为 `v1.5.0`。
- **DSH 侧"每租户一个 `.meh`"是假的**：插件头注释、`dsh/README` 部署模型与 installer 注入 `cordis.patch.yml` 的注释都写"独立 `.meh`（`dbDir/<session-id>.meh`）"，而 server 只在 `-db-dir` 下建**一个**共享 `memhop.meh`，租户是文件内的独立 agent 域（`cmd/memhop-mcp/registry.go:126`）。后果不止文档错：面板 `db:` 指向一个磁盘上永不存在的文件，turn 计数侧车也从这条假路径派生。现统一为"共享文件 + agent 域"表述，侧车改为直接按 tenant 键落盘（文件名与旧实现逐字节相同，无需迁移），`memhop__session` 增报 `tenant`。
- **`memhop__session` 的 `topicId` 有了真来源**：检索退役那一版起 `search` 一度不再返回话题 id，该字段只剩初值 `null`；现在由 `search` 的 `new_topic_id` 直接填充，宿主的面板轮次视图与引擎落笔的话题从此同源。
- **交付文档的数字与死链**：`capabilities/README` 的"MCP 31 工具"先改 30（与当时 `smoke_test.go:36` 的断言一致，`memhop_scene_rename` 加入后为 31，两处同步）；`dsh/README` 的 server 启动示例换成二进制真正接受的 flag（旧示例照抄必然起不来——它传的 `--embed-model` / `--encoder-addr` 已随 embedding 一起删除）；其首段指向 `docs/dsh-memhop-integration-plan.md` 的链接改为纯文本路径（`docs/` 有意不入库，公开 clone 里是死链）。
- **公开会话方法计数纠正**：文档写"30 个工具 / 42 个公开会话方法"，按 `api` + `internal` 导出方法集实测，在 `AppendL4Message` 与 `RefineTopicKeywords` 还在时是 44 个；本版删掉这两个方法、再按"宿主是否真的用得着"重排后为 **33 个会话方法 / 7 个 DB 方法**；发布后的按层接口审查补进 `DeleteL3Nodes` 与 `CompactTo`，现为 **34 / 8**，与 `api/surface_public_test.go` 的清单一致。
- **L0 画像的字段所有权收进库内**：`UpdateL0` 是全量覆盖，而 Dream 蒸馏出的 `EmotionState` / `MBTI` 宿主无从回填——改一次名字就把该域的情绪状态与 MBTI 倾向清零。第一版只在 `memhop_profile_update` 里以"先 `GetL0` 读回、只替换自己那四项"绕过，Go 宿主走 `api` 时仍然被抹。现在规则由库强制：`UpdateL0` 只写宿主四项（Name/Role/Personality/Preferences），两个蒸馏项一律从库里现值继承，`UpdatedAtMs` 由库戳写、不采信调用方传值；`api` 侧入站映射同样不搬运这三项，故宿主无论怎么传都写不进蒸馏半区（反向的 `MergeDistill` 仍只写蒸馏项）。MCP 工具随之退化为纯转发。回归测试 `TestUpdateL0KeepsDistilledHalf`（库内）与 `TestSurfaceL0DistilledHalfIsReadOnly`（门面）。
- **`memhop_profile_get` 描述在说谎**：仍列 v1.4.1 就删掉的"词表、风格与情绪模式"，说明书卡已改而工具描述漏改。现按 `ProfileSlot` 实际字段（含 Dream 蒸馏的情绪状态与 MBTI）表述。
- **MCP 工具计数的注释与实际不符**：`registerTools` 注释写"33 tools"，实际注册 30 个（现 31）。计数唯一的机器校验在 `smoke_test.go` 的 `tools/list` 断言里，注释同步为实测值。
- **`SceneContext` 的消息可能「答在问前」**：话题的 `L4Refs` 按 id `DedupSorted` 存储，而档案 id 由 `(话题, 时间戳, 内容)` 哈希得来——引用顺序与说话顺序毫无关系，场景 id 又每次新建都走 `crypto/rand`，于是同一份测试在不同库上会拿到不同顺序的消息。现在 `sceneContextTopic` 经 `sortSceneMessages` 稳定排序：先按档案时间戳，**同毫秒再按 Role**（`RoleUser` < `RoleAgent` < `RoleSystem` < `RoleDream`，故融合摘要恒在原文之后）——宿主把一轮两侧戳成同一毫秒是合法输入，此时 id 顺序什么都说明不了。回归测试 `TestSceneContextTopicOrdersSameTimestampByRole`、`TestSortSceneMessagesSpeakingOrder`，正向时序由 `TestUpdateStoresDeclaredContentTypes` 一并钉住。
- **`MultiAgentDB.Lock/Unlock` 的文档像并发锁**：原文"serializes the default agent domain against host-side writes"极易被读成"宿主调用前要自己加锁"。事实是每个业务方法已在所属域内串行、跨域本就可并发，这对方法只用于宿主在库外碰同一个 `.meh`（备份/复制），且只冻结默认域。实现不动，注释改为写清用途与不锁其他域的后果（多租户文件要备份请 `Close` → 复制 → `Open`）。
- **`Update` 重放会留下被取代的原文**：同 `topic_id` 重放时话题的 `L4Refs` 被整份重写，旧档案却仍活着——`L4` 里从此每轮多出一对被不再引用的原文，"一轮恒为两条原文"只在文本没变的重试下成立。现在重写引用前先读回该话题的旧引用，落完新引用后把不再被引用者打墓碑（回归测试 `TestUpdateReplaySupersedesPriorArchives`）。首次沉淀没有旧引用，走原路径。
- **`SyncPlanTree` 的部分快照会把已完成步骤退回未完成**：引擎对入参字段无条件覆写，空 `Status` 一律写成 pending、空 `Summary` 直接清空——而 MeowAgent 这类宿主推的是"本轮变化的树"，于是每次同步都把别处已完成的步骤打回，且 rollup 只回填空摘要，清空后永不复活。此前这个继承规则只能由宿主侧"写前先读旧树"绕过。现在库内直接实现：`Title`/`PlanType`/`Status`/`Summary` 空白即继承节点现值，显式传入仍然覆盖（回归测试 `TestSyncPlanTreeInheritsBlankFields`）。
- **Dream 的一个融合组会留下半成品**：单组是"摘要档案 → 提炼关键词 → 建父话题 → 挂引用 → 下沉子话题"的串写，任一步失败原先只是跳过并留痕——最坏形态是一个空的融合父节点悬在从未下沉的子话题之上，下一轮 Dream 还会再挑中它。现在失败即回滚本组已写的记录（`discardFusedGroup`），要么整体生效要么零留痕。同处另修一处：重建后的 `L2Meta` 缓存原先在管线最末才安装，L0 蒸馏的 LLM 调用失败会让整次结构重建白做（且报告已写着各阶段成功），现改到结构阶段结束即安装。
- **瞬时读失败被当成"记录不存在"的一族**：`ErrNotFound` 与"引擎关着 / IO 抖动 / 反序列化失败"是两件事，混起来的后果各不相同——`Search` 的画像读取吞错返回空画像（宿主拿到一份静默缺 L0 的上下文）；`MergeDistill` 把任何读失败当作画像缺失，从空槽重建画像，抹掉宿主写的 Name/Role/偏好；`findCrystallizeTarget` 把读失败当作卡片不存在，于是重新创建一张同名卡并丢掉其使用计数；`UpdateScene` / `DeleteScene` 把任何失败一律改写成 `ErrNotFound`。现统一为「只有 `ErrNotFound` 算不存在，其他错误原样上抛」。
- **未定义的 L4 内容类型能落库**：`Update` 只把 `user_type` / `agent_type` 当 `ContentType` 用，从不校验，于是宿主传 `99` 会写出一条 `String()` 为 `ContentType(99)` 的档案，读回侧 `L4Query.Type` 永远过滤不到它。现在在 `Update` 边界即拒（`ErrInvalidQuery`），判定复用枚举名表 `core.ContentType.Valid()`——加新类型不需要第二处编辑（回归测试 `TestUpdateRejectsUndefinedContentType`）。
- **发布后第三轮复查（全模块 AST 零引用扫描 + 卡面逐条比对公开面）**：删掉的方法在"给 LLM 看的说明书"和自述注释里活得比在代码里久。
  - **内置卡的指引文字指向已删方法**：`memhop-guide` 让 LLM "用 `GetCapability(id)` 取单卡 schema"，而该方法本版本已删且这段是 LLM 取详情的唯一指引（内置卡每次 `OpenMulti` 都过 `capability.Validate`，随库分发）。现改为按 id 过滤 `ListCapabilities`（MCP 面即 `memhop_capability_get`）；`capabilities/README` 的两处同名引用、卡片总量（实测 21.2KB，原写 19.7KB）与 archive 行"三种模式检索 + 单条读取"（`GetArchive` 已删，卡面只有 `SearchL4` 一个资源、五种过滤器）同步纠正。
  - **门面自述漂移**：`surface_public_test.go` 头注释指向一个不存在的守卫测试名；`open_test.go` 注释仍列 `GetCapability`；两个门面测试以已删方法命名（`TestSurfacePlanReplaceAndListPlans` → `TestSurfacePlanReplaceForest`、`TestSurfaceListScenesByL3` → `TestSurfaceListScenesByProject`）；`ErrDeserialization` 注释仍列随索引岛删除的 `index/sparse.go`。
  - **死结构**：`core.AdjacencyEntry`（连三个字段）全模块零引用——AST 扫描连字符串提及一起计入后仍为 0，是已删检索索引岛留下的形状，留着会让人误以为 L3 存在邻接索引。删除。
- **发布后第四轮：按层公开接口审查 + L3 超图闭环实测**（结论先回源码逐条核实，再用本仓自己的 L3 面把公开面导入成图、用图查询验闭环；实测程序与 `.meh` 留在 `/tmp/memhop-l3-audit/` 可复跑）。
  - **N2｜core 记录读取不校验类型（P0，实测复现）**：`readJSON` 把帧里的记录类型直接丢弃，于是任何 typed reader 都能把别的种类解码成自己——`GetL3(节点 id)` 会拿到一个空名图槽，而 `UpdateL3(节点 id, ...)` 随后把那条节点记录**改写成图槽**（节点消失，同一 id 读回是一张图）；`UpdateScene` 也因此能把场景锚到一个节点 id 上。现在 `readJSON` 收期望类型参数，种类不符即 `ErrNotFound`（与 id 不存在同一个答案），每个 accessor 传自己的 `Rec*`；`ReadSceneSlot` 原本手写的这份校验并入同一实现，`DeleteL3` 补上图槽存在性检查。回归测试 `TestTypedReadersRejectForeignRecordType`（core）与 `TestL3GraphWritesRejectNodeID`（门面级）。
  - **N1｜超边身份漏掉 kind（P0，实测复现）**：边 id = `hash(图:排序节点对)`，同一对节点声明 `related` 与 `part_of` 会互相覆盖，实测 4 条声明边只落 1 条（`kind=part_of`）。**六种边里有三种（causal/sequence/dependency）此前与 related 无法区分**——边本就无序、label 又不可写。现在 kind 进身份（`hash(图:节点对:kind)`），导入按「排序成员 + kind」的语义键去重，因此对旧文件里 pair-only 哈希写下的边同样幂等，不会因为换公式而重复建边（`TestImportL3KeepsDistinctEdgeKindsOnOnePair`、`TestImportL3DedupesPairHashedLegacyEdge`）。`internal/graph` 的导入步随之收成一个 `ImportBatch`（mode、result 与三张缓存一处持有，参数个数回到约定上界）。
  - **F-01｜11 个公开方法在 `go doc` 里根本不存在（P0）**：`api.Session` 只显式声明 22 个方法，其余靠内嵌 `*internal.Session` 提升，而 `internal` 不发布——`go doc ...api.Session.Dream` 报 "no method or field"。缺的正是契约最重的那批（Dream 的域锁与 LLM 语义、Crystallize 的 draft 需激活、ImportL3 的 mode 与按 Domain 建图、SceneContext 是唯一不开轮次的纯读、四类删除、PlanReplace、ListTrajectorySessions）。现在这些方法在门面**带注释显式声明**（`go doc api.Session` 34/34 可见），并把 `agents.md` 的约定改写为"禁止的是没有注释的复制纯转发"。
  - **闭环补齐**：`ImportL3` 结果新增 `graph_ids`（图 id = `hash(Domain)`，此前宿主只能 `ListL3` 按名字反查再挂场景）；新增 `DeleteL3Nodes`（节点级删除 + 级联其超边，对齐 `DeleteTopic`/`DeleteCapability` 的纠错闭环，错一个节点不再需要整图重建）；新增 `MultiAgentDB.CompactTo(newPath)`——`agents.go` 的注释一直写着"空间由 Compact 路径回收"，而 core 的 `Compact` 零生产调用方，实测 `DeleteL3` + `Checkpoint` 后文件只增不减（100255 → 101684 → 101825 字节）。`CompactTo` 写出一份只含存活记录的新文件，且**要求目标不存在**（`Create` 带 `O_TRUNC`，指错路径就是销毁），换文件（close → rename → reopen）仍归宿主。
  - **语义与一致性**：`Update` 现在只能沉淀**该场景已开出的轮次**（`turn_seq` 已达的任一 seq），写 Dream 融合父节点、别场景的轮次 id、宿主自造 id 一律 `ErrInvalidQuery` 且在 LLM 调用之前拒绝（零留痕）——此前只校验格式与时间戳，陈旧重试会静默覆盖早已沉淀的旧轮原文；重放当前轮与"先结算后开的轮"照旧合法（`TestUpdateSettlesEachScenesTurnsInOrder` 钉住）。`QueryL3Nodes` 的 ids/keyword/node_type 从优先级 switch 改为全部按 AND 生效（此前同时传会静默忽略两个，卡片却写着 "one or more of"），只填图 id 即列出该图节点；`SearchL4` 关键词改为两边 lowercase（与 L3 一致，实测同词不同大小写此前 L3 命中而 L4 不命中）并新增 `Limit`（保留最新 N 条命中），MCP 侧缺省截 50 条以堵住"空查询把全域原文推进 LLM 上下文"。`SceneContext` 取 depth ≤ 2 平铺是**刻意**的（Dream 下沉到 depth-2 的原文只有这条路径能取回），其"full depth-1"的契约注释与 `TopicCount` 的含义一并改正。
  - **公开面上的死字段**：`HypergraphNode.importance`、`HypergraphEdge.weight` / `label`、`Source.value` / `context_id`（恒 `manual`/空）与 `ArchiveSlot.metadata` 全仓零写入方（只有测试写过），实测新库 51 节点 / 105 边里非零值恰好为 0——宿主按它们写逻辑必然拿到常量。这些字段从 api DTO 摘除（磁盘记录保留，旧文件照常解码，故**格式版本仍 0x0009**），`RoleSystem` 常量同步摘出公开面（引擎只写 User/Agent/Dream）。`core` 侧注释记录"无写入路径、故意不进公开面"。另外补一处真正的写入漏洞：事件侧此前不清 `plan_type`，而记录契约写明该字段只属于计划节点——现在两条追加路径一并清零（`TestPlanAppendCannotInjectNodeType` 扩为钉住整个裸事件形状）。
  - **安全（P1）**：`memhop_capability_import` 把模型可控的路径直传 `os.ReadFile`，同一个二进制里 `registry.openShared` 早就用 `os.Root` 锚定了 db-dir，能力路径漏了。新增 `--capability-dir`（缺省即 `--db-dir`，可用 `MEMHOP_CAPABILITY_DIR`），解析经 `os.Root` + `EvalSymlinks` 双向锚定，绝对路径与越界一律拒（`capability_path_test.go` 覆盖九种命名）。
  - **说明书与工具描述的真实性**：`memhop_knowledge_import` 写着"返回创建/更新的**图 ID**"而实际返回节点 ID；`ListL3` 卡片写着"returns graph summaries (ID/name/**node-edge counts**)"而 `HypergraphSlot` 没有任何计数字段；`memhop_archive_search` 写着"三种模式之一"而条件是 AND；`memhop_dream` 把 `scene_id` 标成必填，于是 MCP 宿主调不到全域巩固（而 L6 修剪与 L1 重建只在 Dream 里发生）。四处全部按真实返回/真实能力改写；每张内置卡的 summary 现在写清"条目是 Go 方法名，MCP 侧对应哪些 `memhop_*` 工具"（`DeleteL3Nodes` 与计划面明确标为 Go-only）。README 的两处 quickstart 片段还在调 v1.4 形态的 `AppendTrajectory(topicID, ev)` 两参签名，`Lock()`/`Unlock()` 的并发说明也留着已删接口——一并纠正。
  - **冗余码**：`cmd/memhop-mcp/tools.go` 的 `idsToHex` / `marshalResult` / 15 键 `idHexFields`（约 70 行）在 api DTO 全量输出 hex 字符串后已是 no-op——反证正是本仓自己的 `TestPublicSignaturesCarryNoNumericIds`；其中 `l4_ref` / `topic_ids` / `edge_ids` / `l3_refs` 四个键在公开 DTO 上根本不存在。删除，`okResult` 直接序列化 DTO。新增两处枚举词表守卫（MCP 的 `content_type` 名字表、卡片里 `related|causal|…` 与 `text|image|…` 的枚举，都通过 `String()` 遍历引擎定义值比对，而不是再抄一份清单）。
  - **裁定为既定决策（不动代码，只写清理由）**：L1 无读接口（已在报告与 `agents.md` 记一行）；`memhop_archive_get` / `memhop_capability_get` 保留为 MCP 侧便捷工具（Go 面删掉按 id 单读后，MCP 宿主确实少了取回单条的入口）；计划状态的数值 `Status*` 与字符串 `PlanStatus*` 双编码保留（分别服务只读的 `TrajectorySlot.Status` 与计划的读写面，`api/exports.go` 已注明不可互换）；`AppendTrajectory` / `PlanCommit` 不返回 event id —— 公开面上没有任何调用接受轨迹事件 id，返回它等于制造一桩没人消费的新契约，改为把"只追加、按 key 整体读回、由 Dream 按保留窗口清理"写进门面注释。

### 兼容

- **不 bump `FormatVersion`**：`TopicSlot.UnmarshalJSON` 在解码点把 v1.4.x 记录的 `user_keywords`/`agent_keywords` 归并进 `fused_keywords`，`turn_seq` 是增量字段，老 `.meh` 直接打开且不丢既有关键词；下一次写回自然收敛为单轨。
- 话题 ID 走 `"turn:"` 命名空间（`ComputeTurnTopicID`），与 Dream 融合节点的 `ComputeTopicID` 分域，两者不会相撞；删除生产零调用的 `ComputeTopicIDForText` 与死字段 `SceneNode.VectorPageRef`。
- 场景归并不再发生在 Dream 内（会连带删掉宿主正持有的 sceneID），合并场景仍是宿主显式调用的对外接口 `MergeScenes`。
- L1 层保留（节点同步 / 关键词 Jaccard 建边 / 衰减 / 重要性反馈，全部改吃单轨关键词），但删除扩散后库内暂无读者。

### 实测基线

热路径 LLM 调用从每轮 2 次（Search 提炼 + Update 提炼）降到每轮 1 次且只在写路径；`Search` 变为一次场景记录读改写 + 一次 L2Meta 内存读，不联系 LLM 与 embedding 服务。检索质量类断言随之重定位：`test/` 黑盒不再测"跨会话召回"（该能力已按设计移除），改测整轮沉淀、场景读回与巩固后不丢事实。

宿主可依赖的不变量测试：`TestSearchOpensOneTurnPerRead`（一次读开一轮，且开轮不建任何话题记录）、`TestUpdateSettlesEachScenesTurnsInOrder`（各场景轮次互不串台、按序读回）、`TestUpdateReplayIsIdempotent`（同 `topic_id` 重放只覆盖不叠加：1 个话题 + 2 条档案）、`TestUpdateStoresDeclaredContentTypes`（两侧各按声明类型归档，未声明的一侧保持 `text`，场景上下文按时间戳读回「问在前 + 类型」）、`TestSearchReportsSceneTopicCount`（读回的 `TopicCount` 与 `ListScenes` 一致）、`TestUpdateSceneNameSurvivesLaterTurns`（改名后被 `Search` 读改写命中/轮次计数覆盖不掉，且空名与未知场景各按码拒绝）、`TestTurnRunsOnOneTopicID` 与 `TestCrystallizeReadsOneTurnTopic`（轨迹追加/读取/结晶三条路径同键）、`TestSurfaceTurnFlow`（门面侧一轮闭环）、`TestSSETurnFlow`（MCP over SSE 端到端：`search` 铸 id → `trajectory_append` → `update` → 复读看到该话题，缺 `topic_id` 的 `update` 被拒）。

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
