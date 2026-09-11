# internal/config — 配置类型与校验

## 职责

- `MemHopConfig` / `LlmConfig` / `MemHopDefaults` 与它们的 `Validate`、
  `DefaultMemHopDefaults` 的唯一定义处。
- 只放类型与校验，不放装配：本包不打开任何东西，也不构造任何客户端。

## 契约

- 校验按类型归位：LLM 三项必填的规则住在 `LlmConfig.Validate` 自己身上，
  `MemHopConfig.Validate` 只加一条 `DBPath` 再复用它。只拿到一份 `LlmConfig` 的
  调用方因此不必伪造整份库配置才能校验，同一条规则也不会有第二份副本。

## 陷阱

- 新增一个旋钮必须有真实消费者（真的会读它的阶段），并同步
  `DefaultMemHopDefaults`。
- `DefaultMemHopDefaults` 是**值**不是指针：调用点直接赋值，要调参就复制一份改。
  导出的指针全局谁都能改坏，而且改坏的是所有其它调用方读到的那一份。
