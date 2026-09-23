# Codex を TokenHub に接続する：4 つの設定方法と復旧

Language: [English](../codex-tokenhub-configuration.md) | [简体中文](../zh-CN/codex-tokenhub-configuration.md) | 日本語

> このガイドでは、ローカルの Codex CLI、Codex デスクトップアプリ、および IDE 拡張機能を TokenHub に接続する方法を説明します。分離 Profile、プロセス単位の一時設定、CLI グローバル設定、デスクトップ設定の 4 方式を扱います。
>
> Profile の最短手順のみが必要な場合は、[Profile クイック設定](codex-tokenhub-profile-quick-start.md)を参照してください。

## 1. 設定方法の選択

| 方法 | 適用範囲 | 永続性 | 推奨用途 | 復旧方法 |
| --- | --- | --- | --- | --- |
| 分離 Profile | Profile を指定して開始したセッション | あり | 特定のプロジェクトまたはタスク | `--profile tokenhub` を付けずに起動 |
| プロセス単位の一時設定 | 現在のプロセスまたはターミナル | なし | 初回検証または一時利用 | Codex を終了して環境変数を削除 |
| CLI グローバル設定 | 現在のユーザーのローカル Codex セッション | あり | CLI から常に TokenHub を使用 | `config.toml` を復元 |
| デスクトップ設定 | デスクトップ、CLI、IDE 拡張機能 | あり | アプリを含む継続利用 | `config.toml` を復元して再起動 |

初回接続では、まずプロセス単位の一時設定で検証してから、永続化方法を選択してください。

Codex CLI、デスクトップアプリ、IDE 拡張機能は `~/.codex/config.toml` を共有します。信頼済みプロジェクトの `.codex/config.toml` では、`model_provider`、`model_providers`、`openai_base_url` を上書きできません。プロバイダーを分離する場合は Profile を使用してください。

TokenHub コンソールのログイントークンとプロジェクト API Key は異なります。API Key をソース管理に登録したり、シェル履歴に残したりしないでください。通常は `env_key` を使用します。`experimental_bearer_token` は管理された開発環境でのみ使用でき、Key は平文で保存されます。

スクリーンショットには実際の設定またはリクエストのみを使用してください。API Key、Authorization ヘッダー、ログイン／OAuth トークン、非公開ホスト、ユーザー名、パス、プロジェクト ID、アカウント ID、セッション ID、リクエスト IDは、すべてマスキングしてください。

---

## 2. 接続前の準備

### 2.1 必要な情報

| 項目 | 取得元 | 要件 |
| --- | --- | --- |
| TokenHub Base URL | デプロイ情報 | `/v1` で終わる URL |
| TokenHub プロジェクト API Key | TokenHub の **Key Management** | ログイントークンではなくプロジェクト API Key |
| モデル ID | プロジェクト API Key で `GET /v1/models` を実行 | 実際に返された `data[].id` |

### 2.2 現在のターミナルに環境変数を設定

#### macOS zsh

```bash
export TOKENHUB_BASE_URL="実際の TokenHub Base URL を入力"
read -r -s "TOKENHUB_API_KEY?TokenHub プロジェクト API Key: "
export TOKENHUB_API_KEY
echo
```

コマンドは上から順に実行します。`read` のプロンプトが表示されたら Key を貼り付け、Enter キーを押してください。`-s` により入力内容が非表示になるため、文字やアスタリスクは表示されません。`export` は値を Codex から参照可能にし、`echo` は入力後のプロンプトを改行します。

#### Bash

```bash
export TOKENHUB_BASE_URL="実際の TokenHub Base URL を入力"
read -r -s -p "TokenHub プロジェクト API Key: " TOKENHUB_API_KEY
export TOKENHUB_API_KEY
echo
```

#### Windows PowerShell

```powershell
$env:TOKENHUB_BASE_URL = Read-Host "TokenHub Base URL（/v1 で終わる URL）"
$tokenHubSecureKey = Read-Host "TokenHub プロジェクト API Key" -AsSecureString
$env:TOKENHUB_API_KEY = [System.Net.NetworkCredential]::new("", $tokenHubSecureKey).Password
Remove-Variable tokenHubSecureKey
```

これらの環境変数は、現在のターミナルセッションでのみ有効です。

### 2.3 利用可能なモデルを確認

#### macOS、Linux、Git Bash

```bash
curl --fail-with-body \
  --url "${TOKENHUB_BASE_URL%/}/models" \
  --header "Authorization: Bearer ${TOKENHUB_API_KEY}"
```

#### Windows PowerShell

```powershell
$tokenHubModels = Invoke-RestMethod `
  -Uri "$($env:TOKENHUB_BASE_URL.TrimEnd('/'))/models" `
  -Headers @{ Authorization = "Bearer $env:TOKENHUB_API_KEY" }

$tokenHubModels.data | Select-Object id
```

返された実際のモデル ID を環境変数に保存します。

```bash
read -r "TOKENHUB_MODEL_ID?上記レスポンスに含まれる実際のモデル ID: "
export TOKENHUB_MODEL_ID
```

PowerShell：

```powershell
$env:TOKENHUB_MODEL_ID = Read-Host "上記レスポンスに含まれる実際のモデル ID"
```

### 2.4 Responses ストリーミングを検証

モデル一覧に表示されるだけでは、ルートが正常とは判断できません。Codex では Responses API のストリーミング対応が必要です。

```bash
curl --fail-with-body --no-buffer \
  --request POST \
  --url "${TOKENHUB_BASE_URL%/}/responses" \
  --header "Authorization: Bearer ${TOKENHUB_API_KEY}" \
  --header "Content-Type: application/json" \
  --data "$(printf '{"model":"%s","input":"Reply only: Connection successful","stream":true}' "$TOKENHUB_MODEL_ID")"
```

PowerShell：

```powershell
$tokenHubRequestBody = @{
  model = $env:TOKENHUB_MODEL_ID
  input = "Reply only: Connection successful"
  stream = $true
} | ConvertTo-Json -Compress

Invoke-WebRequest `
  -Method Post `
  -Uri "$($env:TOKENHUB_BASE_URL.TrimEnd('/'))/responses" `
  -Headers @{ Authorization = "Bearer $env:TOKENHUB_API_KEY" } `
  -ContentType "application/json" `
  -Body $tokenHubRequestBody
```

TokenHub が `provider_capability_not_supported` を返す場合、管理者がモデルルートまたはプロバイダーリソース種別を修正する必要があります。

DeepSeek 公式 Provider では、Responses と Codex の能力はモデル単位で、`deepseek-v4-flash` と `deepseek-v4-pro` の両方で利用できます。両モデルはサーバー側の `web_search`、Codex の `apply_patch` カスタムツール、0～20 の `top_logprobs` をサポートしますが、画像やファイルの入力には対応していません。DeepSeek の Responses API はステートレスであるため、クライアントは `previous_response_id` や `conversation` に依存せず、各ターンの `input` に会話履歴全体を渡す必要があります。DeepSeek のコンテキストキャッシュは自動管理です。`TOKENHUB_CACHE_AFFINITY_ENABLED=true` を有効にすると、TokenHub は `session-id`、`client_metadata.session_id`、`prompt_cache_key` などの安定した Codex セッションヒントを使い、連続する Responses リクエストを同じ上流アカウントへ固定します。このキーはゲートウェイルーティング専用であり、TokenHub 内に別のレスポンスキャッシュを作成するものではありません。

---

## 3. 方法 1：分離 Profile

### 3.1 既存 Profile のバックアップ

Profile のパス：

- macOS / Linux：`~/.codex/tokenhub.config.toml`
- Windows：`%USERPROFILE%\.codex\tokenhub.config.toml`

```bash
if [ -f "$HOME/.codex/tokenhub.config.toml" ]; then
  cp -p "$HOME/.codex/tokenhub.config.toml" \
    "$HOME/.codex/tokenhub.config.toml.before-edit.$(date +%Y%m%d-%H%M%S)"
fi
```

PowerShell：

```powershell
if (Test-Path "$env:USERPROFILE\.codex\tokenhub.config.toml") {
  $tokenHubBackupTime = Get-Date -Format "yyyyMMdd-HHmmss"
  Copy-Item `
    "$env:USERPROFILE\.codex\tokenhub.config.toml" `
    "$env:USERPROFILE\.codex\tokenhub.config.toml.before-edit.$tokenHubBackupTime"
}
```

### 3.2 Profile の作成

```toml
model_provider = "tokenhub"
model = "GET /v1/models で返された実際のモデル ID"

[model_providers.tokenhub]
name = "TokenHub"
base_url = "実際の TokenHub Base URL"
env_key = "TOKENHUB_API_KEY"
env_key_instructions = "Codex を起動する前に TOKENHUB_API_KEY を設定してください"
wire_api = "responses"
```

`base_url` には `/v1` が必要です。プロバイダーテーブルや最上位キーを重複して記述しないでください。Codex 0.134.0 以降では、Profile ごとに独立した `<profile>.config.toml` ファイルを使用します。

管理された個人開発端末で Key を設定ファイルに直接保存する場合は、`env_key` と `env_key_instructions` を削除し、次のように記述できます。

```toml
[model_providers.tokenhub]
name = "TokenHub"
base_url = "実際の TokenHub Base URL"
experimental_bearer_token = "TokenHub プロジェクト API Key を入力"
wire_api = "responses"
```

`experimental_bearer_token` を `env_key`、プロバイダーの `auth`、`requires_openai_auth` と併用しないでください。ファイルの権限は `600` に設定し、コミット、アップロード、共有、スクリーンショットへの掲載は禁止してください。

![Base URL をマスキングした実際の TokenHub Profile 設定](../assets/codex-profile/tokenhub-profile-config-redacted.png)

*図 1：環境変数を使用する実際の Profile 設定。Base URL はマスキング済みです。*

### 3.3 起動と確認

```bash
codex --profile tokenhub
codex --profile tokenhub --cd "/実際のプロジェクトの絶対パス"
codex exec --profile tokenhub --cd "/実際のプロジェクトの絶対パス" "実際のタスク内容"
```

`/status` を実行し、モデル、TokenHub プロバイダー、および対応する TokenHub の成功ログを確認します。

![TokenHub Profile を使用した Codex の実際のステータス画面（機密情報をマスキング済み）](../assets/codex-profile/codex-status-redacted.png)

*図 2：Profile を有効にした `/status`。プロバイダー詳細、ウィンドウタイトル、セッション ID はマスキング済みです。*

### 3.4 復旧

既定設定を使用する場合：

```bash
codex
```

Profile を無効化する場合：

```bash
mv "$HOME/.codex/tokenhub.config.toml" \
  "$HOME/.codex/tokenhub.config.toml.disabled"
```

既存 Profile があった場合は、タイムスタンプ付きバックアップから復元してください。

---

## 4. 方法 2：プロセス単位の一時設定

この方法ではファイルを変更せず、現在の Codex プロセスにのみ設定を適用します。

```bash
codex \
  -c 'model_provider="tokenhub"' \
  -c "model=\"${TOKENHUB_MODEL_ID}\"" \
  -c 'model_providers.tokenhub.name="TokenHub"' \
  -c "model_providers.tokenhub.base_url=\"${TOKENHUB_BASE_URL}\"" \
  -c 'model_providers.tokenhub.env_key="TOKENHUB_API_KEY"' \
  -c 'model_providers.tokenhub.env_key_instructions="Codex を起動する前に TOKENHUB_API_KEY を設定してください"' \
  -c 'model_providers.tokenhub.wire_api="responses"'
```

PowerShell：

```powershell
codex `
  -c 'model_provider="tokenhub"' `
  -c "model=`"$env:TOKENHUB_MODEL_ID`"" `
  -c 'model_providers.tokenhub.name="TokenHub"' `
  -c "model_providers.tokenhub.base_url=`"$env:TOKENHUB_BASE_URL`"" `
  -c 'model_providers.tokenhub.env_key="TOKENHUB_API_KEY"' `
  -c 'model_providers.tokenhub.env_key_instructions="Codex を起動する前に TOKENHUB_API_KEY を設定してください"' `
  -c 'model_providers.tokenhub.wire_api="responses"'
```

`/status` で確認します。Codex を終了すると一時設定は破棄されます。使用後に環境変数を削除する場合：

```bash
unset TOKENHUB_BASE_URL TOKENHUB_API_KEY TOKENHUB_MODEL_ID
```

---

## 5. 方法 3：CLI グローバル設定

ユーザー設定ファイルのパス：

- macOS / Linux：`~/.codex/config.toml`
- Windows：`%USERPROFILE%\.codex\config.toml`

設定ファイルをバックアップします。

```bash
if [ -f "$HOME/.codex/config.toml" ]; then
  cp -p "$HOME/.codex/config.toml" \
    "$HOME/.codex/config.toml.before-tokenhub.$(date +%Y%m%d-%H%M%S)"
fi
```

3.2 節のプロバイダー設定を `config.toml` に統合します。既存キーを重複追加せず、値を更新してください。`experimental_bearer_token` は、前述の制約を満たす場合に限り使用できます。

検証：

```bash
codex doctor --summary
codex
```

`/status` を実行し、TokenHub のログで対応するリクエストを確認します。

復旧するには、バックアップを復元するか、`model_provider` と `model` を以前の値に戻して TokenHub のプロバイダーテーブルを削除します。その後 Codex を完全に再起動してください。

---

## 6. 方法 4：Codex デスクトップ設定

デスクトップアプリ、CLI、IDE 拡張機能は `~/.codex/config.toml` を共有します。

次の順に開きます。

**Settings → Configuration → Open config.toml**

ファイルをバックアップし、3.2 節のプロバイダー設定を統合します。

ターミナル以外から起動したデスクトップアプリは、通常、ターミナルの環境変数を引き継ぎません。Key を `~/.codex/.env` に追加します。

```dotenv
TOKENHUB_API_KEY=実際の TokenHub プロジェクト API Key を入力
```

```bash
chmod 600 "$HOME/.codex/.env"
```

既存の `.env` の設定を上書きしないでください。また、このファイルをソース管理またはスクリーンショットに含めないでください。

Codex を完全に再起動し、ローカルタスクを作成してモデルを確認した後、TokenHub のログでリクエストを確認します。ローカルの `config.toml` は Codex クラウドタスクの既定モデルを制御しません。

復旧するには、`config.toml` を復元し、`.env` に追加した `TOKENHUB_API_KEY` の行だけを削除してから再起動します。

---

## 7. トラブルシューティング

| 症状 | 主な原因 | 対処 |
| --- | --- | --- |
| `TOKENHUB_API_KEY` が見つからない | プロセスに環境変数が渡されていない | 変数を確認し、`.env` 更新後はデスクトップアプリを再起動 |
| HTTP 401 / `invalid_api_key` | Key が未設定、不正、または認識されていない | コンソールのログイントークンではなく、TokenHub プロジェクト API Key を使用 |
| HTTP 403 | Key が無効または期限切れ、あるいはモデルが許可されていない | プロジェクト、Key、モデル許可リスト、ポリシーを確認 |
| HTTP 404 | Base URL またはモデル ID が不正 | `/v1` を確認し、`GET /v1/models` を再実行 |
| HTTP 429 / `quota_exceeded` | リクエスト数、トークン数、コスト、同時実行数、プロバイダーの制限 | 制限の回復を待つか、ポリシーを調整 |
| HTTP 503 / `provider_unavailable` | 正常なルートが存在しない | ルート、プロバイダー、アカウントリソースの状態を確認 |
| HTTP 501 / `provider_capability_not_supported` | ルートが Responses または Responses ストリーミングに非対応 | モデルルートまたはプロバイダーリソースを変更 |

Key の値を表示せず、設定済みかどうかだけを確認するには次を実行します。

```bash
test -n "${TOKENHUB_API_KEY:-}" && echo "TOKENHUB_API_KEY is set"
```

設定の優先順位：

1. CLI 引数と `--config`
2. 信頼済みプロジェクトの `.codex/config.toml`
3. 選択した Profile ファイル
4. ユーザーの `~/.codex/config.toml`
5. システム設定
6. Codex の既定値

## 8. 参考資料

- [Codex Config basics](https://learn.chatgpt.com/docs/config-file/config-basic)
- [Codex Advanced Configuration](https://learn.chatgpt.com/docs/config-file/config-advanced)
- [Codex Environment variables](https://learn.chatgpt.com/docs/config-file/environment-variables)
- [Codex Configuration Reference](https://learn.chatgpt.com/docs/config-file/config-reference)
