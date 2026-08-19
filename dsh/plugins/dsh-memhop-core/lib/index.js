// @deepseek-ai/dsh-memhop-core
//
// MemHop 记忆子系统 · 控制面插件（P1–P3：per-agent 数据库 + 记忆循环自动化 + 窗口控制）
//
// 部署模型（用户定版）：
//   DSH 会话（agent）──绑定──> 独立 .meh 文件（dbDir/<session-id>.meh）
//                                   └── 文件内 L2 可多场景（会话内主题划分）
//
// 职责：
//   1. per-agent 连接管理：agent/session-start 时连接常驻多租户 memhop-mcp
//      server（/mcp/<tenant-id>，tenant = 会话 ID，DB 由 server 懒创建于
//      --db-dir/<tenant-id>.meh）并注册 31 个 mcp__memhop__* 工具到 agent
//      作用域（agent.ctx.tools，shadow 全局）；agent/disposed 时先
//      memhop_checkpoint 落盘再断开。
//   2. 记忆循环自动化：turn 开始自动 search（写原文+取快照）、最终回复后自动
//      update（归档回复）、按策略调度 dream —— 全部宿主执行，不经主 LLM。
//   3. 窗口控制（P3）：每轮用"记忆快照"user 消息以 surfaceOp replace 遮蔽旧
//      历史，保留最近 N 条 surface 节点 —— LLM 上下文恒定，不再随对话爆炸。
//   4. 快照缓存：search 结果按 agent 缓存，供 UI/后续阶段消费。
//   5. 服务面：ctx.memhop（state / snapshot / call）。

import { Service } from "@deepseek-ai/cordis";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { ListToolsResultSchema } from "@modelcontextprotocol/sdk/types.js";
import { createUserMessage } from "@deepseek-ai/dsh-llm";
import { toolPairingBalancedAfter } from "@deepseek-ai/dsh-compaction";
import { readFileSync, writeFileSync } from "node:fs";

export const name = "dsh-memhop-core";

export const inject = ["tools", "systemPrompt", "agents"];

/** 默认配置：与 memhop-mcp 多租户 HTTP 接入对齐。 */
export const DEFAULTS = {
  /** 多租户 memhop-mcp server 地址（SSE/streamable-http）；server 以 --db-dir 启动。 */
  serverUrl: "http://127.0.0.1:3939",
  /** 每个 agent 一个 .meh 的存放目录（~ 展开）；须与 server 端 --db-dir 一致
   *  （turns 计数文件与 .meh 同目录，跨重启累计 dream 阈值）。 */
  dbDir: "~/.memhop/agents",
  /** MCP 工具调用超时（ms）。 */
  toolCallTimeoutMs: 120000,
  /** 工具名前缀。 */
  toolPrefix: "mcp__memhop__",
  /** P1：turn 开始自动 search。 */
  autoSearch: true,
  /** P1：最终回复后自动 update。 */
  autoUpdate: true,
  /** P1：Dream 自动触发阈值（turn 数，0=关闭）。 */
  dreamEveryTurns: 20,
  /** P1：空闲 Dream 触发阈值（ms，0=关闭）。 */
  idleDreamMs: 10 * 60 * 1000,
  /** P2：记忆快照最大字符数。 */
  snapshotMaxChars: 16000,
  /** P3：窗口控制——每轮用记忆快照遮蔽旧历史（上下文恒定）。 */
  windowControl: true,
  /** P3：surface 保留节点数（快照消息之外最近 N 条消息级节点）。 */
  keepRecentNodes: 40,
};

/** 直接写 stderr：harness-node 的 stderr 会被 desktop 壳转发到 harness.log。 */
function emit(tag, message) {
  try {
    process.stderr.write(`[memhop-core] ${tag}: ${message}\n`);
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

/** turns 计数持久化文件（<db 去 .meh>.turns.json）——dream 阈值跨重启累计。 */
function turnsFileFor(dbPath) {
  return String(dbPath || "").replace(/\.meh$/, "") + ".turns.json";
}

function loadTurns(dbPath) {
  try {
    const n = JSON.parse(readFileSync(turnsFileFor(dbPath), "utf8"))?.turns;
    return typeof n === "number" && Number.isFinite(n) ? Math.max(0, Math.floor(n)) : 0;
  } catch {
    return 0;
  }
}

/** 从 MCP 工具列表构造 ToolRuntime 定义（对照 dsh-mcp-client 的 createDefinition 精简版）。 */
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

/** 单个 agent 的 memhop 连接：进程 + MCP client + 工具注册。 */
class AgentConnection {
  constructor(agent, cfg) {
    this.agent = agent;
    this.cfg = cfg;
    this.client = null;
    this.transport = null;
    this.disposers = new Map();
    this.ready = false;
    this.state = {
      startedAt: Date.now(),
      dbPath: "",
      toolsRegistered: 0,
      lastSearchAt: 0,
      lastUpdateAt: 0,
      lastDreamAt: 0,
      turns: 0,
      lastError: null,
      topicId: null,
    };
  }

  dbPathFor(sessionId) {
    const dir = expandHome(this.cfg.dbDir);
    const safeId = String(sessionId || "default").replace(/[^A-Za-z0-9_-]/g, "_");
    return `${dir}/${safeId}.meh`;
  }

  async start(sessionId) {
    const safeId = String(sessionId || "default").replace(/[^A-Za-z0-9_-]/g, "_");
    const dbPath = this.dbPathFor(safeId);
    this.state.dbPath = dbPath;
    // 恢复跨重启的 turn 计数（dream 阈值累计；占位/重连不归零）。
    this.state.turns = loadTurns(dbPath);
    emit("connect", `agent=${this.agent.id} tenant=${safeId} db=${dbPath} turns=${this.state.turns}`);
    // 多租户接入：tenant = 清洗后的会话 ID，DB 由 server 端懒创建于
    // --db-dir/<tenant-id>.meh；不再由插件 spawn 子进程（encoder/LLM 配置
    // 随 server 进程启动，见 --embed-model / --encoder-addr / MEMHOP_LLM_*）。
    this.transport = new StreamableHTTPClientTransport({
      url: `${this.cfg.serverUrl}/mcp/${safeId}`,
    });
    this.client = new Client({ name: "dsh-memhop-core", version: "0.1.0" });
    await this.client.connect(this.transport);
    await this.registerTools();
    this.ready = true;
    emit("ready", `agent=${this.agent.id} tools=${this.state.toolsRegistered}`);
  }

  async registerTools() {
    const agentCtx = this.agent.ctx;
    if (!agentCtx?.tools) {
      // 懒补建阶段可能只有 {id, session} 占位对象（无 ctx）——工具注册延后，
      // 等真 agent 对象到达（session-start 事件或 agents 服务轮询）时由
      // attachRealAgent 补做。连接本身（宿主侧自动 search/update）不受影响。
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
    // 宿主级只读工具：当前会话的 memhop 数据库状态（UI 面板用）。
    // 名字避开 MCP 前缀 mcp__memhop__，直接用 memhop__ 前缀。
    const sessionDef = {
      name: "memhop__session",
      description:
        "只读：当前会话的 MemHop 数据库状态（agentId、.meh 路径、自动循环统计）。仅供 UI 面板调用，模型无需使用。",
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

  /** 真 agent 对象（带 ctx）到达时补注册工具——懒补建阶段只有占位对象。 */
  attachRealAgent(agent) {
    if (!agent?.ctx?.tools) return;
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
  }

  async call(rawName, args, timeoutMs) {
    if (!this.client || !this.ready) throw new Error(`memhop(${this.agent.id}) not connected`);
    const result = await this.client.callTool({ name: rawName, arguments: args ?? {} }, undefined, {
      timeout: timeoutMs ?? this.cfg.toolCallTimeoutMs,
    });
    if (result.isError === true) throw new Error(extractText(result.content, rawName));
    return parseResultContent(result.content);
  }

  /** 把 turn 计数写盘（turn/end 后调用；失败记录错误）。 */
  persistTurns() {
    try {
      if (!this.state.dbPath) return;
      writeFileSync(turnsFileFor(this.state.dbPath), JSON.stringify({ turns: this.state.turns }));
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
        // 多租户下 DB 常驻 server 进程，没有"会话结束即 Close"；主动 checkpoint
        // 触发索引快照落盘，保证 dispose 时数据已持久化。
        await this.call("memhop_checkpoint", {}, this.cfg.toolCallTimeoutMs);
      } catch (err) {
        emit("warn", `agent=${this.agent.id} checkpoint before close failed: ${String(err?.message ?? err)}`);
      }
    }
    try {
      // close 释放 MCP 会话；DB 数据已由上面的 checkpoint 落盘。
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

/** 解析 MCP 工具结果 content 为 JSON 值（text 块内 JSON 解析失败则原样返回）。 */
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

/** 渲染 search 结果为模型可读的记忆快照文本（截断到 maxChars）。 */
function renderSnapshot(res, maxChars) {
  if (!res) return "";
  const lines = [];
  lines.push("【MemHop 记忆快照】以下为本会话此前记忆的检索结果，供参考（以当前对话消息为准）。");
  const profile = res.profile;
  if (profile && (profile.name || profile.role || profile.personality)) {
    const bits = [profile.name, profile.role, profile.personality].filter(Boolean);
    lines.push(`画像: ${bits.join(" · ")}`);
  }
  const contexts = res.contexts ?? [];
  if (contexts.length > 0) {
    const topics = contexts
      .map((c) => (Array.isArray(c.user_keywords) ? c.user_keywords.slice(0, 8).join("/") : ""))
      .filter(Boolean);
    if (topics.length > 0) lines.push(`相关话题(${contexts.length}): ${topics.join(" | ")}`);
  }
  const archives = res.archives ?? [];
  for (const a of archives) {
    const who = a.role === 1 ? "助手" : "用户";
    const text = String(a.content ?? "").replace(/\s+/g, " ").trim();
    if (text) lines.push(`${who}: ${text}`);
  }
  let out = lines.join("\n");
  if (out.length > maxChars) out = `${out.slice(0, maxChars)}\n…(快照截断)`;
  return out;
}

/**
 * P3 窗口控制：用一条"记忆快照"user 消息以 surfaceOp replace 遮蔽旧历史，
 * 保留最近 keepRecentNodes 条 surface 节点。cut 位置保证 tool-pairing 平衡
 * （不会切断未配对的 tool_call/tool_result）。日志 append-only，遮蔽仅影响
 * 模型可见 surface，原始记录仍可审计。
 */
function replaceHistory(session, snapshotText, cfg) {
  const nodes = session.surface?.nodes;
  if (!Array.isArray(nodes) || nodes.length === 0) return 0;
  const keep = Math.max(8, cfg.keepRecentNodes);
  if (nodes.length <= keep) return 0; // 历史还不够长，暂不遮蔽
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

/** 服务面：ctx.memhop —— agent 级状态、快照查询与 search 参数偏好。 */
class MemhopService extends Service {
  constructor(ctx, config) {
    super(ctx, "memhop");
    this.config = config;
    this.state = {
      pluginStartedAt: Date.now(),
      agents: new Map(), // agentId -> AgentConnection
    };
    /** agentId -> 自动 search 参数偏好（UI 面板可读写，发送对话时携带）。 */
    this.searchPrefs = new Map(); // agentId -> { autoCreate?, directedL2Id?, directedL3Id? }
  }

  /**
   * 取 agent 连接；不存在时尝试懒补建（apply 里通过 onNeedAgent 挂接
   * attachAgent）。原因：会话打开瞬间 UI 插件（dock/面板）可能先于
   * agent/session-start 事件发起调用，事件驱动建连存在竞态窗口。
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

  /** 全部 agent 连接的只读快照（UI 面板 agent 选择器用）。 */
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
    // 最活跃的排最前（lastSearchAt 降序）。
    list.sort((a, b) => b.lastSearchAt - a.lastSearchAt);
    return list;
  }

  /** 设置某 agent 的自动 search 参数偏好（null 值字段清除）。 */
  setSearchPrefs(agentId, prefs) {
    const cur = this.searchPrefs.get(agentId) ?? {};
    const next = { ...cur };
    if (prefs === null) {
      this.searchPrefs.delete(agentId);
      return { ...cur };
    }
    if ("autoCreate" in prefs) next.autoCreate = prefs.autoCreate;
    if ("directedL2Id" in prefs) next.directedL2Id = prefs.directedL2Id || null;
    if ("directedL3Id" in prefs) next.directedL3Id = prefs.directedL3Id || null;
    this.searchPrefs.set(agentId, next);
    return { ...next };
  }

  getSearchPrefs(agentId) {
    return { ...(this.searchPrefs.get(agentId) ?? {}) };
  }
}

export function apply(ctx, config = {}) {
  const cfg = { ...DEFAULTS, ...config };
  // 服务注册到 root ctx（cordis 服务查找沿 fiber.parent 链向上；兄弟插件
  // 如 dsh-client-memhop-ui 的 fiber 是 root 的另一子，只有注册在 root 上
  // 才能被所有插件访问到）。
  const root = ctx.root ?? ctx;
  const service = new MemhopService(root, cfg);
  const connections = service.state.agents;
  /** agentId -> 当前 turn 的 topicId（search 返回，供 update 使用）。 */
  const topicByAgent = new Map();
  /** agentId -> turn 计数（Dream 调度）。 */
  const turnsByAgent = new Map();
  /** Session id -> agentId（session/event 的 subject 是 Session，映射回 agent）。
   *  键用 session.id（字符串）而非对象引用：懒补建占位 session 与真 session 是
   *  不同对象但 id 相同（agentId === sessionId，1:1），对象引用键会导致
   *  session/event 永远查不到——update/turns 静默失效（日志曾实证该 bug）。 */
  const sessionToAgent = new Map();

  // ---- per-agent 生命周期 ----

  /** 为一个 agent 建立 memhop 连接（幂等）；已存在时补注册工具（懒补建占位→真 agent）。 */
  function attachAgent(agent) {
    if (!agent) return;
    const existing = connections.get(agent.id);
    if (existing) {
      // 懒补建在前（占位对象，无 ctx）、真 agent 后到时：补注册工具。
      existing.attachRealAgent(agent);
      return;
    }
    if (agent.session?.id) sessionToAgent.set(agent.session.id, agent.id);
    const conn = new AgentConnection(agent, cfg);
    connections.set(agent.id, conn);
    conn
      .start(agent.session?.id ?? agent.id)
      .catch((err) => {
        conn.state.lastError = String(err?.message ?? err);
        emit("error", `agent=${agent.id} connect failed: ${conn.state.lastError}`);
      });
  }

  /** 从 agents 服务按 id/sessionId 找回 agent；找不到返回 null。 */
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

  /** 轮询 agents 服务找回真 agent（覆盖 session-start 事件已错过的场景），
   *  补注册工具。连接已就绪且工具已注册时自动停止。 */
  function scheduleRealAgentLookup(agentId) {
    const delays = [2000, 4000, 8000, 16000];
    for (const ms of delays) {
      setTimeout(() => {
        const conn = connections.get(agentId);
        if (!conn || conn.state.toolsRegistered > 0 || conn.agent.ctx) return;
        const real = findAgent(agentId);
        if (real) {
          conn.attachRealAgent(real);
          emit("info", `agent=${agentId} real agent attached via lookup`);
        }
      }, ms);
    }
  }

  ctx.on("agent/session-start", ({ agent, source }) => {
    attachAgent(agent);
  });

  // 懒补建钩子：任何插件（UI dock/面板）在事件竞态窗口内请求连接时，立即建连。
  // agentId 即 sessionId（1:1），连接只需要 agentId（db 名 = agentId + ".meh"），
  // 不需要 agents 服务——DSH 的 agents.list() 在会话打开瞬间可能尚未包含该
  // agent（日志实证：UI 首次请求早于 agents 注册），等待事件会错过窗口。
  // 优先取真 agent（有 ctx，工具可立即注册）；取不到先用占位对象建连
  // （宿主侧记忆循环可用），真 agent 后到时由 attachRealAgent 补注册工具。
  // attachAgent 幂等判重；建连失败时连接留在 map（ready=false），后续请求
  // 走 "agent not ready" 而非反复 spawn，天然防循环。
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

  // 兜底：插件在 HMR/热重载后加入时，已有 agent 不会再触发 session-start，
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
    topicByAgent.delete(agent.id);
    turnsByAgent.delete(agent.id);
    if (agent.session?.id) sessionToAgent.delete(agent.session.id);
    conn.dispose().catch((err) => {
      emit("warn", `agent=${agent.id} dispose failed: ${String(err?.message ?? err)}`);
    });
  });

  // ---- 记忆循环自动化 ----
  // 1) turn 开始：自动 search（写本轮原文 + 取快照），topicId 缓存供 update。
  ctx.on("agent/pre-step", async (payload, next) => {
    const decision = await next();
    if (!cfg.autoSearch) return decision;
    const agentId = payload.agent?.id;
    const conn = agentId ? connections.get(agentId) : null;
    if (!conn || !conn.ready || payload.step !== 1) return decision;
    // 提取本轮第一条非 tool-result 的 user 消息原文。
    const messages = decision.messages ?? [];
    const userText = messages
      .map((m) => m.content)
      .flat()
      .filter((b) => b && (b.type === "text" || b.type === "content")) // 保守过滤
      .map((b) => (typeof b.text === "string" ? b.text : ""))
      .join("")
      .trim();
    if (!userText) return decision;
    try {
      // 合并 UI 面板设置的 search 参数偏好（auto_create / 定向 L2 / 定向 L3）。
      // auto_create 默认关闭（复用已有场景/常规检索，不自动新建）：
      // 未设置（未保存过）按关闭处理，与 UI 指示条显示一致；仅显式开启才传 true。
      const prefs = service.getSearchPrefs(agentId);
      const searchArgs = { text: userText, timestamp: Date.now() };
      if (prefs.autoCreate === true) searchArgs.auto_create = true;
      if (prefs.directedL2Id) searchArgs.directed_l2_id = prefs.directedL2Id;
      if (prefs.directedL3Id) searchArgs.directed_l3_id = prefs.directedL3Id;
      const res = await conn.call("memhop_search", searchArgs);
      if (res && typeof res.new_topic_id === "string") {
        topicByAgent.set(agentId, res.new_topic_id);
      }
      conn.state.lastSearchAt = Date.now();
      conn.state.lastSearchResult = res;
      // P3：窗口控制——用快照遮蔽旧历史，保持上下文恒定。
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

  // 2) 最终回复后：自动 update（归档本轮回复到 search 返回的 topic）。
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
      const topicId = topicByAgent.get(agentId);
      if (!replyText || !topicId) return;
      conn
        .call("memhop_update", { topic_id: topicId, text: replyText, timestamp: Date.now() })
        .then(() => {
          conn.state.lastUpdateAt = Date.now();
          emit("update", `agent=${agentId} archived ${replyText.length} chars -> topic ${topicId}`);
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

  // 3) Dream 调度：turn 数阈值或空闲触发。失败记录错误，不阻塞循环
  // （调度本身不是业务操作，但错误必须可见）。
  // dream 会调 LLM（L2 压缩 + L0 蒸馏），多场景时可能远超默认 120s——
  // 用放大超时，避免客户端误报失败（Go 侧 context.Background 不受影响）。
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

  // 空闲 Dream（挂钟时间）。
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

  emit("started", `dbDir=${cfg.dbDir} autoSearch=${cfg.autoSearch} autoUpdate=${cfg.autoUpdate} dreamEveryTurns=${cfg.dreamEveryTurns}`);
  ctx.logger.info(`[memhop-core] started (dbDir=${cfg.dbDir})`);

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
