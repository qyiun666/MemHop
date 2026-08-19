// Panel.js — 主面板：顶栏（agent 选择 + 巩固操作）+ 页签容器。

const TABS = [
  { id: "status", label: "状态" },
  { id: "search", label: "搜索" },
  { id: "scene", label: "场景" },
  { id: "knowledge", label: "知识" },
  { id: "archive", label: "档案" },
  { id: "capability", label: "能力" },
  { id: "profile", label: "画像" },
  { id: "sleep", label: "睡眠" },
];

const tabStyle = (active) => ({
  padding: "5px 10px",
  borderRadius: 6,
  border: "none",
  background: active ? c.bgActive : "transparent",
  color: active ? c.textPrimary : c.textSecondary,
  cursor: "pointer",
  fontSize: 12,
  fontWeight: active ? 600 : 400,
});

/**
 * MemHop 记忆面板（P4 重做）：
 * - 状态页签：会话数据库自检（session + L0/L2/L3/L4/L5 读取工具连通性）
 * - 搜索页签：自动 search 参数偏好（auto_create / 定向 L2 / 定向 L3），
 *   保存后每轮发送对话都会携带这些参数
 * - 场景/知识/档案/能力/画像：浏览选中 agent 的 .meh 数据库各层内容
 * - 睡眠页签：勾选内存中的场景执行巩固（dream），可全选
 * - 顶栏：agent 选择器（多会话切换）
 * 记忆循环（search/update）由宿主自动执行，面板只做浏览与管理。
 */
function MemhopPanel({ rpc, sessions, sessionId, onClose }) {
  const [tab, setTab] = React.useState("status");
  const [toastMsg, setToastMsg] = React.useState(null);
  const [agents, setAgents] = React.useState([]);
  // 会话 tab 场景：初始即当前会话（agentId === sessionId，1:1）。
  const [agentId, setAgentId] = React.useState(sessionId || null);
  const [agentsError, setAgentsError] = React.useState(null);
  // 用户手动切换过 agent 后暂停自动跟随（重开面板恢复跟随）。
  const manualRef = React.useRef(false);

  // 会话 tab 由 conversation.view 注入 sessionId：切换会话时面板自动跟随。
  React.useEffect(() => {
    if (sessionId && !manualRef.current) {
      setAgentId((prev) => (prev === sessionId ? prev : sessionId));
    }
  }, [sessionId]);

  // 跟随 DSH 当前激活的会话：agentId === sessionId（1:1）。
  // sessions.list 是 client runtime 的 snapshot store，current 即当前选中会话。
  React.useEffect(() => {
    if (!sessions || !sessions.list) return;
    const apply = (state) => {
      const cur = state && state.current;
      if (cur && !manualRef.current) {
        setAgentId((prev) => (prev === cur ? prev : cur));
      }
    };
    apply(sessions.list.getSnapshot());
    return sessions.list.subscribe(apply);
  }, [sessions]);

  // 加载 agent 连接列表（host 半按 lastSearchAt 排序），作为跟随的兜底：
  // sessions 不可用或未选中会话时，默认选最活跃的已就绪连接。
  React.useEffect(() => {
    let alive = true;
    callMemhopJson(rpc, "agents", {})
      .then((list) => {
        if (!alive) return;
        const arr = Array.isArray(list) ? list : [];
        setAgents(arr);
        if (!manualRef.current && arr.length > 0) {
          setAgentId((prev) => prev || arr[0].agentId);
        }
      })
      .catch((e) => {
        if (alive) setAgentsError(e);
      });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rpc]);

  const toast = (msg) => {
    setToastMsg(String(msg));
    setTimeout(() => setToastMsg(null), 3000);
  };

  const sectionProps = { rpc, agentId, toast };
  const currentAgent = agents.find((a) => a.agentId === agentId);

  // tab 场景（onClose 为空）渲染为会话内页面：撑满容器；弹层场景保持 fixed 侧栏。
  const rootStyle = onClose
    ? overlay
    : { ...overlay, position: "relative", width: "100%", maxWidth: "none", height: "100%", minHeight: 0, borderLeft: "none", boxShadow: "none" };

  return React.createElement(
    "div",
    { style: rootStyle },
    // 顶栏
    React.createElement(
      "div",
      {
        style: {
          display: "flex",
          alignItems: "center",
          gap: 8,
          padding: "10px 12px",
          borderBottom: "1px solid " + c.border,
          background: c.bgInput,
        },
      },
      React.createElement("span", { "aria-hidden": true, style: { fontSize: 15 } }, "🧠"),
      React.createElement("span", { style: { fontWeight: 700, fontSize: 13 } }, "MemHop 记忆面板"),
      React.createElement(
        "select",
        {
          style: { ...input, flex: 1, minWidth: 0, fontSize: 11, padding: "3px 6px" },
          value: agentId || "",
          onChange: (e) => {
            manualRef.current = true;
            setAgentId(e.target.value || null);
          },
          title: "目标会话（agent）——所有页签操作都作用于该会话的 .meh 数据库（默认跟随 DSH 当前会话）",
        },
        agents.length === 0
          ? React.createElement("option", { value: "" }, agentsError ? "加载失败" : "加载中…")
          : agents.map((a) =>
              React.createElement(
                "option",
                { key: a.agentId, value: a.agentId },
                (a.ready ? "● " : "○ ") + String(a.agentId).slice(0, 13) + "…"
              )
            )
      ),
      onClose
        ? React.createElement(
            "button",
            {
              type: "button",
              style: { ...buttonStyle(), padding: "3px 8px", fontSize: 13, lineHeight: "16px" },
              onClick: onClose,
              title: "关闭面板",
            },
            "✕"
          )
        : null
    ),
    currentAgent && currentAgent.dbPath
      ? React.createElement(
          "div",
          { style: { ...faint, padding: "4px 12px", borderBottom: "1px solid " + c.border, fontSize: 10, wordBreak: "break-all" } },
          "db: " + currentAgent.dbPath
        )
      : null,
    // 页签
    React.createElement(
      "div",
      { style: { display: "flex", gap: 2, padding: "8px 12px 0", borderBottom: "1px solid " + c.border } },
      TABS.map((t) =>
        React.createElement(
          "button",
          { key: t.id, type: "button", style: tabStyle(tab === t.id), onClick: () => setTab(t.id) },
          t.label
        )
      )
    ),
    // 内容
    React.createElement(
      "div",
      { style: { flex: 1, overflowY: "auto", padding: 10 } },
      !agentId
        ? React.createElement(
            "div",
            { style: { ...faint, padding: 12 } },
            agentsError ? "加载 agent 列表失败: " + str(agentsError) : "等待 agent 连接…"
          )
        : React.createElement(
            React.Fragment,
            null,
            tab === "status" ? React.createElement(StatusSection, sectionProps) : null,
            tab === "search" ? React.createElement(SearchPrefsSection, sectionProps) : null,
            tab === "scene" ? React.createElement(SceneSection, sectionProps) : null,
            tab === "knowledge" ? React.createElement(KnowledgeSection, sectionProps) : null,
            tab === "archive" ? React.createElement(ArchiveSection, sectionProps) : null,
            tab === "capability" ? React.createElement(CapabilitySection, sectionProps) : null,
            tab === "profile" ? React.createElement(ProfileSection, sectionProps) : null,
            tab === "sleep" ? React.createElement(SleepSection, sectionProps) : null
          )
    ),
    // Toast
    toastMsg
      ? React.createElement(
          "div",
          {
            style: {
              position: "absolute",
              bottom: 16,
              left: 12,
              right: 12,
              background: c.bgInput,
              border: "1px solid " + c.border,
              borderRadius: 8,
              padding: "8px 12px",
              fontSize: 12,
              boxShadow: "0 4px 16px " + c.shadow,
              wordBreak: "break-word",
            },
          },
          toastMsg
        )
      : null
  );
}


/**
 * 会话内 tab 视图（conversation.view id="memhop"）：
 * 出现在对话 / 轨迹旁边，天然跟随当前会话——inject 直接携带 sessionId
 * （agentId === sessionId，1:1），DSH 切到哪个会话，这个 tab 就属于哪个会话。
 */
function MemhopTab({ rpc, sessionId }) {
  return React.createElement(
    "div",
    { style: { display: "flex", flexDirection: "column", height: "100%", minHeight: 0, position: "relative" } },
    React.createElement(MemhopPanel, { rpc, sessionId })
  );
}
