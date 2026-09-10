# internal/plan — L5 计划树小方法

- **职责**：`PlanStatus` 字符串面与 `StatusToU8`/`StatusToString`/
  `IsTerminalStatus`；两个入参形状 `NodeSpec`（要创建的一步：归属轮次 + 父序号 +
  Title）与 `Step`（要重述的一步：归属轮次 + 序号 + 状态 + Title + Summary）；
  写步 `CreateNode`（发号并落一条节点记录）/`UpdateNode`（状态 + 节点自身字段）/
  `UpdateNodeSummaryLocked`（只填 Summary，绝不动 Status）；树构建 `BuildTree`/
  `Forest`/`ToNodeView`/`CountForest`；`RollupTree`（只在直接子全部终态时回填空
  Summary）。
- **L5 只有计划节点**：一轮的事件是对话原文的同层邻居（L4 的 `Kind=event`），
  不在本包。一棵树属于打开它的那一轮——键就是那个轮次话题 ID，所以
  `PlanState(topic)` 与 `SearchL4{TopicID, Kind=event}` 用同一个键取两件不同的
  东西：树来自 L5 节点记录，事件来自 L4 内容轨。
- **一步由两个数说清**：`Seq` 是该轮内的序号，库从 1 起顺序发号（
  `PlanCache.NextSeq`），`ParentSeq` 说它挂在谁下面、0 即根。记录 id 由
  `HashPlanNode(topicID, seq)` 派生，所以同一个序号在两轮下是两个互不相干的
  节点，而序号本身只是「这一轮里的第几步」——它不是记录 id，也就不受门面那条
  「对宿主的 id 一律 hex」契约约束（`api` 的 `TestPublicSignaturesCarryNoNumericIds`
  正是据此仍为绿）。发号在 `PlanCache`，调用方与宿主都不自备序号。键的解析与拒零
  在 `content.ParseTopicID`，读写两侧共用它。
- **契约**：所有写步都要求调用方持 `ac.Mu`；缓存同步经 `ac.Plans`（无自带锁）。
  `PlanStatus`/`PlanTree`/`Step`/`PlanNodeView` 的唯一定义处，根经 `models.go`
  恒等别名。节点排序只看 `Seq`，也就是创建顺序。
- **陷阱**：`Status` 是节点里唯一跨文件版本解读的裸 uint8，词表只有一张
  （`statusNames`，双向都读它）：未定义的存储值在 `StatusToString` 报错而不是
  回落某个能叫出名字的状态——回落会把「引擎叫不出名字的一步」显示成一步它从没
  到达过的状态。状态面只有 `in_progress`/`done`/`failed`，且 `in_progress` 就是
  0：新建的一步零值即合法，创建口因此不需要宿主先声明状态。同义的第二词
  （`running`）与「已计划未开始」（`pending`）都不在词表里——留两个引擎分辨不出
  差别的词等于让宿主掷硬币，而一个从没开始过的步骤在本模型里并不存在。
  **收词表不是改个常量**：被腾出的存储值在存量记录里会走进上面那条报错路径，
  所以它落在格式版本上而不是落在兼容里。
  `RollupTree` 在非 done 父节点上什么都不做（Model A：父节点完成只由宿主显式
  声明），有一个直接子还没到终态时也不折——半成品拼起来读起来像结论。写节点永不
  触碰 L4 内容轨：重述一个步骤不会新增、重排或覆写任何内容记录。
- 两层的写入契约各自前置、互不越界。树侧：`UpdateNode` 第一件事是
  `StatusToU8`，解析不出就返回，节点还没被读过；`CreateNode` 第一件事是查父序号
  在不在树上。所以被拒的写零留痕，宿主不必拿现树去 diff 才知道哪条落了自己。
  内容侧：`content.ValidateAppend`（两种 Kind 各自的轴 + 4KB 事件预算）由
  `AppendArchive` 在任何落盘之前调用，本包不参与。
- 计划绑定事件的 `EventType` **由宿主自定**，与裸轮次事件同口径：引擎不按它
  分支，名字只在内容读回（`SearchL4`）与结晶 prompt 里原样回显。这里**不设许可集**——
  一份只用来拒写、库自己从不读的名单，代价全在宿主侧（自己的事件名被拒即静默
  丢轨迹）。
- 事件归位到某一步靠记录上的 `NodeSeq`：`AppendArchive` 采信宿主写在记录上那一
  个，本包从不改写它；但那个步骤必须已在树上——指向空头步骤的事件在落盘前就被
  拒。**除此之外没有任何路径能创建节点**：`CreateNode` 是唯一来源，父序号缺失也
  一律拒而不补链。读侧一步的取值范围是它自己加整棵子树，闭包由 `PlanCache.Subtree`
  沿 `ParentSeq` 求——序号没有前缀形状可匹配，树的形状只在本包与缓存里。不存在
  第二份「事件指向节点」的引用字段：节点与事件既然分住两层，一个序号就够了。
