# TokenHub プラグイン Getting Started

Language: [English](../../plugin-development/getting-started.md) | [简体中文](../../zh-CN/plugin-development/getting-started.md) | 日本語

[`plugin-devkit`](../../../plugin-devkit/README.md) を使って Plugin API v1 を学び、検証します。`examples/` は contract fixture であり、本番 integration ではありません。

## 1. Devkit を検証する

```bash
cd plugin-devkit
go test ./...
go run ./cmd/tokenhub-plugin-test provider \
  --package "$PWD/examples/provider-mock-go"
```

## 2. 実際のプラグインを始める

実際のプラグインは独立したディレクトリまたは repository で開発します。最も近い Example を複製し、plugin ID、capability ID、実装、fixture、配布 metadata を一緒に変更します。実装に必要な最小権限だけを宣言します。

Go プラグインでは独自の module を初期化し、レビュー済みの Devkit バージョンまたは commit を固定します。

```bash
go mod init example.com/your-plugin
go get github.com/astaxie/TokenHub/plugin-devkit@<approved-version>
```

SDK の import パスは `github.com/astaxie/TokenHub/plugin-devkit/sdk/go/tokenhubplugin` です。

```text
your-plugin/
├── plugin.yaml
├── go.mod              # Go module と固定済み Devkit 依存関係
├── bin/your-plugin
├── ui/                 # 任意の Schema または asset
└── contract-tests/     # plugin 固有のテスト
```

## 3. 契約を検証する

```bash
go run ./cmd/tokenhub-plugin-test provider --package /path/to/your-plugin
go run ./cmd/tokenhub-plugin-test hook --package /path/to/your-plugin
go run ./cmd/tokenhub-plugin-test background --package /path/to/your-plugin
go run ./cmd/tokenhub-plugin-test action --package /path/to/your-plugin
```

## 4. package を導入する

実行ファイルを build し、`plugin.yaml` を 1 つだけ含む ZIP と SHA-256 checksum を作成し、TokenHub の **Install Plugin** 画面から導入します。TokenHub は package を `TOKENHUB_PLUGIN_DIR` に書き込みます。`plugin-devkit/examples/` にあるだけでは runtime に読み込まれません。

次に [Manifest リファレンス](manifest-reference.md) と[パッケージと公開](packaging-and-release.md) を参照してください。
