# モデルカタログの更新

Language: [English](../model-catalog-updates.md) | [简体中文](../zh-CN/model-catalog-updates.md) | 日本語

2026-10-10 のカタログ確認では、現在の上流モデルと、アクセス条件、提供終了、呼び出し互換性、料金の適用範囲を記録しました。カタログへの掲載は候補の発見を支援するものであり、呼び出し対応、アカウント権限、稼働状況、本番デプロイを保証しません。

## 対象範囲

標準モデルテンプレート、公式 Provider カタログ、生成される組み込み Provider プラグインカタログを更新します。OpenAI GPT-6 Sol/Luna と新しいメディアモデル、Claude 5.5 と Fable 5.1、Gemini 3.8 と Embedding 2、Qwen 3.8 とテキスト検索・画像・リアルタイムモデル、DeepSeek V4.1 Flash、Grok 4.7 と Imagine、MiMo V2.6、Doubao Seed 2.1、Mistral Large 4、Cohere Embed 5/Rerank 4、Voyage Rerank 3、および確認した Kimi、GLM、MiniMax、StepFun モデルが対象です。同じモデルが地域別やサブスクリプション別の Provider に載るため、Provider エントリー数と一意のテンプレート数は異なります。

標準候補テンプレートには OpenAI `text-embedding-3-small` と `text-embedding-3-large` も追加します。Amazon Nova 2 Sonic と Nova Multimodal Embeddings はカタログ掲載のみ（`call_support=unsupported`）で、Bedrock 固有の推論と課金契約は別途検証が必要です。

旧モデルの非推奨、提供終了、リダイレクトの根拠も保持します。モデル名の変更、ルート移行、保存済みコストの変更、本番リリースは自動実行しません。

新たに掲載された画像・音声・動画・ライブセッションのうち、`call_support=unsupported` のモデルはカタログ掲載のみです。この更新ではメディアやリアルタイムの新しいプロトコルアダプターを追加しません。既存のメディア連携はそれぞれの対応範囲に従います。[メディア API](media-apis.md)を参照してください。埋め込みと再ランキングの対応は、[テキスト埋め込み](embeddings.md)と[テキスト再ランキング](rerank.md)に記載された検証済みのテキスト契約に限られます。

## 管理画面での表示と動作

Provider の選択、モデル作成、Provider のモデル在庫で、プレビュー、サブスクリプションや追加権限、終了日時、代替 ID、リダイレクト、未対応の操作、確認が必要なコストを表示します。日時は選択した UI 言語とローカルタイムゾーンで表示します。

- `active` は上流の提供状態であり、個別アカウントの利用権限を保証しません。
- `preview` は上流のプレビュー状態を保持します。`restricted` またはアクセス条件は、必要なプラン、承認、特別な権限を示します。
- `deprecated` は移行計画が必要な状態です。有効な `shutdown_at` に達すると、新規ルートの公開を制限します。
- `retired` は、その公式モデルへの新しい有効ルートの公開を拒否します。
- `redirected` は、上流が旧名を受け付けて別モデルで処理する状態です。利用不能とは自動判定しません。
- `call_support=unsupported` は、カタログに表示しつつ、新しい有効ルートの公開を拒否します。

ポリシーは Provider の `catalog_id` と正確な上流モデル ID で照合します。公式 API の終了情報を、同名の第三者ホスティングや自前ホスティングへ一律適用しません。公開済みルートは自動で無効化・移行されません。既存ルートの対象と状態を変えない編集は保存できますが、対象の変更や再有効化では公開チェックを再実行します。従来の実行時の機能チェックは引き続き適用されます。

## 公開前に上流コストを確認する

カタログの価格は参考値であり、仕入先の請求書ではありません。保存済み Provider コストの自動更新にも使いません。参考価格が欠落またはゼロでも、無料とは限りません。地域、プラン、コンテキスト段階、時間帯、キャッシュ、画像品質、解像度、秒数、文字数、検索単位などで料金が変わります。

Provider 在庫のモデルが `pricing_status=unverified` で、正の入力・出力コストも明示的な確認も設定されていない場合、新しい有効ルートの公開を拒否します。その Provider のモデル在庫で、実際のアカウントの料金と課金単位を確認し、上流コストを入力して保存してください。確認済みコストの保存により、設定済みと記録されます。実際に無料なら、確認後にゼロを明示入力して保存してください。未確認のカタログのゼロを放置するだけでは確認になりません。検索モデルには従来の Token または検索単位料金と確認の要件も適用されます。

正の価格でも、カタログの参考値にすぎない場合があります。`pricing_status=unverified` と `catalog_price_reference=true` が設定されていると、正の数値だけでは新規ルートのコスト確認を満たしません。確認済みの GPT-6 項目は短コンテキストの参考単価を保持しますが、入力が 272,000 Token を超える場合は別の料金段階になります。適用コストを確認し、明示的に `pricing_status=configured` として保存すると、この公開制限が解除されます。通知情報の適用は既存在庫の設定済みの正のコストを上書きせず、この料金フラグもコピーしません。

現在の未確認カタログ項目と正確に一致し、過去の在庫の入力・出力コストがともにゼロで、料金状態や明示確認もない場合は、一覧表示と新規ルート確認時に未確認の通知を適用します。この情報は永続化せず、既存ルートの課金を変更しません。保存済みの正のコストと明示的に設定したコストは保持します。正確なカタログ一致がない在庫は遡及チェックの対象外です。テナント料金と上流コストは分けて管理してください。CNY から USD への自動換算、ピーク/オフピークや長コンテキスト料金の平坦化、従量料金のサブスクリプションへの転用、画像・秒・文字料金の Token 料金への変換は行いません。構造化された料金メモは上流条件の記録であり、新しい課金式を実装しません。公開前に必要な課金単位への対応も確認してください。

Token コストの全項目フォームを保存すると、表示された入力・出力・キャッシュ読み取りの Token 単価を、ゼロを含めて明示確認したことを `pricing_status=configured` として保存します。ルート公開検証と上流課金スナップショットは同じ確認状態を使用します。キャッシュ書き込みは個別の設定フラグと継承規則を維持し、ネイティブ課金単位には別途価格が必要です。未確認の過去のゼロ値は不明のままとし、確認前のリクエストスナップショットを再計算しません。

検索モデルの簡易エディターは従来どおり入力またはネイティブ単位の価格のみを確認し、非表示の出力・キャッシュ読み取り単価は確認済みとみなしません。

## 保守者向け metadata の規約

カタログモデルの `metadata` は文字列値として渡されます。構造化された根拠は JSON 文字列に変換してください。コードが明示的に利用する項目以外は説明情報です。

| フィールド | 意味と保守ルール |
| --- | --- |
| `upstream_source`, `verified_at` | 一次資料の URL と確認日（`YYYY-MM-DD`）。ID とパラメーターを定義する提供元を記録します。 |
| `catalog_reviewed_at` | 確認済みの印。後の公開カタログが古い場合でも、この正確なモデル項目を保持します。上流が変わったら再確認します。 |
| `availability`, `access_requirement` | `active`、`preview`、`restricted` と、必要なプランや承認。従量 API の提供からサブスクリプション対応を推測しません。 |
| `lifecycle_status`, `lifecycle_source` | `active`、`deprecated`、`retired`、`redirected`。該当 Provider の公式告知を根拠にします。 |
| `shutdown_at`, `replacement_model` | オフセット付き RFC 3339 の終了チェック時刻と正確な代替 ID。日付しか公開されていない場合は `shutdown_time_note` に不確実性を残し、公式の時刻を捏造しません。 |
| `redirect_at`, `replacement_note` | 上流のリダイレクト予定と条件。リダイレクト日を呼び出し失敗と同一視しません。 |
| `call_support`, `support_note`, `call_support_scope` | 掲載のみの操作には `unsupported` を使用し、不足する契約や `text-only` などの範囲を説明します。省略はアカウント・結合テスト済みを意味しません。 |
| `pricing_status`, `pricing_source`, `pricing_note` | 使用可能な参考価格は `reference`、管理者の設定が必要なら `unverified`、明示確認後は `configured`。出典と条件を保持します。 |
| `catalog_price_reference` | `true` は入力済み価格が参考値であることを示します。`pricing_status=unverified` と併用すると、入力・出力価格が正でも新しい有効ルートの公開前に明示的なコスト確認が必要です。`configured` として保存すると、この制限が解除されます。 |
| `original_currency`, `original_unit`, `original_prices` | 元の通貨、課金単位、構造化価格。暗黙の為替換算には使いません。 |
| `pricing_tiers`, `pricing_schedule`, `reference_token_prices` | 条件付き・キャンペーン価格。条件と期間を保持し、最安値を一律のコストにしません。 |
| `billing_unit` と単位別価格フィールド | Token、検索単位、画像、秒、文字を区別します。検索単位料金は百万 Token 当たりの料金ではありません。 |
| `max_input_tokens`, `max_output_tokens`, 推論・埋め込み項目 | 入力、出力、コンテキスト、推論予算、次元、ベクトル空間 ID を区別します。未確認の上限は省略して理由を記録します。 |

カタログ更新は確認済みモデルを保持し、未確認モデルと新しい上流 ID の更新を許可します。通知情報の適用はライフサイクルと対応範囲を更新し、管理者が保存したコストやカスタムメタデータを上書きしません。

## 確認と更新の手順

1. 3 層のカタログを調べ、正確な Provider ID、上流モデル ID、地域、プランを照合します。無関係なローカル変更を保持します。
2. 公式モデルカード、API 契約、料金表、終了告知を読みます。発表、プレビュー、ホスト API、ダウンロード可能な重みを区別します。上限の矛盾は現在の API/モデルページで再確認し、残る不確実性を記録します。
3. 入出力モダリティ、エンドポイント、推論制御、ツール履歴、コンテキスト・出力上限、課金単位を確認します。未対応操作を明示し、新しいアダプターがあるとは主張しません。埋め込みモデル変更時はインデックス再構築が必要な場合があります。同じ次元だけでは互換性を証明できません。
4. 標準テンプレートと適切な公式 Provider 項目を更新し、出典と確認 metadata を保持します。`node tools/generate-builtin-provider-plugins.mjs` で組み込みプラグインを再生成します。公式の終了情報や価格を集約サービスへ自動転用しません。
5. 変更に関係するカタログ、公開ポリシー、プロトコル契約、翻訳のチェックを実行します。ゼロコストの明示保存、未対応操作、終了モデルを含め、新規と既存のルートを別々に確認します。
6. 顧客トラフィックを有効にする前に、実際のアカウントで地域、権限、パラメーター、ストリーミング・ツール利用、計量、料金を確認します。ローカルテストや PR は、アカウント確認や本番稼働を意味しません。ルート移行は別途計画します。

## 一次資料

以下は 2026-10-10 の確認に使用した公式情報の入口です。モデル別の詳細な出典は metadata に保持します。後の更新時には再確認してください。

| 提供元 | 公式資料 |
| --- | --- |
| OpenAI | [API changelog](https://developers.openai.com/api/docs/changelog) |
| Amazon Nova | [Core inference](https://docs.aws.amazon.com/nova/latest/nova2-userguide/core-inference.html) |
| Anthropic | [Release notes](https://platform.claude.com/docs/en/release-notes/overview) |
| Google | [Gemini changelog](https://ai.google.dev/gemini-api/docs/changelog), [deprecations](https://ai.google.dev/gemini-api/docs/deprecations) |
| Qwen / Alibaba Cloud | [Model releases](https://help.aliyun.com/zh/model-studio/newly-released-models), [China pricing](https://help.aliyun.com/zh/model-studio/model-pricing), [international pricing](https://www.alibabacloud.com/help/en/model-studio/model-pricing) |
| DeepSeek | [Models and pricing](https://api-docs.deepseek.com/quick_start/pricing/), [Chat API](https://api-docs.deepseek.com/api/create-chat-completion/) |
| xAI | [Models](https://docs.x.ai/developers/models), [pricing](https://docs.x.ai/developers/pricing), [Image Quality migration](https://docs.x.ai/developers/migration/imagine-image-quality-nov-2) |
| Xiaomi MiMo | [Models](https://mimo.mi.com/docs/en-US/quick-start/model), [pricing](https://mimo.mi.com/docs/pricing) |
| Doubao | [Models](https://docs.volcengine.com/docs/ark/model-list?lang=zh), [retirement notices](https://docs.volcengine.com/docs/ark/model-deprecation-notice?lang=zh) |
| Mistral | [Models and lifecycle](https://docs.mistral.ai/models), [changelog](https://docs.mistral.ai/resources/changelogs) |
| Cohere | [Models](https://docs.cohere.com/docs/models), [pricing](https://cohere.com/pricing) |
| Voyage AI | [Rerank](https://docs.voyageai.com/docs/reranker), [pricing](https://docs.voyageai.com/docs/pricing) |
| MiniMax | [Models](https://platform.minimax.io/docs/guides/models-intro), [M Plan](https://platform.minimax.io/docs/m-plan/intro) |
| Kimi | [Models](https://platform.kimi.ai/docs/models) |
| GLM / Z.AI | [Model documentation](https://docs.z.ai/guides/llm/glm-5.3) |
| StepFun | [Model migration](https://platform.stepfun.ai/docs/en/guides/model-migration) |
