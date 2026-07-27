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

TokenHub は推論強度をベストエフォートのヒントとして扱い、ルートの順序を変更しません。OpenAI 互換 Provider には値をそのまま渡します。Anthropic のネイティブルートでは対応する値を `output_config.effort` に変換します。Gemini のネイティブルートでは、モデル固有の対応表に従って Gemini 3 以降の `thinkingLevel`、または Gemini 2.5 の公式 `thinkingBudget` に変換します。対応しない値や空値は省略され、上流モデルのデフォルト動作が使われます。上流 Provider が推論強度フィールドを明示する `400` または `422` のパラメーターエラーを返した場合、TokenHub は同じルートでそのフィールドを除いて一度再試行し、その後は従来のフェイルオーバー処理を適用します。物理的な再試行はそれぞれ Provider Resource RPM に加算され、ルート試行として記録されます。

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

### 推論の継続

Anthropic と Gemini では、複数ステップのツール呼び出しにおいて、推論ステップに付随する不透明な署名を次のターンでそのまま返す必要があります。OpenAI Chat Completions のスキーマには該当するフィールドがないため、TokenHub は拡張フィールドで返します。

| フィールド | 対応するプロバイダーのデータ |
| --- | --- |
| `message.reasoning_content` | Anthropic の `thinking` テキスト、Gemini の thought パート |
| `message.reasoning_signature` | Anthropic の `thinking.signature` |
| `message.redacted_reasoning_content` | Anthropic の `redacted_thinking.data` |
| `message.tool_calls[].thought_signature` | Gemini の `thoughtSignature` |

次のリクエストの assistant メッセージでこれらのフィールドをそのまま返すと、推論の連続性が保たれます。これらを無視するクライアントでも動作します。TokenHub はプロバイダーが拒否する署名を送り返すのではなく、推論ブロックを省略します。署名には発行元プロバイダーの識別子が付与され、別のプロバイダーへ送られることはありません。

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

ローカル Claude Code には `/v1` suffix を付けず、TokenHub Host URL を設定します。

```bash
export ANTHROPIC_BASE_URL="http://localhost:8080"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKENHUB_API_KEY"
export ANTHROPIC_MODEL="CLAUDE_COMPATIBLE_MODEL_ID"

claude
```

`ANTHROPIC_AUTH_TOKEN` は TokenHub Key を `Authorization: Bearer` で送信します。Authorization header がない場合は、`ANTHROPIC_API_KEY` の `x-api-key` も利用できます。Token 見積もりは Key とモデル権限を確認しますが、課金対象の推論レコードは作成しません。

## Codex サブスクリプション画像生成

`POST /v1/images/generations` は OpenAI 互換の `model`、`prompt`、`quality`、`size`、`n`、`response_format` を受け付けます。公開仮想モデル `model: "codex-gpt-image-2"` と `n: 1` を使用してください。`gpt-image-2` は別の標準 API モデルであり、Codex サブスクリプションには決してルーティングされません。`Prefer: respond-async` を付けると画像ジョブが返り、`GET /v1/image-jobs/{id}` でポーリングできます。

`POST /v1/images/edits` は multipart の `image` または `image[]` で参照画像を受け付けます。`gpt-image-2` は単一の `mask` を OpenAI API に転送できますが、Codex サブスクリプションではマスク編集は利用できません。TokenHub は Codex CLI をインストールまたは起動せず、Codex サブスクリプションの Images エンドポイントを直接呼び出します。プロンプトはデータベースで暗号化され、入力画像と出力画像はサーバーに保持されます。署名付きダウンロード URL の有効期間は 24 時間です。URL の期限後もファイルは残り、ジョブを再取得すると新しい URL が発行されます。選択された Codex アカウントには画像生成権限が必要です。

画像ジョブの既定の実行タイムアウトは 5 分で、`TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS` で変更できます。

TokenHub はアカウントの実際の呼び出し結果から画像生成機能を記録します。対応確認済みのアカウントを優先し、`403` を返したアカウントは一時的に除外します。未確認のアカウントは初回利用時の検出対象として残ります。`TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS`（既定 24 時間）の経過後、非対応アカウントはモデル検出とルーティングの対象に戻り、次の実リクエストで低頻度に再試行されます。回復確認のための画像をバックグラウンドで自動生成することはありません。

正常な接続済み Codex アカウントのうち、少なくとも1つが画像生成対応済み、または低頻度の再試行期間に入った場合、`codex-gpt-image-2` が `GET /v1/models` に表示されます。これはサブスクリプション型の仮想モデルであり、通常の Provider モデルルートは不要です。別の `gpt-image-2` モデルは OpenAI API Provider を使用し、Codex サブスクリプション枠を消費しません。

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
