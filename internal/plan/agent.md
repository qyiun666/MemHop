# internal/plan — L6 计划树小方法

- **职责**：`PlanStatus` 字符串面与 `StatusToU8`/`StatusToString`/
  `IsTerminalStatus`；`Step`（一次提交的节点侧字段）；`SplitNodePath`；
  写步 `EnsureNode`（沿路径建 pending 节点链，`NodePath` 点号分隔）/
  `CommitNode`（状态 + 节点自身字段，留空即继承现值，终态只戳一次
  `FinishedAt`）/`UpdateNodeSummaryLocked`；树构建 `BuildTree`/`Forest`/
  `ToNodeView`/`CountForest`；`RollupTree`（只回填空 Summary，永不改 Status）。
- **L6 只有计划节点**：一轮的事件是对话原文的同层邻居（L4 的 `Kind=event`），
  不在本包。一棵树属于打开它的那一轮——键就是那个轮次话题 ID，所以
  `PlanState(topic)` 与 `ReadTrajectory(topic)` 用同一个键取两件不同的东西：
  树来自 L6 节点记录，事件来自 L4 内容轨。节点 ID 由
  `HashPlanNode(topicID, nodePath)` 派生，同一个 `NodePath` 在两轮下是两个互不
  相干的节点。本包既不发号也不接受调用方自备的第二个 ID：键的解析与拒零在
  `content.ParseTopicID`，读写两侧共用它。
- **契约**：所有写步都要求调用方持 `ac.Mu`；缓存同步经 `ac.Plans`（无自带锁）。
  `PlanStatus`/`PlanTree`/`PlanStep`/`PlanNodeView` 的唯一定义处，根经
  `models.go` 恒等别名。节点排序只看 `CompareNodePath`（节点上不再有 Seq）。
- **陷阱**：`Status` 是节点里唯一跨文件版本解读的裸 uint8，词表只有一张
  （`statusNames`，双向都读它）：未定义的存储值在 `StatusToString` 报错而不是
  回落 `pending`——回落会把「引擎叫不出名字的一步」显示成「还没开始的一步」。
  `RollupTree` 在非 done 父节点上什么都不做（Model A：父节点完成只由宿主显式
  提交）。写节点永不触碰 L4 内容轨：提交一步不会新增、重排或覆写任何内容记录。
- 事件的写入契约不在本包：`content.ValidateEvent`（非空 `EventType` + `CreatedAt`
  + 4KB payload）由根的大方法在 `EnsureNode`/`CommitNode` **之前**调用——校验晚于
  改树就会留下已推进的节点状态（实测过这个 bug）；`PlanCommit` 还在建链前先验
  `Step.Status`，未知状态同样不该留下一条 pending 链。
- 计划绑定事件的 `EventType` **由宿主自定**，与裸轮次事件同口径：引擎不按它
  分支，名字只在 `ReadTrajectory` 与结晶 prompt 里原样回显。这里**不设许可集**——
  一份只用来拒写、库自己从不读的名单，代价全在宿主侧（自己的事件名被拒即静默
  丢轨迹）。
- 事件归位到某一步靠记录上的 `NodePath` 字符串，由 `content.AppendEvent` 从调用
  参数盖上；不存在第二份「事件指向节点」的引用字段——节点与事件既然分住两层，
  一个人类可读的路径名就够了，多一个库内哈希只会和它派生自的路径不一致。
