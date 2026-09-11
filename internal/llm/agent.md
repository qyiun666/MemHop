# internal/llm — LLM 传输

- **职责**：`Provider`（go-openai 薄封装）= `cap/llmops.Chat` 契约的唯一
  实现：单次 Chat（429/5xx 指数退避）、`ChatWithRetry`（截断升级重试）、
  `MaxOutputTokens`。传输策略在此；prompt 契约与解析在 `cap/llmops`。
- **契约**：采样参数固定在传输层，不是调用方输入——`Chat` 签名里没有
  temperature/topP，Provider 内写死 0.0/1.0。`llmops` 的每个认知操作都要从
  回复里解析严格 JSON，输出必须能对同一批记录重放核对，确定性采样是这件事
  的前提。别为了「灵活性」把这两个参数加回签名：加回去的结果是七个调用点
  各传一遍同一对常量，接口从 4 个参数变成 6 个而没有任何一处真的在选。
- **装配**：`internal.Open` 用 `llm.New(cfg.LLM)` 构造一次，经
  `domain.NewContext` 注入每个域的 `Context.LLM`；任何域不自己建客户端。
  `New` 只吃一份 `LlmConfig` 而不是整份库配置：它读的本来就只有那四个字段，
  收窄之后「给一个域另配一个端点」不必伪造一份带 DBPath 与 Defaults 的配置。
- **陷阱**：`normalizeBaseURL` 会补 `/v1`、剥 `/chat/completions`；
  APIKey 绝不出现在错误信息里（错误只带状态码与消息体）。
