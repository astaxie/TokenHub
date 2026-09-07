# Provider プラグイン

Language: [English](../../plugin-development/provider-plugins.md) | [简体中文](../../zh-CN/plugin-development/provider-plugins.md) | 日本語

Provider プラグインは TokenHub を上流モデルサービスまたは subscription account に接続します。最小契約は [`examples/provider-mock-go`](../../../plugin-devkit/examples/provider-mock-go) から始め、より広い operation は Kimi と GLM Example を参照します。

> **ランタイムでの利用可否:** これらの外部 Provider の Example と Schema は開発契約です。パッケージのインストールと検証は可能ですが、ホストレベルの隔離をまだ強制できないため、現在の TokenHub ランタイムは外部 Provider コマンドを起動前に拒否します。プロセス内の組み込み Provider アダプターは引き続き動作します。

TokenHub は、設定された `provider-catalog.json` の全 158 entry を組み込み Provider plugin package として表現します。各 package には検査可能な Manifest、README、license、catalog metadata があります。Catalog plugin は vendor identity、Provider 追加画面 metadata、詳細ページ、lifecycle を所有し、Host Adapter は `OpenAI-Compatible` などの実行可能 protocol を所有します。複数の Catalog plugin は実行コードを複製せずに 1 つの adapter を共有できます。無効にすると Provider 追加画面から vendor が除外され、再び有効にすると直ちに復元されます。

Provider credential と connection 設定は Provider 管理ページに置き、架空の plugin Settings ページは作りません。Provider plugin の詳細ページから Provider 管理へ移動できます。model category は catalog metadata であり、独立した plugin ではありません。

Provider type、resource type、operation、protocol policy、model discovery、credential scope、必要な Admin UI contribution を宣言します。`ServeProvider` は `stdio-json-v1` で投影済みの Provider、resource、model、request、credential data を受け取り、Core storage へ直接アクセスしません。

本番実装では、固定 Example response を cancel 可能な実際の上流 call に置き換え、secret を stdout、error、audit metadata、fixture に出力しません。chat、streaming、error、usage、timeout、cancellation、discovery、protocol conversion をテストします。定期的な quota refresh と account sync は[バックグラウンドジョブ](background-jobs.md) に分離します。

`tokenhub-plugin-test provider` で検証後、実際の TokenHub model route と API Key で integration test を行います。完全な投影契約は[ガイド](guide.md) を参照してください。
