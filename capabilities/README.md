# capabilities/ — 内置 L5 能力卡（LLM 可调用面）

本目录是 MemHop 内置的 **L5 能力卡工具箱**，`memhop-capability/v3` 格式，通过 `capabilities.go` 的 `//go:embed` 内嵌进库。卡的受众是 **LLM**：宿主获取后投影为工具契约/说明书注入上下文，LLM 据此调用 MemHop。

**只收录 LLM 可调用的能力**。宿主自动执行的核心循环（`OpenMulti` / `Search` / `Update` / `Dream` / 轨迹逐事件记录与 7 天自动清理 / `Checkpoint`）不做成卡——它们是宿主每轮的固定职责，不占 LLM 上下文；对应 Go API 与 MCP 工具不受影响。

## 卡定位：说明书，不是执行接口

卡描述"该调哪个 API、传什么参数"（resource 的 `name/desc/input/output` 与宿主工具规格同构，`ref` 用 `api:MethodName` 指向 `api` 包方法），真正执行永远走 `api/` 的全部对外方法或 MCP 31 工具。

## 分层注入契约（省 token）

全量 6 张卡共 19.7KB、全英文（≈4.5–5.5K token，比中文版省约 15–25% 注入 token），**默认不要全量注入**：

1. **默认注入**：`ListCapabilities` 结果投影成一行一卡索引（`id + name + summary + trigger`，6 张共 ≈300–500 token；**必须带 id**——`GetCapability` 只收 16 位 hex ID），外加 `memhop-guide` 的循环分工说明（约 2KB，也可只取其 summary/trigger 两行）
2. **按需取详情**：LLM 首次使用某张卡前，先 `GetCapability(id)`（MCP `memhop_capability_get`）取完整参数 schema，再调具体 API

## 工作方式：单独获取，零配置、零写入

- **获取通道**：`ListCapabilities` / `GetCapability` 直接返回内置卡，与库存能力共用同一套过滤器（status / type / keyword）
- **不附带检索**：`Search` 响应不携带内置卡——检索只返回库内存储并按查询匹配的能力
- **只读**：内置卡不落 `.meh` 文件、不参与 Activate / RecordCapabilityUsage / Update / Delete 生命周期
- **去重**：宿主导入同名能力后，库存记录（含使用统计）优先，内置副本自动让位

宿主可通过 `capabilities.FS` 读取这套内嵌文件（检查、扩展或自行入库）。

## 清单（6 张）

| 文件 | 能力 | 内容 |
|---|---|---|
| `memhop-guide.json` | `memhop-guide` | 记忆循环分工总纲（Search/Update/Dream/轨迹记录宿主自动，LLM 勿手动调）+ 五张卡的索引 |
| `memhop-knowledge.json` | `memhop-knowledge` | L3 知识图谱：读取/列出/导入/更新/删除/节点查询/子图展开 |
| `memhop-scene.json` | `memhop-scene` | L2 场景管理：列表/激活场景/话题上下文/合并/DeleteTopic/DeleteScene（记忆纠错） |
| `memhop-archive.json` | `memhop-archive` | L4 档案：关键词/时间范围/ID 列表三种模式检索 + 单条读取 |
| `memhop-profile.json` | `memhop-profile` | L0 画像读取与全量更新（GetL0 后回填再 UpdateL0） |
| `memhop-capability.json` | `memhop-capability` | L5 能力闭环：Crystallize 结晶 → Activate 激活 → Usage 反馈 + Import 导入 + List/Get 索引与详情 + Update/Delete |

## 编写宿主自己的能力

宿主自己的能力走 `ImportCapability(path)` 入库（单文件，或含 `capability.json` 的目录），导入即 `active`、参与 Search 关键词匹配；内容未变（FileHash 相同）的重复导入会跳过，不产生新记录。

**资源即工具声明**：每个 resource 的 `name/desc/input/output` 与宿主工具规格（如 meowire `ToolSpec`）字段完全同构——宿主投影为自身工具时只需纯字段拷贝，零格式转换；`input` 为参数 JSON Schema 字符串，`ref` 为资源定位（MCP server 地址 / skill 路径 / api:Method / 命令），`config` 为连接配置。

最小 mcp 示例（`type: mcp` / `skill` / `api` 需要恰好一个同类型 resource）：

```json
{
  "format": "memhop-capability/v3",
  "name": "my-runbook",
  "version": "1",
  "type": "mcp",
  "summary": "一句话说明",
  "trigger": "什么时候命中该能力（参与 Search 匹配的关键词）",
  "resources": [
    {"type": "mcp", "name": "my_tool", "desc": "工具契约（给 LLM）", "input": "{\"type\":\"object\",\"properties\":{\"arg\":{\"type\":\"string\"}},\"required\":[\"arg\"]}", "output": "工具输出描述", "ref": "harness:my_tool"}
  ]
}
```

最小 api 示例（封装一个 MemHop api 包方法，宿主直接调用）：

```json
{
  "format": "memhop-capability/v3",
  "name": "my-memhop-api",
  "version": "1",
  "type": "api",
  "summary": "封装一个 api 方法",
  "trigger": "需要调用该方法时",
  "resources": [
    {"type": "api", "name": "GetL0", "ref": "api:GetL0", "desc": "读取 L0 画像的调用契约"}
  ]
}
```

最小 composite 示例（`type: composite` 需要至少一个 resource；workflow 可选，若存在则每步 `ref` 必填，`args` 携带动作链参数）：

```json
{
  "format": "memhop-capability/v3",
  "name": "my-composite",
  "version": "1",
  "type": "composite",
  "summary": "一句话说明",
  "trigger": "命中关键词",
  "resources": [
    {"type": "skill", "name": "step1", "ref": "skills/step1.md", "desc": "第一步"},
    {"type": "skill", "name": "step2", "ref": "skills/step2.md", "desc": "第二步"}
  ],
  "workflow": {"steps": [{"ref": "step1", "action": "run", "args": {"mode": "fast"}}, {"ref": "step2", "action": "run"}]}
}
```

校验规则：`name` 必填；`trigger` 或 `summary` 至少一个；`mcp`/`skill`/`api` 恰好一个同类型 resource（`api` 的 `ref` 用 `api:MethodName`，如 `api:GetL3`，宿主通过 `api` 包直接调用）；`composite` 至少一个 resource；`workflow.steps[].ref` 必填；资源 `name` 必填，`input` 非空时必须是合法 JSON（Schema）。guide 卡的卡指针用 `type=api` + `ref=capability:<卡名>` 指向工具箱内另一张卡（校验不约束 ref 格式）；`GetCapability` 只收 16 位 hex ID，宿主从 `ListCapabilities` 响应的 `id_hash` 取得。MemHop 只存储与匹配能力，不执行其中引用的工具或服务。
