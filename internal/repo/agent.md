# internal/repo — 存储层契约（模块级 agent 上下文）

本目录（含子包 `core`、`index`）是 MemHop 的**存储层**。任何 AI agent 或
开发者修改本层前必须先读完本文件，修改后必须同步更新本文件。

## 唯一职责

按 **agent 域**（`agentID uint64`）提供记录的读、写、遍历与检索原语：

- `core/`：.meh 引擎——记录帧（26 字节：type/flags/length/agent_id/
  id_hash/crc32）、A/B 文件头、快照（0x02 分域）、空间回收、
  `StorageEngine` 索引（`agent -> idHash -> offset` 两级分域）、Slot 数据模型。
  `StorageEngine` 按功能分文件：`engine.go`（索引模型/访问器）、
  `engine_lifecycle.go`（Create/Open/Checkpoint/Close）、`engine_write.go`（追加）、
  `engine_read.go`（索引查找读）、`engine_delete.go`（墓碑删除）、
  `engine_recovery.go`（扫描/撕裂尾帧截断/索引重建）；数据模型分
  `model.go`（Slot 结构）/ `model_enums.go`（枚举）。
- `index/`：索引——L2Meta（场景读回的唯一话题缓存，`rebuild.go` 全量重建）/
  `traj.go`（L6 轮轨迹形状）/ `tokenizer.go`（gse 分词，唯一读者是 llmops 的关键词兜底）。
  只依赖 `core`。检索退役后 BM25 / 实体模糊 / L3 图索引三块已作为死码删除。
- 根目录 `l0layer.go` ~ `l6layer.go`、`agentlayer.go`：各层记录读写原语，
  一层一个文件组（单文件超 400 行时按功能拆分，命名
  `<layer>layer_<aspect>.go`：`l1layer_sync.go`、`l2layer_topic.go`），
  所有函数以 `agentID` 为域参数。L1 建边/遗忘算法已上提至
  `internal/cap/engram`（ DecayNetwork/RebuildFromL2/BuildHyperedges）、
  L0 画像生成/蒸馏合并至 `internal/cap/profile`、L3 匹配与节点合并至
  `internal/cap/knowledge`——本层只保留记录读写原语
  （如 `MutateNodeL3` 以回调接受调用方策略）与索引维护。

## 边界纪律

1. **域隔离**：所有读写必须携带 `agentID`；跨 agent 的联合查询/共享记忆
   不属于本层，禁止引入。同名记录在不同域内互不可见是正确行为。
2. **无业务语义**：本层不做业务判断（何时压缩、何时结晶、容量策略等一律
   由 `internal` 业务层决定），不调用 LLM。
3. **原语必须有活调用方**：本层导出函数不得为"将来可能用到"预留
   （检索退役后残留的 `UpdateChildrenL2`、`RecoverDeletedScenesL2` +
   `ScanDeletedPayloads` 恢复链、`AgentRecordCount`、
   `CapabilityIDsFromNames`、`SetToSlice`、`index.TokenizeWords`（实体索引
   专用的免停用词分词，删后 `runPipeline`/`processSegments` 的 `filterStop`
   参数一并消失）已一律删除）；模式类参数必须
   具名——L4 查询用 `ArchiveByKeyword` / `ArchiveByTime` / `ArchiveByID`，
   L2 批量删除用 `DeleteScenesL2` / `DeleteTopicsL2`，不再往调用点传裸
   `1/2/3`。
4. **实现不外露**：记录帧布局、快照格式、回收/压缩细节只在 `core` 内部
   流转；`internal` 业务层只能经本目录导出的函数访问数据，不得直接解析
   帧或操作 `StorageEngine` 未导出的状态。
5. **单向依赖**：`repo -> repo/core`、`repo/index -> repo/core`、
   `repo -> common`；禁止反向依赖 `internal`、`api`、`cmd`。
6. **默认域**：`core.DefaultAgentID = 0` 即全零 hex 域，公开 `Session("0000000000000000")` 可绑定；
   注册记录 `RecAgentRegistry (0x10)` 的 `idHash == agentID`，data 为
   agent 名 JSON，Open 时扫描重建 `name -> agentID` 映射。

## 修改者义务

改动本层导出签名、帧/快照格式或域语义时，必须同步更新本文件与
`internal/agent.md` 中受影响的条目，并保证 `go vet ./...` 与
`grep -rn 'L7\|RecL7' --include='*.go'` 零残留。
