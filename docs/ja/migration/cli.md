# CLI リファレンス

## インストール

```bash
cd backend && go build -o tokenhub-migrate ./cmd/tokenhub-migrate/
```

## コマンド一覧

| コマンド | 説明 |
|--------|------|
| `extract litellm --from <yaml> --out <json>` | LiteLLM 設定から bundle を抽出 |
| `plan --bundle <json>` | Dry-run、作成/更新/スキップ数を表示 |
| `apply --bundle <json>` | 移行を実行。ロールバック用 checkpoint を `<bundle>.checkpoint.json` に、新規発行 API key の平文を `<bundle>.new-keys.json` に保存（いずれも 0600、`--checkpoint-out` / `--new-keys-out` でパス変更可） |
| `verify --bundle <json>` | 移行後の整合性を検証 |
| `rollback --checkpoint <json>` | 作成されたリソースをロールバック |

`plan` / `apply` / `verify` / `rollback` はリモート TokenHub ターゲット（`--to` または `TOKENHUB_API`）が必須です。未指定の場合は終了コード 5 で拒否されます。

> 注意：`apply` は Admin CSV インポートエンドポイント経由でユーザーを作成します。このエンドポイントはターゲットインスタンスにアクティブなメール通知チャネルが設定されていることを要求します。未設定の場合、新規ユーザーを含む bundle の apply は失敗します。また、新規インポートされた各ユーザーには apply 中にパスワードリセットメールが送信されます。
>
> リモート apply はトランザクションではありません。前半のリソース変更後に後続処理が失敗した場合も、コマンドは部分ロールバック用 checkpoint と発行済みの一回限りの API key を保存してから終了コード 5 を返します。

## 共通フラグ

| フラグ | 説明 | デフォルト |
|--------|------|------------|
| `--secret-source` | シークレット解決元：`env` または `file` | `env` |
| `--secret-file` | `--secret-source=file` で使用する `key=value` ファイル | — |
| `--id-strategy` | ID 生成方式：`stable`、`prefixed`、`source` | `prefixed` |

## 環境変数

| 変数 | 説明 |
|------|------|
| `TOKENHUB_API` | TokenHub Admin API URL（必須、`--to` でも指定可） |
| `TOKENHUB_ADMIN_TOKEN` | Admin API 認証トークン |

完全なコマンド仕様は `docs/migration/cli.md` を参照してください。
