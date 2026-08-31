# TokenHub プラグイン開発ガイド

Language: [English](../plugin-development.md) | [简体中文](../zh-CN/plugin-development.md) | 日本語

このガイドは、現在の TokenHub のプラグインモデルと、その上でプラグインをどう開発するかを説明します。プラグイン作者、プラットフォームエンジニア、運用担当者向けに、概念よりも実践を重視してまとめています。

TokenHub のプラグイン設計は慎重です。

- Core は小さく、監査しやすく、セキュリティ中心に保つ。
- 変更の多い振る舞いは plugin に任せる。
- built-in plugin と external plugin は同じ契約を使う。
- 管理画面、Provider、Gateway hook、バックグラウンドジョブ、テーマ、UI 追加はすべて明示的なプラグインメタデータ経由で扱う。

## 1. プラグインモデル

TokenHub ではプラグインを 3 つの軸で考えます。

| 軸 | 答える質問 | 例 |
| --- | --- | --- |
| Kind | どの種類のプラグインか | `provider`、`admin_ui`、`sim`、`extension` |
| Placement | どこで動くか | `presentation`、`gateway_chain`、`background`、`management_action` |
| Capability | 何を提供するか | `provider_types`、`actions`、`hooks`、`background_jobs`、`theme_tokens` |

1 つの plugin が複数の kind や placement をまたぐことは普通にあります。

例:

- Codex subscription plugin は `provider` であり、`gateway_chain` と `management_action` も提供します。
- Trace exporter は通常 `extension + gateway_chain` です。
- Heartbeat job は通常 `extension + background` です。
- Shell theme は通常 `sim + presentation` です。

基本原則は単純です。

> Core はパイプライン、認証、ルーティング、監査、互換性を担当し、plugin は変化する振る舞いを担当する。

## 2. built-in plugin と external plugin

TokenHub は built-in plugin と external plugin に同じ契約を使います。

| 種別 | 出どころ | 主な用途 |
| --- | --- | --- |
| built-in plugin | TokenHub 本体に同梱 | Core provider adapter、基本管理画面、core hook、標準テーマ |
| external plugin | marketplace や private repo から導入 | サードパーティ Provider、企業向け拡張、パートナー UI 追加 |

違いは主に配布方法と信頼方法であり、能力の形ではありません。

built-in plugin は製品同梱なので通常は既定で信頼されます。external plugin は有効化前に manifest 検証、権限検証、署名/信頼検証を通過する必要があります。

## 3. manifest 形式

各 plugin package は `plugin.yaml` で記述します。

最小構成は次のとおりです。

```yaml
schema_version: 1
id: tokenhub.provider.kimi-go
name: Kimi Subscription Go Provider
version: 1.0.0
description: Reference Go provider plugin for the TokenHub stdio-json-v1 contract kit.
tokenhub:
  plugin_api: v1
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
  repository_url: https://github.com/tokenhub-dev/tokenhub-plugin-marketplace
  homepage_url: https://github.com/tokenhub-dev/tokenhub-plugin-marketplace/tree/main/samples/provider-kimi-go
  license: Apache-2.0
```

### 3.1 重要フィールド

- `schema_version`: manifest スキーマの版。
- `tokenhub.plugin_api`: plugin API の版。現在は `v1`。
- `kinds`: `provider`、`admin_ui`、`sim`、`extension` のいずれか 1 つ以上。
- `placement`: `presentation`、`gateway_chain`、`background`、`management_action` のいずれか 1 つ以上。
- `entry.backend`: 実行エントリーポイント。現在のサンプルは `stdio-json-v1` を使います。
- `capabilities`: 実際の機能宣言。
- `permissions`: 最小権限の宣言。
- `distribution`: repository URL、homepage URL、checksum、signature、license などのメタデータ。

manifest は最初のゲートです。manifest が正しくなければ、それ以外は動かしません。

## 4. 実行時契約

TokenHub の Go SDK は 4 つの標準入口を提供します。

- `ServeProvider`
- `ServeAction`
- `ServeGatewayHook`
- `ServeBackgroundJob`

それぞれ 1 つの契約種別に対応します。

### 4.1 Provider invocation

Provider plugin が受け取るのは次の情報です。

- operation
- provider projection
- resource projection
- provider model
- request payload
- credentials projection

つまり plugin は投影済みデータだけを見て、Core の内部実装には直接触れません。

### 4.2 Action invocation

Action plugin が受け取るのは次の情報です。

- plugin ID
- action ID
- actor
- payload

用途:

- OAuth start / exchange
- probe
- quota refresh
- カスタム管理操作

### 4.3 Gateway hook invocation

Gateway hook plugin が受け取るのは次の情報です。

- request ID
- stage
- envelope
- optional stage data

用途:

- privacy filter
- context optimizer
- route rank
- cache lookup / write
- request / response transform
- trace export

### 4.4 Background job invocation

Background job plugin が受け取るのは次の情報です。

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

## 5. プラグインの作り方

おすすめの順序は次のとおりです。

1. kind と placement を先に決める。
2. 最小限の capability を決める。
3. manifest を書く。
4. runtime handler を実装する。
5. contract tests を追加する。
6. ローカルで実行する。
7. marketplace へ公開する。
8. TokenHub にインストールして確認する。

### 5.1 まず解く問題を明確にする

コードを書く前に次を確認します。

- これは Provider 連携か？
- これは管理画面 UI の追加だけか？
- これは shell / theme の変更か？
- これは gateway-chain 拡張か？
- これはバックグラウンドジョブか？
- これは management action か？

これに答えられないなら、plugin の境界がまだ曖昧です。

### 5.2 まず最小 capability だけを書く

最初から全部は入れません。

例:

- Provider plugin: `provider_types` と `gateway`
- Action plugin: 1 つの action ID
- Hook plugin: 1 つの stage
- Background plugin: 1 つの job
- SIM plugin: 1 つの theme または layout 追加

### 5.3 handler を実装する

handler は短く保ちます。

- invocation を解析する
- plugin の処理を行う
- 構造化結果を返す
- secret を出力しない

### 5.4 contract tests を追加する

すべての plugin 種別で contract test を用意すべきです。

確認項目:

- manifest が解析できるか
- capability が揃っているか
- 入出力の形が正しいか
- secret が stdout に漏れないか
- failure の振る舞いが想定どおりか

### 5.5 ローカル contract kit を実行する

marketplace repository にはローカル harness があります。

```bash
go test ./...
go run ./cmd/tokenhub-plugin-test provider --package ./samples/provider-kimi-go
go run ./cmd/tokenhub-plugin-test action --package ./samples/action-echo-go
go run ./cmd/tokenhub-plugin-test hook --package ./samples/hook-trace-go
go run ./cmd/tokenhub-plugin-test background --package ./samples/background-heartbeat-go
```

`--package` は自分の plugin ディレクトリに置き換えてください。

### 5.6 公開とインストール

公開時には最低でも次を含めます。

- version
- repository URL
- homepage URL
- checksum
- signature
- license
- compatibility metadata

インストール後は次を確認します。

1. manifest が検証を通ること。
2. 権限が最小であること。
3. 再起動後に本当に有効になること。

## 6. plugin 種別ごとの作り方

### 6.1 Provider plugin

Provider plugin は TokenHub をモデルサービスや subscription account に接続します。

通常は次を宣言します。

- `provider_types`
- `provider_resource_types`
- `provider.route_protocols`
- `provider.default_base_url`
- `provider.model_discovery`
- `provider.error_profile`
- `provider.credentials_scope`
- `provider.api_key_required`
- `provider.supports_custom_headers`

主な責務:

- protocol translation
- model discovery
- quota / account sync
- credential refresh
- provider-specific route behavior
- provider-specific UI metadata

subscription 型 provider では、quota refresh と account sync はできるだけ background job か action に分けます。

### 6.2 Admin UI plugin

Admin UI plugin は管理画面の設定・操作領域を提供します。

典型例:

- provider form section
- resource panel
- dashboard card
- route detail panel
- settings panel
- page template

ルール:

- UI は plugin から提供できる。
- ただし実行は必ず Core を通す。
- plugin は RBAC を迂回しない。
- plugin は raw admin credential を直接使わない。

推奨パターンは declarative です。

1. metadata で UI contribution を記述する。
2. action は core-mediated endpoint に通す。
3. データ整形は frontend domain helper に寄せる。
4. React view はできるだけ表示だけにする。

### 6.3 SIM plugin

SIM plugin は見た目とレイアウトを担当します。

典型的な contribution:

- theme tokens
- logo / icon assets
- shell layout preset
- navigation composition
- dashboard composition
- page template

SIM plugin は `presentation` のみに影響させるべきです。

バックエンド挙動も必要なら、それは SIM だけではありません。

### 6.4 Extension plugin

Extension plugin は横断的な能力を提供します。

典型例:

- DLP
- prompt firewall
- semantic cache
- context optimizer
- model router
- billing connector
- notification channel
- approval workflow
- export / import
- trace exporter

Extension plugin は複数の placement で動けます。

- `gateway_chain`: request path のロジック
- `background`: 同期や定期ジョブ
- `management_action`: 運用者トリガーの操作
- `presentation`: 関連 UI

可能な限り `observe_only` または `read_only` を既定にしてください。  
mutation は明示的かつ限定的にします。

## 7. 推奨リポジトリ構成

現在の marketplace repository は「1 つの SDK + 複数の sample package」の形です。

```text
tokenhub-plugin-marketplace/
  go.mod
  sdk/go/tokenhubplugin/
  contract-tests/
    provider/
    gateway-hook/
    management-action/
    background-job/
    protocol/stdio-json-v1/
  samples/
    provider-mock-go/
    provider-kimi-go/
    provider-glm-go/
    action-echo-go/
    hook-trace-go/
    background-heartbeat-go/
  cmd/tokenhub-plugin-test/
```

最小の実用 package には通常次が含まれます。

- `plugin.yaml`
- `main.go`
- 1 つの fixture file
- 1 つの contract test set

命名のおすすめ:

- `provider-xxx-go`
- `action-xxx-go`
- `hook-xxx-go`
- `background-xxx-go`

## 8. バージョンと互換性

TokenHub は現在、バージョンを 3 つに分けています。

| Version | 意味 |
| --- | --- |
| Core Version | TokenHub 製品の版 |
| Plugin API Version | plugin protocol と envelope contract の版 |
| Plugin Package Version | plugin package 自身の版 |

互換性のルール:

1. `plugin_api` は同一 major 内で additive にする。
2. manifest schema はできるだけ forward compatible にする。
3. 同じ API major 内の stage 名は安定させる。
4. envelope field は追加できるが、既存の意味を黙って変えない。
5. 新しい sensitive permission は再承認が必要。
6. 新しい placement や capability は Core の検証を通す。

移行の原則は単純です。

- 旧 provider ID を残す
- 旧 route を残す
- 旧 resource と quota を残す
- 新 contract が整うまで旧 admin payload alias を残す

## 9. セキュリティと信頼

TokenHub は最小権限を前提にします。

plugin は次をしてはいけません。

- core DB へ直接アクセスする
- RBAC を迂回する
- raw admin token を直接使う
- 権限を黙って広げる
- public `/v1` endpoint を再定義する

plugin は次を明示する必要があります。

- 何を読むか
- 何を書くか
- どんな network access が必要か
- どの stage / job / action に結びつくか
- restart が必要かどうか

marketplace distribution には少なくとも次を含めます。

- checksum
- signature
- key ID
- repository URL
- homepage URL
- license
- compatibility verdict
- advisories
- release notes

## 10. テストと公開フロー

おすすめの順序は次のとおりです。

1. ローカル unit test
2. manifest 解析テスト
3. contract test
4. package level test
5. TokenHub integration test
6. marketplace / signature / compatibility 確認
7. install と restart の確認

kind ごとの重点:

- Provider plugin: route protocols、discovery、credentials projection、response shape、secret redaction
- Admin UI plugin: schema 解析、action binding、payload redaction を行い、任意の admin API を直接呼ばないこと
- SIM plugin: theme selection、layout selection、template rendering、dashboard composition
- Extension plugin: stage order、mutation limit、failure policy、retry / cancel、permission enforcement

## 11. built-in から plugin への移行経路

現実的な移行順序は次のとおりです。

1. 現在の built-in descriptor / registry を plugin という見方にそろえる。
2. provider adapter、quota、OAuth、model discovery を plugin contract の下に移す。
3. 管理画面のページ、panel、button を declarative contribution にする。
4. gateway enhancement を明示的な hooks に分割する。
5. 定期ジョブを background plugin にする。
6. marketplace を拡張して third-party author に開く。

この方法なら、各ステップを独立に公開でき、contract test で回帰も防げます。

## 12. 簡単な判断ツリー

開発前に次を確認してください。

```text
モデルや subscription account に接続するか？
  -> Provider plugin

管理画面のページ、panel、外観だけを変えるか？
  -> Admin UI または SIM plugin

ユーザー token から provider response までの経路に影響するか？
  -> gateway_chain placement を持つ Extension plugin

定期ジョブか、起動後のジョブか？
  -> Background plugin

運用者が起動する action か？
  -> Management action capability
```

次にこう問います。

```text
安全にするための最小権限は何か？
```

これに答えられないなら、plugin boundary をもう少し小さくします。

## 13. 最後の原則

plugin を作るときは、常に次を優先してください。

1. より小さく
2. より安全に
3. よりアップグレードしやすく
4. Core からより分離しやすく

ある振る舞いを plugin に置けるなら、できるだけ plugin に置く。  
Core に残す必要があるなら、Core は最終判断だけを行い、実装経路はできるだけ安定させてください。
