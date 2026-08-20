# capabilities/ — 内置 L5 能力工具箱（对外能力合集）

本目录是 MemHop 对外的**能力工具箱**，`memhop-capability/v2` 格式，通过 `capabilities.go` 的 `//go:embed` 内嵌进库。内置能力分两类：

1. **MemHop 自身的能力说明书**（`memhop-*`，manual）：教宿主 LLM 正确驱动记忆循环
2. **harness/agent 应具备的原子能力卡**（`agent-*`，atomic）：通用工具契约卡，宿主据此映射自己的实际工具

## 工作方式：单独获取，零配置、零写入

- **获取通道**：`ListCapabilities` / `GetCapability` 直接返回内置工具箱，与库存能力共用同一套过滤器（status / type / keyword）；宿主可在系统提示词组装时一次性拉全量
- **不附带检索**：`Search` 响应不携带内置工具箱——检索只返回库内存储并按查询匹配的能力
- **只读**：内置能力不落 `.meh` 文件、不参与 Activate / RecordCapabilityUsage / Delete 生命周期
- **去重**：宿主导入同名能力后，库存记录（含使用统计）优先，内置副本自动让位

宿主可通过 `capabilities.FS` 读取这套内嵌文件（检查、扩展或自行入库）。

## 清单

### MemHop 说明书（manual）

| 文件 | 能力 | 内容 |
|---|---|---|
| `memhop-guide.json` | `memhop-guide` | 记忆循环总纲：Search 回忆+存储 → Update 回写 → Dream 巩固 → L7 轨迹 → L5 结晶 |
| `memhop-search.json` | `memhop-search` | Search 三路由（默认混合检索 / auto_create / directed_l2_id / directed_l3_id）与返回字段用法 |
| `memhop-update.json` | `memhop-update` | Update 回写契约（new_topic_id、参数校验、串行调用） |
| `memhop-dream.json` | `memhop-dream` | Dream 巩固周期的阶段、no-op 条件与注意事项 |
| `memhop-trajectory.json` | `memhop-trajectory` | L7 轨迹记录契约（event_type 约定、4KB 截断、Seq 自动分配） |
| `memhop-crystallize.json` | `memhop-crystallize` | L5 生成：Crystallize → draft → ActivateCapability → RecordCapabilityUsage 闭环 |
| `memhop-capability-import.json` | `memhop-capability-import` | L5 导入：文件格式与幂等语义 |

### Agent 原子能力卡（atomic）

| 文件 | 能力 | 工具契约 |
|---|---|---|
| `agent-read-file.json` | `agent-read-file` | 按路径读文件，带行号，支持分段 |
| `agent-write-file.json` | `agent-write-file` | 创建或整体重写文件 |
| `agent-edit-file.json` | `agent-edit-file` | 原文唯一定点替换 |
| `agent-run-command.json` | `agent-run-command` | 执行 shell，回收 stdout/stderr/退出码 |
| `agent-search-files.json` | `agent-search-files` | glob 找路径 + 正则搜内容 |
| `agent-web-search.json` | `agent-web-search` | 联网搜索，附来源链接 |

## 编写宿主自己的能力

宿主自己的能力走 `ImportCapability(path)` 入库（单文件，或含 `capability.json` 的目录），导入即 `active`、参与 Search 关键词匹配；内容未变（FileHash 相同）的重复导入会跳过，不产生新记录。

最小 mcp 示例（`type: mcp` / `skill` 需要恰好一个同类型 resource）：

```json
{
  "format": "memhop-capability/v2",
  "name": "my-runbook",
  "version": "1",
  "type": "mcp",
  "summary": "一句话说明",
  "trigger": "什么时候命中该能力（参与 Search 匹配的关键词）",
  "resources": [
    {"type": "mcp", "name": "my_tool", "ref": "harness:my_tool", "description": "工具契约"}
  ]
}
```

最小 composite 示例（`type: composite` 需要至少一个 resource；workflow 可选，若存在则每步 `ref` 必填）：

```json
{
  "format": "memhop-capability/v2",
  "name": "my-composite",
  "version": "1",
  "type": "composite",
  "summary": "一句话说明",
  "trigger": "命中关键词",
  "resources": [
    {"type": "skill", "name": "step1", "ref": "skills/step1.md"},
    {"type": "skill", "name": "step2", "ref": "skills/step2.md"}
  ],
  "workflow": {"steps": [{"ref": "step1", "action": "run"}, {"ref": "step2", "action": "run"}]}
}
```

校验规则：`name` 必填；`trigger` 或 `summary` 至少一个；`mcp`/`skill` 恰好一个同类型 resource；`composite` 至少一个 resource；`workflow.steps[].ref` 必填。MemHop 只存储与匹配能力，不执行其中引用的工具或服务。
