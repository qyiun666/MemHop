# internal/llm — LLM 传输

## 职责

- `Provider`：go-openai 客户端的薄封装——`Chat`（一次非流式调用，429/5xx 指数退避）、
  `ChatWithRetry`（截断升级重试）、`MaxOutputTokens`（输出上限）。
- 本包只管传输：不构建 prompt，也不解析回复。

## 契约

- 采样参数固定在传输层，不是调用方输入——`Chat` 签名里没有 temperature/topP，
  Provider 内写死 0.0/1.0。确定性采样是「同一批输入能重放出同一份回复」的前提。
  别为了「灵活性」把这两个参数加回签名：加回去的结果是每个调用点各传一遍同一对
  常量，接口从 4 个参数变成 6 个而没有任何一处真的在选。
- `New` 只吃一份 `LlmConfig`，不吃整份库配置：它读的本来就只有那五个字段，收窄之后
  「给一个端点另配一份」不必伪造一份带 DBPath 与 Defaults 的配置。
- 一次调用的结论分三档：端点拒绝或回复不成形 → `ErrLLM`；回复被输出上限截断 →
  `ErrLLM` 且 cause 为 `ErrTruncated`（升级预算的重试靠这个判定）；调用方的上下文
  已尽 → `ErrCancelled`，cause 留着 `ctx.Err()`。第三种不是端点的失败：一次被撤掉的
  请求在 HTTP 栈里报回来的也是错误，按状态码分类就成了「服务拒绝了」。

## 陷阱

- `normalizeBaseURL` 会补 `/v1`、剥 `/chat/completions`：go-openai 不自动补前缀，
  而 SDK 会重新拼接后缀。
- APIKey 绝不出现在错误信息里：`httpError` 只带状态码与消息体，且消息体只留开头
  `maxUpstreamEcho` 字节（截口按 UTF-8 收口）。那句话有两个去处——工具客户端看到的
  错误文本、和 stderr 上的 WARN——而网关在 4xx 上回显什么不由本包决定：一页 HTML
  逐字贴过来，一次调用失败就成了一条两万字节的日志，且有把请求内容带进日志面的路。
