// SearchChip.js — 输入框（conversation.composer.dock）内的自动 search 参数指示条。
// 实时显示当前会话的检索偏好：auto_create 开/关 + 定向 L2 场景 + 定向 L3 图谱，
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
  const [scenes, setScenes] = React.useState([]);
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
    callMemhopJson(rpc, "scene_list", {}, sessionId)
      .then((v) => {
        if (alive) setScenes(Array.isArray(v) ? v : []);
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
  const autoOn = p.autoCreate === true; // 默认关闭
  const scene = scenes.find((s) => s.scene_id === p.directedL2Id);
  const graph = graphs.find((g) => g.id_hash === p.directedL3Id);

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
          "检索参数加载失败: " + str(error)
        )
      : prefs === null
        ? React.createElement("span", { style: { fontSize: 11, color: "var(--dsw-alias-label-tertiary)" } }, "加载中…")
        : React.createElement(
            React.Fragment,
            null,
            chip(autoOn ? "auto_create: 开" : "auto_create: 关", autoOn),
            chip("场景: " + (scene ? scene.scene_name : "不限定"), !!scene),
            chip("图谱: " + (graph ? graph.name : "不限定"), !!graph)
          )
  );
}
