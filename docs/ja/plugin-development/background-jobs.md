# バックグラウンドジョブプラグイン

言語: [English](../../plugin-development/background-jobs.md) | [简体中文](../../zh-CN/plugin-development/background-jobs.md) | 日本語

クォータ更新、アカウント同期、クリーンアップ、レポート、ヘルスチェックなど、モデルリクエスト経路に置くべきでない処理にはバックグラウンドジョブプラグインを使用します。ジョブは宣言したスケジュールで実行するか、プラグイン管理画面から管理者が手動で実行できます。

## ジョブの宣言

種類に `extension`、配置先に `background` を指定し、1 つ以上の `capabilities.background_jobs` エントリを追加します。

```yaml
kinds: [extension]
placement: [background]
entry:
  backend:
    protocol: stdio-json-v1
    command: bin/your-plugin
capabilities:
  background_jobs:
    - id: quota.refresh
      title: Refresh quota
      capability: provider.quota.refresh
      subject: example-provider
      schedule: "@startup"
      timeout_millis: 5000
      max_concurrency: 1
      retry:
        max_attempts: 2
        backoff_millis: 1000
      input_schema:
        type: object
        required: [resource_id]
        properties:
          resource_id:
            type: string
      output_schema:
        type: object
```

プラグイン ID とジョブ ID は安定した互換性契約として扱います。入力を小さく保ち、明示的な JSON Schema を使用し、タイムアウトと同時実行数を制限してください。処理が一部完了した後でも安全な再試行規則を選びます。

## ハンドラーの実装

`ServeBackgroundJob` は標準入力から 1 回の `stdio-json-v1` 呼び出しを読み、構造化された結果を標準出力へ書きます。呼び出しにはプラグイン ID、ジョブ ID、トリガー、操作者、ペイロードが含まれます。ログは標準エラーだけに出力し、結果をサニタイズして、認証情報や未加工の Provider 応答を含めないでください。

ハンドラーは可能な限り冪等にします。外部状態を変更する場合は、安定した操作キーを使うか、再試行を安全にするための状態を保存してください。スケジュールは厳密な 1 回実行を保証しません。

## テスト

Devkit には完全なフィクスチャと契約コマンドがあります。

```bash
cd plugin-devkit
go test ./...
go run ./cmd/tokenhub-plugin-test background \
  --package "$PWD/examples/background-heartbeat-go"
```

フィクスチャを別のプラグインワークスペースへコピーし、manifest、ハンドラーの検証、Schema、テストを一緒に更新します。インストールとバックエンド再起動の後、プラグインの**詳細**ページで登録済みジョブを確認し、バックグラウンドジョブの拡張タイプページから実行します。

配布前に、呼び出し例を含む[完全ガイド](guide.md)と[パッケージングとリリース](packaging-and-release.md)を参照してください。
