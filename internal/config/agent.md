# internal/config — 配置类型与校验

## 职责

- `MemHopConfig` / `LlmConfig` / `MemHopDefaults` 的唯一定义处，另有
  `LlmConfig.Validate` 与 `DefaultMemHopDefaults`。
- 只放类型与校验，不放装配：本包不打开任何东西，也不构造任何客户端。

## 契约

- 本包只有 `LlmConfig` 自带校验：端点三项必填是**这个端点自己**的规则，只拿到一份
  `LlmConfig` 的调用方因此不必凑出整份库配置才能校验它，这条规则也没有第二份副本。

## 陷阱

- 新增一个旋钮必须有真实消费者（真的会读它的阶段），并同步
  `DefaultMemHopDefaults`。
- `DefaultMemHopDefaults` 是**值**不是指针：调用点直接赋值，要调参就复制一份改。
  导出的指针全局谁都能改坏，而且改坏的是所有其它调用方读到的那一份。
