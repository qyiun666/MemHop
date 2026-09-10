# internal/turn — 轮次归属小方法

- **职责**：`Targets`（把 Update 的两个 hex 入参解析成场景 id 与轮次话题 id）、
  `SettleTarget`（纯函数：`TopicID` 必须是该场景已开出的轮次）、
  `ReadProfile`（Search 的 L0 读面，未建立按空画像）。
- **契约**：本包只解析与判定，不读内容也不写内容。`Update` 的语义是「把这一轮
  已有的一次蒸馏成关键词轨」，写入与定序全在 `internal/content`；话题槽位 1/2
  是给对话预留的位置，谁能被沉淀由 `SettleTarget` 一处决定。
- **陷阱**：`SettleTarget` 不读记录、只按规则判定（`k` 从 1 到场景 `TurnSeq` 逐个
  派生轮次 id 比对，轮次数量级下成本可忽略）：Dream 融合父节点与轮次 id 同为
  depth-1、同属一个场景，只能靠派生式区分——靠读记录区分会把「读失败」和
  「不是轮次」混为一谈。
- 归属闸只在沉淀这一侧：一条内容记录只被它的**话题**寻址，话题里推不出场景，
  所以往别的话题 append 不会被这里拦下，那是 `content.Append` 的键语义；
  跨场景沉淀必须由 `SettleTarget` 拒绝，否则一个陈旧 id 就能改写别的会话的关键词轨。
