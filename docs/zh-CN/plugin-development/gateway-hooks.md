# 网关 Hook 插件

Language: [English](../../plugin-development/gateway-hooks.md) | 简体中文 | [日本語](../../ja/plugin-development/gateway-hooks.md)

网关 Hook 在 API Key 鉴权到最终响应之间的指定 stage 中执行。可从 [`examples/hook-trace-go`](../../../plugin-devkit/examples/hook-trace-go) 开始。

选择一个边界窄的 stage，例如 `privacy_pre`、`guardrail_pre`、`cache_lookup`、`route_candidates`、`route_rank`、`request_transform`、`provider_call`、`guardrail_post`、`usage_attribution`、`cache_write`、`settlement` 或 `trace_export`。精确声明 Hook 读写的数据类。TokenHub 会拒绝超出 stage 契约的写入，并保护模型标识等 Core 字段。

显式选择失败策略：安全和准入使用 `fail_closed`，可选缓存使用 `fail_open`，Provider 尝试使用 `skip_route`，结算和 Trace 使用 `observe_only`。Handler 应当确定、有 timeout 上界、支持取消，且不记录原始凭证。

运行 `tokenhub-plugin-test hook`，然后在 TokenHub 集成测试中验证顺序和失败行为。完整 stage 与 envelope 契约见[完整指南](guide.md)。
