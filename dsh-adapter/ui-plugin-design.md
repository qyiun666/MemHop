# MemHop UI 插件设计（dsh-adapter）

## 1. 目标

在 DeepSeek Harness 聊天界面集成 MemHop 记忆管理面板：

- 用户可视化浏览/操作记忆（场景、知识图谱、档案、能力、画像）
- Search 可选参数（auto_create / directed_l2_id / directed_l3_id）每次可选择
- 场景合并等管理操作一键完成
- 会话中的 `mcp__memhop__*` 工具调用渲染为结构化卡片（二期）

## 2. 架构：双半插件

```
packages/client/memhop-ui/
├── package.json          # dshClient 声明（exports["./client"]）
├── src/
│   ├── index.ts          # host 半：apply() 空（或状态上报），注册进 cordis
│   └── client/
│       ├── index.tsx     # client 半：React 面板入口
│       ├── MemhopPanel.tsx      # 侧边栏面板（场景/知识/档案/能力/画像 页签）
│       ├── SearchForm.tsx       # 参数化 Search 表单
│       ├── SceneMerge.tsx       # 场景合并（多选次场景 + 主场景下拉）
│       ├── KnowledgeView.tsx    # 知识图谱浏览/导入/编辑
│       ├── ArchiveSearch.tsx    # 档案检索
│       ├── CapabilityView.tsx   # 能力浏览/激活/导入
│       ├── ProfileView.tsx      # L0 画像查看/编辑
│       ├── DreamActions.tsx     # Dream/Checkpoint/Crystallize 一键操作
│       └── rpc.ts               # client→host RPC（复用 MCP 工具）
└── tsdown.client.ts
```

## 3. 关键机制

### 3.1 client → host 工具调用

- host 半通过 `ctx.tools` 获取已注册的 `mcp__memhop__*` 工具（serverName=memhop）
- client 半通过 Connection RPC 通道（`/rpc/memhop`）转发调用请求
- host 半实现 `ConnectionRpcHandler`：解析 endpoint（如 `memhop.search`）→ 调用对应 MCP 工具 → 返回 RpcResult
- 好处：**零重复逻辑**——面板操作与模型调用共用同一套 MemHop 工具实现

### 3.2 Search 参数表单

```tsx
<SearchForm
  autoCreate={bool}          // 开关
  directedL2={sceneId?}      // 场景下拉（memhop_scene_list 加载）
  directedL3={graphId?}      // 知识图谱下拉（memhop_knowledge_list 加载）
  onSearch={(params) => rpc('memhop.search', params)}
/>
```

### 3.3 场景合并

```tsx
<SceneMerge
  scenes={sceneList}          // memhop_scene_list
  primary={selectedMain}      // 主场景下拉
  secondaries={selectedMulti} // 次场景多选
  onMerge={() => rpc('memhop.scene_merge', { primary_id, secondary_ids })}
/>
```

### 3.4 面板布局

- 侧边栏新页签「记忆」（与现有页签并列）
- 页签内 5 个分区：场景、知识、档案、能力、画像
- 顶部常驻：状态徽标（memhop_status）+ 巩固按钮（Dream）+ 落盘按钮（Checkpoint）
- Search 参数表单置顶（常用）

## 4. 部署

1. 开发：`pnpm run dev:web`（client-plugin HMR 热重载）
2. 生产：构建 web 产物 → 更新 DSH profile 的 client bundle → 重启 DSH
3. profile 注册：cordis.yml/patch 添加 `@deepseek-ai/dsh-memhop-ui` 条目

## 5. 二期（可选）

- 工具调用渲染卡片：会话中 memhop 工具调用折叠成结构化卡片（复用 dsh-client-ui-cordis 模式）
- 轨迹时间线可视化（memhop_trajectory_read）
- L3 子图可视化（canvas 渲染）
