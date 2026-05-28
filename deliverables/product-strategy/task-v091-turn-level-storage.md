# MemHop v0.9.1 开发任务 — Turn 级存储 + 多轮合并

**日期**: 2026-05-28  
**当前版本**: v0.9.0  
**目标版本**: v0.9.1  

---

## 背景

LongMemEval-S benchmark 验证结果：
- 当前 R@5=21.4%（agentmemory 95.2%），根因是**平均池化把 20 轮信号淹没了**
- 检索管线本身无损（NDCG 117% 纯余弦上限，RRF + Hopfield 正确增强）
- 需要 turn 级独立存储，而不是 session 级 blob

---

## 核心改动

### 1. Turn 级 Perceive

**现状**: 一次 `perceive` 存一条 engram，session_id 为可选字段  
**改为**: 必填 `turn_id` + `session_id` + `segment_index`（段落序号）

```rust
PerceptionInput {
    content: String,           // 本轮对话原文
    session_id: String,        // 必填，所属会话
    turn_id: String,           // 必填，本轮唯一ID
    turn_index: u32,           // 第几轮（0-based）
    segment_index: u32,        // 长文本段落号（0 = 首段）
    topic_label: Option<String>, // 话题标签（MeowAgent 拆分时传入，"jwt"/"logo"）
    source: Option<TurnSource>, // 来源：用户/Agent/系统
    ..  // 其余字段不变
}
```

### 2. Turn 级 Recall

**现状**: recall 返回 `associations` (Vec<Engram>)，engram 对应整场 session  
**改为**: 返回命中 turn 列表 + 按 session 聚合统计

```rust
RecallResponse {
    hit_turns: Vec<TurnHit>,        // 命中的具体 turn
    aggregated_sessions: Vec<SessionScore>,  // 按 session 聚合得分
    ..
}

TurnHit {
    engram_id: String,
    turn_id: String,
    session_id: String,
    score: f32,
    snippet: String,  // 原文摘要
}

SessionScore {
    session_id: String,
    total_score: f32,    // 该 session 所有命中 turn 得分总和
    top_turn_ids: Vec<String>,
}
```

### 3. 多轮合并 (Crystallizer)

Dream NREM 自动语义聚类，不依赖时间相邻：

**触发条件**：
- 语义相似度 > 0.85 的 turn → 自动聚为一组
- 不分 session、不依赖轮次间隔
- 合并为 Schema + 保留原文指针（不删除原始向量）

**交替话题**（20 轮讲两件不同的事）：
```
turn_1: "JWT 报错 invalid signature"
turn_2: "logo 颜色改成蓝色"
turn_3: "把 HMAC 换成 RSA"
turn_4: "logo 改完发你"

Dream 语义聚类 →
  Schema_A: "修 JWT bug"  ← turns [1,3,5,...,19]
  Schema_B: "改 logo"     ← turns [2,4,6,...,18]
  跨回合 Hebbian 边增强自动发现关联
```

**单轮多话题**（由 MeowAgent 拆分后传入）：
```
store("JWT token 过期", turn_id=T5, seg=0, topic_label="jwt")
store("logo 改蓝色",   turn_id=T5, seg=1, topic_label="logo")
→ Dream 聚类时各归各的组
```

### 4. 长文本分段

单 turn 超过 5000 字符 → 自动分段，每段独立 engram：

```
store("JWT 原理... [3000字] ...", turn_id=T1, seg=0)
store("token 刷新... [3000字] ...", turn_id=T1, seg=1)
store("总结... [1000字] ...", turn_id=T1, seg=2)
```

recall 命中任意一段 → 通过 turn_id 拉回完整原文。

---

## API 变更

| 方法 | 变更 |
|------|------|
| `perceive` | 新增 `turn_id`, `turn_index`, `segment_index`, `source` |
| `recall` | 返回 `hit_turns` + `aggregated_sessions` |
| `update` | 不做（turn 级不再支持修改，直接忘记重存） |
| `forget` | 接受 `turn_id` 删除整轮所有段 |

---

## 目标数据

| 指标 | v0.9.0 | v0.9.1 目标 |
|------|--------|------------|
| LongMemEval-S R@5 | 21.4% | > 80% |
| 失忆率 | 92.7% | < 20% |
| 存储粒度 | session 级 | turn 级 |
| 长文本支持 | 截断 | 分段 |

---

## 不变的东西

- HNSW + RRF + Hopfield 检索管线（已验证无损）
- 双模式（Retrieval / Associative）
- 编码器策略（api > ONNX > ngram）
- MCP-only 接口

---

## 实现状态

**实现日期**: 2026-05-29  
**状态**: 已实现 ✅

### 实现的文件

| 文件 | 变更 |
|------|------|
| `memhop/src/engram.rs` | TurnSource 枚举、Engram.turn_id、DialogueTurn 扩展（+5 字段） |
| `memhop/src/types.rs` | PerceptionInput/RecallResponse/DreamReport 扩展、TurnHit/SessionScore 类型 |
| `memhop/src/lib.rs` | 新类型导出 |
| `memhop/src/brain.rs` | perceive() 分段循环、recall() turn 聚合、dream() NREM-2b Crystallizer、forget()/update()、schema 版本 |
| `memhop/src/storage.rs` | delete_dialogue() |
| `memhop/src/schema.rs` | turn_cluster_emergence() |
| `memhop-mcp-server/src/main.rs` | store schema 扩展、recall 响应扩展、forget 工具、v0.9.1 |
| `memhop/tests/integration_test.rs` | PerceptionInput 适配 |
| `memhop/tests/plan_integration_test.rs` | PerceptionInput 适配 |
| `memhop/src/bin/latency_bench.rs` | PerceptionInput 适配 |
| `memhop/src/bin/longmemeval_bench.rs` | PerceptionInput 适配 |
| `memhop/src/bin/quality_bench.rs` | PerceptionInput 适配 |

### 已知限制

- **HNSW 删除**: forget() 跳过 HNSW 清理（HNSW 无删除 API），不会影响召回正确性
- **数据库迁移**: v0.9.0 → v0.9.1 因 bincode 序列化格式变更，旧对话数据需重新创建 DB
- **稀疏索引清理**: forget() 跳过 sparse_index 清理（MVP 简化）

