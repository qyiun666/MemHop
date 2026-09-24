# internal/config — 配置类型与校验

## 职责

- `MemHopConfig` / `LlmConfig` / `MemHopDefaults` 的唯一定义处，另有
  `LlmConfig.Validate` 与 `DefaultMemHopDefaults`。
- 只放类型与校验，不放装配：本包不打开任何东西，也不构造任何客户端；`Normalized` 是纯取值变换，
  谁调用它才算装配。

## 契约

- 本包的校验只有两处，各自贴着自己的类型：`LlmConfig.Validate`（端点三项必填，**这个端点自己**
  的规则，只拿到一份 `LlmConfig` 的调用方不必凑出整份库配置才能校验它）与
  `MemHopDefaults.Validate`（保留窗是否可表示，见「陷阱」）。两条规则都没有第二份副本。
- `MemHopConfig` 不带校验：它的每一半各有各的判法（端点问 `LlmConfig.Validate`，旋钮问
  `MemHopDefaults.Validate`，路径问开文件的那个调用方），本包再判一遍就是同一规则的第二份副本，
  而副本会在只改一处时变成两套口径。
- 四个旋钮共用一套词汇，`Normalized` 是这套词汇的唯一定义处：**留 0 就是「没填」，由
  `DefaultMemHopDefaults` 顶上；「关掉这一项」是显式负数**。`DreamCompressMinTopics` 的负数折成 0——
  0 才是「不设目标」那个真实取值，负数不该出现在任何被读出来的地方。保留窗没有「关掉」的写法，
  也不在这一条里被折回默认：那样的值由 `Validate` 拒，`Normalized` 只把 0 顶上成默认（内部手搭的
  非正窗口与 dream 未配置时的答案一致）。谁把宿主递来的表变成引擎配置，谁就调一次；读取侧不再
  各自解释零值。
- 新增一个旋钮必须同时进 `Normalized` 与 `DefaultMemHopDefaults`（有真实消费者才配占一行），
  字段注释按这套词汇写。一个旋钮也可以有**两个**消费者：`DreamCompressMinTopics` 既是一道门槛
  （够不够格压缩），也是交给模型的目标条数（提示词里那个数与引擎用的必须是同一个），只在一处
  写明语义就会让另一处按另一个数行动。

## 陷阱

- `DefaultMemHopDefaults` 是**值**不是指针：调用点直接赋值，要调参就复制一份改。
  导出的指针全局谁都能改坏，而且改坏的是所有其它调用方读到的那一份。
- 保留窗的两个错方向都不是「什么都没发生」：负数要的是一场永不发生的清扫，而超过
  `MaxContentRetentionMs` 的毫秒数在乘成 `time.Duration` 时会绕回，绕回来的窗口把 cutoff 甩到
  未来——整域的记录当场全被判为过期。`Validate` 因此两头都拒（`ErrConfig`），并且调用方要在
  动文件系统之前问一次，被拒的入口才留不下文件。那个上限不是随手写的数，就是这次乘法还能
  不绕回的最大值（实测：上限落在约 292 年之前，多一毫秒 cutoff 即落到未来）。
