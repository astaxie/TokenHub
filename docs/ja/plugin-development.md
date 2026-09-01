# TokenHub プラグインアーキテクチャと開発ガイド

Language: [English](../plugin-development.md) | [简体中文](../zh-CN/plugin-development.md) | 日本語

このガイドは、現在の TokenHub のプラグイン方針と、その上でどう開発するかを説明します。プラグイン作者、プラットフォームエンジニア、運用担当者向けです。

TokenHub は core を小さく保ちます。

- core は認証、ルーティング、課金、監査、互換性、アップグレード安全性を担当する
- 変化の速い部分は plugin に任せる
- built-in plugin と external plugin は同じ契約を使う
- UI テンプレート、Provider、チェーン注入、バックグラウンドジョブ、Admin UI 貢献はすべて明示的なメタデータから入る

## 1. プラグイン家族

TokenHub はプラグインをいくつかの明確な家族に分けます。

| 家族 | 担当するもの | 例 |
| --- | --- | --- |
| UI テンプレート | シェル全体の見た目、レイアウト、テンプレートパッケージ | shell theme、page template、dashboard composition |
| Provider | 上流モデル接続、認証、探索、quota | Codex、Kimi、Gemini、Anthropic、OpenAI-compatible Provider |
| チェーン注入 | ユーザー要求から upstream 応答までの経路 | privacy control、routing、cache、context optimization、trace export |
| バックグラウンドジョブ | 定期実行または運用者トリガーの作業 | quota refresh、sync、cleanup、reporting |

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
8. TokenHub にインストールして確認する

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

marketplace repository にはローカル harness があります。

```bash
go test ./...
go run ./cmd/tokenhub-plugin-test provider --package ./samples/provider-kimi-go
go run ./cmd/tokenhub-plugin-test hook --package ./samples/hook-trace-go
go run ./cmd/tokenhub-plugin-test background --package ./samples/background-heartbeat-go
```

`--package` は自分の plugin ディレクトリに置き換えてください。

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

UI テンプレート plugin は見た目と layout を担当します。

典型的な貢献:

- theme tokens
- shell layout presets
- navigation composition
- dashboard composition
- page templates

UI テンプレート plugin は `presentation` のみに影響すべきです。

バックエンド挙動も必要なら、それはもはや UI テンプレート plugin だけではありません。

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

plugin marketplace の URL は既定で `https://plugins.betokenhub.com` です。運用者は marketplace あるいは直接の ZIP URL から package を導入し、checksum を確認し、backend を再起動して有効化します。

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
7. install と restart の検証

家族ごとの重点:

- Provider plugin: route protocol、discovery、credentials projection、response shape、secret redaction
- チェーン注入 plugin: stage 順序、mutation 上限、failure policy、retry / cancel の挙動、permission enforcement
- UI テンプレート plugin: theme selection、layout selection、template rendering、dashboard composition
- バックグラウンドジョブ plugin: schedule、retry ルール、concurrency、result sanitization
- Admin UI 貢献: schema parsing、action binding、payload redaction、任意の admin API を呼ばないこと

## 9. 現在の built-in からの移行

実際の移行順序は次のとおりです。

1. 現在の built-in descriptor と registry を plugin 視点にそろえる
2. provider adapter、quota、OAuth、model discovery を provider plugin の下へ移す
3. gateway の拡張を明示的な chain hook に分解する
4. 定期処理を background plugin にする
5. admin page、panel、button を declarative contribution にする
6. 旧 action 面は、request path を切り出すまでの互換 bridge としてだけ残す
7. marketplace を広げて外部作者を受け入れる

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

- [ ] 現在の built-in module を Provider、チェーン注入、UI テンプレート、バックグラウンドジョブ package に割り当てる
- [ ] provider 固有の model discovery と quota ロジックを provider plugin に切り出す
- [ ] request-path ロジックを明示的な chain hook に移す
- [ ] Admin UI 貢献を declarative かつ permission-scoped に保つ
- [ ] main plugin manager から古い action execution surface を外す
- [ ] rename が終わるまで `sim` を内部互換 alias として残す
- [ ] external plugin の sample を marketplace repo で公開する
- [ ] release 前にすべての plugin family で contract tests を揃える

## 12. 最後の原則

plugin を作るときは、次を優先します。

1. 小さいこと
2. 安全であること
3. アップグレードしやすいこと
4. Core から分離しやすいこと

ある挙動が plugin に置けるなら、plugin に置く。
Core に残すしかないなら、最後の判断は Core が行い、実装経路は安定させます。
