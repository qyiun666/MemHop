# internal/llm — LLM 传输

- **职责**：`Provider`（go-openai 薄封装）= `cap/llmops.Chat` 契约的唯一
  实现：单次 Chat（429/5xx 指数退避）、`ChatWithRetry`（截断升级重试）、
  `MaxOutputTokens`。传输策略在此；prompt 契约与解析在 `cap/llmops`。
- **装配**：`internal.Open` 用 `llm.New(cfg)` 构造一次（DB 级共享），经
  `domain.NewContext` 注入每个域的 `Context.LLM`；任何域不自己建客户端。
- **陷阱**：`normalizeBaseURL` 会补 `/v1`、剥 `/chat/completions`；
  APIKey 绝不出现在错误信息里（错误只带状态码与消息体）。
