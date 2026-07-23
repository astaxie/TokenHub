# 利用者 LLM API ガイド

Language: [English](../user-guide.md) | [简体中文](../zh-CN/user-guide.md) | 日本語

このガイドは、TokenHub 経由で承認済み大規模言語モデルを呼び出す社員とアプリケーション開発者向けです。

## 必要なもの

| 項目 | 用途 |
| --- | --- |
| Base URL | OpenAI 互換 endpoint root。例: `http://localhost:8080/v1` |
| Project API Key | `Authorization: Bearer YOUR_TOKENHUB_API_KEY` で送信 |
| Model ID | `GET /v1/models` で返り、`model` に指定 |
| request_id | 失敗時に Request Logs で調査するために利用 |

コンソールログイントークンではモデル API を呼び出せません。**Key Management** の Project API Key を利用します。

## 呼び出し順序

1. **Key Management** を開き、API Key を作成またはコピーします。新しい Key は一度だけ表示されます。
2. TokenHub は個人 Key を割り当て済み Project に自動帰属し、未割り当ての場合はプラットフォームのデフォルト Project に帰属します。
3. `GET /v1/models` で、その Key から利用できるモデル一覧を確認します。
4. モデル ID を選び、`POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/embeddings` を呼び出します。
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
| `description` | モデル説明 |
| `context_size` | 最大コンテキスト長 |

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
