# Changelog

MemHop 遵循语义化版本。本文件记录每个版本的核心改动；完整历史见
README 的版本表与 git log。

## v1.6.6 — 2026-09-23 — 一轮只剩一次收束：场景与轮次由域自持，轮中写入不再收宿主填的归属字段

1. **`Settle` 退役，一轮的终点只剩一次 `Update(TurnEnd{Input, Output, Outcome, CreatedAt})`**：Input 与 Output 落在该轮话题预留的 Seq 1 / Seq 2（对话主干的两个槽位，所以重关一轮是原地覆写而不是累积），Outcome 作为一条 `turn_outcome` 事件按调用次数追加——一次挂起加一次恢复本就是两条事实（`TestTwoClosesOfOneTurnKeepBothEndingsAndTheLastDialogue`、`TestUpdateWritesTheTurnEndItIsGiven`）。轮末照旧蒸一次关键词轨，并把 `fused_keywords` 随话题交回。
2. **场景与轮次这两个 id 改由域上下文自持、不再回传宿主**：`Search(SearchQuery{})` 续用该域当前的场景，并为「即将进行的这一轮」铸出 topic id（`NewScene:true` 才另开一条会话；跨进程重启从记录里恢复，靠的是场景上原有的 `turn_seq` 计数器——这是读路径唯一的写入，也是 load-bearing 字段）。`SceneContext("")` 是同一条会话的**纯读**：不吃轮次、不点名，也不替从没读过的域新建场景（`ErrNotFound`）。一轮里「问模型之前」的读可以发生很多次，只有开轮那一次前进计数——否则带工具调用的决策循环会让计数替根本没发生过的轮白跳，而那把计数正是轮次 id 的派生依据（`TestSearchContinuesItsSceneUnlessAskedForANewOne`、`TestSearchOpensOneTurnPerRead`、`TestSceneContextOpensNoTurn`、`TestSceneContextWithoutAnIdReadsTheDomainsScene`、`TestSearchContinuesTheDomainScene`）。
3. **锚点只能由会新建场景的那一读采纳**：续用一条会话时递来 `L3ID` 一律拒——不静默丢弃，也不拿它当作「另起一条会话」的暗示（`TestSearchRefusesAnAnchorWhileContinuingItsScene`）。
4. **写形状与读形状分开**：轮中逐条写入的入参换成 `ArchiveInput`，八个字段全是宿主的决定；读回形状 `ArchiveSlot` 上那两个归属字段（`id`/`topic_id`）在写侧**没有位置**——是「没有位置可传」而不是「传了不采信」（与 `ProfileInput` 同一条标准），于是「写到宿主没在做的轮上」这条路被形状关死。返回该条占用的槽位 Seq；没有开着轮时写入即拒（`TestArchiveInputCarriesNoAddress`、`TestAppendArchiveCannotAddressATurnTheLibraryDidNotOpen`、`TestTurnWritesRefuseWhenNoTurnIsOpen`）。
5. **自持字段与删除/合并同步**：`DeleteScene` 与 `DeleteTopic` 摘掉指向被删记录的那一半，`MergeScenes` 把被吞场景的轮次清空（轮次 id 由场景派生，跨合并活不下来），所以一个已毁的轮不可能被下一次 `Update` 继续写。一轮的键若真撞上保留的全零值，`Search` 直接报损坏而不是收下：那个值同时是「没有开着轮」的哨兵，静默收下等于让宿主写到别人的轮上。
6. **宿主的 hex id 只在一处跨界**：渲染走 `internal.FormatID`，解析走 `internal.parseID`（它命名自己读的是哪个字段），根的大方法把 hex 换成 uint64 之后才往下传，第 3 层以下不再收 id 字符串。公开面维持 `Session` 25 + `DB` 7；`TestTurnWritesCarryNoId` 与 `TestPublicSignaturesCarryNoNumericIds` 钉住「形状里没有宿主该持有的键」。
7. **时间单位在写边界判**：`AppendArchive` 与 `TurnEnd` 的毫秒保留窗拒掉秒级（1e9–1e11）与微秒级（>1e14）两段值（两条边界各一条用例：`TestAppendArchiveRefusesATimestampInTheWrongUnit`、`TestUpdateRefusesATimestampInTheWrongUnit`，后者另钉住「拒在 LLM 之前、零留痕、这一轮仍开着」）——保留窗按毫秒算 cutoff，一个秒级值写进去既会被下一次 Dream 当成过期扫掉，又永远命不中时间过滤。这条是跨仓端到端集成实测撞出来的：三仓同为毫秒之后，宿主侧不再有任何一次单位换算。
8. **读回来的角色重新有名可指**：`api.RoleDream` 上架（v1.6.3 曾随「宿主不该写它」一起下架）。写侧一字未松——`content.ValidateAppend` 照旧拒任何带它的记录，那两条用例仍在；被撤掉的只是一个名字，而 `SceneContext` 与 `SearchL4` 都会把它交回来（巩固组的摘要就是靠它和一轮的两句原文区分）。宿主因此不必写 `m.Role == 3` 这种魔数来决定往模型前摆哪几行——命名不授予写能力，边界才是。
9. **磁盘格式 `0x0012` 不变**：自持的两个字段不落新记录也不加新键，轮次计数器就是场景记录上原本就有的 `turn_seq`，旧文件照常打开。测试脚手架顺带收形：7 个 mock LLM server 的同一套骨架收成三处；本轮语义由离线接口面 `TestInterface*` 逐条钉住（不花额度），另有 `TestSecondLibraryOpenedMidRound` 与 `TestEachDomainHoldsItsOwnTurn` 钉住「中途再开一个库也不串、各域各持自己那一轮」，`TestDreamLeavesTheOpenTurnCloseable` 钉住「定时巩固撞在开着的那一轮上：既不掉那一轮，也不扫掉它已写的记录」，`TestIdleReclaimRefusesTheDroppedRound` 钉住另一头：空闲回收跨过一轮的中途时，那一轮的写入与收束被拒到宿主重开一轮为止。
10. **接入指南补上第七条「宿主要知道的事实」**：一次召回读要把 `SceneContext("")` 在「这个域还没读过」时报的 `ErrNotFound` 翻成一次空答案——内核把记忆端口上的错误当作整次调用的终止，于是「还没有任何东西可记」的第一轮反而会被打死。这条是拿宿主真实的决策循环跑出来的，写进 `INTEGRATION_GUIDE*.md` §6.5；同一处顺带把 `api.RoleDream` 的两段旧文（「不作公开常量」/ "deliberately not exported"）改口，两份指南的版本头随本轮取 v1.6.6。同一节还记下召回读的成本形状（300 轮 / 900 条记录的域上，`SceneContext("")` 一次交出全部话题实测 1.4–2.2 ms，按话题补一次读每条 43–64 µs，五次跑）：「为每条召回项再查一次结局」付的是 N × 数十微秒，所以**不加新读面**——那条按 N 个话题批量取的口子在被证明需要之前不存在。
11. **巩固组的父 id 把自己的成员算进派生式**：原来只由「场景 + 一对时间界」派生，而时间界不能标识一个组——宿主打时间戳粗到几轮共一时，两个不相交的组撞出同一个父，后到的那一组被拒（这个拒绝是对的：让一段没提过它们的摘要去概括它们是一条假记忆）。代价是永久的：被拒的组留在场景表面，该场景的 depth-1 计数再也降不到压缩门限之下，于是**每一趟巩固都重新问一次模型、再重新拒一次**，一次真额度调用换零产出，而触发调度的正是这些压不掉的组自己。四趟巩固的探针实测：第一趟 1 落地 1 被拒，其后三趟全部 0 落地、模型调用涨到 4 次、表面话题数一动不动。成员升序后参与哈希，所以「同一组重放算出同一个父」这条原有性质一条没丢；占用校验留着，但它现在守的是同一成员集合被复读（已应用组的重放），仍在第一条记录之前拒掉、无回滚（`TestApplyGroupsLandsDisjointGroupsWithOneBoundsPair`、`TestApplyGroupsRefusesReplayedMemberSet`）。已存在的融合父 id 按旧式记录在盘上，没有任何读路径会重算它，所以 `FormatVersion` 不动、旧文件照开照读，公开面一字不变。
12. **计划序号在被事件指着时不再重发**：发号只看这一轮**幸存**的步骤，而 Dream 的 L5 裁剪会把过期步骤从树上摘掉，绑在它上面的那条事件却还在——归因只是 L4 记录上的一个 `NodeSeq` 字段，正文按自己的保留窗计时，两把尺子不同步。于是这一步的序号退回给新写的步骤，新步骤按步骤读事件就读到了上一步做过的事。实测复现过一次：1 号步骤被裁掉、它那条 `old_work` 事件幸存 → 同一轮再 `PlanNodeAdd` 又拿到 1 → 按步骤读到 1 条不属于它的记录。发号因此多一个下限：该轮事件轨上被点名的最大序号（`L4Index` 现在跟着记 `NodeSeq`，读的是镜像不是记录，所以这一步没有新增一次记录扫描）。代价是这一轮的序号可能跳号——一个序号只要还被指着就不重发，宿主不该假定编号密集（`TestPlanOrdinalSkipsAnEventThatOutlivedItsStep`、`TestL4IndexTracksBoundOrdinals`）。磁盘格式 `0x0012` 不变，公开面一字不变。
13. **把「一次场景读怎么折成进模型的几行」写成契约**：`SceneContext` 摊平到 depth ≤ 2 是刻意的——巩固组是 depth-1 的那条、它的摘要作为它自己的一条原文带着 `RoleDream` 回来，被它吞掉的轮次是它的 depth-2 子节点，而这是唯一会列出那些子、也是那些原文唯一还能回来的读。这条判定此前只活在首个换后端宿主的 26 行里（实测那棵克隆树），第二个宿主得重写一遍，而重写错的后果不是难看是**同一件事进模型两遍**（摘要 + 它取代的每一轮各一次）。§6.5 因此从七条事实变成八条：丢 `Depth > 1`、`ChildCount > 0` 取那条 `RoleDream`、其余取本轮那两句、时间取最早一条消息，四条按序抄即可。「引擎不渲染正文」这条边界不动，动的只是把「这一行是什么」写清楚——它本来就写在行上（`Depth`/`ChildCount`/`Role`）。形状、公开面、磁盘格式一字未动。
14. **进程内也守住的单实例，补上门面级的那条测试**：`.meh` 的排他锁按打开文件描述来算，所以同一个进程拿同一个路径开第二次 `Open`，与另一个进程来开是同一件事——被拒、`ErrIO`、先持有那份的句柄一点不受影响，另开一个路径照常成功。「一家一份库」这个形状里唯一的一种撞车就是「worker 与宿主被喂了同一个路径」，而它此前只在引擎层有测试（`TestSecondInstanceRejectedByLock`），门面级没有；补上 `TestOpenRefusesAFileThisProcessAlreadyHolds`，并写进 §6.5：宿主要的动作是换一个路径，不是把文件读成坏了。**负例证明**：把 `lockFile` 改成一放过，这条测试当场红。形状一字未动。
15. **「一家一份库」里项目知识怎么走，钉在门面上**：L3 是一份文件一个池（不是域一个池），所以模型用一次工具调用招来的
 worker 若另开一条路径，起手是一张空图；若作为父文件的子域建出来，则不必重导就继承那张图。此前这两种形状只在读面行为上成立、 门面级没有测试钉着，而宿主挑哪一种（共享项目知识 vs 各自一家）恰恰是按这条来定的。补 `TestKnowledgeGraphStaysInsideItsFile`：同文件的第二个域看得见、第二个文件看不见、且第二个文件自己导入不动到第一份文件。**负例证明**：把 `ListL3` 的池从公共域改成调用方域，第一条断言当场红。形状一字未动。
16. **指南里那段可跑骨架有了编译检查**：`make check-guides` 把两份指南 §11 的 `package main` 代码块抽进一个临时模块，用 `replace` 指向当前工作树、`GOPROXY=off` 编一遍。「照文档抄就能集成」这句话此前只靠人读——示例里的符号随公开面改口而烂掉时没有任何检查会红，而 §11 恰恰是宿主最快的一条接入路径。两份指南的骨架现在都编得过（实测）；**负例证明**：把示例里的 `SearchQuery{}` 改成一个不存在的名字，该 target 当场以 `undefined: api.SearchQueryNope` 退出 2。零新依赖，也不塞进 pre-commit（它是开发机上显式的一步）。
17. **四个调参旋钮统一成「留 0 就是没填」**：原来三个字段各自解释自己的零值——两个当「关掉」、一个当「不跳过而且让模型按 0 去合」，只有保留窗口把 0 与负数都读成库默认；而 `LlmConfig` 的两个预算早就是「留 0 = 默认」这一种读法。归一化只落在宿主的结构体变成引擎配置的那一刻（`OpenDB`），六个读取点一个不改：0 由 `DefaultMemHopDefaults` 顶上，「关掉这一项」改用显式负数，压缩门限的负数折成 0（0 才是「不设目标」的真实取值，负数不该进提示词）。代价说清：`DreamCompressMinTopics` 那个 0 不是「更保守」而是**更激进**——它同时是提示词里让场景收敛到的目标数，零值宿主拿到的是「把话题压向 0 或以下」。三仓 e2e 复跑 14 条全过，而它本身就是拿 `api.MemHopDefaults{}` 在跑：改前那次 `Dream` 有一次巩固调用、场景清单多出一个融合父行（4 vs 3）、关键词调用 6 次；改后巩固按 20 的门限跳过、那次调用归零、关键词 5 次。方向是保守而非丢数据，被改变的只是把 0 当「关掉」用的宿主——那批读法从未随 tag 发布。新增 `internal/config` 的第一份测试与`TestOpenTakesUnfilledDefaultsAsTheLibraryDefaults`（负例：撤掉归一化即红）。磁盘格式 `0x0012` 不变，公开面一字不变。决策与四条落选方案见 `notes/implemented/architecture/2026-09-23-unfilled-means-default-for-the-four-knobs.md`。
18. **召回契约补上「摘要先老化、行还在」这一档**：第 8 条把规则写成「丢 `Depth > 1`、`ChildCount > 0` 取那条 `RoleDream`」，而保留窗扫的是正文不是话题行——一周之后一个巩固组正是「表面上一行、自己没有正文」的状态，照原规则渲染会**整段失声**，而库里那些轮的关键词轨还在（实测新用例：`TestFusedGroupAgesIntoKeywordTracksNotSilence`：摘要扫掉，父行留在 depth-1 且带着自己那条 `FusedKeywords`，两个子行也各自带着）。规则因此改成有条件的：有摘要时跳过 depth-2，没摘要就交出**这一行自己的** `Keywords`——摊平的那份列举不带父指针，读者走不进那棵子树，所以能退回的东西必须就写在行上（那条轨正是建组时从成员摘要折出来的）；`Messages` 为空是过期的终局而不是丢了一行。零代码改动，两份指南同批改口。
19. **场景读的每一行带上自己的两个时间界**（`SceneContextTopic.UserTimestamp`/`AgentTimestamp`，与 `TopicSlot` 同名同义）：上一条把「父行没正文就退回 `Keywords`」写成规则之后，拿宿主那条召回路实测了一遍（`ContentRetentionMs=1` 逼出过期态）——**四行全部 `messages=0`，而按旧契约「时间取最早一条消息」只能定成 1970 年**，于是这些记忆在「最近 N 条」的截断里排在最前、第一个被丢掉；更糟的是没收进组的普通一轮两句原文被扫掉后按契约直接被丢弃，一份跑了两周的场景读起来是空的。行上本来就有那两个界（轮＝刺激/应答时刻，组＝组内最早/最晚那一轮），只是这条读一直没交出来；交出来之后第（4）判定不再扫消息，宿主少一个循环。规则同时推广到任意一行没有正文的表面行：退回这一行自己的 `Keywords`。实测证据在宿主那侧（同树的过期场景用例：改前 1 条且日期 1970，改后 2 条且日期是这两行自己的时刻）；引擎侧新增映射断言（负例：把 `scene.ContextTopic` 里那两行映射摘掉即红）。磁盘格式 `0x0012` 不变，公开面方法数不变，只多两个读侧字段。

20. **过期行上的结局词写成契约（零形状变更）**：上一条在宿主那条召回路上实测时顺手看见 `Entry.Outcome` 全是空的，回源码确认原因在写入侧——`Update` 一次调用写的两句原文与那条 `turn_outcome` 事件**共用同一个 `CreatedAt`**（`internal/update.go` 里那一批记录一起交给保留窗），于是结局词与正文同批出局。宿主那些「这轮做完了吗」的字段在过期行上就是空，正确的读法是「不知道」而不是「没做完」；而轮中所见的 `AppendArchive` 事件各按自己的时刻出局，所以「`Messages` 为空」仍然不等于「这一轮什么也没记」。两份指南 §6.5 补上这一段，草案 `aged_test.go` 把「过期行结局词为空」钉成断言（实测 2 条，结局词均为空）。

21. **召回第（1）条按实测改准，并把「折两趟」的形状钉进用例**：一趟巩固可以把**上一趟建出的那个组**再折下去，这条形状此前没被测过。离线跑两趟 `applyGroups` 量下来：中间那个组仍然带着自己的 `RoleDream` 摘要、它的子（更早的那两轮）仍然指着它，而它与那几轮**同为 depth 2**——因为 `CompressTopicsL2` 每趟只把一个表浅行往下带一层，只有表浅行会被点名下沉，所以「场景列举只到 depth 2」不会替读者藏起任何一条话题（**负例**：把下沉改成 `Depth += 2`，五行里只剩一行还在列举内，新用例当场以「the read's own depth cap hid a topic」红）。指南里第（1）条的措辞因此改口：被跳过的那一行不一定是一轮的原文，也可能是更早的一个组；跳过它的理由是「它说过的已经进了后一份摘要」，而不是「那只是原文」。宿主侧无需改动，新增 `internal/dream` 一条用例，方法数与磁盘格式不变。

22. **把「没收束的那一轮不出现在场景读里」写成契约并钉住**：话题那一行是**收束时**由 `Update` 建的（`internal/update.go` 调 `CreateTurnTopicL2`），开轮那次只铸 id、不建行——所以 `SceneContext("")` 列出的永远是已收束的轮。这条此前没写下来，而它正是「一轮里召回好几次」安全的根据：带着工具调用的循环不会把自己写了一半的这一轮再喂回模型，而轮中 `AppendArchive` 的原文在收束后成为这一轮自己的消息。新用例实测整条序：开轮后 0 行、记一句 + 一条事件之后仍 0 行、同一时刻 `SearchL4{TopicID}` 交出那 2 条、收束后 1 行且消息 3 条、再开一轮时已收的那行仍在而新开的仍不在（正向对照，排除「列举根本没看见」）。**负例**：把建行时机搬到开轮（`search.go` 里直接 `CreateTurnTopicL2` 并装回镜像），用例当场报 `the open turn showed on the scene read`。零生产代码改动，两份指南 §6.5 同批补一句。

23. **把「模型不回合并，下一趟还会再问」这条成本形状量出来并写进配置表**：门限按 depth-1 计数、每次收轮检查一次，而被拒的那趟不改计数——于是一个模型始终不肯合并的场景每次收轮都仍然超阈。新用例数着 stub 的调用次数实测：三趟 = 三次，也就是每场景每趟一次是上界（同一场景有 Dream 在飞时中间那几次收轮不叠加），且表层行数一动不动（排除「其实压掉了但没记」这种解释）。另一头的闸门也量到了，但它收不住这条花费：`DreamCompressMinTopics` 确实判在**问模型之前**（`compress.go` 里低于门限直接 return），可默认 20 本来就低于触发的 24，而模型一直不回合并时每收一轮表层还多一行——抬那道闸是延后询问而不是取消询问（且它同时是提示词里的收敛目标，动它等于改巩固的激进度）。于是收住这条花费只有一条路：把触发写负数、按宿主自己的节奏驱动 `Dream`。库刻意不去记「上一次拒过」——那要新造一份会出错的持久真相，本次明确不做。两份指南的配置表同一格里补完。零生产代码改动。

24. **补上不花钱就出数的引擎基准**（`test/benchmark_engine_test.go`，七条 `BenchmarkEngine*`，与离线接口面共用同一只假服务器，零额度零网络）：收束一轮、纯场景读、开轮、逐条记、巩固一趟、重开一份有内容的库、重开一份只有画像的库。Apple M2 实测（`-benchtime=20x/40x` 两遍，语料 **3 场景 × 40 已收轮**、当前场景表层 40 行）：一轮 ≈18ms 且**每轮 1350 字节、6 条记录**——append-only 单文件的膨胀率第一次有了数；纯读 ≈0.2–0.3ms、开轮 ≈2.4ms、逐条记 ≈2.0ms、巩固一趟 ≈19ms、重开这份 120 轮的库 ≈0.4–0.6ms 而空文件 ≈0.3ms，于是「扫语料」与「开文件的固定成本」第一次分得开，且前者只占后者的一成——「一个 worker 另开一份库」的启动代价基本是固定的那部分。这条是先量错一次才对上的：播种跑在计时区里，重开被报成 400 倍的天价，靠「空文件对照」＋「另跑一次只开不写并单独 profile」两步拆穿自己的；修法不是补一句 `ResetTimer`，是把播种整个放进计时器停着的区域（形状上不可能再算错），另加一条断言挡住「语料其实是空的」（负例：把该断言的门限乘 50，它当场报 722 条记录 / 120 轮），再把**实际造出的形状**打出来（场景数 + 这条读真正看到的表层行数，负例＝把 `NewScene` 改回只在整场第一轮为真，自检当场报 `1 scenes, 120 rows`）。三处测试 helper 的入参从 `*testing.T` 放宽到 `testing.TB`，生产代码一字未动。
    补一条同一批里改掉的播种错：`seedEngineBench` 里 `NewScene` 只在该域第一轮写了 `true`，而空场景入参的 `Search` 是**续用**当前会话，所以「3 场景 × 40 轮」其实一直是「1 场景 × 120 轮」——场景读的数因此偏高（1.16ms 量的是 120 行的表层）。改成每组第一轮才 `NewScene` 后，标签与语料一致，上表就是改后的复测。

25. **「从没说过话的域」从一次错误改成一次空答案**：`SceneContext("")` 对这样的域此前报
`ErrNotFound`，而这条读承诺零写入——它承诺的是「不替你新建会话」，不是「答不出没有」。两份指南的
§6.5 第八条事实第一条原本写的是**要宿主自己把这一码翻成空答案**：决策循环每次问模型之前都读记忆，
端口报错即整次调用终止，于是「还没有任何记忆」的第一次召回会打死刚建好的那只猫，每个宿主都得抄同
一段特判。现在库自己交出空转录（`scene_name` 为空即「还没有会话」，与「有会话但一轮没收束」仍然分
得开），而**点名而要读的场景不存在仍是 `ErrNotFound`**（含 0 这个库从未发出的 id，所以「空答案」不
会吞掉一次真正的找错）。证据：换后端克隆树里那 7 行特判删掉后 memory 三包全绿；负例＝把引擎改回拒
答（overlay 只改 `readScene` 那一个分支），`TestFirstRecallOnAFreshFileIsAnEmptyAnswer` 当场以
`the first recall answered with an error` 红。引擎两处、门面注释、两份指南、AGENTS 与 agent.md 同
批；`readScene` 多出一个 `hasScene` 返回值，签名不外露。公开面方法数、磁盘格式一字未动。

26. **把「宿主可见的列表恒为 `[]`」从承诺变成门禁**（`api/surface_shape_nil_test.go`）：AGENTS 那条「列表恒为 `[]`、map 恒为 `{}`」此前没有任何测试覆盖——它靠门面那一个 `mapSlice`（`make([]U, len(in))`）与 `cloneStrings` 成立，改坏它没有任何检查会红。新测试反射走完 **每条读的返回形状，两遍**：一本全新的库（什么都没写过，空集合最容易露出 `nil` 的地方）与一轮已收束＋一步计划＋一张图之后的同一批读。
    **负例**：把 `mapSlice` 在空输入时的构造换成 `var out []U`＋`append`，该测试当场点名七条越山的 `nil`（`ListL1`、`ListScenes`、`ListL3`、`SearchL4`、`Search.Topics`、`PlanState.Roots` 以及已填充那一遍的 `ListL1`）。零生产代码改动。

27. **列表读口的顺序从此是答案的一部分**（`internal/repo/core/engine.go` 一处 + `api/surface_order_test.go`）：引擎把索引快照交给遍历前不排序，而快照来自 map 键——同一份文件、同一个库，`ListScenes` 每次调用交回的顺序都不一样（实测：同一只猫六个场景，第二次调用就把首个场景挪到了末尾）。修在唯一的那一处：`iterSnapshot` 交出 id 升序，于是一切走枚举的读（`ListScenes`/`ListL3`/`QueryL3Nodes`/`GetL3` 的节点与边/无话题过滤的 `SearchL4`）一起定下来；要别的顺序的调用方照旧自己排（`GroupPlanNodes` 按话题、场景读按时间）。新测试把 12 条纯读各取 8 次、要求编码逐字节相同，**负例**＝撤掉那一句排序，它当场报 `ListScenes answered differently on repeat 1`。id 升序不是宿主眼中的相关性顺序，它是「每次一样」这件事本身；开销在实测里看不见（同一台机器：一轮 ≈20.1ms / 1352 B、纯场景读 ≈0.29ms、重开 ≈0.54ms，与改动前同量级）。公开面形状、磁盘格式一字未动。

28. **指南不再要求宿主复制一份它并不需要的默认表**：`MemHopDefaults` 是四个旋钮，而「没填=库默认」早在 `d427d67`/`b4c62ff` 就归一了，
最短的正确写法是 `api.MemHopDefaults{}`。两份指南此前写的是「要改就复制 `DefaultMemHopDefaults` 再改」，还把字段数写成「三个业务开关」——
一句过期话会让宿主多写一次复制、并且怀疑留空是不是关掉什么。判据钉在入口处（`TestOpenTakesUnfilledDefaultsAsTheLibraryDefaults`：全零表到达引擎时逐字段等于默认表），
按库形状写的宿主侧后端草案带着同一句过期注释，同批去掉后它的三包测试仍全绿。纯文档，代码与形状一字未动。

29. **新增门禁 `api/guide_symbols_test.go`**：两份指南里出现的每个 `api.X` 与每个 `handle.Method()` 都必须存在于已发布的面上。§11 骨架此前只证明它**自己那段**编得过，正文与各层速查表里的符号名没有任何检查对着——而宿主抄的正是这些行。符号集由 `go/parser` 现读 `api` 包自己的源码（非测试文件，包级名与方法各一组），不是手抄清单，所以门禁不可能与被检面各自漂移；实测两份指南当前各有 30~31 个包级符号、22 处方法调用，全部命中。**负例内置成第二条测试**：一段含 `api.SessionContextTopic2`、`db.PlanCreate()`、`db.ListCapabilities()`、`sess.BogusMethod()` 的文本被逐条点名，而同一段里的 `api.LlmConfig`、`db.Search` 放行——退役名与虚构名走同一条判据。零生产代码改动。

30. **§12 两条陷阱说过头，按实测改准并补两条测试**：
    第 5 条写的是「开了没沉淀的轮次不留残渣」——不成立：那一轮已经 `AppendArchive` 的记录仍在，只是没有话题行，于是场景读看不见它，而它挂的那个 id 域往前走之后不再由任何读点名，要等保留窗扫掉。现在这句改成「看不见 ≠ 不存在：放弃一本记了东西的轮花的是空间，不是零」，并由 `TestAbandonedRoundKeepsItsRecords` 钉住两面（场景读只列出已收的那本；整域 `SearchL4` 仍交出那四条记录，含被放弃那一条）。
    第 3 条把话题上的两个时间界写成「其内容的最早与最晚」——也不准：`settleLocked` 取的是**原文**的跨度，轮中事件各按自己的时刻存在，不撑宽那两个界（`TestTurnTopicBoundsIgnoreMidRoundEvents`；负例＝把事件并进取界的那次读取，当场报 `1000/9000, want 1000/1000`）。
    两处都是宿主据以排 prompt 与保留窗的判断句。零生产代码改动。

31. **形状承诺补上编码那一半**（`api/json_shape_test.go`）：「宿主可见的列表恒为 `[]`、map 恒为 `{}`」此前只在 Go 值上钉过，而宿主真正存盘、转发、喂模型的通常是 `json.Marshal` 之后的形状——那里一个没填的集合会变成 `null`，是第三种谁也没要的答复，宿主再解回自己的类型时就得为它加分支。字段名由 `go/ast` 现读本包结构体得出，且**只认「凡声明它的类型都把它声明成 slice/map」的那些 json 键**，所以刻意可缺的标量（`parent_id`、锚点）不会被牵连。跑在未收束过任何东西的库与写过一轮之后的同一批读上。**负例**：摘掉门面对 `Preferences` 的补空映射，测试当场点名 `GetL0.preferences` 与 `Search.profile.preferences`。零生产代码改动。

32. **真实语料的逐字往返钉进离线接口面**（`test/api_interface_corpus_test.go`，零额度）：靶子①里「按 fixtures 出数」这一半其实不需要模型——问答对的答案是推出来的（`7 May 2023` 不出现在原文里），那半确实要真额度；但「语料进了循环再出来，还是不是那一套」是引擎自己的账。新测试取 `benches/fixtures/locomo10.json` 第一个样本（19 个会话、约 370 行，折成 185 轮）整份跑完，逐会话断言：行数等于已收束轮数、每行两句原文与写入时逐字相同、行序即说话顺序（时间戳不减）、每行有可寻址的话题 id 与非空关键词轨，再按宿主的召回规则（在自己那轮的散文里找线索）取中间一轮的原话，要求**恰好命中那一行**。**负例**：在场景读返回前把行序颠倒，它当场报 `round 0 message 0 came back altered`。离线接口面因此从 25 条到 26 条，整组 4.4 秒跑完。零生产代码改动。

33. **门面文档补上漏掉的类型，并把「反向可发现性」变成门禁**：核对时发现 `DB.Stats()` 的返回类型 `DBStats` 在两份指南里**一次都没出现**（§8 把方法列全了，§9 的类型表却漏了它），而 §9 那行还漏着 `DreamReport`/`DreamStage`。宿主读不到类型名就只能去翻 `go doc`——这不是「集成就能用」。现在两份指南各补两行（`DBStats` 进「文件诊断」、`DreamReport`+`DreamStage` 进响应 DTO），并把 `api/guide_symbols_test.go` 的那条配对反过来查一遍：**包里每个导出类型都必须在两份指南里被点名**（`TestEveryPublishedTypeIsDocumented`）。两条负例都跑过：函数级——给一段只提到两个类型的文本，它报出另两个没提的；接线级——临时从英文指南删掉 `DBStats` 一词，测试当场报 `never mentions [DBStats]`，还原后复绿（顺带记一条：`-overlay` 换 `api/types.go` 加新类型骗不动这条检查，因为它读的是盘上的源文件，不是编译器看到的覆盖层）。零生产代码改动。

34. **压实后的「读等价」改为逐字节比对**（`test/api_interface_compact_test.go`，零额度）：`CompactTo` 是全文件重写，也是唯一能悄悄丢一层的路径——副本能打开、能答、看起来正常，而某个桶、某个计数器、某个域没被搬过去。此前只测了「删掉的场景没了、图还在」两件事。
现在建两域共 6 个场景的语料（每轮一次逐条记 + 一步计划 + 一次收束）、删掉一个场景制造墓碑，压实前后对**每一条读**做 JSON 逐字节比较（L0 画像、L1 节点表、场景清单、每个场景的整份转录含关键词轨与两个时间界、整域归档、L3 图），
并断言可达记录数不变而文件确实变小（实测 19370 → 17705 字节、47 条两侧相同）；最后在副本上再收一轮：行数恰好 +1、收的正是刚开的那轮、新铸的 id 不出现在压实前的快照里（计数器被重置就会在这里撞车）。
**负例两条各自独立**：漏掉一类记录 → 报 `copy reaches 22 records, want 47`；把全部记录改挂默认域（记录数只差 1）并摘掉计数断言 → 快照比较自己报 `the compacted copy answers the primary domain differently`。
写的过程中自己也错两处：先拿打开中的文件比 `Stats()`（`CompactTo` 从不动它），以及把 id 以 `0000` 开头当可疑信号（那是约六万分之一的合法情形），已换成断言真实不变量。离线接口面 26 → 27 条。零生产代码改动。
35. **你描述的拓扑第一次有了并发证据**（`api/two_libraries_test.go`）：一家一份库、被招来的 worker 另开一份 `.meh`、两把句柄在同进程里被三个 goroutine 同时驱动（每把 6 轮：开轮→逐条记→收束）。断言三件事：每把句柄铸出 6 个互不相同的话题 id、两个文件铸出的 id 集合完全不相交（一份家族的轮次不会出现在另一份里）、每把句柄的 `SceneContext` 恰好读回自己那 6 轮；外加 worker 文件记录数严格大于 parent（它跑了两倍域数）而 parent 的计数在 worker 全程并发之后一字不变。`go test -race` 连跑三轮干净，顺带把 CI 没进 race 步骤的两个包也本地测了一遍：`-race ./api/...` 与 `-race ./test/ -run TestInterface` 均绿（CI 现在只 race `./internal/...`，扩到全包大约多花 10 秒，属建议不擅动）。零生产代码改动。写这条时我自己先写错一次断言：要求两个文件记录数相等——worker 那份里跑着两个域、parent 只有一个，本来就不该等，已换成能说明「跨文件不串」的判据。

36. **「删图置空锚」对被空闲回收的域也成立，且这条现在看得见**（`internal/anchor_detach_test.go`）：既有的那条只测了一个活着的租户上下文，并且用 `ListScenes(已删图 id)` 为空作判据——那个断言即使扫描把场景全删了也会通过。新测试补三件旧测试看不见的事：域被空闲回收（内存里没有上下文）之后仍要被扫到、场景本身不能因为置空锚而消失、主域同样在覆盖范围内。**负例**：把 `detachGraphAnchors` 的遍历对象从租户注册表换成当前活上下文映射——新测试当场报 `domain … still anchors scene … to the deleted graph`，而既有那条测试全绿（它看不见这个缺陷）。顺带一条由这次核查得到的结论（不是缺陷）：租户注册表在 Open 时由记录重建、且不随空闲回收删除，所以被回收的域不会被漏掉——这个性质此前无人断言，现在由这条测试钉住。零生产代码改动。

37. **指南说清『两口时钟』**（§12 第 3 条，中英各一段）：宿主记的内容与它收束的那一轮带的是**调用方的**毫秒戳，缺 `CreatedAt` 时库**不补表**；而计划节点自己的三个戳、画像的 `UpdatedAtMs`、图的内容钟由库按同一刻度盖。这句话之前只在实现里，而它是端口形状问题：决策循环的记忆端口（`Remember(facts)` 一类）通常不带时间字段，适配器于是必须自己取钟——不写出来，第一个宿主会以为留空即可（实测：`Update` 与 `AppendArchive` 同一道闸，`CreatedAt=0`、负数、秒级带内都拒，秒带以下的相对值放行）。既有测试已覆盖这两个边界（`internal/update_test.go`、`internal/append_test.go`），本次只补文档。零生产代码改动。

38. **合并要活过重开，这条此前没人测**（`test/api_interface_merge_reopen_test.go`）：合并是唯一会**改写记录归属**的纠错口（被并场景的每个话题把 `scene_id` 换成活下来的那个），而它原有的那条用例只看活着的答复——缓存改了、记录没改，它照样绿。新用例走完「甲两书 + 乙一书 → 合并 → 关掉重开」，重开后必须仍只看得到一个场景、三行、六句原文一字不差、每行关键词轨非空，且被并进来的那一轮按它自己的话题 id 还能读出两条归档。**负例**：让 `MergeScenesL2` 只搬缓存、把话题记录原样写回（`scene_id` 仍指着已删场景），新用例当场报 `the surviving scene lists 2 rows after reopen`，而既有那条 `TestInterfaceMergeScenes` 在同一变异下全绿——盲区被证实，不是预防性加测。零生产代码改动。

39. **删场景之后，文件必须与『从没建过这一场景』的文件逐层等量**（`internal/delete_scene_orphans_test.go`）：级联漏删任何一层都不会被现有测试抓到，因为它们都只查活着的答复；被缓存不再显示、但日志仍留着的记录，会在下一次重开时（索引由记录重建）原样回来。新测试不枚举『 cascade 应该删什么』，而是拿两份文件做对照：X 建两场景（2 轮 + 1 轮）删掉前者，Y 只建那个活下来的场景，两边都关掉重开，四层计数必须逐项相同（场景、话题、归档、计划节点）——期望于是变成算术而不是信念。**L1 有意排除**：被删场景的共现超边按设计留给下一次 Dream 的衰减剪（`internal/agent.md` 已写明），而它不是任何宿主读得回来的记录。**负例**：让 `DeleteCascade` 跳过计划节点那一步，测试当场报 `deleted-file {… planNodes 3} vs never-had {… planNodes 1}`。零生产代码改动。

40. **图的边界与删除级联各钉一条会红的断言**（`internal/graph_scope_test.go`）：关系按标题指名另一端，而解析范围**限于本图**——这条划界正是「删掉一张项目域」可安全的原因：若某条边伸进别的图，活下来的那张图的子图遍历就会点一条读不回的记录，而本仓对这种分叉的回答是硬 `ErrIO`，不是跳过一行。测试同时钉三件事：跨图关系**逐项被拒**（不是静默解析）、提问的那张图不留下任何边、删掉该图后在重开之下与「从没导入过它」的对照文件逐项等量（图槽、节点、边）。三个变异各自红在正确的地方：只放宽成员查找（别的图的标题也算命中）→ 当场报 `a relation naming a title from another graph was accepted`；删图时漏掉图槽本身 → `the pool holds [4 2 1], while the file that never had it holds [1 2 1]`；（第一版变异顺手污染了边去重的键，红在对照侧——不算精准证据，故重做了只放宽查找的那一版。）过程中也修了自己两处想当然：按 `GraphIDs` 下标认图（顺序不保证，报 `record not found`），改成「除目标图外全部删掉」的与顺序无关写法。零生产代码改动。

41. **删话题的级联要带走「子名下」的记录，不只是被点名那一个的**（`internal/l2_test.go` 的一条测试加宽）：原断言查了父子两个话题记录与归档，但归档与计划节点只挂在**父**话题名下——闭包算错时子的记录会留在盘上，谁也都读不到它，只在压缩与统计里占位。现在给子话题也备上一条归档与一步计划，删除后逐项要求它们不存在。**负例**：把 `DeleteTopic` 的闭包改成「只处理被点名的那个 id」，四条断言一起红（子话题记录、其缓存项、子的归档、子的计划节点），而另一条 `TestDeleteTopicSubtreeComesFromParentID` 在同一变异下**不红**——它查的是子话题是否消失与场景读，覆盖不到归属记录的删除，故这不是重复覆盖。零生产代码改动。

42. **巩固后的场景读必须活过重开**（`test/api_interface_consolidate_reopen_test.go`，零额度）：巩固是唯一「不删东西却改写可见形状」的一趟——被合并的轮次沉到 depth 2、组行出现在 depth 1，而宿主读到的列举来自一份由 Dream 增量维护的缓存。缓存与记录一旦分叉，症状就是**同一份文件在进程重启前后给出不同的召回**：宿主眼看着某些轮消失。测试先跑一趟巩固，再把同一文件关闭重开（索引由记录重建），要求两次快照逐字节相同；快照里另设一道守卫：必须既有带子的组行、也有下沉的子行，否则这条比较就是空转。两个变异红在不同的地方，正好把两层守卫分开：**L4 镜像重建漏掉每轮的用户那一行** → 红在逐字节比较那一行（只有重开之后缺项，活路径看不出来，正是要防的事故形状）；**重建索引时丢掉 depth>1 的行** → 红在快照守卫（`groups 0, sunk 0`）。过程中也纠正了自己一个想当然：以为「只改缓存不落记录」会造成活路径看得见、重开后消失——实测它两头都看不见，说明列举不是纯缓存驱动，这条不变量因此比预想的更稳。离线接口面 28 → 29 条。零生产代码改动。

43. **`SceneContext` 的行序三键全部有断言了，指南也第一次把它写出来**：实现里排序是 `(UserTimestamp, 浅的在前, 话题 id)`——第二键不是装饰：融合组带的是它吞掉的第一轮的戳，并列是常态，而宿主的四条折叠判据按行线性读（组行若落在自己总结的原文中间，就等于没总结）。此前只有一处断言查主键（`test/api_interface_corpus_test.go`），次键只在实现注释与 `internal/agent.md` 第 14 条里；两份指南的 `SceneContext` 那行更是**根本没提行序**，于是宿主只能自己再排一次（换后端草案确实排了——可证是多余的）。现在：巩固那条重开测试把三个键逐项断言，并加一道「本次数据必须真的出现过并列」的守卫（否则次键等于没测）；**负例**＝把 `Depth` 次键反向，测试当场报 `at a timestamp tie the deeper row leads at 1, so a group does not introduce its own originals`；两份指南的 `SceneContext` 行补上这句顺序承诺；草案里那次 `slices.SortFunc` 随之删掉（宿主三包仍全绿）。零生产代码改动。

44. **指南的可跑骨架现在被真的跑一遍**（`test/guide_skeleton_test.go`，零额度、随 `go test ./test/` 与 CI 一起走）：`make check-guides` 只把骨架编译一次——那只证明名字对得上，而宿主照的是文档不是测试：一个编得过、第一次调用就 `log.Fatal` 的 quickstart 它看不见。新测试用与 `check-guides` 相同的抽取规则取出两份指南里 `package main` 那块，照宿主的处境放进一个临时模块（`replace` 指向本仓、`GOPROXY=off`、从模块缓存解析依赖），构建后**以假端点跑起来**：四个环境变量指向包内的 mock LLM 与临时 `.meh`，要求退出码 0 且输出里没有 `panic:`。两份指南各自独立成子测试。**负例**：临时把骨架里一次 `Update` 的时间戳改成 0（正是本轮刚在 §12 写明「库不替你补表」的那条边界），测试当场报 `a positive timestamp is required` / `exit status 1`；还原后复绿，文件经 `git checkout` 回到提交状态（改完即核对工作树）。零生产代码改动。

45. **门面的每条公开方法都必须带注释，这条现在被机器守着**（`api/facade_docs_test.go`）：`internal` 不发布，`go doc …/api.Session` 是宿主唯一读得到的文本，一条没写注释的方法就等于宿主无法从文档学到它（正是「集成就能用」的反面）。扫描与指南符号门禁同一路子：`go/parser` 现读本包源码，要求 `Session`/`DB` 上每个导出方法都有 doc，并把数到的方法条数钉在 32（25+7）——否则解析走空也会「通过」。当前 32 条全部有注释。**负例**：删掉 `RenameTopic` 上面那七行注释块，测试当场点名 `(Session).RenameTopic is exported but carries no doc comment`；还原后复绿（改的是盘上真实文件——这条与符号门禁一样读源码而非编译器的覆盖层，所以 `-overlay` 骗不动它，第一次用 overlay 做的变异因此假绿，换成临时改+`cp` 还原后才拿到真红）。同一轮也把 AGENTS 里可机械核对的事实逐条对回代码：直接依赖 3 个、记录帧 `RecordHeaderSize = 26`、`FormatVersion = 0x0012`、`SnapshotVersion = 0x03`、平台文件 `filelock_unix/_windows` 齐——全部相符；并把三仓 e2e（14 条）与宿主换后端整棵树在 `c33fe43` 上重跑，均退出 0。零生产代码改动。

46. **指南里每个被反引号包住的标识符都要有出处**（`api/guide_symbols_test.go` 新增第三条检查）：符号门禁只管 `api.X` 与 `handle.Method()`，而正文与速查表里更多是**裸名字**——字段、常量、枚举模式。这些位置正是改名后会悄悄留下假话的地方。现在扫描 `api` 与 `internal` 全部源码，把包级名、方法名、结构体字段与（供文档引用的）测试函数名合成一套合法出处，指南里每个反引号内的大写标识符必须在其中；唯一白名单是文档刻意说「不存在」的那两个名字（`FormatID`/`ParseID`）。**它当场抓到两处会坑宿主的写法**：枚举清单写成 `L3ImportSkip` / `Merge` / `Overwrite`——实际名是 `L3ImportMerge`/`L3ImportOverwrite`，宿主照抄 `api.Merge` 直接编不过（中英各一处，另一处是 `ImportL3` 的 `Merge` 模式）；还有一处把状态词写成 `Done` 反引号（并非任何常量，已改为「状态为 done」）。**负例**：把清单里一个名字改成 `L3ImportMerged`，门禁当场点名。零生产代码改动，两条指南骨架照常编译与运行。

47. **两个 LLM 预算的「留空即默认」从一句话变成断言**（`internal/llm/provider.go` 抽出 `budgets` + `internal/llm/provider_test.go`）：`TimeoutSecs` 未填取 120 秒、`MaxOutputTokens` 未填取 8192——门面别名的注释早就这么写，此前却没有任何测试钉着。两个方向的失败都静默：0 输出上限会把每次回复截成空，而 0 HTTP 超时不是「立刻失败」是「永远等」，一次挂死的端点就能把一个域的锁一直占着。抽成纯函数只为可测，行为与门面面积一字未动（go-openai 这个版本没有读回 HTTP 客户端的访问器，故断言落在 `budgets` 与 `Provider.MaxOutputTokens()` 上）。同时补指南 §4 那两行：英文写的是「—」、中文写的是「否」，都没说留空会怎样——现在写明 120/8192 与「留空才是常态」；`internal/llm/agent.md` 补上这条参数语义。

48. **保留窗的清扫读的是钟，不是轮的状态——这条此前被写反了**（`internal/dream_midturn_test.go` + 三处文档）：既有那条只测了**窗口内**的轮中记录（时间戳取「刚刚」），于是模块文档与本机 AGENTS 都把它写成「Dream 不扫开着那一轮已写的记录」。实测不成立：把 `ContentRetentionMs` 压到 1 秒、往开着的一轮里写一条钟在一小时前的记录，一趟 `Dream` 之后它就是 0 条——**过窗即扫，与轮开没开无关**；而这趟 Dream 也不接管那一轮：挂着的 `Update` 照样落在这个轮上（它自己写的两句是新钟，因此留下），收束成功后场景读正是 1 行 2 句。现在两个方向各有一条测试钉住，并把夸大处改准（`internal/agent.md` 第 30 行、两份指南的保留窗那一格明确写「开着的一轮也不例外」、既有测试的注释划回窗口内）。新测的**负例**：让清扫在有开轮时把 cutoff 归零（等价于豁免），它当场报 `the expired mid-round record survived the sweep`，而窗口内那条不受影响。零生产代码改动。

49. **计划树要被豁免得同时满足两条，指南只写了一条**（`internal/dream/plan_prune_test.go` + 两份指南 §12 第 7 条）：实现里是「仍有未到终态的步」**且**「窗口内有过活动」才跳过整棵树（`internal/dream/prune.go` 的 `agg.HasNonDone && agg.LastActiveAt >= cutoff`），不豁免的树里每一步再各按自己的 `UpdatedAt` 计时；指南写的是「仍在进行的树豁免」，宿主据此会以为一棵被搁置的在途树永远留着——而那正是这一阶段存在理由的反面（L5 失去上界）。两个组合此前都没有测试钉着：既有那条（`TestPrunePlanStageSkipsWhenTheTreeIsIncomplete`）只测了被豁免的一侧。新用例一次摆三棵树（在途且窗口内活动 / 在途但静默 / 已完结且两步一旧一新），一趟裁剪后逐棵读回：第一棵 1 步全留，第二棵整棵没了，第三棵只留下窗口内那一步（4 条节点剪掉 2 条）。**负例**：把 `&&` 换成 `||`，后两条断言当场分别报 `an in-flight but silent tree survived the window, so L5 has no bound` 与 `a finished tree should lose only the stale step`，第一条不受影响——哪一条漏了都红在自己那个方向上。零生产代码改动。

50. **图槽的 `updated_at` 是内容变化钟，这句话第一次有了断言**（`internal/l3_graph_clock_test.go` + `api/types.go` 的注释补上漏掉的那一半）：宿主按「最近改过的知识」排一张图清单时读的就是这个字段。实现里一次批量导入维护两个集合——把某个域解析到了哪几张图（`GraphIDs` 交回的就是这一份），以及真写过节点或边的有哪几张（只有这一份会被盖钟）——四个方向此前都没有测试：读口盖不盖钟、什么都没写的批次盖不盖、只被解析到没被写过的图盖不盖、真写过的图盖没盖。新用例先把两张图的钟倒回 1000 与 2000，再逐项验：`ListL3`、`GetL3`、`QueryL3Subgraph` 之后仍是 1000 与 2000；一趟 `Skip` 批次把两个域都解析到、四条节点全部跳过、`GraphIDs` 仍点名两张图，而两口钟一动不动；只改写其中一张图那两个节点的那一趟，让那张图的钟前进、另一张精确停在 2000；改标签也算一次变化，同样盖钟。钟是从记录本身读的，所以「前进的那一口」不是被上一次列举顺手盖出来的。**四条负例各红在自己那条断言上**：让 `ListL3` 顺手盖钟 → `reads advanced the clocks`；把 `Skip` 也算成变化 → `a batch that wrote nothing advanced the clocks`；把盖章范围从「写过的图」换成「解析到的图」→ 同一条报出来；不再把任何图标记为已改 → `the graph this batch wrote kept its old clock`。门面那条注释此前写的是「改了会动、没改不动」，漏了「读也不动」，补上。零生产代码改动，公开面与磁盘格式一字未动。

51. **节点衰减「读钟即回戳」这条设计第一次有了可核算的不变式**（`internal/cap/engram/decay_compose_test.go` + `api/types.go` 的 `SceneNodeView` 注释 + `internal/repo/l1sync_test.go` 一处断言补牙）：L1 节点没有独立的衰减钟字段，衰减按 `UpdatedAt` 算，而算完那一趟又把它回戳到本趟时刻——两者只有在**衰减可复合**时不打架：两趟各一小时，必须落在与一趟两小时同一个重要性上。实测成立（差 <1e-12），而此前没有任何测试守着这条：把 `dt` 的来源改按 `CreatedAt` 算，两趟就衰到 0.9704 而一趟是 0.9802（头一个小时被算了两遍），这正是回戳存在的理由。新用例钉三件事：本趟之后钟停在本次时刻；两趟一小时与一趟两小时等价；区间里被别的写者推进过这口钟的节点，不为整段区间买单。**两条负例各红在自己那条断言上**：删掉回戳那一行 → `the pass left the node's clock at`；`dt` 改按 `CreatedAt` → `forgetting does not compose`。同一口钟的三个写者（`l1layer_sync` 只在话题集合变化时推进、`decay` 每趟回戳、`BackfillL1Emotions` 只在数值真的动过时推进）此前在门面文档里一个字没有——`SceneNodeView.updated_at` 只写了「毫秒」，宿主会当成「最后改写内容的时间」来排记忆；注释按实话补明它是**遗忘所依据的那口钟**，答的是「这段记忆最近一次要紧是什么时候」。顺带把 `TestSyncL1NodesFromL2` 的两头补齐：那条「空转的一趟不许回戳」原写作 `err == nil && UpdatedAt != first`，记录不见了就不读也不报（负例：让空转那一路把节点删掉，旧断言静默放过，现在报 `the no-op sync left the node unreadable`；真去回戳的负例报 `a no-op sync refreshed the clock decay measures from`）；另一头「集合变了必须带着新钟写下去」此前一个字都没钉，现在把钟预先停在哨兵值 1，同步之后不许还是 1（负例：删掉 `syncOneSceneNode` 里那次回戳，报 `a changed topic set left the clock on the sentinel`）。`internal/cap/agent.md` 与 `internal/repo/agent.md` 各补上这条钟归谁读、谁推进。零生产代码改动，公开面与磁盘格式一字未动。

52. **关键词提炼不再把「撞到输出上限」说成「这个模型不会答 JSON」**（`internal/cap/llmops/keywords.go`，本轮唯一一处生产代码改动）：提炼的尝试阶梯是三档递增的输出预算加最后一次格式重试，而推理型模型最常见的失败形态就是整段预算被思考吃掉、每一档都回 `finish_reason=length`。此前最后一步把这种截断改写成了 `errKeywordFormat`，文本是「returned no parseable JSON; check the model's structured-output capability」——两档都带 `ErrLLM`，错误文本是宿主分辨「抬 `MaxOutputTokens`」还是「换模型」的唯一通道，那句把宿主送去查一个从没答错过的端点。现在最后一步让传输层自己的错误原样上抛（那句话带着 `max_tokens=N` 与 `ErrTruncated` 因果），分块提炼再补上「第几块 / 共几块」并把原错误留在因果里。同一条判据在本仓已写过两次（巩固与蒸馏把那处「重试没跑成被报成答非所问」改准过），这是第三处，也是最容易被宿主当成模型质量问题的一处。`ErrTruncated` 仍是传输层的 `errors.Is` 标记，**不新增公开错误码**，方法数与磁盘格式一字未动。证据：`TestKeywordExtractionKeepsATruncationAsTheCeilingItIs`（阶梯走满 4 次调用、截断留在错误链里、文本不再出现 no parseable JSON）与 `TestChunkedExtractionNamesTheChunkAndKeepsTheCause`（5000 字切成 3 块，报 `chunk 0 of 3` 且在第一块就停）。**四条负例各红在对应断言上**：把截断改回伪装、包装时丢掉因果、包装时丢掉块号、以及去掉那层包装。

53. **宿主可以只存一件标识符：域既有名字可幂等取，也有 id 可读回、可拿回去**（`Session.AgentID` + `DB.Agent(llm, id)`，本轮公开面两处加法，用户裁定）：v1.6.4 把域 id 从公开面收掉，理由是「名字就是唯一的把手」；真接入里宿主仍然要为每个域存一件标识符，而名字要在 `Open`、`SubAgent`、`ProfileInput` 三处各抄一遍，抄错一次就安静地建出第二个子域。现在句柄上的 `AgentID()` 交出该域 16 位 hex 的 id（主域就是那个隐式零号值），库句柄上的 `DB.Agent(llm, id)` 按它取回同一个域、并把端点换成本次传入的那个；它**不建任何东西**——这个文件从没注册过的 id 以 `ErrAgentNotFound` 拒，一句不是 hex 的字符串在碰任何域之前以 `ErrInvalidQuery` 拒，所以一个打错或编出来的 id 开不出一个顶替真域的空记忆。id 仍然不透明、仍然全部由库发出、仍然不许宿主自造，放弃掉的只是「id 彻底不出门面」这一条形式收益。测试按形状钉：三个域三个互不相同的 16 位 hex，同一 id 在重开之后仍指向 worker 自己的画像（`先写测试再动手` 逐字段对回），另一个 id 落在另一个域上，主域的 id 交回主域，未知 id 与被当作 id 递进来的名字各拿自己那一档码。按 id 取回同时把该域挂到本次传入的端点上（正活着的域立刻换 transport），这条由 `TestAgentByIDMovesTheLiveDomainToTheNewEndpoint` 钉。**四条负例各红在自己的断言上**：撤掉准入 → 未知 id 答成 code 0；撤掉解析 → 名字递进 id 这道门也不报错；把 id 换成「一律回主域」→ 句柄交出 `0000000000000000`；撤掉装端点那一步 → 活域仍跑在旧端点上。**公开面从 25 + 7 变到 26 + 8**：清单、门面注释门禁的条数（34）、两份指南的入口段与 §9 速查表、AGENTS 与 `internal/agent.md` 的「两个入口」同批改口；决策档案里那条「保留一个按 id 取会话的方法作为逃生口」的落选理由改写为「2026-09-23 改判采纳」，原理由与为什么不再成立都留在 `notes/implemented/architecture/2026-09-11-open-decides-the-primary-domain.md`。磁盘格式 `0x0012` 不动（域 id 本来就在每条记录的帧里，注册记录早已存在），对宿主是纯加法，不断任何既有调用。

54. **域 id 的作用域是一个文件——这条被三仓 e2e 第一次点这道门就撞了出来**（新增 `TestAnAgentIDAddressesADomainInsideOneFile`；门面注释、两份指南、AGENTS 与 `internal/agent.md` 的「域身份三个入口」同补一段）：按「一个 agent 一个文件」部署时，两个文件各自的主域**都是那个隐式零号域**，`AgentID()` 交出同样的 16 个 0；如果宿主照上一轮的写法拿 id 建一张跨文件的全局表，两个 agent 的记忆就会被叠到一起。现在把实话写进宿主读到的每一处文本：一个 id 在**一个文件内**唯一地指一个域（`DB.Agent` 因此没有歧义），跨文件的键是路径；测试实测两个文件的同一个零号 id 各读回自己那份画像（`first-primary` / `second-primary`），并钉住「同一文件内主域 id 与子域 id 必不相同」，所以宿主在单文件里按 id 建表不撞车。指南里那句「宿主可以只存一件标识符」随之限定为「在一个文件内只存一件」。e2e 程序从 14 条长到 16 条：按 id 重开本文件主域交出同一批话题、一个文件内两种 id 互不相同、编出来的 id 在门口被 `ErrAgentNotFound` 拒。零生产代码改动（本轮改的全是文档注释与一处测试），公开面与磁盘格式一字未动。

55. **发现口：`DB.Agents()` 列出这个文件里有哪几个域**（用户裁定；`internal/agents.go` + `api/open.go`，公开面 26 + 9）：上一轮开的 id 门只能「已知 id 就取回」，反过来问不出来——一个 `.meh` 里有几个域、各自 id 与名字是什么，宿主只能靠自己那份名册；名册丢了只能猜名字，而 `SubAgent` 猜错会在真域旁边安静建出第二个空域。现在 `Agents()` 交回 `[]AgentInfo{ id, name, primary }`：主域领头（它的 id 就是那 16 个 0），其余按 id 升序，所以同一份文件两次答案逐字节相同——这条挂在 `surface_order_test.go` 的第 13 条纯读上（八轮编码相同）。它读磁盘上的注册记录而不是内存里那份表，因为这份列表的用途就是「完整」：某条租户键解不出名字，整个列表带着那个原因停下，而不是交回一份少一条的错答（`TestAgentsStopsOnAKeyThatResolvesToNoName`）。宿主侧的形状是「列出来 → 挑一个 → `Agent(llm, id)` 开回来」，测试两头都走：列出的每条 id 都能开回它自己那个域、`GetL0` 交回的名字与列表一致，重开文件后名册一模一样（`TestAgentsDiscoversEveryDomainInTheFile`）。**三条负例各红在对应断言上**：不排序 → 纯读那条报 `Agents answered differently on repeat…`；漏掉主域 → 报 `Agents listed 2 domains…`；吞掉读不回的键 → 包内那条报 `must stop the listing`。文件级 L3 公共池不是域、也没有注册记录，因此不会出现在名单里；新增导出类型 `AgentInfo` 同步进两份指南 §9 与门面注释，AGENTS 里那句「没有按租户划域的注册表」改准为「没有跨进程的多租户注册表（`DB.Agents` 只列本文件的域）」。磁盘格式 `0x0012` 不动（读的就是既有记录），对宿主是纯加法。

56. **「返回 plan 式上下文」第一次有了实测代价，并写进两份指南**（`test/benchmark_engine_test.go` 新增 `BenchmarkEnginePlanContext`，零额度零网络）：宿主每轮循环都要把这一轮的计划送进 prompt，而「自动进行压缩返回」这句话此前没人量过——两次读就是它的全部代价：`PlanState()` 拿树，再一次 `SearchL4{TopicID, Kind: event}` 拿绑步骤的事件（每行 `NodeSeq` 就是归因）。语料按你说的形状摆：一棵 21 步的树（一个根、五个子、每子三叶）且**每步都带自己的事件**。Apple M2 对离线桩端点实测（`-benchtime=100x`，两遍一致）：这一对读 ≈**97–99 µs**，一次答案 **8268 字节** JSON（每步约 0.4 KB，算的是传输格式而非宿主渲染出的文本）。这条也顺带把「压缩」二字的实话写清：引擎不截断任何东西，`PlanState` 交出整片森林；它唯一会做的压缩是语义那一层——一个 done 父节点的摘要在其直接子全部到达终态后折上来（`TestPlanRollupWaitsForEveryChild`），一步的取值范围是它自己加整棵子树（`TestStepReadCoversItsSubtree`）——所以 token 预算仍归宿主，与召回那条路同一条标准：在被证明需要之前不加按预算裁剪的读口。语料自己报数（21 步 / 21 事件 / 树总数三个数一起断言），把「量了个空树当便宜」这条路堵在断言里；这条是新加第八个基准，负例＝把事件循环改成不写（`AppendArchive` 那一步去掉），断言当场报步数与事件数不等。**同步**：AGENTS 的测试现状从七条改为八条并补上这两个数，两份指南的 L5 一节加一段实测代价。生产代码一字未动，公开面与磁盘格式不变。

57. **把一张图搬进新库这条宿主必经的路，第一次跑通并去掉它身上唯一的类型转换**（`test/api_interface_graph_copy_test.go` + `api.HypergraphNode.SourceRef` 由 `*string` 改为 `string`）：worker 自己开一个 `.meh` 就自带一个空的 L3 池——公共池按文件算，不跨文件跟人走，所以宿主必然要会复制图，而这条路此前没人走过。走通的结果是它全程只经公开面：`GetL3` 交回节点（带标题）与超边（成员 id 列表），宿主一个节点写一条 `L3ImportItem`、把每条边挂到它序号最小的成员名下作为那条目的 `Related`（超边是无序集，选谁当锚都不丢东西），再用同一 `Domain` 导入——图的 id 由标签派生，副本因此落在另一个文件里**同一个 id** 上。断言逐节点比 `NodeType`/`Content`/`Keywords`/`SourceRef`、按 kind 加排序后的标题集合比边，并要求同一份复制再来一遍仍是三条边（Skip 模式重放）。**路上撞到的唯一一次转换**：写入侧 `L3ImportItem.source_ref` 是 `string`，读回来的 `HypergraphNode.source_ref` 是 `*string`——而库里那个指针从不存空串（`MergeFields` 空即保留、`OverwriteFields` 空即清成 nil），也就是说两侧表达的是同一个集合「没有，或一个非空引用」。读侧因此收成 `string`：`omitempty` 下键的缺席条件一字未动（**编码后的 JSON 与改前逐字节相同**：实测同一个 `omitempty` tag，`*string` 的 nil 与 `string` 的空串都让整个键缺席，非空两边写出同一个 token），只是宿主不再需要 `deref` 才能把读回的节点写回去；`TopicSlot.parent_id` 保持可选指针，因为它没有写侧可对称，指针在那里回答的是「depth-1 还是被折走的」这一件真事实。**对 Go 宿主这一处是 breaking**：写过 `*n.SourceRef` 的调用点编译断（改成 `n.SourceRef` 即可），meowagent 跟版时一并处理。两条负例各红在对应断言上：`copyBatch` 丢掉 `Related` → 报 `EdgesCreated:0`；丢掉 `Keywords` → 报某个节点「came across changed」。指南 L3 一节新增一段「把一张图复制进另一个文件」，AGENTS 的测试现状与字段所有权同批（离线接口面 29 → 30 条）。磁盘格式 `0x0012` 不动。

58. **宿主那半画像是整次写入替换，不是按 key 合并——这条以前只写在实现里**（`internal/l0_test.go` 新增 `TestUpdateL0WritesTheHostHalfWhole` + 门面注释与两份指南同补一段）：`ProfileInput` 里除 `Name` 外的三项「可以留空」，此前没人说清留空是什么意思——实测是**替换**：只写一条 `Preferences` 就只剩那一条，省略 `Role` 就清空它，nil 表与空表是同一个答案；而被继承的是蒸馏那两项与 `AgentType`（`TestUpdateL0KeepsDistilledHalf` 早就钉着）。指南以前那句「`Name`、`Role`、`Preferences` 只有宿主一个写者」说的是所有权、不是合并语义，宿主照字面做「改一条偏好」的工具就会静悄悄丢掉其余偏好——这条正好落在你的第 1 条上（用户可修改字段）。为什么不做按 key 合并：那样一条偏好就永远删不掉（要删就得先造一套墓碑或一个显式删除口，比现在多一处形状），所以姿势是先 `GetL0` 拿表、改完整张写回，用例把替换与这条读-改-写两头都钉住。**负例**＝让写侧顺手继承空的 `Role` 并合并偏好表，用例当场报 `Preferences survived the edit as map[language:en tone:terse], want exactly the one this write named`。零生产代码改动（本轮改的是断言与文档），公开面、编码与磁盘格式一字未动。

59. **「`ErrAgentNotFound` 公开面上没有任何调用能产出它」这句被两批前的改动自己作废了，这轮清掉**（两份指南的错误码段 + `internal/common` 那条注释 + 可跑骨架里的新门）：`Agent(llm, id)` 正是宿主交回 agent id 的那个口子，id 从没注册过时它答的就是 3002——上一轮加门时改了入口段与 §9，却漏了远处这句相反的话。同时把两条新门写进 §11 那段可跑骨架：`lib.Agents()` 列名册、`lib.Agent(llm, db.AgentID())` 按 id 开回来，`llm` 因此从 `api.Open` 的实参提成了变量。这段才是宿主真正抄走的东西，而 `make check-guides` 只保证编得过、`TestGuideSkeleton` 才保证跑得起，两道现在都绿。核对方式：把两份指南、AGENTS、`internal/agent.md` 与门面注释里所有「id 不越门面 / 没有任何调用能产出该码」的句子逐条扫一遍，剩下的只有讲锁序与准入的那句（`contextFor` 校验注册表），它仍然对。本轮只动文档与注释，形状与生产代码一字未改。

60. **读侧的时间界补上与写侧同一把尺子**（`internal/content` 抽出 `wrongScale` 一处判两段、`CheckQueryBound` 只管查询界，`internal/l4.go` 的 `SearchL4` 接上）：`AppendArchive`/`TurnEnd` 的 `CreatedAt` 早就拒秒级（1e9–1e11）与微秒级（>1e14），但 `SearchL4{Start, End}` 不拒——而这两条界是拿记录的毫秒戳去比的。错单位的界**从来不是它自己说的那个窗口**：`Start` 给秒级值比任何戳都低，于是整库都放行（实测那一次答回 4 行）；`End` 给秒级值又把所有记录挡在外面，宿主很容易把一次空读解成「那段时间没有记忆」。同一判据本仓已经用在两处（枚举过滤器一律拒未定义值、写边界拒错单位），这次补上第三条：错界以 `ErrInvalidQuery` 拒，文本里点名看到的是哪一档尺度；`0` 在查询里仍然是「不设界」（与写侧的「必填正数」不同形，所以两个函数分开：`checkTimestamp` 管写入，`CheckQueryBound` 管界，共用同一组常数与 `wrongScale`，判断只做一次）。新用例 `TestInterfaceL4TimeBoundsRefuseTheWrongUnit` 五种界各钉一条（含两端正确界的正向对照、小于秒级下限的相对值仍然放行）。**负例**＝摘掉那两次调用，秒级 `Start` 当场答回 4 行而不是拒绝。L4Query 两条字段的注释、两份指南的 L4 一节、AGENTS 的毫秒那条与三份包文档（`internal/content`、`internal`、`internal/repo` 各自那处判断的归属）同批改口。磁盘格式与公开面一字未动。

61. **`Dream(ctx, sceneID)` 的那条界到底划到哪，第一次被测出来**（新增 `test/api_interface_dream_scope_test.go` 两条 + 门面与两份指南的 Dream 节各补一段）：库里 `SceneSet` 早就写了「点名一个不存在的场景要让这一趟失败，不许报一次干净的空跑」，`RunDream` 的注释也写了「两条清扫每次 Dream 都跑」，但宿主读到的那一页（`go doc api.Session.Dream` 与指南 §6.4）只说了「传场景 id 就只巩固那一个场景」——按这句话办事的宿主会得到两个意外：① 模型从上下文里回声一个已经被删掉的场景 id，是一次 `ErrNotFound`（而不是空报告）；② 一次限定场景的巩固，报告里照样带着**没被点名那些场景**的 L4/L5 裁剪计数。第一条按既有语义测住（删场景后 `Dream(id)` 报 `ErrNotFound`，同一域不点名的 `Dream("")` 照常成功），第二条用一个反直觉形状测住：两个场景各两轮、`DreamCompressMinTopics=2`、只在**没被点名**的那个场景里留一条一天前的记录（写边界允许，无需睡眠），一趟 `Dream(scoped)` 之后——被点名的场景长出唯一一个融合组（`ConsolidatedScenes=1`、`L2TopicsCompressed=2`、面上只有一行带孩子），没被点名的场景一个组都没有，而它那条过期记录已被扫掉，`l4_prune` 阶段在报告里是 `ok`。顺带纠正一处我自己差点写错的判据：融合与否不能拿 `SceneContext` 的行数比（场景读把融合组连同它吞掉的轮次一起摊平列出，两边都是三行），要看的是 `Depth==1 && ChildCount>0` 那一行。**两条负例各红在自己那条断言上**：让 `SceneSet` 把读不到的场景当成空集合（于是报回一份 code 0 的空报告）、以及让清扫遵守场景范围（于是那条过期记录活了下来）。磁盘格式与公开面一字未动。

62. **宿主可见形状的键命名补齐并上了门禁：入参不再是 PascalCase**（`core.TurnEnd`/`core.ScenePatch` 补 json tag + 新门禁 `api/surface_keys_test.go`）：每个响应 DTO 与每个查询入参本来就带 snake_case 键（`scene_id`、`content_type`、`node_seq`、`graph_id`…），唯独宿主每轮都要交上来的两个形状没打 tag。代价不是难看而是**多出一套命名**：宿主生成工具 schema、把一次调用回声进事件轨、或比较两次读的 JSON 时，`created_at` 旁边站着 `CreatedAt`——正是清单里最没必要的那种跨仓转换。Go 字段名一字未动（宿主代码不会断），两个类型都不落盘也不经任何编码路径，磁盘格式 `0x0012` 不变。新门禁从 `Session`/`DB` 每个导出方法出发，走穿所有入参与返回里可达的每个导出字段，要求三件事：有 json tag、键为 snake_case、`json:"-"` 的字段必须在允许表里且理由以标记出现在两份指南里。最后那条一跑就抓到 `MBTI.Type`：它是每次读现推、从不落盘的派生值（轴才是盘上的事实），于是「Go 读得到、JSON 编不出」从沉默变成写明的契约，并告诉宿主把画像放进 prompt 走 `ProfileBrief`（那句 `mbti: ESFP` 已渲染好）。**四条负例**各红在对应分支：摘掉 `TurnEnd` 的 tag（四个字段点名）、往 `ArchiveInput` 塞一个无 tag 字段、塞一个没有理由的 `json:"-"`、把指南里的标记删掉。以前只核「列表不为 nil、编码不出 null」，键名从没核过——这类缺口就此补上。

63. **导入词表从 `Skip/Merge/Overwrite` 改为 `skip/merge/overwrite`，并把整套词表的走法钉住**（`internal/repo/core/model_dto.go` + 新门禁 `api/surface_enums_test.go` + 两份指南新增 §7.6 对照表）：宿主可见的字符串值本只有一种写法（计划状态 `in_progress`/`done`/`failed`），唯独 `L3ImportMode` 的三个值是 PascalCase——而它正是「注入成工具供 llm 调用」那条路上模型最可能要填的值：模型按整套词表的直觉写 `overwrite`，`Valid()` 拒（`ErrInvalidQuery`），宿主就得为这一个参数单独写一层映射。值改成小写之后 `L3ImportMode(模型给的词)` 直接可用；Go 常量名一字未动（`L3ImportMerge` 还是 `L3ImportMerge`），且这个类型从不落盘、不经任何编码路径，所以磁盘格式与既有数据都不变。同时把拒绝文本改为由常量拼出来（原来硬写着「mode must be Skip, Merge or Overwrite」，照这句话填会被继续拒——错误文本自此不可能跑在词表前面），并把收到的值也写进拒绝里。新门禁核三件事：字符串型枚举的每个值都是小写下划线；三套数字型枚举的 `String()`、编码数字、以及「多出一档即 `Valid()` 为假」逐项对上表；表里每个词必须以**完整单词**出现在两份指南里（`\b` 匹配——`L3ImportMerge` 里的 merge 不算，这条是我自己差点写成的假绿，先按 `strings.Contains` 时它白捡一次通过）。**四条负例**：把值改回 PascalCase、把 `ContentOther` 从 255 挪到 7、把 `KindUtterance` 的词改成 dialogue、把指南表里 `dependency` 一词换掉——各自红在对应断言上。另记一条诚实话：这条门禁的函数一开始漏了 `Test` 前缀，跑成了 `no tests to run` 的假绿，改名之后才真的核。

64. **第 4 条「注入成工具供 llm 调用」有了可抄的一张表，而且双向核**（两份指南新增 §7.7 + 门禁 `api/surface_tool_map_test.go`）：宿主把这套接口绑给模型时要手写 18 份 schema——工具名、该向模型索要哪些键、答案拿来干什么；此前这份对照只存在于宿主自己脑子里，而键名正是刚补齐的那套 JSON tag（上一轮的 `TurnEnd`/`ScenePatch` 补齐之后才谈得上「一张表抄得完」）。表按任务面一个方法一行列出建议工具名（`memory_search`/`memory_record`/`memory_close_turn`/`plan_add_step`…）与逐键，并把八个管理面方法**故意留在表外**写明理由（能删记录的口子要走宿主批准路径）。门禁双向：正向按表去代码里核每个方法存在且入参宽度与表一致、键直接从结构体 tag 现读（新增字段没写进表即红，实测加一个 `scratch` 立刻在两份指南上各报一次）；反向按 `Session` 的导出方法集核「非管理面的每一个方法都得有一行」（加一个 `ScratchFace` 即报 `§7.7 owes it a tool row`）；再加上表行数须等于任务面方法数、管理面方法不得出现在表里、以及键必须作为**完整 token** 出现在行里（`\b` 式匹配，标识符里的子串不算——上一轮踩过的那次假绿这次一开始就按词边界写）。**四条负例**：结构体多一个未列的键、指南行删掉一个键、表里少一行（新方法）、管理面方法混入。**同步**：AGENTS 的测试现状补这条门禁。生产代码一字未动，公开面与磁盘格式不变。

65. **工具面表补上「哪两个键归宿主填」，并写进门面**（两份指南 §7.7 一段 + `api/surface_tool_map_test.go` 一条断言 + `AppendArchive` 的 go doc 一句）：表里 `memory_record` 与 `memory_close_turn` 都带 `created_at`，`memory_record` 还带 `seq`——这两样一旦进 schema 让模型填，回收到的常是秒级数或空值，而库在写边界上拒这两种尺度（保留窗按毫秒算），结果是模型每填错一次就吃一次拒绝、宿主还得在旁边补一层兜底。现在明写：调用由模型发起也是**宿主盖 clock**，`seq` 留 0 表示「取下一个空槽」，其余键都可以交给模型；`AppendArchive` 的门面注释同步补这一句（那里本来已写明 CreatedAt 由宿主供、库不代盖，只是没说绑成工具时怎么办）。门禁要求 §7.7 那一段与两个键名一起留在表旁（负例：把中文指南的标记删掉即报 `the tool table no longer says which keys the host fills itself`，恢复即绿）。行为一字未动，只补文档与一条断言。

66. **「ProfileBrief 有界」从形容词改成算得出的数**（`internal/cap/profile` 加常量 `briefWorstCaseRunes` + 新测试 `TestBriefIsBoundedAndRepeatable`，两份指南的 `ProfileBrief` 行改写）：指南一直说这摘要「有界/紧凑」，却没写多大、也没写偏好几十条时到底取哪几条——宿主拿这句规划 prompt 预算其实什么也没拿到。现在量化：`name`/`role`/`personality` 各截到 160 字（截过以 `…` 结尾）、类型词单占一行、偏好取**排序后最低的 5 条**（键截 160、值截 120），最坏整段实测 2010 字，常量上界写 2100 并由测试守住；且两次调用必须逐字相同——同一份画像每轮注入不能变。语料形状这轮返工过一次：第一版 40 个键共享同一长前缀，截断后五者一模一样，「选哪五条」因此不可见，不排序的负例当场逃过；改成可区分的键（一个以 A 开头的超长键排最前，其余 k00…k39）后同一负例立刻红在 `the same profile digested twice differently`，再补一条把字段上限从 160 抬到 400 的负例，四处报「未标记截断」并撞上 2100 上界。`internal/cap/agent.md` 同批改口。行为一字未动（库本来就排序取前五），这次是把它到底多大、到底选哪五条写清并钉住。

67. **同一个场景的两次读从此不许各说一套**（新增 `test/api_interface_scene_parity_test.go`，两份指南 §6.5 那条判定各补一句）：宿主跑一轮会连着读同一个场景两次——`Search` 开轮时交回表层话题，`SceneContext("")` 在召回时交回摊平到 depth ≤ 2 的转录。两条都从 `ac.L2Meta` 那份缓存出（`scene.SurfaceTopics` 与 `repo.ListTopicsL2` 同一个源），但这件事此前只是碰巧成立：没有任何断言说 `Search.Topics` 必须等于转录里那批 depth-1 行，一旦两条读各排各的序、或其中一条忘了只取表浅行，宿主就会在一轮里看到两份互相矛盾的场景。新用例把「同一批行、同一顺序、逐字段相同」钉住，并且**在巩固之后**测（那时两层真的不一样：4 轮收口 → 一趟巩固折掉两轮 → 表层 3 行、转录 5 行），断言包含行序非降（按宿主自己盖的 `UserTimestamp`）与关键词轨逐行相等，另有一条守卫确认转录确实比表层多出行——少了它整个用例就退化成拿同一条读跟自己比。**两条负例各红在对应断言上**：把 `SurfaceTopics` 的排序反过来 → 报 `row 0 is … from Search and … from SceneContext, so the two reads disagree on order`；把它的 depth==1 过滤放宽 → 报 `Search listed 5 topics while the transcript's depth-1 rows are 3`。行为未动，公开面与磁盘格式一字未改；离线接口面 33 → 34 条。

68. **一趟巩固的阶段名单与顺序变成一套封闭集，写进宿主那一页并双向钉住**（`test/api_interface_dream_stages_test.go` + 两份指南 §6.4 各补一段）：`DreamReport.Stages` 是宿主唯一能看出「哪一步做了什么、哪一步没跑到」的东西，此前两条指南只写了 `Stages []DreamStage{Name, Status, DurationMs}` 与状态四取值，九个名字一次都没列出来——宿主想按阶段名记日志或判断卡在哪一步，只能去读源码；而代码里改个名字也不会有任何检查变红。现在把一趟完整流水线实际发出的序列钉成九项、按序：`l4_prune → l5_prune → l2_compress → index_rebuild → l1_nodes → l1_hyperedges → l1_rebuild → l1_decay → l0_distill`（实测输出，非推测），并写明两条裁剪排最前是因为「没什么可巩固」的域照样要甩掉过期内容与计划节点；`index_rebuild` 在任何 L1 阶段读它之前把重建好的 L2Meta 装上；**没跑到的阶段是从列表里缺席而不是标 `skipped`**——`skipped` 是跑了但自己决定什么都不做，跟中途取消是两码事（这条区分此前只在实现里）。门禁两头：代码侧断言实际序列逐项相等、状态取值都在公布的四个词里；文档侧断言九个名字以**完整单词**出现在两份指南里并带 `dream-stage-order` 标记（词边界匹配，沿用上一轮那课的教训）。**三条负例**：把 `l0_distill` 改名成 `l0_profile` → 报 `stage 8 is "l0_profile", want "l0_distill"`；把中文指南的阶段链整行删掉 → 报 `does not name the stage "l4_prune"`（以及后续每个名字）；只改解说句里出现过的名字不会被误报——那正是第二版语料该抓的形态。行为未动，公开面与磁盘格式一字未改；离线接口面 34 → 35 条。
69. **L1 从「只在工具表里露了一面」补成一节，并给字段表配一道按形状比对的门禁**（两份指南 §8 新增 `### L1 场景关联` / `### L1 scene associations` + `api/surface_keys_test.go` 新增 `TestGuideFieldTablesEnumerateTheShapeExactly`）：验收清单第 2 条要「l1 用户可以查看，dream 接口负责更新」，而指南里 L1 那一节根本不存在——§8 从 L0 直接跳到 L2，`ListL1` 只在工具表、公开面清单和 DTO 名字行里露过一面，`api.Session.ListL1` 自己的注释也只有三行。宿主读完那一页学不到：一个场景节点代表什么、`topic_ids` 是快照还是实时列举、边有没有读口（没有）、`importance` 从哪里起只往哪里落、`updated_at` 其实是衰减钟，以及**这一层根本没有写入口**。现在逐条对着源码补上，并把已有用例名挂到每句承诺后面（`TestListL1SortsByIDHash`、`TestListL1OnAnUndreamedDomainIsEmptyNotNil`、`TestListL1ReportsUnreadableNode`、`TestDeleteSceneLeavesNoOrphansInReadableLayers`、`TestNodeDecayComposesAcrossPasses`）。补写过程自己踩出一个假字段：表里先按画像的 `EmotionState` 写了三条情感轴，而 `SceneNodeView` 只有 `valence`/`arousal`——**画像三条、节点两条**，dominance 不在这条形上，指南现在把这条不对称明写出来。现有门禁抓不到那种错：`TestGuideIdentifiersResolve` 只盯首字母大写的反引号词，`TestEveryHostFacingFieldCarriesASnakeCaseKey` 只判拼写是否 snake_case，而 `dominance` 在别的形状（`EmotionScore`）上确实是个真 key。新门禁因此按**形状**比：带 `l1-node-fields` 标记的那张表，其首列反引号里的 key 必须与标记所指类型 reflect 出的 json key 集合**逐字段相等**——多写一个（宿主去读一个永不出现的字段）与漏写一个（只能去 `go doc` 里找）都红。**负例拿自己刚犯的那个错跑在真指南上**：把 `dominance` 加回表里即报 `../INTEGRATION_GUIDE.md: the table for SceneNodeView omits [] and names keys the shape does not emit: [dominance]`，改回即绿；另有一条构造文本的用例同时钉住「漏写」那一头（`emotion_set`）。行为、公开面、磁盘格式一字未动。

70. **`UpdateL3` 从「只在方法清单里被点名」补成宿主看得懂的一段，写清改名到底动了什么、没动什么**（两份指南 §8 的 L3 节各一段 + `api.Session.UpdateL3` 的 go doc 补一段）：验收清单第 4 条要「l3 用户可以导入，**更新**」，而指南里 `UpdateL3` 只出现两次，两次都在讲 id 类型（`UpdateL3(nodeID, …)` 报 `ErrNotFound`），一次没说它改的是什么、改完那个 id 还归不归这张图。写这段时先按 `repo.EnsureGraphL3` 判过一次「改名会劈出第二张图」——那个函数只按 `hash(Domain)` 查，照它推：改名为 `release` 之后再以 Domain `release` 导入算出的是另一个 id，于是另起一张，而 `CheckName` 只守改名那一头。回源码看导入批次才把它否掉：`graph.NewImportBatch` 一上来就把**所有槽的 `Name` 索引进 `graphIDs`**，两个标签因此都留得住路由——新 label 由记录上的名字命中，创建时那个 label 由派生出的 id 命中，所以两个名字下面再导都是延伸同一张图；这条不是推出来的，`TestUpdateL3RenameSurvivesReimport` 两头都钉过（含「不许起第二张图」）。指南记下四件事：改的是 label；**id 永不动**，改名之后它不再等于 `hash(label)`，场景锚与每条读参数都继续用库交过来的那一个（库从不要求宿主自己算）；两个 label 都还在路由；改一个**节点**走的还是「图 + 标题」再导入（`merge`/`overwrite`），而删除的粒度仍只有整图。门面注释同步补这一段。行为、公开面、磁盘格式一字未动。

71. **保留窗的两个错方向都改为在 `Open` 处拒，而不是悄悄折回 7 天**（`internal/config/defaults.go` 新增 `MaxContentRetentionMs` 与 `MemHopDefaults.Validate`，`internal/config.go` 把它调在 `openEngine` 之前；用例 `TestValidateRefusesWindowsTheSweepCannotRepresent` 与 `TestOpenDBRefusesIncompleteArguments` 里新增的三档）：这是本轮唯一一条会删数据的路径。原来 `ContentRetentionMs` 负数被折回默认，而整套旋钮的词汇写在同一句里说「负数 = 关掉这一项」——宿主按那句写 -1，要的是「永不清扫」，拿到的是「7 天照扫」。更坏的一头是「要更长就把窗口调大」这句被两份指南当成正规解法写着，却没有上界：清扫按毫秒量，`time.Duration(ms) * time.Millisecond` 一旦越界就绕回，绕回来的窗口把 cutoff 甩到**未来**，于是整域每条记录都被判过期。本机实测（`go test ./internal/dream` 的一次性探针，测完删除）：`1 << 45` ms（约 1115 年）→ duration 为负、cutoff 落在 2080 年；`1 << 62` → duration 恰好绕成 0、cutoff 即此刻；`math.MaxInt64` → 绕成 -1ms。可表示的最长窗口是 `MaxContentRetentionMs = 9223372036854` ms（约 292 年，cutoff 落在 1734 年，仍稳稳在过去）。现在两头都以 `ErrConfig` 拒，且判在碰文件系统之前——被拒的入口照旧不在宿主路径上留下文件（`TestOpenDBRefusesIncompleteArguments` 目录为空那条一并守住）。门禁的负例分两处：拿掉 `defaults.Validate()` 那一调用 → 报 `content_retention_ms -1: want ErrConfig, got <nil>`；把边界测试多写一毫秒 → 断言「cutoff 不再落在过去」当场红，所以那个上限将来是被测试追着改的，不是注释里的形容词。三份文档同批改口（两份指南 §4 引言与该行、§12 第 9 条、`AGENTS.md` 的旋钮句、`internal/config/agent.md` 的契约与陷阱），词汇句从「四个旋钮」改成「三个旋钮 + 保留窗是唯一例外」。**这是对外契约的一次收紧**（写 -1 或超大窗的宿主从「拿到默认窗口」变成「`Open` 失败」），随下一个 tag 生效。

72. **上一条那一族剩下的另一处：`LLM.TimeoutSecs` 补上界**（`internal/config/config.go` 新增 `MaxTimeoutSecs` 并接进 `LlmConfig.Validate`；用例 `TestValidateRefusesUnrepresentableTimeouts` 与 `TestOpenDBRefusesIncompleteArguments` 新增一档）：全仓 grep 只剩这一处生产代码会把宿主给的数缩放成 `time.Duration`（`internal/llm/provider.go:58`）。下界一直有人守：`budgets` 把 0 与负数折成默认 120s，注释写着理由——「零超时等于不限超时，挂死的端点会把域锁一直占着」。上界没人守，而绕回去恰好撞上同一句话：`time.Duration(secs) * time.Second` 越过 `MaxInt64` 纳秒即成负数，`net/http` 只在 `Client.Timeout > 0` 时才装 deadline（`$(go env GOROOT)/src/net/http/client.go:200`），于是「把超时调到无限大」拿到的正是那条注释要避免的无限等待——LLM 调用在域锁内，挂住的是整个域。可表示的最长是 `MaxTimeoutSecs = 9223372036` s；这条边界由编译器亲自认：把它当常量写成 `time.Duration(MaxTimeoutSecs+1) * time.Second`，vet 直接报 `constant 9223372037000000000 of int64 type time.Duration overflows int64`，所以测试里那一步绕道 int64 变量，让断言跑在真算式上（红的一头：多一毫秒不再是「更久的等待」而是「没有等待上限」）。判在 `LlmConfig.Validate`，`Open` / `SubAgent` / `Agent` 三个入口一次覆盖，且与上一条一样排在碰文件系统之前。32 位构建里 int 最大约 68 年、永远够不到这道界，比较因此统一走 int64——同一份规则，两种宽度。门面 `LlmConfig` 注释、两份指南 §4 的 `TimeoutSecs` 行、`internal/config/agent.md` 与 `AGENTS.md` 的校验句同批改口。这也是对外契约的一次收紧（超大值从「静默等于不限」变成 `Open` 失败），随下一个 tag 生效。

73. **「取多少」这一类入参统一成一条读法：`max_depth` 非正即不设界**（`internal/graph/query.go` 的 `BfsWithinDepth` 把界判在自己身上，`internal/l3query.go` 删掉 `maxDepth <= 0 → 1` 那道上移的钳位；用例 `TestQueryL3SubgraphDepth` 改判）：同一份面上两个「要多少」的入参此前对 0 说着相反的话——`core/model_dto.go:143`/`:168` 写着 `Limit <= 0 means unlimited`（`QueryL3Nodes` 与 `SearchL4` 都走它），而子图那条在 `internal/l3query.go` 里把非正数钳成 1 跳，原注释还写着「maxDepth<=0 means 1」。这条不对称正好落在验收清单第 4 条「注入成工具供 llm 调用」最疼的地方：`max_depth` 与 `limit` 都是模型会亲手填的键（两份指南 §7.7），宿主得给同一个「0」写两句相反的 schema 描述，而模型填 0 想问「这节点都连着什么」时拿到的是「只看一跳」——一个会被读成「这张图几乎什么都没有」的答案。**行为因此改了**：非正的 `max_depth` 现在走到可达分量为止；钳位从调用方挪进 BFS 本身，判据只有一处。环不成问题：一个节点只入队一次，走到分量尽头即停。断言取的是「0 与一档已覆盖全图的深度逐字段相等」（`reflect.DeepEqual`）加「-5 与 0 同样」，两头钉住这是「不设界」而不是「又一个数」。**负例**：把 `BfsWithinDepth` 的循环条件退回原样 → `depth 0: want the whole graph (4 nodes / 3 edges), got 1 / 0`，改回即绿。门面 `QueryL3Subgraph` 注释、两份指南 L3 节各新增一段、`AGENTS.md` 读侧那一条补上「limit/max_depth 非正即不设界」。宿主侧无需改任何调用点（实测：本仓所有测试与三仓 e2e 程序传的都是正数深度），但这是一次对外语义变更，随下一个 tag 生效。

74. **关闭之后整份公开面答同一个码，这件事第一次被逐方法钉住**（新门禁 `api/surface_closed_test.go` / `TestEveryCallAnswersErrClosedAfterClose`；`internal/db.go` 新增包级 `errDBClosed`，原先 10 处各写一遍的构造收成一个值；门面 `Close`/`IsClosed` 注释、两份指南 §5 各一段、`internal/agent.md` 第 3 条补两句）：宿主早晚会在 worker 还跑着一轮的时候结束运行，而这条竞态输掉的两种写法都比「拒绝」难查——读到已释放的 mmap 会被报成损坏，nil 解引用把进程一起带走。此前只有零散几处断言过 `ErrClosed`。新门禁先按宿主的正常姿势把库喂满（开一轮、写一条、建一步、导一张图），`Close` 之后逐个调用**反射出来的**每个导出方法：除 `Session.AgentID` 与 `DB.IsClosed` 这两个不读引擎状态的句柄口之外，一律要求 `CodeOf(err) == ErrClosed`，任何 panic 直接判失败。表不靠人维护——`publishedMethodNames()` 现读 `Session`/`DB` 的方法集，少调一个就红；唯一显式豁免的是 `DB.CompactTo`（入参是一条任意文件写路径，测试门禁不该顺手造文件），理由就写在豁免表旁边。**两条负例各红在对应断言上**：删掉 `DB.Agents` 那一行 → `published methods this walk never calls: [DB.Agents] — the table did not grow with the surface`；把 `DB.IsClosed` 从 `readsNoState` 摘掉 → `DB.IsClosed: want ErrClosed (5002), got code=0`。顺带按「重复错误处理 ≥3 次提取 helper」收口：`common.NewError(common.ErrClosed, "database is closed")` 原在 `db.go` 7 处、`agents.go` 3 处各写一遍，现在是一个包级值（与 `repo/core` 既有的 `errEngineClosed` 同一种写法），四处判点保留（它们各在自己的锁序位置上），措辞只有一份。实测：`Close` 之后 34 个调用点全答 `5002`、无 panic；第二次 `Close` 也答 `ErrClosed`——它什么都没关掉，这一点连同 `IsClosed` 能用来分辨「句柄没了」与「这条记录读不回」一起写进了门面注释与两份指南。行为一字未改。

75. **`UpdateL0` 改为按值收 `ProfileInput`，三个画像入口的形态就此对齐**（`api/session.go` 签名与注释、`api/mapping.go` 的 `toCoreProfileSlot` 去掉 nil 分支、`api/open.go` 的 `Open` 注释说明为什么只有它继续收指针；两份指南 §5/§8 与 `AGENTS.md` 的字段所有权句同批；28 处调用点随编译改过）：验收清单第 1 条要「l0 可以给用户查看更新字段」，而宿主在三个入口填的是同一个类型，形状却分两种写法——`Open(path, llm, defaults, *ProfileInput)`、`SubAgent(llm, ProfileInput)`、`UpdateL0(*ProfileInput)`。指针只在真正需要「没有」这个状态的地方才付得起代价：`Open` 的 nil 有含义（不播种，只开已有文件），`SubAgent` 早已按值收，而 `UpdateL0(nil)` 从来只是被门面拒成一句 `ErrInvalidQuery: profile is required`——一个**结构上就表达不出来的输入**，却占着一个运行时分支、一处宿主必须记得的 `&`，以及一条只有跑了才知道的拒绝。按值之后那条分支与它的测试一起消失（`api/surface_test.go` 那一档改成同形状下真正还能被拒的那件事：`ProfileInput{}` 的空 `Name`，仍由 `ErrInvalidQuery` 拒）。`toCoreProfileSlot` 里那段 `if s == nil` 本来就是不可达代码——门面先拦下了 nil——一并删掉。公开面的方法名一条没动（`api/surface_public_test.go` 仍钉 `Session` 26 + `DB` 9），磁盘格式与行为未动；对宿主是一次**编译期**跟版（少写一个 `&`），随下一个 tag 生效。指南的两份 §11 骨架与 §8 例子由 `make check-guides` 编译、`TestGuideSkeleton` 实跑过。


76. **`UpdateL3` 不再有「什么都不改、照样盖一次钟」的写法，改成自己已有的标签变成一次真正的空操作**（`api`/`internal` 的 `UpdateL3` 收 `name string`，`graph.CheckName` 改成 `CheckRename` 同时报告「标签会不会动」，根方法在不动时直接走只读路径；`internal/repo.UpdateGraphL3` 的 `nil` 保留但注释改准——它是 `StampChanged` 给真写过的节点/边补的那一次钟，不是公开口；两份指南 §8、`internal/agent.md`、`internal/graph/agent.md`、`AGENTS.md` 的图槽时钟句同批）：v1.6.x 第 15 项已经把图槽的 `updated_at` 裁定成**内容变化钟**（「只被读到的图不动」「skip 模式重导不再让图看起来刚变过」），第 50 项给了四个方向的断言。公开口却留着两个例外：`UpdateL3(id, nil)` 门面注释明写「`nil` 是『什么都不改，仍然盖钟』的拼法」，`UpdateL3(id, 已有的标签)` 也照样追加一条记录并推进那口钟——等于公开面自带一条「让一张没碰过的图看起来刚变过」的路，而宿主拿这个字段就是用来排「最近改过的知识」。现在 `name` 是必填字符串（空串仍拒，理由不变：没标签的图 `ImportL3` 再也指不回来），**改成自己已带的那个标签什么都不写**：不追加记录、不盖钟、返回现值，于是重放一次改名收敛而不是每次刷新。`CheckRename` 在同一次严格扫描里既判占用又判「会不会动」，判据仍只有一处。**负例**：把根方法里 `if !moves` 那道短路拆掉（退回「无论如何都写一次」），新断言当场红在 `a no-op rename moved the graph's clock from 1790216095178 to 1790216095191`，恢复即绿；同一用例的另一头钉着「真改标签仍然盖钟」。测试里那条 `nil 是不改的拼法` 一档随之删除，`ptrToString` 成了死代码一并删。对宿主是一次编译期跟版（少一层指针），随下一个 tag 生效。


77. **等值的元数据写不再追加记录：`UpdateScene` 的空 patch 与 `RenameTopic` 的同名重试现在什么都不写**（`internal/l2.go` 落笔前比现值、`internal/repo/l2layer_topic.go` 同；新用例 `internal/l2_noop_write_test.go`；顺带补上两份指南 §8 L2 表里 `RenameTopic` 那一直缺的行）：文件是纯追加的，「写一条与现值等价的记录」不是免费——它只是让文件变长，等到 `CompactTo` 才回收。而这两处恰恰是宿主最常重试的口：`UpdateScene(id, ScenePatch{})` 是指南写着「不列举域就确认一次锚点」的那条读（`SceneSlot` 上没有钟，它每次确认都追加一份一模一样的场景记录），`RenameTopic` 是一次网络重试会重发的那类写。本机实测（同一用例的红态）：40 次等值调用把文件从 9617 推到 16237 字节，**+6620 B（每次约 121–211 B）而可达记录数一动不动**（4 → 4）——也就是说这笔开销在 `Stats` 的记录数上完全隐形，只有按字节量才看得见。分开量：只拆场景那一道闸 → +2420 B；只拆话题那一道 → +4220 B；两道都在 → 0。**断言两头都带牙**：另一头钉着「真改了就必须写」（两次真改名让字节上涨、`ListScenes` 与 `SceneContext` 读回新值），所以「干脆永不写」这种变异当场红。同一判据也补进上一轮那条改名规则（`UpdateL3` 改到当前标签已经不写），`AGENTS.md` 的 at-least-once 那一句现在把三处一起说清。门面两处注释、`internal/agent.md` 第 6 条、`internal/repo/agent.md` 陷阱同步；`RenameTopic` 此前在指南里只出现三次（工具面排除句、管理面清单、一条规则里的顺带提及），宿主读不出它到底改什么、什么时候可见、空名会怎样，这一行补上。公开面方法数不变，磁盘格式不变；这是一次只减不增的行为变化（不再有等值追加），随下一个 tag 生效。


78. **验收清单第 6/11 条的「把 plan 更新到 prompt」第一次在可跑骨架里跑通**（两份指南 §11 各补一段 `PlanState` 读出→折叠成 prompt 文本→打出来；`test/guide_skeleton_test.go` 从「退出码为零」升级成断言它真的印出该印的东西）：`PlanState` 在指南里一直被解释（§5 那句「plan 式上下文读回来的是开着的那一轮」、§7.7 的 `plan_state` 行、§8 那段渲染与预算），可宿主照抄的那份 §11 骨架走到 `PlanNodeUpdate` 就直接收轮了——第 11 条流程里「将 plan 更新到 prompt 继续循环」这一步在最主要的可跑样例里是缺的，第二个宿主得自己发明一遍渲染。现在骨架里有了：`tree.DoneCount/TotalCount` 一行汇总 + 沿 `Children` 缩进把每一步的 `Seq`/`Title`/`Status` 排出来，接在 `res.ProfileBrief` 后面组成这一次调用要送出去的整块，并且把它打印出来。门禁跟着升级：`TestGuideSkeleton` 此前只判「没崩、退出码 0」，现在要求输出里出现 `name:`、`plan: 1/2 steps done`、`- #1 `、`[done]` 四样——正是这份骨架自己承诺交给宿主的东西，其中 `1/2` 就是「汇总数按全树算而不是只数根」那条契约的实跑值。**负例**：把那行打印换回 `_ = promptContext` → 一次跑出两条红（`the skeleton printed no "name:"` 与 `…no "plan: 1/2 steps done"`），恢复即绿（渲染段整体被删时 profile 那行也一起没了，正是该抓的形态）。英文与中文两份骨架同步改，`make check-guides` 编译、`TestGuideSkeleton` 实跑双语版都过。行为与公开面一字未动。



## v1.6.5 — 2026-09-22 — MCP 面整体退役：对外只剩 Go module

1. **`cmd/memhop-mcp` 整包删除**（14 个文件 3109 行）：25 个工具、多租户 HTTP（SSE 与 streamable-http 双传输）、按 `/mcp/<tenant>` 建/取域的租户注册表、`--tenants` 白名单与锚定 db-dir 的读入口一起消失。仓库不再有 server 形态、不再有后台进程，对外只剩「以 Go module 使用 `api`」一种接入。
2. **直接依赖 4 → 3**：它是全仓唯一读 `modelcontextprotocol/go-sdk` 的地方，`go mod tidy` 连带清掉它带入的 7 个间接依赖（`google/jsonschema-go`、`segmentio/asm`、`segmentio/encoding`、`yosida95/uritemplate/v3`、`x/oauth2`、`x/sync`、`x/time`）。留下 xxhash、go-openai、golang.org/x/sys。
3. **公开 Go 面一字不动**：`Session` 25 + `DB` 7 的名单、锁范围、幂等语义与 ID 契约全部原样；`api.CodeOf` 保留为宿主把错误读成数值码的门面唯一口（退役后它在本仓零调用，留任理由见 `notes/implemented/architecture/2026-09-22-go-module-only-surface.md`）。
4. **构建与门禁收口**：`make build-mcp` / `test-mcp` 两个 target 删除，pre-commit 与 workflow 的 vet / gofmt 包清单去掉 `cmd`——`cmd/` 已不存在，留着会让门禁直接报错退出（已实测：`go vet ./cmd/...` 退 1）。
5. **文档按实话重写**：`AGENTS.md` 的形态、对外面、依赖、宿主接入四条改写，「不存在的能力」补上「不带 server 形态」；README 的 MCP 特性条目、分层图与双语集成指南里「仅 Go 侧」的措辞改成对调用方的约束（`CompactTo` 的入参是一条任意写路径，目的地由宿主限定）。顺带纠正一处归因错误：`SceneContextTopic.messages` 的空值例外原本记在 MCP 工具头上，实为 `omitempty` 的性质——Go 侧读回恒为非 nil 列表。磁盘格式 `0x0012` 不变，旧文件照常打开。

## v1.6.4 — 2026-09-11 — 公开面重做：入口换成 Open，域以句柄交回；随后的分包审查把功能归位、冗余清除

1. **入口换成 `Open(path, llm, defaults, profile)` → `*api.DB`**，成败由「文件在不在 + 主域画像在不在」决定：文件与画像都在则成功（入参不被采纳）；文件在而画像不在，带了才成功、没带报错；文件不在，带了才建库、没带报错**且不留下任何文件**（新建以「手里有东西可播种」为前提；已存在的文件是先被打开、含撕裂尾帧修复，才查出它没有主域画像）。`OpenMulti`、`MultiAgentDB`、`MemHopConfig` 一并删除。
2. **域以句柄交回，agent id 不再越门面**：`Primary()` 拿文件被打开所依据的那个域，`SubAgent(llm, profile)` 按 `profile.Name` 幂等建/取一个子域并挂上它自己的 LLM 端点。`CreateAgent`/`ListAgents`/`Session(hexID)`/`DefaultAgentID` 删除——宿主不再持有也不再回传任何域 id，名字是它唯一的把手。
3. **L0 画像新增 `AgentType`**（0=主 agent / 1=子 agent）：建域时由库盖章，宿主写画像时由库继承现值，所以改画像动不了域身份。
4. **L1 开只读面**：新增 `ListL1() → []SceneNodeView`，返回本域全部场景节点、顺序稳定、id 为 hex。节点与共现边仍只由 Dream 建立与衰减，**没有 L1 写接口**；`EdgeIDs` 没有独立的读取口，两个节点共享同一个 id 即意味着 Dream 判定它们相关。
5. **L2 新增 `RenameTopic(topicID, name)`**：话题名归宿主，引擎不派生，所以三条会重写话题记录的路径都不碰它——巩固建父、压缩下沉子话题、以及重放同一轮（`Update` 第二次结算同一个轮次键时只重写引擎那半，名字从存量记录带过来）；空名被拒（空是「还没命名」而不是一个名字），未知话题报 `ErrNotFound`。新名字在 `Search` 与 `SceneContext` 上立刻可见。
6. **整体退役**：能力面（`Crystallize`、`ParseCapabilityPackage`/`ValidateCapabilityCard`、`internal/cap/capability` 整包）、`ListTrajectorySessions`（读事件轨走 `SearchL4{TopicID, Kind:event}`）、`DeleteL3Nodes`（L3 删除只剩整图一个粒度）、`DeleteAgent` 与它背后的整域删除链。公开面 27 + 8 → **26 + 6**。
7. **磁盘格式 `0x0011` → `0x0012`**：`0x0011` 及更早的文件在 `Open` 时被显式拒绝、不迁移。理由是硬的——旧文件的画像没有 `agent_type`，解码后每个域都读作主 agent，而新语义要求「一个文件恰好一个主」，容错打开会无声地违反这条不变量。
8. **MCP 仍是 24 个工具**：删 `memhop_crystallize` 与 `memhop_trajectory_sessions`，新增 `memhop_l1_nodes` 与 `memhop_topic_rename`。多租户改为：进程启动时 `Open` 落定主域，每个 `/mcp/<tenant>` 首次访问经 `SubAgent(name=tenant)` 建/取自己的子域——租户名就是域的地址，重连回到同一个域。共享库在启动时打开，配置不可用则进程拒绝启动，而不是等第一个请求报 500。工具面与实现重新对齐：`memhop_dream` 在阶段失败时把部分报告连同错误一起带回（此前统一的 `handle` 一律丢弃返回值，而门面承诺的恰恰是那份「已经做到哪一步」），`memhop_profile_update` 把 `name` 声明为必填并写明四项是整写而非打补丁（一个全 `omitempty` 的形状读起来像补丁，漏填即清空那一项），`memhop_scene_list` 不再承诺话题条数，`memhop_scene_topics` 说的是 depth ≤ 2 的平铺清单（融合父话题连同它吞掉的轮次都在内），`memhop_search` 不再声称话题带 L4 原文 id。
9. **`DefaultMemHopDefaults` 从指针改为值**：宿主要调参就复制一份改，不再能经由一个导出的全局改到所有调用方读到的默认值。
10. **读错误分类回到读侧（两处）**：`repo.GetProfileL0` 不再把一切改写成 `ErrNotFound`——读不动报 `ErrIO`、解不开报 `ErrDeserialization`。三处上游的分支（`GetL0` 何时给空画像、`UpdateL0` 何时不继承蒸馏半区、Dream 蒸馏遇瞬时失败是否重写画像）此前**永不触发**，其中一处的注释还正以那条分支为立论依据。被修掉的后果是一次瞬时读失败就能让 `UpdateL0` 抹掉该域的 `EmotionState`/`MBTI`/域身份；契约测试 `TestUnreadableProfileIsNotAbsentProfile` 钉住「解不开的画像不落盘」。同一分类补到压缩下沉：`repo.CompressTopicsL2` 此前把「点名却读不动」的组成员当成已经消失而跳过，于是父摘要与该条自己的原文会同时留在场景里——现在改写攒到最后一次批写，成员读不动就整组不动、把错误交回巩固侧回滚（`TestCompressTopicsL2RefusesUnreadableMember`）。
11. **Dream 的缓存重建不再被 L1 失败带走**：L2Meta 整表重建一算出来就装回域上下文，排在 L1 各阶段之前——L1 只写 L1 记录，一次 L1 失败不会让按当前 L2 记录算出的缓存失效。反向的漏项补进文档：本包的压缩改写话题深度时**没有任何增量镜像步**，那次重建就是唯一的对账点（`TestStructureStagesKeepsRebuildAcrossL1Failure`）。
12. **轮次键的取锁与解析各只剩一处**：`db.lockSession`（取域锁 → 解析轮键 → 解析失败先解锁）此前全仓零调用而 L4/L5 各口手写同一段前言，现由 `AppendArchive`/`PlanNodeAdd`/`PlanNodeUpdate`/`PlanState`/`Update` 共用；「解析话题键并拒保留全零」在 `turn` 与 `content` 各写一份的两份合一到 `content.ParseTopicID`，并补上 `RenameTopic`/`DeleteTopic` 两处只解析不拒零的入口。
13. **同一判断与同一形状合一**：「索引点名却读不到 = `ErrIO`」的三份等价实现收成 `repo.ReadArchivesByIDs` 一处，`scene.ContextTopic` 退成纯渲染（不再自己读记录，小包也就不再跨层调 `core`）；`repo.ArchiveContent` 这个与 `core.ArchiveSlot` 同形的中间结构删除（每条内容此前被复制两趟）；`CompressTopicsL2` 不再返回无人读取的时间界（同一件事 Dream 自己算过，且算出的值才拿去铸父 id）；L1 情感回填从「L0 profile primitives」模块迁回 L1 文件，并停止把读失败说成「节点不存在」。
14. **死代码与无读者字段净删**：引擎内手工加减的记录计数器（header 里的计数由索引现算，Open 时那次读入没有消费者）、`ListTopicsL2` 里按 id 读单个话题的那个模式取值、`MemHopConfig.Validate`、`L2MetaIndex.Len`、L3 的 `Node.Importance`/`Edge.Weight`/`Edge.Label`（无写入路径或只写常量、不进公开 DTO）、`llmops.L1Sample` 与 `core.DistillSample` 两份同形形状合一：留下 `core.DistillSample` 那一份具体形状，`llmops.L1Sample` 改为指向它的恒等别名（与 `EmotionScore`/`MBTIScore` 同一先例）。**磁盘格式与 `FormatVersion` 一律不动**：旧文件里多出的 JSON 键本来就被解码跳过。
15. **公开面收敛（本轮的 breaking 部分）**：
    - **话题不再存子话题清单**：`TopicSlot.ChildrenIDs` 连宿主可见的 `children_ids` 键一起删除。引擎内没有任何一处遍历它——子树闭包与 `child_count` 都由子话题自己的 `parent_id` 现算——它只贡献了删除时的一次修剪步、缓存里多镜像的一个字段，以及「与 `parent_id` 说法不一致」的可能。随它删除 `scene.PruneParentChild` 与失去唯一调用者的 `common.RemoveOnce`。
    - **画像写入换 `api.ProfileInput`**：`Open`/`SubAgent`/`UpdateL0` 的入参只含宿主四项（`Name`/`Role`/`Personality`/`Preferences`）。库自有的 `EmotionState`/`MBTI`/`AgentType`/`UpdatedAtMs` 从「传了不采信」变成没有位置可传；读回形状仍是全量。`Name` 是这三个入口共同的必填项（域就靠它被称呼），`UpdateL0` 收到空白名报 `ErrInvalidQuery` 而不是存下一个无从指认的画像（`TestUpdateL0RequiresName`）。
    - **两个纯 `len` 键删除**：`PlanNodeView.child_count`（同一视图里 `Children` 全量返回）与 `SceneContext.topic_count`（就是本次返回的条目数）。`SceneContextTopic.child_count` **保留**——它由 `parent_id` 数出来，宿主自行要重扫整份平铺列表。
    - **图槽的 `updated_at` 改为内容变化钟**：一次导入对真写过内容的每张图各推进一次（一图一次写，不是每条记录一次），只被读到而没被写过的图不动，skip 模式重导不再让图看起来刚变过。
16. **文档与实现对齐**：`PlanTree.DoneCount/TotalCount` 的说明此前两处都写「数根」，实现 `CountForest` 是沿每棵树递归汇总（口径以 `TestPlanStateForestMultipleRoots` 为准）；L5 族那份「统一前言」文档写的是全仓零调用的函数、"内容只有一个写入口"被巩固摘要的写入打破——两处都按现状改写。宿主的两份集成指南另算一处：它们挂着「部分更新」的告示继续教已不存在的入口（`OpenMulti`/`CreateAgent`/`Session(hexID)`/`MultiAgentDB`/`Lock()`/`Crystallize`/`ListTrajectorySessions`/`ParseCapabilityPackage`），§1 的硬性契约表与 §9 的导出类型清单整段是旧的，一处还把格式版本写成 `0x0011`——两份都按当前面重写，告示随之改为陈述现状；「压缩后每场景 ≤20 个话题」这条规模保证在代码里没有对应常量，改成按触发式收敛的说法；`content.Append` 那句「唯一写入口」的系统级断言收回到本包边界（两个入口的事实只在 `internal/agent.md` 讲一次）；`llm` 包两处注释引用了不存在的 `Config` 类型且字段数少一项，测试与 `internal/agent.md` 里的三处失效指针（一个已改名的测试、`CreateAgent`、一段「原先由 X 读 Y」的过程叙述）一并清掉；重写时的示例本身也有一处断路：§5 把 `db` 绑成 `api.Open` 返回的 `*api.DB`，而后文每个示例都在 `db` 上调 Session 方法（`Search`/`AppendArchive`/`Update`/`Dream`/`SearchL4`），宿主照抄编译不过——现在两份都按 §11 已有示例的写法统一为 `lib` = `*api.DB`、`db` = `lib.Primary()` 给的 `*api.Session`，两份指南的可编译完整示例各自 `go vet` 通过。
17. **对消费方 breaking**：入口、句柄类型、方法集与磁盘格式版本同时变，加上第 15 项的三个 JSON 键消失、`ProfileInput` 换型与 `UpdateL0` 开始拒空白名（此前会存下无名画像），宿主 meowagent 需在其自身的跟版轮次里适配。
18. **审查轮补的读面五处**：L3 的三份列举此前直接交出哈希表扫描的顺序，同一个调用两次可以给出两个顺序，`QueryL3Nodes` 的 `Limit` 因此落在任意子集上——现在节点、边、图槽都按 id 升序，`Limit` 是这条确定顺序的前 N 个（`TestL3ReadsAreOrderStable`）；`QueryL3Subgraph` 此前把「BFS 走到却读不动」的节点静默跳过、交回一张小一号的图，而那个节点是某条边点名的成员，现在如实上报读失败（`TestQueryL3SubgraphReportsUnreadableNode`）；`SearchL4` 的话题过滤只做 hex 解析、不拒保留的全零键，一个没拿到轮次键的查询会收到空清单、与「这一轮真没内容」分不清，现在与写侧同口径拒绝（`TestSearchL4RefusesReservedZeroTopic`）；另有两处宿主读面把「读不回的记录」答成「少一条」——`ListL1` 的场景节点列举与 `SearchL4` 不带话题过滤的那条全扫路径（它底下那层 `repo.QueryArchivesL4` 的文档本就写着「记录读不回是错误」，只有这条分支没照做），宿主拿到一份短了的清单时分不清「Dream 没建过它」与「这条坏了」，现在两处都走严格扫描（`TestListL1ReportsUnreadableNode`、`TestQueryArchivesL4ScanReportsUnreadableRecord`）。
19. **审查轮补的记忆质量**（这一轮问的是「一轮记忆写进去之后还回不回得来」，不是接口能不能调）：
    - **一个场景落地的融合组成员互斥**：模型按对话线程分组，相邻两条线程可以都点名同一轮，而两组都应用等于把那一轮沉两次——第二次改父指向，第一个组的摘要于是管着一个不再应答它的子，那一轮也落到最深的读路径之下。现在组按提出顺序应用，点名已落地成员的组计入「提出但未应用」（`TestApplyGroupsRejectsOverlappingGroups`）。同一保证的下半句是**父 id 未被占用**：父话题 id 由组的时间界派生，两个成员互斥的组照样可能算出同一个（宿主打时间戳粗到几轮共一时很容易），落第二个就是让一份摘要描述一组、另一组的原文藏在它下面——父 id 已被一个话题占用、或已被一个不是话题的记录占用，本组都拒，且拒在写任何记录之前（`TestApplyGroupsRefusesCollidingParentID`）。
    - **重放一轮不再把已沉入组的轮次拉回 surface**：`depth` 与 `parent_id` 连同宿主的 `name` 一起从存量记录带过来，否则第二次结算会让那一轮的原文与取代它的那份组摘要并排出现、组还少一个子；存量记录读不回时这次沉淀**拒绝**而不是猜一个位置（`TestCreateTurnTopicL2RefusesUndecodableRecord`）；`ReadTopicLenient` 此前把解不开的 payload 原样交出 `json` 错误，任何按错误码分道的调用方看到的都是 0 码，现在与 `readJSON` 同口径报 `ErrDeserialization`。
    - **衰减级联不再把读不动的边说成「已剪好」**：`removeNodeFromEdge` 此前把任何读失败都返回成「没删、也没错」，于是一个节点继续指向一张没人剪过的边；现在与它的对向函数同一口径（只有 `ErrNotFound` 算已消失，其余上报），两处手写的「从切片里摘掉一个 id 并记住有没有摘到」换成 `slices.DeleteFunc`（`TestRemoveNodeFromEdgeReportsUnreadableEdge`）。同一族的更深一处是 `RebuildFromL2`：一个 depth-3 节点的保留规则要读它的话题，读失败被答成「不保留」，于是**删掉这条 L1 节点记录**——旧文件里真可能存在 depth-3 话题（正是上面那个「沉两次」缺陷留下的残迹），所以那条路径不是理论上的；现在读不动就中止这一轮并原样上报（`TestRebuildFromL2StopsOnUnreadableDeepTopic`）。
    - **同族再补：一个枚举要拿去决定删谁、覆写谁，就得整个读得回来，且排在任何墓碑之前**。`repo.SyncL1NodesFromL2` 读不回一棵 L1 节点时当作「还没有」，就地新建一条覆盖上去——`Importance`/`Valence`/`Arousal`/`CreatedAt` 一次归零、`EdgeIDs` 清空（共现边那侧还指着它），而同文件注释正承诺「existing nodes keep …, this pass never decays them」；它统计话题的那份扫描同病，一条读不回的子话题从节点的清单里静默消失（`TestSyncL1NodesFromL2KeepsANodeItCannotRead`、`TestSyncL1NodesFromL2StopsOnUnreadableTopic`）。删除侧三处同一形状：`TopicClosureL2` 少一个子就少删一个子（父被删了、子活着，此后再没有一条路径会去删它）；场景批删跳过读不回的话题却**返回成功**；`MergeScenesL2` 更糟——先把能改的改到新场景、再删掉旧场景记录，那条孤儿话题从此没有场景。三者现在都走新的 `core.CollectAllStrict`，`MergeScenesL2` 的返回值从 bool 改成 error（`err == nil` 那一行原本把「删了没有」压成一个布尔）。计划树的保留窗清扫同族：`CollectPlanNodes` 改为严格扫描并带出错误，`PrunePlanStage` 在枚举不全时整轮不扫——在途豁免按整棵树算，读不回的那一个偏偏可能就是唯一没做完的节点；域内缓存仍按幸存记录分组（分组逻辑抽成 `GroupPlanNodes` 一份，两处各自选自己的容错），因为镜像的缺席与被裁掉同形，而删除的缺席不是（`TestPlanNodeScansReportAnUnreadableNode`、`TestPrunePlanStageSkipsWhenTheTreeIsIncomplete`）。
    - **枚举与批删分家，顺序才立得住**：`DeleteL2` 连它的 `DeleteScenesL2`/`DeleteTopicsL2` 模式常量一并删除，本层的批删换成 `DeleteL2Records` 与 `DeletePlanNodesByIDs`——只照给定 id 落墓碑、不读任何东西，「读了才知道删谁」全部留在带 error 的枚举口里（`PlanNodeIDsByTopicIDs` 是 `DeletePlanNodesByTopicIDs` 拆出的只读那一半）。于是一次 `DeleteScene` 从「话题桶扫三遍、节点桶扫一遍」降到各一遍，且两遍枚举都排在第一张墓碑之前，被拒一次盘上原封不动（`TestTopicClosureL2RefusesUnreadableChild`、`TestTopicIDsBySceneL2RefusesUnreadableTopic`、`TestMergeScenesL2RefusesUnreadableTopic`、`TestDeleteSceneRefusesWhileThePlanBucketIsUnreadable`）。巩固的组回滚同向修正：`discardFusedGroup` 此前也要先扫一遍全域话题才肯删掉自己刚写的父记录，于是**正是那条读不回的记录**把回滚挡住，场景留下一个有父话题、没摘要内容的空壳；现在撤销按那个 id 定点做（`TestApplyGroupsRollsBackTheGroupWhenASinkRefuses`）。
    - **同一族剩下的四处读取**。`engram` 建共现边时把「这条边读不回」当作「还没有这条边」，就地新建一张：`CreatedAt` 从零起算（衰减读的就是它），且绕过「权重不比现值大就不写」那道守卫——一次读失败把一条已衰减的边复原成满权重（`TestBuildHyperedgesReportsUnreadableEdge`）。`graph.ResolveSubgraphStart` 把起点记录的**任何**读失败都改写成 `ErrNotFound`，正是本仓错误模型明令禁止的那一步（`TestQueryL3SubgraphReportsUnreadableStartNode`）。`DeleteGraphL3` 是这一族最后一个 bool 站点，且两桶都容错扫——跳过的节点活下来、继续指着那张已被删掉的图（`TestDeleteL3RefusesUnreadableNode`）；它的调用点同时把阶段顺序改回「先删图、再摘锚」，这是承重的顺序：锚点写入按「图还在不在」校验，反过来会让一个校验通过的锚点落在摘除之后、活过它自己的图（`TestDeleteL3RaceWithSceneAnchor` 抓到过一次）。租户注册表原先丢掉一切解不出名字的记录，于是 `SubAgent` 会给一个**已被占用**的名字新建第二个域——真域从此既列不出也删不掉；现在 `ensureRegistered` 在建新域前现场重扫一次注册表，只要有一条键解不出就拒「建」（拒因带出那个域的 id），`Open` 与所有已解析出的名字不受影响（`TestSubAgentRefusedWhileATenantKeyWillNotResolve`）。
    - **两个话题写入口不再把因压成布尔**：`CreateTurnTopicL2`/`CreateFusedTopicL2` 此前返回 bool，调用点只能自己编一句 `NewError(ErrIO, "...", nil)`——「存量记录读不回」与「写失败」在宿主耳朵里是同一句话。现在错误原样带出，`Update` 的失败保留它自己的错误码（`TestCreateTurnTopicL2RefusesUndecodableRecord` 断的就是这一码）。**融合组必须整组看得见**：`groupTimestamps` 此前跳过列举里认不出的成员，于是两个名字里只认得一个也能铸出父话题——摘要说两件事、底下只沉一条（`TestApplyGroupsRefusesAGroupItCannotSeeWhole`）。
20. **读侧剩下的定序与拒绝改诚实**：跨话题的 `SearchL4` 此前按 `Seq` 排序，而 `Seq` 是**轮内**槽位号，于是 `Limit` 留下的是「槽位最多的那一轮」而不是最新内容，`Seq` 相同的记录又按扫描顺序出现，同一查询两次可给出不同子集（现在单话题仍按槽位，跨话题按记录自己的时间、以 id 收尾，`TestDomainWideL4ReadOrdersByTimeAndKeepsNewest`）。`Dream` 在压缩已落盘之后被取消，会整段跳过 L2Meta 重建，于是该域继续列出这一趟已经吞掉的轮次，直到缓存被回收或重开文件（取消检查点移到装回之后，`TestCancelledDreamReconcilesTheReadPath`）。图导入批次用一份「跳过读不回的槽」的扫描预载标签→图，于是导入一张被坏槽占着的标签会答成「这里没有图」并在同一标签下铸出第二张图，该域节点从此分属两个 id（播种与改名检查都改走严格扫描，且严格拒绝会把读不回的那条记录 id 带出来——引擎没有别的口能指出哪条坏了，`TestImportL3RefusesUnreadableGraphSlot`）。话题列举只保留镜像那条路，删掉生产上不可达的扫记录回退分支与它永远不会产生的 error，宽容版 `CollectAllTopics` 随之消失；一批次给多张图盖章失败时，合并出的错误行按图 id 升序而不是 map 顺序。 随它一起退役的还有图槽的来源：`HypergraphSlot.source` 的 kind 恒为 "manual"、另两个字段没有任何写入路径能设置，`SourceKind` 四个值里三个连写点都没有，于是那个形状、那份枚举与 `source` 键一并删除，旧文件里多出的键在解码时被忽略（对读这个键的宿主是 breaking）。 同一批的「每图标题集 / 每图边键」此前是按图懒加载、且来自一份跳过读不回记录的列举，于是对一个读不回的节点，Merge 导入会答「这个标题还没有」，按位置式 id 原地覆写那条读不回的记录、还算进 CreatedIDs 当成新增；现在三张索引都在批次创建时一次建好、且走严格扫描——顺带把「K 张图各扫两遍全池」换成整池两遍。
    - **话题列举给出确定顺序**：排序键是 (UserTimestamp, Depth)，而**同深度**的两个话题可以共用一个 user 时间戳（融合父带的就是它组内首轮的 `UserTimestamp`，任何同深度、时间戳相同的话题都与它打平），打平时顺序只能由记录扫描给出——现在以记录 id 收尾，同一个场景两次读给出同一个顺序（`TestListTopicsL2BreaksTiesOnID`）。
    - **顺手净删**：`index` 里与 `core.IterAll` 同形的第二份扫描（自己 `json.Unmarshal`，绕过帧类型校验）、`domain` 两处永不成立的 `L2Meta == nil` 分支（同文件第三个函数就直接解引用）、`ensureRegistered` 里第二份永不触发的空名校验（唯一的调用方 `SubAgent` 先拒）、一个零调用的 L3 测试辅助函数；`scene.DetachGraph` 扫完一遍场景后又按 id 把每条命中的重读一次再改写，同锁内那次重读的失败分支永不成立——现在直接用扫描已解出的 slot 改写。
    - **文档四处不实/越界**：`cap` 的「不认识层，收到的都是渲染好的文本」与四个包的现状冲突（`profile.Samples` 自己扫 L1 并按本包常量裁剪，`engram`/`knowledge` 收的是 engine/节点原语），改写为「不认识编排，预算各归各包」；`domain` 两处复制根条目已有的镜像属主纪律、`turn` 一处替键的语义说话、`dream` 一处断言别包的写入集合，各按 D01 收回或上移（「内容只被话题寻址、话题里推不出场景」此前只写在 `turn` 里，现归根条目）；两个写入口的取舍与代价落为决策档案 `notes/implemented/architecture/2026-09-11-l4-content-two-write-entries.md`。

21. **上抛的错误都带着自己的码**：`ReadRecord` 此前把帧解码器的裸 `io.EOF` 原样交出，而 `api.CodeOf` 对不是 `*common.Error` 的错误返回 0——0 正是「成功」那一档，一次拒绝于是穿着「没有结论」的外衣到达宿主；快照装进来的索引条目不校验偏移，一条 CRC 自洽而偏移高到记录区之外的条目就能让某个 id 指到日志以外（现在按 id 读把它译成 `ErrCorruption`，`TestReadRecordCodesAnIndexEntryTheLogDoesNotHold`）。`Dream` 的「一个场景都没巩固成」同样是裸 `errors.New`，现带 `ErrLLM`。剩下的几处说的是同一件事：**取消**。新增 `ErrCancelled`（5008），Dream 的每个检查点与 LLM 传输里被调用方撤掉的两种等待（请求在途、退避待重试）都报它，`ctx.Err()` 留在 cause 里，`errors.Is(err, context.Canceled)` 照旧成立——被撤掉的请求在 HTTP 栈里报回来的也是一个错误，按状态码分类就成了「服务拒绝了」，而一次报成模型失败的取消会让宿主去查一个从没拒绝过它的东西。回复被输出上限截断也补上 `ErrLLM`（cause 仍是 `ErrTruncated`，升级预算的那次重试靠它判定）：截断通常被升级吃掉，活下来的那一次正是宿主的最后一手信息，此前它带着 0 码。`memhop_dream` 改用 MCP 请求自己的上下文，客户端走开后这一趟在下一个检查点停下，不再以 `context.Background()` 顶着一把域锁跑完整条流水线（`TestCancelledDreamReconcilesTheReadPath`、`TestDreamCancelledBeforeAnySceneLandedReportsCancellation`、`internal/llm/provider_test.go` 三条、`TestHandlePartialKeepsResultAlongsideError`）。

22. **顺序与判据的说法各处对齐**：跨话题定序改了实现，却留下五处对外文本仍在说「按 `Seq` 升序」（门面注释、宿主自己构造的 `core.L4Query` 文档、MCP `memhop_archive_search` 的工具描述、两份集成指南），其中 MCP 那句同时写着「一个都不填即全域扫描」与「limit 只保留最新的 N 条」——在按 `Seq` 的说法下这两半互相矛盾；五处统一按「一个话题内看 `Seq`，跨话题看记录自己的时间、以 id 收尾，`Limit` 取该序末尾」复述。`internal/agent.md` 把「哪些枚举必须整个读得回来」的判据外扩成「决定下一次的写（删谁、覆写谁、要不要新建哪一条）」，`internal/repo/agent.md` 里那份范围更宽的副本随之删除（一处事实一处），§7 同一编号条目内「读回顺序只看 `Seq`」补上「一个话题内」的主语；决策档案记下这次外扩到文件级公共池的代价。

23. **`SubAgent` 换端点真的落到活域上**：`db.llmByAgent` 只被 `contextFor` 在**新建**上下文时读，而 `ac.LLM` 全仓唯一的赋值点在 `domain.NewContext`，于是门面注释承诺的「同名再调一次就换掉它的端点」对一个活着的域永不发生——要等一次 idle 回收，而 `AgentIdleTTLMs=0`（关掉回收）时进程生命周期内都不换。后果落在记忆上：换了 key 或模型的宿主每一轮 `Update` 继续打旧端点，旧 key 一失效就是每轮 `ErrLLM`、那一轮永不沉淀。现在 `setDomainLLM` 交出构造好的 transport，`SubAgent` 在域锁内把它装到现上下文上（`TestSubAgentMovesALiveDomainToItsNewEndpoint` 以关掉回收的方式钉住「只有替换这一条路能移动它」）。

24. **场景链的三处断路补上**：`scene.Create` 此前先落场景记录、再去解析宿主给的 `L3ID`，于是一个拼错的锚 id 会在盘上留下一条 `session:<id>`——它的 id 是在被拒的那次调用里铸出的、从没有交回调用方，宿主既指认不了它也没法删（`Search` 带着未知锚现在一字节不写，`TestSearchRefusesAnUnknownAnchorWithoutLeavingAScene`）。`scene.DeleteCascade` 的三步删除按「最深的先删、场景与话题记录的墓碑殿后」重排：那三步都只照给定 id 落墓碑、不读 payload，还能拒绝的只剩引擎关闭与 IO，而一次落在旧顺序中间的拒绝会留下「记录已墓碑、缓存还在列它」的半成品，且调用方重试时在读场景记录那一步就永远 `ErrNotFound`——再也删不掉。`SearchL4` 补上与 append 同一口径的词表校验：填了未定义的 `Kind`/`Type` 报 `ErrInvalidQuery`，不再用一份空清单去回答「这一轮真没这类内容」和「这个值不存在」两种问题（`TestSearchL4RefusesUndefinedFilterValues`）。另把两处说法改准：「开了没沉淀的轮次不留残渣」限定为 `Search` 自己那一侧（宿主在那把键下写的内容与计划树是真实记录，只能由保留窗收走），而读取带回的 `role 3` 是融合组摘要自己的标记，门面写明它刻意没有名字。

25. **LLM 那一层的记忆质量四处收紧**：蒸馏的回包此前只要「是合法 JSON」就算答到，`{}` 也一样过——`emotion` 抹成 0/0/0、四个 0 维被 `deriveMBTIType` 译成凭空的「ESFP」，`MergeDistill` 再无条件覆写上去，一次答非所问就把画像那半区擦掉还报 `l0_distill` 成功（现在 `emotion`/`mbti` 缺整块即 `ErrLLM`，`TestParseDistillResponseRefusesAReplyWithNoContract`）。同一份回包里点名一个本次没交给它的节点 id（抄错一位的合法 hex）原先会一路走到 L1 情感回填，被「这条记录读不回」挡下——**每次 Dream 都在最后一步失败**，情绪加权的衰减从此长期缺位；现在按本次样本集在 `llmops` 内丢弃那一行（`TestParseDistillResponseKeepsOnlySampledNodeRows`）。输出预算此前有三处直接用 8192 而不管端点声明的上限：上限低的模型恰好在「升级救场」那一档收到一个被直接拒掉的请求（那一轮就此不写），上限高的模型又被 8192 顶住、`ChatWithRetry` 因 primary==retry 而根本不升级（大场景的巩固整场失败）；现在阶梯的每一档都不越配置上限，截断升级则正好用它（`TestKeywordLadderStaysWithinTheConfiguredCeiling`、`TestTruncationRetryEscalatesToTheEndpointCeiling`）。巩固 prompt 里「必须压到 20 以内」是写死的，宿主把下限调成 40 仍被要求压到 20，而凑够 21 个话题就强制合并一组、与「不同主题禁并」直接冲突——目标条数现在随 `DreamCompressMinTopics` 渲染，且明写任何数字都排在那条规则之后（`TestSystemConsolidateStatesTheConfiguredFloor`）。同族净删两处无人读取的回包载荷（`l2_compression_needed` 与每组回显的 `scene_id`——后者解析最严，认不出就把整场景的巩固报成失败，而没有任何人读它），退化的一成员组不再静默跳过而是计入「提出但未应用」（`TestApplyGroupsCountsADegenerateGroupAsProposedButUnapplied`），`profile.Samples` 那个没人读的第二个返回值与 `keywords.go` 指向已改名词元的注释一并清掉。

26. **一次合并留下的 L1 幽灵，与回收的那把竞态**：`MergeScenes` 把次场景的话题改挂到主场景、再墓碑掉次场景记录，却留着次场景的 L1 节点没人管——`engram.RebuildFromL2` 判陈旧看的是节点自己的 `TopicIDs`，而那些话题条条读得回来，于是这个节点永远顶着一批不再属于它的轮次：继续按 `UpdatedAt` 衰减、继续被 `BuildHyperedges` 拿去与活场景配对、继续进 L0 蒸馏的样本，要等几百小时墙钟衰减才把它抹掉。现在次场景的节点在验完 id 之后、任何写之前删掉——先删才谈得上可重试，合并被拒时场景还活着，下一次 Dream 会把节点建回来（`TestMergeScenesRemovesTheMergedSceneNode`）。同族的另一半是句不实的承诺：`DeleteScene` 的注释写着「incident hyperedges 由下一次 Dream 的 rebuild 清理」，而 rebuild 遍历的是**还存在**的节点，被删掉的那个再没人看；现在 `decayOneEdge` 按「本域还持有这个节点吗」过滤成员（此前只看本轮刚删的那几个），一条只剩幽灵成员的边随 `MinEdgeNodes` 一起删（`TestDecayOneEdgeDropsAMemberThatIsGone`），该判据唯一的辅助函数 `removeUint64s` 随之消失。空闲回收的竞态：清扫在域锁**外**决定回收、解锁之后再摘，一个已经取到上下文、却在取锁前被调度出去超过 TTL 的调用方会拿到一把没人持有的锁，把写落在一份已作废的缓存上——同一个域就此出现两个活上下文，镜像与记录的对应关系断裂。现在摘除与 `ac.Reclaimed` 打标在同一个持锁区间内完成，`lockAgent` 锁内复检到标记就重取域（`TestIdleReclaimMarksTheDomainItDrops` 钉住「打标与摘除一起落地」；重取那一步要把一次停顿塞进同一函数的两条语句之间，除在生产路径上开口子无法稳定复现，故没有负例测试）。

27. **对外文本与代码的七处对不上**：`Search` 带着已存在的 `scene_id` 又填 `l3_id` 是**硬拒**（`ErrInvalidQuery`），但六处文本写的是「只在新建时生效」——门面注释、`core.SearchQuery` 的字段文档、内部大方法注释、MCP `memhop_search` 的工具描述与两份指南，全都可以读成「填了会被忽略」；六处统一改成陈述这次拒绝，门面与 MCP 两条并指明改锚只走 `UpdateScene`（工具面没有改锚的口），原先只断「有个错误」的那条用例现在钉住错误码（`TestSceneAnchorAgreesWithTheGraphSurface`）。`Limit` 还剩三处写着「只保留最新 N 条」，与第 22 项统一过的定序说法打架（一个话题内看 `Seq` 时，末尾就是槽位最高的那几条，不是「最新」）——门面字段文档、两份指南的示例注释、MCP 的 `limit` 参数描述都改为「取该序末尾」；两份 README 的 L4 行是同一族剩下的两处（英文写着「keeps the newest matches」、中文写着「只留 Seq 最高的 N 条命中」，各自只说到两种定序里的一种），现按 `core.L4Query` 字段文档统一为「该次读取排序后的末尾」。MCP 的两个返回值词表此前只写在 Go 侧：`memhop_archive_search` 的描述现在直接给出 `kind` 0/1 与 `role` 0/1/2 以及 `role 3` 是什么（一个纯 MCP 客户端只会看到数字）。`memhop_knowledge_nodes` 说关键词「模糊匹配标题」，实际还对正文与关键词轨做子串匹配，且条件全部 AND、结果按节点 id 升序、`limit` 取该序前 N 条——按实现补齐。README 头图的「seven-layer」是第 v1.6.3 轮收敛成六层 L0–L5 之后没跟上的半句；两份 README 的「项目结构」段整段是旧的（`openmulti`、`api/ids`、`internal/defaults`、`tuning`、`l6`、`agentctx`、`plancache`、`llm_client`、`llm_ops`、`strutil` 都不存在了，第 3 层六个小方法包与 `cap`/`config`/`domain`/`llm`/`cmd` 一个都没列），按当前树重写。指南里指向已删除的 `internal/tuning.go` 的两处改为按现在的归属陈述（衰减与共现阈值随 Dream 阶段、输出预算随 `cap/llmops`、蒸馏样本预算随 `cap/profile`）；中文指南一处把任务面写成 20 个方法，同文件 §8 列的是 19 个。另清掉一条测试注释里对已删键 `topic counts` 的引用。`internal/repo/agent.md` 里 `ArchiveQuery` 的 `Limit`「保 Seq 最高 N 条」是定序改动后剩下的半句（本层两个比较器按 `TopicID` 有无分工），按实现改成「排序之后保留末尾 N 条」；L3 节点读的三份契约（条件全 AND、关键词覆盖标题/正文/关键词轨、按 id 升序且 `Limit` 取该序前 N 条）的权威表述在 `core.L3NodeQuery` 的字段文档里，MCP 那条描述此前只说了其中一半且说错了匹配范围，现在与它对齐，不在根条目再抄一份。

28. **一次读不回不该藏起刚沉淀的一轮，`Depth` 也不再被说成父/子之分**：
    - `ac.SyncL2Meta` 此前回读它刚写出去的那条记录，而任何一次读失败（含一次瞬时 IO）得到的回答都是「把这条从镜像里摘掉」——于是宿主已经被告知沉淀成功的一轮，从 `Search` 与 `SceneContext` 两路场景读里同时消失，要等下一次 Dream 整表重建才回来。现在刷新用的就是刚写出去那个值（`CreateTurnTopicL2` 把它交回去，`RenameTopicL2` 本来就交回），镜像这一步不再有自己独立的失败分支。
    - `SceneContext` 对空场景给出的是 `topics: null`，而 `Search` 给 `[]`——同一句「这个场景还没有话题」两种形状，现在统一为空清单。
    - 三处文本把 `Seq` 的空洞与 `Depth` 说成了它们给不出的判据。一个话题的 `Seq` 槽位空间由原文与事件**共用**（事件从 3 起自动发号，点名槽位可跨 Kind 覆写），所以原文视图里跳号只说明那个槽空着、不指认是谁腾空的（`core.SceneMessage` 与 `scene.ContextTopic` 各一处）；`Depth` 说的是「还在场景表浅（1）/ 已被某轮巩固折走（2）」，而融合父自己也会在下一轮被折走，那时它与它归并的那几轮同为 2——认一组看 `ChildCount` 与它那条 role 3 的摘要（DTO 文档、两份指南的 `SceneContext` 行各一处）。两份 README 的 L2 行还写着「4 级压缩深度」，实际一条话题至多下沉一次；`ListTopicsL2` 那条「浅者先行」的兜底也补上适用边界（父被折走之后两者打平，只剩记录 id 定序）。
    - 两处不实的说法按现状改写：`scene.DeleteCascade` 的「镜像都在磁盘同意之后」——内容镜像是随它镜像的那批内容一起在 `DeleteTopicArchives` 内摘掉的，只有 L2Meta 与计划树等到最后一张墓碑之后；决策档案把「跑一次 `Compact`」写成这一类损坏的修复手段——那次重写按记录自己的字节搬运，CRC 自洽却解不开的 payload 原样进新文件，这条被拒因此是长期的。`internal/cap` 的第 7 条纪律复述了 `dream` 与 `llm` 各自拥有的事实，收回到本包边界。
    - 测试面三处补牙：关键词阶梯由「至少三档且不超过上限」改为钉住每一档的具体预算（上限 16384 时 `[512,4096,8192,8192]`，上限 64 时四档全 64），把格式约束重试那一档的预算写低即被抓住；巩固假端点不再伪造无人读取的 `scene_id`/`l2_compression_needed`/`capabilities` 键；新增两轮巩固的树形测试钉住「融合父可被后一轮折走，且与自己的子落在同一个 depth」（去掉 `Depth++` 会被它抓到）。

29. **共现边的遗忘此前每轮被自己抹平**：一条边的权重来自两个场景关键词集合的 Jaccard，而每一次巩固都会把这份集合重算一遍——集合没变，重算出的就是建边时那个相似度，于是 `weight = max(衰减后的值, 相似度)` 每一步都把它抬回满强度：一条不再有新证据的关联永远落不到删除阈值，`LambdaEdge` 与 `LastDecayAt` 形同虚设（`max` 挡住的是「更弱的旧相似度把边压低」，从来没有挡住复活，而 `BuildHyperedges` 的文档恰好把这件没做的事写成做了）。现在一次上升需要一个端点的证据真的动过：`repo.SyncL1NodesFromL2` 不再只交出「写了几个节点」，而是交出**它写过哪些节点**，`dream.l1Stages` 把那份集合递给 `engram.BuildHyperedges`——本轮新建的边照旧取相似度为权，端点都没动的边保持衰减后的权重继续淡出（`TestBuildHyperedgesKeepsADecayedEdgeDecayed` 钉住这两半：把 `!evidenceChanged` 那一支短路掉即报 `n=1`）。同族把另一处时钟的说法说清楚：`decayOneNode` 读 `UpdatedAt` 又在同一步把它重戳，所以节点的重要度按「距上一次巩固的间隔」计衰减，而不是按「距上一次活动」——本轮被同步重戳过的节点于是 `dt≈0`，一条还在被经历的 scene 不因看见它长大的那一步而褪色（`l1layer_sync.go` 那句反过来的因果说明随之改写成现状）。

30. **一步的序号不再被覆写，「唯一」限定为「同时活着」**：
    - `plan.CreateNode` 此前照镜像发来的序号直接落记录，而那份镜像由 `CollectAllPlanNodes` 建（走 `IterAll`，payload 解不开的记录被跳过）——一条损坏的计划节点因此从镜像里消失，可它的序号仍写在由 (轮次键, 序号) 派生的地址上，下一次创建就会顶到那个地址、把它当成空槽覆掉，原本指着那个序号的事件从此读作新步骤做过的事。现在创建先问盘：那次定点读除「不存在」以外报任何失败都不写，错误带它自己的码（`TestPlanCreateRefusesAnAddressItCannotRead`，把那道预读短路掉即报 `got <nil>`）。
    - 门面对序号的承诺由「同一轮的两步不共用一个序号」改为「同一轮没有两步**同时**活在一个序号上」：保留窗扫掉的步骤会腾出序号，扫空一棵树时那棵树的键一起消失、这一轮重新从 1 起号，而事件按自己的 `CreatedAt` 老化、可以活过它所标注的那一步——那正是本仓 `TestDreamPrunePlanNodesAndContent` 钉住的形状。`NextSeq` 的 `ponytail:` 随之把这个上限与升级路径写全；那条把缺口说成「靠 L4/L5 共用同一保留窗兜住」的决策档案改写成它未闭合，并把宿主要遵守的边界写到门面方法上。

31. **镜像发出的地址不止计划树那一处，随它把一批对外说法改准**：
    - 同一族补到 L4：`content.Append` 的 `Seq=0` 也是本包在替宿主选地址，而它读的那份内容镜像与计划镜像同一形状——`BuildL4FromEngine` 跳过解不开的记录，槽位号却仍写在 (话题, `Seq`) 派生的 id 里，于是自动分配能顶到一条读不回的记录上、把它当成空槽覆掉（`TestAppendArchiveRefusesASlotItCannotRead`，短路那道预读即报 `got <nil>`）。点名 `Seq` 的覆写照旧不查，那是宿主自己选的重放语义。计划创建那条预读的**长期**后果此前没写：下一个序号还是从幸存节点数出来的，所以那个地址每次都会被重新提出、每次都被拒——门面与 `internal/plan` 各补一句（本包不绕过读不动的地址去发邻号）。
    - 门面按 go doc 读会读错的四处：`DeleteScene`/`DeleteTopic` 只承诺删「原文」，级联实际删的是该轮全部 L4 内容（事件同层）加计划树，且 `DeleteTopic` 还在教一份已不存在的「父话题的子清单」；`DeleteScene` 把共现边的收尾归给「下一次 Dream 的 L1 rebuild」，实际是 `decayOneEdge` 按成员还在不在本域来剪；`PlanNodeView.ParentSeq` 写「0 即根」，而 `BuildTree` 会把父不在本次清单里的那一步提为它自己的根并保留原 `ParentSeq`，按 0 认根就漏节点。另有三处补边界：`PlanState` 对没有树的键给空树而非报错，`PlanNodeUpdate` 的「零留痕」限的是被拒的重述本身、回填发生在它落盘之后（失败只欠一份父摘要），`SearchL4` 的 `NodeSeq` 拒因从「never created」改成「不在这轮的树上」（被保留窗扫掉的步骤撞的是同一句）。`core.L4Query` 末尾残留的「most recent matches」与同段已统一的「该序末尾」并成一句。
    - 两处判定与一处族系说法随之改准：`internal/cap` 第 5 条把 depth≥3 的记录归给「收敛之前写成的文件」，那在打不开的版本里（`0x0012` 由 `d7837e7` 落下，成员互斥由 `d859f08` 修好，隔一天），真实来源是这中间那批构建把同一轮沉了两次；共现边「只有新证据才抬权」在两份 README、`internal/cap`、`internal/repo` 与 `l1layer_sync.go` 五处都写成「端点的关键词变了」，而 `touched` 记的是**端点名下的轮次清单变了**——同一批轮次被重新蒸馏一次换了措辞不算新证据，五处按判据改写。`internal/domain` 那句「镜像缺席只影响读」随 L4 分配一处不再成立，改成分别说清两份镜像各自怎么兜。

32. **L1 边上那格恒为常量的 `kind` 与它的六值词表删除**：`SceneEdge.Kind` 只有一个写入点、写的就是 `HyperCoOccurrence`（边的身份本就由两端派生，一个节点对只有一条边），全仓没有任何一处读它——不进 DTO、不进 MCP、不进日志，那张名字表与它的 `String()` 也没有调用者。旧文件里多出的 `kind` 键在解码时本来就被跳过，所以**帧布局与 `FormatVersion` 一律不动**（与 L3 那三个无读者字段同一判据）；随它删掉的还有一条只重述常量取值的枚举测试。其余三份词表**留下**：`ContentType`/`ArchiveKind`/`GraphEdgeKind` 的取值由宿主写入决定、被边界校验与对外名表读着，那三个 `String()` 是未知取值的唯一渲染口。`repo.MaxDepth` 那条「沉到第 4 级就删」的分支同样**留**——它现在够不着（要够得着，得有一条 depth≥3 的话题出现在 depth-1 清单里），但 depth≥3 的存量记录真实存在，留着它是给折叠计数兜底的止损，`internal/dream` 已写明它永不触发。

33. **MCP 这一面按「客户端只看得到工具描述」重校，四处行为 + 五处文案**：
    - 三个入参形状缺陷：**可选字符串过滤器的空串不再被当成一个取值**——`memhop_archive_search` 此前把 `kind:""` 解析成 utterance、`content_type:""` 解析成 text（那两个空值默认是给 append 那侧用的：不声明种类的记录就是某人说过的话），于是「不填即两种都要」在最常见的一种客户端写法下失效，只回对话而把这一轮的事件静默藏掉；现在空串等于不填，两份属性说明也随之写明（`TestSSETurnFlow` 钉住：`kind:""` 要回三条含一条事件，短路掉那道判空即报 `2 records, 0 events`）。**图不能被改名成空标签**：`UpdateL3` 的 `name` 是 `*string`，不填即不改，可一个指向空串的指针此前一路走到落盘，而 domain 标签正是 `ImportL3` 寻图的唯一方式——改完那张图谁也找不回来了；现在与 `RenameTopic` 同口径在 Go 边界拒空标签（`ErrInvalidQuery`），MCP 那条说明随之写清「不填」与「空串」的分别。第三个是**二进制报的版本停在 v1.6.3**，而 `initialize` 的返回值是每个客户端第一眼看到的东西，随仓库记录的 v1.6.4 对齐。
    - **拒绝带码上抛到工具面**：`errResult` 此前只落错误文本，而多条工具描述拿码名承诺拒绝口径，宿主又没有任何别的通道拿码——「这条记录没有」(3001) 与「这条记录读不回」(5001) 只能靠猜文本；现在引擎的错误带 `[码] ` 前缀（`memhop_archive_search` 撞保留全零键那条断言钉住，去掉前缀即报无码）。参数解析层自己的拒绝（如 `invalid role ""`）不带码，那是描述里本就写明的可读文案，不为其编造码值。
    - 五处文案与实现对齐：`memhop_update` 的拒因从「内容已被 7 天保留窗裁光」改回真实条件「该轮当前没有可提炼的对话原文」（最常见成因是 append 没落盘或 topic_id 拿错，裁光只是其中一种）；`memhop_dream` 的 `consolidated` 写明它只表示「这一趟真做过 L2 融合压缩」，保留窗清理、L1 重建与 L0 蒸馏都不算它，要看做了什么读 report；`memhop_knowledge_list` 与 `memhop_knowledge_delete` 说出图池是全文件共享的那一份——删除的影响范围包含别的租户挂在这张图上的场景锚点；`memhop_knowledge_import` 补上两层拒绝（整批预校验不过是一条不写，过了之后单条失败才进 errors）；门面 `AppendArchive` 与两份指南把上一轮加的「分配先确认那一格是空的」写到宿主看得见的地方（此前只有计划那半写了，内容这半的拒绝理由无处可查）。
    - 另把抬权判据的方向改准：`touched` 记的是端点名下的轮次清单**变了**（少一轮与多一轮同算，删除与合并都会造成），此前五处写成「多了轮次/关键词变了」，`hyperedge.go` 那条参数说明是漏改的第六处。
34. **第 11 轮审查（门面映射 / 索引镜像 / 装配与工具管道）**：
    - **巩固组的回滚要覆盖它已经沉下去的成员**：`applyOneGroup` 一直承诺「要么整组落地、要么什么都不留」，而下沉是**一次批写**——`WriteRecordBatch` 在推进日志尾界之后才失败（`Sync`/remap 失败即这一路）时，已到达的成员停在 depth 2 挂着这个父，回滚却只删父记录与它的摘要内容。后果不是「多了一条脏数据」而是**那一轮从此读不到**：`Search` 只列 depth 1，下一趟 Dream 的挑组也只列 depth ≤ 1，它既不在 surface 也不会再被任何路径合并，只有 `SceneContext` 还认得它。新增 `repo.RestoreSunkTopicsL2` 定点撤回，判据就是那条父链接本身（挂在别的父下、以及本组没点名的成员都不动），且**先撤子再删父**——两步之间再失败留下的是「摘要与原文并存」，那还能被下一趟合掉，反过来留下的是找不回的轮次（`TestRestoreSunkTopicsL2BringsBackOnlyThisGroupsMembers`：把判据放宽成「点名的都撤」即报别的组的成员被误撤，去掉深度下限即报浅层成员被踩到 depth 0）。
    - **上游的拒绝不再逐字贴进错误文本**：`httpError` 此前把 `RequestError.Body` 整个 `string()` 出来拼进错误，而这段文本一路去两个地方——MCP 客户端看到的工具错误、以及 stderr 上那条 WARN。网关与代理在 4xx 上回显一整页 HTML 是常态，一次调用失败于是变成一条两万字节的日志；现在只留开头 256 字节，且截口按 UTF-8 收口（中文网关的正文是多字节的，硬切会产出乱码）。额度内的正文原样保留，两端都有断言（`TestUpstreamErrorBodyIsEchoedBounded`：去掉截断即报 21615 字节的错误文本）。
    - **第三个空串入参**：`memhop_archive_search` 的 `topic_id:""` 仍被当成一个地址，被 `ParseTopicID` 硬拒（`[1003] invalid id ""`），而同一个工具里 `kind`/`content_type` 上一轮刚改成「空串即不填」——客户端按同一种写法发，三种参数两种结果。现在空串等于不加这条约束，`TestSSETurnFlow` 断它回的记录数与完全不填一致。
    - **一个枚举的词表只判一次**：`GraphEdgeKind.Valid` 新增，`ImportL3` 那道裸范围比较（`> EdgeCustom`）改走它，`QueryL3Subgraph` 的 `edge_kinds` 过滤器同时开始拒未定义值——写侧拒收的值在读侧过滤不出任何东西，宿主分不清「图里没有这种边」与「这个名字我拼错了」，这与 `SearchL4` 的 `Kind`/`Type` 上一轮修掉的是同一件事（`TestQueryL3SubgraphEdgeKindFilter`）。
    - **宿主唯一的文档入口此前读不到六个响应形状**：`DreamReport`/`DreamStage`/`SceneContext`/`SceneContextTopic`/`SceneMessage`/`L3ImportResult` 都是门面别名，而 `go doc github.com/qyiun666/MemHop/api DreamStage` 对一个别名只出一行——`internal` 不发布，字段语义（九个阶段名与四种状态、depth 1/2 的分别、`child_count` 数的是什么、`role 3`、`GraphIDs` 含只读到的图）对宿主根本不存在。文件头那句「只有入参类型是别名」也因此不成立，两处一起改成实情：需要渲染 id 的响应是真实结构体，其余是别名而别名带着自己的说明。
    - 六处对外文本与代码对不上：`ProfileSlot` 还在教「填了只读字段会被接受并忽略」，而自 `ProfileInput` 拆出后没有任何调用接受它作入参；`ContentType` 常量段的说明把「计划写面」算成会存内容的路径（门面自己的 `PlanCreate` 注释写着 This call writes no content），且漏了读侧同一条拒绝；`ImportL3` 说 `GraphIDs` 是「本批写入的图」（只读到的也报）；`UpdateL3` 说改回原名是 no-op（标签没动，图的变化钟照样推进，`name=nil` 就是它的盖章拼法）；`AppendArchive` 的字段清单读起来像宿主传的每个字段都照存，实际 `CreatedAt` 由宿主提供且非正即拒、event 的 `Role`/`ContentType` 是库定的；`Open` 与被它照抄的一条内部注释都承诺「拒绝排在碰文件系统之前」，成立的是「不新建文件」。`memhop_scene_topics` 的描述指挥客户端「由 parent_id 相认」，而那份返回里**没有 parent_id 这个键**（改成按 depth + child_count 认父子），并补上它真正的代价：域锁内读回整场景原文再丢弃。返回里的数值字段补词表（`type` 的 `255=other` 不在任何名字序列里、子图的 `kind`）。`sse.go` 文件头说租户白名单「保证每租户恰好一个文件」，实际全部租户共用 `--db-dir` 下那一个 `.meh` 里的 agent 域；`serverForRequest` 把建域失败静默压成一个 400，现在带 tenant 与原因落一条 WARN。`DreamCompressMinTopics` 的 0 值语义补进配置类型（同结构另两个旋钮都写了）：它不是关闭，而是「不设目标数、尽量合」，且这个数会进模型提示词的规则 3 与 9。
    - **两处审查处方被证据否掉，未采纳**：一是把 `DeleteTopicArchives` 的删除集从镜像换成严格枚举——严格枚举读不出「一条归档属于哪个话题」（id 哈希 (话题, 槽位) 而不点名任何一侧），换成它只会让一条坏记录挡住全域删除，而坏记录照样删不掉，于是改为把这条极限写进注释（它的可见痕迹是该话题 `Seq` 上一个永不回填的洞）。二是说 `l1layer_sync.go`/`decay.go` 里 depth>2 的判据在生产中不可达——写入侧确实产不出，但 `d7837e7` 到 `d859f08` 之间落盘的 0x0012 文件里真存在 depth 3 的话题，读侧那条分支是这些文件的入口，不是死码。另把 `api/mapping.go` 四处不可达的 nil 分支删除（四个生产者成功时恒返回非 nil，其中计划那处是唯一可能交出 `Roots == nil` 的出口），api 测试里断公开契约的那一处错误码改走门面自己的 `CodeOf`/`ErrInvalidQuery` 而不是未发布的 `internal/common`。

35. **第 12 轮审查（情绪标度 / 存储内核的损坏放大 / cap 计算包）**：
    - **情绪强度改成「离中性点多远」**：蒸馏回包的 valence 落在 `[0,1]`，0 是最负面、0.5 才是中性，而衰减的情感因子此前直接取 `|valence|`——它量到的是「有多正面」。后果是一倒到底：一个轻度正向、低唤起的节点算出 `lambda≈0`，而删除节点的唯一通路就是衰减，于是它**永远收不回去**；同时最痛苦的那批记忆按满速消退。现在按距中性点的距离算，且给保护设上限（最强烈的记忆仍留十分之一的基速率），带外取值也夹回因子 `[0,1]`——负 lambda 会让一条记忆每轮变重要（`TestEmotionalBoostMeasuresDistanceFromNeutral`：把标度退回 `|valence|` 即在中性断言上失败）。
    - **`0` 是合法读数，不是「还没盖章」**：L1 情感回填的判据是「两个字段都为 0 就还没写过」，可 `(0,0)` 恰是「极负面且平静」这份合法答案，于是这种节点每趟 Dream 都被重写一次，而每次重写都刷新衰减所读的 `UpdatedAt`——它同样永不衰减。回填前先比现值，值相同就不写（`TestBackfillL1EmotionsLeavesASettledNodeAlone`：去掉那道比对即报 `UpdatedAt` 被推到当下）。另把这个写 pass 没人读过的写入计数返回值删掉。
    - **两处「一条坏记录毁掉其后全部」在内核里断开**：一是 `appendFrames`/墓碑批写失败——被写进一半的那帧留在日志中间，其声明长度指到后面那条记录身上，而 Open 的尾截断够不着它（边界上的「拒绝即不落盘」讲的是另一回事，那条一直成立）；现在失败即把文件截回本批起点（`TestUndoAppendCutsBackToTheBatchStart` 直接钉截回、报错与截后仍可写）。二是 Open 的扫描把「校验失败」与「帧装不进文件」并成一件事而统统截尾——校验失败的那条帧是完整的，它的头说得出下一帧从哪开始，所以现在只跳过它并继续扫，末尾真被裁短的帧才截掉（`TestChecksumFailedFrameKeepsTheRestOfTheLog`：坏帧之后那一条现在读得到；退回旧判据即报「record 3 was taken with the damaged one」，同时 `TestTornTailFrameTruncatedOnOpen` 保持绿，说明截尾那条通路本身没动）。撕尾那两条模拟用例也各自归位：一个用真·半帧，一个用改一字节 payload 的完整帧。
    - **`Compact` 的拒绝保住自己那一档码并点名记录**：它此前把 `RecordData` 的一切失败压成 `ErrCorruption`，于是「checksum 坏了」与「帧装不进文件」这两种修法不同的损坏在宿主耳朵里是同一句话，也没说坏在哪一条（`TestCompactRefusalNamesTheRecordItCannotRead`）；裸错误仍统一收进一档，因为 0 码是「成功」。
    - **cap 层收形与收预算**：蒸馏回包的逐节点行改按已解析出的节点 id 直出（`per_node` 的 hex 在解析时本来就要解一遍判归属，交给调用方再解第二次是纯往返），`llmops.NodeEmotion` 那份同形类型随之消失，`DreamL0Stage` 里那段二次解析的循环与两次逐字段拷贝一并删掉（`EmotionScore`/`MBTIScore` 是别名，拷的正是自己）；`profile.Samples` 先排名再取关键词——排名只用节点自身的重要度与时钟，而关键词是每个话题一次记录读，旧写法为活下来的 200 行付了全量 L1 的读价；MBTI 四个维度答 0 即该轴没强度，取 `X`，四轴全静默就一个类型词都不给（此前它读成一个自信的人格标签，还会随画像摘要进入之后每一次调用）；人格长度上限只写一次（prompt 里那句由常量渲染，`TestSystemDistillStatesTheBudgetTheParserEnforces` 钉住两侧同源）；`Brief` 的「按构造有界」此前只对偏好值成立，宿主自写的 name/role/personality 与偏好键任意长度都能整段进 prompt，现在四处共用一个预算；四个只有包内读者的导出符号转私（`profile.Default`、`profile.SampleRank`、`llmops.BuildConsolidatePrompt`，以及响应形状上那两份无人编解码的 json 标签）。
    - **取值区间第一次写到宿主看得见的地方**：`internal` 不在发布文档里，宿主点进 `api.ProfileSlot`/`SceneNodeView` 时读不到任何字段语义，而「这个 0 是最负面还是没蒸馏过」正是宿主判断这份画像能不能用的前提——`ProfileInput` 的读回形状、`SceneNodeView` 与内核两个形状各写下一次标度与 `X`/空类型词的分别。
    - **全仓文档滞后清账**（逐条回源码复算，约 690 个符号）：README 两份的 Dream 步骤表列了一个不存在的阶段（「L0 画像 — 基于巩固后的记忆重建」，实际 L0 只有蒸馏那一步），三处还写着「压缩后每场景 ≤20 个话题」这条第 16 项声称已经改掉的假上限，`Config.LLM` 与 `L1EdgeMinSimilarity` 是两个公开面上不存在的标识符，`TestUpdateLongTurnNeverFails` 早已改名；`internal/agent.md` 的 `lastDreamAt` 随它的写入者一起没了、`dreamInFlight` 少写了字段所属；本地 `docs/mcp/README.md` 整份停在退役前的面（31 个工具、`--embed-model`、按租户分文件、7 个能力工具），按现状重写并记下「本服务跑零值调参、自动巩固不触发」这条一直存在却没写的事实，`docs/benchmarks/TESTING_GUIDE.md` 的测试与 benchmark 名单里那几个已经不存在的名字换成实名。另删掉两处指向未随仓库发布的规则文档的注释指针（`G-01`）——那条码段划分与「纯数据无行为」的约束由代码自己陈述。

36. **第 13 轮审查（场景写侧 / 事件预算 / 索引枚举 / MCP 生命周期与锁范围）**：
    - **场景记录「读不回」不再被当成「还没有」**：建场景那一步把任何读失败都当作不存在，随即写一条新的覆盖上去。代价不在这一条记录本身——读不回的那条正持有本域的 `TurnSeq`，而轮次话题 id 由它铸出，覆写等于把发号器归零，之后的每一轮都会拿到这个域已经占用过的键（原文、事件与计划树从此混进别人的轮次）。现在只有 `ErrNotFound` 允许建，其余原样带着自己那一档码上抛（`TestCreateSceneL2RefusesAnUnreadableRecord`）。
    - **一个场景落地只剩一次记录写**：原本写两条——先落 `session:<id>`，再由 `SetSceneL3ID` 补锚。中间任一步失败就留下一条场景记录，它的 id 是在被拒的那次调用里铸出的、从没交回调用方，宿主既指认不了它也没有删除的口。现在锚拼在待写的 slot 上一次写出去，函数直接返回刚写的这条（发号已在调用方的域锁下证明该 id 空闲，回读给不出新信息）；`SetSceneL3ID` 随它唯一调用者的消失一起删除，write-once 那条性质折进 `TestCreateSceneL2LeavesAnExistingSceneAlone`——重复调用后**名字与锚都幸存**。
    - **给已存在的场景带锚是「拒」，不是「找不到」**：这条拒绝此前先去文件级公共池读那个图槽，于是宿主听到的一句 `record not found` 说的是他从未要求读的记录，而且那次跨域读白付在他自己的域锁内。现在按入参直接拒，报 `ErrInvalidQuery` 并带出那个场景 id（`TestSearchRefusesAnAnchorOnAnExistingSceneWithoutLookingItUp`）。
    - **事件的 4 KiB 量的是整条记录**：预算此前只算 `Content`，`EventType` 没有上限——把一批正文写进那个「步骤名」字段就绕过去了，而它同样进入 prompt、同样占日志。`TestAppendEventPayloadRefused` 与 `TestAppendArchiveRefusesAndStoresNothing` 各钉两侧：正好卡满的正文照写，把正文塞进名字里以同样的方式被拒且零留痕。
    - **按场景列举话题不再扫全域**：scene 过滤走的是整个域镜像的遍历，压缩下沉后的 retarget 是每条记录一次加锁的二次方。换成 `TopicsByScene`（一次读锁）与 `RetargetScene`（一次写锁，且同时改掉 `entries` 行里那个 `SceneID`——只挪 id 清单会留下一份「按场景列得出、按行读却属于别的场景」的镜像，`TestRetargetSceneMovesTheWholeScene` 钉的就是这一条）。`TopicListQuery` 的 `ByScene` 位随之消失（生产只剩 scene-only 一条路），镜像上零生产调用者的 `Iter`/`GetByScene` 一并删。
    - **三处没人读的回执**：`content.Append` 交回的 `Seq`（内容只按话题键寻址，写口没有消费者）、`L4Index.RemoveIDs` 交回的条数（唯一生产调用点整个丢掉它，而镜像本就照给定 id 摘，那个数没有下一站）、`L2MetaIndex.Remove` 交回被摘掉的那一行（唯一的读者就是被 `RetargetScene` 取代的那段「摘出来改场景再塞回去」的循环）；`newL2MetaIndex` 转私，索引只能由记录或整表重建而来。
    - **MCP 的锁范围与退出路径**：registry 的锁此前盖住 `db.SubAgent`，而 stateless streamable-http 每个请求都要经这里拿 server——一个租户在一次 LLM 往返期间把所有其它租户顶在门外。现在锁只护 map 与白名单，`SubAgent` 在锁外做，回来后双检插入、保留第一个 server（域注册由引擎串行且按名幂等，两次并发首访于是落到同一个域，`TestRegistryConcurrentFirstAccessServesOneServerPerTenant`）。`ListenAndServe` 失败此前直接 `os.Exit(1)`，而落 checkpoint 的正是 `Close`——端口被占的那个进程白丢一次已经写进内存的巩固结果。`--transport` 在两处判定，`parseFlags` 那处的 default 分支不可达（`buildHandler` 是唯一读者），收成为唯一判定点的 `buildHandler`，且**排在开共享库之前**：一个跑不起来的传输配置不该顺手把文件打开。
    - **对外文本的过度承诺收回**：门面管理面自称「这几口在 MCP 都有对应工具」，而两个记忆纠错口只在 Go 侧；`tools.go` 头注释声称「每个公开 DB 方法一个工具」；`OpenShared` 把 `os.Root` 说成所有文件操作都经它解析——文件名是常量，唯一能让路径走出 db-dir 的是被人放在那里的 symlink，`os.Root` 检出的正是它，真正的 open 走的是拼接路径；`api/types.go` 里 11 个入参别名一个字都没写（`SearchQuery`/`L4Query`/`ScenePatch`/`PlanStatus` 恰恰是宿主点进去要找契约的地方），`LlmConfig` 那两个 0 值默认（120 秒 / 8192）也没落在宿主读得到的位置；`memhop_trajectory_read` 的 `session_id` 没写明它是 `Search` 带回的那个场景 id、填错只会静默给一份空清单。
    - **本轮偏离批准计划之处（命名）**：计划写了「不改函数命名」，这里改了三个且都有理由——`CreateSceneL2WithID` → `CreateSceneL2`（签名换成收 slot 之后 `WithID` 已无所指），`scene.Create` 与 `scene.FreshID` 转私（各自只有本包读者，转私是本包的边界声明，不是风格偏好）。

37. **第 14 轮审查（内核的销毁路径 / L3 地址占用 / LLM 失败的归类）**：
    - **一次被锁拒掉的 `Create` 会清空一份完好的库**：`O_TRUNC` 由 `os.OpenFile` 自己执行，排在排他锁回答「有没有别人正读着这个文件」之前——失败的一方什么也没拿到，被它清掉的那一方还在 mmap 上读它已经不存在的内容。创建现在只 `O_RDWR|O_CREATE`，清空记录区是拿到锁之后那一次 `Truncate(DataStart)` 的事（`TestCreateRefusesAFileAnotherInstanceHolds`）。
    - **一个 rot 掉的长度字段此前会带走它后面的整段日志**：声明长度大到装不进文件时 `RecordData` 报 `ErrCorruption`，而 Open 恢复把它和撕裂尾帧当成同一件事，从那个偏移截断——坏记录之后的每一条都被砍掉，下一次 checkpoint 把这件事变成永久的。现在两种校验和/长度都不符的帧走同一条损坏分支：向前找第一个「自己的校验和过得去」的偏移（不能照这一帧自己声明的长度走，那 4 个字节正处在刚被它自己的校验和否定过的那段里，差一字节就落进下一条记录中间，而它读出来的坏头通常又报「装不进文件」，于是截断信号被一条 rot 骗出来）；只有游标之后再也读不出任何记录时才截断；每一次跨过的退出都落一条 WARN，包括提前停止的那些（`TestRottedLengthFieldKeepsTheRecordsAfterIt`）。
    - **长度的算术按无符号做**：声明长度此前先窄成 `int` 再与剩余字节比，32 位构建上一个 rot 成 4 GiB 的值会翻过符号、把这一帧切到映射之外；随它删掉的还有 `frameSpanOf`——恢复不再需要照 rot 的长度算宽度，它就没有读者了。
    - **L3 的三个建记录原语会把别的种类的记录就地改写**：图槽/节点/边的 id 全部由宿主给的文本派生，而整个公共池共用一个 id 空间，所以一张图的标签可以正好拼出一个节点的地址；typed reader 对这种碰撞答的是 `ErrNotFound`，「没有」于是成了唯一能允许落笔的答案——三个 create 现在先问 `Contains`（它不看种类，而这道闸要拦的恰恰是「任何种类正占着」），占着就带 `ErrInvalidQuery` 拒掉，而不是把那条记录改写成图槽还报告成功（`TestImportL3RefusesADomainNamingANodeAddress`）。
    - **一个枚举的词表只列一处**：`L3ImportMode.Valid()` 是唯一的取值列举，`NewImportBatch` 在建批时就拒未定义的 mode（把策略函数连同 mode 一起收进批次，未定义 mode 因此永远走不到某个 `default`），根里那份 `switch mode` 随之删掉。
    - **两处「模型答非所问」其实是被取消的重试**：巩固与蒸馏的格式化重试在第二次调用失败时交回的是**第一次**那份解析错误——一次取消或一个拒了的端点因此被报成模型给过一份不合契约的回答，宿主去查的是从没拒绝过它的东西。现在带重试自己那一档码上抛，两份失败一起留在因果里（`TestFormatRetryFailureKeepsItsOwnCode`）。
    - **一处永不带码的退出被删成可达的那一条**：传输层的 `attempt == len(delays)` 让循环末尾的 `return "", lastErr` 成为死路，而「最后一次尝试的失败就是要给的答案」本来就只该由那一条来说；其余死码与不实文本一并清账：索引的第三份枚举 `allEntries`（零调用者，测试改走 `IndexByType`）、一处把机器本地测量写进注释、`config` 与 README/AGENTS 把 `MemHopConfig` 说成宿主入参（它是组合根拼出的装配视图），以及 plan/graph/domain/engram/repo 五份 `agent.md` 里的主语越界与同一事实的第三份副本。
    - **本轮记下但没有动的两件事**：`llmops.ConsolidationMaxTokens` 与库默认的 `MaxOutputTokens` 相等，所以宿主没抬过那个字段时截断升级没有更宽的一档可用——一条装不下的融合摘要就是该场景这次巩固的 `ErrLLM`。这是记忆质量的真实取舍（抬那个数会同时收窄关键词阶梯最宽的一档），已按 `ponytail:` 写进常量旁；**已裁定：两者保持相等，只留文档**。一轮转录的总量没有预算（单条 64 KiB 只量一条记录），锁内停留由取消而不是由条数上限管着，这一条已写进 `internal/agent.md` 第 3 项；**已裁定：不设总量预算**。

38. **第 15 轮审查（时间戳单位 / 公开面的绕过通道 / 拒绝不带码 / 返回形状与断言的牙）**：
    - **一个没写明的时间戳单位会真丢正文**：`ArchiveSlot.CreatedAt` 与 `L4Query.Start/End` 全文一个字没写单位，而保留窗按 `UnixMilli()` 算 cutoff、写边界只拒 `<= 0`——宿主按秒填，写进去的正文在下一次 Dream 被当成过期扫掉，时间过滤还永不命中。门面与两份指南现在写明毫秒，写边界拒掉两段不可能的值（秒级 1e9–1e11、微秒级 >1e14），拒绝时说清各自的后果（`TestAppendArchiveRefusesATimestampInTheWrongUnit`）。这是信任边界的输入校验：一个秒级值今天能通过校验，然后在保留窗里静默消失。
    - **公开面上留了一条绕过通道**：`api.Session` 内嵌的是**导出字段** `*internal.Session`，而 26 个方法早已在门面逐个显式声明，内嵌一个方法也不提升——它只让宿主能 `sess.Session.Search(...)` 拿到 `NewTopicID uint64`、`Scene.TurnSeq`、`Profile.IDHash` 这些从不出门面的量。改成未导出字段（与 `api.DB` 同形），并加一条反射断言把「两个句柄都没有导出字段」钉住（`TestHandlesExposeNoField`）。**破坏性**：写过 `sess.Session.X` 的调用点编译断。
    - **导出 `api.NewError`，工具面的拒绝带上码**：MCP 的六处词表拒绝与 `memhop_archive_get` 的「没有这条」此前经 `errResult` 到客户端时不带码，而「拒绝必须带码」早已是这一面的契约，库内同一判断一律 1003。七处现在用同一个构造器答 `ErrInvalidQuery`/`ErrNotFound`，smoke 逐条钉住 `[1003]`/`[3001]` 前缀。
    - **`DetachGraph` 不再半途而废**：第一条改写失败即返回，已清与未清各留一半，而唯一的调用序是先删图再摘锚（那一序不可反转，理由写在 `internal/l3.go`），重试进不来——剩下的场景永久指着一张已删的图，而 `ListScenes(l3ID)` 不校验存在性照样把它们列出来。改成逐场景收错、走完整个域再 `errors.Join`，与跨域那一份同形。
    - **一条注释与一条测试都在断言一件不存在的事**：`turn.SettleTarget` 的文档声称闸门能拒「已被融合组沉下的那一轮」，而真形态的轮次 id 是 `ComputeTurnTopicID(sceneID, seq)`，闸门**放行**它——放行是有意的（`CreateTurnTopicL2` 从存量记录带回 depth 与 parent，重放因此不会把一轮送回 surface）；`update_test.go` 那条 "sunk child" 用的是时间戳形 id，测的不是真形态，于是这条重放契约至今无人钉。注释改准，用例换成真 turn 形 id，断言 depth、parent、宿主给的名字与关键词轨全都保住（`TestUpdateReplayKeepsASunkTurnSunk`）。
    - **同一判断与同一形状仍有副本**：`db.GetL0` 与 `turn.ReadProfile` 是同一份「读不回 ≠ 不存在」分类的两份副本（一处返回指针、一处返回值），根层改调小方法后取地址；`scene.ResolveForRead` 交回整条 slot，而唯一调用方只读 `.SceneID`、随后开轮把同一条记录重读一遍，收窄成交回 id；`scene.OpenTurn` 是对 `repo.OpenSceneTurn` 的一行转发，且它那句注释是同一事实的第三份副本，删转发、根直接调 repo。
    - **返回形状：同一个「没有」只有一种写法**：门面映射的两处 `slices.Clone` 让 nil 序列化成 `null`，而本包其余列表恒为 `[]`（`TestMappedListsEncodeAsEmptyNotNull`）。同一缺陷在「别名直达宿主、不经映射」的那三个 DTO 上还有一半：`L3ImportResult` 的 `graph_ids`/`errors` 带 `omitempty`，而邻居 `created_ids`/`updated_ids` 恒在，于是一个干净批次答的是「键缺失」而不是 `[]`（`errors` 的非 nil 初值随之补在批次唯一的构造点）；`SceneContextTopic.keywords` 没有 `omitempty`，值却由 `slices.Clone` 供。`messages` 的 `omitempty` **保留**：`memhop_scene_topics` 刻意把它置 nil 以只交话题元数据，那条 `omitempty` 是承重的，去掉就变成 `null`。
    - **api 文档四处失真改准**：`AppendArchive` 的「存下的是哪些字段」漏了 `Role` 与 `ContentType`，而同段又说原文这两者都归宿主；`ArchiveSlot.Role` 被写成封闭三值，而同包 `exports.go` 明说读侧会回 role 3（融合组摘要，刻意不给导出名）；`ErrAgentNotFound` 在公开面不可达（域 id 不越门面，两个生产点只被库内自己的句柄触发），常量保留但两份指南不再把它列为宿主可收到的码；`SurfaceTopics`/`CompareTopicOrder`/`ListTopicsL2` 三处把排序说成「轮次序」，真键是 (`UserTimestamp`, `Depth`, `ID`)——场景的轮次计数器在这条路径上根本没被读，宿主给一批轮次盖同一时间戳时次序退化成按 id。
    - **离线接口面的断言补牙（21 → 24 条）**：把「非空/非零」逐条收成等值。融合组的摘要正文此前**全链路无人读回**，现在两处都对回它，连同它独占的 role 3 与父话题的用户槽；画像此前只断言 `MBTI.Type != ""`，现在钉死由四维导出的 `ESTP`——mock 故意答 `ESFP`，所以一个改成照抄模型那个词的实现会红；关键词轨、L3 三个读口的逐字段、事件轨的 `EventType` 与原文、合并后两轮各自的正文，全部按 id 认领后对回；`SubAgent` 同名两次的「同域」此前只断言句柄非 nil（那条分支不可达），现在断言第二个句柄列得出第一个沉淀的场景、也读得回它写下的原文。另补三条失败注入：模型答非所问时 `Update` 与 `Dream` 各自报错且不落半份记忆（话题记录不建、原文一条不少、没有融合摘要写下来），已取消的 ctx 让 `Dream` 回取消码 5008 而不是模型码 9002。MCP 面把 24 个工具的 `required` 名单逐个钉死（此前改一个入参名整套 smoke 全绿），并要求每个 required 名字都在 `properties` 里声明过。
    - **本轮记下但没动的一件事**：`api.ProfileSlot` 那两个字段类型 `internal.EmotionScore`/`internal.MBTIScore` 没有公开别名——宿主读得到字段、也没法在自己的签名里写出这个类型（`test/` 在模块内，可以直接 import internal，所以这一面照不出这个缺口）。加两个别名是公开面加法，等用户裁定。

39. **第 16 轮审查（融合摘要的时钟 / 写一次的判据 / 报告里两个数 / 工具面的标度与词表）**：
    - **巩固出来的那份摘要一生下来就已过期**：融合组的摘要此前拿组内最后一轮的时间戳当自己的 `CreatedAt`，而保留窗按 `CreatedAt` 扫——巩固一批已经越过 7 天窗口的旧轮次时，摘要在写下的那一刻就比 cutoff 老，下一次 Dream 删掉的正是巩固唯一产出的那段叙述文本，而被它吞掉的那几轮原文也已在同一次清扫里走了，那一段记忆于是只剩一个指不到任何正文的父话题。摘要现在从写下它的那一次流水线起算年龄，组自己的时间跨度仍留在话题记录上（读的人本来就在那里找它）（`TestFusedSummaryOutlivesTheTurnsItFolded`）。
    - **「这个节点蒸馏过了吗」此前拿取值来答，而 0 是合法读数**：情感两轴各 `[0,1]`，`(0,0)` 是「极负面且平静」，与「从没被盖过章」在值上不可分。回灌于是会拿新一轮的蒸馏结果替换一个已经定下来的 `(0,0)`，并顺手刷新节点衰减所依据的那个时钟。节点记录上多一个 `emotion_set` 标记（`omitempty`，旧文件里没有这个键就读作 false，格式版本不动），「写过一次」改由它来判：已盖章的 `(0,0)` 与已盖章的 `(0.9,0.9)` 都原样不动（`TestBackfillL1EmotionsLeavesASettledNodeAlone`）。
    - **一次沉淀可以把别的种类的记录改写成一个话题**：轮次地址上住着一条别的种类的记录时，宽容读答的是 `(nil, nil)`——三个姊妹写口（下沉、融合父查重、L3 的三个 create）都拒这一档，唯独沉淀把它当「没人占着」照写；写出来的话题读回是一个它从不是的类型，而它原本那个种类的列举从此看不见它。现在同样拒，带 `ErrIO` 并点名那个地址（`TestCreateTurnTopicL2RefusesAForeignRecordAtTheTurnAddress`）。
    - **报告里两个数不数它们名字说的那个东西**：`l2_topics_compressed` 数的是**沉进融合组的话题**（此前交回组数，一组吞两轮报 1），`l1_edges_added` 数的是本轮新建**或抬权**的共现边。两个数都是宿主判断巩固效果的唯一读数，名字与值不符就会被读成「只压掉了一轮」；改的是值不是键（键是宿主可见的 JSON 名字），两份指南各写明一次。
    - **引擎自己拒掉的那一组不再被算成「模型没答」**：`applyGroups` 此前把「模型提的组没法应用」与「引擎写失败」折成同一个计数，再由调用方按「全场景失败」报 `ErrLLM`——一次磁盘写失败于是被说成模型没答，宿主去查的是一个从没拒绝过它的端点。现在按码分道：模型那一侧的退化组、成员撞车、算不出时间界、空摘要照旧计数，引擎那一侧原样上抛自己那一档；`Dream` 的「一个场景都没巩固成」因此只在真的是模型那一侧时才报 `ErrLLM`（`TestApplyGroupsRollsBackTheGroupWhenASinkRefuses`）。
    - **域锁内的 LLM 扇出此前没有界**：每场景一个 goroutine、每个 goroutine 一次巩固调用，场景数就是并发数——而这一切都在域锁内：顶住同域其它操作的时长由最慢的那一条决定，端点那一侧同时收到的是整批。收成常量 `compressFanout = 4` 的信号量（先取信号量再 `wg.Add`，反过来计数会先涨上去）。
    - **两处读失败被吞掉，代价记在记忆质量上而不是错误上**：蒸馏取样此前把一条「关键词读不回」的节点当成关键词为空的节点照样排名，于是一份变薄的样本被交给模型去推人格；共现建边同样把读失败当成「这个节点的关键词是空集」，而一条掉到相似度下限以下的边**不会自己长回来**。两处都改成非 `ErrNotFound` 原样上抛、让这一轮停下来（只有 `ErrNotFound` 才是真没了）。
    - **进程关掉之后，一个还连着的会话能把共享库重新打开**：`CloseAll` 把 `db` 置 nil，而 nil 同时也是「还没开过」，于是关闭之后到达的请求走的是同一条开库路径——复活的那份库没人关它，它的 checkpoint 永远不会写下，而进程照旧报告一次干净退出。registry 加一个 `closed` 标记，`get` 与 `OpenShared` 在顶上拒掉（`TestSSECloseAllPersists` 补两条断言）。
    - **工具面交回带标度的信号，却一个字没写标度**：`memhop_l1_nodes` 与 `memhop_profile_get` 把 VAD 与 MBTI 原样交给模型，而这两组的 0 都是合法读数（`0` 是极负面/平静，不是「没测过」；MBTI 某一轴 0 在类型词里是 `X`）。两段描述现在各写明取值区间、中性点、类型词怎么导出、以及「没蒸馏过的域/节点是全零」。
    - **四份手写的词表只有一份被钉住**：`contentTypeNames` 此前是唯一一份与引擎枚举对账的，`kindNames`/`roleNames`/`edgeKindNames` 改一个名字整套 smoke 全绿。收成一条测试、两个方向都对账（引擎定义了而本包说不出名字、本包接受而引擎没定义）；role 3 刻意没有导出名，所以那一份按等值钉死三个键（`TestStatedVocabulariesMatchTheEngine`）。
    - **一处坏 id 说不出是哪个**：`ParseAll` 此前把 `ParseID` 的原因丢掉，宿主只听到一句「invalid id」；现在带上那个字符串与它自己的错误。
    - **净删与收形**：计划层两个原语交回的计数零读者（`WritePlanNode` 恒交回 1，`DeletePlanNodesByIDs` 的条数下一站本来就是 `CollectPlanNodes`），改成只交 error；L3 边的 id 有两份 `Sprintf` 公式，合成 `EdgeKeyL3` 一处；`GetProfileL0` 那条永不可达的 `ErrIO` 兜底删掉（它此前会把引擎自己的 `ErrClosed`/`ErrCorruption` 改写成「读不动」）；`CreateGraphL3` 只有包内读者，转私，对外的建图入口只剩 `EnsureGraphL3`；蒸馏样本上没人读的 `UpdatedAt` 字段删掉。
    - **本轮撤回与驳回的两条**：「蒸馏回包应当要求七个轴全都答到」撤回——`{"emotion":{},"mbti":{}}` 必须能解析、MBTI 只答两轴时类型词导出 `XNFX`，这是被 `llmops_test.go` 与 `cap/agent.md` 第 6 条钉住的既有契约（两块都在而安静是关于情绪的真实回答，四个静默的维度什么都不答，所以不声称类型词），不是缺陷；「取样排名的年龄项在唯一调用路径上是死的」驳回一半——depth>2 的节点不被 `skipDeepNode`/`decayOneNode` 改写 `UpdatedAt`，那一项对它们是活的，删掉的只是那个没人读的字段。
    - **本轮记下但没有动的三件事**：`cmd/memhop-mcp` 跑的是 `MemHopDefaults` 的零值而不是 `DefaultMemHopDefaults`（三条后果已全部写进 `docs/mcp/README.md`：自动巩固不触发、压缩下限 0 同时关掉「话题太少就跳过」并把 prompt 里的目标条数渲染成「往 0 压」、`AgentIdleTTLMs=0` 不回收空闲域）——换成默认值是一次**行为变更**而不是门面变更，等裁定；`memhop_trajectory_read` 的入参名叫 `session_id`，而它要的是 `Search` 带回的轮次话题 id，改名断线上客户端，等裁定；`api.ProfileSlot` 那两个字段类型仍无公开别名（第 38 项已记）。

40. **第 17 轮审查（会做决定的宽容枚举 / 留下来那份开始留痕 / 门面与工具面的失真）**：
    - **同一份宽容被用在了两种清单上**：`CollectAll*` 跳过读不回的记录，这对「可以从幸存者重建」的清单是对的，而本轮查出七处用它的地方并不重建清单、而是**拿这份集合做决定**。L3 的四个读口（`GetL3`/`QueryL3Nodes`/`QueryL3Subgraph`/`ListL3`）交回的是「这张图有什么」，其中子图那份邻接表还决定 BFS 能走到谁——少一个成员不是少一条边，是那个成员从此不可达；L1 的三处按域枚举（建边、陈旧重建、衰减）里，衰减是唯一会移除节点的通道，被扫描跳过的节点永远不会淡出，而建边按这份集合决定谁与谁相关。七处全部改走 `core.CollectAllStrict`：读不回即带着它自己那一档码停下这一轮，并点名那个 id（引擎没有任何读面能指出「哪条坏了」）（`TestL1PassesRefuseANodeTheyCannotRead`、`TestL3ReadsRefuseARecordTheyCannotDecode`）。变异证据：把严格那份改回跳过，六条断言全红，且衰减顺手把一个活节点的 `Importance` 抹成 0。
    - **三份宽容的孪生实现随之净删**：宽容版 `CollectAllGraphSlots`/`CollectAllHypergraphNodes`/`CollectAllHypergraphEdges` 删除，严格版 `CollectAllGraphSlotsStrict` 去掉后缀成为 `CollectAllGraphSlots`——**这是一处重命名**，调用方（`internal/graph`、`internal/l3.go`）同批改完。
    - **留下来的那份宽容现在开始留痕**：`IterAll` 每丢一条记录就记一行 WARN 并点名 id（索引尚未跟上的那个合法空洞不记），错误文本也带上形状（`unmarshal core.ArchiveSlot:` 而不是 `unmarshal :`）。`BuildL4FromEngine` 的理由改准：撕裂尾帧根本走不到这次扫描——能进索引的帧都已在开库时过了 CRC；一次丢弃真正的代价是 `MaxSeq` 看不见的那个 Seq，于是这个话题的下一次 append 会盖掉那条记录。`profile.Samples` 是**刻意保留**的宽容，`ponytail:` 注明理由：它排在那三处 L1 枚举之后、同一次持锁之内，坏节点早已让本轮停下，在这里再拒一次只是同一份拒绝的第二份副本。
    - **陈旧重建带走的边此前不进报告**：`RebuildFromL2` 现在把它连同移除的节点一起交回，`l1_edges_removed` 因此如实覆盖两个会移除的阶段（`TestRebuildFromL2CountsTheEdgeItTakesWithIt`）。
    - **一个没写过偏好的画像此前序列化成 `"preferences":null`**：门面交回的其余每一份集合恒为 `[]`/`{}`，`fromProfileSlot` 现在把 nil 归一成空 map（`TestMappedListsEncodeAsEmptyNotNull` 加一例）。
    - **`Personality` 有两个写者，而全部文档都说它归宿主**（已裁定「都写」）：宿主的 `UpdateL0` 与 Dream 的 `MergeDistill` 都写它。本轮把 `api/types.go`、`api/session.go`、`internal/l0.go`、两份接入指南与 `AGENTS.md` 改成陈述这件事，而不是把其中一个写者藏起来。
    - **门面与工具面另有十处失真改准**：`api/open.go` 仍说门面靠内嵌提升方法（那个字段第 15 轮已转私、26 个方法全部显式声明）；`DeleteTopic` 补上 L1 的滞后（节点的 `TopicIDs` 是上一次同步的快照，删掉的话题要等下一次 Dream 才从里面消失，期间按它去读答 `ErrNotFound`）；`MergeScenes` 补上被并掉那个场景的 L1 节点随它一起走、指名它的超边留给边衰减（与 `DeleteScene` 同形）；`SceneNodeView.TopicIDs` 与 `HypergraphEdge.IDHash` 各补一句定义（后者没有任何方法收它：边只由 `ImportL3` 建、只随 `DeleteL3` 删，没有哪个阶段改写或衰减它）；工具面上 `memhop_scene_topics` 的 depth/child_count 规则、`memhop_archive_get` 也会交回事件（并复述词表与 `[3001]`）、`memhop_knowledge_get` 的数字 kind 词表、`memhop_status` 的两个数不同域（`closed` 是整份共享文件的、`scene_count` 只数本租户）各改准；`memhop_knowledge_import` 不再声称 `content` 必填——整批预校验只拒缺 title 或 domain，那条嵌套 required 清单同批被一条新断言钉住。
    - **第 39 项记下的三件待裁定事项原样留着**（`cmd/memhop-mcp` 跑的零值 `MemHopDefaults`、`memhop_trajectory_read` 的入参名、`api.ProfileSlot` 那两个没有公开别名的字段类型）。

41. **字段面逐层审计（磁盘只存源事实 / 读侧时间戳写明单位 / L1 数值链统一精度）**：
    - **`MBTI.Type` 此前是磁盘上的派生冗余**：类型词恒等于四轴的函数（唯一的写入点本就写明「绝不采信 LLM、从轴重推」），却与轴并排落盘——一份事实存两份，一致只靠唯一的写路径恰好记得派生。现在 `type` 键不再落盘（`json:"-"`），`ReadProfileSlot` 解码后从轴重派生，派生函数从 `llmops` 下沉为 `core.DeriveMBTIType`；旧文件里的 `type` 键解码时跳过，读回与轴一致，公开形状不变（`TestReadProfileSlotDerivesMBTITypeFromAxes` 钉住「盘上字面与轴矛盾时以轴为准」，`TestProfileSlotRoundtrip` 改钉磁盘无 `type` 键；两处自带漂移的夹具——轴推 ESFP、字面写 INTJ/INTP——改成自洽）。
    - **`ProfileSlot.IDHash` 删除**：画像是固定地址 `hash("profile")` 的单例，payload 里那份 id 全仓零读者（其余记录的 IDHash 都有真实读者：映射输出、L4 索引镜像、衰减回指）；旧文件多出的键解码时跳过，格式版本不动。
    - **L1 数值链统一 float64**：`Importance`/`Weight` 此前是 float32，而情感与 MBTI 信号全 float64——同属巩固/蒸馏算出的信号，精度选择没有语义依据，且衰减是连乘，float32 的舍入误差随轮次累积。记录字段、`DecayParams` 阈值、dream 调参常量、jaccard 与建边权重、蒸馏样本同批统一；公开面随之变化一处：`api.SceneNodeView.Importance` 变 float64。
    - **读侧时间戳的单位写明毫秒**：写侧那轮（`ArchiveSlot.CreatedAt` 与 `L4Query.Start/End` 写明毫秒并在写边界拒错单位）只覆盖了 L4；同样毫秒落盘、同样出公开面的 `TopicSlot.UserTimestamp/AgentTimestamp`、`SceneNodeView` 两个时间戳、L3 三个类型的 `CreatedAt/UpdatedAt`、`PlanNodeView` 三个时间戳与 `SceneMessage.CreatedAt` 补同一句话——宿主拿它们与自己的秒级时钟比较，是同一类事故的反方向。

## v1.6.3 — 2026-09-10 — L4 是一轮唯一的内容层，L5 只剩计划树，`Update` 只蒸馏

一轮发生过什么，此前被劈在两层：L4 存两条对话原文，轨迹层存事件与计划节点。两层早就共用
同一个键（`Search` 为这一轮铸出的话题 id），却仍是两种记录、两套扫描、两条生命周期，并且 L2
话题要靠一份 `L4Refs` 第二真相指向自己的原文。本版本把它们收敛成一个判据：**内容是内容，
计划是计划**——凡「一轮里的内容」（说了什么 + 做了什么）同住 L4、以 (话题, `Kind`, `Seq`)
寻址；计划层只剩每个话题一棵计划树（该层在本版本内由 L6 改号为 L5）；话题不再持有内容引用。

### 记录与格式（该两轮 `FormatVersion 0x000D → 0x000F`；本版本最终为 `0x0011`，见下文计划层一节）

- `ArchiveSlot` 吸收事件侧字段：`Kind`（`KindUtterance` / `KindEvent`）、`Seq`、`EventType`、
  `NodeSeq`。归档 id 从「正文哈希」改为**位置式** `hash("content:"+话题+":"+seq)`，于是同 (话题, Seq)
  重写就是原地覆写——重放一轮能收敛，靠的是这个键，不再靠「先列出旧的、再给没重写到的打墓碑」
  那套差分（连同它需要的第二份清单一起删除）。
- 计划树的新记录类型 `core.PlanNode`（帧值 `0x0F`，随能力记录层退役空出来的号）：一节点
  一条，字段是 `TopicID` + 轮内步骤序号 `Seq` + `ParentSeq` + `Status` + `Title`/`Summary` +
  `CreatedAt`/`UpdatedAt`/`FinishedAt`（这里的 `Seq` 是**库发号的步骤序号**，与内容槽位那个
  `Seq` 不是一个东西）。`TrajectorySlot` / `NodeType*` / `PlanNodeRef` 消失。
- `0x000D` 与 `0x000E` 都在 `Open` 显式拒绝、无迁移：前者把事件存在一个已不存在的记录类型里、
  把归档按正文哈希发号；后者的归档把归属话题记在 `context_id` 键下、记录按 `l1:` / `l4:` 前缀
  派生——按最终规则两条都指不到东西（`0x000E` 是本版本开发序列中的中间版，从未随 tag 发布）。
- `index/traj.go` 就地改造为 `index/l4.go` 的 `L4Index`，**条目带 `Kind`**——否则「哪些轮记了事件」
  会把只有对话的轮也报出来，而场景读回会为一轮两句话读出几十条事件。
- Dream 的清理拆成 `l4_prune`（按内容时间戳）与 `l5_prune`（按计划节点 `UpdatedAt`，仍豁免在途
  树）；跨层级联删除整段退役——树与事件既然分居两层，节点过期就不该带走正文。
  `TrajectoryRetention` 更名 `ContentRetention`，值仍是 7 天，两侧共用。

### 写路径换主：`Update` 不再生产内容

- **`AppendArchive(topicID, ArchiveSlot)` 是一条记录进入话题的唯一途径**。`content.ValidateAppend`
  集中全部宿主不可信字段：`Kind` 必须已定义、`Role` 只能是 user/agent/system（值 3 是库给融合摘要
  自己盖的标记，拒）、`ContentType` 必须已定义、`EventType` 非空 **iff** 事件、`NodeSeq` 仅事件侧、
  超预算拒写不截断（事件 4 KiB、原文 64 KiB——原文上限的理由是分片提炼会把锁内 LLM 调用数推到
  无界）。校验严格排在任何写入之前。
- `Seq` 是一个话题内跨 Kind 共享的单一空间：`0` 自动分配且从不说谎地跳过 1/2（那两个位置属于
  对话）；显式命名的槽位被占用即覆写，跨 Kind 也覆写。
- **`Update(sceneID, topicID)` 只蒸馏**：读该话题的全部 `Kind=utterance` 记录、按 `Seq` 序渲染成带
  说话者标签的转录、一次 `llmops.ExtractKeywords` 出关键词轨。一条内容都没读到就
  `ErrInvalidQuery` 且不碰 LLM；提炼失败时宿主先前 append 的内容原样留着（回收它就要再记一份
  「本轮写了哪几条」，那是被本版本明确否掉的第二真相）。
- 删除：`TurnUpdate`（含 `core` 侧 DTO）、`turn.WriteArchives`、`llmops.ExtractTurnKeywords`
  （`Update` 与 Dream 从此共用同一个 `ExtractKeywords` 入口）。

### 公开面收敛：`api.Session` 26 → 25

- 删 `AppendTrajectory`（能力并入 `AppendArchive`）、删 `ReadTrajectory`
  （`SearchL4{TopicID, Kind}` 完全覆盖，与当年删 `GetArchive` 同判据）、**新增** `AppendArchive`；
  `Update` 改为两参且不再回 id（话题 id 是 `Search` 给的）。`MultiAgentDB` 8 个不变。
- DTO：删 `api.TrajectorySlot`、`TurnUpdate`；`ArchiveSlot` 升为写读两用（写侧忽略 `IDHash`/
  `TopicID`，所以「读回来改一句写回原槽」天然成立）；`L4Query` 加 `Kind` 条件、
  `SceneMessage` 加 `Seq`（空洞由此可判别）；计划写面的事件入参换 `ArchiveSlot`（该写面在下文重排为
  逐节点入口）；
  `TrajectorySessionSummary.Steps` 改名 `Events`（它统计的一直只是事件）。
- 常量：补 `KindUtterance`/`KindEvent`/`RoleSystem`，**收回 `RoleDream`**。
- 场景读回的完整性判据改写：「索引点名、记录读不到」= 镜像漂移，仍硬 `ErrIO`；
  「话题在、内容为空或有洞」= 合法的过期终局，不报错。
- `internal/trajectory` 包更名 `internal/content`——它服务的不再只是轨迹。

### MCP（24 个工具不变，名字变）

- `memhop_trajectory_append` → **`memhop_archive_append`**（`kind` 缺省 utterance、原文必须说
  话者、`role=dream` 在边界拒）；`memhop_update` 入参收缩到 `{scene_id, topic_id}`；
  `memhop_trajectory_read` 留作便捷工具、实现改走 `SearchL4{TopicID, Kind:event}`
  （Go 面删、MCP 面留，是 `AGENTS.md` 已确立的对称）；`memhop_archive_search` 补 `kind` 过滤。

### 已接受的能力回退（不是 bug）

- 超过 7 天的轮次与融合话题**正文永久不可回读**，只剩关键词轨——依据是 Dream 的融合链从不
  回读 L4 原文，关键词轨本来就是唯一长寿产物。融合摘要因此降为 7 天寿命的中间产物。
- `test/core_cycle_test.go` 的「细节保留」语义从「原文长寿」变成「当轮细节保留」。
- 宿主热路径的一轮从 1 次调用变成 3 次（`AppendArchive` ×2 + `Update`）。

### 字段与层号收敛（同版本内的第二轮）

- **Dream 的 usage-feedback 阶段退役**：它读的是场景记录上的 `HitCount`/`LastHitAt`，判定只是给
  L1 节点 `Importance` ±0.05——在 `nodeRemoveThreshold` 0.05、λ 0.01/h 之下这是 5% 量程的慢游走，
  左右不了节点存活。L1 重要性此后只有两个来源：同步新建时的 `1.0` 与时间衰减。代价说明白：
  引擎不再能分辨「被读过」与「最近有写入」。
- **场景记录删三键**：`hit_count`、`last_hit_at` 随上述消费者消失；`topic_count` 从来没落过盘
  （`NewSceneSlot` 不置它，读改写路径回写的也是刚读到的那条），每次都是读后现算——因此
  `CollectAllScenesL2` 里「全扫话题现算每场景根数」那段填充与 `Search` 的回填一起删除。
  `api.SceneSlot` 同步收窄为 `{scene_id, scene_name, l3_id}`。`SceneContext.TopicCount` 是本次
  返回的条目数，语义真实，保留。
- **`ArchiveSlot.ContextID` → `TopicID`**（Go 字段与 JSON tag 同时）：它存的一直是拥有这条记录的
  那个轮次话题，而 `context` 在引擎里已无对应概念。L3 的 `HypergraphSource.ContextID` 是另一件
  事（`SourceContext` 的语境 id），未改名。
- **id 命名空间去层号**：`l1:` → `scene-node:`、`l4:` → `content:`；`turn:` / `plan:` / `profile`
  与 L3 的两个派生式本就不含层号。前缀担的是防碰撞，把层号烘进主键意味着下次改层号要重算全部
  记录 id。
- **L6 → L5 改号**：能力记录层退役后 L5 号位空着，而引擎真实记录的这一层仍叫 L6——认知栈由七层
  （L0–L6）收敛为六层（L0–L5）。`RecL6PlanNode` → `RecL5PlanNode`（帧值仍 `0x0F`）、
  `l6_prune` → `l5_prune`、`internal/l6.go` → `l5.go`、`repo/l6layer.go` → `l5layer.go`；payload
  字段一概未动。撞词一并清掉：结晶的 prompt 文本与「L5 能力」这类标题都不再带层号——能力不是
  引擎的一层。
- **`L4Query` 新增步骤归因条件**：事件的归因此前只能靠宿主把整轮拉回后自筛。填了 `NodeSeq` 却没给
  `TopicID` 直接 `ErrInvalidQuery`（步骤是轮次内的地址，缺话题这一读会退化成全域扫）；序号从 1 起
  发号，`0` 就是不加这条约束，因此也不存在「形状非法」这种输入要解析。Go 面最终 **27 + 8**，
  MCP 仍 24（`L4Query` 是别名链，MCP 侧只多一个入参属性）。

### 计划层重做：一轮一棵树，按步骤逐个写

v1.6.2 唯一的树写面 `PlanCommit` 一次只走一步，并且**强制绑一条事件**（事件侧 `Content` 与
`EventType` 都非空才过闸）。于是第 N 轮要把计划重述成四步加三个子步，宿主得发七次调用，还得给
早已完成的步骤**各编一条假事件**才能把 `done` 落进这一轮的树——计划没法被重述，只能被追加。

- **写面是三个逐节点入口**：`api.Session.PlanCreate(topicID, title) → seq` 开一棵树、
  `PlanNodeAdd(topicID, parentSeq, title) → seq` 加一步（`parentSeq` 为 0 即再加一个根）、
  `PlanNodeUpdate(topicID, PlanStep{Seq, Status, Title, Summary})` 重述一步。Go 面 **25 → 27**。
  中途曾试过「一次声明整棵 + 点号路径寻址」，最终形态把它换掉了：改一步只发一次调用，不必修正
  整棵树的文本。
- **一步由轮内序号寻址**：`Seq` 是该轮内库顺序发号的整数（从 1 起），`ParentSeq` 指它挂在谁下面、
  0 即根，记录 id 由 `hash("plan:"+话题+":"+序号)` 派生。序号是一轮之内的**地址**而不是记录 id，
  所以它以一个普通整数越过门面，`TestPublicSignaturesCarryNoNumericIds` 不需要为此开口子。
  `PlanNodeView` 随之以 `Seq`/`ParentSeq` 呈现嵌套树，`Roots` 仍是森林（父记录已失效的节点上浮为
  根，不让一棵活树被死父藏住）。
- **不做「树上没有这一步即删除」**：`6c1aa7d` 退役 `SyncPlanTree` 时那条理由仍然成立——部分重述与
  完整重述在库这边长得一模一样，猜错就是静默删掉宿主的一步。也不开节点删除口：放弃一步的手段就是
  不在此后的轮里再创建它，旧树由 `l5_prune` 的保留窗回收，这与 L4 刚定过的「重放不再去填的槽位
  不回收」同一个姿态。
- **节点只由创建口建立**：`AppendArchive` 不建步骤，事件绑到一个树上没有的序号即 `ErrInvalidQuery`
  且记录与节点都不留；`PlanNodeAdd` 的父序号不在树上即 `ErrNotFound` 而不是补出一条链。判据是产品
  语义本身——步骤按计划执行要求计划先于步骤存在；一步的父是谁只有宿主知道，库替它猜就会长出一枝
  没人计划过的树。副产品是关掉了「打错一段路径凭空多出一棵树」这个 v1.6.2 自认的已知代价。
- **读侧一步取其子树**（`PlanCache.Subtree` 沿 `ParentSeq` 求闭包）：一步拆成子步之后它做过的事在
  孩子身上，只匹配这一步 own 的那条线是个部分答案。代价：拿不到「只属于这一步、不含子步」那个切面。
- **计划节点去 `plan_type`**：三个库定值 `plan/step/tool_call` 无任何读者——`Status` 担生命周期、
  `Title`/`Summary` 担语义，「调了哪个工具」本来就记在该步事件的 `EventType` 上。与退役
  `planEventTypes` 名单同一条论证。
- **状态词表收成三个**：`in_progress` / `done` / `failed`。`running` 与 `in_progress` 同义而引擎
  分辨不出差别；`pending` 一起去掉——节点只能被创建出来，而创建出来的那一步就是在做了，所以
  `in_progress` 占存储值 0、新建节点零值即合法状态，创建口也因此不要宿主给状态。仍**不加状态迁移
  表**：状态是宿主声明的事实，库不裁决它能不能跳。
- **折叠判据收紧**：父节点只在自身 `done`、自身 `Summary` 为空**且全部直接子到达终态**时才折
  （此前有一个 done 子就拼）。半成品拼出来的摘要读起来与成品无异，而父节点上没有任何东西说明
  第三个子当时还开着。
- **步骤被重开时清 `FinishedAt`**：原先钉的是「首次完成时间永不改写」，但那会让一条自称
  `in_progress` 的记录带着完成时间回给读者。摘要文本由宿主在重述时带；**L5 不调 LLM**——
  节点完成与 `Update` 那一轮的话题蒸馏是两件事，压缩总结的归属仍在 `Update`。
- **一个已知上限，写在代码里**：序号从现存节点的最大值 +1 发，因此保留窗裁掉最高号之后该号会被
  复用。缓解依据是计划节点与其事件共用同一个 7 天窗口；`PlanCache.NextSeq` 处以 `ponytail:` 注明
  上限与升级路径（改为持久化的每话题高水位）。
- 格式版本最终 `0x0011`：一个步骤的寻址从路径串换成序号，状态字节重排（旧表里 `0` 是 pending、
  `2` 是 done），事件归因字段随之改名 `node_seq`——三者都是解码层面对不上的，故 `0x0010` 及更早
  拒绝打开、无迁移。

### 对宿主的破坏性变更（跟版清单）

`TurnUpdate` / `api.TrajectorySlot` / `AppendTrajectory` / `ReadTrajectory` 四个符号消失；
`Update` 签名与返回值变更；`TrajectorySessionSummary.Steps` → `Events`；`api.RoleDream` 不再导出；
`api.SceneSlot` 去掉 `topic_count`/`hit_count`/`last_hit_at` 三字段；
`api.ArchiveSlot.ContextID` 改名 `TopicID`（Go 字段与 JSON 键同时变）；`DreamStage` 词表
去 `usage_feedback`、`l6_prune` 改号 `l5_prune`；**计划写面重排**——`PlanCommit` 与本版本中途试过的
`PlanSet` 都不在了，换 `PlanCreate` / `PlanNodeAdd` / `PlanNodeUpdate`（Go 面 25 → 27）；
`api.PlanStep` 去 `Type` 与 `NodePath`、改带 `Seq`，`api.PlanNodeView` 去 `Type`、以
`Seq`/`ParentSeq` 寻址，`api.PlanStatusPending` 与 `api.PlanStatusRunning` 常量退役（词表三个）；
事件记录的步骤归因字段 `NodePath`(string) → `NodeSeq`(uint32)，MCP
`memhop_archive_append` / `memhop_archive_search` 的 `node_path` 参数随之为 `node_seq`（工具数仍 24，
计划写面仍只在 Go 侧，纯 MCP 宿主用不了这个参数）；格式版本最终为 `0x0011`——`0x0010` 及更早的
`.meh` 拒绝打开（无迁移）。

### 测试与档案

- 新增派生式 id 命名空间的穷举互斥测试（`turn:` / `content:` / `plan:` / `scene-node:` + Dream 的
  无前缀融合键，同一输入空间两两不撞）；`0x000D` 与 `0x000E` 都在版本拒绝列表内，且做过
  「旧版本被拒」断言真红一次的负例证明。
- 第二轮新增：`TestSceneContextOpensNoTurn`（「SceneContext 不开轮次」此前钉在宿主可见的计数字段
  上，该字段消失后搬到能读到 `TurnSeq` 的地方，并断言下一轮拿到的正是紧邻的那个 id）、
  `TestSearchL4ByNodeSeq` 与 `TestNodeSeqFilterNeedsTopicID`（后者兼作「漏带话题就退化成全域扫」
  的守卫；前者的谓词做过变异检验——摘掉即变红）、`TestOpenSceneTurnAdvancesTurnSeq`；
  两个只为读侧计数存在的用例随之删除。
- `Update` 侧的落盘断言方向反转（失败不再要求零内容留痕），新增「内容为空 ⇒ 零 LLM 调用」、
  `ValidateAppend` 逐条拒因、跨 Kind 的 Seq 覆写、`RenderForDistill` 的角色标签、
  过期转录读回为空且不报错的端到端一条。
- 计划层最终形态新增/改写：`TestPlanCreateHandsOutOrdinals`（首步是 1、逐节点连续发号、两轮各自
  从 1 起）、`TestPlanNodeCreateStampsTimes`（新建即 `in_progress`，`CreatedAt` 只戳一次）、
  `TestPlanNodeUpdateLeavesOtherStepsAlone`（**没有这条，逐节点写面就退回到「改一步要重述整棵」**）、
  `TestPlanWritesRefuseWithoutLeavingTrace`（未知父序号 / 未知序号 / 未知状态 / 空状态四种拒因各钉
  「树上零留痕」）、`TestEventBindsOnlyToACreatedStep`、`TestStepReadCoversItsSubtree`（父/子/叶三层
  各查一遍，并钉住未归因记录不进任何步骤的读）、`TestPlanStateOrphansSurfaceAsRoots`、
  `TestPlanRollupModelA`、`TestPlanRollupWaitsForEveryChild`、`TestPlanNodeUpdateFinishedAt`；
  缓存侧 `TestPlanCacheHasSeqIsTurnScoped` / `TestPlanCacheSubtreeWalksParentLinks` /
  `TestPlanCacheNextSeq`，数据层 `TestWritePlanNodeRejectsZeroSeq`（0 不是可寻址的步骤），
  门面 `TestPlanWritesRejectedLeaveTreeUntouched`，离线接口面
  `TestInterfacePlanTreeLivesOnItsTurn`（含「别的轮的序号在这一轮不存在」）、
  `TestInterfacePlanTreesStayPerTurn`、`TestInterfacePlanAndTrajectorySurviveReopen`（重开后树按原
  序号读回、新步接着发号）。随点号路径一起删除的判据：同前缀兄弟不被误伤（整数序号无前缀形状）、
  「未列出的节点不动」（不再有整棵声明）、以及建链时顺手补 pending 父节点的一组用例。
- 版本拒绝用例的断言加牙并做过变异检验：逐条核对错误消息里的实际版本与期望版本，把
  `FormatVersion` 临时改回 `0x000F` 时 `0x000f` 子例立刻变红——只判「消息里有 version 字样」的话，
  一个常量漏改会静默通过。
- **`test/` 里的 `api_interface_*` 那一批走包内 mock LLM，可本机离线跑**
  （`go test -tags integration ./test/ -run TestInterface` → 21 条）；要真额度的只有
  `core_cycle` / `e2e_flow` / `fidelity` / `keyword_extraction_e2e` / `benchmark`。本轮正是跑这一批
  才发现 `TestInterfaceTurnContentSharesOneKey` 的前提（事件绑到一个还不存在的步骤）已被推翻。
- 决策档案第三轮：`notes/implemented/architecture/2026-09-10-plan-tree-declared-per-turn.md`；
  `notes/rejected/architecture/2026-09-09-plan-whole-tree-record-and-cross-layer-cascade.md` 里
  「提交即追加胜出」与「纠正手段是作废整轮」两条按本轮现状改写（整树一记录那条否决仍然成立）。
- 决策档案（2026-09-09/10）：`notes/implemented/architecture/2026-09-09-l4-content-layer-and-plan-only-l6.md`、
  `notes/implemented/simplification/2026-09-09-addressing-content-by-topic-and-kind.md`、
  `notes/implemented/simplification/2026-09-09-l4-seven-day-retention-and-transcript-completeness.md`、
  `notes/implemented/simplification/2026-09-09-update-distills-its-own-topic.md`、
  `notes/implemented/architecture/2026-09-09-turn-topic-id-is-the-only-join-key.md`、
  `notes/rejected/architecture/2026-09-09-plan-whole-tree-record-and-cross-layer-cascade.md`。

## v1.6.2 — 2026-09-07 — 计划事件不再受词表约束（`EventType` 归宿主）

`internal/plan` 的 `ValidateEvent` 包装与它背后的 10 词 `planEventTypes` 名单一并删除：计划绑定事件的 `EventType` 与裸轮次事件同口径——任意非空宿主命名即接受，原样存回。理由是这条约束不挣自己的饭钱：引擎从不按 `EventType` 分支，全仓非测试引用只有 `ReadTrajectory` 的字段回显与结晶 prompt 里的一行格式化，所以名单唯一的行为就是拒写；代价全落在宿主侧——自己的事件名被拒后轨迹静默少一条，而库并没有因此保住任何结构（写入路径自己决定记录形状并清零全部节点字段，宿主伪装不了树视图）。

- **校验点只有一处**：内容写入的 `content.ValidateAppend`（非空 `EventType` + `CreatedAt` > 0 + payload ≤ 4 KiB）。追加与 `PlanCommit` 都在 `EnsureNode` 之前调用它，被拒的写零留痕；v1.6.1 修掉的那条「校验晚于改树」顺序不变量原样保留
- **MCP 面无改动**：`memhop_trajectory_append` 的描述本就写着 `event_type` 由宿主自定，而计划写面（`PlanCommit`/`PlanState`）不在 MCP 工具面上。这轮是把 Go 面对齐到交付面已有的口径，24 个工具不变
- **本改动不增删方法、不触及记录布局**：`event_type` 是记录内的 JSON 字符串字段，收紧与放宽都不改变帧结构
- **对宿主是放宽方向**：原本被拒的写入现在成功，无需宿主改调用点即可受益；meowagent 的沙箱裁决反问（`sandbox_ask`）由此可直接入计划轨迹
- **测试**：`internal/l6_test.go` 的 `TestPlanEventVocabularyRejectsUnknown` 改写为 `TestPlanEventNamesAreHostOwned`（钉住宿主命名被接受、名字原样回读、被拒仍不建节点链）；`api` 面两处拒写断言改钉空 `EventType`（`surface_l6_test.go`、`surface_closed_loop_test.go`）；`test/api_interface_plan_test.go` 的「被拒不改动树」例子改用缺 `EventType` 的事件
- **文档同步**：`api/session.go` 的 `AppendTrajectory` / `PlanCommit` 注释、`INTEGRATION_GUIDE.md` 与 `.zh.md` 的 L6 计划面表格、`internal/plan/agent.md`
- 决策档案：`notes/implemented/simplification/2026-09-07-plan-event-vocabulary-retirement.md`

## v1.6.1 — 2026-09-06 — 公开面收敛（34→27）、内置说明书卡删除、公开面按使用者分两类、L5 记录层退役（目录即能力）

### 两个折叠（34→32）

- **`ActivateCapability` 删除**：repo 层的激活是读卡→置 active→写回，与 `UpdateCapability(id, CapabilityPatch{Status: &CapabilityActive})` 完全同形——同一能力只留一个实现；激活并入 `UpdateCapability`（同过卡片校验，缺 summary/resources 的裸夹具即拒）
- **`PlanReplace` 删除**：`SyncPlanTree(planID, nil)` 清整树（节点与绑定事件全删、planID 保留、事件 Seq 从 1 重排）；下一任务的播种 = 单节点同步（空 status 落 pending）；`0000000000000000` 拒绝语义不变（ParsePlanID 在 nil 判断之前）
- MCP 工具 31 → 30：`memhop_capability_activate` 并入 `memhop_capability_update` 的 `status` 参数

### 内置说明书卡删除（对标主流能力面设计）

**动机**：对主流 agent 能力面设计（Claude Code / OpenClaw 的 skill、MCP 工具面、Manus 的上下文工程）调研的结论是——能力池只装 **LLM 可触发单元**（工具 schema / skill 文件 / 动作链），说明书按需检索不驻留，渐进披露（一行描述常驻 + 正文按需）。引擎内置卡（9 张覆盖 32 个库方法用法，约 28KB）是「宿主方法说明常驻能力池」——主流生态无先例的第三种形态：宿主要么整体 skip（meowagent），要么与 MCP 工具描述双份重复；10 个纯管理方法还混进了 LLM 视野。

- **删除**：`internal.BuiltinCards()` 代码组装、`SetBuiltinCapabilities`/`findBuiltinCapability`/`builtinMatchingList` 装配与合并链、ListCapabilities 的 stored-shadow 去重段、结晶折回的保留名检查（`trajectory.ApplyCandidate` 删 `reserved` 谓词参数）、`CapabilityOriginBuiltin` 常量（core→internal→api 别名链一并删）
- **能力池回归本意**：池里只有宿主导入的卡、结晶草稿与 `plug/` 注入；空库 `ListCapabilities` 返回 0 条；方法用法说明归 `go doc api.Session` 与 `INTEGRATION_GUIDE.md`（即主流的「文档检索通道」角色）
- **对消费方 breaking**：meowagent 的 `Origin == builtin` skip 成死代码、`contracts.CapabilityOriginBuiltin` 编译断——归属主自己的适配轮次；`plug/` 自动注入与 `Origin` 枚举（imported/crystallized/host）未随本版本存活——连同整个 L5 记录层在下方「L5 记录层退役（目录即能力）」小节移除

### 公开面按使用者分两类（注释 + 文档层，代码零结构变化）

- **任务面（20 个）**：宿主每轮驱动（`Search`/`Update`/`Dream`/`AppendTrajectory`）+ LLM 工具绑定的全部读写
- **组装/管理面（7 个 + DB 8 个）**：会话边界与管理通道调用，不做成 LLM 工具（`UpdateScene`/`MergeScenes`/`DeleteScene`/`DeleteTopic`/`UpdateL3`/`DeleteL3`/`DeleteL3Nodes`）——能力方法 5 个随 L5 记录层退役移除（见下节）
- 落点：`api/session.go` 头注释总表（go doc 可见）、`api/surface_public_test.go` want 列表分组钉住、`INTEGRATION_GUIDE.md` §8 两类速查

格式在本版本内最终落到 `0x000C`（`0x0F` 帧型随 L5 记录层退役，`0x000B` 及更早文件 Open 时显式拒绝）；决策档案 `notes/implemented/architecture/2026-09-06-remove-builtin-cards.md`（其前身的 embed→代码组装决策同日整体被取代，档案移入 `notes/rejected/`）与 `notes/implemented/architecture/2026-09-06-l5-record-layer-retirement.md`。

### L5 记录层退役（目录即能力）

**动机**：v1.6.1 时点 L5 池里存的已经是 plug/ 文件的副本——`FileHash` 水位、重注入零写入、patch 幸存、状态补丁全部为「文件 + 库双事实源同步」服务，而目录里的文件本身从未失去意义。双事实源的同步成本买不来库侧增值：把唯一事实源归还宿主目录，库只保留它真正的两块纯能力——v4 格式与纯提炼。

- **目录即能力**：能力卡的唯一事实源 = 宿主自有的 `plug/<包>/capability.json` 目录；宿主自扫自装配、变更重启生效；结晶草稿落 `plug/draft/`，activate = 文件转正。引擎不再存储任何能力记录，Open 也不做任何目录扫描/注入
- **删除**：五个会话方法（`ImportCapability`/`UpdateCapability`/`DeleteCapability`/`ListCapabilities`/`RecordCapabilityUsage`，公开面 32→27：任务面 22→20、管理面 10→7）、八个类型（`Capability`/`CapabilityImportResult`/`CapabilityListQuery`/`CapabilityPatch`/`CrystallizeResult`/`CrystallizeDetail` 与 `CapabilityStatus`/`CapabilityOrigin` 及其常量）、`internal/l5.go`/`l5plug.go`（Open 时注入、`FileHash` 水位、重注入零写入、patch 幸存）、`repo/l5layer.go` 与 core 记录层（`RecL5Capability`、typed 读写器、`CollectAllCapabilities`、`0x0F` 帧型）；MCP 六工具（含便捷工具 `memhop_capability_get`）与 `--capability-dir`/`MEMHOP_CAPABILITY_DIR` 随之删除，工具面 30 → 24
- **保留并转为纯能力**：`capability` 包成为 v4 磁盘格式的唯一事实源，卡片文档类型（`CapabilityImport`/`CapabilityPackageDoc`/`ResourceRef`）迁入该包，经 api 导出包级函数 `CapabilityFormatV4` + `ParseCapabilityPackage(data, source)`（内走整包校验）+ `ValidateCapabilityCard(card)`——不加 api→cap 直连边，沿用恒等别名模式
- **`Crystallize(ctx, turnID, existing)` 转纯提炼**：入参轨迹轮 id + 宿主现有卡清单（`[]CapabilityImport`），出参即候选列表（`CrystallizeOutput.Capabilities`：action=create|reuse|merge + reuse_id + 卡载荷）；删掉 ActiveOnly 读取与折回落库循环，`trajectory.ApplyCandidate`/`applyCrystallized`/`findTarget` 整链移除；ReadTurn/TrimByBudget 链原样保留（128KB 预算留在引擎内、锁内 LLM 契约不变）。**reuse_id 语义变更：从 16-hex 记录 id 改为已有卡名**（卡名是 v4 文档的唯一性保证、宿主目录可寻址身份），systemCrystallize 提示词同步改写；未过卡级校验的候选原样返回，过滤职责移交宿主。MCP `memhop_crystallize` 传 `existing=nil` 并在描述中声明「引擎不落盘、去重落盘归宿主」
- **PromptCard 迁入 capability 包**：删去宿主无法复刻的 `id:`（hash 派生）/`package:`（目录名复述）/`usage:`（用量统计失去写入方）三行渲染
- **格式 0x000B → 0x000C**：`0x0F` 帧型退役，引擎不再存储能力卡；`0x000B` 及更早文件 Open 时显式拒绝、不迁移（本仓先例）；`SnapshotVersion=0x02` 不变（索引结构无变化）
- **对消费方 breaking**：meowagent 的五个能力调用点、八个类型与 `CapabilityOriginBuiltin` skip 在其自身适配轮次前编译会断（断点清单见决策档案）
- 决策档案 `notes/implemented/architecture/2026-09-06-l5-record-layer-retirement.md`

## v1.6.0 — 2026-09-04 — 接口去 fallback、按层闭环修复与文件级 L3/L5 公共池（实测驱动）

### L5 统一卡 + 能力池公共化 + plug/ 自动注入

**动机**：能力卡的四种类型（mcp/skill/api/composite）各带一套校验，而宿主的执行路径只认资源条目本身——卡片级 type 与 Workflow 字段是宿主不读的双真相源（动作链实际住在资源 Config 里）。统一成一种形态后这些特判全部消失；同时 L5 像知识图一样天然是「一份文件一份能力」，插件目录让宿主零注入成本拿到能力。

- **一卡统一形态**：卡 = 名称 + N 个功能条目（ResourceRef），无卡片级 type/Workflow；每个条目自带启动方式（type=mcp/skill/api/composite + ref/config）/说明（desc）/怎么用（input/output），与宿主 ToolSpec 逐字段同构。动作链唯一形态 = composite 条目 Config 的 `{"steps":[{"tool":...,"args":...}]}`（`tool` 键与宿主可执行解析器同款）；`Workflow`/`WorkflowStep` 类型与旧 `ref` 键形态删除。校验收敛为包级+卡级两条（name/trigger-summary/resources/卡名包内唯一），Config 以 `{`/`[` 开头须合法 JSON 且 steps 每步带非空 `tool` 键
- **v4 插件包文档**：`memhop-capability/v4` = 一个文档 1..N 张卡（`name` + `capabilities[]`），卡 ID 仍由卡名派生、包内唯一，包名盖到每张卡的 `Package` 字段；v3 文档显式拒绝。`ImportCapability` 返回逐卡 `CapabilityImportResult{CreatedIDs/UpdatedIDs/Errors}`，同字节重导入零写入（追加日志不随启动膨胀）；`CapabilityListQuery` 的 `Type` 过滤换成 `Package` 过滤
- **能力池文件级公共化**：L5 记录全部迁入保留公共域（`SharedL3AgentID` 改名 `SharedPoolAgentID`，底层域值不变），所有 L5 大方法走 `lockSharedPool`——能力池全 agent 共用，任何 agent 的写全家可见，`DeleteAgent` 不动池
- **plug/ 自动注入**：`<meh 同目录>/plug/<包>/capability.json` 在每次 Open 注入共享池（Origin=imported、Package=包名；坏包 Warn 跳过不阻断 Open，同字节重注入零写入）
- **格式 0x000A → 0x000B**：L5 记录换域 = 语义布局变更，旧文件 Open 时显式拒绝、不迁移
- 结晶与导入拒收与内置卡同名的卡（记进 `Errors`，不落库——存储影子卡将永远无法再更新或删除），结晶提示的现有目录并入内置卡；资源条目 `type` 限定 mcp/skill/api/composite 四值，结晶候选按落盘点过卡级校验（create/merge 前置全量校验，reuse 命中不落盘免校验，reuse 未命中降级 create 在落库前过闸——最小载荷不写入）；`UpdateCapability` 保留 `FileHash`（包水位，非内容指纹）——未变更包的重导入是 no-op，宿主对卡的生命周期与定义修改存活到包内容真正变更；plug/ 目录读取失败（权限/IO）告警，缺目录仍为 no-op
- 结晶 prompt 改按新形态产出（功能条目数组 + config 动作链），PromptCard 渲染 `package:` 行与条目级 `steps:` 行（`a -> b -> c`）；内置 6 张卡转 v4 单卡包，链序并入 summary
- 公开面方法数不变（34 会话 + 8 DB 方法）；MCP 工具数不变（31：capability list/update 的 type/workflow 参数删除、list 增 package、import 返回包摘要）；决策档案 `notes/implemented/architecture/2026-09-06-l5-uniform-card-shared-pool.md`

### L3 知识图升级为文件级公共池

**动机**：一个 `.meh` 承载主 agent + 多个子 agent（家族共用一份记忆文件）时，L3 是项目级知识/代码图——宿主导入、库只存——本就不该按 agent 复制。现在 L3 是**文件级公共池**：文件内所有 agent 域共享同一份知识图，导一次全家可见、可挂锚；删 agent（`DeleteAgent`）不动公共池。场景/原文/画像/能力/轨迹仍按域完全隔离。

- **机制**：新增保留域 `core.SharedPoolAgentID`（宿主不可见、不可删、`Session` 拒绑、`ListAgents` 不列、空闲回收豁免；0x000B 起同时承载 L3 与 L5 公共池）；全部 L3 记录住该域，`internal` 根的 8 个 L3 大方法改走 `lockSharedPool`（先 `CheckSession` 校验调用方活着，再锁公共域——L3 操作跨 agent 全局串行，链路无 LLM、操作短）。graph/repo 小方法包零改动
- **锚点跨域**：场景锚点校验（`Search{L3ID}` / `UpdateScene{L3ID}`）改读公共域记录；`DeleteL3` 两阶段——公共锁内删图，释放后遍历「默认域 + 注册表」逐域清锚（`detachGraphAnchors`），不嵌套双锁
- **格式 0x0009 → 0x000A**：0x0009 及更早文件在 Open 时显式拒绝、不迁移（沿用先例；不升版本的替代会让旧按域 L3 记录变孤儿、同名重导入产出双份）
- **MCP 语义变更**：单文件多租户下所有租户共享同一份 L3 池——原「no data is ever shared across tenants」承诺改写为「除 L3 外按域隔离」
- 公开面不变（34 会话 + 8 DB 方法）；新增跨域共享/删档存活/保留域守卫/跨域清锚/重启回归/并发竞速六组测试（`internal/l3shared_test.go`），api 租户隔离用例补 L3 共享断言；决策档案 `notes/implemented/architecture/2026-09-05-l3-file-wide-shared-pool.md`

### 接口去 fallback 与按层闭环修复

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
