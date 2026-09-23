# Gateway Hook Plugins

Language: English | [简体中文](../zh-CN/plugin-development/gateway-hooks.md) | [日本語](../ja/plugin-development/gateway-hooks.md)

Gateway hooks participate in a specific stage between API Key authentication and the final response. Start from [`examples/hook-trace-go`](../../plugin-devkit/examples/hook-trace-go).

> **Runtime availability:** This page defines the external gateway-hook contract. Packages and hook ordering can be validated, but the current TokenHub runtime rejects external gateway commands before launch because host-level isolation is not yet enforceable. A hook's failure policy determines how that rejection affects a request; in-process built-in hooks are unaffected.

Choose one narrow stage such as `privacy_pre`, `guardrail_pre`, `cache_lookup`, `route_candidates`, `route_rank`, `request_transform`, `provider_call`, `guardrail_post`, `usage_attribution`, `cache_write`, `settlement`, or `trace_export`. Declare exactly which data classes the hook reads and writes. TokenHub rejects writes outside the stage contract and protects model identity and other Core-owned fields.

Plugin API v2 orders hooks within a stage with explicit `before` and `after` references. It does not accept numeric `priority`. If two v2 hooks write the same data class, declare an order between them; cycles and ambiguous shared writes are rejected. Exclusive stages accept only one v2 hook.

Select failure behavior deliberately: `fail_closed` for security or admission, `fail_open` for optional cache behavior, `skip_route` around Provider attempts, and `observe_only` for settlement or tracing. Keep the handler deterministic, bounded by timeout, cancellation-aware, and free of raw credential logging.

Run `tokenhub-plugin-test hook`, then verify ordering and failure behavior in a TokenHub integration test. See the [complete guide](guide.md) for the ordered stage and envelope contracts.
