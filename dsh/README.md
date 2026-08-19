# MemHop × DSH 插件集

MemHop 在 DeepSeek Harness 中的全面集成实现。完整规划见
[`docs/dsh-memhop-integration-plan.md`](../docs/dsh-memhop-integration-plan.md)。

## 部署模型（用户定版）

```
一个 DSH 会话 = 一个 Agent = 一个独立 .meh 文件（dbDir/<session-id>.meh）
                                   └── 文件内 L2 可多场景（会话内主题划分）
```

- 每个 agent 通过常驻多租户 memhop-mcp server 接入（`/mcp/<tenant-id>`，tenant = 会话 ID）；
  `dsh-memhop-core` 按生命周期建立/断开连接（`agent/session-start` → 连接 + 注册工具；
  `agent/disposed` → checkpoint 落盘 + 断开）
- 31 个 `mcp__memhop__*` 工具注册到 **agent 作用域**（`agent.ctx.tools`，shadow 全局）
- 记忆循环**宿主自动化**：turn 开始自动 `search`、最终回复后自动 `update`、
  按 `dreamEveryTurns` / `idleDreamMs` 调度 `dream` —— 不再依赖模型自觉

## 插件清单

| 插件 | 面 | 状态 | 职责 |
|---|---|---|---|
| `dsh-memhop-core` | Node 控制面 | **P1 完成** | per-agent 连接管理 + 工具注册 + 记忆循环自动化 + 快照缓存 + ctx.memhop 服务 |
| `dsh-memhop-prompt` | Node 上下文 | 规划（P2/P3） | systemPrompt.context() 记忆快照 section（P2）；surfaceOp replace 窗口控制（P3） |
| `dsh-memhop-ui` | Web | 规划（P4/P5） | 侧边栏记忆视图、会话内快照预览/循环心跳、设置页、图谱/轨迹/检索调试 |

> 注：`dsh-mcp-client` 的 memhop 条目已移除（P1 起由 core 全权接管工具注册与进程管理）。

## 目录结构

```
dsh/
├── README.md                  # 本文件
├── scripts/
│   └── deploy.mjs             # 部署插件到 DSH profiles
└── plugins/
    └── dsh-memhop-core/       # 控制面插件（P0/P1）
        ├── package.json
        └── lib/index.js       # 纯 ESM，零构建
```

## 部署

```bash
node dsh/scripts/deploy.mjs
# 默认部署到 ~/Library/Application Support/dsh-desktop/harness/profiles
# 可用 --profiles <dir> 或 DSH_PROFILES_DIR 覆盖
```

部署后需**重启 DSH Desktop** 生效（cordis loader 启动时读取
`profiles/web/cordis.patch.yml`）。重启后查看 `~/Library/Logs/DSH Desktop/harness.log`：

```
[memhop-core] started serverUrl=http://127.0.0.1:3939 autoSearch=true ...
[memhop-core] connect agent=<id> tenant=<session-id> db=~/.memhop/agents/<session-id>.meh
[memhop-core] ready agent=<id> tools=31
```

每个会话应看到独立的 `<session-id>.meh` 文件，且每轮对话自动产生
search（原文）与 update（回复）写入。

## 架构要点

- **多租户进程契约**：一个常驻 memhop-mcp server 服务所有 agent，每个租户
  （`/mcp/<tenant-id>`）一个 `.meh` 文件（排他锁单实例）；agent 销毁时插件先
  `memhop_checkpoint` 落盘再断开连接。server 启动示例：
  `memhop-mcp --db-dir ~/.memhop/agents --embed-model <model> --encoder-addr <addr>`
  （LLM 凭据经 `MEMHOP_LLM_API_URL/KEY/MODEL` 注入）。
- **工具作用域**：per-agent 注册（`agent.ctx.tools.register`），多个会话并行时
  各自工具互不干扰；模型看到的 `mcp__memhop__*` 只连本会话数据库。
- **服务契约**：`ctx.memhop`（Service）暴露 `connection(agentId)` /
  `agentState(agentId)` / `state.agents`，供 prompt 插件与 UI 插件消费。
- **自动化时序**：`agent/pre-step`（step 1）取用户原文 → `search`（写原文+
  缓存 topicId 与快照）；`session/event` `assistant/message`（无 tool-call 的
  最终回复）→ `update`（归档回复）；`turn/end` 与空闲定时器 → `dream`。
- **阶段规划**：P0 骨架 → P1 per-agent 数据库 + 循环自动化（当前）→ P2 快照
  注入（system prompt context）→ P3 窗口控制（surface replace）→ P4 UI 全面
  集成 → P5 高级视图（图谱/轨迹/检索调试）。
