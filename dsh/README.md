# MemHop × DSH 插件集

MemHop 在 DeepSeek Harness 中的全面集成实现。完整规划见本地文件
`docs/dsh-memhop-integration-plan.md`（`docs/` 不随公开仓库发布）。

## 部署模型（用户定版）

```
常驻 memhop-mcp server = 一个共享数据库文件（dbDir/memhop.meh）
一个 DSH 会话 = 一个 Agent = 文件内一个独立 agent 域（tenant = 会话 ID）
                                   └── 域内 L2 可多场景（会话内主题划分）
```

- 每个 agent 通过常驻多租户 memhop-mcp server 接入（`/mcp/<tenant-id>`，tenant = 会话 ID）；
  `dsh-memhop` 按生命周期建立/断开连接（`agent/session-start` → 连接 + 注册工具；
  `agent/disposed` → checkpoint 落盘 + 断开）
- 30 个 `mcp__memhop__*` 工具注册到 **agent 作用域**（`agent.ctx.tools`，shadow 全局），
  另有 1 个宿主只读工具 `memhop__session`（UI 面板自检）
- 记忆循环**宿主自动化**：turn 开始自动 `search`、最终回复后自动 `update`、
  按 `dreamEveryTurns` / `idleDreamMs` 调度 `dream` —— 不再依赖模型自觉
- 记忆快照双通道注入：P2 system prompt 动态 context 段 + P3 历史窗口控制
  （surfaceOp replace 遮蔽旧历史，上下文恒定）

## 插件清单（单插件，一包双半区）

| 插件 | 面 | 状态 | 职责 |
|---|---|---|---|
| `dsh-memhop` | **Node 控制面 + Web 前端** | ✅ 完成（合并版 v1.0） | 连接管理 + 工具注册 + 记忆循环自动化 + P2 快照注入 + P3 窗口控制 + 服务器/launchd 管理 + Web 记忆面板 |

> 历史：v1.0 之前按面拆为 `dsh-memhop-core`（控制面）与 `dsh-client-memhop-ui`（UI）
> 两个包；已合并为单一 `@deepseek-ai/dsh-memhop`（package.json 同时声明
> `dsh.harness` 与 `dsh.client`，cordis.patch.yml 只注册一条即两半都生效）。

一个包内部分工：

- **harness 半**（`lib/index.js`）：per-agent 连接（多租户 streamable-http）、
  31+1 工具注册、自动 search/update/dream、P2 `systemPrompt.context()` 快照段
  （per-agent，动态渲染）、P3 窗口控制、`ctx.memhop` 服务（状态/快照/参数偏好）、
  `ctx.memhopServer` 服务（进程/launchd/日志）、UI RPC 桥（`/api` 通道 `memhop/*`）。
- **client 半**（`lib/client.js`，由 `src/client/` 打包）：会话「记忆」tab 面板
  （状态/搜索参数/场景/知识/档案/能力/画像/睡眠/**服务器** 9 页签）+
  输入框自动 search 参数指示条。

## 目录结构

```
dsh/
├── README.md                  # 本文件
├── scripts/
│   └── install.mjs            # 一键部署：装包 + 更新 patch（单条 insert）+ 清理旧包 + 可选 launchd
└── plugins/
    └── dsh-memhop/            # 单插件（一包双半区）
        ├── package.json       # dsh.harness + dsh.client 双声明
        ├── lib/
        │   ├── index.js       # harness 半（纯 ESM，零构建）
        │   └── client.js      # client 半 bundle（scripts/bundle.mjs 生成）
        ├── src/client/        # UI 源码（theme/rpc/sections/Panel/SearchChip/index/trigger）
        └── scripts/
            └── bundle.mjs     # 打包 client 半
```

## 部署（一键）

```bash
node dsh/scripts/install.mjs [--profiles <dir>] [--launchd]
```

- 默认部署到 `~/Library/Application Support/dsh-desktop/harness/profiles`
  （可用 `--profiles <dir>` 或 `DSH_PROFILES_DIR` 覆盖）
- 自动把 `cordis.patch.yml` 中旧的 memhop-core / memhop-ui 两条注册替换为
  单条 `dsh-memhop` 注册（修改前备份 patch；幂等，可重复执行）
- 自动删除已废弃的旧部署包（`dsh-memhop-core`、`dsh-client-memhop-ui`）
- `--launchd`：同时安装 memhop-mcp 常驻服务（wrapper + plist + bootstrap）

部署后需**重启 DSH Desktop** 生效（cordis loader 启动时读取
`profiles/web/cordis.patch.yml`）。重启后查看 `~/Library/Logs/DSH Desktop/harness.log`：

```
[memhop] started dbDir=~/.memhop/agents autoSearch=true ... promptSnapshot=true
[memhop] connect agent=<id> tenant=<session-id> db=~/.memhop/agents/memhop.meh turns=0
[memhop] ready agent=<id> tools=30
[memhop] prompt agent=<id> snapshot context registered
```

每个会话在 `memhop.meh` 内取得自己的 agent 域（`~/.memhop/agents/<tenant-id>.turns.json`
是插件本地的 turn 计数侧车），且每轮对话自动产生 `search`（纯读场景，零写入）与
`update`（一次沉淀本轮用户原文 + 回复）。

## 服务器管理（UI「服务器」页签）

「记忆」面板 → 「服务器」页签可查看/控制 memhop-mcp：

- 状态卡：健康（端口探测）、进程、launchd 安装/加载、spawn 模式、dbDir/bin/wrapper/env
- 操作：启动 / 停止 / 安装 launchd / 卸载 launchd
- 日志：stdout/stderr 尾部各 80 行

对应 host 端 `ctx.memhopServer` 服务（RPC 端点 `memhop/server*`）。
服务器独立于 DSH 运行（launchd 常驻或直启），DSH 崩溃不影响记忆库。

## 架构要点

- **多租户进程契约**：一个常驻 memhop-mcp server 服务所有 agent，`-db-dir` 下只有
  **一个** `memhop.meh`（排他锁单实例），每个租户（`/mcp/<tenant-id>`）是该文件内的
  独立 agent 域；agent 销毁时插件先 `memhop_checkpoint` 落盘再断开连接。server 启动示例：
  `memhop-mcp -db-dir ~/.memhop/agents -transport streamable-http -listen 127.0.0.1:3939`
  （LLM 凭据经 `MEMHOP_LLM_API_URL/KEY/MODEL` 注入；引擎不再联系任何 embedding 服务）。
- **工具作用域**：per-agent 注册（`agent.ctx.tools.register`），多个会话并行时
  各自工具互不干扰；模型看到的 `mcp__memhop__*` 只连本会话的 agent 域。
- **服务契约**：`ctx.memhop`（Service）暴露 `connection(agentId)` /
  `agentState(agentId)` / `state.agents` / `getSearchPrefs`；`ctx.memhopServer`
  （Service）暴露 `status/start/stop/installLaunchd/uninstallLaunchd/logs`，
  供 UI 面板与其他插件消费。
- **自动化时序**：`agent/pre-step`（step 1）取用户原文 → `search`（纯读该会话的场景，
  返回 depth-1 话题集；`scene_id` 首次留空由库新建并按 agent 缓存，本轮原文暂存 pending）；
  `session/event` `assistant/message`（无 tool-call 的最终回复）→ `update`
  （一次沉淀本轮用户原文 + 回复并提炼关键词，返回话题 id）；`turn/end` 与空闲定时器 → `dream`。
- **P2 快照注入**：真 agent 到达时 `agent.ctx.systemPrompt.context()` 注册
  `memhop:snapshot` 段（order 150），text 动态渲染最近一次 search 结果；
  agent 作用域销毁自动清理。
- **P3 窗口控制**：每轮用快照 user 消息 surfaceOp replace 遮蔽旧历史，保留
  最近 `keepRecentNodes` 条节点，tool-pairing 平衡裁剪。
