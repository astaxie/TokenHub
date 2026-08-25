# TokenHub から Super Grok サブスクリプションを使う

Language: [English](../super-grok-subscription.md) | [简体中文](../zh-CN/super-grok-subscription.md) | 日本語

TokenHub は Super Grok / Grok CLI アカウントをサブスクリプション Provider として接続できます。呼び出し側は TokenHub のプロジェクト Key と OpenAI 互換 `/v1` を使い続けます。CCswitch や CLIProxyAPI は不要です。

この経路は非公開の Grok CLI chat-proxy を使います。組織が所有するアカウントだけを使い、転売用の中継ではなく内部機能として扱ってください。

## 前提

- xAI のデバイスコードログインを完了できる Super Grok（または Grok CLI）アカウント。
- Provider とルートを作成できる管理者。
- `auth.x.ai` と `cli-chat-proxy.grok.com` への outbound HTTPS。

## アカウント接続

1. **Provider チャネル** を開き、Provider を作成します。
2. **アカウントプール** を選び、**Super Grok** チャネルを選択します。
3. チャット Base URL `https://cli-chat-proxy.grok.com/v1` を確認します。
4. 認可を開始します。TokenHub はユーザーコードを表示し、xAI のデバイスページを開きます。
5. 承認後、TokenHub は refresh token を暗号化して保存し、access token を自動更新できます。
6. `grok-4.5`、`grok-4.6`、`grok-composer-2.5-fast`、`grok-build-0.1` などのサブスクリプションモデルを取り込み、ルートを作成します。

この Provider に xAI API Key を貼り付けないでください。公式 `api.x.ai` の Key は `openai_compatible` チャネルに置きます。

## クライアントから TokenHub を呼び出す

ルート作成後、呼び出し側は **Key 管理** の**プロジェクト API Key** を使います。Provider に保存した Super Grok OAuth Token は使いません。コンソールのログイン Token では `/v1` を呼べません。

**Key 管理** では Codex CLI の `config.toml` と `.env` テンプレートをダウンロードできます。Grok CLI 用の独立 home はダウンロードされません。Grok CLI は次のとおり手で設定します。

### OpenAI 互換クライアント

ゲートウェイのルートは `/v1` で終わる必要があります。プロジェクト Key を Bearer Token として使います。ホストはデプロイメントの実際の TokenHub URL に置き換えてください。

```bash
export TOKENHUB_BASE_URL="http://localhost:8080/v1"
read -r -s "TOKENHUB_API_KEY?TokenHub project API key: "
export TOKENHUB_API_KEY
echo

curl --fail-with-body \
  --url "${TOKENHUB_BASE_URL%/}/models" \
  --header "Authorization: Bearer ${TOKENHUB_API_KEY}"

curl --fail-with-body \
  --url "${TOKENHUB_BASE_URL%/}/chat/completions" \
  --header "Authorization: Bearer ${TOKENHUB_API_KEY}" \
  --header "Content-Type: application/json" \
  --data '{"model":"grok-4.5","messages":[{"role":"user","content":"Reply with pong."}]}'
```

`model` はその Key の `GET /v1/models` に含まれている必要があります。`POST /v1/responses` も同じ Base URL と Bearer Token を使います。

### Grok CLI（独立した home）

TokenHub の設定を `~/.grok` に書き込まないでください。そのディレクトリは公式 Grok ログインと Super Grok セッションです。`GROK_CONFIG` / `GROK_CONFIG_PATH` の overlay では推論 Base URL を変更できません。

別の home を使い、通常の `grok` コマンドは xAI のままにします。

```bash
GROK_HOME="${HOME}/.grok-tokenhub"
mkdir -p "$GROK_HOME"
chmod 700 "$GROK_HOME"
```

`$GROK_HOME/config.toml` を作成します。

```toml
[models]
default = "grok-4.5"

[endpoints]
models_base_url = "http://localhost:8080/v1"

[model.grok-4.5]
model = "grok-4.5"
base_url = "http://localhost:8080/v1"
name = "TokenHub Super Grok"
api_backend = "chat_completions"
env_key = "TOKENHUB_API_KEY"
```

`http://localhost:8080/v1` は上と同じゲートウェイルートに、`grok-4.5` はその Key が呼べるモデルに置き換えます。同じシェルで次を実行します。

```bash
export GROK_HOME="${HOME}/.grok-tokenhub"
export GROK_MODELS_BASE_URL="${TOKENHUB_BASE_URL}"
export XAI_API_KEY="${TOKENHUB_API_KEY}"
chmod 600 "$GROK_HOME/config.toml"

grok inspect
grok --model grok-4.5
grok -p "Reply with pong." --model grok-4.5 --yolo --max-turns 1
```

`GROK_MODELS_BASE_URL` を設定すると、Grok CLI は `XAI_API_KEY` で `Authorization: Bearer` を送り、`grok login` は使いません。`config.toml` の `env_key` を解決するため `TOKENHUB_API_KEY` も設定したままにします。`grok inspect` は独立した `GROK_HOME` を表示し、`~/.grok` であってはなりません。

`GROK_HOME` を解除する（または新しいシェルを開く）と公式 Grok プロファイルに戻ります。これらのファイルで `~/.grok/config.toml` や `~/.grok/auth.json` を上書きしないでください。

プロジェクト Key をリポジトリにコミットしないでください。`read -s`、またはリポジトリ外の `chmod 600` 環境ファイルを使います。

## 対応インターフェース

- `POST /v1/chat/completions`（ストリーミング含む）
- `POST /v1/responses`（ストリーミング含む）
- Playground のチャットも同じ Responses ブリッジを使用

このリリースでは、画像/動画、`/v1/responses/compact`、WebSocket、Gemini ネイティブ `v1beta`、Anthropic Messages、サブスクリプション枠のダッシュボードは対象外です。

## 運用上の注意

- TokenHub は chat-proxy へ現行 Grok CLI のクライアントバージョンを付けます。古いバージョンは xAI に拒否されることがあります。
- Composer モデルは `prompt_cache_key` / 会話ヘッダーでセッションアフィニティを維持します。
- 定期更新のリードタイムは Codex と同じ 5 分です。xAI が refresh token を無効化すると、再認可が必要な状態になります。

アダプター能力は [アーキテクチャ](architecture.md) の `xai_grok` を参照してください。
