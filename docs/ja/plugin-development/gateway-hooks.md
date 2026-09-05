# Gateway Hook プラグイン

Language: [English](../../plugin-development/gateway-hooks.md) | [简体中文](../../zh-CN/plugin-development/gateway-hooks.md) | 日本語

Gateway Hook は API Key 認証から最終 response までの指定 stage に参加します。[`examples/hook-trace-go`](../../../plugin-devkit/examples/hook-trace-go) から始められます。

`privacy_pre`、`guardrail_pre`、`cache_lookup`、`route_candidates`、`route_rank`、`request_transform`、`provider_call`、`guardrail_post`、`usage_attribution`、`cache_write`、`settlement`、`trace_export` など、境界の狭い stage を 1 つ選びます。Hook が read/write する data class を正確に宣言します。TokenHub は stage contract 外の write を拒否し、model identity など Core-owned field を保護します。

security/admission には `fail_closed`、任意 cache には `fail_open`、Provider attempt には `skip_route`、settlement/trace には `observe_only` を意図的に選びます。handler は deterministic、timeout 制限付き、cancellation-aware とし、raw credential を log に残しません。

`tokenhub-plugin-test hook` の後、TokenHub integration test で順序と失敗動作を検証します。完全な stage/envelope contract は[ガイド](guide.md) を参照してください。
