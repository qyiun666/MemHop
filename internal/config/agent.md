# internal/config — 宿主配置类型

- **职责**：`MemHopConfig` / `LlmConfig` / `MemHopDefaults` 与 `Validate`、
  `DefaultMemHopDefaults` 的唯一定义处。只放类型与校验，不放装配逻辑
  （`Open` 在 internal 根）。校验按类型归位：LLM 三项必填的规则住在
  `LlmConfig.Validate` 自己身上，`MemHopConfig.Validate` 只加一条 `DBPath`
  再复用它——只拿到一份 `LlmConfig` 的调用方因此不必伪造整份库配置才能校验，
  同一条规则也不会有第二份副本。
- **被谁引用**：internal 根（经 `exports.go` 恒等别名暴露给 api，api 不得
  直接 import 本包）、`internal/llm`（构造 Provider）、`internal/domain`
  （Context.Defaults 注入）。
- **陷阱**：新增宿主旋钮必须有真实消费者（读它的阶段），并同步
  `DefaultMemHopDefaults`。
