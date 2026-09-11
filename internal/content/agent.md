# internal/content — 话题内容与键的小方法

## 职责

- `ParseTopicID`：话题键的解析 + 拒零，读写两侧共用一个入口。
- `ValidateAppend`：内容写入契约，两种 Kind 各管各的轴（`TestValidateAppendRefusals`）。
- `Append`：把一条记录写进该话题的内容轨，必要时分配 `Seq`——本包唯一的写路径。
- `Read`：经域 `L4` 索引按 Kind 读回一个话题的内容，`Seq` 升序。
- `RenderForDistill`：把一个话题的原文渲染成一段带说话者标签的转录
  （`TestRenderForDistillLabelsEveryLineInSeqOrder`）。
- `MaxEventPayload`（单事件 4 KiB）/ `MaxUtterancePayload`（单条原文 64 KiB）。
- 本包不按层划分自己：它服务的是「内容与寻址它的那把键」，同一个话题键下的两种
  记录只差一个 `Kind`。

## 契约

- 全部入口在调用方持有 `ac.Mu` 时被调用。
- `ValidateAppend` 排在任何落盘之前：被拒的写入零留痕。
- 调用方传入的 `IDHash` 与 `TopicID` 一律不采信——前者由 (话题, `Seq`) 派生，后者
  就是调用键本身。
- 超预算是**拒绝而不是截断**：被剪短的记录读回来与完整的无法区分。原文另有上限，
  因为一段文本要付出的 LLM 往返随长度增长，无上限的原文就是无上限的锁内停留。

## 陷阱

- `Kind` 决定采信哪一组字段：原文侧照收 `Role` 与 `ContentType`；事件侧 `Kind` 恒为
  event、`ContentType` 恒为 text、`Role` 恒为 0——说了发生了什么的东西没有说话者，
  也没有媒介。越界的轴是**拒**的：原文带 `EventType` 或 `NodeSeq`、或自称
  `RoleDream`（库给融合摘要盖的标记），都是 `ErrInvalidQuery`。被丢弃的那些字段
  因此不产生分叉，不必为它增设校验分支。
- `Seq` 是一个话题内跨 Kind 共享的单一空间：显式 `Seq=0` 才分配，且
  `seq = max(话题现有 Seq, 2) + 1`。下限 2 是给对话预留的位置：无论先记事件还是
  后补原文，自动分配都踩不到 1 和 2。写一个已被占用的 `Seq` 是**覆写**而非报错，
  跨 Kind 也覆写——重放因此收敛，而不是攒出同一轮的多个版本。
- `Read` 把「索引点名而引擎读不动」报成 `ErrIO`，而不是少几条的转录：一次缺行读起来
  与一轮少说了一句完全一样。
- `RenderForDistill` 按传入顺序逐行渲染 `<说话者>: <内容>`，而传入的就是 `Read` 的
  `Seq` 序——一个话题只有一个序。说话者标签不是装饰：没有标签，一轮的两侧塌成一坨，
  读的人就分不清是谁主张的。
