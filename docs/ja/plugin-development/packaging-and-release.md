# パッケージングとリリース

言語: [English](../../plugin-development/packaging-and-release.md) | [简体中文](../../zh-CN/plugin-development/packaging-and-release.md) | 日本語

本番プラグインは独立したソースディレクトリまたはリポジトリで管理します。`plugin-devkit/examples/` は開発と契約テスト用のフィクスチャであり、インストール元でも TokenHub Marketplace でもありません。

## パッケージのビルド

リリース ZIP には、検出可能な `plugin.yaml` を 1 つだけ含め、必要な場合はランタイムエントリポイントと、パッケージ相対パスで参照する Schema やアセットを含めます。manifest はアーカイブのルート、または 1 つのトップレベルディレクトリ内に配置できます。

シンボリックリンク、認証情報、`.env` ファイル、ローカルデータベース、ログ、バージョン管理メタデータ、ビルドキャッシュを含めないでください。`entry.backend.command` はパッケージルートからの相対パスにし、実行権限を保持します。

例:

```bash
cd plugin-devkit
go build -o examples/background-heartbeat-go/bin/background-heartbeat-go \
  ./examples/background-heartbeat-go
cd examples/background-heartbeat-go
zip -r ../../../background-heartbeat-go.zip plugin.yaml bin
cd ../../..
shasum -a 256 background-heartbeat-go.zip
```

ZIP を作成する前に、パッケージに対応する Devkit 契約コマンドを実行します。アーカイブの構造と実行権限もリリース契約の一部なので、最終 ZIP は TokenHub でのインストールテストも行ってください。

## バージョンと公開

成果物の内容が変わるたびに、新しいセマンティックバージョンを使用します。プラグイン ID と公開済み capability ID は安定させます。TokenHub と Plugin API の互換範囲、権限変更、チェックサム、リリースノート、ダウンロード URL をリリース情報に記録します。

TokenHub Marketplace はリモートの HTTPS JSON インデックスです。プラグインとリリースバージョンを記述し、不変の ZIP 成果物を参照します。`plugin-devkit` や `TOKENHUB_PLUGIN_DIR` とは別のものです。Marketplace レコードには、少なくとも成果物 URL と小文字の SHA-256 チェックサムを含めます。署名付きリリースでは Ed25519 署名 URL とキー ID も提供できます。

同じリリース URL で異なるバイト列を公開しないでください。チェックサムと署名は特定のアーカイブを保護するため、公開済みバージョンを再ビルドする場合は、新しいバージョンと成果物が必要です。

## インストールと検証

管理者は**プラグイン管理 > プラグインをインストール**から Marketplace リリース、直接 URL、または ZIP アップロードを選択できます。インストール前に互換性、チェックサム、信頼情報、権限差分を確認します。直接 URL からのインストールにはパッケージのチェックサムが必要です。

TokenHub は検証済みパッケージを `TOKENHUB_PLUGIN_DIR` に展開します。ランタイム capability を追加または変更したパッケージは `pending_restart` になる場合があります。新しいバージョンが有効か確認する前にバックエンドを再起動してください。その後、次を検証します。

- 詳細ページでバージョン、互換性、信頼状態を確認する
- ファイルページでファイル一覧と想定したパッケージ内容を照合する
- 設定ページで権限、ジョブ、Hook、UI 宣言を確認する
- 最初に非本番の認証情報で実際の Provider またはゲートウェイ動作を検証する

更新でも同じレビューと検証を繰り返します。永続データを変更する更新の前には TokenHub の関連状態をバックアップし、制御されたロールバックのために以前の不変 ZIP を保持してください。
