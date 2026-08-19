// theme.js — 复用 DSH Web（DSW）的 CSS alias 变量，保证深浅色主题自适应。
const c = {
  bgPanel: "var(--dsw-alias-bg-layer-2)",
  bgInput: "var(--dsw-alias-bg-layer-3)",
  bgActive: "var(--dsw-alias-interactive-bg-active)",
  border: "var(--dsw-alias-border-l2)",
  textPrimary: "var(--dsw-alias-label-primary)",
  textSecondary: "var(--dsw-alias-label-secondary)",
  textTertiary: "var(--dsw-alias-label-tertiary)",
  btnPrimary: "var(--dsw-alias-button-primary-fill)",
  btnGhost: "var(--dsw-alias-button-ghost-active-fill)",
  danger: "var(--dsw-static-red-500)",
  success: "var(--dsw-static-green-500)",
  chip: "var(--dsw-alias-markdown-inline-code)",
  codeBg: "var(--dsw-alias-markdown-code-block)",
  shadow: "var(--dsw-alias-bg-mask-3)",
  brand: "var(--dsw-alias-brand-primary)",
  fgOnPrimary: "var(--dsw-alias-label-primary-foreground)",
};

/** 面板 overlay 容器样式。 */
const overlay = {
  position: "fixed",
  top: 0,
  right: 0,
  bottom: 0,
  width: 460,
  maxWidth: "94vw",
  background: c.bgPanel,
  color: c.textPrimary,
  borderLeft: "1px solid " + c.border,
  boxShadow: "-8px 0 24px " + c.shadow,
  zIndex: 9999,
  display: "flex",
  flexDirection: "column",
  fontSize: 13,
};

/** 通用小按钮。 */
function buttonStyle(variant) {
  const base = {
    padding: "3px 10px",
    borderRadius: 6,
    border: "1px solid " + c.border,
    background: variant === "primary" ? c.btnPrimary : variant === "danger" ? c.danger : c.bgInput,
    color: variant === "primary" ? c.fgOnPrimary : variant === "danger" ? "#fff" : c.textPrimary,
    cursor: "pointer",
    fontSize: 12,
    lineHeight: "18px",
  };
  if (variant === "primary") base.border = "none";
  if (variant === "danger") base.border = "none";
  return base;
}

/** 小号灰字。 */
const muted = { fontSize: 11, color: c.textSecondary };
/** 更弱灰字。 */
const faint = { fontSize: 10, color: c.textTertiary };
/** dream 压缩摘要话题徽标。 */
const fusedBadge = {
  display: "inline-block",
  padding: "1px 6px",
  borderRadius: 4,
  fontSize: 10,
  fontWeight: 700,
  background: "rgba(255,180,0,0.18)",
  color: "var(--dsw-static-orange-500, #e8930c)",
  flexShrink: 0,
};
/** 等宽代码块。 */
const mono = { fontFamily: "var(--dsw-font-mono, ui-monospace, SFMono-Regular, Menlo, monospace)", fontSize: 11, wordBreak: "break-all" };
/** 标签 chip。 */
const chip = {
  display: "inline-block",
  padding: "1px 6px",
  borderRadius: 4,
  fontSize: 10,
  background: c.chip,
  color: c.textPrimary,
  marginRight: 4,
};
/** 卡片容器。 */
const card = {
  background: c.bgInput,
  border: "1px solid " + c.border,
  borderRadius: 8,
  padding: "8px 10px",
  marginBottom: 8,
};
/** 输入框。 */
const input = {
  background: c.bgInput,
  border: "1px solid " + c.border,
  borderRadius: 6,
  color: c.textPrimary,
  padding: "4px 8px",
  fontSize: 12,
  width: "100%",
  boxSizing: "border-box",
};
