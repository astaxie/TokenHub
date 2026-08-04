# 移行フレームワークアーキテクチャ

## パイプライン概要

```
Source Gateway  ──extract──▶  CanonicalMigrationBundle  ──apply──▶  TokenHub
(LiteLLM, ...)               (バージョン付き JSON、平文秘密なし)        (Admin API / DB)
```

- **Source Adapter** — 競合ゲートウェイの設定を読み取り、標準化された bundle を出力。
- **Sink** — bundle を受け取り、Admin API または DB 直接接続で幂等な upsert を実行。ロールバック用の checkpoint を記録。
- **CLI** (`tokenhub-migrate`) — `extract`、`plan`、`apply`、`verify`、`rollback` サブコマンド。

## ディレクトリ構成

```
backend/
  cmd/tokenhub-migrate/          # CLI エントリポイント
  internal/migration/
    bundle/                      # 中間フォーマット + JSON Schema
    cli/                         # cobra コマンド登録
    sink/tokenhub/               # StoreSink（DB 直接）と HTTPSink（リモート API）
    source/litellm/              # LiteLLM アダプター
```

## 拡張

新しい Source Adapter を追加するには `source.Extractor` インターフェースを実装し、`source.Register()` で登録してください。

完全な英語版は `docs/migration/architecture.md` を参照してください。
