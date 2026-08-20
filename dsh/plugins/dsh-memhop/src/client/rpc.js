// rpc.js — client → host RPC 桥（与 lib/index.js 的 handle("/memhop") 对应）。
// 注意:必须用独立单段通道 "/memhop" 而非共享通道 "/api"。
//  1. /api 共享通道的 intercept 只允许一个拦截器,dsh-api-gateway 已独占;
//  2. handle() 的 assertChannel 只接受单段通道(/^\/[A-Za-z0-9._~-]+$/),
//     "/api/memhop" 含斜杠会被直接拒绝。
// 因此前端以 rpc.call("/memhop", "<method>") 走官方独立通道,
// host 侧 connection.rpc.handle("/memhop", ...) 接收,互不冲突。

/**
 * 调用一个 memhop 端点（经 host 桥转发到 core 服务）。
 * @param {object} rpc - client connection 的 rpc 调用器。
 * @param {string} method - 端点名（如 `session`、`agents`、`prefs`、`scene_list`）。
 * @param {object} [args] - 工具参数。
 * @param {string} [agentId] - 目标 agent（缺省由 host 半自动选最活跃连接）。
 * @returns {Promise<*>} 工具结果值。
 */
async function callMemhop(rpc, method, args, agentId) {
  const response = await rpc.call(
    "/memhop",
    method,
    { args: agentId ? { ...(args ?? {}), agentId } : (args ?? {}) }
  );
  if (!response.ok) {
    throw new Error(`memhop.${method}: ${response.error?.message ?? "unknown error"}`);
  }
  return response.value;
}

/** 同 callMemhop，但把字符串结果按 JSON 解析后返回。 */
async function callMemhopJson(rpc, method, args, agentId) {
  const value = await callMemhop(rpc, method, args, agentId);
  if (typeof value === "string") {
    try {
      return JSON.parse(value);
    } catch {
      return value;
    }
  }
  return value;
}

/** 格式化时间为 `HH:MM:SS`。 */
function fmtTime(ms) {
  if (!ms) return "—";
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

/** 格式化时间为正式时间 `YYYY-MM-DD HH:MM:SS`（时间戳转可读时间）。 */
function fmtFullTime(ms) {
  if (!ms) return "—";
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

/** 格式化毫秒时长为 `Xd Xh Xm Xs`（<=0 显示 —）。 */
function fmtDuration(ms) {
  if (!ms || ms <= 0) return "—";
  const s = Math.floor(ms / 1000);
  const parts = [];
  const days = Math.floor(s / 86400);
  const hours = Math.floor((s % 86400) / 3600);
  const mins = Math.floor((s % 3600) / 60);
  const secs = s % 60;
  if (days) parts.push(days + "d");
  if (hours) parts.push(hours + "h");
  if (mins) parts.push(mins + "m");
  if (secs || parts.length === 0) parts.push(secs + "s");
  return parts.join(" ");
}

/** 安全字符串化（Error → message，对象 → JSON，其他 → String）。 */
function str(v) {
  if (v instanceof Error) return v.message || String(v);
  if (v === null || v === undefined) return "";
  if (typeof v === "object") {
    try {
      return JSON.stringify(v);
    } catch {
      return String(v);
    }
  }
  return String(v);
}
