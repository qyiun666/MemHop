// MemHop UI plugin — host half (rewrite v0.3).
//
// 桥接 web 面板的 RPC 调用到 memhop 核心服务（dsh-memhop-core 的 ctx.memhop）：
//
//   - `/api` 通道 `memhop/agents`   -> ctx.memhop.agentsInfo()
//     （全部 agent 连接的只读快照，面板顶部 agent 选择器用）
//   - `/api` 通道 `memhop/session`  -> 指定 agent 的连接状态（agentId 参数）
//   - `/api` 通道 `memhop/prefs`    -> 读写该 agent 的自动 search 参数偏好
//     （autoCreate / directedL2Id / directedL3Id，发送对话时 core 自动携带）
//   - `/api` 通道 `memhop/<method>` -> AgentConnection.call("memhop_<method>", args)
//     （直连该 agent 的 memhop-mcp 进程，与模型工具调用同一套实现）
//
// 不走 tools registry：memhop 工具注册在 agent 作用域（agent.ctx.tools），
// 插件级 ctx.tools 查不到，因此面板操作直接复用 core 服务的 MCP 连接。

/** 逻辑通道前缀（挂在共享 /api 通道上）。 */
const MEMHOP_RPC_NS = "memhop";

/**
 * RPC 错误（必须符合 DSH 的 rpcErrorSchema 枚举，否则 client 侧 schema parse 直接抛错）：
 * - 通用业务失败用 `command-error`（details 为空对象）
 * - 参数类错误用 `bad-request`（details.issues 必填数组）
 */
function rpcError(message, code = "command-error") {
  const error = { code, message };
  if (code === "bad-request") error.details = { issues: [] };
  else error.details = {};
  return { ok: false, error };
}

/** 记录 host 半错误到 harness 日志（便于排查）。 */
function logError(method, message) {
  try {
    // eslint-disable-next-line no-console
    console.error(`[memhop-ui] ${method}: ${message}`);
  } catch {
    /* noop */
  }
}

function rpcOk(value) {
  return { ok: true, value };
}

/** 解析目标 agent：优先显式 agentId，否则取最活跃的已就绪连接。
 * agentsInfo() 返回的是只读快照（无 call 方法），必须回查 connection() 拿真实连接。 */
function resolveAgent(memhop, agentId) {
  if (agentId) return memhop.connection(agentId);
  const info = memhop.agentsInfo();
  const pick = info.find((a) => a.ready) ?? info[0] ?? null;
  return pick ? memhop.connection(pick.agentId) : null;
}

/**
 * 执行面板 RPC：返回统一 RpcResult（拦截器约定）。
 * @param {import("@deepseek-ai/cordis").Context} ctx 插件上下文
 * @param {string} endpoint `memhop/<method>`
 * @param {object} payload `{ args?, agentId? }`
 */
async function handleEndpoint(ctx, endpoint, payload) {
  const method = endpoint.slice(MEMHOP_RPC_NS.length + 1);
  if (method === "" || method.includes("/")) {
    return rpcError(`invalid memhop endpoint ${JSON.stringify(endpoint)}`, "bad-request");
  }
  const memhop = ctx.memhop;
  if (!memhop || typeof memhop.connection !== "function") {
    logError("agents", "memhop core 服务不可用");
    return rpcError("memhop core 服务不可用（dsh-memhop-core 未加载？）");
  }
  const args = payload?.args ?? {};

  if (method === "agents") {
    return rpcOk(memhop.agentsInfo());
  }
  if (method === "session") {
    const conn = resolveAgent(memhop, args.agentId);
    if (!conn) { logError("session", `no agent connection (agentId=${args.agentId ?? "(auto)"})`); return rpcError(`memhop: 没有可用 agent 连接（agentId=${args.agentId ?? "(auto)"}）`); }
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
    if (!conn) { logError("prefs", `no agent connection (agentId=${agentId ?? "(auto)"})`); return rpcError(`memhop: 找不到 agent 连接（agentId=${agentId ?? "(auto)"}）`); }
    const id = conn.agent?.id;
    if (args.prefs !== undefined) {
      return rpcOk(memhop.setSearchPrefs(id, args.prefs));
    }
    return rpcOk(memhop.getSearchPrefs(id));
  }

  // 其余：直连该 agent 的 memhop-mcp 调用 MCP 工具。
  const conn = resolveAgent(memhop, args.agentId);
  if (!conn || !conn.ready) {
    logError(method, `agent not ready (agentId=${args.agentId ?? "(auto)"})`);
    return rpcError(`memhop: agent 未连接（agentId=${args.agentId ?? "(auto)"}）`);
  }
  const rawName = `memhop_${method}`;
  const toolArgs = { ...args };
  delete toolArgs.agentId;
  // dream 调 LLM 耗时长（多场景可达数分钟），放大超时避免误报失败；
  // 其余工具用 core 默认（120s）。
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

/** Host 插件主体：拦截 /memhop/* endpoint 并转发到 memhop 核心服务。 */
function apply(ctx) {
  ctx.inject(["connection"], (connectionCtx) => {
    const disposer = connectionCtx.connection.rpc.intercept(
      "/api",
      (endpoint) => endpoint.startsWith(`${MEMHOP_RPC_NS}/`),
      async (endpoint, payload) => handleEndpoint(ctx, endpoint, payload),
      { authority: "loopback" }
    );
    ctx.on("dispose", () => disposer());
  });
}

export { apply };
