# internal/repo — 存储层契约（模块级 agent 上下文）

本目录（含子包 `core`、`index`）是 MemHop 的**存储层**。任何 AI agent 或
开发者修改本层前必须先读完本文件，修改后必须同步更新本文件。

## 唯一职责

按 **agent 域**（`agentID uint64`）提供记录的读、写、遍历与检索原语：

- `core/`：.meh 引擎——记录帧（26 字节：type/flags/length/agent_id/
  id_hash/crc32）、A/B 文件头、快照（0x02 分域）、空间回收、
  `StorageEngine` 索引（`agent -> idHash -> offset` 两级分域）、Slot 数据模型。
- `index/`：检索索引——BM25 sparse、L2Meta、L1 实体索引、重建。只依赖 `core`。
- 根目录 `l0layer.go` ~ `l6layer.go`、`agentlayer.go`：各层记录读写原语，
  一层一文件，所有函数以 `agentID` 为域参数。

## 边界纪律

1. **域隔离**：所有读写必须携带 `agentID`；跨 agent 的联合查询/共享记忆
   不属于本层，禁止引入。同名记录在不同域内互不可见是正确行为。
2. **无业务语义**：本层不做业务判断（何时压缩、何时结晶、容量策略等一律
   由 `internal` 业务层决定），不调用 LLM/encoder。
3. **实现不外露**：记录帧布局、快照格式、回收/压缩细节只在 `core` 内部
   流转；`internal` 业务层只能经本目录导出的函数访问数据，不得直接解析
   帧或操作 `StorageEngine` 未导出的状态。
4. **单向依赖**：`repo -> repo/core`、`repo/index -> repo/core`、
   `repo -> common`；禁止反向依赖 `internal`、`api`、`cmd`。
5. **默认域**：`core.DefaultAgentID = 0` 是单 agent 宿主的默认域；
   注册记录 `RecAgentRegistry (0x10)` 的 `idHash == agentID`，data 为
   agent 名 JSON，Open 时扫描重建 `name -> agentID` 映射。

## 修改者义务

改动本层导出签名、帧/快照格式或域语义时，必须同步更新本文件与
`internal/agent.md` 中受影响的条目，并保证 `go vet ./...` 与
`grep -rn 'L7\|RecL7' --include='*.go'` 零残留。
