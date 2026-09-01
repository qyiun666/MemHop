// @deepseek-ai/dsh-memhop — MemHop 记忆子系统(单插件,harness 半)
//
// 部署模型(用户定版):
//   常驻 memhop-mcp server ── 一个共享数据库文件(dbDir/memhop.meh)
//     ├── DSH 会话(agent)──绑定──> 文件内独立 agent 域(tenant = 会话 ID)
//     │                                 └── 域内 L2 可多场景(会话内主题划分)
//
// 职责(合并自原 dsh-memhop-core + dsh-client-memhop-ui host 半):
//   1. per-agent 连接管理:agent/session-start 时连接常驻多租户 memhop-mcp
//      server(/mcp/<tenant-id>,tenant = 会话 ID,server 在该 ID 上懒建
//      agent 域)并注册 30 个 mcp__memhop__* 工具到 agent
//      作用域(agent.ctx.tools,shadow 全局);agent/disposed 时先
//      memhop_checkpoint 落盘再断开。
//   2. 记忆循环自动化:turn 开始自动 search(读本会话场景=宿主会话)、最终回复
//      后自动 update(一次写入本轮双原文并提炼)—— 全部宿主执行,不经主 LLM。
//   3. P2 system prompt 注入:每轮把记忆快照作为动态 context 段注入
//      (systemPrompt.context,per-agent 作用域)。
//   4. P3 窗口控制:每轮用"记忆快照"user 消息以 surfaceOp replace 遮蔽旧
//      历史,保留最近 N 条 surface 节点 —— LLM 上下文恒定。
//   5. 服务器管理:ctx.memhopServer 服务 —— 检查/启动/停止 memhop-mcp,
//      launchd 安装/卸载,日志尾部;UI「服务器」页签经 RPC 调用。
//   6. UI RPC 桥:拦截 /api 通道 memhop/* 端点,转发到 memhop 服务
//      (agents/session/prefs/server* + 直通 MCP 工具)。

import { Service } from "@deepseek-ai/cordis";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { ListToolsResultSchema } from "@modelcontextprotocol/sdk/types.js";
import { createUserMessage } from "@deepseek-ai/dsh-llm";
import { toolPairingBalancedAfter } from "@deepseek-ai/dsh-compaction";
import { readFileSync, writeFileSync, existsSync, rmSync, createWriteStream } from "node:fs";
import { spawn, spawnSync } from "node:child_process";
import net from "node:net";

export const name = "dsh-memhop";

export const inject = ["tools", "systemPrompt", "agents", "connection"];

/** 剥离 DSH 注入到 user 消息的系统段(runtime context 快照 / MemHop
 *  记忆快照),避免把注入文本当作用户原文写入记忆。 */
function stripInjected(text) {
  const markers = [
    "Current runtime context. This snapshot supersedes",
    "【MemHop 记忆快照】",
  ];
  let cut = text;
  for (const mk of markers) {
    const i = cut.indexOf(mk);
    if (i !== -1) cut = cut.slice(0, i);
  }
  return cut.trim();
}

/** 默认配置:与 memhop-mcp 多租户 HTTP 接入对齐。 */
export const DEFAULTS = {
  /** 多租户 memhop-mcp server 地址(streamable-http);server 以 -db-dir 启动。 */
  serverUrl: "http://127.0.0.1:3939",
  /** server 端共享数据库 memhop.meh 的存放目录(~ 展开);须与 server 端 -db-dir
   *  一致(插件的 turns 计数侧车落在同目录,跨重启累计 dream 阈值)。 */
  dbDir: "~/.memhop/agents",
  /** MCP 工具调用超时(ms)。 */
  toolCallTimeoutMs: 120000,
  /** 工具名前缀。 */
  toolPrefix: "mcp__memhop__",
  /** P1:turn 开始自动 search。 */
  autoSearch: true,
  /** P1:最终回复后自动 update。 */
  autoUpdate: true,
  /** P1:Dream 自动触发阈值(turn 数,0=关闭)。 */
  dreamEveryTurns: 20,
  /** P1:空闲 Dream 触发阈值(ms,0=关闭)。 */
  idleDreamMs: 10 * 60 * 1000,
  /** P2:记忆快照注入 system prompt(per-agent context 段)。 */
  promptSnapshot: true,
  /** P2/P3:记忆快照最大字符数。 */
  snapshotMaxChars: 16000,
  /** P3:窗口控制——每轮用记忆快照遮蔽旧历史(上下文恒定)。 */
  windowControl: true,
  /** P3:surface 保留节点数(快照消息之外最近 N 条消息级节点)。 */
  keepRecentNodes: 40,
  // ---- 服务器管理(ServerControl,UI「服务器」页签)----
  /** memhop-mcp 可执行文件;缺省从 wrapper 或 PATH 自动探测。 */
  serverBin: "",
  /** wrapper 脚本路径(launchd 实际执行体)。 */
  serverWrapperPath: "~/.memhop/memhop-mcp.sh",
  /** LLM 凭据 env 文件(KEY=VALUE 或 export KEY=VALUE)。 */
  serverEnvFile: "~/.memhop/server.env",
  /** 服务器 stdout/stderr 日志。 */
  serverOutLog: "~/.memhop/server.out.log",
  serverErrLog: "~/.memhop/server.err.log",
  /** 服务器监听端口(状态/健康探测用)。 */
  serverPort: 3939,
};

const MCP_PLIST_LABEL = "com.memhop.mcp";

/** 直接写 stderr:harness-node 的 stderr 会被 desktop 壳转发到 harness.log。 */
function emit(tag, message) {
  try {
    process.stderr.write(`[memhop] ${tag}: ${message}\n`);
  } catch {
    /* 静默 */
  }
}

function expandHome(p) {
  if (typeof p === "string" && p.startsWith("~/")) {
    const home = process.env.HOME || process.env.USERPROFILE || ".";
    return `${home}${p.slice(1)}`;
  }
  return p;
}

function homeDir() {
  return process.env.HOME || process.env.USERPROFILE || ".";
}

function guiUid() {
  try {
    return process.getuid ? process.getuid() : 501;
  } catch {
    return 501;
  }
}

/** per-tenant turn 计数侧车(<db-dir>/<tenant-id>.turns.json)——dream 阈值跨重启累计。 */
function turnsFileFor(dbDir, tenant) {
  return `${expandHome(dbDir)}/${String(tenant || "default")}.turns.json`;
}

function loadTurns(dbDir, tenant) {
  try {
    const n = JSON.parse(readFileSync(turnsFileFor(dbDir, tenant), "utf8"))?.turns;
    return typeof n === "number" && Number.isFinite(n) ? Math.max(0, Math.floor(n)) : 0;
  } catch {
    return 0;
  }
}

/** 从 MCP 工具列表构造 ToolRuntime 定义(对照 dsh-mcp-client 的 createDefinition 精简版)。 */
function createDefinition(client, agentCtx, publicName, rawName, description, parameters, timeoutMs) {
  return {
    name: publicName,
    description,
    parameters,
    output: {
      schema: {
        type: "object",
        properties: {
          content: { type: "array", items: {} },
        },
        required: ["content"],
        additionalProperties: false,
      },
      render(_args, value) {
        const text = Array.isArray(value?.content)
          ? value.content
              .map((b) => (b && typeof b === "object" && typeof b.text === "string" ? b.text : ""))
              .join("")
          : JSON.stringify(value ?? null);
        return [{ type: "text", text: text || "(no output)" }];
      },
    },
    async execute(args, exec) {
      const raw = typeof args === "object" && args !== null ? args : {};
      const result = await client.callTool(
        {
          name: rawName,
          arguments: raw,
        },
        undefined,
        { timeout: timeoutMs, signal: exec?.signal }
      );
      if (result.isError === true) {
        const text = extractText(result.content, rawName);
        throw new Error(text || `tool ${rawName} failed`);
      }
      const content = result.content;
      return {
        content: Array.isArray(content) ? content : [{ type: "text", text: JSON.stringify(content ?? null) }],
        ...(result.structuredContent !== undefined ? { structuredContent: result.structuredContent } : {}),
      };
    },
  };
}

function extractText(content, rawName) {
  if (!Array.isArray(content)) return "(no output)";
  return (
    content
      .map((b) => (b && typeof b === "object" && typeof b.text === "string" ? b.text : ""))
      .join("")
      .trim() || "(no output)"
  );
}

/** 单个 agent 的 memhop 连接:多租户 MCP client + 工具注册 + 状态。 */
class AgentConnection {
  constructor(agent, cfg) {
    this.agent = agent;
    this.cfg = cfg;
    this.client = null;
    this.transport = null;
    this.disposers = new Map();
    this.ready = false;
    this.promptRegistered = false;
    this.state = {
      startedAt: Date.now(),
      dbPath: "",
      tenant: "",
      toolsRegistered: 0,
      lastSearchAt: 0,
      lastUpdateAt: 0,
      lastDreamAt: 0,
      turns: 0,
      lastError: null,
      topicId: null,
    };
  }

  /** 全租户共用 server 端那一个数据库文件（租户是其内的独立 agent 域）。 */
  sharedDbPath() {
    return `${expandHome(this.cfg.dbDir)}/memhop.meh`;
  }

  async start(sessionId) {
    const safeId = String(sessionId || "default").replace(/[^A-Za-z0-9_-]/g, "_");
    this.state.tenant = safeId;
    this.state.dbPath = this.sharedDbPath();
    // 恢复跨重启的 turn 计数(dream 阈值累计;占位/重连不归零)。
    this.state.turns = loadTurns(this.cfg.dbDir, safeId);
    emit("connect", `agent=${this.agent.id} tenant=${safeId} db=${this.state.dbPath} turns=${this.state.turns}`);
    // 多租户接入:tenant = 清洗后的会话 ID,server 端在该 ID 上懒建 agent 域
    // (所有租户共用 <db-dir>/memhop.meh 这一个文件);插件不再 spawn 子进程,
    // LLM 配置随 server 进程启动(见 -db-dir / MEMHOP_LLM_*)。
    // SDK >= 1.9:构造函数签名是 (url, opts),不是旧版 ({ url }) 对象。
    // (安装版本 @modelcontextprotocol/sdk 1.30.0,传对象会 TypeError: Invalid URL)
    this.transport = new StreamableHTTPClientTransport(`${this.cfg.serverUrl}/mcp/${safeId}`);
    this.client = new Client({ name: "dsh-memhop", version: "1.0.0" });
    await this.client.connect(this.transport);
    await this.registerTools();
    this.ready = true;
    emit("ready", `agent=${this.agent.id} tools=${this.state.toolsRegistered}`);
  }

  async registerTools() {
    const agentCtx = this.agent.ctx;
    if (!agentCtx?.tools) {
      // 懒补建阶段可能只有 {id, session} 占位对象(无 ctx)——工具注册延后,
      // 等真 agent 对象到达(session-start 事件或 agents 服务轮询)时由
      // attachRealAgent 补做。连接本身(宿主侧自动 search/update)不受影响。
      emit("warn", `agent=${this.agent.id} tool registration deferred: no agent ctx yet`);
      return;
    }
    if (this.state.toolsRegistered > 0) return; // 幂等
    const list = await this.client.request(
      { method: "tools/list", params: {} },
      ListToolsResultSchema
    );
    for (const tool of list.tools) {
      const publicName = `${this.cfg.toolPrefix}${tool.name}`;
      const definition = createDefinition(
        this.client,
        agentCtx,
        publicName,
        tool.name,
        tool.description ?? "",
        tool.inputSchema,
        this.cfg.toolCallTimeoutMs
      );
      try {
        const dispose = agentCtx.tools.register(definition);
        this.disposers.set(publicName, dispose);
        this.state.toolsRegistered += 1;
      } catch (err) {
        emit("warn", `agent=${this.agent.id} register ${publicName} failed: ${String(err?.message ?? err)}`);
      }
    }
    // 宿主级只读工具:当前会话的 memhop 数据库状态(UI 面板用)。
    // 名字避开 MCP 前缀 mcp__memhop__,直接用 memhop__ 前缀。
    const sessionDef = {
      name: "memhop__session",
      description:
        "只读:当前会话的 MemHop 数据库状态(agentId、tenant、共享 .meh 路径、自动循环统计)。仅供 UI 面板调用,模型无需使用。",
      parameters: { type: "object", properties: {}, additionalProperties: false },
      output: {
        schema: {
          type: "object",
          properties: { content: { type: "array", items: {} } },
          required: ["content"],
          additionalProperties: false,
        },
        render(_args, value) {
          const text = Array.isArray(value?.content)
            ? value.content
                .map((b) => (b && typeof b === "object" && typeof b.text === "string" ? b.text : ""))
                .join("")
            : JSON.stringify(value ?? null);
          return [{ type: "text", text: text || "(no output)" }];
        },
      },
      async execute() {
        const s = this.state;
        return {
          content: [
            {
              type: "text",
              text: JSON.stringify({
                agentId: this.agent.id,
                dbPath: s.dbPath,
                tenant: s.tenant,
                ready: this.ready,
                toolsRegistered: s.toolsRegistered,
                lastSearchAt: s.lastSearchAt,
                lastUpdateAt: s.lastUpdateAt,
                lastDreamAt: s.lastDreamAt,
                turns: s.turns,
                topicId: s.topicId,
                lastError: s.lastError,
              }),
            },
          ],
        };
      },
    };
    try {
      const dispose = agentCtx.tools.register(sessionDef);
      this.disposers.set("memhop__session", dispose);
    } catch (err) {
      emit("warn", `agent=${this.agent.id} register memhop__session failed: ${String(err?.message ?? err)}`);
    }
  }

  /** 真 agent 对象(带 ctx)到达时补注册工具与 prompt 段——懒补建阶段只有占位对象。 */
  attachRealAgent(agent, onPrompt) {
    if (!agent?.ctx) return;
    if (this.agent.ctx) return; // 已是真 agent
    this.agent = agent;
    if (this.ready && this.state.toolsRegistered === 0) {
      this.registerTools()
        .then(() => {
          if (this.state.toolsRegistered > 0) {
            emit("ready", `agent=${this.agent.id} tools=${this.state.toolsRegistered} (deferred)`);
          }
        })
        .catch((err) => {
          emit("warn", `agent=${this.agent.id} deferred tool registration failed: ${String(err?.message ?? err)}`);
        });
    }
    if (typeof onPrompt === "function") onPrompt();
  }

  async call(rawName, args, timeoutMs) {
    if (!this.client || !this.ready) throw new Error(`memhop(${this.agent.id}) not connected`);
    const result = await this.client.callTool({ name: rawName, arguments: args ?? {} }, undefined, {
      timeout: timeoutMs ?? this.cfg.toolCallTimeoutMs,
    });
    if (result.isError === true) throw new Error(extractText(result.content, rawName));
    return parseResultContent(result.content);
  }

  /** 把 turn 计数写盘(turn/end 后调用;失败记录错误)。 */
  persistTurns() {
    try {
      if (!this.state.tenant) return;
      writeFileSync(turnsFileFor(this.cfg.dbDir, this.state.tenant), JSON.stringify({ turns: this.state.turns }));
    } catch (err) {
      emit("warn", `agent=${this.agent.id} persist turns failed: ${String(err?.message ?? err)}`);
    }
  }

  async dispose() {
    for (const dispose of this.disposers.values()) {
      try {
        dispose();
      } catch (err) {
        emit("warn", `agent=${this.agent.id} dispose tool registration failed: ${String(err?.message ?? err)}`);
      }
    }
    this.disposers.clear();
    if (this.client && this.ready) {
      try {
        // 多租户下 DB 常驻 server 进程,没有"会话结束即 Close";主动 checkpoint
        // 触发索引快照落盘,保证 dispose 时数据已持久化。
        await this.call("memhop_checkpoint", {}, this.cfg.toolCallTimeoutMs);
      } catch (err) {
        emit("warn", `agent=${this.agent.id} checkpoint before close failed: ${String(err?.message ?? err)}`);
      }
    }
    try {
      // close 释放 MCP 会话;DB 数据已由上面的 checkpoint 落盘。
      await this.client?.close();
    } catch (err) {
      emit("warn", `agent=${this.agent.id} close memhop-mcp failed: ${String(err?.message ?? err)}`);
    }
    try {
      await this.transport?.close();
    } catch (err) {
      emit("warn", `agent=${this.agent.id} close transport failed: ${String(err?.message ?? err)}`);
    }
    this.ready = false;
    emit("stopped", `agent=${this.agent.id} db=${this.state.dbPath}`);
  }
}

/** 解析 MCP 工具结果 content 为 JSON 值(text 块内 JSON 解析失败则原样返回)。 */
function parseResultContent(content) {
  if (!Array.isArray(content)) return content;
  const texts = content
    .map((b) => (b && typeof b === "object" && typeof b.text === "string" ? b.text : ""))
    .join("");
  if (!texts) return content;
  try {
    return JSON.parse(texts);
  } catch {
    return texts;
  }
}

/** 渲染 search 结果为模型可读的记忆快照文本(截断到 maxChars)。
 *  过往对话按聊天记录形式:时间升序、角色前缀、紧凑时间戳。 */
function renderSnapshot(res, maxChars) {
  if (!res) return "";
  const lines = [];
  lines.push("【MemHop 记忆快照】以下为本会话此前记忆的检索结果(以当前对话消息为准)。");
  const profile = res.profile;
  if (profile && (profile.name || profile.role || profile.personality)) {
    const bits = [profile.name, profile.role, profile.personality].filter(Boolean);
    lines.push(`画像: ${bits.join(" · ")}`);
  }
  const topics = res.topics ?? [];
  const topicKws = topics
    .map((c) => (Array.isArray(c.fused_keywords) ? c.fused_keywords.slice(0, 8).join("/") : ""))
    .filter(Boolean);
  if (topicKws.length > 0) lines.push(`本会话话题(${topics.length}): ${topicKws.join(" | ")}`);
  const archives = res.archives ?? [];
  const msgs = archives
    .map((a) => ({
      role: a.role,
      t: Number(a.created_at) || 0,
      text: String(a.content ?? "").replace(/\s+/g, " ").trim(),
    }))
    .filter((m) => m.text)
    .sort((a, b) => a.t - b.t);
  if (msgs.length > 0) {
    lines.push("──────── 过往对话 ────────");
    for (const m of msgs) {
      const who = m.role === 1 ? "🤖 助手" : "👤 用户";
      const stamp = m.t
        ? new Date(m.t).toLocaleString("zh-CN", {
            month: "2-digit",
            day: "2-digit",
            hour: "2-digit",
            minute: "2-digit",
            hour12: false,
          })
        : "····";
      lines.push(`[${stamp}] ${who}: ${m.text}`);
    }
    lines.push("──────── 对话结束 ────────");
  }
  let out = lines.join("\n");
  if (out.length > maxChars) out = `${out.slice(0, maxChars)}\n…(快照截断)`;
  return out;
}

/**
 * P3 窗口控制:用一条"记忆快照"user 消息以 surfaceOp replace 遮蔽旧历史,
 * 保留最近 keepRecentNodes 条 surface 节点。cut 位置保证 tool-pairing 平衡
 * (不会切断未配对的 tool_call/tool_result)。日志 append-only,遮蔽仅影响
 * 模型可见 surface,原始记录仍可审计。
 */
function replaceHistory(session, snapshotText, cfg) {
  const nodes = session.surface?.nodes;
  if (!Array.isArray(nodes) || nodes.length === 0) return 0;
  const keep = Math.max(8, cfg.keepRecentNodes);
  if (nodes.length <= keep) return 0; // 历史还不够长,暂不遮蔽
  let cutIndex = nodes.length - keep - 1; // 要遮蔽的最后一条的 index
  while (cutIndex >= 0 && !toolPairingBalancedAfter(session, nodes[cutIndex])) {
    cutIndex -= 1;
  }
  if (cutIndex < 0) return 0;
  const start = nodes[0];
  const end = nodes[cutIndex];
  const shadowed = nodes.slice(0, cutIndex + 1);
  session.append(
    "user/message",
    createUserMessage({
      content: [{ type: "text", text: snapshotText }],
      source: { kind: "plugin", plugin: "memhop" },
    }),
    {
      surfaceOp: { op: "replace", start, end },
      sourceEventSeqs: shadowed,
    }
  );
  return shadowed.length;
}

/** 服务面:ctx.memhop —— agent 级状态、快照查询与 search 参数偏好。 */
class MemhopService extends Service {
  constructor(ctx, config) {
    super(ctx, "memhop");
    this.config = config;
    this.state = {
      pluginStartedAt: Date.now(),
      agents: new Map(), // agentId -> AgentConnection
    };
    /** agentId -> 自动 search 参数偏好(UI 面板可读写,发送对话时携带)。 */
    this.searchPrefs = new Map(); // agentId -> { autoCreate?, directedL2Id?, directedL3Id? }
  }

  /**
   * 取 agent 连接;不存在时尝试懒补建(apply 里通过 onNeedAgent 挂接
   * attachAgent)。原因:会话打开瞬间 UI 插件(dock/面板)可能先于
   * agent/session-start 事件发起调用,事件驱动建连存在竞态窗口。
   */
  connection(agentId) {
    const existing = this.state.agents.get(agentId);
    if (existing) return existing;
    if (agentId && typeof this.onNeedAgent === "function") {
      try {
        this.onNeedAgent(agentId);
      } catch (err) {
        emit("warn", `lazy attach ${agentId} failed: ${String(err?.message ?? err)}`);
      }
    }
    return this.state.agents.get(agentId) ?? null;
  }

  agentState(agentId) {
    return this.connection(agentId)?.state ?? null;
  }

  /** 全部 agent 连接的只读快照(UI 面板 agent 选择器用)。 */
  agentsInfo() {
    const list = [];
    for (const [agentId, conn] of this.state.agents) {
      list.push({
        agentId,
        ready: conn.ready,
        dbPath: conn.state.dbPath,
        toolsRegistered: conn.state.toolsRegistered,
        lastSearchAt: conn.state.lastSearchAt,
        lastUpdateAt: conn.state.lastUpdateAt,
        lastDreamAt: conn.state.lastDreamAt,
        turns: conn.state.turns,
        lastError: conn.state.lastError,
      });
    }
    // 最活跃的排最前(lastSearchAt 降序)。
    list.sort((a, b) => b.lastSearchAt - a.lastSearchAt);
    return list;
  }

  /** 设置某 agent 的自动 search 参数偏好(null 值字段清除)。 */
  setSearchPrefs(agentId, prefs) {
    const cur = this.searchPrefs.get(agentId) ?? {};
    const next = { ...cur };
    if (prefs === null) {
      this.searchPrefs.delete(agentId);
      return { ...cur };
    }
    if ("l3Id" in prefs) next.l3Id = prefs.l3Id || null;
    this.searchPrefs.set(agentId, next);
    return { ...next };
  }

  getSearchPrefs(agentId) {
    return { ...(this.searchPrefs.get(agentId) ?? {}) };
  }
}

// ---- 服务器管理:ctx.memhopServer ----

function parseEnvFile(file) {
  const env = {};
  try {
    for (const raw of readFileSync(file, "utf8").split("\n")) {
      const line = raw.trim();
      if (!line || line.startsWith("#")) continue;
      const m = line.match(/^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$/);
      if (m) env[m[1]] = m[2].replace(/^["']|["']$/g, "").trim();
    }
  } catch {
    /* env 文件缺失视为空 */
  }
  return env;
}

function tailFile(file, limit) {
  try {
    const lines = readFileSync(file, "utf8").split("\n");
    return lines.slice(-(limit ?? 100)).join("\n");
  } catch {
    return "";
  }
}

/** env 值引用化:含空白/引号时加双引号(API key、URL 一般无需)。 */
function quoteEnv(v) {
  const s = String(v);
  return /[\s"']/.test(s) ? `"${s.replace(/"/g, '\\"')}"` : s;
}

/** 序列化 env 为 KEY=VALUE 文本(已知键固定顺序在前,其余保留)。 */
function serializeEnv(env) {
  const order = ["MEMHOP_LLM_API_URL", "MEMHOP_LLM_API_KEY", "MEMHOP_LLM_MODEL"];
  const lines = ["# MemHop 服务器环境变量 —— 由 dsh-memhop 面板「服务器 → 配置」管理"];
  for (const k of order) {
    if (env[k]) lines.push(`${k}=${quoteEnv(env[k])}`);
  }
  for (const [k, v] of Object.entries(env)) {
    if (!order.includes(k)) lines.push(`${k}=${quoteEnv(v)}`);
  }
  return lines.join("\n") + "\n";
}

/** 从 wrapper 脚本解析 memhop-mcp 启动参数(bin/dbDir/port)。 */
function parseWrapper(file) {
  const out = { bin: "", dbDir: "", port: 0 };
  try {
    const execLine = (readFileSync(file, "utf8").split("\n").find((l) => l.trim().startsWith("exec ")) || "");
    const binM = execLine.match(/^exec\s+["']?([^"'\s]+)/);
    if (binM) out.bin = binM[1];
    const flag = (name) => {
      const r = execLine.match(new RegExp(`-${name}\\s+["']?([^"'\\s]+)`));
      return r ? String(r[1]).replace(/["']$/, "") : "";
    };
    out.dbDir = flag("db-dir");
    const listen = flag("listen");
    out.port = listen ? Number(String(listen).split(":").pop()) || 0 : 0;
  } catch {
    /* 无 wrapper 视为空 */
  }
  return out;
}

/** API key 打码:仅显示首尾 4 位。 */
function maskSecret(s) {
  if (!s) return "";
  if (s.length <= 8) return `${s.slice(0, 2)}***`;
  return `${s.slice(0, 4)}…${s.slice(-4)}`;
}

function plistTemplate(wrapperPath, outLog, errLog) {
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>${MCP_PLIST_LABEL}</string>
  <key>ProgramArguments</key>
  <array><string>${wrapperPath}</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>${outLog}</string>
  <key>StandardErrorPath</key><string>${errLog}</string>
</dict>
</plist>
`;
}

function wrapperTemplate(binPath, dbDir, port) {
  return `#!/bin/bash
set -a; source "${homeDir()}/.memhop/server.env"; set +a
exec "${binPath}" -db-dir "${dbDir}" -transport streamable-http -listen "127.0.0.1:${port}"
`;
}

class MemhopServerControl extends Service {
  constructor(ctx, cfg) {
    super(ctx, "memhopServer");
    this.cfg = cfg;
    this.child = null; // 插件直接 spawn 的进程(非 launchd 模式)
  }

  get plistPath() {
    return `${homeDir()}/Library/LaunchAgents/${MCP_PLIST_LABEL}.plist`;
  }
  get wrapperPath() {
    return expandHome(this.cfg.serverWrapperPath);
  }
  get envFile() {
    return expandHome(this.cfg.serverEnvFile);
  }
  get outLog() {
    return expandHome(this.cfg.serverOutLog);
  }
  get errLog() {
    return expandHome(this.cfg.serverErrLog);
  }
  get dbDir() {
    return expandHome(this.cfg.dbDir);
  }
  get port() {
    return this.cfg.serverPort ?? 3939;
  }

  /** 探测可执行文件:显式配置 → wrapper 的 exec 行 → PATH。 */
  detectBin() {
    if (this.cfg.serverBin) return this.cfg.serverBin;
    try {
      for (const line of readFileSync(this.wrapperPath, "utf8").split("\n")) {
        const m = line.match(/exec\s+(['"]?)([^'"]+)\1/);
        if (m) return m[2].trim();
      }
    } catch {
      /* 无 wrapper,回退 PATH */
    }
    return "memhop-mcp";
  }

  launchdInstalled() {
    return existsSync(this.plistPath);
  }

  launchdLoaded() {
    try {
      const r = spawnSync("launchctl", ["print", `gui/${guiUid()}/${MCP_PLIST_LABEL}`], {
        encoding: "utf8",
        timeout: 5000,
      });
      return r.status === 0;
    } catch {
      return false;
    }
  }

  runningPids() {
    try {
      const r = spawnSync("pgrep", ["-f", "memhop-mcp"], { encoding: "utf8", timeout: 5000 });
      if (r.status !== 0) return [];
      return String(r.stdout)
        .trim()
        .split("\n")
        .map((s) => Number(s.trim()))
        .filter((n) => Number.isInteger(n) && n > 0);
    } catch {
      return [];
    }
  }

  portAlive() {
    return new Promise((resolve) => {
      const sock = net.connect({ host: "127.0.0.1", port: this.port });
      const done = (v) => {
        try {
          sock.destroy();
        } catch {
          /* noop */
        }
        resolve(v);
      };
      sock.setTimeout(1500, () => done(false));
      sock.once("connect", () => done(true));
      sock.once("error", () => done(false));
    });
  }

  async status() {
    const pids = this.runningPids();
    return {
      port: this.port,
      running: pids.length > 0,
      pids,
      health: (await this.portAlive()) ? "ok" : "down",
      launchdInstalled: this.launchdInstalled(),
      launchdLoaded: this.launchdLoaded(),
      launchdLabel: MCP_PLIST_LABEL,
      dbDir: this.dbDir,
      serverBin: this.detectBin(),
      wrapper: existsSync(this.wrapperPath) ? this.wrapperPath : null,
      envPresent: Object.keys(parseEnvFile(this.envFile)).length > 0,
      spawnMode: this.child ? "spawn" : this.launchdLoaded() ? "launchd" : pids.length > 0 ? "external" : "none",
    };
  }

  async start() {
    const st = await this.status();
    if (st.health === "ok" && st.running) {
      return { started: false, message: "服务器已在运行", status: st };
    }
    if (st.launchdInstalled) {
      const r = spawnSync("launchctl", ["bootstrap", `gui/${guiUid()}`, this.plistPath], {
        encoding: "utf8",
        timeout: 10000,
      });
      if (r.status === 0) {
        emit("server", "started via launchd");
        return { started: true, via: "launchd", status: await this.status() };
      }
      // bootstrap 失败(可能已加载)时检查状态
      if (this.launchdLoaded()) {
        emit("server", "launchd already loaded");
        return { started: true, via: "launchd", status: await this.status() };
      }
      return { started: false, message: String(r.stderr || r.stdout || "launchctl bootstrap failed") };
    }
    // 直接 spawn 兜底(不依赖 launchd):env 注入 server.env,日志落盘。
    const env = { ...process.env, ...parseEnvFile(this.envFile) };
    const args = [
      "-db-dir", this.dbDir,
      "-transport", "streamable-http",
      "-listen", `127.0.0.1:${this.port}`,
    ];
    try {
      const out = createWriteStream(this.outLog, { flags: "a" });
      const err = createWriteStream(this.errLog, { flags: "a" });
      const child = spawn(this.detectBin(), args, { env, stdio: ["ignore", out, err] });
      this.child = child;
      child.on("exit", () => {
        this.child = null;
      });
      child.on("error", (e) => {
        emit("server", `spawn failed: ${String(e?.message ?? e)}`);
      });
      emit("server", `spawned memhop-mcp pid=${child.pid}`);
      return { started: true, via: "spawn", pid: child.pid, status: await this.status() };
    } catch (e) {
      return { started: false, message: String(e?.message ?? e) };
    }
  }

  async stop() {
    const st = await this.status();
    if (st.launchdLoaded) {
      const r = spawnSync("launchctl", ["bootout", `gui/${guiUid()}/${MCP_PLIST_LABEL}`], {
        encoding: "utf8",
        timeout: 10000,
      });
      return { stopped: r.status === 0, via: "launchd", message: String(r.stderr || ""), status: await this.status() };
    }
    if (this.child) {
      try {
        this.child.kill("SIGTERM");
      } catch {
        /* noop */
      }
      this.child = null;
      return { stopped: true, via: "spawn", status: await this.status() };
    }
    for (const pid of st.pids) {
      try {
        process.kill(pid, "SIGTERM");
      } catch {
        /* noop */
      }
    }
    return { stopped: st.pids.length > 0, via: "kill", pids: st.pids, status: await this.status() };
  }

  /** 安装 launchd 常驻服务:wrapper + plist + bootstrap。 */
  installLaunchd() {
    const wrapper = this.wrapperPath;
    try {
      writeFileSync(
        wrapper,
        wrapperTemplate(this.detectBin(), this.dbDir, this.port)
      );
      writeFileSync(this.plistPath, plistTemplate(wrapper, this.outLog, this.errLog));
      spawnSync("chmod", ["755", wrapper], { timeout: 5000 });
    } catch (e) {
      return { installed: false, message: String(e?.message ?? e) };
    }
    const r = spawnSync("launchctl", ["bootstrap", `gui/${guiUid()}`, this.plistPath], {
      encoding: "utf8",
      timeout: 10000,
    });
    if (r.status !== 0 && !this.launchdLoaded()) {
      return { installed: true, loaded: false, message: String(r.stderr || "bootstrap failed") };
    }
    emit("server", "launchd installed & loaded");
    return { installed: true, loaded: true };
  }

  uninstallLaunchd() {
    try {
      if (this.launchdLoaded()) {
        spawnSync("launchctl", ["bootout", `gui/${guiUid()}/${MCP_PLIST_LABEL}`], { timeout: 10000 });
      }
      if (this.launchdInstalled()) rmSync(this.plistPath);
    } catch (e) {
      return { uninstalled: false, message: String(e?.message ?? e) };
    }
    return { uninstalled: true };
  }

  logs(limit) {
    const n = Math.max(1, Math.min(1000, Number(limit) || 100));
    return { out: tailFile(this.outLog, n), err: tailFile(this.errLog, n) };
  }

  /** 读取当前配置(env + wrapper 实参;API key 仅返回打码值)。 */
  getConfig() {
    const env = parseEnvFile(this.envFile);
    const w = parseWrapper(this.wrapperPath);
    return {
      envFile: this.envFile,
      wrapperPath: this.wrapperPath,
      llmApiUrl: env.MEMHOP_LLM_API_URL ?? "",
      llmApiKeyMasked: maskSecret(env.MEMHOP_LLM_API_KEY),
      llmApiKeySet: !!env.MEMHOP_LLM_API_KEY,
      llmModel: env.MEMHOP_LLM_MODEL ?? "",
      dbDir: w.dbDir || this.dbDir,
      port: w.port || this.port,
      bin: w.bin || this.detectBin(),
      serverUrl: this.cfg.serverUrl,
    };
  }

  /** 保存配置:合并写 server.env + 重写 wrapper,并重启服务使配置生效。 */
  async saveConfig(patch = {}) {
    const env = parseEnvFile(this.envFile);
    const w = parseWrapper(this.wrapperPath);
    const next = { ...env };
    const apply = (key, val) => {
      if (val !== undefined && val !== null && String(val).trim() !== "") {
        next[key] = String(val).trim();
      }
    };
    apply("MEMHOP_LLM_API_URL", patch.llmApiUrl);
    apply("MEMHOP_LLM_MODEL", patch.llmModel);
    apply("MEMHOP_LLM_API_KEY", patch.llmApiKey); // 空 = 保留原 key
    writeFileSync(this.envFile, serializeEnv(next));

    const dbDir = String(patch.dbDir || w.dbDir || this.dbDir);
    const port = Number(patch.port || w.port || this.port) || this.port;
    const bin = w.bin || this.detectBin();
    writeFileSync(this.wrapperPath, wrapperTemplate(bin, dbDir, port));
    spawnSync("chmod", ["755", this.wrapperPath], { timeout: 5000 });

    // 重启服务使配置生效(launchd 优先;其次插件 spawn 的 child;再次外部进程)。
    let restarted = false;
    let message = "配置已写入";
    try {
      const st = await this.status();
      if (st.launchdLoaded) {
        spawnSync("launchctl", ["bootout", `gui/${guiUid()}/${MCP_PLIST_LABEL}`], { timeout: 10000 });
        const r = spawnSync("launchctl", ["bootstrap", `gui/${guiUid()}`, this.plistPath], {
          encoding: "utf8",
          timeout: 10000,
        });
        restarted = r.status === 0 || this.launchdLoaded();
      } else if (this.child) {
        try {
          this.child.kill("SIGTERM");
        } catch {
          /* noop */
        }
        this.child = null;
        const started = await this.start();
        restarted = started.started;
      } else if (st.running) {
        for (const pid of st.pids) {
          try {
            process.kill(pid, "SIGTERM");
          } catch {
            /* noop */
          }
        }
        const started = await this.start();
        restarted = started.started;
      }
      if (restarted) message = "配置已写入,服务已重启生效";
    } catch (e) {
      message = `配置已写入,但服务重启失败: ${String(e?.message ?? e)}`;
    }
    return { ...this.getConfig(), saved: true, restarted, message, status: await this.status() };
  }
}

// ---- UI RPC 桥(原 dsh-client-memhop-ui host 半)----

const MEMHOP_RPC_NS = "memhop";
/** 允许的复合端点(方法名含 "/")。 */
const COMPOSITE_ENDPOINTS = new Set(["server/start", "server/stop", "server/install", "server/uninstall", "server/logs", "server/config", "server/save"]);

function rpcError(message, code = "command-error") {
  const error = { code, message };
  if (code === "bad-request") error.details = { issues: [] };
  else error.details = {};
  return { ok: false, error };
}

function rpcOk(value) {
  return { ok: true, value };
}

function logError(method, message) {
  try {
    // eslint-disable-next-line no-console
    console.error(`[memhop-ui] ${method}: ${message}`);
  } catch {
    /* noop */
  }
}

/** 解析目标 agent:优先显式 agentId,否则取最活跃的已就绪连接。 */
function resolveAgent(memhop, agentId) {
  if (agentId) return memhop.connection(agentId);
  const info = memhop.agentsInfo();
  const pick = info.find((a) => a.ready) ?? info[0] ?? null;
  return pick ? memhop.connection(pick.agentId) : null;
}

/** 等待连接 ready(面板打开瞬间连接可能仍在建立),最多等 timeoutMs。 */
function waitForReady(conn, timeoutMs) {
  return new Promise((resolve) => {
    if (conn.ready) return resolve(true);
    const started = Date.now();
    const timer = setInterval(() => {
      if (conn.ready) {
        clearInterval(timer);
        resolve(true);
      } else if (Date.now() - started >= timeoutMs) {
        clearInterval(timer);
        resolve(false);
      }
    }, 100);
  });
}

/**
 * 执行面板 RPC:返回统一 RpcResult(拦截器约定)。
 * @param {object} ctx 插件上下文(含 memhop / memhopServer 服务)
 * @param {string} endpoint `memhop/<method>`
 * @param {object} payload `{ args?, agentId? }`
 */
async function handleEndpoint(ctx, endpoint, payload) {
  // 兼容两种 endpoint 形态:独立通道 /memhop 直传 "knowledge_list";
  // 旧拦截器形态 "memhop/knowledge_list"(前缀切片)。
  let method = endpoint;
  if (method.startsWith(`${MEMHOP_RPC_NS}/`)) {
    method = method.slice(MEMHOP_RPC_NS.length + 1);
  }
  if (method === "" || (method.includes("/") && !COMPOSITE_ENDPOINTS.has(method))) {
    return rpcError(`invalid memhop endpoint ${JSON.stringify(endpoint)}`, "bad-request");
  }
  const memhop = ctx.memhop;
  if (!memhop || typeof memhop.connection !== "function") {
    logError("agents", "memhop core 服务不可用");
    return rpcError("memhop 服务不可用(dsh-memhop 未加载?)");
  }
  const args = payload?.args ?? {};

  if (method === "agents") {
    return rpcOk(memhop.agentsInfo());
  }
  if (method === "session") {
    const conn = resolveAgent(memhop, args.agentId);
    if (!conn) {
      logError("session", `no agent connection (agentId=${args.agentId ?? "(auto)"})`);
      return rpcError(`memhop: 没有可用 agent 连接(agentId=${args.agentId ?? "(auto)"})`);
    }
    return rpcOk({
      ...conn.state,
      agentId: conn.agent?.id,
      ready: conn.ready,
      searchPrefs: memhop.getSearchPrefs(conn.agent?.id),
    });
  }
  if (method === "prefs") {
    const agentId = args.agentId;
    const conn = agentId ? memhop.connection(agentId) : resolveAgent(memhop, agentId);
    if (!conn) {
      logError("prefs", `no agent connection (agentId=${agentId ?? "(auto)"})`);
      return rpcError(`memhop: 找不到 agent 连接(agentId=${agentId ?? "(auto)"})`);
    }
    const id = conn.agent?.id;
    if (args.prefs !== undefined) {
      return rpcOk(memhop.setSearchPrefs(id, args.prefs));
    }
    return rpcOk(memhop.getSearchPrefs(id));
  }

  // ---- 服务器管理端点(不依赖具体 agent)----
  if (method === "server") {
    return rpcOk(await ctx.memhopServer.status());
  }
  if (method === "server/start") {
    return rpcOk(await ctx.memhopServer.start());
  }
  if (method === "server/stop") {
    return rpcOk(await ctx.memhopServer.stop());
  }
  if (method === "server/install") {
    return rpcOk(ctx.memhopServer.installLaunchd());
  }
  if (method === "server/uninstall") {
    return rpcOk(ctx.memhopServer.uninstallLaunchd());
  }
  if (method === "server/logs") {
    return rpcOk(ctx.memhopServer.logs(args.limit));
  }
  if (method === "server/config") {
    return rpcOk(ctx.memhopServer.getConfig());
  }
  if (method === "server/save") {
    return rpcOk(await ctx.memhopServer.saveConfig(args));
  }

  // 其余:直连该 agent 的 memhop-mcp 调用 MCP 工具。
  const conn = resolveAgent(memhop, args.agentId);
  if (conn && !conn.ready) {
    // 面板打开瞬间连接可能仍在建立:懒补建 placeholder 的 start() 在
    // client.connect() 之后才置 ready,UI 首屏请求会撞上这个窗口
    // (日志实证:connect 与 ready 之间出现 not ready)。等就绪窗口后
    // 再判失败,避免"刚启动就报 agent 未连接"的竞态误报。
    await waitForReady(conn, 10000);
  }
  if (!conn || !conn.ready) {
    logError(method, `agent not ready (agentId=${args.agentId ?? "(auto)"})`);
    return rpcError(`memhop: agent 未连接(agentId=${args.agentId ?? "(auto)"})`);
  }
  const rawName = `memhop_${method}`;
  const toolArgs = { ...args };
  delete toolArgs.agentId;
  // dream 调 LLM 耗时长(多场景可达数分钟),放大超时避免误报失败;
  // 其余工具用 core 默认(120s)。
  const callTimeoutMs = method === "dream" ? 10 * 60 * 1000 : undefined;
  try {
    const value = await conn.call(rawName, toolArgs, callTimeoutMs);
    return rpcOk(value);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    logError(method, message);
    return rpcError(`memhop_${method}: ${message}`);
  }
}

export function apply(ctx, config = {}) {
  const cfg = { ...DEFAULTS, ...config };
  // 服务注册到 root ctx(cordis 服务查找沿 fiber.parent 链向上;兄弟插件
  // 的 fiber 是 root 的另一子,只有注册在 root 上才能被所有插件访问到)。
  const root = ctx.root ?? ctx;
  const service = new MemhopService(root, cfg);
  const serverControl = new MemhopServerControl(root, cfg);
  const connections = service.state.agents;
  /** agentId -> 该 agent 的记忆场景 id(场景 = 宿主会话;首轮由库分配后复用)。 */
  const sceneByAgent = new Map();
  /** agentId -> 本轮待沉淀的 { userText, userTS }(search 记录,update 消费一次)。 */
  const pendingTurnByAgent = new Map();
  /** agentId -> turn 计数(Dream 调度)。 */
  const turnsByAgent = new Map();
  /** Session id -> agentId(session/event 的 subject 是 Session,映射回 agent)。
   *  键用 session.id(字符串)而非对象引用:懒补建占位 session 与真 session 是
   *  不同对象但 id 相同(agentId === sessionId,1:1),对象引用键会导致
   *  session/event 永远查不到——update/turns 静默失效(日志曾实证该 bug)。 */
  const sessionToAgent = new Map();

  // ---- per-agent 生命周期 ----

  /**
   * P2:把记忆快照注册为该 agent 的 system prompt 动态 context 段。
   * 只在真 agent(有 ctx)上注册;agent 作用域销毁时自动清理。
   */
  function registerPromptContext(conn) {
    if (!cfg.promptSnapshot) return;
    const agent = conn.agent;
    if (!agent?.ctx?.systemPrompt || conn.promptRegistered) return;
    try {
      agent.ctx.systemPrompt.context({
        name: "memhop:snapshot",
        order: 150,
        text: () => renderSnapshot(conn.state.lastSearchResult ?? null, cfg.snapshotMaxChars),
      });
      conn.promptRegistered = true;
      emit("prompt", `agent=${agent.id} snapshot context registered`);
    } catch (err) {
      emit("warn", `agent=${agent.id} prompt context failed: ${String(err?.message ?? err)}`);
    }
  }

  /** 为一个 agent 建立 memhop 连接(幂等);已存在时补注册工具/prompt。 */
  function attachAgent(agent) {
    if (!agent) return;
    const existing = connections.get(agent.id);
    if (existing) {
      // 懒补建在前(占位对象,无 ctx)、真 agent 后到时:补注册工具 + prompt。
      existing.attachRealAgent(agent, () => registerPromptContext(existing));
      registerPromptContext(existing);
      return;
    }
    if (agent.session?.id) sessionToAgent.set(agent.session.id, agent.id);
    const conn = new AgentConnection(agent, cfg);
    connections.set(agent.id, conn);
    conn
      .start(agent.session?.id ?? agent.id)
      .then(() => registerPromptContext(conn))
      .catch((err) => {
        conn.state.lastError = String(err?.message ?? err);
        emit("error", `agent=${agent.id} connect failed: ${conn.state.lastError}`);
      });
  }

  /** 从 agents 服务按 id/sessionId 找回 agent;找不到返回 null。 */
  function findAgent(agentId) {
    try {
      for (const agent of ctx.agents.list()) {
        if (agent.id === agentId || agent.session?.id === agentId) return agent;
      }
    } catch (err) {
      emit("warn", `agents service scan failed: ${String(err?.message ?? err)}`);
    }
    return null;
  }

  /** 轮询 agents 服务找回真 agent(覆盖 session-start 事件已错过的场景),
   *  补注册工具与 prompt。连接已就绪且工具已注册时自动停止。 */
  function scheduleRealAgentLookup(agentId) {
    const delays = [2000, 4000, 8000, 16000];
    for (const ms of delays) {
      setTimeout(() => {
        const conn = connections.get(agentId);
        if (!conn || conn.state.toolsRegistered > 0 || conn.agent.ctx) return;
        const real = findAgent(agentId);
        if (real) {
          conn.attachRealAgent(real, () => registerPromptContext(conn));
          emit("info", `agent=${agentId} real agent attached via lookup`);
        }
      }, ms);
    }
  }

  ctx.on("agent/session-start", ({ agent }) => {
    attachAgent(agent);
  });

  // 懒补建钩子:任何插件(UI dock/面板)在事件竞态窗口内请求连接时,立即建连。
  // agentId 即 sessionId(1:1),连接只需要 agentId(tenant = 会话 ID,
  // 对应共享 memhop.meh 内的 agent 域),
  // 不需要 agents 服务——DSH 的 agents.list() 在会话打开瞬间可能尚未包含该
  // agent(日志实证:UI 首次请求早于 agents 注册),等待事件会错过窗口。
  // 优先取真 agent(有 ctx,工具可立即注册);取不到先用占位对象建连
  // (宿主侧记忆循环可用),真 agent 后到时由 attachRealAgent 补注册工具。
  // attachAgent 幂等判重;建连失败时连接留在 map(ready=false),后续请求
  // 走 "agent not ready" 而非反复 spawn,天然防循环。
  service.onNeedAgent = (agentId) => {
    const real = findAgent(agentId);
    if (real) {
      attachAgent(real);
      return;
    }
    emit("warn", `agent=${agentId} not in agents service, attaching placeholder`);
    attachAgent({ id: agentId, session: { id: agentId } });
    scheduleRealAgentLookup(agentId);
  };

  // 兜底:插件在 HMR/热重载后加入时,已有 agent 不会再触发 session-start,
  // 必须扫描当前 live agents 并补建连接。
  try {
    for (const agent of ctx.agents.list()) attachAgent(agent);
  } catch (err) {
    emit("warn", `initial agent scan failed: ${String(err?.message ?? err)}`);
  }

  ctx.on("agent/disposed", ({ agent }) => {
    const conn = connections.get(agent.id);
    if (!conn) return;
    connections.delete(agent.id);
    sceneByAgent.delete(agent.id);
    pendingTurnByAgent.delete(agent.id);
    turnsByAgent.delete(agent.id);
    if (agent.session?.id) sessionToAgent.delete(agent.session.id);
    conn.dispose().catch((err) => {
      emit("warn", `agent=${agent.id} dispose failed: ${String(err?.message ?? err)}`);
    });
  });

  // ---- 记忆循环自动化 ----
  // 1) turn 开始:自动 search(纯读该 agent 场景的话题快照),本轮原文暂存 pending
  //    供 update 一次性沉淀。
  ctx.on("agent/pre-step", async (payload, next) => {
    const decision = await next();
    if (!cfg.autoSearch) return decision;
    const agentId = payload.agent?.id;
    const conn = agentId ? connections.get(agentId) : null;
    if (!conn || !conn.ready || payload.step !== 1) return decision;
    // 提取本轮第一条非 tool-result 的 user 消息原文。
    const messages = decision.messages ?? [];
    const userText = stripInjected(
      messages
        .map((m) => m.content)
        .flat()
        .filter((b) => b && (b.type === "text" || b.type === "content")) // 保守过滤
        .map((b) => (typeof b.text === "string" ? b.text : ""))
        .join("")
        .trim()
    );
    if (!userText) return decision;
    try {
      // 读该 agent 的记忆场景(= 宿主会话):首次 scene_id 留空由库新建并回填,
      // 之后固定复用。面板偏好只剩 l3_id(项目域),仅在新建场景时挂锚。
      const prefs = service.getSearchPrefs(agentId);
      const known = sceneByAgent.get(agentId) ?? "";
      const searchArgs = { scene_id: known };
      if (!known && prefs.l3Id) searchArgs.l3_id = prefs.l3Id;
      const userTS = Date.now();
      const res = await conn.call("memhop_search", searchArgs);
      if (res?.scene?.scene_id) sceneByAgent.set(agentId, res.scene.scene_id);
      pendingTurnByAgent.set(agentId, { userText, userTS });
      conn.state.lastSearchAt = Date.now();
      conn.state.lastSearchResult = res;
      // P3:窗口控制——用快照遮蔽旧历史,保持上下文恒定。
      if (cfg.windowControl && res) {
        try {
          const snapshot = renderSnapshot(res, cfg.snapshotMaxChars);
          if (snapshot) {
            const session = payload.agent?.session;
            if (session) {
              const replaced = replaceHistory(session, snapshot, cfg);
              if (replaced > 0) {
                conn.state.lastWindowReplace = { at: Date.now(), shadowed: replaced };
                emit("window", `agent=${agentId} replaced ${replaced} nodes, keeping ${cfg.keepRecentNodes}`);
              }
            }
          }
        } catch (err) {
          emit("warn", `agent=${agentId} window control failed: ${String(err?.message ?? err)}`);
        }
      }
    } catch (err) {
      conn.state.lastError = String(err?.message ?? err);
      emit("warn", `agent=${agentId} auto search failed: ${conn.state.lastError}`);
    }
    return decision;
  });

  // 2) 最终回复后:自动 update(归档本轮回复到 search 返回的 topic)。
  ctx.on("session/event", (session, event) => {
    const agentId = sessionToAgent.get(session?.id);
    const conn = agentId ? connections.get(agentId) : null;
    if (!conn || !conn.ready) return;
    if (event.type === "assistant/message") {
      const message = event.data?.message;
      if (!message) return;
      const hasToolCall = (message.content ?? []).some((b) => b && b.type === "tool-call");
      if (hasToolCall || !cfg.autoUpdate) return;
      const replyText = (message.content ?? [])
        .filter((b) => b && b.type === "text")
        .map((b) => b.text ?? "")
        .join("")
        .trim();
      const pending = pendingTurnByAgent.get(agentId);
      const sceneId = sceneByAgent.get(agentId);
      if (!replyText || !sceneId || !pending) return;
      pendingTurnByAgent.delete(agentId);
      const agentTS = Date.now();
      conn
        .call("memhop_update", {
          scene_id: sceneId,
          user_text: pending.userText,
          user_ts: pending.userTS,
          agent_text: replyText,
          agent_ts: agentTS,
        })
        .then((res) => {
          conn.state.lastUpdateAt = Date.now();
          conn.state.topicId = res?.topic_id ?? null;
          emit("update", `agent=${agentId} archived ${replyText.length} chars -> topic ${res?.topic_id ?? "?"}`);
        })
        .catch((err) => {
          conn.state.lastError = String(err?.message ?? err);
          emit("warn", `agent=${agentId} auto update failed: ${conn.state.lastError}`);
        });
    } else if (event.type === "turn/start") {
      conn.state.turns += 1;
      turnsByAgent.set(agentId, conn.state.turns);
      conn.persistTurns();
    } else if (event.type === "turn/end") {
      maybeDream(agentId, conn);
    }
  });

  // 3) Dream 调度:turn 数阈值或空闲触发。失败记录错误,不阻塞循环
  // (调度本身不是业务操作,但错误必须可见)。
  // dream 会调 LLM(L2 压缩 + L0 蒸馏),多场景时可能远超默认 120s——
  // 用放大超时,避免客户端误报失败(Go 侧 context.Background 不受影响)。
  const DREAM_CALL_TIMEOUT_MS = 10 * 60 * 1000;
  function maybeDream(agentId, conn) {
    if (cfg.dreamEveryTurns <= 0) return;
    const turns = turnsByAgent.get(agentId) ?? 0;
    if (turns > 0 && turns % cfg.dreamEveryTurns === 0) {
      conn
        .call("memhop_dream", {}, DREAM_CALL_TIMEOUT_MS)
        .then(() => {
          conn.state.lastDreamAt = Date.now();
          emit("dream", `agent=${agentId} consolidated (turn ${turns})`);
        })
        .catch((err) => {
          conn.state.lastError = String(err?.message ?? err);
          emit("warn", `agent=${agentId} dream failed: ${conn.state.lastError}`);
        });
    }
  }

  // 空闲 Dream(挂钟时间)。
  if (cfg.idleDreamMs > 0) {
    const idleTimer = setInterval(() => {
      for (const [agentId, conn] of connections) {
        if (!conn.ready) continue;
        const idle = Date.now() - Math.max(conn.state.lastSearchAt, conn.state.lastUpdateAt);
        if (idle >= cfg.idleDreamMs && turnsByAgent.get(agentId) > 0) {
          conn
            .call("memhop_dream", {}, DREAM_CALL_TIMEOUT_MS)
            .then(() => {
              conn.state.lastDreamAt = Date.now();
              emit("dream", `agent=${agentId} idle consolidation`);
            })
            .catch((err) => {
              conn.state.lastError = String(err?.message ?? err);
              emit("warn", `agent=${agentId} idle dream failed: ${conn.state.lastError}`);
            });
        }
      }
    }, Math.min(cfg.idleDreamMs, 5 * 60 * 1000));
    idleTimer.unref?.();
    ctx.on("dispose", () => clearInterval(idleTimer));
  }

  // ---- UI RPC 桥 ----
  // 必须用独立单段通道 handle("/memhop"),不能用 intercept("/api") 或
  // handle("/api/memhop"):
  //  1. connection.rpc.intercept 的 /api 共享通道只允许一个拦截器
  //     (interceptors Map),dsh-api-gateway 已独占;重复注册会在 effect 里
  //     抛错并被吞掉,前端请求落入 fallback 返回 404。
  //  2. connection.rpc.handle 的 assertChannel 只接受单段通道
  //     (/^\/[A-Za-z0-9._~-]+$/),"/api/memhop" 含斜杠会被直接拒绝。
  // 前端 rpc.call("/memhop", "<method>") 的 URL 正是 /memhop/<method>,
  // 与 handle("/memhop") 的前缀路由、endpoint 提取、method 校验完全对齐。
  ctx.inject(["connection"], (connectionCtx) => {
    const disposer = connectionCtx.connection.rpc.handle(
      "/memhop",
      async (endpoint, payload) => handleEndpoint(ctx, endpoint, payload),
      { authority: "loopback" }
    );
    ctx.on("dispose", () => disposer());
  });

  emit("started", `dbDir=${cfg.dbDir} autoSearch=${cfg.autoSearch} autoUpdate=${cfg.autoUpdate} dreamEveryTurns=${cfg.dreamEveryTurns} promptSnapshot=${cfg.promptSnapshot}`);
  ctx.logger.info(`[dsh-memhop] started (dbDir=${cfg.dbDir})`);

  ctx.on("dispose", () => {
    for (const conn of connections.values()) {
      conn.dispose().catch((err) => {
        emit("warn", `agent=${conn.agent.id} dispose on plugin teardown failed: ${String(err?.message ?? err)}`);
      });
    }
    connections.clear();
    emit("stopped", "plugin disposed");
  });
}
