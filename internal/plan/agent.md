# internal/plan — L5 计划树小方法

- **职责**：`PlanStatus` 字符串面与 `StatusToU8`/`StatusToString`/
  `IsTerminalStatus`；`Step`（声明里的一项：`NodePath` + 状态 + Title + Summary）；
  `SplitNodePath`；
  写步 `ValidateDeclaration`（写入前查完整棵声明：每条路径形状合法、状态叫得出
  名字、同一 `NodePath` 不在一次声明里出现两次）/`EnsureNode`（沿路径建 pending
  节点链）/`SetNodes`（逐个 `EnsureNode` 后复述该节点）/`CommitNode`（状态 +
  节点自身字段，留空即继承现值，终态只戳一次 `FinishedAt`，被重述回非终态则清
  0）/`UpdateNodeSummaryLocked`；树构建 `BuildTree`/`Forest`/
  `ToNodeView`/`CountForest`；`RollupTree`（永不改 Status，只在直接子全部终态时
  回填空 Summary）。
- **L5 只有计划节点**：一轮的事件是对话原文的同层邻居（L4 的 `Kind=event`），
  不在本包。一棵树属于打开它的那一轮——键就是那个轮次话题 ID，所以
  `PlanState(topic)` 与 `SearchL4{TopicID, Kind=event}` 用同一个键取两件不同的
  东西：树来自 L5 节点记录，事件来自 L4 内容轨。节点 ID 由
  `HashPlanNode(topicID, nodePath)` 派生，同一个 `NodePath` 在两轮下是两个互不
  相干的节点。本包既不发号也不接受调用方自备的第二个 ID：键的解析与拒零在
  `content.ParseTopicID`，读写两侧共用它。
- **契约**：所有写步都要求调用方持 `ac.Mu`；缓存同步经 `ac.Plans`（无自带锁）。
  `PlanStatus`/`PlanTree`/`PlanStep`/`PlanNodeView` 的唯一定义处，根经
  `models.go` 恒等别名。节点排序只看 `CompareNodePath`（节点上不再有 Seq）。
- **陷阱**：`Status` 是节点里唯一跨文件版本解读的裸 uint8，词表只有一张
  （`statusNames`，双向都读它）：未定义的存储值在 `StatusToString` 报错而不是
  回落 `pending`——回落会把「引擎叫不出名字的一步」显示成「还没开始的一步」。
  状态面只有 `pending`/`in_progress`/`done`/`failed`：`running` 与 `in_progress`
  同义而引擎分辨不出差别，留两个词等于让宿主掷硬币。节点也不再带类型字段——
  「这一步是什么」由 `Title`/`Summary` 说，「调了哪个工具」记在该步事件的
  `EventType` 上。**收词表不是改个常量**：被删的那个存储值（4）在存量记录里
  会走进上面那条报错路径，所以它落在格式版本上而不是落在兼容里。
  `RollupTree` 在非 done 父节点上什么都不做（Model A：父节点完成只由宿主显式
  声明），有一个直接子还没到终态时也不折——半成品拼起来读起来像结论。写节点永不
  触碰 L4 内容轨：重述一棵树不会新增、重排或覆写任何内容记录。
- 两层的写入契约各自前置、互不越界。树侧：`ValidateDeclaration` 排在 `SetNodes`
  **之前**，一次声明里任何一条不合格就整份拒（校验晚于改树就会留下半棵已推进的
  树，这个 bug 实测过）。内容侧：`content.ValidateAppend`（两种 Kind 各自的轴 +
  4KB 事件预算）由 `AppendArchive` 在任何落盘之前调用，本包不参与。
- 计划绑定事件的 `EventType` **由宿主自定**，与裸轮次事件同口径：引擎不按它
  分支，名字只在内容读回（`SearchL4`）与结晶 prompt 里原样回显。这里**不设许可集**——
  一份只用来拒写、库自己从不读的名单，代价全在宿主侧（自己的事件名被拒即静默
  丢轨迹）。
- 事件归位到某一步靠记录上的 `NodePath` 字符串：`AppendArchive` 采信宿主写在记录
  上的那一个，本包从不改写它；但那个步骤必须是本包声明过的——树是节点唯一的来源，
  指向空头步骤的事件在落盘前就被拒。读侧按**子树**取（`repo.NodePathUnder`）：一步
  拆成子步之后，它做过的事在孩子身上。不存在第二份「事件指向节点」的引用字段——
  节点与事件既然分住两层，一个人类可读的路径名就够了，多一个库内哈希只会和它派生
  自的路径不一致。
