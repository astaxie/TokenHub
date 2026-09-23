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

ZIP を作成する前に、パッケージに対応する Devkit 契約コマンドを実行します。最終 ZIP は TokenHub にインストールしてアーカイブの受理とファイル検査を確認できますが、実行動作は Devkit で検証してください。現行 TokenHub ランタイムは外部コマンドを起動しません。

## バージョンと公開

成果物の内容が変わるたびに、新しいセマンティックバージョンを使用します。プラグイン ID と公開済み capability ID は安定させます。TokenHub と Plugin API の互換範囲、権限変更、チェックサム、リリースノート、ダウンロード URL をリリース情報に記録します。

TokenHub Marketplace はリモートの HTTPS JSON インデックスです。プラグインとリリースバージョンを記述し、不変の ZIP 成果物を参照します。`plugin-devkit` や `TOKENHUB_PLUGIN_DIR` とは別のものです。Marketplace レコードには、少なくとも成果物 URL と小文字の SHA-256 チェックサムを含めます。署名付きリリースでは Ed25519 署名 URL とキー ID も提供できます。

同じリリース URL で異なるバイト列を公開しないでください。チェックサムと署名は特定のアーカイブを保護するため、公開済みバージョンを再ビルドする場合は、新しいバージョンと成果物が必要です。

## インストールと検査

管理者は**プラグイン管理 > プラグインを探す**から利用可能なリリースを選ぶか、**手動インストール**で直接 URL または ZIP を指定できます。導入前に互換性、チェックサム、信頼情報、権限差分を確認します。直接 URL にはパッケージのチェックサムが必要です。

TokenHub は検証済みパッケージを `TOKENHUB_PLUGIN_DIR` に展開し、install、update、enable、disable、rollback の後に再評価します。宣言的な画面の変更には通常 TokenHub service の再起動は不要です。`entry.backend.command` を持つパッケージは現行リリースでは動作できません。enable を試みると **Startup Failed** を記録し、インストール済みで検査可能なまま Provider、Hook、ジョブ、Action の機能を一切登録しません。lifecycle fact は独立しているため、Installed を実行可能または active と解釈してはいけません。その後、次を検証します。

- 詳細ページでバージョン、互換性、信頼状態を確認する
- ファイルページでファイル一覧と想定したパッケージ内容を照合する
- `plugin.yaml` で宣言済みジョブ、Hook、Action、権限を確認し、詳細ページで対応済みの宣言的 UI 貢献を確認する
- 対応する Devkit 契約コマンドで実行動作を検証する。TokenHub 経由の実 Provider またはゲートウェイ外部実行は利用できない

更新でも同じレビューと検証を繰り返します。永続データを変更する更新の前には TokenHub の関連状態をバックアップし、制御されたロールバックのために以前の不変 ZIP を保持してください。

権限プレビューは選択した URL とチェックサムに対応します。いずれかの値やインストール元を変更するとプレビューは消去され、Marketplace のパッケージを選択するとそのパッケージの URL インストールに切り替わります。インストール済み一覧は導入可能な Marketplace リリースから更新を表示し、更新エラーと完了状態を通知します。検証済み配布情報のないオンライン検出レコードは引き続き直接インストールできません。

チェックサム、信頼、依存関係の検証で更新が拒否された場合、現在のパッケージと既存のロールバック用バックアップを保持します。プラグインの置き換えでも、ID が `.previous` で終わるものを含め、他のインストール済みプラグインは保持されます。
