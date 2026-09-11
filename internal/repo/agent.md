# internal/repo — 存储层契约（模块级 agent 上下文）

本目录（含子包 `core`、`index`）是 MemHop 的**存储层**。任何 AI agent 或
开发者修改本层前必须先读完本文件，修改后必须同步更新本文件。

## 唯一职责

按 **agent 域**（`agentID uint64`）提供记录的读、写、遍历与检索原语：

- `core/`：.meh 引擎——记录帧（26 字节：type/flags/length/agent_id/
  id_hash/crc32）、A/B 文件头、快照（0x03 分域，只存记录索引）、空间回收、
  `StorageEngine` 索引（`agent -> idHash -> offset` 两级分域）、Slot 数据模型。
  `FormatVersion` 是 `0x0012`：Open 对任何其它版本（更旧**或**更新）都显式拒绝、
  无迁移路径。上抬改的仍不是 26 字节帧布局而是记录含义：域身份落在 L0 画像的
  `agent_type` 上（`AgentTypePrimary` = 0 是打开文件所用的那个隐式零号域，
  `AgentTypeSub` = 1 是注册域），话题带一个只有调用方写的 `name`（`omitempty`，
  无名话题不落这个键）。旧文件的画像没有 `agent_type`，解码后每个域都读作主
  agent——不是某一处取值错，而是每个域同时错，且与「一个文件恰好一个主」这条
  不变量直接冲突，所以这一版不能像缺省一个可选字段那样容错打开。
  计划节点由 `(topic_id, seq)` 寻址——`seq` 是该轮内库发号的序号、`parent_seq`
  指向父步骤、0 即根，节点记录上没有路径字符串，事件记录的归因字段因此叫
  `node_seq`；状态词表只有三态且 `in_progress` 占值 0（新建的节点零值即合法
  状态）。**值 3 起是未定义存储值**：本层按字节原样存取，不看状态含义——把一
  个字节渲染成名字叫得出名字的状态是读它的人的事，回落出来的名字是假的。
  一轮的内容同住 L4（`Kind` 区分原文与事件，id 由
  `hash("content:"+topic+":"+seq)` 派生），L5 只剩计划节点、走帧型
  `RecL5PlanNode 0x0F`；id 命名空间不烘层号（场景节点 `scene-node:`、内容槽
  `content:`、计划节点 `plan:`），归档的归属字段叫 `topic_id`，场景记录只剩
  `turn_seq` 一个计数器。
  **`SnapshotVersion` 与 `FormatVersion` 不是一回事**：快照版本不符只让 Open 丢掉
  那份快照、回退一次全量记录扫描重建索引并打一条 WARN，文件照开、记录一条不少，
  下一次 checkpoint 就写成当前布局。所以改快照布局的代价是「旧文件首次 Open 慢
  一次」，改记录布局的代价才是「旧文件打不开」；当前布局是每个 agent 段只有
  id 头加偏移条目，末尾那段不透明 blob 已随它退役的生产者一起删除。
  `StorageEngine` 按功能分文件：`engine.go`（索引模型/访问器）、
  `engine_lifecycle.go`（Create/Open/Checkpoint/Close）、`engine_write.go`（追加）、
  `engine_read.go`（索引查找读）、`engine_delete.go`（墓碑删除）、
  `engine_recovery.go`（扫描/撕裂尾帧截断/索引重建）；数据模型分
  `model.go`（Slot 结构）/ `model_enums.go`（枚举）/ `model_dto.go` 与
  `model_distill.go`（跨包使用的请求与响应结构，最底层纯数据）。
- `index/`：索引——L2Meta（场景读回的唯一话题缓存，`rebuild.go` 全量重建）/
  `l4.go`（`L4Index`：一个话题名下有哪些内容槽位，按 Seq 升序、条目带 `Kind`）。
  只依赖 `core`。
- 根目录 `l0layer.go`~`l5layer.go`、`agentlayer.go`：各层记录读写原语，
  一层一个文件组（单文件超 400 行时按功能拆分，命名
  `<layer>layer_<aspect>.go`：`l1layer_sync.go`、`l2layer_topic.go`），
  所有函数以 `agentID` 为域参数。存在一个保留域
  `core.SharedPoolAgentID`（文件级公共池：L3 知识图）：本层原语对它和普通域无差别
  （`agentID` 只是参数），路由与守卫都不在本层。本层不含建边、衰减、画像生成或
  字段合并这类算法——只有记录读写原语（如 `MutateNodeL3` 以回调接受调用方策略）
  与索引维护。

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
   L2 话题列举收为 `TopicListQuery`（`Depth` 上界 + `ByScene` 决定是否限定到
   一个场景，没有第三种模式——按 id 读单个话题的那个取值全仓零调用，已删），
   L2 批量删除已无模式参数可言（`DeleteL2Records` 只照给定 id 落墓碑），
   调用点不再有裸 `1/2/3`。
3.1 **一个 id 只对应一种记录**：`core` 的 typed reader 一律带期望的
   `Rec*`（`readJSON` 比对帧内类型，不符即 `ErrNotFound`）。丢掉这个校验
   会让 `UpdateL3(节点 id)` 读到"空名图槽"再把节点记录改写成图槽。
3.2 **L3 超边身份 = 排序成员 + kind**：`CreateEdgeL3` 的 id 含 kind，
   `EdgeKeyL3` 是同一身份的语义键，导入侧按它去重。边没有权重：引擎不算边权，
   也没有按权重分支的读法，一条边携带的就是「谁与谁相关、以哪种关系」。
3.3 **共享域的图解析只有一个入口**：`ReadSharedGraphL3(engine, hexID)` 把
   「解析 hex 图 id + 确认图存在于文件级公共域」收成一处，锚点校验与 L3 的
   读/改/删全部经它取图槽。调用方不得自己 `ParseID` 再 `ReadGraphSlot`——
   公共域这一半约束漏掉一次，就会拿调用方自己的域去读一张不住在那里的图。
4. **实现不外露**：记录帧布局、快照格式、回收/压缩细节只在 `core` 内部
   流转；`internal` 业务层只能经本目录导出的函数访问数据，不得直接解析
   帧或操作 `StorageEngine` 未导出的状态。
5. **单向依赖**：`repo -> repo/core`、`repo/index -> repo/core`、
   `repo -> common`；禁止反向依赖 `internal`、`api`、`cmd`。
6. **默认域**：`core.DefaultAgentID = 0` 即全零 hex 域；
   注册记录 `RecAgentRegistry (0x10)` 的 `idHash == agentID`，data 为
   agent 名 JSON，Open 时扫描重建 `name -> agentID` 映射。
   `ListAgentRegistry` 除映射外还带出第一条「记录在、名字解不出来」的失败（读不回 /
   解不开 / 空键）：那条记录仍然占着一个域，只是没有任何名字能指到它。本层只报，
   「这个名字能不能用」是调用方的判断。

## 修改者义务

改动本层导出签名、帧/快照格式或域语义时，必须同步更新本文件与
`internal/agent.md` 中受影响的条目，并保证 `go vet ./...` 与
`grep -rn 'L7\|RecL7' --include='*.go'` 零残留。

- `EnsureGraphL3`：槽存在就复用其 id、不覆写记录；`CreateGraphL3` 是无条件写槽，只用于确认不存在时。两者都不再接收「来源」：图槽记的是标签与两把时钟，而 `source` 那一格的 kind 恒为 manual、另两个字段没有任何写入路径能设置，`SourceKind` 四个值里三个连写点都没有——形状、枚举与对外的 `source` 键一起删，旧文件里多出的键在解码时被忽略，磁盘格式不动。
- 记录区末端（`nextOffset`）说的是**已经落进文件的帧**，不是「已 fsync 且已能经 mmap 读」的帧：flush 或 remap 失败时末端照旧前移，因为 checkpoint 的 `RecordEnd` 与 compact 的截断点都读它——留在后面就是让 header 宣称一个比日志短的记录区。索引与镜像相反，remap 成功之前一步都不动：指到映射外的字节比少给一条记录糟得多。
- 按 id 读（`ReadRecord`）把帧解码器的 `io.EOF` 换成 `ErrCorruption`：那个哨兵说的是「扫描到此为止」，只有逐帧扫描用得上；一个被索引点名、却落在记录区末端或零填充区上的 id 就是索引与记录区不一致，而裸 `io.EOF` 一个码都不带。
- 头部的 CRC 只在 `FileHeaderFromBytes` 判一次。到 `SelectValidHeader` 手上的两个头必然都已过了那道闸，它只回答「谁的 commit id 高」；在这里重算校验和既买不到安全，又留下两个永不进入的分支。
- 本层两份扫描各一套：`CollectAll*` 跳过读不回的记录，`core.CollectAllStrict` 把那一次读失败报出来。走严格那份的枚举口都带 error：`TopicClosureL2`/`TopicIDsBySceneL2`/`MergeScenesL2`/`SyncL1NodesFromL2`/`CollectPlanNodes`/`PlanNodeIDsByTopicIDs`，加上按单条读的 `ListScenesL2`/`CollectAllScenesL2`/`QueryArchivesL4`。**枚举与批删是两件事**：`DeleteL2Records`/`DeletePlanNodesByIDs` 只照给定的 id 落墓碑、不读任何东西，「读了才知道要删谁」全在上面那些枚举口里——分开放，删的人才可能把所有枚举排在第一张墓碑之前。严格那套的判据是「这次读决定下一次的写」而不是「决定删除」——「要不要新建哪一条」同样由枚举的答案决定。被拒时错误带着记录 id 出来：引擎没有任何读面能指出「哪条坏了」，不带 id 的拒绝等于让宿主自己去猜。
- L3 的两份列举（`ListNodeL3`/`ListEdgeL3`）按记录 id 升序返回：底下的 `CollectAll*` 是哈希表迭代，不排序就让同一个调用两次给出两个顺序，调用方带的条数上限也落在任意子集上。
- L0 有**两个**画像读原语，按调用方要不要 payload 分。`GetProfileL0` 把记录自身的错误码带出来：`ErrNotFound` 只回答「没有这条记录」，读不动与解不开分别报 `ErrIO`、`ErrDeserialization`——拿到它的三处都在这个分界上分道，读侧只在「没有」时给空画像，`UpdateL0` 只在「没有」时才不继承库自有那几项（带着读不回来的 payload 去写，就是把情绪、MBTI 与域身份覆盖掉）。`HasProfileL0` 服务只要「在不在」、根本不读 payload 的调用方：打开文件时靠它决定是否播种主域画像，前者是 `(false, nil)`，后者原样上报。
- `RenameTopicL2` 是读-改-写：整条记录重写，所以关键词轨、父指向与场景归属都原样保留，改的只有 `name` 一个字段。名字是否合法（空名算不算一个名字）不在本层判断，那是业务层的规则；话题不在就如实报 `ErrNotFound`，绝不凭空造一个——那会留下一个没有场景、没有深度、没有关键词的话题挂在一个别的记录都不指向的 id 上。
- 轮次话题的重写口 `CreateTurnTopicL2` 从存量记录继承两样东西：宿主的 `name`，以及巩固已经给过这一轮的位置（`depth` + `parent_id`）。重放一次不该改名，也不该把一个已沉入融合组的轮次送回 surface——那会让它自己的原文与取代它的那份组摘要并排出现、组还少一个子。存量记录读不回（`ErrNotFound` 以外的任何错误）就是**这次写不做**，且带自己的错误码上抛（本层这两个写入口返回 error 而不是 bool：调用方编不出比因更准的说法）：猜一个 depth-1 的位置等于凭空造出第二轮的第二个真相。列举的排序键是 (UserTimestamp, Depth)，两者打平时以记录 id 收尾——**同深度**的两个话题可以共用一个 user 时间戳（融合父带的就是它组内首轮的时间戳），不收尾就是让同一个场景两次读出两个顺序（`SurfaceTopics` 本就如此）。列举也只有镜像一条路：它读的正是场景读路径服务的那张表，所以列举与场景读取不会互相矛盾——此前「无镜像就扫记录」的回退分支在生产上不可达（域上下文一建出来就带镜像），而它的语义也并不「相同」。
- L4 的两种读判据不同：**按 id 读**时不存在的 id 可以跳过（已墓碑的、或本来就不是本域的 id 都只是「选不中」），**按话题索引读**时索引点名却读不到就是镜像与磁盘不一致，必须 `ErrIO` 而不是少给一条对话。「槽位被保留窗回收」不属于任何一种：那次清扫同时摘掉索引条目，读侧看到的是 `Seq` 上的一个空洞，由 `Seq` 本身带出去判别。
- `Kind` 是**条件**而不是模式：`ArchiveQuery.Kind == nil` 表示「两种都要」，非 nil 表示「只要这一种」。它必须在每一条读路径上都生效，包括只给 id 的那条快路径——快路径绕过过滤谓词就是这个条件最容易静默失灵的地方。
- L4 原语的签名带着归属信息：`AppendArchiveL4(engine, agentID, idx, *core.ArchiveSlot)` 收下调用方给的记录，
  在本层把 (话题, `Seq`) 换算成 `core.HashContent` 落进 `IDHash`、落盘后同步 `index.L4Index`，
  不回传句柄（地址就是调用方手里的那对键）。写同一个 (话题, Seq) 是**原地覆写**而不是追加第二条——
  这就是重放一轮能收敛的机制，本层因此没有也不需要「先列出这个话题旧有的归档、再删掉没被重写的那几条」
  这类原语（第二真相的活形式）。`ReadArchivesByIDs` 是把镜像点名的 id 清单换成记录的唯一一处，
  上一条那个「点名却读不到 = `ErrIO`」的判据就实现在这里，读侧不再各写一份。
- 删除只有两条入口，都内置「磁盘删成功后才摘镜像」这一步序：带话题的 `DeleteTopicArchives`（整话题连删带摘）、按保留窗的 `DropExpiredArchives`（先 `ExpiredBefore` 只读地拿 id，删成后逐话题 `RemoveIDs`）。`TopicClosureL2` 只返回话题闭包——内容由闭包里的每个 id 去索引取回。
- L5 只有计划节点（`l5layer.go`）：一个节点一条记录，键是开出这一轮的话题 id。`GroupPlanNodes` 按它分组（组内按 `Seq` 升序，即创建顺序），`CollectPlanNodes` 是「严格扫全域节点桶 + 分组」那一个入口，缓存重建走的分组是幸存记录那一份，`PlanAggregate` 只带 `{TopicID, Nodes, LastActiveAt, HasNonDone}`——不带事件计数，也不带事件清单。`WritePlanNode` 校验 `IDHash == HashPlanNode(TopicID, Seq)` 且 `Seq != 0`：节点身份是从序号派生的，本层不接受调用方自备的第二把键，而 0 是「没赋值」不是一个可寻址的步骤。删除分两步：`PlanNodeIDsByTopicIDs` 扫节点桶按 `TopicID` 过滤出树上的 id（树没有内容侧那样的位置键可推），`DeletePlanNodesByIDs` 照 id 落墓碑。本层没有「删某分支」的原语：作废发生在键上，不在记录上；节点记录上也没有路径字符串，一步加它整棵子树的序号集合由读的人沿 `ParentSeq` 求闭包得到。节点顺序与聚合的两个派生量只有这一份实现（`ComparePlanNodeSeq` / `RecomputePlanAgg`），`CollectPlanNodes` 与任何增量改动都经它们：增量维护出来的树与从磁盘重建出来的树因此不可能各算一套而漂移，`RecomputePlanAgg` 先把两个派生量归零也正是为了节点被摘掉后不留下已删节点贡献的旧值。
