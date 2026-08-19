# 利用者 LLM API ガイド

Language: [English](../user-guide.md) | [简体中文](../zh-CN/user-guide.md) | 日本語

このガイドは、TokenHub 経由で承認済み大規模言語モデルを呼び出す社員とアプリケーション開発者向けです。

## 必要なもの

| 項目 | 用途 |
| --- | --- |
| Base URL | OpenAI 互換 root は `http://localhost:8080/v1`、Claude Code Host URL は `http://localhost:8080` |
| Project API Key | `Authorization: Bearer YOUR_TOKENHUB_API_KEY` で送信 |
| Model ID | `GET /v1/models` で返り、`model` に指定 |
| request_id | 失敗時に Request Logs で調査するために利用 |

コンソールログイントークンではモデル API を呼び出せません。**Key Management** の Project API Key を利用します。

## 呼び出し順序

1. **Key Management** を開き、API Key を作成またはコピーします。新しい Key は一度だけ表示されます。
2. TokenHub は個人 Key を割り当て済み Project に自動帰属し、未割り当ての場合はプラットフォームのデフォルト Project に帰属します。
3. `GET /v1/models` で、その Key から利用できるモデル一覧を確認します。
4. モデル ID を選び、`POST /v1/chat/completions`、`POST /v1/messages`、`POST /v1/responses`、`POST /v1/embeddings` を呼び出します。
5. **Usage Analytics** と **Request Logs** でリクエスト、Token、コスト、エラーを確認します。

## Playground でモデルをテストする

コンソールの **Model Playground** を開くと、API スクリプトを作成せずに利用可能な chat model をテストできます。各レスポンスには streaming / buffered mode、計測可能な場合の TTFT、出力スループット、総所要時間、コンテキスト全体の input tokens、output tokens、推定コスト、ローカル完了時刻、Request ID が表示されます。レスポンスを展開すると、実レスポンスの詳細を確認できます。Provider と route の内部情報は routing-read 権限を持つロールだけに表示されます。

画像アップロードは、選択したモデルの `input_modalities` に `image` が含まれる場合にのみ利用できます。マルチモーダルモデルでは、モデルディレクトリでこのフィールドを設定してください。Playground は JPEG、PNG、WebP 画像に対応し、1 メッセージあたり最大 4 枚、1 枚あたり最大 5 MiB、現在の会話全体で最大 12 MiB までアップロードできます。エクスポートしたセッションには画像名、メディアタイプ、サイズがコンテキスト情報として残りますが、画像データは含まれません。

セッションは一時的で、**Export Playground** を選ばない限り現在のページだけに保持されます。**Stop** は部分出力を残します。**Rerun** はそのターンから別候補を作り、後続ターンを削除します。モデル変更はデフォルトで新しいセッションになり、既存コンテキストを保持するには明示的な選択が必要です。上流がストリーミング非対応の場合、画面は buffered mode を使い、TTFT を「該当なし」と表示します。

## モデル一覧

```bash
curl --request GET \
  --url "http://localhost:8080/v1/models" \
  --header "Authorization: Bearer YOUR_TOKENHUB_API_KEY" \
  --header "Content-Type: application/json"
```

主なモデルフィールド:

| フィールド | 意味 |
| --- | --- |
| `id` | API 呼び出しで使うモデル ID |
| `object` | オブジェクト種別。通常は `model` |
| `created` | モデル作成 Unix timestamp |
| `input_token_price_per_m` | JieKou 互換の 100 万 input tokens あたり整数価格 |
| `output_token_price_per_m` | JieKou 互換の 100 万 output tokens あたり整数価格 |
| `title` | モデルタイトル |
| `display_name` | Anthropic 互換のモデル表示名 |
| `description` | モデル説明 |
| `context_size` | 最大コンテキスト長 |
| `created_at` | Anthropic 互換の RFC 3339 作成日時 |
| `max_input_tokens` | Anthropic 互換の最大入力コンテキスト |
| `max_tokens` | 設定済みの最大出力 tokens。未設定時は `0` |

## 指定モデル情報

```bash
curl --request GET \
  --url "http://localhost:8080/v1/models/gpt-4.1-mini" \
  --header "Authorization: Bearer YOUR_TOKENHUB_API_KEY" \
  --header "Content-Type: application/json"
```

この API は単一モデルオブジェクトを返し、フィールドは `GET /v1/models` のモデル項目と同じです。

## Chat Completions

```bash
curl --request POST \
  --url "http://localhost:8080/v1/chat/completions" \
  --header "Authorization: Bearer YOUR_TOKENHUB_API_KEY" \
  --header "Content-Type: application/json" \
  --data '{
    "model": "gpt-4.1-mini",
    "messages": [
      {"role": "system", "content": "You are an internal enterprise AI assistant."},
      {"role": "user", "content": "Summarize today'\''s support tickets."}
    ],
    "temperature": 0.7,
    "stream": false
  }'
```

主なリクエストフィールド:

| フィールド | 必須 | 説明 |
| --- | --- | --- |
| `model` | はい | `GET /v1/models` の ID |
| `messages` | はい | `system`、`user`、`assistant` のメッセージ配列 |
| `max_tokens` | いいえ | 最大生成 tokens |
| `temperature` | いいえ | サンプリング温度 |
| `reasoning_effort` | いいえ | 対応するモデルとルートで使用する推論強度 |
| `stream` | いいえ | `true` の場合 SSE stream |
| `tools` | いいえ | 上流モデル対応時の関数ツール |
| `response_format` | いいえ | 上流モデル対応時の JSON object または JSON schema |

### 推論強度

Chat Completions は OpenAI 互換の `reasoning_effort` フィールドを受け付けます。

```json
{
  "model": "REASONING_MODEL_ID",
  "messages": [{"role": "user", "content": "Analyze the trade-offs."}],
  "reasoning_effort": "high"
}
```

Responses は OpenAI 互換のネスト形式を受け付けます。

```json
{
  "model": "REASONING_MODEL_ID",
  "input": "Analyze the trade-offs.",
  "reasoning": {"effort": "high"}
}
```

TokenHub は推論強度をベストエフォートのヒントとして扱い、ルートの順序を変更しません。OpenAI 互換 Provider には値をそのまま渡します。Anthropic のネイティブルートでは対応する値を `output_config.effort` に変換します。Gemini のネイティブルートでは、モデル固有の対応表に従って Gemini 3 以降の `thinkingLevel`、または Gemini 2.5 の公式 `thinkingBudget` に変換します。対応しない値や空値は省略され、上流モデルのデフォルト動作が使われます。上流 Provider が推論強度フィールドを明示する `400` または `422` のパラメーターエラーを返した場合、TokenHub は同じルートでそのフィールドを除いて一度再試行します。推論強度の拒否ではない `400` / `422` は、他の Provider で再試行せずそのまま返します。上流が不正と判断したリクエストは、どの Provider でも不正だからです。物理的な再試行はそれぞれ Provider Resource RPM に加算され、ルート試行として記録されます。

Responses の推論強度は OpenAI 互換、Anthropic、Gemini の各ルートで利用できます。Azure OpenAI Responses と Responses のストリーミングは未実装で、`501 provider_capability_not_supported` を返します。

## Anthropic および Gemini ルートでのツール呼び出しとマルチモーダル入力

ネイティブの Anthropic または Gemini プロバイダーへルーティングされる Chat Completions リクエストでは、プレーンテキストだけでなくリクエストとレスポンスの全体が変換されます。

| 機能 | Anthropic | Gemini |
| --- | --- | --- |
| `tools` と `tool_choice` | 対応 | 対応 |
| Assistant の `tool_calls` と `role: "tool"` の結果 | 対応 | 対応 |
| `parallel_tool_calls: false` | 対応 | `501 provider_capability_not_supported` |
| 画像コンテンツパート | `http(s)` URL と base64 data URI | base64 data URI のみ |
| ストリーミング | 逐次中継 | 逐次中継 |

ストリーミングは上流のイベントを到着順に中継するため、最初のトークンまでの時間はレスポンス全体ではなくプロバイダーの応答速度を反映します。これらのルートで表現できないコンテンツ種別（音声パートなど）は破棄されず `400 unsupported_content_block` を返します。

Codex Subscription アカウントへルーティングされる Chat Completions は内部で Responses プロトコルを使用し、同等のテキスト、画像、関数ツール、並列ツール、推論継続、ストリーミング機能を提供します。

Codex サブスクリプションの上流は、クライアントのサンプリング、出力トークン上限、停止条件フィールドを受け付けません。TokenHub の互換エンドポイントはこれらのフィールドを受理しますが、サブスクリプション要求からは除外するため、Codex ルートでは `max_tokens`、`max_completion_tokens`、`temperature`、`top_p`、停止条件は強制されません。これらの制御が契約上必要な場合は、標準 API Provider を使用してください。

### 推論の継続

Anthropic と Gemini では、複数ステップのツール呼び出しにおいて、推論ステップに付随する不透明な署名を次のターンでそのまま返す必要があります。OpenAI Chat Completions のスキーマには該当するフィールドがないため、TokenHub は拡張フィールドで返します。

| フィールド | 対応するプロバイダーのデータ |
| --- | --- |
| `message.reasoning_content` | Anthropic の `thinking` テキスト、Gemini の thought パート、Codex の推論サマリー |
| `message.reasoning_signature` | Anthropic の `thinking.signature`、Codex の暗号化推論 |
| `message.reasoning_details` | ツール呼び出し ID に紐づく Codex の暗号化推論 |
| `message.redacted_reasoning_content` | Anthropic の `redacted_thinking.data` |
| `message.tool_calls[].thought_signature` | Gemini の `thoughtSignature` |

次のリクエストの assistant メッセージでこれらのフィールドをそのまま返すと、推論の連続性が保たれます。`reasoning_details` では項目全体と `id` を維持してください。TokenHub は、その ID が同じ assistant メッセージ内のツール呼び出しと一致する場合にのみ受け付けます。これらを無視するクライアントでも動作します。TokenHub はプロバイダーが拒否する署名を送り返すのではなく、推論ブロックを省略します。署名には発行元プロバイダーの識別子が付与され、別のプロバイダーへ送られることはありません。

## Anthropic Messages と Claude Code

TokenHub は Claude Code と Anthropic 互換クライアント向けに `POST /v1/messages` と `POST /v1/messages/count_tokens` を提供します。Project Key は Bearer Token として送信します。

```bash
curl --request POST \
  --url "http://localhost:8080/v1/messages" \
  --header "Authorization: Bearer YOUR_TOKENHUB_API_KEY" \
  --header "anthropic-version: 2023-06-01" \
  --header "Content-Type: application/json" \
  --data '{
    "model": "CLAUDE_COMPATIBLE_MODEL_ID",
    "max_tokens": 2048,
    "messages": [
      {"role": "user", "content": "Understand this repository and summarize its architecture."}
    ]
  }'
```

Anthropic ネイティブルートでは Anthropic content block と beta header を保持します。OpenAI 互換ルートではテキスト、画像、クライアントツール、ツール結果、並列ツール呼び出し、ストリーミング event を変換します。OpenAI 互換 Provider で表現できない Anthropic サーバーツールには `400 unsupported_tool` を返します。

OpenAI 互換ルートでは、Provider と Provider Resource の `options` で Claude の reasoning パラメーターを上流の語彙へ変換できます。`reasoning_effort_map` は `{"minimal":"low","xhigh":"max"}` のような JSON object、`reasoning_effort_values` はカンマ区切りの許可値、`reasoning_effort_unsupported` は `omit`（既定）、`reject`、または明示的に選択した `passthrough` のいずれかです。`reasoning_budget_map` は最大 token 数と任意の `*` fallback を effort 値へ割り当てます（例：`{"2048":"low","8192":"medium","*":"max"}`）。Provider Resource の設定が Provider の設定を上書きします。TokenHub は `thinking.type=disabled` を `none` に変換し、明示的な effort がない `adaptive` では上流の既定値を使い、`enabled` は `budget_tokens` から変換します。明示的な `output_config.effort` は top-level `effort` と budget 由来の値より優先されます。後続の assistant message で上流自身の `reasoning_content` を受け付ける場合に限り、`preserve_reasoning_content=true` を設定してください。OpenAI 互換上流の `reasoning_content` は、正しい順序の Claude `thinking` / `thinking_delta` block と TokenHub の replay signature に変換されます。

OpenAI Codex Subscription アカウントへルーティングされるモデルも、同じ Messages エンドポイントを利用できます。TokenHub が Messages を Responses プロトコルへ直接変換し、結果を Anthropic event に戻すため、Claude Code は CC-Switch などのローカルプロトコルプロキシなしで TokenHub に直接接続できます。Codex が発行した reasoning signature はツール実行ターン間で引き継がれ、同じ Claude Code セッションは同一の正常なサブスクリプションアカウントに固定されます。

Codex ルートの Messages リクエストでは、サブスクリプション上流に対応するフィールドがないため、`max_tokens`、`temperature`、`top_p`、`stop_sequences`、Anthropic の構造化出力フォーマットを強制できません。

`mid-conversation-system-2026-04-07` を有効にした Claude Code リクエストでは、`messages` 内に `system` エントリを含めることができます。TokenHub は Anthropic ネイティブルートではそのエントリを保持し、OpenAI 互換ルートでは順序を維持した system message に変換します。この beta がない場合、`messages` で使用できる role は引き続き `user` と `assistant` のみです。

ローカル Claude Code には `/v1` suffix を付けず、TokenHub Host URL を設定します。

```bash
export ANTHROPIC_BASE_URL="http://localhost:8080"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKENHUB_API_KEY"
export ANTHROPIC_MODEL="CLAUDE_COMPATIBLE_MODEL_ID"

claude
```

`ANTHROPIC_AUTH_TOKEN` は TokenHub Key を `Authorization: Bearer` で送信します。Authorization header がない場合は、`ANTHROPIC_API_KEY` の `x-api-key` も利用できます。Token 見積もりは Key とモデル権限を確認しますが、課金対象の推論レコードは作成しません。

## 永続化バックグラウンド Responses

`POST /v1/responses` に `background: true` を設定すると、Responses リクエストが永続化され、ゲートウェイが管理する安定した Response ID が直ちに返されます。

```bash
curl http://localhost:8080/v1/responses \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.4","input":"Summarize the report","background":true}'
```

`GET /v1/responses/{id}` で状態または最終結果を取得し、`POST /v1/responses/{id}/cancel` でキャンセルを要求します。取得とキャンセルには、送信時と同じ Project、API Key、帰属所有者、および現在のモデルアクセス権が必要です。いずれかが一致しない場合は `404` を返します。再開可能なバックグラウンド SSE は未実装のため、`background: true` と `stream: true` の併用は拒否されます。

公開状態は `queued`、`in_progress`、`completed`、`failed`、`cancelled` です。Worker は上流呼び出しの前後で quota、budget、同時実行制限、routing、guardrail、cache affinity、cost accounting、request log、trace を適用します。キャンセルと完了が競合した場合、永続化される結果は一方だけです。上流で既に発生した使用量は 1 回だけ精算されます。

待機中のジョブはサーバー再起動後も継続します。Admission 前に lease を失ったジョブは安全に再キューイングされます。Admission 後に Worker を失ったジョブは、Provider が既に受信した可能性があるため再送せず、`response_execution_lost` で明示的に失敗します。PostgreSQL の複数レプリカは fencing 付き lease と row lock で取得を調整します。SQLite は再起動復旧に対応しますが、単一バックエンド構成に限定されます。同じ SQLite ファイルを複数のバックエンドで共有しないでください。

リクエスト envelope と結果は `TOKENHUB_SECRET_KEY` で保存時に暗号化されます。認証 header は保存されず、長さを制限した protocol header の allowlist だけが保持されます。バックグラウンドのリクエスト本文とレスポンス本文は、平文の request payload 監査レコードや trace export へ複製されず、route attempt レコードからも上流のエラーテキストが除去されます。暗号化または復号に失敗した場合、平文へフォールバックせず処理を拒否します。終端 payload は `TOKENHUB_RESPONSE_RESULT_TTL_SECONDS` の間保持され、期限後にリクエストと結果の暗号文が消去されます。その後の取得は `404` です。返される ID は TokenHub の取得用 ID であり、上流の `previous_response_id` には変換されません。

metrics を有効にすると、Prometheus に `tokenhub_gateway_response_jobs_queued`、`tokenhub_gateway_response_job_queue_wait_seconds`、`tokenhub_gateway_response_job_execution_seconds`、`tokenhub_gateway_response_jobs_total`、`tokenhub_gateway_response_job_recoveries_total` が公開されます。Worker 数、polling、timeout、lease、retention、queue 上限は[デプロイ](deployment.md#バックエンド環境変数)を参照してください。

## Gemini CLI で Codex サブスクリプション GPT を使用する

Gemini CLI は TokenHub の Gemini ネイティブ `v1beta` API に直接接続し、OpenAI Codex Subscription アカウントへルーティングされた GPT モデルを利用できます。`GEMINI_API_KEY` に TokenHub の Project Key、`GOOGLE_GEMINI_BASE_URL` に `/v1beta` を含まない TokenHub Host を設定し、対象 GPT モデルを選択します。CCswitch は不要です。分離起動、プロジェクト設定、対応エンドポイント、検証方法、制限については [Gemini CLI から Codex サブスクリプション GPT を使用する](gemini-cli-codex-subscription.md) を参照してください。

## Codex サブスクリプション画像生成

`POST /v1/images/generations` は OpenAI 互換の `model`、`prompt`、`quality`、`size`、`n`、`response_format` を受け付けます。公開仮想モデル `model: "codex-gpt-image-2"` と `n: 1` を使用してください。`gpt-image-2` は通常、別の標準 API モデルのままです。限定的な互換処理として、Codex の `originator` または `x-codex-image-turn-id` ヘッダーが付いた生成リクエストは `codex-gpt-image-2` にマッピングされ、`b64_json` が返されます。API キーでは `codex-gpt-image-2` を許可する必要があります。`Prefer: respond-async` を付けると画像ジョブが返り、`GET /v1/image-jobs/{id}` でポーリングできます。

`POST /v1/images/edits` は multipart の `image` または `image[]` で参照画像を受け付けます。`gpt-image-2` は単一の `mask` を OpenAI API に転送できますが、Codex サブスクリプションではマスク編集は利用できません。TokenHub は Codex CLI をインストールまたは起動せず、Codex サブスクリプションの Images エンドポイントを直接呼び出します。プロンプトはデータベースで暗号化され、入力画像と出力画像はサーバーに保持されます。署名付きダウンロード URL の有効期間は 24 時間です。URL の期限後もファイルは残り、ジョブを再取得すると新しい URL が発行されます。選択された Codex アカウントには画像生成権限が必要です。

画像ジョブの既定の実行タイムアウトは 5 分で、`TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS` で変更できます。

管理者は **Provider チャネル** でこの機能を設定します。OpenAI Codex Provider を開き、**モデル** タブで **Codex サブスクリプション画像生成** を選択します。有効なアカウントを選ぶと、TokenHub はクォータ消費の警告を表示し、その実アカウントへ低品質の `gpt-image-2` リクエストを 1 回送信します。空でない有効な画像を受信した場合に限り、アカウントを対応済みとして記録し、Provider ルートを作成または再有効化します。`403` は非対応として記録され、認証情報が期限切れの場合は再認証が必要です。レート制限、タイムアウト、一時的な上流障害では以前の機能判定を上書きせず、ダイアログから再試行できます。このテストは少量のサブスクリプションクォータを消費し、バックグラウンドでは自動実行されません。

このチェック項目は、`codex-gpt-image-2` から OpenAI Codex Provider の上流モデル `gpt-image-2` への有効なルートを冪等に管理します。アップグレード時には、既に対応確認済みの有効なアカウントに対して不足するルートを 1 回だけ補完します。選択を解除すると一致するルートを無効化しますが、アカウントの機能判定は保持します。起動時の補完処理は、明示的に無効化されたルートを再有効化せず、移行済みとして記録された後に削除されたルートも再作成しません。管理者は明示的に再テストして有効化できます。優先度、重み、プロジェクト範囲、指定リソース、リソースグループの詳細設定は引き続きルーティング画面で編集できます。有効なルートがある場合、対応確認済みアカウントが優先され、`403` を返したアカウントは一時的に除外されます。`TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS`（既定 24 時間）の経過後、次の実リクエストで低頻度に再試行できます。初回テストが失敗してルートが作成されなかった場合は、管理者が手動で再試行する必要があります。利用可能なルートとアカウントが存在する場合にのみ、`codex-gpt-image-2` が `GET /v1/models` に表示されます。上記の Codex クライアント互換マッピングを除き、別の `gpt-image-2` モデルは OpenAI API Provider を使用し、Codex サブスクリプション枠を消費しません。

## SDK 設定

```ts
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.TOKENHUB_API_KEY,
  baseURL: "http://localhost:8080/v1",
});
```

## トラブルシューティング

| ステータス | 主な原因 | 対応 |
| --- | --- | --- |
| 401 | API Key の不足、形式不正、無効化、期限切れ | `Authorization` と Key 状態を確認 |
| 403 | Project、Key、モデル権限がリクエストを許可しない | チームリーダーに Project メンバーとモデル権限の確認を依頼 |
| 404/503 | モデルを処理できる健全なルートがない | 管理者にルートと Provider ヘルス確認を依頼 |
| 429 | クォータ、同時実行、Provider リソース制限 | 回復を待つか、増枠を依頼 |
| 500 | 上流 Provider またはルーティングエラー | Request Logs で `request_id` を検索 |

## スクリーンショット

![Gateway documentation](../assets/screenshots/gateway-en.png)
