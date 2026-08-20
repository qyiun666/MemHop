// trigger.js — 侧边栏底部触发器：打开/关闭 MemHop 记忆面板。

const railIcon = {
  width: 28,
  height: 28,
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  fontSize: 15,
  lineHeight: 1,
  cursor: "pointer",
  background: "none",
  border: "none",
  color: "inherit",
  padding: 0,
};

const rowStyle = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  cursor: "pointer",
  background: "none",
  border: "none",
  color: "inherit",
  fontSize: 12,
  padding: "4px 8px",
  width: "100%",
  textAlign: "left",
};

function MemhopTrigger({ rpc, wide = true }) {
  const [open, setOpen] = React.useState(false);
  return React.createElement(
    React.Fragment,
    null,
    wide
      ? React.createElement(
          "button",
          { type: "button", style: rowStyle, onClick: () => setOpen(true), title: "MemHop 记忆面板（当前会话数据库）" },
          React.createElement("span", { "aria-hidden": true }, "🧠"),
          React.createElement("span", null, "记忆")
        )
      : React.createElement(
          "button",
          { type: "button", style: railIcon, onClick: () => setOpen(true), title: "MemHop 记忆面板（当前会话数据库）" },
          React.createElement("span", { "aria-hidden": true }, "🧠")
        ),
    open
      ? React.createElement(MemhopPanel, { rpc, onClose: () => setOpen(false) })
      : null
  );
}
