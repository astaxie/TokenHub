# TokenHub プラグインアーキテクチャと開発ガイド

Language: [English](../../plugin-development/guide.md) | [简体中文](../../zh-CN/plugin-development/guide.md) | 日本語

このガイドは、現在の TokenHub のプラグイン方針と、その上でどう開発するかを説明します。プラグイン作者、プラットフォームエンジニア、運用担当者向けです。

このガイドは、まず最小のプラグインを動かし、その後で完全な契約を説明する順序で構成しています。WordPress がメインファイルのヘッダーからプラグインを検出するのと同様に、TokenHub はパッケージルートの `plugin.yaml` からプラグインを検出、検証、読み込みます。TokenHub ではさらに、配置先、capability、最小権限を明示的に宣言する必要があります。

> **現在の実装範囲:** Plugin API v2 が現在の Manifest contract であり、既存 package は v1 adapter 経由で引き続き動作します。ランタイムはホストレベルのプロセス、ネットワーク、リソース隔離をまだ強制できないため、動的に読み込まれた外部 Provider、Gateway Hook、バックグラウンドジョブ、管理 Action の各コマンドを起動前にすべて拒否します。このガイドのコマンド例は、現在デプロイ可能な integration ではなく、開発契約を定義するものです。UI template は宣言的な theme/layout capability で、任意の React / JavaScript 拡張機構ではありません。すべての built-in plugin に検査可能な package file があります。Manifest が実在する編集可能設定を宣言した場合だけ Settings route を表示し、source file は read-only preview のままで管理画面から編集できません。

TokenHub は core を小さく保ちます。

- core は認証、ルーティング、課金、監査、互換性、アップグレード安全性を担当する
- 変化の速い部分は plugin に任せる
- built-in plugin と external plugin は同じ契約を使う
- UI テンプレート、Provider、チェーン注入、バックグラウンドジョブ、Admin UI 貢献はすべて明示的なメタデータから入る

## インストール済みプラグインの管理

TokenHub は Office Add-ins を企業ガバナンスの参考にし、Obsidian を plugin management interaction の参考にします。Plugin Management の第 1 階層は 2 つです。**インストール済みプラグイン**は検索、status filter、version、update、lifecycle action を扱い、**プラグインを探す**は利用可能な plugin と Marketplace、URL install、ZIP upload、checksum、permission diff preview を扱います。Installed view の統一リスト左側に Provider Integration、Request Pipeline、UI Template、Automation の 4 category filter を直接配置し、独立した Extension Types table は置きません。

インストール済み一覧では、プラグイン名と **詳細** が概要を開きます。**設定** は編集可能な項目があるプラグインだけに表示され、設定ページを直接開きます。UI テンプレート一覧では、編集可能な theme token があるテンプレートは設定を、それ以外は詳細を開きます。デフォルトの変更は別の操作なので、設定を開くだけで現在の UI が意図せず切り替わることはありません。この階層は WordPress の[インストール、更新、管理パターン](https://www.waimaob2c.com/wordpress-plugins)を参考にしていますが、企業ゲートウェイに適さないオンラインコード編集や自動更新はコピーしません。

| ルート | 用途 |
| --- | --- |
| `/plugins` | インストール済みプラグイン一覧とライフサイクル操作 |
| `/plugins/[pluginId]` | 目的、利用場所、状態、バージョン、更新、提供元の信頼性、互換性を分かりやすく表示し、実装宣言は折りたたんだ「開発者情報」に配置 |
| `/plugins/[pluginId]/files` | パッケージ相対パスのファイル一覧と安全なテキストプレビュー |
| `/plugins/[pluginId]/settings` | プラグインが編集可能な値を宣言した場合に、安全な theme token を分かりやすいコントロールで調整 |

概要には capability の生値やシリアライズされた JSON を表示しません。「開発者情報」はデフォルトで折りたたまれ、展開すると capability、Hook、UI 拡張、管理操作、バックグラウンドジョブの各項目で用途を説明してから技術 ID を表示します。

「ファイル」ページは WordPress の Plugin File Editor をそのまま再現しません。TokenHub はパッケージの絶対パスを公開せず、インストール済み実行コードを編集しません。symbolic link は除外し、binary、runtime state、hidden file、credential、secret、private、サイズ超過ファイルのプレビューを拒否します。これにより、管理画面をリモートコード実行面にせず、パッケージを確認できます。

管理者認証が必要な inspection API は読み取り専用です。

- `GET /api/admin/plugins/{plugin_id}/detail`
- `GET /api/admin/plugins/{plugin_id}/file?path={package-relative-path}`

built-in と external plugin はどちらも、file count、total size、kind、および安全条件を満たす Manifest、README、license、source、configuration、Schema を含む検査可能な package inventory を表示します。概念上の参考は WordPress の[プラグイン管理ドキュメント](https://wordpress.org/documentation/article/manage-plugins/)ですが、TokenHub は独自の Manifest、permission、security model を維持します。

## 1. プラグイン家族

TokenHub はプラグインをいくつかの明確な家族に分けます。

| 家族 | 担当するもの | 例 |
| --- | --- | --- |
| Provider Integration | 上流モデル接続、認証、探索、quota | Codex、Kimi、Gemini、Anthropic、OpenAI-compatible Provider |
| Request Pipeline | ユーザー要求から upstream 応答までの経路 | privacy control、routing、cache、context optimization、trace export |
| UI Template | シェル全体の見た目、layout、template package | shell theme、page template、dashboard composition |
| Automation | 定期実行または operator-triggered work | quota refresh、sync、cleanup、reporting |

Admin UI 貢献は top-level family ではなく、能力面です。通常は Provider、チェーン注入、バックグラウンドジョブのどれかに付属します。

現在の repository では、互換性 payload の中で内部名 `sim` がまだ使われています。ユーザー向けには「UI テンプレート」と読んでください。

1 つの plugin が複数の家族をまたぐことはありますが、各家族は小さく保つべきです。例:

- Codex subscription plugin は Provider plugin であり、Admin UI、chain hook、background job も提供できる
- trace exporter は通常、`observe_only` の narrow なチェーン注入 plugin です
- quota sync worker は通常、バックグラウンドジョブ plugin です
- shell replacement は通常、UI テンプレート plugin です

## 2. Manifest 契約

各 plugin package は `plugin.yaml` で記述します。

```yaml
schema_version: 2
id: tokenhub.provider.kimi-go
name: Kimi Subscription Go Provider
version: 1.0.0
summary: Connect TokenHub to a Kimi subscription account.
description: Reference Go Provider plugin using the stdio-json-v1 transport.
category: provider_integration
tokenhub:
  plugin_api: v2
  min_core: 0.7.0
kinds:
  - provider
placement:
  - gateway_chain
entry:
  backend:
    protocol: stdio-json-v1
    command: bin/provider-kimi-go
capabilities:
  provider_types:
    - kimi_subscription
permissions:
  data:
    read:
      - provider_credentials
distribution:
  repository_url: https://github.com/astaxie/TokenHub
  homepage_url: https://github.com/astaxie/TokenHub/tree/main/plugin-devkit/examples/provider-kimi-go
  license: Apache-2.0
```

重要フィールド:

- `schema_version`: manifest version
- `tokenhub.plugin_api`: plugin API version
- `kinds`: `provider`、`admin_ui`、`sim`、`extension` のいずれか 1 つ以上
- `placement`: `presentation`、`gateway_chain`、`background`、`management_action` のいずれか 1 つ以上
- `capabilities`: 実際の能力面
- `permissions`: least privilege の宣言
- `distribution`: repository URL、homepage URL、checksum、signature、license metadata

`management_action` は運用者トリガーの操作のための過渡的な面です。新しい request-path の振る舞いは `gateway_chain` へ、繰り返し処理は `background` へ移すべきです。

## 3. Runtime 面

TokenHub には 3 つの core runtime 面と、1 つの過渡的な互換面があります。

- `ServeProvider`
- `ServeGatewayHook`
- `ServeBackgroundJob`
- `ServeAction` は互換性のための管理操作のみ

### 3.1 Provider invocation

Provider plugin が受け取るもの:

- operation
- provider projection
- resource projection
- provider model
- request payload
- credentials projection

これにより plugin は投影済みデータだけを扱い、core の内部実装には触れません。

### 3.2 Gateway hook invocation

Gateway hook plugin が受け取るもの:

- request ID
- stage
- envelope
- optional stage data

用途:

- privacy control
- route candidate generation and ranking
- cache lookup / cache write
- context optimization
- request / response transform
- trace export

### 3.3 Background job invocation

Background job plugin が受け取るもの:

- plugin ID
- job ID
- trigger
- actor
- payload

用途:

- quota sync
- heartbeat
- refresh
- cleanup
- reporting

### 3.4 Admin UI 貢献

Admin UI 貢献は独立した runtime 面ではありません。宣言的な panel、tab、card、settings section として描画され、実行は引き続き Core 経由で行います。

## 4. プラグインの作り方

安全な進め方は次の順序です。

1. まず家族を決める
2. 最小の capability を決める
3. manifest を書く
4. runtime handler または UI 貢献を実装する
5. contract tests を追加する
6. ローカルで実行する
7. marketplace に公開する
8. TokenHub にインストールしてパッケージ検査とライフサイクル表示を確認する

コードを書く前に次を答えます。

- これは Provider 連携か？
- これは UI テンプレートか Admin UI 貢献か？
- これは chain injection の問題か？
- これはバックグラウンドジョブか？
- これは一時的な管理操作か？

答えられないなら、境界がまだ曖昧です。

### 4.1 まず最小から始める

最初から全部入れないでください。

- Provider plugin: 1 つの provider type と 1 つの resource / route contract から始める
- チェーン注入 plugin: 1 つの hook stage から始める
- バックグラウンドジョブ plugin: 1 つの job から始める
- UI テンプレート plugin: 1 つの template、shell、layout 貢献から始める

### 4.2 handler を実装する

handler は短く保ちます。

- invocation を解析する
- plugin の処理を実行する
- 構造化結果を返す
- secret を出力しない

### 4.3 contract tests を追加する

すべての plugin family で contract tests が必要です。

確認項目:

- manifest が解析できるか
- capability が揃っているか
- 入出力の形が正しいか
- secret が漏れないか
- failure の振る舞いが想定どおりか

### 4.4 ローカル contract kit を実行する

Plugin Devkit にはローカル harness があります。

```bash
go test ./...
go run ./cmd/tokenhub-plugin-test provider --package "$PWD/examples/provider-kimi-go"
go run ./cmd/tokenhub-plugin-test hook --package "$PWD/examples/hook-trace-go"
go run ./cmd/tokenhub-plugin-test background --package "$PWD/examples/background-heartbeat-go"
```

`--package` は自分の plugin ディレクトリに置き換えてください。

### 4.5 5 分で最初のプラグインを動かす

最短の出発点は、リポジトリで管理されている heartbeat バックグラウンドジョブのサンプルです。

```text
plugin-devkit/
├── cmd/tokenhub-plugin-test/          # ローカル契約テストツール
├── sdk/go/tokenhubplugin/             # Go プロトコル補助
└── examples/background-heartbeat-go/
    ├── main.go
    └── plugin.yaml
```

実行ファイルをビルドしてから契約テストを実行します。

```bash
cd plugin-devkit
mkdir -p examples/background-heartbeat-go/bin
go build -o examples/background-heartbeat-go/bin/background-heartbeat-go \
  ./examples/background-heartbeat-go
go run ./cmd/tokenhub-plugin-test background \
  --package "$PWD/examples/background-heartbeat-go"
```

サンプルの manifest は、プラグインの識別情報と TokenHub から呼び出せる契約を一緒に定義します。

```yaml
schema_version: 2
id: tokenhub.background.heartbeat-go
name: Heartbeat Go Background Job
version: 1.0.0
summary: Run a supervised heartbeat job in the background.
description: Reference background job plugin.
category: automation
tokenhub:
  plugin_api: v2
  min_core: 0.7.0
kinds:
  - extension
placement:
  - background
entry:
  backend:
    protocol: stdio-json-v1
    command: bin/background-heartbeat-go
capabilities:
  background_jobs:
    - id: heartbeat.ping
      title: Heartbeat ping
      capability: contract.heartbeat
      subject: background-heartbeat-go
      schedule: "@startup"
      timeout_millis: 5000
      max_concurrency: 1
      retry:
        max_attempts: 2
        backoff_millis: 10
      input_schema:
        type: object
        required: [resource_id]
        properties:
          resource_id:
            type: string
          count:
            type: integer
      output_schema:
        type: object
        required: [resource_id, heartbeat, trigger, actor_id]
        properties:
          resource_id:
            type: string
          heartbeat:
            type: string
          trigger:
            type: string
          actor_id:
            type: string
          count:
            type: integer
```

`main.go` は標準入力から JSON invocation を 1 件読み、標準出力へ JSON result を 1 件書きます。ログと診断は必ず標準エラーへ出力し、標準出力へ混在させないでください。

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/astaxie/TokenHub/plugin-devkit/sdk/go/tokenhubplugin"
)

type payload struct {
	ResourceID string `json:"resource_id"`
	Count      int64  `json:"count"`
}

func main() {
	os.Exit(tokenhubplugin.ServeBackgroundJob(
		context.Background(), os.Stdin, os.Stdout, os.Stderr, handle,
	))
}

func handle(_ context.Context, invocation tokenhubplugin.BackgroundJobInvocation) (tokenhubplugin.BackgroundJobResult, error) {
	input, err := tokenhubplugin.DecodeBackgroundPayload[payload](invocation)
	if err != nil {
		return tokenhubplugin.BackgroundJobResult{}, err
	}
	if input.ResourceID == "" {
		return tokenhubplugin.BackgroundJobResult{}, fmt.Errorf("resource_id is required")
	}
	return tokenhubplugin.BackgroundJobResult{Data: map[string]any{
		"resource_id": input.ResourceID,
		"heartbeat":   "ok",
		"trigger":     invocation.Trigger,
		"actor_id":    invocation.Actor.ID,
		"count":       input.Count,
	}, Metadata: map[string]string{"status": "ok"}}, nil
}
```

最初はサンプルを変更せずに動かしてください。その後、plugin ID、job ID、入出力 Schema、handler をまとめて変更します。`plugin.yaml`、handler、contract fixture の識別子は常に一致させます。

## 5. 各家族の作り方

### 5.1 Provider plugin

Provider plugin は TokenHub を model service や subscription account に接続します。

通常は次を宣言します。

- `provider_types`
- `provider_resource_types`
- provider policies
- `provider.default_base_url`
- `provider.model_discovery`
- `provider.credentials_scope`

主な責務:

- protocol translation
- model discovery
- quota または account sync
- credential refresh
- provider 固有の route 振る舞い
- provider 固有の UI metadata

subscription 型 Provider では、quota refresh と account sync を background job に寄せるのが理想です。

### 5.2 チェーン注入 plugin

チェーン注入 plugin は、ユーザー要求から upstream 応答までの経路を形作ります。

典型的な stage:

- `decode_normalize`
- `admission`
- `privacy_pre`
- `guardrail_pre`
- `cache_lookup`
- `route_candidates`
- `route_rank`
- `provider_call`
- `guardrail_post`
- `usage_attribution`
- `cache_write`
- `settlement`
- `trace_export`

典型的な policy:

- `fail_closed` は admission、privacy、guardrail、routing に使う
- `fail_open` は cache lookup と cache write に使う
- `skip_route` は provider call wrapper に使う
- `observe_only` は settlement と trace export に使う

良い chain plugin は deterministic で、境界が狭く、読むものと書くものを明確にします。

### 5.3 UI テンプレート plugin

UI テンプレート plugin は、視覚的アイデンティティと限定的な宣言レイアウトを提供します。実行ファイルは不要で、最小パッケージは `plugin.yaml` だけで構成できます。

```yaml
schema_version: 2
id: example.sim.operations
name: Operations UI Template
version: 1.0.0
description: A compact operations template for TokenHub.
summary: Apply a compact operations layout to TokenHub.
category: ui_template
tokenhub:
  plugin_api: v2
  min_core: 0.7.0
kinds:
  - sim
placement:
  - presentation
capabilities:
  sim:
    theme_tokens:
      - id: operations-light
        mode: light
        default: true
        tokens:
          bg: "#f5f7fa"
          surface: "#ffffff"
          ink: "#172033"
          accent: "#1677ff"
          border: "#d9d9d9"
    shell_layouts:
      - id: operations-shell
        navigation: sidebar
        density: compact
        content_width: fluid
        default: true
    page_templates:
      - id: provider-detail
        target: provider.detail
        layout: two_column
        regions: [main, side]
    dashboard_compositions:
      - id: operations-dashboard
        layout: grid
        cards:
          - contribution_id: cost-overview
            region: main
            size: wide
            order: 100
```

Plugin API v2 は現在、4 種類の UI template capability をサポートします。

| Capability | 現在宣言できる内容 |
| --- | --- |
| `theme_tokens` | allowlist にある色、テキスト、境界線、状態色、影の token。mode は `light`、`dark`、`all` |
| `shell_layouts` | `sidebar` navigation、`compact` / `comfortable` / `spacious` density、`fluid` / `comfortable` content width |
| `page_templates` | target、`single_column` / `two_column` / `grid` / `detail` layout、region 名 |
| `dashboard_compositions` | `grid` / `operations` / `compact_grid` layout、および card の位置、サイズ、順序 |

重要な制限:

- 任意の CSS、JavaScript、remote script、stylesheet URL、`@import`、`url(...)` は注入できません。
- インストール後はテンプレートを選択し、そのテンプレートが宣言した allowlist 内の安全な theme token だけを調整できます。調整内容は現在のブラウザに保存され、server-side または team-wide の設定ではありません。
- 設定ページには実際に編集できる theme 値だけを表示し、見た目への影響ごとに分類します。capability 宣言、配置、権限、raw Schema は設定として表示しません。
- 複数の theme variant はタブで切り替えます。各項目には分かりやすい名前、短い説明、入力コントロール、デフォルト復元操作があります。
- 編集可能な theme 値がないプラグインには設定入口を表示しません。draft preview、revision history、server-side one-click rollback は未実装です。

したがって、現在公開できる単位は構造化された theme/layout preset であり、完全な page builder や任意の CSS editor ではありません。バックエンド動作も必要なら、Provider、Hook、background job、management action に分離し、それぞれの権限を宣言してください。

### 5.4 バックグラウンドジョブ plugin

バックグラウンドジョブ plugin は定期作業や運用者トリガーの作業を扱います。

典型例:

- quota refresh
- heartbeat
- sync
- cleanup
- reporting

バックグラウンドジョブ plugin は、小さい input、予測可能な retry、脱敏済み result を持つべきです。

### 5.5 Admin UI 貢献

Admin UI 貢献は plugin の状態や運用操作を見せる declarative な panel、tab、card、route section です。

`plugin.yaml` から、パッケージ相対パスで JSON schema ファイルを参照します。

```yaml
kinds: [admin_ui]
placement: [presentation]
entry:
  frontend:
    schema: ui/admin-ui.schema.json
```

`ui/admin-ui.schema.json` の最小構造は次のとおりです。

```json
{
  "schema_version": 1,
  "contributions": [
    {
      "id": "provider-setup",
      "slot": "provider.form.section",
      "title": "Connection settings",
      "provider_types": ["example_provider"],
      "schema": {
        "placement": "advanced",
        "fields": [
          {"name": "base_url", "type": "url", "target": "provider"},
          {"name": "api_key", "type": "secret", "target": "plugin_options"}
        ]
      }
    }
  ]
}
```

利用可能な slot は `nav.section`、`dashboard.card`、`provider.catalog.card`、`provider.form.section`、`provider.model.panel`、`provider.resource.form.section`、`provider.resource.panel`、`route.detail.panel`、`settings.panel`、`report.template`、`theme.tokens`、`layout.preset`、`page.template`、`dashboard.composition` です。

Schema の control type には `text`、`secret`、`url`、`select`、`multi_select`、`switch`、`segmented`、`metric`、`table`、`log_viewer`、`code_viewer`、`action_button`、`oauth_button`、`file_import` があります。ただし renderer の対応範囲は slot ごとに異なります。公開前に対象ページで統合テストを行い、manifest の検証成功だけを全 control 対応の根拠にしないでください。

ルール:

- 実行は常に Core を通す
- plugin は RBAC を迂回してはいけない
- plugin は raw admin credential を直接使ってはいけない
- plugin 管理の操作は権限を絞り、監査可能であること

## 6. パッケージ化と配布

TokenHub は built-in と external plugin に同じ package 形を使います。

典型的な package 内容:

- `plugin.yaml`
- 1 つの runtime entrypoint
- 任意の assets
- contract tests

distribution metadata には少なくとも次を含めます。

- repository URL
- homepage URL
- download URL
- checksum
- signature
- license
- compatibility metadata

plugin marketplace の URL は既定では未設定です。有効な HTTP または HTTPS のサイトを設定すると外部リンクを表示します。オンライン JSON インデックスは分離署名と失効フィードを検証するまで探索専用です。運用者はオフラインミラーまたは直接の ZIP URL から package を導入して checksum を確認できます。 TokenHub は package の検証結果とライフサイクル状態を直ちに再評価します。この処理で対応済みの宣言的な貢献を有効化できますが、外部コマンド実行は有効になりません。

ZIP では `plugin.yaml` をアーカイブルート、または 1 階層だけの plugin directory に置けます。検出される manifest は必ず 1 つだけにしてください。symlink は含めないでください。runtime entrypoint の実行権限を保持し、`entry.backend.command` は plugin directory からの相対パスにします。

heartbeat サンプルの場合:

```bash
cd plugin-devkit
go build -o examples/background-heartbeat-go/bin/background-heartbeat-go \
  ./examples/background-heartbeat-go
cd examples/background-heartbeat-go
zip -r ../../../background-heartbeat-go.zip plugin.yaml bin
cd ../../..
shasum -a 256 background-heartbeat-go.zip
```

Admin console の **Plugin Management > Browse Plugins > Manual Install** で ZIP を upload するか、HTTPS `download_url` と小文字の SHA-256 checksum を指定します。install 成功後、TokenHub はパッケージを再評価します。宣言的な画面貢献は有効化できますが、外部コマンドを持つパッケージは `failed_startup` を記録し、検査可能なままランタイム機能を一切公開しません。実行可能契約のテストには Devkit を使用します。

## 7. バージョンと互換性

versioning は 3 つに分けて考えます。

| Version | 意味 |
| --- | --- |
| Core version | TokenHub 製品の version |
| Plugin API version | plugin protocol と envelope 契約の version |
| Plugin package version | plugin package 自身の version |

互換性ルール:

1. plugin API の変更は major 内で additive にする
2. manifest schema の変更はできるだけ forward compatible にする
3. stage 名は同じ API major 内で安定させる
4. envelope は field を増やせるが、既存の意味は黙って変えない
5. 新しい sensitive permission は再承認が必要
6. 新しい placement や capability は Core で検証する
7. `sim` の互換 alias は、UI テンプレートの rename が終わるまで内部に残してよい

移行の原則は単純です。

- 古い provider ID を維持する
- 古い route を維持する
- 古い resource と quota を維持する
- 新しい契約が整うまで古い admin payload alias を維持する

## 8. テストとリリース

推奨順序:

1. local unit tests
2. manifest parsing tests
3. contract tests
4. package-level tests
5. TokenHub integration tests
6. marketplace / compatibility checks
7. install、ファイル検査、lifecycle failure の検証

家族ごとの重点:

- Provider plugin: route protocol、discovery、credentials projection、response shape、secret redaction
- チェーン注入 plugin: stage 順序、mutation 上限、failure policy、retry / cancel の挙動、permission enforcement
- UI テンプレート plugin: theme selection、layout selection、template rendering、dashboard composition
- バックグラウンドジョブ plugin: schedule、retry ルール、concurrency、result sanitization
- Admin UI 貢献: schema parsing、action binding、payload redaction、任意の admin API を呼ばないこと

## 9. Built-In Compatibility と残りの移行

現在の package boundary は、全 Provider catalog entry を含む built-in module を検査可能な plugin package に割り当て済みです。残りの移行順序は次のとおりです。

1. provider adapter、quota、OAuth、model discovery を focused Provider Integration の下に保つ
2. gateway enhancement を明示的な Request Pipeline hook に分けて保つ
3. recurring work を Automation plugin に保つ
4. admin page、panel、button を declarative に保つ
5. v1 action surface は compatibility bridge としてだけ残す
6. marketplace を拡張して external author を受け入れる

このやり方なら、各段階を独立に出せて、contract tests でも検証できます。

## 10. 簡単な判断ツリー

```text
それは TokenHub を model service や subscription account に接続するか？
  -> Provider plugin

それは user token request から provider response までの path に影響するか？
  -> チェーン注入 plugin

それは admin page、panel、card、外観だけを変えるか？
  -> Admin UI 貢献 または UI テンプレート plugin

それは定期実行か、起動後実行か？
  -> バックグラウンドジョブ plugin

それは運用者トリガーの action を公開するか？
  -> hook か job に移せない間だけ transitional management_action を使う
```

次に自分へ問いかけます。

```text
この plugin を安全にする最小権限セットは何か？
```

答えられないなら、さらに boundary を小さくします。

## 11. 移行チェックリスト

- [x] built-in module を Provider Integration、Request Pipeline、UI Template、Automation package に割り当てる
- [ ] provider 固有の model discovery と quota ロジックを provider plugin に切り出す
- [ ] request-path ロジックを明示的な chain hook に移す
- [ ] Admin UI 貢献を declarative かつ permission-scoped に保つ
- [ ] main plugin manager から古い action execution surface を外す
- [ ] rename が終わるまで `sim` を内部互換 alias として残す
- [ ] 実際の external plugin を独立した marketplace index 経由で公開する
- [ ] release 前にすべての plugin family で contract tests を揃える

## 12. 最後の原則

plugin を作るときは、次を優先します。

1. 小さいこと
2. 安全であること
3. アップグレードしやすいこと
4. Core から分離しやすいこと

ある挙動が plugin に置けるなら、plugin に置く。
Core に残すしかないなら、最後の判断は Core が行い、実装経路は安定させます。

プラグインと署名のダウンロードには HTTPS と公開 IP アドレスが必要です。リダイレクトは元のスキームとホストを維持し、接続時に DNS を解決して検証します。Provider の上流アクセス設定に関係なく、プライベート、ループバック、リンクローカル宛先を拒否します。

`provider_call` Hook がルートの能力として認められるのは、プロジェクト、API Key、Provider、リソース、ルートプロトコル、`provider_call` 操作の完全な scope が現在のリクエストと一致する場合だけです。能力判定と Hook 実行は旧形式の scope metadata を含めて同じ照合規則を使います。無関係な Hook はプラグイン専用ルートを有効にせず、未対応ルートは呼び出し前に除外され、Provider リソースにペナルティを与えません。組み込みアダプターが対応する能力はプラグイン Hook とは独立して利用できます。

能力判定は Chat（ストリーミングを含む）、Embeddings、Responses（ストリーミングとバックグラウンドジョブを含む）、Anthropic、Gemini、画像生成に適用し、対応するプロトコルブリッジも維持します。レスポンスを返す Hook は、ストリーミング呼び出しには `stream_events`、非ストリーミング呼び出しには `provider_response` を宣言する必要があります。両方の宣言で両モードに対応できます。どちらも宣言しない Hook は、利用可能なアダプターまたはレスポンス Hook があれば実行に参加できますが、Provider 能力を成立させません。反対の出力モードのみを宣言する Hook は、能力判定と実行の両方でスキップします。

チャネル索引には、このサーバーのコアバージョン範囲や必要機能と互換性のない過去・将来のリリースも保持できます。互換性の構文エラーは索引を拒否します。現在のプラットフォーム向け artifact があり、互換性を満たす承認済みの最高 SemVer を優先します。厳密に新しい SemVer のみ更新として表示します。未導入の市場プラグインも概要を表示でき、掲載カテゴリに従って分類します。

ストリーミングの `provider_call` Hook は `stream_events` に `{event, data}` の配列を返し、`usage` も返せます。各イベントを出力する前に `stream_transform`、`response_post`、`guardrail_post` を実行します。拒否されたイベントは出力せずストリームを停止します。`stream_transform` はイベントと監査出力を扱い、完全な `provider_response` や最終 `usage` の書き込み宣言は検証で拒否します。Provider 使用量は `provider_call`、完了時の補正は使用量帰属段階を使用します。

ルート一覧段階（`route_candidates`、`route_rank`）はエンドポイント、プロジェクト、API Key、操作の scope をサポートします。ルート未選択のため Provider・リソース scope は拒否し、ハンドラーは候補全体を確認できます。制約のある不明な次元は一致しません。ジョブの schedule は `@startup`、正の Go duration、`*/N * * * *` の間隔をサポートし、未対応構文やオーバーフローを拒否します。独立 DevKit の Manifest 型は本番 schema から生成し、本番 package fixture で互換性を検証します。
