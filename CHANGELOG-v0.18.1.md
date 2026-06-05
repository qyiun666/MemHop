# Changelog v0.18.1

## 版本升级

- 版本号从 v0.18.0 升级到 v0.18.1
- 统一所有文件中的版本号注释

## 功能改进

### CandleEncoder 恢复

- 恢复 CandleEncoder 作为默认编码器
- 添加 candle 相关依赖（candle-core, candle-nn, candle-transformers, tokenizers）
- 支持 multilingual-e5-small 模型（384维）
- NgramEncoder 作为回退方案

### 代码质量修复

- 修复 `run_compaction` 中的空洞碎片追踪问题
- 修复 `add_hyperedge` 参数顺序错误
- 修复 `handle_feedback` 中变量遮蔽导致反馈无效
- 修复 `calculate_rrf_scores` 未合并多通道结果
- 修复 `activate_topic` TTL 未传递
- 修复 `consolidate_chains` 未正确执行
- 修复 `update_topic` 未持久化扩展字段

### 测试覆盖

- 集成测试: 12 个测试全部通过
- MCP API 测试: 15 个测试全部通过
- 单元测试: 43 个测试全部通过

## 文档更新

- AGENT_INTEGRATION.md: 完整的 20 个 MCP 接口文档
- CROSS_PROJECT_UPGRADE_GUIDE.md: 更新版本号到 v0.18.1
- jiagou.md: 更新编码器描述

## 环境说明

- macOS C++ 工具链不完整，基准测试无法编译
- 需要安装完整的 Xcode 或修复 C++ 标准库头文件

## 兼容性

- 向后兼容 v0.18.0 数据格式
- LMDB 数据库无需迁移
- MCP 接口无破坏性变更
