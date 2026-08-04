# Bundle スキーマ

`CanonicalMigrationBundle` は Source Adapter と Sink 間のバージョン付き中間表現です。

## 主要原則

- 平文の秘密を保存しない。認証情報は `SecretRef`（`{"$secretRef": "ENV_NAME"}`）で参照。
- `schema_version` フィールドで前方/後方互換性を検証。
- JSON Schema 検証後に apply フローに入る。

## 含まれるリソース

Providers、ProviderResources、Models、Routes、Teams、Projects、Users、APIKeys、QuotaPolicies。

JSON Schema 定義: `backend/internal/migration/bundle/schema/bundle.schema.json`。
詳細は `docs/migration/bundle-schema.md` を参照してください。
