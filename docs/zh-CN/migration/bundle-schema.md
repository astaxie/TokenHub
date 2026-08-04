# Bundle 规范

`CanonicalMigrationBundle` 是 Source Adapter 与 Sink 之间的版本化中间表示。

## 核心原则

- 不存储明文密钥，凭证以 `SecretRef`（`{"$secretRef": "ENV_NAME"}`）形式引用。
- 通过 `schema_version` 字段进行向前/向后兼容性检查。
- 经 JSON Schema 验证后再进入 apply 流程。

## 包含资源

Providers、ProviderResources、Models、Routes、Teams、Projects、Users、APIKeys、QuotaPolicies。

完整 JSON Schema 定义参见 `backend/internal/migration/bundle/schema/bundle.schema.json`。
详细字段说明请参阅 `docs/migration/bundle-schema.md`。
