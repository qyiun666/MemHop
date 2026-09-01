// SearchChip.js — 输入框（conversation.composer.dock）内的记忆参数指示条。
// 实时显示当前 agent 的项目域偏好（l3_id，仅在新建记忆场景时挂锚）；
// 保存后（面板广播 memhop:prefs-saved 事件）立即刷新。
// 错误处理原则：失败就显示错误，不做退避重试、不静默吞错。

const chipStyle = {
  display: "inline-flex",
  alignItems: "center",
  gap: 4,
  fontSize: 11,
  lineHeight: "16px",
  color: "var(--dsw-alias-label-tertiary)",
  background: "var(--dsw-alias-interactive-bg-hover)",
  borderRadius: 999,
  padding: "0 8px",
  whiteSpace: "nowrap",
};

const chipActiveStyle = {
  ...chipStyle,
  color: "var(--dsw-alias-state-business-primary)",
};

function SearchPrefsChip({ rpc, sessionId }) {
  const [prefs, setPrefs] = React.useState(null);
  const [graphs, setGraphs] = React.useState([]);
  const [error, setError] = React.useState(null);

  // 一次性加载：任一请求失败立即显示真实错误，不重试。
  const load = React.useCallback(() => {
    if (!rpc || !sessionId) return undefined;
    let alive = true;
    callMemhopJson(rpc, "prefs", {}, sessionId)
      .then((v) => {
        if (alive) setPrefs(v || {});
      })
      .catch((e) => {
        if (alive) setError(e);
      });
    callMemhopJson(rpc, "knowledge_list", {}, sessionId)
      .then((v) => {
        if (alive) setGraphs(Array.isArray(v) ? v : []);
      })
      .catch((e) => {
        if (alive) setError(e);
      });
    return () => {
      alive = false;
    };
  }, [rpc, sessionId]);

  // 挂载或会话切换时加载。
  React.useEffect(() => {
    return load();
  }, [load]);

  // 面板「保存 search 参数」成功后广播，这里同步刷新。
  React.useEffect(() => {
    const onSaved = () => {
      setError(null);
      load();
    };
    window.addEventListener("memhop:prefs-saved", onSaved);
    return () => window.removeEventListener("memhop:prefs-saved", onSaved);
  }, [load]);

  if (!rpc || !sessionId) return null;

  const p = prefs || {};
  const graph = graphs.find((g) => g.id_hash === p.l3Id);

  const chip = (text, active) =>
    React.createElement(
      "span",
      { style: active ? chipActiveStyle : chipStyle, title: text },
      text
    );

  return React.createElement(
    "div",
    { style: { display: "flex", alignItems: "center", gap: 6, padding: "2px 10px", minWidth: 0, overflow: "hidden" } },
    React.createElement("span", { style: { fontSize: 11, color: "var(--dsw-alias-label-tertiary)", flex: "none" } }, "🧠"),
    error
      ? React.createElement(
          "span",
          { style: { fontSize: 11, color: "var(--dsw-alias-state-error-primary)" }, title: str(error) },
          "记忆参数加载失败: " + str(error)
        )
      : prefs === null
        ? React.createElement("span", { style: { fontSize: 11, color: "var(--dsw-alias-label-tertiary)" } }, "加载中…")
        : React.createElement(
            React.Fragment,
            null,
            chip("检索本会话记忆（场景=宿主会话）", true),
            chip("新项目域: " + (graph ? graph.name : "未设"), !!graph)
          )
  );
}
