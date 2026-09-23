# 网关 Hook 插件

Language: [English](../../plugin-development/gateway-hooks.md) | 简体中文 | [日本語](../../ja/plugin-development/gateway-hooks.md)

网关 Hook 在 API Key 鉴权到最终响应之间的指定 stage 中执行。可从 [`examples/hook-trace-go`](../../../plugin-devkit/examples/hook-trace-go) 开始。

> **运行能力：** 本页定义外部请求链 Hook 契约。插件包与 Hook 顺序可以完成校验，但由于宿主级隔离目前无法强制执行，TokenHub 运行时会在启动前拒绝外部请求链命令。Hook 的失败策略决定该拒绝如何影响请求；进程内的内置 Hook 不受影响。

选择一个边界窄的 stage，例如 `privacy_pre`、`guardrail_pre`、`cache_lookup`、`route_candidates`、`route_rank`、`request_transform`、`provider_call`、`guardrail_post`、`usage_attribution`、`cache_write`、`settlement` 或 `trace_export`。精确声明 Hook 读写的数据类。TokenHub 会拒绝超出 stage 契约的写入，并保护模型标识等 Core 字段。

Plugin API v2 使用显式 `before` 和 `after` 引用安排同一 stage 内的 Hook，不接受数字 `priority`。两个 v2 Hook 写入同一数据类时必须声明先后关系；循环依赖和顺序不明确的共享写入会被拒绝。独占 stage 只接受一个 v2 Hook。

显式选择失败策略：安全和准入使用 `fail_closed`，可选缓存使用 `fail_open`，Provider 尝试使用 `skip_route`，结算和 Trace 使用 `observe_only`。Handler 应当确定、有 timeout 上界、支持取消，且不记录原始凭证。

运行 `tokenhub-plugin-test hook`，然后在 TokenHub 集成测试中验证顺序和失败行为。完整 stage 与 envelope 契约见[完整指南](guide.md)。
