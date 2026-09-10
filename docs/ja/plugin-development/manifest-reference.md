# プラグイン Manifest リファレンス

Language: [English](../../plugin-development/manifest-reference.md) | [简体中文](../../zh-CN/plugin-development/manifest-reference.md) | 日本語

各 package の root に `plugin.yaml` を 1 つ配置します。TokenHub は capability を登録または実行する前にこれを検証します。

| フィールド | 目的 |
| --- | --- |
| `schema_version` | Manifest Schema version。Plugin API v2 は `2`、v1 adapter は `1` を受け付ける |
| `id` | 安定したグローバル一意 plugin ID |
| `version` | plugin package version |
| `summary` | v2 で必須の、利用者向け価値説明 |
| `category` | `provider_integration`、`request_pipeline`、`ui_template`、`automation` の主分類 1 つ |
| `tokenhub.plugin_api` | Manifest/runtime contract major。現在は `v2`、adapter 経由で `v1` もサポート |
| `dependencies` | 任意の plugin ID と version 制約 |
| `settings.scopes` | 実在する編集可能設定を宣言する。設定がなければ省略する |
| `kinds` | `provider`、`extension`、`admin_ui`、`sim` |
| `placement` | `gateway_chain`、`background`、`presentation`、過渡的 `management_action` |
| `entry` | backend command/protocol または frontend Schema path |
| `capabilities` | Provider type、Hook、Job、Action、UI/template 宣言 |
| `permissions` | 最小権限の data read/write 宣言 |
| `distribution` | repository、homepage、license、release、checksum、signature metadata |

capability ID と plugin ID は互換性契約です。既存の Provider、route、resource、configuration reference を移行で保持しない限り、update で名前を変更しないでください。

package-relative path と `stdio-json-v1` transport は、外部 backend command の契約を定義します。transport 名は Plugin API v2 とは独立しています。package 外への path、symlink、曖昧な Manifest、非互換の宣言は拒否されます。現在のランタイムは必要なホストレベルのプロセス、ネットワーク、リソース隔離を強制できないため、フェイルクローズし、動的に読み込まれた外部コマンドを起動前にすべて拒否します。この制限は、プロセス内の組み込みプラグインや宣言的な画面貢献には影響しません。

Schema、stage policy、互換性の詳細は[完全なガイド](guide.md) を参照してください。
