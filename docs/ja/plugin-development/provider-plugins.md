# Provider プラグイン

Language: [English](../../plugin-development/provider-plugins.md) | [简体中文](../../zh-CN/plugin-development/provider-plugins.md) | 日本語

Provider プラグインは TokenHub を上流モデルサービスまたは subscription account に接続します。最小契約は [`examples/provider-mock-go`](../../../plugin-devkit/examples/provider-mock-go) から始め、より広い operation は Kimi と GLM Example を参照します。

TokenHub は、設定された `provider-catalog.json` の各 entry を組み込み Provider プラグインとして表現します。Catalog プラグインは vendor identity、Provider 追加画面の metadata、詳細ページ、lifecycle state を所有し、protocol プラグインは `OpenAI-Compatible` などの実行可能 adapter を所有します。そのため、複数の Catalog プラグインが実行コードを複製せずに 1 つの protocol adapter を共有できます。Catalog プラグインを無効にすると Provider 追加画面からその vendor が除外され、再び有効にすると直ちに復元されます。

Provider type、resource type、operation、protocol policy、model discovery、credential scope、必要な Admin UI contribution を宣言します。`ServeProvider` は `stdio-json-v1` で投影済みの Provider、resource、model、request、credential data を受け取り、Core storage へ直接アクセスしません。

本番実装では、固定 Example response を cancel 可能な実際の上流 call に置き換え、secret を stdout、error、audit metadata、fixture に出力しません。chat、streaming、error、usage、timeout、cancellation、discovery、protocol conversion をテストします。定期的な quota refresh と account sync は[バックグラウンドジョブ](background-jobs.md) に分離します。

`tokenhub-plugin-test provider` で検証後、実際の TokenHub model route と API Key で integration test を行います。完全な投影契約は[ガイド](guide.md) を参照してください。
