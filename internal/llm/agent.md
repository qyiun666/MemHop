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
- 两个预算按「没填＝库默认」读：`TimeoutSecs` 未填取 120 秒、`MaxOutputTokens` 未填取
  8192（`budgets`；`TestBudgetsTakeUnfilledValuesAsDefaults`）。两处都不许让 0 原样落下：
  0 输出上限等于把每次回复截成空，而 0 HTTP 超时的语义不是「立刻」是「永远等」，
  一次挂死的端点就会把一个域的锁一直占着。填了的窗口要在网线上真的生效、且**只生效一次**：
  比窗口慢的端点归「端点拒绝」那一档（`ErrLLM`），不归「调用方已尽」那一档（那是宿主自己的
  ctx，报错了会把人去支去看自己的上下文），也不进退避重试——慢不是 429 也不是 5xx，
  每重跑一次就多占一个窗口的域锁（`TestSlowEndpointIsAbandonedInsideItsOwnWindow`）。
- 调用方选的 token 预算要走到**请求体**里才算存在：`Chat` 交来的 `maxTokens` 就是发出去的 `max_tokens`，升级那一趟发的是较大的那个，两者相等（调用方没留余量）或第一次不是截断而是拒绝时**不发第二趟**（`TestOutputCeilingReachesTheRequestItWasNamedFor`、`TestRefusedRequestIsNotEscalated`，读的是服务端收到的 `max_tokens`）。本包其余用例都通过假传输跑，它们能证调用方怎么选档位，证不了数字有没有出网——少了这条，「升级重试」可以在完全不起作用的情况下全绿。
- 一次调用的结论分三档：端点拒绝或回复不成形 → `ErrLLM`；回复被输出上限截断 →
  `ErrLLM` 且 cause 为 `ErrTruncated`（升级预算的重试靠这个判定）；调用方的上下文
  已尽 → `ErrCancelled`，cause 留着 `ctx.Err()`。第三种不是端点的失败：一次被撤掉的
  请求在 HTTP 栈里报回来的也是错误，按状态码分类就成了「服务拒绝了」。
- 三档之外没有第四种出口，也没有不带码的返回：重试预算用尽时交回的就是**最后一次尝试
  自己那一档**，所以循环末尾那条 return 是可达的、且必然带着码——一条裸 error 经
  `CodeOf` 读作 0，而 0 是成功码。

## 陷阱

- `normalizeBaseURL` 会补 `/v1`、剥 `/chat/completions`：go-openai 不自动补前缀，
  而 SDK 会重新拼接后缀。
- APIKey 绝不出现在错误信息里：`httpError` 只带状态码与消息体，且消息体只留开头
  `maxUpstreamEcho` 字节（截口按 UTF-8 收口）。那句话有两个去处——调用方读到的
  错误文本、和 stderr 上的 WARN——而网关在 4xx 上回显什么不由本包决定：一页 HTML
  逐字贴过来，一次调用失败就成了一条两万字节的日志，且有把请求内容带进日志面的路。
