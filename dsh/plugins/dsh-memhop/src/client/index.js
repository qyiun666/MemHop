// index.js — client 插件入口：注册侧边栏底部触发器。
// 上下文按结构使用（无 cordis 类型导入），保持 bundle 自包含。

// 依赖 sessions 服务（client runtime 提供）：面板跟随 DSH 当前激活的会话
// （agentId === sessionId，1:1），切换会话时面板自动切换目标数据库。
const inject = ["connection", "slots", "sessions"];

function apply(ctx) {
  // 入口 0：输入框（composer dock）内的自动 search 参数指示条——
  // 会话级 slot，inject 携带 sessionId；面板保存参数后经 window 事件刷新。
  ctx.slots.inject("conversation.composer.dock", () =>
    ctx.slots.register(
      {
        name: "conversation.composer.dock",
        id: "memhop-search-prefs",
        order: 10,
        inject: (sessionId) => ({ rpc: ctx.connection.rpc, sessionId }),
      },
      SearchPrefsChip
    )
  );
  // 入口 1（主）：会话内 tab —— 对话 / 轨迹旁边出现「记忆」tab，
  // inject 直接携带 sessionId，天然跟随当前会话（agentId === sessionId）。
  ctx.slots.inject("conversation.view", () =>
    ctx.slots.register(
      {
        name: "conversation.view",
        id: "memhop",
        order: 20,
        label: "记忆",
        inject: (sessionId) => ({ rpc: ctx.connection.rpc, sessionId }),
      },
      MemhopTab
    )
  );
}

module.exports = {
  MemhopPanel,
  MemhopTab,
  SearchPrefsChip,
  apply,
  inject,
  callMemhop,
  callMemhopJson,
};
