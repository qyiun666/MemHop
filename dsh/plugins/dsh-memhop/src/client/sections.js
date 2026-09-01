// sections.js — 面板各分区组件（状态自检/场景/知识/档案/能力/画像）。
// 所有数据均来自当前会话的 memhop 数据库（agent 作用域工具）。

// ---------- 通用小组件 ----------

function ErrBox({ error }) {
  if (!error) return null;
  return React.createElement(
    "div",
    { style: { color: c.danger, fontSize: 11, margin: "4px 0", wordBreak: "break-word" } },
    "⚠ " + str(error)
  );
}

function Loading({ label }) {
  return React.createElement("div", { style: muted }, label ? "加载中… " + label : "加载中…");
}

function useLoad(rpc, method, args, agentId, deps) {
  const [data, setData] = React.useState(null);
  const [error, setError] = React.useState(null);
  const [loading, setLoading] = React.useState(false);
  const [tick, setTick] = React.useState(0);
  const reload = React.useCallback(() => setTick((t) => t + 1), []);
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError(null);
    callMemhopJson(rpc, method, args, agentId)
      .then((v) => {
        if (alive) setData(v);
      })
      .catch((e) => {
        if (alive) setError(e);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rpc, method, tick, agentId]);
  return { data, error, loading, reload };
}

function SectionHeader({ title, onReload, loading, extra }) {
  return React.createElement(
    "div",
    { style: { display: "flex", alignItems: "center", gap: 8, marginBottom: 8 } },
    React.createElement("span", { style: { fontWeight: 600, fontSize: 12 } }, title),
    React.createElement(
      "button",
      { type: "button", style: buttonStyle(), onClick: onReload, disabled: loading },
      loading ? "…" : "刷新"
    ),
    extra || null
  );
}

function fmtHex(id) {
  return id === null || id === undefined ? "—" : str(id);
}

// ---------- 状态页签：会话数据库自检 ----------

function StatusSection({ rpc, agentId, toast }) {
  const sess = useLoad(rpc, "session", {}, agentId);
  const st = useLoad(rpc, "status", {}, agentId);
  const [checks, setChecks] = React.useState(null);
  const [checking, setChecking] = React.useState(false);

  const runCheck = async () => {
    setChecking(true);
    const results = [];
    const jobs = [
      ["L2 场景", "scene_list", {}],
      ["L3 知识图谱", "knowledge_list", {}],
      ["L4 档案（时间区间）", "archive_search", { start: 0, end: Date.now() }],
      ["L5 能力", "capability_list", {}],
      ["L0 画像", "profile_get", {}],
    ];
    for (const [label, method, args] of jobs) {
      try {
        const v = await callMemhopJson(rpc, method, args, agentId);
        const n = Array.isArray(v) ? v.length : v && typeof v === "object" ? Object.keys(v).length : 1;
        results.push({ label, ok: true, count: n });
      } catch (e) {
        results.push({ label, ok: false, error: e });
      }
    }
    setChecks(results);
    setChecking(false);
  };

  const s = sess.data || {};
  const stt = st.data || {};
  const okCount = (checks || []).filter((r) => r.ok).length;

  return React.createElement(
    React.Fragment,
    null,
    React.createElement(
      "div",
      { style: card },
      React.createElement("div", { style: { fontWeight: 600, fontSize: 12, marginBottom: 6 } }, "会话数据库"),
      React.createElement(
        "div",
        { style: muted },
        "agent: ",
        React.createElement("span", { style: mono }, str(s.agentId) || "…")
      ),
      React.createElement(
        "div",
        { style: muted },
        "db: ",
        React.createElement("span", { style: mono }, str(s.dbPath) || "…")
      ),
      React.createElement(
        "div",
        { style: { marginTop: 4 } },
        React.createElement("span", { style: chip }, s.ready ? "connected" : "connecting"),
        React.createElement("span", { style: chip }, "tools=" + str(s.toolsRegistered)),
        React.createElement("span", { style: chip }, stt.closed ? "closed" : "open"),
        React.createElement("span", { style: chip }, (stt.scene_count ?? 0) > 0 ? `scenes ${stt.scene_count}` : "no scenes"),
        s.lastError
          ? React.createElement("span", { style: { ...chip, color: c.danger } }, "err")
          : null
      ),
      React.createElement(
        "div",
        { style: { ...muted, marginTop: 6, lineHeight: "16px" } },
        "自动循环：search ",
        fmtTime(s.lastSearchAt),
        " · update ",
        fmtTime(s.lastUpdateAt),
        " · dream ",
        fmtTime(s.lastDreamAt),
        " · turns ",
        str(s.turns)
      )
    ),
    React.createElement(
      "div",
      { style: card },
      React.createElement(
        "div",
        { style: { display: "flex", alignItems: "center", gap: 8, marginBottom: 6 } },
        React.createElement("span", { style: { fontWeight: 600, fontSize: 12 } }, "数据库连通性自检"),
        React.createElement(
          "button",
          { type: "button", style: buttonStyle("primary"), onClick: runCheck, disabled: checking },
          checking ? "自检中…" : "运行自检"
        )
      ),
      checks
        ? React.createElement(
            "div",
            null,
            checks.map((r) =>
              React.createElement(
                "div",
                { key: r.label, style: { fontSize: 11, lineHeight: "18px", display: "flex", gap: 6, alignItems: "center" } },
                React.createElement(
                  "span",
                  { style: { color: r.ok ? c.success : c.danger, fontWeight: 600 } },
                  r.ok ? "✓" : "✗"
                ),
                React.createElement("span", { style: { color: c.textSecondary, minWidth: 110 } }, r.label),
                r.ok
                  ? React.createElement("span", { style: faint }, "ok" + (r.count !== undefined ? " (" + r.count + ")" : ""))
                  : React.createElement("span", { style: { color: c.danger, wordBreak: "break-all" } }, str(r.error))
              )
            ),
            React.createElement(
              "div",
              { style: { ...faint, marginTop: 6 } },
              okCount + "/" + checks.length + " 层连通"
            )
          )
        : React.createElement("div", { style: faint }, "点击运行：依次调用 L0/L2/L3/L4/L5 读取工具，验证本会话 .meh 数据库各层可读。")
    )
  );
}

// ---------- 场景页签 ----------

function SceneSection({ rpc, agentId, toast }) {
  const { data, error, loading, reload } = useLoad(rpc, "scene_list", {}, agentId);
  const [mergeSel, setMergeSel] = React.useState({});
  const [primary, setPrimary] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [openScene, setOpenScene] = React.useState(null);

  const scenes = Array.isArray(data) ? data : [];
  const toggle = (id) => {
    const next = { ...mergeSel };
    if (next[id]) delete next[id];
    else next[id] = true;
    setMergeSel(next);
  };
  const doMerge = async () => {
    const secondaries = Object.keys(mergeSel);
    if (!primary || secondaries.length === 0) {
      toast("请选择主场景和至少一个次场景");
      return;
    }
    if (secondaries.includes(primary)) {
      toast("主场景不能同时选为次场景，请先取消主场景的勾选");
      return;
    }
    setBusy(true);
    try {
      await callMemhopJson(rpc, "scene_merge", { primary_id: primary, secondary_ids: secondaries }, agentId);
      toast("合并完成");
      setMergeSel({});
      setPrimary("");
      reload();
    } catch (e) {
      toast("合并失败: " + str(e));
    } finally {
      setBusy(false);
    }
  };

  return React.createElement(
    React.Fragment,
    null,
    React.createElement(SectionHeader, { title: "L2 场景", onReload: reload, loading }),
    React.createElement(ErrBox, { error }),
    loading && !data ? React.createElement(Loading, null) : null,
    scenes.length === 0 && !loading
      ? React.createElement("div", { style: faint }, "暂无场景（首次 search 时会自动创建）。")
      : null,
    scenes.map((sc) => {
      const isOpen = openScene === sc.scene_id;
      return React.createElement(
        React.Fragment,
        { key: sc.scene_id },
        React.createElement(
          "div",
          { style: { ...card, display: "flex", alignItems: "center", gap: 8 } },
          React.createElement("input", {
            type: "checkbox",
            checked: !!mergeSel[sc.scene_id],
            disabled: primary === sc.scene_id,
            onChange: () => toggle(sc.scene_id),
            title: primary === sc.scene_id ? "主场景不能同时选为次场景" : "选为次场景（合并）",
          }),
          React.createElement(
            "button",
            {
              type: "button",
              style: {
                ...buttonStyle(),
                fontSize: 10,
                padding: "1px 6px",
                background: primary === sc.scene_id ? c.btnPrimary : undefined,
                color: primary === sc.scene_id ? c.fgOnPrimary : undefined,
                border: primary === sc.scene_id ? "none" : undefined,
              },
              onClick: () => {
                // 设为主场景时自动取消该场景的"次场景"勾选（互斥）。
                const next = { ...mergeSel };
                delete next[sc.scene_id];
                setMergeSel(next);
                setPrimary(primary === sc.scene_id ? "" : sc.scene_id);
              },
              title: "设为主场景",
            },
            "主"
          ),
          React.createElement(
            "div",
            { style: { flex: 1, minWidth: 0 } },
            React.createElement("div", { style: { fontSize: 12, fontWeight: 500, wordBreak: "break-all" } }, sc.scene_name || "—"),
            React.createElement("div", { style: faint },
              fmtHex(sc.scene_id),
              " · depth1 ",
              React.createElement("b", null, typeof sc.topic_count === "number" ? sc.topic_count : 0),
              " 条"
            )
          ),
          React.createElement(
            "button",
            {
              type: "button",
              style: buttonStyle(),
              onClick: () => setOpenScene(isOpen ? null : sc.scene_id),
            },
            isOpen ? "收起" : "查看"
          )
        ),
        isOpen
          ? React.createElement(SceneDetail, { rpc, agentId, sceneId: sc.scene_id, name: sc.scene_name })
          : null
      );
    }),
    Object.keys(mergeSel).length > 0 || primary
      ? React.createElement(
          "div",
          { style: { ...card, background: c.bgActive } },
          React.createElement("div", { style: muted, marginBottom: 6 },
            "合并：主场景 ",
            React.createElement("b", null, primary || "未选"),
            " ← 次场景 ",
            Object.keys(mergeSel).length,
            " 个"),
          React.createElement(
            "button",
            { type: "button", style: buttonStyle("danger"), onClick: doMerge, disabled: busy || !primary || Object.keys(mergeSel).length === 0 },
            busy ? "合并中…" : "执行合并"
          )
        )
      : null,
    React.createElement(RestoreCard, { rpc, agentId, toast, onRestored: reload })
  );
}

// ---------- 场景上下文查看 ----------

// SceneDetail renders one scene's L2 topic metadata (from memhop_scene_topics,
// pure L2, no messages) with an on-demand 「原文」 button that fetches the
// topic's L4 archive messages via memhop_archive_search (topic_id filter).
function SceneDetail({ rpc, agentId, sceneId, name }) {
  const { data, error, loading, reload } = useLoad(rpc, "scene_topics", { scene_id: sceneId }, agentId);
  const [openTopic, setOpenTopic] = React.useState(null); // { topic_id, loading, msgs, error }
  const all = data && Array.isArray(data.topics) ? data.topics : [];
  // SceneContext 已按用户首条时间升序;话题含 dream 压缩层级
  // (depth 0 = 原始话题、1 = 压缩组、2+ = 再次压缩),全部列出。
  const topics = all;
  // dream 压缩话题：带子节点（child_count 非空）的 depth1 根话题，
  // 消息即合并摘要（L4 dream archive）。
  const isFused = (tp) => (tp.child_count || 0) > 0;

  // 「全文」按需拉取:优先用话题携带的 L4 档案 ID 列表一次批量取原文
  // (memhop_archive_search ids 模式);无 l4_ids(旧版数据/压缩摘要)
  // 时回退按 topic_id + 时间区间检索。
  const toggleOriginal = async (tp) => {
    if (openTopic && openTopic.topic_id === tp.topic_id) {
      setOpenTopic(null);
      return;
    }
    setOpenTopic({ topic_id: tp.topic_id, loading: true, msgs: [], error: null });
    try {
      const l4ids = Array.isArray(tp.l4_ids) ? tp.l4_ids.filter((x) => x) : [];
      const msgs = l4ids.length > 0
        ? await callMemhopJson(rpc, "archive_search", { ids: l4ids }, agentId)
        : await callMemhopJson(rpc, "archive_search", { topic_id: tp.topic_id, start: 0, end: Date.now() }, agentId);
      const list = Array.isArray(msgs) ? msgs : [];
      // 聊天记录格式:按时间升序排列。
      list.sort((a, b) => (Number(a.created_at) || 0) - (Number(b.created_at) || 0));
      setOpenTopic({ topic_id: tp.topic_id, loading: false, msgs: list, error: null });
    } catch (e) {
      setOpenTopic({ topic_id: tp.topic_id, loading: false, msgs: [], error: str(e) });
    }
  };

  return React.createElement(
    "div",
    { style: { ...card, marginTop: 4, background: c.bgActive, borderLeft: "3px solid " + c.btnPrimary } },
    React.createElement(
      "div",
      { style: { display: "flex", alignItems: "center", gap: 8, marginBottom: 6 } },
      React.createElement(
        "span",
        { style: { fontSize: 12, fontWeight: 600, flex: 1, wordBreak: "break-all" } },
        "L2 场景查询（" + sceneId + "）→ 话题 " + topics.length + " 个"
      ),
      React.createElement(
        "button",
        { type: "button", style: buttonStyle(), onClick: reload, disabled: loading },
        loading ? "…" : "刷新"
      )
    ),
    React.createElement("div", { style: faint, marginBottom: 4 }, "memhop_scene_topics(scene_id=" + sceneId + ") · 共 " + (data ? data.topic_count : "…") + " 个话题（含 dream 压缩层级）· 原文经 memhop_archive_search 按需加载"),
    React.createElement(ErrBox, { error }),
    loading && !data ? React.createElement(Loading, { label: "场景上下文" }) : null,
    data && topics.length === 0
      ? React.createElement("div", { style: faint }, "该场景暂无话题。")
      : null,
    topics.map((tp, idx) => {
      const fused = isFused(tp);
      const isOpen = openTopic && openTopic.topic_id === tp.topic_id;
      const join = (a) => (Array.isArray(a) && a.length > 0 ? a.join(", ") : "");
      const kw = join(tp.keywords);
      const metaRow = (label, value, kw) =>
        React.createElement(
          "div",
          { style: { display: "flex", gap: 6, fontSize: 11, lineHeight: "16px" } },
          React.createElement(
            "span",
            { style: { color: c.btnPrimary, fontWeight: 700, flexShrink: 0, minWidth: 92 } },
            label
          ),
          React.createElement(
            "span",
            { style: { flex: 1, wordBreak: "break-all", color: value ? (kw ? undefined : c.text) : "#999" } },
            value || "—"
          )
        );
      return React.createElement(
        "div",
        { key: tp.topic_id, style: { borderTop: "1px solid " + c.border, padding: "4px 0" } },
        React.createElement(
          "div",
          { style: { display: "flex", alignItems: "center", gap: 6, marginBottom: 2 } },
          React.createElement(
            "span",
            { style: { color: c.btnPrimary, fontWeight: 700, flexShrink: 0, fontSize: 11 } },
            "#" + (idx + 1)
          ),
          fused ? React.createElement("span", { style: fusedBadge }, "🧩 压缩摘要") : null,
          React.createElement("span", { style: { ...faint, fontSize: 10, marginLeft: 2 } }, "d" + tp.depth),
          React.createElement(
            "span",
            { style: { flex: 1, ...faint, fontSize: 10, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" } },
            (tp.child_count ? "涵盖 " + tp.child_count + " 个原话题 · " : "") + (fused ? "摘要" : "话题")
          ),
          React.createElement(
            "button",
            { type: "button", style: buttonStyle("ghost"), onClick: () => toggleOriginal(tp), disabled: !!(openTopic && openTopic.topic_id !== tp.topic_id && openTopic.loading) },
            isOpen ? (openTopic.loading ? "加载中…" : "收起") : "全文"
          )
        ),
        metaRow("关键词", kw, true),
        isOpen
          ? (openTopic.loading
              ? React.createElement(Loading, { label: "L4 原文" })
              : openTopic.error
                ? React.createElement(ErrBox, { error: openTopic.error })
                : openTopic.msgs.length > 0
                  ? openTopic.msgs.map((m, i) =>
                      React.createElement(
                        "div",
                        { key: tp.topic_id + ":" + i, style: { ...msgBubble(m.role), whiteSpace: "pre-wrap", wordBreak: "break-word", marginTop: 4 } },
                        React.createElement(
                          "div",
                          { style: { display: "flex", alignItems: "center", gap: 6, marginBottom: 2 } },
                          React.createElement(
                            "span",
                            { style: { ...roleBadge(m.role), fontWeight: 700 } },
                            m.role === 1 ? "🤖 助手" : "👤 用户"
                          ),
                          React.createElement("span", { style: faint, fontSize: 10 }, fmtFullTime(m.created_at)),
                          m.id_hash
                            ? React.createElement("span", { style: { ...faint, fontSize: 10, marginLeft: "auto" } }, "L4 " + str(m.id_hash))
                            : null
                        ),
                        m.content || ""
                      )
                    )
                  : React.createElement("div", { style: faint, marginTop: 4 }, "该话题暂无 L4 原文（压缩摘要节点）。"))
          : null
      );
    })
  );
}

function roleBadge(role) {
  return {
    fontSize: 10,
    padding: "0 4px",
    borderRadius: 3,
    color: "#fff",
    background: role === 1 ? c.btnPrimary : role === 2 ? "#e8930c" : "#888",
  };
}

function msgBubble(role) {
  return {
    margin: "2px 0",
    padding: "4px 6px",
    borderRadius: 4,
    fontSize: 11,
    lineHeight: "16px",
    background: role === 1 ? c.bgActive : "rgba(128,128,128,0.12)",
  };
}

// ---------- 场景恢复 ----------

// RestoreCard offers a one-click recovery for L2 scene/topic records whose
// newest frame is a tombstone (e.g. after a scene-merge mishap).
function RestoreCard({ rpc, agentId, toast, onRestored }) {
  const [busy, setBusy] = React.useState(false);
  const restore = async () => {
    setBusy(true);
    try {
      const res = await callMemhopJson(rpc, "restore_deleted", {}, agentId);
      const n = res && typeof res.restored === "number" ? res.restored : 0;
      toast(n > 0 ? "已恢复 " + n + " 条被删记录" : "没有需要恢复的记录");
      onRestored();
    } catch (e) {
      toast("恢复失败: " + str(e));
    } finally {
      setBusy(false);
    }
  };
  return React.createElement(
    "div",
    { style: { ...card, marginTop: 8 } },
    React.createElement("div", { style: muted, marginBottom: 6 }, "误删/合并事故补救："),
    React.createElement(
      "button",
      { type: "button", style: buttonStyle("danger"), onClick: restore, disabled: busy },
      busy ? "恢复中…" : "恢复误删记录"
    )
  );
}

// ---------- 知识页签 ----------

function KnowledgeSection({ rpc, agentId, toast }) {
  const { data, error, loading, reload } = useLoad(rpc, "knowledge_list", {}, agentId);
  const [openId, setOpenId] = React.useState(null);
  const [detail, setDetail] = React.useState(null);
  const [detailLoading, setDetailLoading] = React.useState(false);

  const graphs = Array.isArray(data) ? data : [];

  const openGraph = async (id) => {
    setOpenId(openId === id ? null : id);
    if (openId === id) return;
    setDetailLoading(true);
    setDetail(null);
    try {
      const g = await callMemhopJson(rpc, "knowledge_get", { id }, agentId);
      setDetail(g);
    } catch (e) {
      setDetail({ error: e });
    } finally {
      setDetailLoading(false);
    }
  };

  return React.createElement(
    React.Fragment,
    null,
    React.createElement(SectionHeader, { title: "L3 知识图谱", onReload: reload, loading }),
    React.createElement(ErrBox, { error }),
    loading && !data ? React.createElement(Loading, null) : null,
    graphs.length === 0 && !loading
      ? React.createElement("div", { style: faint }, "暂无知识图谱。可在对话中让模型调用 knowledge_import 写入。")
      : null,
    graphs.map((g) =>
      React.createElement(
        "div",
        { key: g.id_hash, style: card },
        React.createElement(
          "div",
          { style: { display: "flex", alignItems: "center", gap: 8, cursor: "pointer" }, onClick: () => openGraph(g.id_hash) },
          React.createElement("span", { style: { fontSize: 12, fontWeight: 500, flex: 1, wordBreak: "break-all" } }, g.name || "(未命名)"),
          React.createElement("span", { style: faint }, "src=" + str(g.source)),
          React.createElement("span", { style: faint }, openId === g.id_hash ? "▾" : "▸")
        ),
        openId === g.id_hash
          ? React.createElement(
              "div",
              { style: { marginTop: 6 } },
              detailLoading
                ? React.createElement(Loading, { label: "图谱详情" })
                : detail
                  ? React.createElement(
                      React.Fragment,
                      null,
                      detail.error
                        ? React.createElement(ErrBox, { error: detail.error })
                        : React.createElement(
                            "div",
                            null,
                            React.createElement("div", { style: faint, marginBottom: 4 },
                              "graph=", fmtHex(detail.id_hash ?? detail.graph_id), " · nodes=", str((detail.nodes || []).length), " · edges=", str((detail.edges || []).length)),
                            (detail.nodes || []).slice(0, 20).map((n) =>
                              React.createElement(
                                "div",
                                { key: n.id_hash, style: { ...muted, wordBreak: "break-all", lineHeight: "16px" } },
                                React.createElement("span", { style: chip }, str(n.node_type || "node")),
                                n.title || str(n.content || "").slice(0, 40),
                                React.createElement("div", { style: faint }, fmtHex(n.id_hash))
                              )
                            ),
                            (detail.nodes || []).length > 20
                              ? React.createElement("div", { style: faint }, "…共 " + (detail.nodes || []).length + " 节点（仅显示前 20）")
                              : null
                          )
                    )
                  : null
            )
          : null
      )
    )
  );
}

// ---------- 档案页签 ----------

function ArchiveSection({ rpc, agentId, toast }) {
  const [mode, setMode] = React.useState("kw"); // kw | range | ids
  const [kw, setKw] = React.useState("");
  const [start, setStart] = React.useState("");
  const [end, setEnd] = React.useState("");
  const [ids, setIds] = React.useState("");
  const [rows, setRows] = React.useState(null);
  const [error, setError] = React.useState(null);
  const [busy, setBusy] = React.useState(false);
  const [openId, setOpenId] = React.useState(null);

  const search = async () => {
    setBusy(true);
    setError(null);
    setRows(null);
    let args = {};
    try {
      if (mode === "kw") {
        if (!kw.trim()) throw new Error("请输入关键词");
        args = { keyword: kw.trim() };
      } else if (mode === "range") {
        if (!start || !end) throw new Error("请输入起止时间戳（Unix ms）");
        args = { start: Number(start), end: Number(end) };
      } else {
        const list = ids
          .split(/[\s,，]+/)
          .map((s) => s.trim())
          .filter(Boolean);
        if (list.length === 0) throw new Error("请输入档案 ID（逗号/空格分隔）");
        args = { ids: list };
      }
      const v = await callMemhopJson(rpc, "archive_search", args, agentId);
      setRows(Array.isArray(v) ? v : []);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const openRow = (id) => setOpenId(openId === id ? null : id);

  return React.createElement(
    React.Fragment,
    null,
    React.createElement("div", { style: card },
      React.createElement("div", { style: { display: "flex", gap: 4, marginBottom: 8 } },
        ["kw", "range", "ids"].map((m) =>
          React.createElement(
            "button",
            {
              key: m,
              type: "button",
              style: {
                ...buttonStyle(),
                background: mode === m ? c.btnPrimary : undefined,
                color: mode === m ? c.fgOnPrimary : undefined,
                border: mode === m ? "none" : undefined,
                fontSize: 11,
              },
              onClick: () => setMode(m),
            },
            m === "kw" ? "关键词" : m === "range" ? "时间区间" : "按 ID"
          )
        )
      ),
      mode === "kw"
        ? React.createElement("input", { style: input, placeholder: "内容子串，如：重启", value: kw, onChange: (e) => setKw(e.target.value) })
        : mode === "range"
          ? React.createElement(
              React.Fragment,
              null,
              React.createElement("div", { style: { display: "flex", gap: 6 } },
                React.createElement("input", { style: { ...input, flex: 1 }, placeholder: "start (ms)", value: start, onChange: (e) => setStart(e.target.value) }),
                React.createElement("input", { style: { ...input, flex: 1 }, placeholder: "end (ms)", value: end, onChange: (e) => setEnd(e.target.value) })
              ),
              React.createElement("div", { style: { ...faint, marginTop: 4 } }, "示例：start=0 end=当前时间戳 → 全部档案")
            )
          : React.createElement("input", { style: input, placeholder: "16 位 hex ID，多个用逗号/空格分隔", value: ids, onChange: (e) => setIds(e.target.value) }),
      React.createElement(
        "button",
        { type: "button", style: { ...buttonStyle("primary"), marginTop: 8, width: "100%" }, onClick: search, disabled: busy },
        busy ? "检索中…" : "检索档案"
      ),
      React.createElement(ErrBox, { error })
    ),
    rows
      ? React.createElement(
          React.Fragment,
          null,
          React.createElement("div", { style: { ...faint, marginBottom: 6 } }, "共 " + rows.length + " 条"),
          rows.map((a) =>
            React.createElement(
              "div",
              { key: a.id_hash, style: card, cursor: "pointer", onClick: () => openRow(a.id_hash) },
              React.createElement(
                "div",
                { style: { display: "flex", alignItems: "center", gap: 6 } },
                React.createElement("span", { style: chip }, a.role === 1 ? "助手" : "用户"),
                React.createElement("span", { style: chip }, a.content_type === 1 ? "文本" : "type" + str(a.content_type)),
                React.createElement("span", { style: { ...faint, flex: 1, textAlign: "right" } }, new Date(a.created_at).toLocaleString()),
                React.createElement("span", { style: faint }, openId === a.id_hash ? "▾" : "▸")
              ),
              React.createElement(
                "div",
                { style: { fontSize: 11, marginTop: 4, wordBreak: "break-word", color: c.textSecondary, lineHeight: "16px", maxHeight: openId === a.id_hash ? "none" : 44, overflow: "hidden" } },
                String(a.content || "")
              ),
              openId === a.id_hash
                ? React.createElement("div", { style: { ...faint, marginTop: 4, wordBreak: "break-all" } }, "id=" + fmtHex(a.id_hash) + " · ctx=" + fmtHex(a.context_id))
                : null
            )
          )
        )
      : null
  );
}

// ---------- 能力页签 ----------

function CapabilitySection({ rpc, agentId, toast }) {
  const { data, error, loading, reload } = useLoad(rpc, "capability_list", {}, agentId);
  const [busyId, setBusyId] = React.useState(null);
  const caps = Array.isArray(data) ? data : [];

  const activate = async (id) => {
    setBusyId(id);
    try {
      const v = await callMemhopJson(rpc, "capability_activate", { id }, agentId);
      toast("已激活 " + str(v && v.name ? v.name : id));
      reload();
    } catch (e) {
      toast("激活失败: " + str(e));
    } finally {
      setBusyId(null);
    }
  };

  return React.createElement(
    React.Fragment,
    null,
    React.createElement(SectionHeader, { title: "L5 能力（" + caps.length + "）", onReload: reload, loading }),
    React.createElement(ErrBox, { error }),
    loading && !data ? React.createElement(Loading, null) : null,
    caps.map((cap) =>
      React.createElement(
        "div",
        { key: cap.id_hash, style: card },
        React.createElement(
          "div",
          { style: { display: "flex", alignItems: "center", gap: 6, marginBottom: 4 } },
          React.createElement("span", { style: { fontSize: 12, fontWeight: 600, flex: 1, wordBreak: "break-all" } }, cap.name || "(未命名)"),
          React.createElement("span", { style: chip }, str(cap.kind)),
          React.createElement("span", { style: chip }, str(cap.status))
        ),
        cap.summary
          ? React.createElement("div", { style: { ...muted, margin: "2px 0", wordBreak: "break-word" } }, cap.summary)
          : null,
        React.createElement(
          "div",
          { style: { display: "flex", alignItems: "center", gap: 8, marginTop: 4 } },
          cap.status === "draft"
            ? React.createElement(
                "button",
                { type: "button", style: buttonStyle("primary"), onClick: () => activate(cap.id_hash), disabled: busyId === cap.id_hash },
                busyId === cap.id_hash ? "激活中…" : "激活"
              )
            : null,
          React.createElement("span", { style: faint },
            "use=" + str(cap.trigger_count) + " · " + (cap.success_rate !== undefined ? "sr=" + Math.round(Number(cap.success_rate) * 100) + "%" : "") + (cap.confidence !== undefined ? " · conf=" + Math.round(Number(cap.confidence) * 100) + "%" : ""))
        )
      )
    )
  );
}

// ---------- 画像页签 ----------

function ProfileSection({ rpc, agentId, toast }) {
  const { data, error, loading, reload } = useLoad(rpc, "profile_get", {}, agentId);
  const [editing, setEditing] = React.useState(false);
  const [form, setForm] = React.useState({});
  const [busy, setBusy] = React.useState(false);

  const startEdit = () => {
    const p = data || {};
    setForm({
      name: p.name || "",
      role: p.role || "",
      personality: p.personality || "",
      preferences: p.preferences ? str(p.preferences) : "",
      lexicon: p.lexicon ? str(p.lexicon) : "",
      style_traits: Array.isArray(p.style_traits) ? p.style_traits.join(", ") : "",
      emotion_patterns: p.emotion_patterns ? str(p.emotion_patterns) : "",
    });
    setEditing(true);
  };

  const save = async () => {
    setBusy(true);
    const parseKV = (s) => {
      try {
        const v = JSON.parse(s);
        if (v && typeof v === "object" && !Array.isArray(v)) return v;
      } catch {
        /* 非 JSON，忽略 */
      }
      return undefined;
    };
    const args = {
      name: form.name || undefined,
      role: form.role || undefined,
      personality: form.personality || undefined,
      preferences: parseKV(form.preferences),
      lexicon: parseKV(form.lexicon),
      style_traits: form.style_traits
        .split(/[,，]/)
        .map((s) => s.trim())
        .filter(Boolean),
      emotion_patterns: parseKV(form.emotion_patterns),
    };
    try {
      await callMemhopJson(rpc, "profile_update", args, agentId);
      toast("画像已保存");
      setEditing(false);
      reload();
    } catch (e) {
      toast("保存失败: " + str(e));
    } finally {
      setBusy(false);
    }
  };

  const p = data || {};
  const field = (label, v) =>
    v
      ? React.createElement(
          "div",
          { style: { marginBottom: 4 } },
          React.createElement("span", { style: { ...faint, display: "inline-block", minWidth: 90 } }, label),
          React.createElement("span", { style: { wordBreak: "break-word" } }, str(v))
        )
      : null;

  return React.createElement(
    React.Fragment,
    null,
    React.createElement(SectionHeader, {
      title: "L0 画像",
      onReload: reload,
      loading,
      extra: React.createElement(
        "button",
        { type: "button", style: buttonStyle(), onClick: editing ? save : startEdit, disabled: busy },
        busy ? "保存中…" : editing ? "保存" : "编辑"
      ),
    }),
    React.createElement(ErrBox, { error }),
    loading && !data ? React.createElement(Loading, null) : null,
    !editing
      ? React.createElement(
          "div",
          { style: card },
          field("name", p.name),
          field("role", p.role),
          field("personality", p.personality),
          field("preferences", p.preferences),
          field("lexicon", p.lexicon),
          field("style_traits", p.style_traits),
          field("emotion_patterns", p.emotion_patterns),
          field("id_hash", p.id_hash),
          !p.name && !p.role && !p.personality
            ? React.createElement("div", { style: { ...faint, marginTop: 6 } }, "8 个字段均为空——随着对话与 dream 巩固会逐步生成。")
            : null
        )
      : React.createElement(
          "div",
          { style: card },
          React.createElement("input", { style: { ...input, marginBottom: 6 }, placeholder: "name", value: form.name, onChange: (e) => setForm({ ...form, name: e.target.value }) }),
          React.createElement("input", { style: { ...input, marginBottom: 6 }, placeholder: "role", value: form.role, onChange: (e) => setForm({ ...form, role: e.target.value }) }),
          React.createElement("input", { style: { ...input, marginBottom: 6 }, placeholder: "personality", value: form.personality, onChange: (e) => setForm({ ...form, personality: e.target.value }) }),
          React.createElement("input", { style: { ...input, marginBottom: 6 }, placeholder: 'preferences (JSON 对象，如 {"咖啡":"美式"})', value: form.preferences, onChange: (e) => setForm({ ...form, preferences: e.target.value }) }),
          React.createElement("input", { style: { ...input, marginBottom: 6 }, placeholder: 'lexicon (JSON 对象)', value: form.lexicon, onChange: (e) => setForm({ ...form, lexicon: e.target.value }) }),
          React.createElement("input", { style: { ...input, marginBottom: 6 }, placeholder: "style_traits（逗号分隔）", value: form.style_traits, onChange: (e) => setForm({ ...form, style_traits: e.target.value }) }),
          React.createElement("input", { style: { ...input, marginBottom: 6 }, placeholder: 'emotion_patterns (JSON 对象)', value: form.emotion_patterns, onChange: (e) => setForm({ ...form, emotion_patterns: e.target.value }) })
        )
  );
}

// ---------- 睡眠页签：勾选场景（= 宿主会话），执行记忆巩固（dream） ----------
// 列表读 scene_list（域内全部场景都可巩固）；没有场景或未勾选任何场景时，
// 睡眠按钮禁用，不可点击。

function SleepSection({ rpc, agentId, toast }) {
  const { data, error, loading, reload } = useLoad(rpc, "scene_list", {}, agentId);
  const [sel, setSel] = React.useState({});
  const [sleepBusy, setSleepBusy] = React.useState(false);

  const scenes = Array.isArray(data) ? data : [];
  const checked = scenes.filter((s) => sel[s.scene_id]);
  const allSelected = scenes.length > 0 && checked.length === scenes.length;
  // 没有场景或全未勾选 → 睡眠不可点（dream 无目标，执行必然失败）。
  const canSleep = scenes.length > 0 && checked.length > 0 && !sleepBusy;

  const toggleAll = () => {
    if (allSelected) setSel({});
    else {
      const next = {};
      scenes.forEach((s) => (next[s.scene_id] = true));
      setSel(next);
    }
  };

  const doSleep = async () => {
    const ids = checked.map((s) => s.scene_id);
    setSleepBusy(true);
    try {
      if (allSelected) {
        // 全选 = 巩固内存中全部激活场景，一次调用完成
        await callMemhopJson(rpc, "dream", {}, agentId);
      } else {
        for (const id of ids) {
          await callMemhopJson(rpc, "dream", { scene_id: id }, agentId);
        }
      }
      toast("睡眠完成：" + (allSelected ? "全部" : ids.length + " 个") + "场景已巩固");
      setSel({});
      reload();
    } catch (e) {
      toast("睡眠失败: " + str(e));
    } finally {
      setSleepBusy(false);
    }
  };

  return React.createElement(
    React.Fragment,
    null,
    React.createElement(SectionHeader, {
      title: "睡眠 · 记忆巩固",
      onReload: reload,
      loading,
      extra: React.createElement(
        "button",
        { type: "button", style: buttonStyle("primary"), onClick: doSleep, disabled: !canSleep, title: canSleep ? "" : (scenes.length === 0 ? "没有激活场景，无法巩固" : "请先勾选要巩固的场景") },
        sleepBusy ? "睡眠中…" : "🌙 睡眠"
      ),
    }),
    React.createElement(ErrBox, { error }),
    loading && !data ? React.createElement(Loading, null) : null,
    React.createElement(
      "div",
      { style: card },
      React.createElement(
        "label",
        { style: { display: "flex", alignItems: "center", gap: 6, fontSize: 12, cursor: "pointer", marginBottom: 6 } },
        React.createElement("input", { type: "checkbox", checked: allSelected, onChange: toggleAll, disabled: scenes.length === 0 }),
        "全选（" + checked.length + "/" + scenes.length + "）" + (allSelected ? " — 巩固内存中全部激活场景" : "")
      ),
      scenes.length === 0 && !loading
        ? React.createElement("div", { style: faint }, "暂无激活场景（首次 search 时会自动创建并激活）。")
        : scenes.map((sc) =>
            React.createElement(
              "label",
              { key: sc.scene_id, style: { display: "flex", alignItems: "center", gap: 6, fontSize: 12, padding: "3px 0", cursor: "pointer" } },
              React.createElement("input", {
                type: "checkbox",
                checked: !!sel[sc.scene_id],
                onChange: (e) => setSel({ ...sel, [sc.scene_id]: e.target.checked }),
              }),
              React.createElement("span", { style: { flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" } }, sc.scene_name || "—"),
              React.createElement("span", { style: muted }, sc.topic_count + " 条")
            )
          )
    ),
    React.createElement(
      "div",
      { style: { ...faint, lineHeight: "16px", marginTop: 6 } },
      "睡眠 = 记忆巩固（memhop_dream）：勾选的场景逐个巩固，不传 scene_id 则巩固该域内的全部场景；没有场景或未勾选时无法执行。"
    )
  );
}

// ---------- 搜索页签：自动 search 参数偏好（发送对话时携带） ----------

function SearchPrefsSection({ rpc, agentId, toast }) {
  const graphs = useLoad(rpc, "knowledge_list", {}, agentId);
  const [prefs, setPrefs] = React.useState(null);
  const [error, setError] = React.useState(null);
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    let alive = true;
    setError(null);
    setPrefs(null);
    callMemhopJson(rpc, "prefs", {}, agentId)
      .then((v) => {
        if (alive) setPrefs(v || {});
      })
      .catch((e) => {
        if (alive) setError(e);
      });
    return () => {
      alive = false;
    };
  }, [rpc, agentId]);

  const p = prefs || {};
  const graphList = Array.isArray(graphs.data) ? graphs.data : [];

  const save = async () => {
    setBusy(true);
    try {
      const saved = await callMemhopJson(
        rpc,
        "prefs",
        {
          prefs: {
            l3Id: p.l3Id || null,
          },
        },
        agentId
      );
      setPrefs(saved);
      // 广播给输入框内的参数指示条（SearchPrefsChip）同步刷新。
      try {
        window.dispatchEvent(new CustomEvent("memhop:prefs-saved", { detail: agentId }));
      } catch {
        /* 非浏览器环境忽略 */
      }
      toast("search 参数已保存，下一条消息发送时生效");
    } catch (e) {
      toast("保存失败: " + str(e));
    } finally {
      setBusy(false);
    }
  };

  const sel = (list, value, onChange, emptyLabel) =>
    React.createElement(
      "select",
      {
        style: { ...input, width: "100%" },
        value: value || "",
        onChange: (e) => onChange(e.target.value || null),
      },
      React.createElement("option", { value: "" }, emptyLabel),
      list.map((item) =>
        React.createElement(
          "option",
          { key: item.id_hash, value: item.id_hash },
          (item.name || "(未命名)") + " · " + item.id_hash
        )
      )
    );

  return React.createElement(
    React.Fragment,
    null,
    React.createElement("div", { style: card },
      React.createElement("div", { style: { fontWeight: 600, fontSize: 12, marginBottom: 6 } }, "新建会话的项目域（l3_id）"),
      React.createElement("div", { style: { ...muted, marginBottom: 4 } }, "该 agent 下一次新建记忆场景（= 宿主会话）时挂到哪个 L3 项目域；已有会话不受影响。"),
      graphs.loading ? React.createElement(Loading, { label: "图谱" }) : sel(graphList, p.l3Id, (v) => setPrefs({ ...p, l3Id: v }), "（不挂项目域）"),
      React.createElement(ErrBox, { error }),
      React.createElement(
        "button",
        { type: "button", style: { ...buttonStyle("primary"), marginTop: 10, width: "100%" }, onClick: save, disabled: busy || prefs === null },
        busy ? "保存中…" : "保存 search 参数"
      ),
      prefs === null && !error
        ? React.createElement("div", { style: { ...faint, marginTop: 6 } }, "读取当前偏好中…")
        : null
    ),
    React.createElement("div", { style: { ...faint, lineHeight: "16px" } },
      "说明：检索不再猜场景——core 每轮按该 agent 的场景直取记忆（宿主会话即场景），因此这里只影响新建场景的项目域归属；场景/话题的读取与删除请用场景页签。"
    )
  );
}

// ---------- 服务器管理(ServerSection)----------
// RPC 端点:memhop/server(状态)、server/start、server/stop、
// server/install、server/uninstall、server/logs。
// 管理常驻 memhop-mcp 进程(launchd 或直启)与日志查看。

function statusBadge(st) {
  const ok = st && st.health === "ok" && st.running;
  const color = ok ? c.success : st && st.health === "ok" ? c.brand : c.danger;
  return React.createElement(
    "span",
    {
      style: {
        display: "inline-block",
        padding: "2px 8px",
        borderRadius: 10,
        fontSize: 11,
        fontWeight: 600,
        color: "#fff",
        background: color,
      },
    },
    ok ? "运行中" : st && st.health === "ok" ? "进程外运行" : "已停止"
  );
}

function ServerSection({ rpc, toast }) {
  const [st, setSt] = React.useState(null);
  const [busy, setBusy] = React.useState(false);
  const [logs, setLogs] = React.useState(null);
  const [err, setErr] = React.useState(null);
  const [cfg, setCfg] = React.useState(null);
  const [editing, setEditing] = React.useState(false);
  const [form, setForm] = React.useState({});
  const [saving, setSaving] = React.useState(false);

  const refresh = React.useCallback(async () => {
    setBusy(true);
    try {
      const s = await callMemhopJson(rpc, "server", {});
      setSt(s);
      setErr(null);
    } catch (e) {
      setErr(e);
    }
    setBusy(false);
  }, [rpc]);

  React.useEffect(() => {
    refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rpc]);

  const loadConfig = React.useCallback(async () => {
    try {
      setCfg(await callMemhopJson(rpc, "server/config", {}));
    } catch (e) {
      setErr(e);
    }
  }, [rpc]);

  React.useEffect(() => {
    loadConfig();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rpc]);

  const startEdit = () => {
    const c = cfg || {};
    setForm({
      llmApiUrl: c.llmApiUrl || "",
      llmApiKey: "",
      llmModel: c.llmModel || "",
      dbDir: c.dbDir || "",
      port: c.port ? String(c.port) : "",
    });
    setEditing(true);
  };

  const saveConfig = async () => {
    setSaving(true);
    try {
      const res = await callMemhopJson(rpc, "server/save", {
        llmApiUrl: form.llmApiUrl || undefined,
        llmApiKey: form.llmApiKey || undefined,
        llmModel: form.llmModel || undefined,
        dbDir: form.dbDir || undefined,
        port: form.port ? Number(form.port) : undefined,
      });
      setCfg(res);
      toast(res.message || "配置已保存");
      setEditing(false);
      refresh();
    } catch (e) {
      toast("保存失败: " + str(e));
    } finally {
      setSaving(false);
    }
  };

  const act = async (method, label) => {
    setBusy(true);
    try {
      const r = await callMemhopJson(rpc, method, {});
      if (r && r.message) toast(label + ": " + r.message);
      await refresh();
    } catch (e) {
      setErr(e);
    }
    setBusy(false);
  };

  const showLogs = async () => {
    try {
      setLogs(await callMemhopJson(rpc, "server/logs", { limit: 80 }));
    } catch (e) {
      setErr(e);
    }
  };

  const kv = (label, value, extraStyle) =>
    React.createElement(
      "div",
      { style: { display: "flex", gap: 8, fontSize: 11, lineHeight: "18px", alignItems: "baseline" } },
      React.createElement("span", { style: { color: c.textSecondary, minWidth: 100 } }, label),
      React.createElement("span", { style: { color: c.textPrimary, wordBreak: "break-all", ...(extraStyle || {}) } }, value === null || value === undefined ? "—" : String(value))
    );

  const cfgRow = (label, value) =>
    React.createElement(
      "div",
      { style: { display: "flex", gap: 8, fontSize: 11, lineHeight: "18px", alignItems: "baseline" } },
      React.createElement("span", { style: { color: c.textSecondary, minWidth: 110 } }, label),
      React.createElement("span", { style: { color: c.textPrimary, wordBreak: "break-all" } }, value || "—")
    );

  const cfgInput = (label, key, placeholder, type) =>
    React.createElement(
      "div",
      { style: { marginBottom: 6 } },
      React.createElement("div", { style: { ...faint, marginBottom: 2, fontSize: 11 } }, label),
      React.createElement("input", {
        style: { ...input, width: "100%" },
        type: type || "text",
        placeholder: placeholder || "",
        value: form[key] || "",
        onChange: (e) => setForm({ ...form, [key]: e.target.value }),
      })
    );

  return React.createElement(
    "div",
    { style: { display: "flex", flexDirection: "column", gap: 10 } },
    React.createElement(SectionHeader, { title: "memhop-mcp 服务器", onReload: refresh, loading: busy }),
    !st && !err
      ? React.createElement(Loading, { label: "服务器状态" })
      : err
        ? React.createElement(ErrBox, { error: err })
        : React.createElement(
            React.Fragment,
            null,
            React.createElement(
              "div",
              {
                style: {
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  padding: "8px 10px",
                  border: "1px solid " + c.border,
                  borderRadius: 8,
                  background: c.bgInput,
                },
              },
              statusBadge(st),
              React.createElement("span", { style: { fontSize: 11, color: c.textSecondary } }, "spawnMode: " + st.spawnMode + " · port " + st.port),
              st.pids && st.pids.length > 0
                ? React.createElement("span", { style: { fontSize: 11, color: c.textSecondary } }, "pid " + st.pids.join(", "))
                : null
            ),
            React.createElement(
              "div",
              {
                style: {
                  display: "flex",
                  gap: 6,
                  flexWrap: "wrap",
                },
              },
              React.createElement("button", { type: "button", style: buttonStyle("primary"), onClick: () => act("server/start", "启动"), disabled: busy }, "启动"),
              React.createElement("button", { type: "button", style: buttonStyle(), onClick: () => act("server/stop", "停止"), disabled: busy }, "停止"),
              React.createElement("button", { type: "button", style: buttonStyle(), onClick: () => act("server/install", "安装 launchd"), disabled: busy }, "安装 launchd"),
              React.createElement("button", { type: "button", style: buttonStyle(), onClick: () => act("server/uninstall", "卸载 launchd"), disabled: busy }, "卸载 launchd"),
              React.createElement("button", { type: "button", style: buttonStyle(), onClick: showLogs, disabled: busy }, logs ? "刷新日志" : "查看日志")
            ),
            React.createElement(
              "div",
              { style: { padding: "8px 10px", border: "1px solid " + c.border, borderRadius: 8, background: c.bgInput } },
              kv("健康", st.health),
              kv("launchd", (st.launchdInstalled ? "已安装" : "未安装") + (st.launchdLoaded ? "(已加载)" : "")),
              kv("label", st.launchdLabel),
              kv("dbDir", st.dbDir),
              kv("bin", st.serverBin),
              kv("wrapper", st.wrapper || "(无)"),
              kv("env", st.envPresent ? "已注入" : "未找到 server.env")
            ),
            React.createElement(
              "div",
              { style: { padding: "8px 10px", border: "1px solid " + c.border, borderRadius: 8, background: c.bgInput } },
              React.createElement(
                "div",
                { style: { display: "flex", alignItems: "center", gap: 8, marginBottom: 6 } },
                React.createElement("span", { style: { fontWeight: 600, fontSize: 12 } }, "配置"),
                React.createElement(
                  "button",
                  { type: "button", style: buttonStyle(), onClick: editing ? saveConfig : startEdit, disabled: saving, title: "编辑并保存服务器配置(env + wrapper,保存后自动重启服务)" },
                  saving ? "保存中…" : editing ? "保存" : "编辑"
                ),
                editing
                  ? React.createElement(
                      "button",
                      { type: "button", style: buttonStyle("ghost"), onClick: () => setEditing(false), disabled: saving },
                      "取消"
                    )
                  : null
              ),
              !editing
                ? React.createElement(
                    React.Fragment,
                    null,
                    cfgRow("LLM API URL", cfg && cfg.llmApiUrl),
                    cfgRow("LLM API Key", cfg && cfg.llmApiKeySet ? cfg.llmApiKeyMasked + " (已设置)" : "未设置"),
                    cfgRow("LLM 模型", cfg && cfg.llmModel),
                    cfgRow("数据目录", cfg && cfg.dbDir),
                    cfgRow("监听端口", cfg && String(cfg.port)),
                    React.createElement("div", { style: { ...faint, marginTop: 4 } }, "env: " + (cfg && cfg.envFile ? cfg.envFile : "…") + " · wrapper: " + (cfg && cfg.wrapperPath ? cfg.wrapperPath : "…"))
                  )
                : React.createElement(
                    React.Fragment,
                    null,
                    cfgInput("LLM API URL(deepseek 等 OpenAI 兼容端点)", "llmApiUrl", "https://api.deepseek.com/v1"),
                    cfgInput("LLM API Key(留空保持不变)", "llmApiKey", cfg && cfg.llmApiKeySet ? "已设置,留空保持不变" : "sk-…", "password"),
                    cfgInput("LLM 模型", "llmModel", "deepseek-chat"),
                    cfgInput("数据目录(db-dir)", "dbDir", "~/.memhop/agents"),
                    cfgInput("监听端口", "port", "3939"),
                    React.createElement("div", { style: { ...faint, lineHeight: "15px", marginTop: 2 } },
                      "保存后自动重启 memhop-mcp 服务使配置生效;API Key 留空表示保留原值(不会覆盖)。")
                  )
            ),
            logs
              ? React.createElement(
                  React.Fragment,
                  null,
                  React.createElement(
                    "div",
                    { style: { fontSize: 11, color: c.textSecondary, marginTop: 4 } },
                    "stdout 尾部:"
                  ),
                  React.createElement(
                    "pre",
                    { style: { ...codeBlockStyle, maxHeight: 140, overflow: "auto", fontSize: 10, lineHeight: "14px", whiteSpace: "pre-wrap", wordBreak: "break-all" } },
                    logs.out || "(空)"
                  ),
                  React.createElement(
                    "div",
                    { style: { fontSize: 11, color: c.textSecondary, marginTop: 8 } },
                    "stderr 尾部:"
                  ),
                  React.createElement(
                    "pre",
                    { style: { ...codeBlockStyle, maxHeight: 140, overflow: "auto", fontSize: 10, lineHeight: "14px", whiteSpace: "pre-wrap", wordBreak: "break-all" } },
                    logs.err || "(空)"
                  )
                )
              : null
          )
  );
}
