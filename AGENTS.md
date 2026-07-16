# AGENTS.md — MemHop

This file provides guidance to AI agents when working on the MemHop repository.

## 项目概述

MemHop 是一个面向 AI Agent 的嵌入式记忆数据库，用单个 `.meh` 文件实现六层认知架构（L0–L5）。它通过 Go module API 暴露给 MeowAgent 等宿主。

- **语言**: Go 1.25+
- **模块路径**: `github.com/qyiun666/memhop`
- **版本**: v0.57.0
- **许可证**: MIT OR Apache-2.0
- **核心形态**: 嵌入式 Go library，极简依赖（仅 3 个直接依赖）

## 常用命令

```bash
# 构建
go build ./memhop/...

# 测试
go test ./memhop/...                    # 单元测试
go test ./test/...                      # 集成测试（需要 Ollama）
go test ./...                           # 全部测试

# 代码质量
go vet ./...
gofmt -w memhop test

# 基准
go test -bench=. -benchmem -run=^$ ./test/...

# Makefile
make build
make test
make test-unit
make test-integration
make bench
make lint
make fmt
```

## 架构要点

- **六层记忆**: L0 Profile → L1 Engram → L2 Context → L3 Knowledge → L4 Archive → L5 Crystal
- **存储格式**: V2 append-only `.meh`（魔数 `MEH2`），A/B 双 Header + CRC32 + 快照 + mmap 零拷贝读取
- **检索**: BM25（gojieba/gse CJK 分词）+ f16 IVF 向量近似搜索 + RRF 融合
- **Dream 周期**: L3 蒸馏 → L2 压缩 → L1 重建 → L1 衰减 → L0 重建 → 语言习惯蒸馏 → L5 结晶
- **编码器**: HTTP 调用 Ollama /api/embed，f16 半精度存储
- **日志**: 标准库 `log/slog` 结构化日志
- **错误处理**: sentinel errors + 结构化 MemHopError

## 代码组织

```
memhop/                          # 对外 API 门面
├── memdb.go                     # 主入口 + 生命周期
├── export.go                    # 类型别名统一导出
├── search_api.go                # Search
├── update_api.go                # Update
├── *_api.go                     # 各层 CRUD API
└── internal/
    ├── core/
    │   ├── config.go            # 配置
    │   ├── errors.go            # 错误定义
    │   ├── model/               # L0-L5 数据模型
    │   ├── storage/             # V2 存储引擎
    │   ├── index/               # BM25/IVF/L2Meta 索引 + 分词器
    │   ├── query/               # 检索管线 + DTO
    │   ├── dream/               # Dream 巩固管线
    │   ├── encoder/             # HTTP 编码器
    │   ├── l3/                  # L3 超图引擎 + DSL
    │   └── session/             # 会话管理
    └── hash/                    # xxHash64
```

## 开发规则优先级（冲突时按序号）

1. **P10 Ponytail** — 最少代码、删除优于添加、YAGNI、质疑复杂需求
2. **P01 代码质量** — 内存安全、`go vet` 干净、无数据竞争
3. **P02 代码修改** — 修改优于新建、最小化变更、只改必须改的部分
4. **P09 依赖管理** — 极简依赖、标准库能覆盖就不加依赖
5. **P07 性能优化** — 测量优先、渐进优化、禁止过早优化

## 代码审查清单

- [ ] `go build ./...` 无错误
- [ ] `go test ./...` 全部通过
- [ ] `go vet ./...` 无警告
- [ ] `gofmt` 格式正确
- [ ] 关键路径使用零拷贝（mmap）
- [ ] 不引入不必要依赖
- [ ] 不创建未被明确要求的抽象
- [ ] 错误已显式处理，无裸 panic
- [ ] 改 API 同时更新所有调用方
- [ ] 线程安全（sync.RWMutex 正确使用）

## 安全红线

- 禁止硬编码密钥、Token、路径等配置项
- `.env` 与敏感文件禁止提交 Git
- 日志禁止输出密码、Token、完整手机号
- 输入验证使用白名单，非黑名单

## 多 Agent 协作

- **新功能开发**: developer 实现 → reviewer 审查 → tester 补充测试
- **Bug 修复**: debugger 复现定位 → 最小修复 → tester 写回归测试
- **技术调研**: researcher 收集分析 → 输出建议

协作时始终遵循本文件的规则优先级。
