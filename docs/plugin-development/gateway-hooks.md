# Gateway Hook Plugins

Language: English | [简体中文](../zh-CN/plugin-development/gateway-hooks.md) | [日本語](../ja/plugin-development/gateway-hooks.md)

Gateway hooks participate in a specific stage between API Key authentication and the final response. Start from [`examples/hook-trace-go`](../../plugin-devkit/examples/hook-trace-go).

Choose one narrow stage such as `privacy_pre`, `guardrail_pre`, `cache_lookup`, `route_candidates`, `route_rank`, `request_transform`, `provider_call`, `guardrail_post`, `usage_attribution`, `cache_write`, `settlement`, or `trace_export`. Declare exactly which data classes the hook reads and writes. TokenHub rejects writes outside the stage contract and protects model identity and other Core-owned fields.

Select failure behavior deliberately: `fail_closed` for security or admission, `fail_open` for optional cache behavior, `skip_route` around Provider attempts, and `observe_only` for settlement or tracing. Keep the handler deterministic, bounded by timeout, cancellation-aware, and free of raw credential logging.

Run `tokenhub-plugin-test hook`, then verify ordering and failure behavior in a TokenHub integration test. See the [complete guide](guide.md) for the ordered stage and envelope contracts.
