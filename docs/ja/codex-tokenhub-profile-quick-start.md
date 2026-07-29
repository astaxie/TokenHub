# Codex を TokenHub に接続する：Profile クイック設定

Language: [English](../codex-tokenhub-profile-quick-start.md) | [简体中文](../zh-CN/codex-tokenhub-profile-quick-start.md) | 日本語

> 独立した `tokenhub` Profile だけを使って Codex を TokenHub に接続するための簡易ガイドです。Profile の作成、API Key の設定、検証、既定環境への復旧を説明します。
>
> Profile、プロセス限定設定、CLI グローバル設定、デスクトップ設定を比較する場合は、[4 つの設定方法と復旧ガイド](codex-tokenhub-configuration.md)を参照してください。

ユーザーが設定を始める前に、管理者は TokenHub で OpenAI Codex Provider、サブスクリプションアカウントリソース、モデルルートを設定し、対象プロジェクトの API Key を作成する必要があります。

## 1. Profile の動作

`tokenhub` Profile は `--profile tokenhub` を指定したセッションだけで読み込まれます。

| コマンド | 主な設定 | リクエスト経路 |
| --- | --- | --- |
| `codex` | `~/.codex/config.toml` | 既定の Codex Provider |
| `codex --profile tokenhub` | `~/.codex/tokenhub.config.toml` | TokenHub |

既定の `config.toml` は上書きされません。スクリーンショットには実際の設定・結果だけを使用し、API Key、Token、アカウント、Session ID、非公開アドレス、ユーザー名、パス、Project ID、Request ID を完全にマスクしてください。

---

## 2. 設定済み Profile を使用する

TokenHub、Project API Key、モデルルートが有効であることを確認します。

`env_key` を使用する場合：

```bash
read -r -s "TOKENHUB_API_KEY?TokenHub Project API Key: "
export TOKENHUB_API_KEY
echo

codex --profile tokenhub
```

`experimental_bearer_token` を使用する場合、`read` と `export` は不要です。起動後に `/status` を実行し、`Model provider` と `Model` を確認します。

---

## 3. Profile を作成する

### 3.1 Codex CLI を確認する

```bash
codex --version
```

コマンドがない場合は Codex CLI のインストールとサインインを先に完了します。

### 3.2 TokenHub を確認する

```bash
curl --fail-with-body http://127.0.0.1:8080/healthz
```

期待される応答：

```json
{"service":"tokenhub-backend","status":"ok"}
```

サービスが停止している場合：

```bash
cd "/TokenHubリポジトリの絶対パスを入力"
./start.sh
```

### 3.3 既存 Profile をバックアップする

```bash
mkdir -p "$HOME/.codex"

if [ -f "$HOME/.codex/tokenhub.config.toml" ]; then
  cp -p "$HOME/.codex/tokenhub.config.toml" \
    "$HOME/.codex/tokenhub.config.toml.before-edit.$(date +%Y%m%d-%H%M%S)"
fi
```

### 3.4 Profile を設定する

```bash
nano "$HOME/.codex/tokenhub.config.toml"
```

推奨する環境変数方式：

```toml
model_provider = "tokenhub"
model = "gpt-5.6-luna"

[model_providers.tokenhub]
name = "TokenHub Local"
base_url = "http://127.0.0.1:8080/v1"
env_key = "TOKENHUB_API_KEY"
env_key_instructions = "Codexを起動する前にTOKENHUB_API_KEYを設定してください"
wire_api = "responses"
```

`base_url` には `/v1` が必要です。同じキーや Provider テーブルを重複定義しないでください。Codex 0.134.0 以降は独立した `<profile>.config.toml` を使用します。

個人の管理された開発環境では、`env_key` と `env_key_instructions` を削除して Key を直接保存できます。

```toml
[model_providers.tokenhub]
name = "TokenHub Local"
base_url = "http://127.0.0.1:8080/v1"
experimental_bearer_token = "自分のTokenHub Project API Keyをここに貼り付ける"
wire_api = "responses"
```

`experimental_bearer_token` を `env_key`、Provider の `auth`、`requires_openai_auth` と併用しないでください。Key は平文で保存されるため、開発用途に限定します。

```bash
chmod 600 "$HOME/.codex/tokenhub.config.toml"
```

Key を含む Profile は Git に追加、アップロード、共有、スクリーンショット掲載してはいけません。

![Base URL をマスクした実際の TokenHub Profile 設定](../assets/codex-profile/tokenhub-profile-config-redacted.png)

*図 1：環境変数方式の実際の設定。Base URL はマスク済みで、API Key は保存されていません。*

### 3.5 任意：TOML を検証する

Profile が起動し、4.3 の接続テストに成功する場合は省略できます。

```bash
python3 - <<'PY'
from pathlib import Path
import tomllib

path = Path.home() / ".codex" / "tokenhub.config.toml"
with path.open("rb") as file:
    config = tomllib.load(file)

print("Profile configuration loaded")
print("Model:", config["model"])
print("Provider:", config["model_provider"])
print("Base URL:", config["model_providers"]["tokenhub"]["base_url"])
PY
```

---

## 4. 日常利用と検証

### 4.1 Project API Key を入力する

`env_key = "TOKENHUB_API_KEY"` を使用する場合のみ実行します。

1. `read` コマンドを実行します。
2. プロンプトが表示されたら実際の Key を貼り付け、Enter を押します。
3. `-s` により文字やアスタリスクは表示されません。
4. `export TOKENHUB_API_KEY` と `echo` を実行します。

```bash
read -r -s "TOKENHUB_API_KEY?TokenHub Project API Key: "
export TOKENHUB_API_KEY
echo
```

`read` は 1 行を読み取り、`-r` はバックスラッシュを保持し、`-s` は入力表示を無効にします。`export` により Codex から変数を参照できます。この変数は現在のターミナルだけで有効です。

### 4.2 Codex を起動する

```bash
codex --profile tokenhub
```

```bash
codex --profile tokenhub \
  --cd "/プロジェクトの絶対パスを入力"
```

### 4.3 接続テストを実行する

```bash
codex exec \
  --profile tokenhub \
  --ephemeral \
  --sandbox read-only \
  "ツールを使用せず、「接続成功」とだけ回答してください"
```

この環境での実際の結果：

```text
OpenAI Codex v0.145.0
model: gpt-5.6-luna
provider: tokenhub
接続成功
```

モデル、`provider: tokenhub`、最終応答、TokenHub の HTTP 200 ログを確認します。

### 4.4 状態を確認する

Codex で `/status` を実行します。

![TokenHub Profile 経由の実際の Codex 状態](../assets/codex-profile/codex-status-redacted.png)

*図 2：実際の `/status`。Provider 詳細、ウィンドウタイトル、Session ID はマスク済みです。*

---

## 5. リクエスト経路と依存条件

```text
現在のターミナル
  → tokenhub Profile
  → http://127.0.0.1:8080/v1
  → TokenHub Project 認証とモデルルーティング
  → 接続済み OpenAI Codex アカウントリソース
  → モデル応答
```

実際の呼び出しには、正常な Provider とアカウントリソース、有効なモデルルートと Project API Key、ストリーミング Responses API 対応が必要です。この環境では `gpt-5.6-luna` の実リクエストで HTTP 200 を確認済みです。

---

## 6. 復旧と無効化

既定設定で起動：

```bash
codex
```

現在のターミナルから Key を削除：

```bash
unset TOKENHUB_API_KEY
```

Profile を無効化：

```bash
mv "$HOME/.codex/tokenhub.config.toml" \
  "$HOME/.codex/tokenhub.config.toml.disabled"
```

再有効化：

```bash
mv "$HOME/.codex/tokenhub.config.toml.disabled" \
  "$HOME/.codex/tokenhub.config.toml"
```

---

## 7. トラブルシューティング

| 現象 | 主な原因 | 対応 |
| --- | --- | --- |
| `TOKENHUB_API_KEY` がない | 現在のターミナルで Key を設定していない | 4.1 を再実行 |
| HTTP 401 | Key が無効、期限切れ、または別 Project の Key | Key を再取得またはローテーション |
| HTTP 503 / `provider_unavailable` | 正常なルートがない | Provider、アカウントリソース、ルートを確認 |
| Profile が見つからない | ファイルがない、または名前が違う | `~/.codex/tokenhub.config.toml` を確認 |
| 古い Provider が使われる | プロセスが設定を再読込していない | Codex を完全終了して再起動 |

---

## 8. セキュリティ

- 環境変数方式を推奨します。
- `experimental_bearer_token` は管理された個人開発環境だけで使用します。
- Key を保存する Profile の権限を `600` にします。
- `.env`、API Key、OAuth Token、認証情報、Key を含む Profile をコミットしません。
- チャット、スクリーンショット、履歴に露出した Key は直ちにローテーションします。

## 9. 関連ドキュメント

- [Codex を TokenHub に接続する：4 つの設定方法と復旧](codex-tokenhub-configuration.md)
- [モデル API 利用者ガイド](user-guide.md)
