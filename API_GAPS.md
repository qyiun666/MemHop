# MemHop 缺失接口需求文档

## 概述

本文档记录在 meowagent 适配 `memhop` 新版 API（API_NEW.md）过程中发现的 memhop crate 缺失接口。这些接口在 API_NEW.md 中有定义，但 memhop 代码中未实现或实现不完整。

---

## 1. `get_knowledge()` — 查询 L3 知识详情

**状态**: ⚠️ 已补充实现（需 review）

**API 签名**（来自 API_NEW.md 接口10）

```rust
pub fn get_knowledge(&self, id: &str) -> Result<Option<KnowledgeDetail>>
```

**问题描述**：
`memhop/src/lib.rs` 第 418 行声明了 `get_knowledge` 方法，但尝试导入 `crate::query::list::get_knowledge`，该函数在 `query/list.rs` 中不存在（仅有 `list_knowledge` 函数）。

**当前修复**（2026-06-15）：
在 `src/query/list.rs` 中添加了 `get_knowledge()` 实现，遵循与 `get_topic()`、`get_engram()` 相同的模式：

1. 解析 ID 为 hash
2. B-tree 查找
3. `slot_io::get_slot_data` 读取
4. `HypergraphSlot::deserialize` 反序列化
5. 转换为 `KnowledgeDetail`

**注意事项**：

- `HypergraphSlot` 没有 `text`、`summary`、`keywords`、`edge_ptrs` 等字段，当前实现填充了空值
- 如需完整信息，需要：a) `HypergraphSlot` 增加字段，或 b) 从 slot 关联的 L3 节点聚合内容

---

## 2. `update_knowledge_title()` — 修改 L3 知识标题

**状态**: ⚠️ 已补充实现（需 review）

**API 签名**（来自 API_NEW.md 接口15）

```rust
pub fn update_knowledge_title(&mut self, id: &str, new_title: String) -> Result<KnowledgeSummary>
```

**问题描述**：
该接口在 API_NEW.md 中定义，但 `memhop/src/lib.rs` 中完全没有声明或实现。`src/query/update_title.rs` 中也没有对应的实现函数。

**当前修复**（2026-06-15）：

1. 在 `src/query/update_title.rs` 中添加了 `update_knowledge_title()` 实现
2. 在 `src/lib.rs` 中添加了 `pub fn update_knowledge_title` 方法声明
3. 实现遵循与 `update_crystal_title()` 相同的模式

**注意事项**：

- `HypergraphSlot.name` 作为标题字段，实现已更新该字段
- `KnowledgeSummary` 中的 `domain` 和 `knowledge_type` 字段使用了默认值
- 如果需要更精确的值，需要扩展 `HypergraphSlot` 结构

---

## 3. 建议新增的 L3 结构扩展

**背景**：当前 `HypergraphSlot` 存储 L3 知识元数据，但字段有限：

```rust
pub struct HypergraphSlot {
    pub id_hash: u64,
    pub name: String,
    pub source: HypergraphSource,
    pub node_count: u32,
    pub edge_count: u32,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}
```

**建议新增字段**（可选）：

| 字段             | 类型             | 用途                                                      |
| ---------------- | ---------------- | --------------------------------------------------------- |
| `summary`        | `Option<String>` | 知识摘要（用于 L2 检索时展示）                            |
| `domain`         | `String`         | 知识域（目前 KnowledgeSummary 中使用 source.kind() 替代） |
| `knowledge_type` | `String`         | 知识类型：Factual/Procedural/Conceptual/Contextual        |

**影响范围**：

- `HypergraphSlot` 序列化/反序列化格式
- `list_knowledge` 中的 `convert_hypergraph_to_summary()`
- `get_knowledge` 中的 `KnowledgeDetail` 构建
- `update_knowledge_title` 中的 `KnowledgeSummary` 构建
- Dream L3 Distill 阶段（如果写入这些字段）

---

## 4. 回归测试建议

补充接口后，建议在 memhop 的测试套件中添加：

| 测试                             | 验证内容                 |
| -------------------------------- | ------------------------ |
| `test_get_knowledge_by_id`       | 按 ID 获取单条 L3 知识   |
| `test_get_knowledge_not_found`   | 不存在的 ID 返回 None    |
| `test_update_knowledge_title`    | 修改 L3 标题后持久化验证 |
| `test_activate_deactivate_topic` | 会话管理的增删查         |

---

## 发现时间

- 发现日期: 2026-06-15
- 发现场景: meowagent v0.42.0 适配 memhop API_NEW.md 过程中
- 发现方式: `cargo check --workspace` 编译报错
