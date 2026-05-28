# MeowAgent 集成 MemHop v0.9.0 路线图

**日期**：2026-05-28
**定位**：通用 AI Agent。以猫神经架构逐轮处理。MemHop 是它的海马体（MCP 连接）。
**对端**：MemHop MCP Server。MeowAgent 只通过 MCP 协议调 MemHop。

---

## 📌 TL;DR

- **MemHop 是外挂 MCP 服务**，MeowAgent 不编译依赖 MemHop
- 全机一个 MemHop，MeowAgent 连接自己的数据库路径（`A/`）
- MeowAgent 多了两块新皮层：CodeGraph（代码结构）+ KnowledgeBase（挂载任意行业知识）
- 你的记忆 = MemHop 召回 + KnowledgeBase 检索 + CodeGraph 定位 → Thalamus 融合注入 prompt

## 📡 MemHop MCP Tool 速查（对端提供的接口）

以下是 MemHop MCP Server 暴露给 MeowAgent 的 tool 清单。MeowAgent 不关心实现，只管调这些。

| Tool | 参数 | 返回 | 说明 |
|------|------|------|------|
| `memhop_store` | text, vector?, meta?, session_id, source? | engram_id | 存储记忆 |
| `memhop_recall` | query, mode, limit, use_reranker?, max_tokens?, session_id? | [engram] | 召回记忆 |
| `memhop_mount_shelf` | path, domain | shelf_id | 挂载知识库 |
| `memhop_knowledge_search` | query, shelf_id, limit, max_tokens? | [result] | 知识库检索 |
| `memhop_unmount_shelf` | shelf_id | ok | 卸载知识库 |
| `memhop_update` | id, text?, meta? | engram | 更新记忆 |
| `memhop_forget` | id | ok | 删除记忆 |
| `memhop_create_tree` | name | tree_id | 创建领域树 |
| `memhop_dream` | — | stats | 手动触发 Dream |
| `memhop_stats` | — | {memories, size, ...} | 数据库统计 |
| `memhop_health` | — | {memory, disk, latency} | 健康检查 |

`mode` 取值：`retrieval`（纯质量）/ `associative`（类脑联想，带情绪/ngram boost）。
`domain` 取值：`code` / `doc` / `book` / `paper` / `custom`，决定切片策略。
`source` 结构：`{ kind: "book"|"code"|"doc", shelf_id, location, url }`。

---

## 1. MeowAgent 认知架构

```
┌────────────────────────────────────────────────────────┐
│                    MeowAgent                            │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │              Thalamus (L0 路由)                    │  │
│  │  查询类型判断 → 决定调 MemHop / CodeGraph / KB    │  │
│  └──────┬──────────────┬──────────────┬─────────────┘  │
│         │              │              │                 │
│  ┌──────▼──────┐ ┌─────▼─────┐ ┌─────▼──────────────┐ │
│  │ MemHop MCP  │ │ CodeGraph │ │ KnowledgeBase      │ │
│  │ (海马体)     │ │ (代码皮层) │ │ (知识皮层)          │ │
│  │             │ │           │ │                    │ │
│  │ "我记得…"   │ │ "代码结构" │ │ "这本书说了什么"     │ │
│  │ 逐轮实时     │ │ 符号+依赖  │ │ mount 外部知识      │ │
│  │ 情绪+关联    │ │ git blame │ │                    │ │
│  └─────────────┘ └───────────┘ └────────────────────┘ │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │            BrainLoop (11 阶段)                     │  │
│  │  perceive → recall → reflect → dream → respond    │  │
│  └──────────────────────────────────────────────────┘  │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │            Crystallizer (技能晶体化)               │  │
│  │  反复模式 → 沉淀为 schema → 写入 MemHop             │  │
│  └──────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────┘
         │                  │                  │
         ▼                  ▼                  ▼
    ┌─────────┐      ┌──────────┐      ┌──────────────┐
    │ MemHop  │      │ 文件系统  │      │ LLM API      │
    │ MCP Srv │      │ (代码源)  │      │ (推理引擎)    │
    └─────────┘      └──────────┘      └──────────────┘
```

---

## 2. 三大皮层

### MemHop (海马体) — MCP 连接

| 做什么 | 怎么调 |
|--------|--------|
| 存储本轮对话 | `memhop_store(text, session_id, meta)` |
| 召回相关记忆 | `memhop_recall(query, mode, limit)` |
| 挂载外部知识 | `memhop_mount_shelf(path, domain)` |
| 检索知识 | `memhop_knowledge_search(query, shelf_id)` |

MeowAgent 不关心 MemHop 内部怎么存、怎么索引、怎么 Dream。只调 MCP tool。

### CodeGraph (代码皮层) — MeowAgent 内置

| 做什么 | 怎么实现 |
|--------|---------|
| 符号定义查询 | tree-sitter AST |
| 依赖关系图 | import 解析 + caller/callee |
| 项目结构树 | 目录扫描 + 模块层次 |
| Git 历史 | git blame / log |
| 增量更新 | file watcher |

**与 MemHop 的交互**：不存进 MemHop。CodeGraph 是实时解析的。但搜索结果可以通过 Crystallizer 沉淀为 schema 写入 MemHop。

### KnowledgeBase (知识皮层) — MeowAgent 驱动

| 做什么 | 怎么实现 |
|--------|---------|
| 决定挂载什么 | 用户命令 / 自动检测项目文档 |
| 调 MemHop mount | `memhop_mount_shelf(path, domain)` |
| 路由查询 | 知识类问题调 knowledge_search，经历类调 recall |
| 管理书架 | mount / unmount / list shelves |

MeowAgent 决定"这本书值得挂载"，MemHop 负责"怎么切片和索引"。

---

## 3. Thalamus 路由规则

| 查询类型 | 判断特征 | 路由目标 |
|---------|---------|---------|
| 定义/声明 | "XX 在哪定义的"、"XX 是什么" | CodeGraph + KnowledgeBase |
| 实现/调试 | "这个函数做了什么"、"调用了哪些" | CodeGraph |
| 经历/历史 | "上次"、"之前"、"我记得"、"改过" | MemHop (Episodic) |
| 知识查询 | "XXX 的原理"、"怎么理解"、"最佳实践" | KnowledgeBase + MemHop |
| 混合任务 | "帮我改 auth 模块" | CodeGraph (结构) + MemHop (经验) |

---

## 4. MeowAgent 接入 Phase

### Phase A: MCP 客户端适配

**替代当前 Cargo 依赖**：

| 改前 | 改后 |
|------|------|
| `memhop::Brain::open()` | MCP 连接 MemHop Server |
| `brain.perceive()` | `memhop_store` MCP tool |
| `brain.recall()` | `memhop_recall` MCP tool |
| `MeowEncoder` (独立实现) | `memhop_encode_ngram` MCP tool 或本地 BGE-M3 |
| 直调 MemHop lib | 全 MCP 协议 |

**任务**：
- 删除 `MemHopAdapter`（旧 Cargo 依赖版）
- 新建 `MemHopClient`（MCP 客户端，复用现有 MCP client 基础设施）
- 删除 `MeowEncoder`，编码交给 MemHop Server

### Phase B: 知识库驱动

MeowAgent 端决定何时挂载：

```
用户打开项目 → 自动 mount_shelf(project_path, domain="code")
用户导入书籍 → mount_shelf(book_path, domain="book")
用户说"参考 PostgreSQL 文档" → mount_shelf(docs_url, domain="doc")
```

Crystallizer 整合：
```
CodeGraph 发现 auth 模块 → Crystallizer 沉淀 "auth 模块依赖 jwt + user"
  → memhop_store(text="auth模块依赖...", meta={type: "schema", source: "crystallizer"})
  → 下次 recall("auth") 直接命中
```

### Phase C: 自动捕获（agentmemory 对标）

12 个生命周期 hook，对标 agentmemory 的自动化程度：

| Hook | 触发时机 | 动作 |
|------|---------|------|
| SessionStart | 新对话开始 | recall 项目画像，注入 prompt |
| UserPromptSubmit | 用户提问 | 无（留给 Thalamus 路由） |
| PreToolUse | 工具调用前 | 无 |
| PostToolUse | 工具调用后 | `memhop_store(工具调用结果)` |
| LLMResponse | LLM 回复后 | `memhop_store(回复关键信息)` |
| SessionEnd | 对话结束 | 会话摘要 → `memhop_store` |

### Phase D: 多猫管理

| 猫 | MemHop 数据库 | CodeGraph 范围 | 挂载书架 |
|----|--------------|---------------|---------|
| 猫 A (主力) | `data/cats/A/` | 当前项目 | rust-book, pg-docs |
| 猫 B (辅助) | `data/cats/B/` | 同一项目 | 共享书架 |
| 共享空间 | `data/cats/shared/` | — | 团队知识 |

---

## 5. MemHop 不做的 / MeowAgent 做的

| 能力 | 谁做 |
|------|------|
| 自动捕获 hook | MeowAgent |
| 查询路由 | MeowAgent Thalamus |
| 代码结构分析 | MeowAgent CodeGraph |
| 知识库挂载决策 | MeowAgent |
| 多猫协调 | MeowAgent |
| 上下文窗口预算 | MeowAgent |
| 技能晶体化 | MeowAgent Crystallizer |
| 对话管理 | MeowAgent BrainLoop |
| UI / TUI | MeowAgent |
| 记忆存储/检索 | **MemHop** |
| 知识库索引 | **MemHop** |
| Dream 整合 | **MemHop** |
| 隐私过滤 | **MemHop** |

---

## 6. 端到端流程

```
用户: "帮我改一下认证中间件，上次改的时候 token 过期处理有问题"

1. MeowAgent BrainLoop 接收
2. Thalamus 路由:
   ├── CodeGraph: lookup "认证中间件" → 找到 auth.rs + login.rs
   ├── MemHop recall("认证中间件 token 过期"): 
   │     → "上次在 auth.rs:120 改了 JWT refresh token 逻辑"
   │     → "token 过期后没有正确重定向到 login"
   └── KnowledgeBase search("JWT refresh token", shelf="auth-docs"):
         → "RFC 规定 refresh token 轮换策略"
3. 融合注入 prompt
4. LLM 生成修改方案
5. PostToolUse hook: memhop_store(本次修改结果)
```

**MemHop 只做标记为它的那两步**。路由、融合、prompt 构造、工具调用——全是 MeowAgent。

---

> MeowAgent 是大脑，MemHop 是海马体。海马体不知道自己记的东西被怎么用了——它只管存好和找回来。
