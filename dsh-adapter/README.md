# MemHop × DeepSeek Harness 适配指南

本文档定义 MemHop 接入 DeepSeek Harness（DSH）后的**工具分工**与**界面适配方案**：
哪些工具暴露给用户（聊天界面 UI 化）、哪些保留给模型/宿主自动调用。

对应版本：MemHop v1.2.3 · MCP 31 工具 · DSH cordis 接入（常驻多租户 memhop-mcp，streamable-http，serverName: memhop）

## 1. 31 个工具分类总表

### A. 用户界面组（UI 化，放入聊天界面记忆面板）

| 工具 | 用途 | UI 形态 |
|------|------|---------|
| `memhop_scene_list` | 场景列表 | 面板：场景树（名称/话题数/时间），点击展开话题 |
| `memhop_scene_merge` | **合并场景**（主场景 + 次场景话题合并） | 面板：场景多选 + 主场景下拉 + 一键合并，确认弹窗 |
| `memhop_knowledge_list` | 知识图谱列表 | 面板：图谱卡片（域名/节点数） |
| `memhop_knowledge_get` | 图谱详情（节点+边） | 面板：图谱展开视图 |
| `memhop_knowledge_nodes` | 节点查询（关键词/类型） | 面板：搜索框 + 过滤条件 |
| `memhop_knowledge_subgraph` | BFS 子图 | 面板：从节点出发的可视化 |
| `memhop_knowledge_import` | 批量导入知识节点 | 面板：文件选择 + 模式选择（Skip/Merge/Overwrite） |
| `memhop_knowledge_update` | 图谱改名 | 面板：行内编辑 |
| `memhop_knowledge_delete` | 删除图谱 | 面板：删除按钮 + 确认 |
| `memhop_archive_search` | 档案检索（关键词/时间区间/ID） | 面板：检索表单 + 结果列表 |
| `memhop_archive_get` | 单条档案 | 面板：详情展开（对话原文） |
| `memhop_capability_list` | 能力清单（内置 13 张 + 库存） | 面板：能力卡片网格，按 kind/status 过滤 |
| `memhop_capability_get` | 能力详情 | 面板：卡片详情（manual/atomic/composite） |
| `memhop_capability_activate` | 激活 draft 能力 | 面板：draft 卡片上的激活按钮 |
| `memhop_capability_import` | 导入能力文件 | 面板：文件选择 |
| `memhop_capability_delete` | 删除能力 | 面板：删除按钮 + 确认 |
| `memhop_profile_get` | 画像查看 | 面板：L0 画像卡片 |
| `memhop_profile_update` | 画像编辑（整体覆写） | 面板：表单编辑 + 保存 |
| `memhop_status` | 数据库健康状态 | 面板：状态徽标（closed/激活场景数） |
| `memhop_checkpoint` | 落盘快照 | 面板：手动保存按钮 |
| `memhop_dream` | 记忆巩固（五阶段） | 面板：**一键巩固按钮**（确认 + 进度提示，耗时较长） |
| `memhop_crystallize` | 从轨迹结晶能力 | 面板：一键触发 + 结果草稿列表 |
| `memhop_trajectory_read` | 轨迹查看 | 面板：会话轨迹时间线（诊断用） |

### B. 模型/宿主组（保持 MCP 工具，不 UI 化）

| 工具 | 用途 | 调用方 |
|------|------|---------|
| `memhop_search` | 核心记忆检索（回忆+存储） | **模型每轮调用**（AGENTS.md 引导）；UI 提供参数化搜索作为补充（见 §2） |
| `memhop_update` | 回复回写 | 模型回答后调用 |
| `memhop_capability_usage` | 能力使用反馈 | 模型使用能力后记录 |
| `memhop_trajectory_append` | 操作轨迹记录 | 宿主/模型记录重要操作 |
| `memhop_trajectory_delete` | 轨迹清理 | 宿主管理 |

### C. 分工原则

- **写循环**（Search/Update/Trajectory）归模型：每轮对话自动发生，用户无感知
- **管理循环**（场景/知识/能力/档案/画像）归用户：面板可视化操作
- **巩固循环**（Dream/Crystallize/Checkpoint）用户可一键触发，模型也可在引导下自动触发
- 面板所有操作**复用同一套 MCP 工具**（host 半转发 ctx.tools），不重复实现逻辑

## 2. Search 参数化搜索（每次对话可选择）

在记忆面板提供参数化 Search 表单，`memhop_search` 的可选参数全部暴露：

| 参数 | UI 控件 | 说明 |
|------|---------|------|
| `text` | 文本输入 | 搜索内容（必填） |
| `timestamp` | 自动当前时间 | 消息时间戳 |
| `auto_create` | 开关 | 开启=跳过检索直建新话题（不回忆，直接记） |
| `directed_l2_id` | 下拉（场景列表实时加载） | 定向写入指定场景；空=默认三通道检索 |
| `directed_l3_id` | 下拉（知识图谱列表） | 限定知识图谱范围检索 |

结果展示：contexts（话题）+ archives（原文全文）+ capabilities 命中 + new_topic_id（可直接复制给模型/或提示模型已写入）。

## 3. UI 插件架构（开发中）

见 `ui-plugin-design.md`：双半插件（host 转发 + client React 面板），部署到 deepseek-harness 工程 `packages/client/memhop-ui`。

## 4. 相关文档

- 接入配置与引导词：`docs/dsh/README.md`
- MCP 工具清单：`docs/mcp/README.md`
