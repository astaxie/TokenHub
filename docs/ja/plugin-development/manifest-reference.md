# プラグイン Manifest リファレンス

Language: [English](../../plugin-development/manifest-reference.md) | [简体中文](../../zh-CN/plugin-development/manifest-reference.md) | 日本語

各 package の root に `plugin.yaml` を 1 つ配置します。TokenHub は capability を登録または実行する前にこれを検証します。

| フィールド | 目的 |
| --- | --- |
| `schema_version` | Manifest Schema version。Plugin API v1 では `1` |
| `id` | 安定したグローバル一意 plugin ID |
| `version` | plugin package version |
| `tokenhub.plugin_api` | runtime contract major。現在は `v1` |
| `kinds` | `provider`、`extension`、`admin_ui`、`sim` |
| `placement` | `gateway_chain`、`background`、`presentation`、過渡的 `management_action` |
| `entry` | backend command/protocol または frontend Schema path |
| `capabilities` | Provider type、Hook、Job、Action、UI/template 宣言 |
| `permissions` | 最小権限の data read/write 宣言 |
| `distribution` | repository、homepage、license、release、checksum、signature metadata |

capability ID と plugin ID は互換性契約です。既存の Provider、route、resource、configuration reference を移行で保持しない限り、update で名前を変更しないでください。

backend command は package-relative path と `stdio-json-v1` を使います。package 外への path、symlink、曖昧な Manifest、非互換の宣言は拒否されます。

Schema、stage policy、互換性の詳細は[完全なガイド](guide.md) を参照してください。
