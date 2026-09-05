# TokenHub プラグイン開発

Language: [English](../../plugin-development/README.md) | [简体中文](../../zh-CN/plugin-development/README.md) | 日本語

このディレクトリは TokenHub プラグイン開発ドキュメントの統一エントリです。実行可能な SDK、contract harness、参考 package は [`plugin-devkit`](../../../plugin-devkit/README.md) にあり、インストール済み package は `TOKENHUB_PLUGIN_DIR` にあります。ホストされる Marketplace は別の配布 index であり、この Devkit ではありません。

## ここから始める

| 目的 | ドキュメント |
| --- | --- |
| 最初の package を作成してテストする | [Getting Started](getting-started.md) |
| `plugin.yaml` を理解する | [Manifest リファレンス](manifest-reference.md) |
| 上流モデルサービスに接続する | [Provider プラグイン](provider-plugins.md) |
| request pipeline に参加する | [Gateway Hook](gateway-hooks.md) |
| theme、layout、宣言的 panel を追加する | [UI テンプレート](ui-templates.md) |
| 定期または運用作業を実行する | [バックグラウンドジョブ](background-jobs.md) |
| ZIP と release を作成する | [パッケージと公開](packaging-and-release.md) |
| すべての契約と移行詳細を読む | [完全なアーキテクチャと開発ガイド](guide.md) |

## ディレクトリ境界

```text
docs/plugin-development/     ドキュメントとナビゲーション
plugin-devkit/               SDK、contract harness、fixture、Examples
your-plugin-repository/      実際のプラグインソースと release workflow
TOKENHUB_PLUGIN_DIR/         TokenHub deployment に導入済みの package
marketplace index            release version、URL、checksum、signature
```

Devkit はプラグイン開発に使用します。fixture 動作を置き換え、Provider 固有のテストを追加するまで `examples/` を本番 integration として使用しないでください。
