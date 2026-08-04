# E2E テスト

移行フレームワークは Docker Compose ベースの E2E テストを提供します。実際の LiteLLM と TokenHub を起動し、TokenHub Admin API を通じて extract → plan → apply → verify → re-apply → rollback のフロー全体を検証します。OpenAI 互換アップストリームは Nginx、ユーザーインポートに必要な SMTP は Mailpit で提供します。

## 実行方法

```bash
# サービス起動
docker compose -f deploy/docker-compose.migration-e2e.yml up -d --wait

# テスト実行
cd sdk/migration-e2e && npm ci && npm run test:litellm

# クリーンアップ
docker compose -f deploy/docker-compose.migration-e2e.yml down -v
```

## CI トリガー

CI では `migration:e2e` ラベルを付与することで E2E テストが実行されます。

テストは再 apply の作成数と更新数がともに 0 であること、checkpoint と一回限りの API key ファイルが保存されること、rollback が新規ユーザーを削除することを確認します。詳細は `docs/migration/e2e.md` を参照してください。
