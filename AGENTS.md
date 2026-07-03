# AGENTS.md — MemHop

This file provides guidance to Kimi Code CLI when working on the MemHop repository.

## 项目概述

MemHop 是一个面向 AI Agent 的嵌入式记忆数据库，用单个 `.meh` 文件实现六层认知架构（L0–L5）。它通过 Rust crate API 暴露给 MeowAgent 等宿主，同时也支持 Rust 原生 API。

- **语言**: Rust (Edition 2021, MSRV 1.75)
- **版本**: v0.53.0
- **许可证**: MIT OR Apache-2.0
- **核心形态**: `lib`，零外部运行时依赖理念

## 常用命令

```bash
# 构建
cargo build --release     # 构建 library
cargo build

# 测试
cargo test                # 运行测试套件
MEMHOP_LLM_API_KEY=sk-xxx cargo test -- --include-ignored --nocapture

# 代码质量
cargo clippy
cargo fmt --check
cargo fmt

# 基准
cargo bench
```

## 架构要点

- **六层记忆**: L0 Profile → L1 Engram → L2 Context → L3 Knowledge → L4 Archive → L5 Crystal
- **存储格式**: 自定义二进制 `.meh`，A/B 双 Header + CRC32 + WAL，4 KB 页，mmap 零拷贝读取
- **检索**: BM25（CJK 分词）+ f16 向量相似度（SIMD AVX2/NEON）+ BK-Tree 实体模糊匹配
- **Dream 周期**: L3 蒸馏 → L2 压缩 → L1 重建/衰减 → L0 重建 → 语言习惯学习 → L5 结晶
- **gRPC**: `proto/vector_model.proto` 定义 MeowVec 向量编码服务

## 开发规则优先级（冲突时按序号）

1. **P10 Ponytail** — 最少代码、删除优于添加、YAGNI、质疑复杂需求
2. **P01 代码质量** — 零拷贝（mmap/Cow/引用）、内存安全、编译无警告、`cargo clippy` 干净
3. **P02 代码修改** — 修改优于新建、最小化变更、只改必须改的部分
4. **P09 依赖管理** — 零外部运行时依赖、最小化 features、标准库能覆盖就不加依赖
5. **P07 性能优化** — 测量优先、渐进优化、禁止过早优化

## 代码审查清单

- [ ] `cargo build` 无警告
- [ ] `cargo test` 全部通过
- [ ] `cargo clippy` 无警告
- [ ] `cargo fmt --check` 通过
- [ ] 关键路径使用零拷贝
- [ ] 向量运算使用 SIMD（AVX2/NEON）
- [ ] 循环内无分配
- [ ] 不引入不必要依赖
- [ ] 不创建未被明确要求的抽象
- [ ] 错误已显式处理，无裸 `unwrap`
- [ ] 改 API 同时更新所有调用方

## 安全红线

- 禁止硬编码密钥、Token、路径等配置项
- `.env` 与敏感文件禁止提交 Git
- 日志禁止输出密码、Token、完整手机号
- 输入验证使用白名单，非黑名单
- 文件上传需限制类型、大小、重命名、禁止执行权限

## 多 Agent 协作

- **新功能开发**: full-stack-engineer 实现 → code-reviewer 审查 → qa 补充测试
- **Bug 修复**: debugger 复现定位 → 最小修复 → qa 写回归测试
- **技术调研**: researcher 收集分析 → 输出建议

协作时始终遵循本文件的规则优先级。
