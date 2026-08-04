# Gemini CLI から Codex サブスクリプション GPT を使用する

TokenHub は Gemini ネイティブの `v1beta` 互換 API を公開します。公式 Gemini CLI は TokenHub に直接接続でき、TokenHub が OpenAI Codex Subscription アカウントへルーティングします。CCswitch などのローカルプロトコルプロキシは不要です。

## 前提条件

- TokenHub に正常な OpenAI Codex Subscription アカウントが登録されていること。
- `gpt-5.5` などの GPT モデルが有効で、その Provider にルーティングされていること。
- TokenHub の Project Key が対象モデルを許可していること。
- Gemini CLI が `GOOGLE_GEMINI_BASE_URL` をサポートしていること。

`localhost`、`127.0.0.1`、`[::1]` 以外では HTTPS を使用してください。Base URL に `/v1beta` を付けないでください。

## 既存設定を変更せずに起動する

次の環境変数はこのコマンドだけに適用され、`~/.gemini/settings.json` を変更しません。

```bash
export TOKENHUB_GEMINI_KEY='TokenHub の Project Key'

GEMINI_API_KEY="$TOKENHUB_GEMINI_KEY" \
GOOGLE_GEMINI_BASE_URL='https://tokenhub.example.com' \
GEMINI_MODEL='gpt-5.5' \
gemini -m gpt-5.5

unset TOKENHUB_GEMINI_KEY
```

ローカル TokenHub の例：

```bash
GEMINI_API_KEY="$TOKENHUB_GEMINI_KEY" \
GOOGLE_GEMINI_BASE_URL='http://127.0.0.1:8080' \
GEMINI_MODEL='gpt-5.5' \
gemini -m gpt-5.5
```

Gemini CLI は `GEMINI_API_KEY` を `x-goog-api-key` で送信します。OpenAI OAuth Access Token ではなく、TokenHub の Project Key を使用してください。

## 1 つのプロジェクトだけに保存する

対象プロジェクトに `.gemini/.env` を作成します。

```dotenv
GEMINI_API_KEY=TokenHub の Project Key
GOOGLE_GEMINI_BASE_URL=https://tokenhub.example.com
GEMINI_MODEL=gpt-5.5
```

`.gemini/.env` をそのプロジェクトの `.gitignore` に追加してください。ユーザー単位の Gemini 設定は変更されません。

## 対応範囲

- `GET /v1beta/models` と `GET /v1beta/models/{model}`
- `generateContent`、SSE `streamGenerateContent`、`countTokens`
- テキスト、インライン画像、クライアントツール、関数結果、複数ターンのツール呼び出し
- Gemini `thoughtSignature` による Codex reasoning continuation の保持
- 1 つの Gemini セッション内での Codex サブスクリプションアカウント親和性

Gemini サーバー側の `googleSearch`、`codeExecution`、`cachedContent` は未対応です。Gemini CLI がローカルで実行するファイル読み取り、Shell、編集などのツールは使用できます。

`GOOGLE_GEMINI_BASE_URL` の公式説明は [Gemini CLI configuration](https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/configuration.md) を参照してください。
